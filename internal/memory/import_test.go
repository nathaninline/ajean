package memory

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// legacyHome écrit une mémoire telle que l'ancien AJEAN la rangeait.
func legacyHome(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "memory", "_jean")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

var legacySample = map[string]string{
	"profile.md":      "# Profil Jean\n\n- prenom: Camille\n- ville: Lyon\n",
	"lessons.md":      "# Leçons de Jean\n\n- [lecon-2] Toujours donner l'heure en 24 h.\n- [lecon-5] Vérifier un lien avant de le citer.\n",
	"lessons-seq.txt": "9",
	"rules.json": `[{"n":1,"kind":"replace","from":"—","to":",","why":"pas de tiret long"},
		{"n":2,"kind":"no_numbered_lists"},{"n":3,"kind":"max_list_items","max":5}]`,
	"fiche-meteo.md":              "# meteo\nwhen: Donner la météo d'une ville\n\nPasser par le site officiel.\n\n## Pièges\n- La page mobile ment.\n",
	"archive-fiche-vieux-truc.md": "# vieux-truc\nwhen: Plus utilisé\n\nAncien contenu.\n",
	"journal-2026-10.md": "# Journal 2026-10\n" +
		"\n## 2026-10-03 19:16 +0200 · 1a102c42691 · echange\nUtilisateur : Bonjour\nJean : Bonjour Camille.\n" +
		"\n## 2026-10-03 19:17 · 1a102c4c1fb\nUne entrée des premières versions, sans décalage ni genre.\n" +
		"\n## 2026-10-04 08:00 +0200 · 1a102d00000 · correctif\nCorrection notée.\n",
	"usage-fiches.json": `{"meteo":{"n":3,"last":1790000000000,"corrected":1}}`,
	"recalls.md":        `{"1a102c42691":{"n":1,"days":["2026-10-03"]}}`,
	"embed-2026-10.txt": "jean-embed x\n",
}

func TestImportLegacy(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	dir := legacyHome(t, legacySample)
	if got := LegacyDir(filepath.Dir(filepath.Dir(dir))); got != dir {
		t.Fatalf("LegacyDir = %q, attendu %q", got, dir)
	}
	// Ce qui existe déjà ici reste tel quel.
	if err := UpdateFact("", "ville", "Paris"); err != nil {
		t.Fatal(err)
	}

	rep, err := ImportLegacy(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Facts != 1 || rep.Lessons != 2 || rep.Rules != 1 || rep.RulesSkipped != 2 || rep.Fiches != 2 || rep.Entries != 3 {
		t.Errorf("rapport inattendu : %+v", rep)
	}

	facts, _ := Profile()
	got := map[string]string{}
	for _, f := range facts {
		got[f.Key] = f.Value
	}
	if got["prenom"] != "Camille" || got["ville"] != "Paris" {
		t.Errorf("profil = %v", got)
	}

	ls := Lessons()
	if len(ls) != 2 || ls[0].N != 2 || ls[1].N != 5 {
		t.Errorf("leçons = %+v", ls)
	}
	// Le compteur de l'ancien AJEAN est repris : un numéro déjà donné ne ressert pas.
	if msg, err := addLesson("Une leçon neuve et sans rapport avec les autres.", ""); err != nil || !strings.Contains(msg, "lecon-9") {
		t.Errorf("leçon suivante : %q, %v (attendu lecon-9)", msg, err)
	}

	rs := Rules()
	if len(rs) != 1 || rs[0].From != "—" || rs[0].To != "," {
		t.Errorf("règles = %+v", rs)
	}

	f, err := GetFiche("meteo")
	if err != nil || f.When != "Donner la météo d'une ville" || !strings.Contains(f.Content, "## Pièges") || f.Archived {
		t.Errorf("fiche = %+v, %v", f, err)
	}
	if _, err := os.Stat(fichePath("vieux-truc", true)); err != nil {
		t.Errorf("fiche archivée absente : %v", err)
	}

	entries, total := Journal("", 50)
	var texts, kinds []string
	for _, e := range entries {
		texts, kinds = append(texts, e.Text), append(kinds, e.Kind)
	}
	all := strings.Join(texts, "\n")
	if !strings.Contains(all, "AJEAN : Bonjour Camille.") || strings.Contains(all, "\nJean : ") {
		t.Errorf("l'échange doit porter l'étiquette d'ici : %q", all)
	}
	if !strings.Contains(all, "sans décalage ni genre") || !strings.Contains(all, "Correction notée.") {
		t.Errorf("entrées anciennes perdues : %q", all)
	}
	for _, k := range kinds {
		if k == "" {
			t.Errorf("entrée sans genre : %v", kinds)
		}
	}

	// Relancer la reprise n'ajoute rien en double.
	again, err := ImportLegacy(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Facts+again.Lessons+again.Rules+again.Fiches+again.Entries != 0 {
		t.Errorf("deuxième reprise : %+v, attendu rien", again)
	}
	if _, after := Journal("", 50); after != total {
		t.Errorf("journal : %d entrées après la deuxième reprise, %d avant", after, total)
	}
}

func TestImportLegacyRefusesEncryptedMemory(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	dir := legacyHome(t, map[string]string{"profile.md": legacyMagic + "\x00\x01\x02"})
	if _, err := ImportLegacy(dir); !errors.Is(err, ErrLegacyEncrypted) {
		t.Errorf("mémoire chiffrée : %v, attendu ErrLegacyEncrypted", err)
	}
	if facts, _ := Profile(); len(facts) != 0 {
		t.Error("rien ne doit être écrit depuis une mémoire chiffrée")
	}
}
