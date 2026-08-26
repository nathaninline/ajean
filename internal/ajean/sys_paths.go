package ajean

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
)

// « Où sont mes fichiers ? » — la question que personne ne devrait avoir à poser.
// Un binaire unique qui s'auto-installe au premier lancement rend les
// emplacements invisibles : on ne sait plus si le .exe qu'on a double-cliqué EST
// l'application ou seulement un installateur, ni où atterrissent la config, les
// modèles et les fichiers que l'agent écrit. On centralise donc la réponse ici,
// et on l'expose à la fois en ligne de commande (`ajean where`) et dans l'UI.

type ajeanPaths struct {
	Home      string `json:"home"`      // AJEAN_HOME : racine des données
	Database  string `json:"database"`  // ajean.db : config, préférences, conversation, clés
	Exe       string `json:"exe"`       // binaire en cours d'exécution
	Installed string `json:"installed"` // binaire installé (peut différer de Exe)
	Workspace string `json:"workspace"` // dossier de travail du mode agent (jetable)
	Scripts   string `json:"scripts"`   // dossier des scripts de l'agent (protégé)
	Memory    string `json:"memory"`
	Presets   string `json:"presets"`
	Models    string `json:"models"`
	Backends  string `json:"backends"`
}

// installedExePath est l'emplacement canonique du binaire après installation.
// Il diffère par plateforme : sous Unix les installateurs posent /usr/local/bin/ajean
// (c'est ce que référencent les unités systemd et le plist launchd) ; sous Windows,
// faute d'équivalent, on utilise AJEAN_HOME\bin ajouté au PATH utilisateur.
func installedExePath() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(binDir(), "ajean.exe")
	}
	return "/usr/local/bin/ajean"
}

func currentPaths() ajeanPaths {
	exe, _ := os.Executable()
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return ajeanPaths{
		Home:      AjeanHome(),
		Database:  dbPath(),
		Exe:       exe,
		Installed: installedExePath(),
		Workspace: agentWorkspace(),
		Scripts:   scriptsDir(),
		Memory:    memoryDir(),
		Presets:   presetsDir(),
		Models:    modelsDir(),
		Backends:  backendsDir(),
	}
}

func cmdWhere(args []string) error {
	p := currentPaths()
	fmt.Printf("Emplacements AJEAN\n\n")
	for _, row := range [][2]string{
		{"données (AJEAN_HOME)", p.Home},
		{"base", p.Database},
		{"binaire en cours", p.Exe},
		{"binaire installé", p.Installed},
		{"travail de l'agent", p.Workspace},
		{"scripts", p.Scripts},
		{"mémoire", p.Memory},
		{"presets", p.Presets},
		{"modèles", p.Models},
		{"backends", p.Backends},
	} {
		fmt.Printf("  %-20s %s\n", row[0], row[1])
	}
	if p.Exe != p.Installed {
		fmt.Printf("\n%s tu exécutes une copie qui n'est PAS le binaire installé.\n", dim("[info]"))
		fmt.Printf("  Les mises à jour depuis l'application ne modifient que la copie lancée.\n")
	}
	return nil
}

// handlePaths (GET /api/paths) alimente le bloc « Emplacements » de l'UI.
func handlePaths(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, currentPaths())
}
