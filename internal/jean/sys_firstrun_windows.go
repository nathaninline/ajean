//go:build windows

package jean

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

// Premier lancement sous Windows.
//
// Avant : double-cliquer jean-windows-amd64.exe déclenchait une installation
// SILENCIEUSE (copie du binaire dans %ProgramData%\jean\bin, ajout au PATH) dont
// l'utilisateur ne voyait rien — cmdApp appelait cmdInstall sans console pour en
// afficher la sortie. D'où la confusion légitime : est-ce que ça installe ou est-ce
// que ça lance ? Et surtout, deux copies du binaire coexistaient (celle du Bureau,
// qui tourne, et celle installée) : « mettre à jour » ne touchait que celle lancée,
// tandis que le raccourci et le PATH pointaient sur l'autre, restée en arrière.
//
// Maintenant : on DEMANDE, une bonne fois. Si l'utilisateur accepte, on installe
// pour de bon (copie + PATH + raccourcis menu Démarrer/Bureau) puis on relance
// depuis la copie installée et on quitte — il n'existe alors qu'UN seul binaire qui
// compte, à un emplacement connu, et le bouton de mise à jour agit dessus. S'il
// refuse, on lance l'application telle quelle, sans rien écrire ailleurs que dans
// le dossier de données.

const (
	mbYesNo        = 0x00000004
	mbIconQuestion = 0x00000020
	mbIconInfo     = 0x00000040
	mbTopmost      = 0x00040000
	idYes          = 6
)

var pMessageBoxW = u32s.NewProc("MessageBoxW")

func messageBox(text, title string, flags uintptr) int {
	t, _ := syscall.UTF16PtrFromString(text)
	c, _ := syscall.UTF16PtrFromString(title)
	r, _, _ := pMessageBoxW.Call(0, uintptr(unsafe.Pointer(t)), uintptr(unsafe.Pointer(c)), flags|mbTopmost)
	return int(r)
}

// appFirstRun prépare le dossier de données et, si le binaire lancé n'est pas
// celui installé, propose l'installation. Renvoie true si l'app a été relancée
// depuis la copie installée (l'appelant doit alors rendre la main immédiatement).
func appFirstRun() bool {
	// Le dossier de données et config.env sont indispensables pour que l'UI
	// s'ouvre : on les crée sans rien demander, ils ne surprennent personne.
	_ = provisionDataDir()

	exe, err := os.Executable()
	if err != nil {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	target := installedExePath()
	if strings.EqualFold(exe, target) {
		return false // on EST déjà l'application installée
	}
	if _, err := os.Stat(target); err == nil {
		// Une copie est déjà installée — typiquement par l'installation silencieuse
		// des versions ≤ 0.6.9. On le signale UNE SEULE FOIS : répéter la boîte à
		// chaque double-clic du fichier téléchargé serait un harcèlement, pas une
		// information.
		notice := filepath.Join(JeanHome(), ".install_notice")
		if _, seen := os.Stat(notice); seen != nil {
			messageBox(
				"AJEAN est déjà installé sur cet ordinateur :\n\n"+target+
					"\n\nCe fichier-ci n'est qu'une copie téléchargée : vous pouvez la supprimer et lancer AJEAN depuis le menu Démarrer.\n\nCette copie va démarrer normalement.",
				"AJEAN", mbIconInfo)
			_ = os.WriteFile(notice, []byte(target+"\n"), 0o644)
		}
		return false
	}

	if messageBox(
		"Installer AJEAN sur cet ordinateur ?\n\n"+
			"• le programme sera copié dans :\n   "+target+"\n"+
			"• un raccourci sera ajouté au menu Démarrer et au Bureau\n"+
			"• vos réglages et modèles seront rangés dans :\n   "+JeanHome()+"\n\n"+
			"Vous pourrez ensuite supprimer le fichier que vous venez de télécharger.\n\n"+
			"Répondre Non lance AJEAN sans rien installer.",
		"Installation d'AJEAN", mbYesNo|mbIconQuestion) != idYes {
		return false
	}

	if _, err := installSelf(filepath.Dir(target)); err != nil {
		messageBox("Installation impossible :\n\n"+err.Error()+"\n\nAJEAN va démarrer depuis l'emplacement actuel.", "AJEAN", mbIconInfo)
		return false
	}
	_, _ = addToUserPath(filepath.Dir(target))
	shortcuts := createShortcuts(target)

	messageBox("AJEAN est installé.\n\n"+target+"\n\n"+shortcuts+
		"\n\nL'application va démarrer. Vous pouvez supprimer le fichier téléchargé.",
		"AJEAN", mbIconInfo)

	// Relance depuis la copie installée : à partir de maintenant, une seule et
	// même image du programme se met à jour, se relance et apparaît dans le tray.
	cmd := exec.Command(target)
	cmd.Dir = filepath.Dir(target)
	if err := cmd.Start(); err != nil {
		return false // échec du relancement : on continue avec la copie courante
	}
	return true
}

// provisionDataDir crée le dossier de données et un config.env de départ.
// Extrait de cmdInstall pour être réutilisable au premier lancement, sans
// embarquer l'installation du binaire ni les messages de console.
func provisionDataDir() error {
	jeanHome := JeanHome()
	for _, d := range []string{jeanHome, filepath.Join(jeanHome, "configs"), filepath.Join(jeanHome, "SKILLS")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}
	conf := filepath.Join(jeanHome, "config.env")
	if _, err := os.Stat(conf); os.IsNotExist(err) {
		return os.WriteFile(conf, []byte(configTemplate), 0o644)
	}
	return nil
}

// createShortcuts pose un raccourci « AJEAN » dans le menu Démarrer et sur le
// Bureau. Renvoie une phrase décrivant ce qui a été créé (affichée à
// l'utilisateur). Best-effort : un échec n'empêche pas l'installation.
func createShortcuts(target string) string {
	ps := fmt.Sprintf(`$t=%s
$w=New-Object -ComObject WScript.Shell
$done=@()
foreach ($d in @([Environment]::GetFolderPath('StartMenu'), [Environment]::GetFolderPath('Desktop'))) {
  if (-not $d) { continue }
  try {
    $s=$w.CreateShortcut((Join-Path $d 'AJEAN.lnk'))
    $s.TargetPath=$t
    $s.WorkingDirectory=(Split-Path $t)
    $s.Description='AJEAN — IA locale'
    $s.Save()
    $done+=$d
  } catch {}
}
Write-Output ($done -join ';')`, psQuote(target))
	cmd := hideCmd(exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps))
	out, err := cmd.CombinedOutput()
	if err != nil || strings.TrimSpace(string(out)) == "" {
		return "(raccourcis non créés — lancez AJEAN depuis " + target + ")"
	}
	return "Raccourci « AJEAN » ajouté au menu Démarrer et au Bureau."
}

// removeShortcuts efface les raccourcis posés par createShortcuts. Renvoie true
// si au moins un a été supprimé.
func removeShortcuts() bool {
	ps := `$n=0
foreach ($d in @([Environment]::GetFolderPath('StartMenu'), [Environment]::GetFolderPath('Desktop'))) {
  if (-not $d) { continue }
  $p=Join-Path $d 'AJEAN.lnk'
  if (Test-Path $p) { try { Remove-Item $p -Force; $n++ } catch {} }
}
Write-Output $n`
	out, err := hideCmd(exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)).CombinedOutput()
	return err == nil && strings.TrimSpace(string(out)) != "0"
}
