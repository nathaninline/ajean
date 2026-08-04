//go:build windows

package ajean

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// binaryVersion pilote la mise à jour automatique du binaire installé quand on
// lance le fichier téléchargé : si elle renvoie du vide, plus aucune mise à jour
// n'a lieu, et en silence. C'est exactement ce qui arrivait avec la première
// implémentation (exécuter `ajean version`, dont la sortie est vide pour un
// binaire en sous-système GUI). D'où ces tests sur la plomberie syscall.
func TestBinaryVersionReadsResource(t *testing.T) {
	// kernel32.dll porte toujours une ressource de version.
	dll := filepath.Join(os.Getenv("SystemRoot"), "System32", "kernel32.dll")
	if _, err := os.Stat(dll); err != nil {
		t.Skip("kernel32.dll introuvable")
	}
	v := binaryVersion(dll)
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(v) {
		t.Fatalf("binaryVersion(kernel32.dll) = %q, attendu une version x.y.z", v)
	}
}

// Sur une machine vierge, le dossier bin n'existe pas encore : installSelf doit
// le créer. Sans ça, le premier lancement échouait sur « open
// C:\ProgramData\jean\bin\jean.exe: The system cannot find the path specified »
// et AJEAN ne s'installait jamais.
func TestInstallSelfCreatesBinDir(t *testing.T) {
	binDir := filepath.Join(t.TempDir(), "jean", "bin") // deux niveaux absents
	dst, err := installSelf(binDir)
	if err != nil {
		t.Fatalf("installSelf sur un dossier absent : %v", err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatalf("binaire non écrit : %v", err)
	}
}

// replaceInstalled doit écraser le binaire en place, y compris quand le fichier
// cible existe déjà, et laisser l'ancien de côté sans le confondre avec la cible.
func TestReplaceInstalled(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "jean.exe")
	if err := os.WriteFile(target, []byte("ancienne version"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceInstalled(target); err != nil {
		t.Fatalf("replaceInstalled : %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	// installSelf copie l'exécutable courant (ici le binaire de test).
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Skip("binaire de test illisible")
	}
	if len(got) != len(self) {
		t.Fatalf("cible = %d octets, attendu %d (le binaire courant)", len(got), len(self))
	}
	if _, err := os.Stat(target + ".old"); err == nil {
		t.Error(".old subsiste alors que la cible n'était pas verrouillée")
	}
}

// Un fichier sans ressource de version doit renvoyer "" : runAsInstaller s'en
// sert pour s'abstenir plutôt que de remplacer un binaire au hasard.
func TestBinaryVersionWithoutResource(t *testing.T) {
	p := filepath.Join(t.TempDir(), "vide.exe")
	if err := os.WriteFile(p, []byte("pas un exe"), 0o644); err != nil {
		t.Fatal(err)
	}
	if v := binaryVersion(p); v != "" {
		t.Fatalf("binaryVersion sur un fichier sans ressource = %q, attendu \"\"", v)
	}
}

// Reproduit le cas signale en usage : l'alias « jean.exe » est EN COURS
// d'execution au moment de la mise a jour. copyExe seul echouait, l'alias
// restait fige sur une version perimee, et tout raccourci le visant relancait
// indefiniment l'ancienne version — qui affichait « une version plus recente
// est deja installee » a chaque lancement.
func TestReplaceExeSurchargeUnFichierVerrouille(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "neuf.exe")
	dst := filepath.Join(dir, "jean.exe")
	if err := os.WriteFile(src, []byte("VERSION-NEUVE"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, []byte("version-perimee"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Verrou exclusif : imite un .exe en cours d'execution sous Windows.
	held, err := os.OpenFile(dst, os.O_RDONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()

	if err := replaceExe(src, dst); err != nil {
		t.Fatalf("remplacement impossible: %v", err)
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != "VERSION-NEUVE" {
		t.Fatalf("l'alias est reste perime: %q", b)
	}
}

// Le remplacement ne doit JAMAIS faire disparaitre la cible : si la copie
// echoue apres le renommage, l'ancien fichier revient a sa place.
func TestReplaceExeRestaureSiLaCopieEchoue(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "jean.exe")
	if err := os.WriteFile(dst, []byte("a-preserver"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Source inexistante : la copie echouera forcement.
	if err := replaceExe(filepath.Join(dir, "absent.exe"), dst); err == nil {
		t.Fatal("attendu une erreur")
	}
	b, err := os.ReadFile(dst)
	if err != nil {
		t.Fatalf("le fichier a disparu: %v", err)
	}
	if string(b) != "a-preserver" {
		t.Fatalf("contenu altere: %q", b)
	}
}
