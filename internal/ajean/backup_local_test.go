package ajean

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const testBackupPass = "phrase de sauvegarde d'essai"

func localBackupTestHome(t *testing.T) string {
	t.Helper()
	home := testHome(t)
	old := localBackupKDF
	localBackupKDF = kekParams{T: 1, M: 64, P: 1}
	t.Cleanup(func() { localBackupKDF = old })
	return home
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Aller-retour complet : base, mémoire, presets, scripts reviennent à
// l'identique ; modèles et dossier de travail ne partent pas ; rien n'est lisible
// dans l'archive.
func TestLocalBackupRoundTrip(t *testing.T) {
	home := localBackupTestHome(t)
	if err := SetConfigKey("CTX", "12345"); err != nil {
		t.Fatal(err)
	}
	if err := saveTask(Task{ID: "t1", Name: "Tâche gardée", Prompt: "p", Schedule: "@every 2h"}); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("ligne de mémoire très reconnaissable\n", 80000) // > 2 blocs d'1 Mio
	mustWrite(t, filepath.Join(home, "memory", "general", "notes.md"), big)
	mustWrite(t, filepath.Join(home, "presets", "p.env"), "# NAME=P\nCTX=4096\n")
	mustWrite(t, filepath.Join(home, "scripts", "sous", "s.sh"), "#!/bin/sh\necho ok\n")
	mustWrite(t, filepath.Join(home, ".keyvault"), `{"v":1}`)
	mustWrite(t, filepath.Join(home, "models", "gros.gguf"), "poids du modèle")
	mustWrite(t, filepath.Join(home, "workspace", "brouillon.txt"), "jetable")
	mustWrite(t, filepath.Join(home, "memory", "general", ".tmp-notes.md-1"), "écriture en cours")

	out := t.TempDir()
	if _, err := CreateLocalBackup(out, "courte"); err == nil {
		t.Fatal("une phrase trop courte devait être refusée")
	}
	if _, err := CreateLocalBackup(filepath.Join(home, "sauvegardes"), testBackupPass); err == nil {
		t.Fatal("sauvegarder dans le dossier de données devait être refusé")
	}
	path, err := CreateLocalBackup(out, testBackupPass)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("très reconnaissable")) || bytes.Contains(raw, []byte("Tâche gardée")) || bytes.Contains(raw, []byte("notes.md")) {
		t.Fatal("contenu ou nom de fichier lisible dans l'archive")
	}
	if left, _ := filepath.Glob(filepath.Join(out, "*.partial")); len(left) != 0 {
		t.Fatalf("fichier provisoire resté : %v", left)
	}

	dest := filepath.Join(t.TempDir(), "restaure")
	n, err := RestoreLocalBackup(path, dest, testBackupPass)
	if err != nil {
		t.Fatal(err)
	}
	if n != 5 { // ajean.db, .keyvault, notes.md, p.env, s.sh
		t.Fatalf("%d fichiers restaurés, 5 attendus", n)
	}
	for rel, want := range map[string]string{
		"memory/general/notes.md": big,
		"presets/p.env":           "# NAME=P\nCTX=4096\n",
		"scripts/sous/s.sh":       "#!/bin/sh\necho ok\n",
		".keyvault":               `{"v":1}`,
	} {
		got, err := os.ReadFile(filepath.Join(dest, filepath.FromSlash(rel)))
		if err != nil || string(got) != want {
			t.Fatalf("%s : non restauré à l'identique (%v)", rel, err)
		}
	}
	for _, rel := range []string{"models", "workspace", "memory/general/.tmp-notes.md-1"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(rel))); err == nil {
			t.Fatalf("%s ne devait pas partir dans la sauvegarde", rel)
		}
	}
	// La base restaurée est une vraie base : réglages et tâches s'y relisent.
	t.Setenv("AJEAN_HOME", dest)
	if got := allKV(bkConfig)["CTX"]; got != "12345" {
		t.Fatalf("configuration de la base restaurée : %q", got)
	}
	if tk, ok := getTask("t1"); !ok || tk.Name != "Tâche gardée" {
		t.Fatalf("tâche de la base restaurée : %+v", tk)
	}
	// Une restauration n'écrit jamais par-dessus des données.
	if _, err := RestoreLocalBackup(path, dest, testBackupPass); err == nil {
		t.Fatal("restaurer dans un dossier non vide devait être refusé")
	}
}

