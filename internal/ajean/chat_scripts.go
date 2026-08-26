package ajean

// chat_scripts.go — le dossier de scripts DÉDIÉ et PROTÉGÉ de l'agent, et le
// garde-fou qui empêche l'IA (ou un test qu'elle lance) de raser ses propres
// dossiers.
//
// Pourquoi : un jour, l'agent a cloné son dépôt DANS son workspace et un test du
// dépôt a fait un RemoveAll sur ce workspace — tout ce qui y vivait (des scripts
// écrits à la main) est parti. Deux protections en découlent :
//
//  1. Séparation structurelle (la vraie garantie). Les scripts qu'on veut
//     CONSERVER vivent dans scriptsDir() = AJEAN_HOME/scripts, à côté de memory,
//     HORS du workspace jetable. Un nettoyage — ou une catastrophe — dans le
//     workspace ne peut plus les toucher. Le workspace reste le bac à sable où
//     l'IA peut tout casser sans risque (clones, tests, fichiers temporaires).
//
//  2. Garde-fou (défense en profondeur). guardDestructive refuse toute commande
//     shell qui viserait à supprimer/déplacer un dossier protégé (AJEAN_HOME
//     lui-même, scripts, memory, presets, la base ajean.db). Ça ne peut pas
//     arrêter du code arbitraire (un `go test` qui appelle os.RemoveAll), d'où le
//     point 1 comme garantie de fond ; mais ça bloque le cas direct et fréquent
//     du `rm -rf`/`Remove-Item` mal ciblé.

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

// protectedSubtrees : dossiers/fichiers dont la SUPPRESSION est refusée où qu'ils
// soient cités (eux-mêmes ou plus profond) — ils portent l'état durable et ne se
// suppriment jamais au shell. Le workspace ET le dossier scripts n'y sont PAS :
// l'IA les gère librement (elle y crée, écrit, exécute et efface ses fichiers).
func protectedSubtrees() []string {
	return []string{memoryDir(), presetsDir(), dbPath()}
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

// destructiveVerbs : les mots d'une ligne de commande qui dénotent une
// suppression, un déplacement ou un formatage — ceux qui peuvent faire
// disparaître un dossier protégé. En minuscules ; comparaison insensible à la
// casse. On vise large côté verbes (le faux positif se limite à un dossier
// protégé cité dans la commande, cas rarissime en usage normal).
var destructiveVerbs = []string{
	"rm", "rmdir", "unlink", "shred", "rimraf",
	"del", "erase", "rd",
	"remove-item", "ri", // PowerShell (ri = alias de Remove-Item)
	"mv", "move", "ren", "rename", "move-item", "mi",
	"format", "mkfs",
}

// guardDestructive inspecte une commande shell AVANT exécution et renvoie un
// message de refus (non vide) si elle a l'air de vouloir supprimer/déplacer un
// dossier protégé. Renvoie "" si la commande est autorisée.
//
// Deux cas :
//   - un sous-arbre protégé (memory, presets, base) cité N'IMPORTE OÙ (lui-même
//     ou plus profond) → refus ;
//   - la racine des données AJEAN_HOME (ou un de ses ancêtres) visée EN TANT QUE
//     TELLE (rm -rf /etc/ajean, .../*) → refus, car ça emporterait tout ; mais un
//     sous-dossier précis comme workspace ou scripts, lui, reste autorisé (l'IA
//     les gère librement).
//
// Ce n'est pas un bac à sable — du code arbitraire lancé par la commande peut
// toujours contourner ça ; la vraie garantie reste la séparation workspace. C'est
// un filet contre le `rm -rf` mal ciblé.
func guardDestructive(command string) string {
	if !hasDestructiveVerb(command) {
		return ""
	}
	lc := strings.ToLower(command)
	for _, d := range protectedSubtrees() {
		if strings.Contains(lc, strings.ToLower(normPath(d))) {
			return refusDestructif(d)
		}
	}
	// Dossier scripts : on protège le dossier LUI-MÊME (rm -rf scripts, scripts/*)
	// mais on laisse supprimer un script précis (l'IA gère ses fichiers).
	if mentionsDirTarget(command, scriptsDir()) {
		return refusDestructif(scriptsDir())
	}
	// AJEAN_HOME et ses ancêtres : seulement si la commande les vise EUX-MÊMES.
	for anc := normPath(AjeanHome()); anc != ""; anc = parentDir(anc) {
		if mentionsDirTarget(command, anc) {
			return refusDestructif(anc)
		}
	}
	return ""
}

func refusDestructif(dir string) string {
	return fmt.Sprintf("[refusé] commande destructrice visant un dossier/fichier protégé d'AJEAN (%s). "+
		"Mémoire, presets, base, racine des données et le dossier scripts ne se suppriment pas EN BLOC. "+
		"Tu peux en revanche supprimer un fichier précis (ex. un script), et le workspace reste librement videable.", dir)
}

func hasDestructiveVerb(command string) bool {
	lc := strings.ToLower(command)
	for _, v := range destructiveVerbs {
		if containsWord(lc, v) {
			return true
		}
	}
	return false
}

// parentDir renvoie le dossier parent, ou "" une fois la racine atteinte (pour
// arrêter une remontée d'ancêtres sans boucler).
func parentDir(p string) string {
	d := filepath.Dir(p)
	if d == p || d == "." {
		return ""
	}
	return d
}

// mentionsDirTarget indique si la commande vise le dossier `dir` LUI-MÊME (ou tout
// son contenu via un glob), et non un sous-dossier nommé précis. « /etc/ajean »,
// « /etc/ajean/ » et « /etc/ajean/* » ciblent le dossier ; « /etc/ajean/workspace »
// ne le cible pas. Comparaison insensible à la casse.
func mentionsDirTarget(command, dir string) bool {
	lc := strings.ToLower(command)
	d := strings.ToLower(normPath(dir))
	if d == "" {
		return false
	}
	for from := 0; ; {
		i := strings.Index(lc[from:], d)
		if i < 0 {
			return false
		}
		i += from
		if targetsHere(lc, i+len(d)) {
			return true
		}
		from = i + 1
	}
}

// targetsHere : le caractère à l'index `after` (juste après une occurrence du
// dossier) indique-t-il qu'on vise ce dossier lui-même/son contenu ?
func targetsHere(lc string, after int) bool {
	if after >= len(lc) {
		return true // dossier en fin de commande
	}
	switch lc[after] {
	case ' ', '\t', '"', '\'', ';', '&', '|', '\n', '\r':
		return true // borne : le dossier est l'argument
	case '/', '\\':
		// Séparateur : on vise le CONTENU seulement si suit une borne ou un glob
		// (« .../ » ou « .../* »), pas un sous-dossier nommé (« .../workspace »).
		if after+1 >= len(lc) {
			return true
		}
		switch lc[after+1] {
		case ' ', '\t', '"', '\'', '*', ';', '&', '|':
			return true
		}
	}
	return false
}

// containsWord teste la présence de `word` comme jeton délimité (bornes non
// alphanumériques), pour éviter que « rm » matche « warm » ou « format » matche
// « reformatted ».
func containsWord(s, word string) bool {
	for i := 0; ; {
		j := strings.Index(s[i:], word)
		if j < 0 {
			return false
		}
		j += i
		before := j == 0 || !isWordByte(s[j-1])
		after := j+len(word) >= len(s) || !isWordByte(s[j+len(word)])
		if before && after {
			return true
		}
		i = j + 1
	}
}

func isWordByte(b byte) bool {
	return b == '-' || b == '_' ||
		(b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}
