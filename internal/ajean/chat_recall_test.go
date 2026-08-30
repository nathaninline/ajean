package ajean

import (
	"strings"
	"testing"
)

// Archive puis rappel : le bloc doit revenir VERBATIM, à l'octet près.
func TestRecallRoundtrip(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	content := "package main\nfunc main(){ println(\"jeu\") }\n" + strings.Repeat("x", 900)
	id, err := archiveRecallBlock("write: package main", "tool", content)
	if err != nil {
		t.Fatalf("archive: %v", err)
	}
	if !strings.HasPrefix(id, "r") {
		t.Fatalf("id inattendu: %q", id)
	}
	blk, ok := recallGet(id)
	if !ok {
		t.Fatalf("recallGet(%q) introuvable", id)
	}
	if blk.Content != content {
		t.Fatal("le contenu rappelé diffère de l'original")
	}
}

// Les ids sont monotones et jamais réutilisés.
func TestRecallIDsMonotonic(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	id1, _ := archiveRecallBlock("a", "tool", strings.Repeat("a", 900))
	id2, _ := archiveRecallBlock("b", "tool", strings.Repeat("b", 900))
	if id1 == id2 {
		t.Fatalf("deux blocs ont le même id: %q", id1)
	}
	s1, _ := recallIDToSeq(id1)
	s2, _ := recallIDToSeq(id2)
	if s2 <= s1 {
		t.Fatalf("séquence non croissante: %d puis %d", s1, s2)
	}
}

// La recherche lexicale retrouve un bloc par un mot-clé de son contenu, même
// sans connaître son id.
func TestRecallSearchFindsByKeyword(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	_, _ = archiveRecallBlock("web_read: doc", "tool", "page sur les trains "+strings.Repeat("z", 900))
	wanted, _ := archiveRecallBlock("write: jeu", "tool", "code du jeu marqueur_xyzzy "+strings.Repeat("q", 900))
	hits := recallSearch("xyzzy", 5)
	if len(hits) == 0 {
		t.Fatal("recall_search ne trouve pas le bloc par mot-clé")
	}
	if hits[0].ID != wanted {
		t.Fatalf("mauvais bloc en tête: %q (attendu %q)", hits[0].ID, wanted)
	}
}

// Intégration : pendant une compaction en mode agent, un gros bloc du torse est
// archivé et récupérable, et l'historique compacté le référence par recall(id)
// au lieu de l'effacer. (summarizeTranscript échoue faute de llama-server → on
// tombe sur le repli dégraissé, qui porte justement les marqueurs recall.)
func TestCompactArchivesBigBlock(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	bigCode := "func jeu(){\n" + strings.Repeat("  ligne_de_code_unique_wibble\n", 60) + "}"
	msgs := []Message{
		um("fais-moi un jeu en go"),
		atc("write"), tm(bigCode),
	}
	// Assez de tours récents pour que la queue les couvre EUX sans remonter jusqu'au
	// gros bloc (sinon il tomberait dans la queue au lieu du torse).
	for i := 0; i < 16; i++ {
		msgs = append(msgs, um("ajuste un détail"), am("c'est fait"))
	}
	out, changed := compactMessages(t.Context(), msgs, Caps{Agent: true})
	if !changed {
		t.Fatal("la compaction n'a rien changé alors qu'il y a un gros bloc à archiver")
	}
	// Le bloc doit être retrouvable par un mot-clé de son contenu.
	hits := recallSearch("wibble", 5)
	if len(hits) == 0 {
		t.Fatal("le gros bloc n'a pas été archivé (recall_search vide)")
	}
	if !strings.Contains(hits[0].Content, "ligne_de_code_unique_wibble") {
		t.Fatal("le bloc archivé ne contient pas le code original")
	}
	// L'historique compacté doit référencer un id recall, pas juste effacer.
	joined := ""
	for _, m := range out {
		joined += msgText(m) + "\n"
	}
	if !strings.Contains(joined, "recall(") && !strings.Contains(joined, "recall:") {
		t.Fatal("l'historique compacté ne référence aucun id recall")
	}
	// Le gros code brut ne doit plus être présent verbatim dans le contexte.
	if strings.Contains(joined, bigCode) {
		t.Fatal("le gros bloc est resté verbatim dans le contexte (pas de réduction)")
	}
}

// Sans mode agent, on n'archive pas (le modèle n'a pas l'outil recall) : aucun
// bloc ne doit être créé, le résumé reste propre.
func TestCompactNoArchiveWithoutAgent(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	big := strings.Repeat("contenu volumineux ", 100)
	msgs := []Message{um("q"), atc("web_read"), tm(big)}
	for i := 0; i < 8; i++ {
		msgs = append(msgs, um("suite"), am("ok"))
	}
	_, _ = compactMessages(t.Context(), msgs, Caps{}) // agent off
	if hits := recallSearch("volumineux", 5); len(hits) != 0 {
		t.Fatalf("archivage effectué sans mode agent (%d blocs)", len(hits))
	}
}