// Phrase fausse, archive tronquée, altérée ou étrangère : refus net, et rien
// d'écrit dans le dossier de destination.
func TestLocalBackupRejectsDamage(t *testing.T) {
	home := localBackupTestHome(t)
	mustWrite(t, filepath.Join(home, "memory", "general", "notes.md"), strings.Repeat("contenu\n", 300000))
	out := t.TempDir()
	path, err := CreateLocalBackup(out, testBackupPass)
	if err != nil {
		t.Fatal(err)
	}
	good, _ := os.ReadFile(path)
	try := func(name string, data []byte, pass string) {
		t.Helper()
		p := filepath.Join(out, name)
		if err := os.WriteFile(p, data, 0o600); err != nil {
			t.Fatal(err)
		}
		dest := filepath.Join(t.TempDir(), "d")
		if _, err := RestoreLocalBackup(p, dest, pass); err == nil {
			t.Fatalf("%s : la restauration devait être refusée", name)
		}
		if ents, _ := os.ReadDir(dest); len(ents) != 0 {
			t.Fatalf("%s : un refus ne doit rien laisser dans le dossier (%d entrées)", name, len(ents))
		}
	}
	try("mauvaise-phrase", good, "une autre phrase de sauvegarde")
	try("tronquee", good[:len(good)/2], testBackupPass)
	try("sans-dernier-bloc", good[:len(good)-40], testBackupPass)
	flipped := append([]byte(nil), good...)
	flipped[len(flipped)/2] ^= 0x01
	try("alteree", flipped, testBackupPass)
	try("suite-ajoutee", append(append([]byte(nil), good...), 'x'), testBackupPass)
	try("etrangere", []byte("ceci n'est pas une sauvegarde"), testBackupPass)
	// En-tête forgé réclamant une dérivation démesurée : refusé sans la lancer.
	huge := append([]byte(nil), good...)
	huge[len(localBackupMagic)+memSaltLen+4] = 0xFF
	try("kdf-demesuree", huge, testBackupPass)
}

// Une archive qui nomme un chemin hors du dossier est refusée.
func TestLocalBackupRefusesPathEscape(t *testing.T) {
	localBackupTestHome(t)
	salt, _ := randBytes(memSaltLen)
	gcm, err := localBackupCipher(testBackupPass, salt, localBackupKDF)
	if err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.WriteString(localBackupMagic)
	buf.Write(salt)
	buf.Write([]byte{0, 0, 0, byte(localBackupKDF.T), 0, 0, 0, byte(localBackupKDF.M), localBackupKDF.P})
	sw := &sealWriter{w: &buf, gcm: gcm}
	gz := gzip.NewWriter(sw)
	tw := tar.NewWriter(gz)
	body := []byte("hors du dossier")
	if err := tw.WriteHeader(&tar.Header{Name: "../evade.txt", Mode: 0o600, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	tw.Write(body)
	tw.Close()
	gz.Close()
	sw.Close()
	dir := t.TempDir()
	p := filepath.Join(dir, "piege.ajbk")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "dest")
	if _, err := RestoreLocalBackup(p, dest, testBackupPass); err == nil || !strings.Contains(err.Error(), "refusé") {
		t.Fatalf("chemin hors du dossier accepté : %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "evade.txt")); err == nil {
		t.Fatal("un fichier a été écrit hors du dossier de restauration")
	}
}

// Sauvegarde quotidienne : une par 24 h, les 7 dernières gardées, les autres
// fichiers du dossier jamais touchés.
func TestLocalBackupDailyAndPrune(t *testing.T) {
	localBackupTestHome(t)
	out := t.TempDir()
	mustWrite(t, filepath.Join(out, "photo-de-vacances.jpg"), "à moi")
	for i := 0; i < 9; i++ {
		mustWrite(t, filepath.Join(out, localBackupPrefix+"2020010"+string(rune('0'+i))+"-000000"+localBackupSuffix), "ancienne")
	}
	now := time.Now()
	localBackupTick(out, testBackupPass, now)
	list, _ := filepath.Glob(filepath.Join(out, localBackupPrefix+"*"+localBackupSuffix))
	if len(list) != localBackupKept {
		t.Fatalf("%d archives gardées, %d attendues", len(list), localBackupKept)
	}
	if _, err := os.Stat(filepath.Join(out, localBackupPrefix+"20200100-000000"+localBackupSuffix)); err == nil {
		t.Fatal("la plus ancienne devait partir")
	}
	if _, err := os.Stat(filepath.Join(out, "photo-de-vacances.jpg")); err != nil {
		t.Fatal("un fichier étranger aux sauvegardes a été supprimé")
	}
	// Moins de 24 h après : rien de plus.
	localBackupTick(out, testBackupPass, now.Add(23*time.Hour))
	again, _ := filepath.Glob(filepath.Join(out, localBackupPrefix+"*"+localBackupSuffix))
	if len(again) != len(list) || getStr(bkState, stLocalBackupLast) != now.UTC().Format(time.RFC3339) {
		t.Fatal("une seule sauvegarde par jour")
	}
	// Sans dossier ni phrase dans l'environnement, le planificateur ne fait rien.
	t.Setenv("AJEAN_BACKUP_DIR", "")
	t.Setenv("AJEAN_BACKUP_KEY", "")
	StartLocalBackupScheduler()
}
