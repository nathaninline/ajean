package nodeclient

import (
	"context"
	"strings"
	"testing"
)

// Un chemin ENTRE GUILLEMETS doit fonctionner (régression : Go ré-échappait les
// guillemets et cmd renvoyait « syntaxe du nom de fichier incorrecte »).
func TestRunShellQuotedPath(t *testing.T) {
	out := runShell(context.Background(), `dir "C:\Windows" /b`, 10, "")
	if !strings.Contains(out, "exit: 0") {
		t.Fatalf("commande avec chemin entre guillemets a échoué:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "system32") {
		t.Fatalf("sortie inattendue (System32 absent):\n%s", out)
	}
	// La sortie doit être de l'UTF-8 propre : pas de caractère de remplacement
	// U+FFFD (le « � » qui apparaissait avec la codepage OEM).
	if strings.ContainsRune(out, '\uFFFD') {
		t.Fatalf("la sortie contient des caractères mal encodés (�):\n%s", out)
	}
}

// L'accent de « Répertoire » doit être correct (validation de chcp 65001).
func TestRunShellUTF8Accents(t *testing.T) {
	out := runShell(context.Background(), `dir C:\Windows`, 10, "")
	if strings.ContainsRune(out, '\uFFFD') {
		t.Fatalf("caractères mal encodés dans la sortie:\n%s", out)
	}
}
