package ajean

// chat_scripts.go — le dossier de scripts DÉDIÉ et PROTÉGÉ de l'agent.
//
// La durabilité des scripts vient d'une séparation STRUCTURELLE, pas d'un verrou
// sur les commandes : les scripts qu'on veut CONSERVER vivent dans scriptsDir() =
// AJEAN_HOME/scripts, à côté de memory, HORS du workspace jetable. Un nettoyage —
// ou une catastrophe — dans le workspace ne peut plus les toucher. Le workspace
// reste le bac à sable où l'IA peut tout casser sans risque (clones, tests,
// fichiers temporaires).
//
// L'IA a par ailleurs la main libre sur le shell (suppression/déplacement/
// renommage inclus) : aucun garde-fou « anti-rm » ne filtre les commandes. Le
// SEUL accès réservé est le dossier memory, joignable uniquement par les outils
// mem_* (voir guardToolOnly plus bas) — parce qu'il est chiffré, pas pour le
// protéger d'une suppression.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// scriptsPath résout un nom de script fourni par le modèle vers un chemin DANS
// scriptsDir, en refusant toute évasion hors du dossier (« ../ », chemin absolu).
// Renvoie une erreur plutôt qu'un chemin hors périmètre.
func scriptsPath(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("nom de script vide")
	}
	// On interdit les chemins absolus et la remontée : un script vit dans
	// scriptsDir, point. Les sous-dossiers relatifs restent permis. Un nom qui
	// commence par un séparateur (« /etc/… ») est rejeté explicitement : sous
	// Windows il n'est pas « absolu » au sens de filepath mais n'a rien à faire là.
	if strings.HasPrefix(name, "/") || strings.HasPrefix(name, `\`) {
		return "", fmt.Errorf("nom de script invalide (commence par un séparateur) : %s", name)
	}
	// Caractères hostiles au shell : le nom finit dans une ligne de commande
	// (scriptRunCommand l'entoure de guillemets) ; on refuse tout ce qui pourrait
	// en sortir. Le backslash est inclus : sous Windows c'est un séparateur (utiliser
	// « / » pour les sous-dossiers), sous Unix c'est un échappement shell. Refuser
	// partout rend le comportement identique sur les deux plateformes. Un nom de
	// fichier n'a de toute façon besoin d'aucun de ces caractères.
	if i := strings.IndexAny(name, "\"'`$;&|<>*?\\\n\r"); i >= 0 {
		return "", fmt.Errorf("nom de script invalide (caractère interdit %q) : %s", name[i:i+1], name)
	}
	clean := filepath.Clean(filepath.FromSlash(name))
	if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("nom de script invalide (pas de chemin absolu ni de ../) : %s", name)
	}
	full := filepath.Join(scriptsDir(), clean)
	// Ceinture et bretelles : vérifie que le résultat est bien SOUS scriptsDir.
	if !underDir(full, scriptsDir()) {
		return "", fmt.Errorf("nom de script hors du dossier scripts : %s", name)
	}
	return full, nil
}

// scriptExists valide qu'un nom désigne bien un fichier script existant dans le
// dossier (sans lire son contenu). Utilisé pour valider une tâche « script seul ».
func scriptExists(name string) error {
	full, err := scriptsPath(name)
	if err != nil {
		return err
	}
	fi, err := os.Stat(full)
	if err != nil {
		return fmt.Errorf("script introuvable : %s", name)
	}
	if fi.IsDir() {
		return fmt.Errorf("%s est un dossier, pas un script", name)
	}
	return nil
}

// listScripts renvoie les scripts présents (chemins relatifs à scriptsDir, avec
// leur taille), triés. Ignore les dossiers.
func listScripts() ([]scriptInfo, error) {
	root := scriptsDir()
	var out []scriptInfo
	err := filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // dossier absent ou illisible : on renvoie ce qu'on a
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return nil
		}
		size := int64(0)
		if fi, ferr := d.Info(); ferr == nil {
			size = fi.Size()
		}
		out = append(out, scriptInfo{Name: filepath.ToSlash(rel), Size: size})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, err
}

type scriptInfo struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// toolOnlyDirs : dossiers auxquels l'IA n'a AUCUN accès direct (ni lecture, ni
// écriture, ni listing) par bash/write/edit. Le seul chemin autorisé passe par
// les outils dédiés — mem_* pour la mémoire. Ces outils écrivent en Go
// directement (MemAdd…), ils ne passent PAS par fileWrite/runShell, donc ils ne
// sont pas concernés par ces gardes.
//
// Le dossier scripts N'EST PAS ici : l'IA y écrit et y exécute ses scripts
// librement (avec write/bash). Sa durabilité vient seulement de sa séparation
// d'avec le workspace jetable, pas d'un verrou.
func toolOnlyDirs() []string { return []string{memoryDir()} }

// toolOnlyLabel donne le nom des outils à utiliser à la place d'un accès direct
// (seule la mémoire est concernée aujourd'hui, voir toolOnlyDirs).
func toolOnlyLabel() string {
	return "les outils mem_* (mem_search/mem_read/mem_add/mem_edit/mem_delete)"
}

// guardToolOnlyPath refuse un accès write/edit à un chemin (déjà résolu) situé
// dans un dossier « tool-only » (memory). Renvoie "" si le chemin est autorisé.
func guardToolOnlyPath(path string) string {
	for _, d := range toolOnlyDirs() {
		if underDir(path, d) {
			return fmt.Sprintf("[refusé] pas d'accès direct au dossier %s. Utilise %s.", d, toolOnlyLabel())
		}
	}
	return ""
}

// guardToolOnlyCommand refuse une commande shell qui référence un dossier
// « tool-only » (memory), lecture comprise (`cat`, `ls`, `>`…). Contrairement à
// guardDestructive, on bloque TOUTE mention du dossier. Renvoie "" si autorisé.
func guardToolOnlyCommand(command string) string {
	lc := strings.ToLower(command)
	for _, d := range toolOnlyDirs() {
		if strings.Contains(lc, strings.ToLower(normPath(d))) {
			return fmt.Sprintf("[refusé] pas d'accès direct au dossier %s via le shell. Utilise %s.", d, toolOnlyLabel())
		}
	}
	return ""
}

// underDir indique si path est égal à dir ou situé dessous, après nettoyage et
// (sous Windows) insensibilité à la casse.
func underDir(path, dir string) bool {
	a := normPath(path)
	b := normPath(dir)
	if a == b {
		return true
	}
	return strings.HasPrefix(a, b+string(filepath.Separator))
}

// normPath normalise un chemin pour la comparaison : absolu si possible, nettoyé,
// et minusculé sous Windows (système de fichiers insensible à la casse).
func normPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	p = filepath.Clean(p)
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

