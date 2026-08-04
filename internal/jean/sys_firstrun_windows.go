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

	"golang.org/x/mod/semver"
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
// Maintenant, deux cas seulement :
//
//  1. AJEAN n'est pas installé. On DEMANDE, une bonne fois : c'est le seul moment
//     où l'utilisateur gagne à savoir ce qu'on pose sur sa machine et où. Puis
//     copie + PATH + raccourcis, et on démarre depuis la copie installée. Un refus
//     est mémorisé, on ne repose pas la question.
//
//  2. AJEAN est déjà installé. Le fichier téléchargé se comporte alors comme un
//     installeur de mise à jour : s'il est plus récent, il remplace le binaire
//     installé, puis l'application démarre. AUCUN message. Annoncer « vous lancez
//     une copie, ça ne sert à rien » puis démarrer quand même n'apprenait rien et
//     ne laissait rien à décider.
//
// Dans les deux cas il n'existe au final qu'UN binaire qui compte, à un
// emplacement connu, et c'est celui que le bouton de mise à jour modifie.

const (
	mbYesNo        = 0x00000004
	mbIconQuestion = 0x00000020
	mbIconInfo     = 0x00000040
	mbTopmost      = 0x00040000
	idYes          = 6
)

var (
	pMessageBoxW = u32s.NewProc("MessageBoxW")

	verDLL                   = syscall.NewLazyDLL("version.dll")
	pGetFileVersionInfoSizeW = verDLL.NewProc("GetFileVersionInfoSizeW")
	pGetFileVersionInfoW     = verDLL.NewProc("GetFileVersionInfoW")
	pVerQueryValueW          = verDLL.NewProc("VerQueryValueW")
)

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
		// AJEAN est déjà installé. Lancer le fichier téléchargé doit alors se
		// comporter comme un installeur de mise à jour : si cette copie est plus
		// récente, elle remplace celle installée, et l'application démarre. Sans
		// message : annoncer « vous lancez une copie » puis démarrer quand même
		// n'apprend rien à l'utilisateur et ne lui laisse rien à décider.
		upgradeInstalled(target)
		return launch(target)
	}

	// Première installation : là, une question. C'est le seul moment où
	// l'utilisateur a intérêt à savoir ce qui va être posé sur sa machine, et
	// c'est l'absence de cette question qui rendait le double-clic illisible.
	if declinedInstall() {
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
		// Refus mémorisé : reposer la question à chaque double-clic serait
		// ignorer la réponse déjà donnée.
		_ = os.WriteFile(declineFlag(), []byte("non\n"), 0o644)
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

	return launch(target)
}

func declineFlag() string { return filepath.Join(JeanHome(), ".install_declined") }

func declinedInstall() bool {
	_, err := os.Stat(declineFlag())
	return err == nil
}

// launch démarre la copie installée et demande à l'appelant de rendre la main.
// Renvoie false si le lancement échoue, auquel cas l'exécutable courant prend le
// relais : mieux vaut démarrer depuis le mauvais dossier que pas du tout.
func launch(target string) bool {
	cmd := exec.Command(target)
	cmd.Dir = filepath.Dir(target)
	return cmd.Start() == nil
}

// upgradeInstalled remplace le binaire installé par celui qu'on exécute, quand
// ce dernier est plus récent : lancer le fichier téléchargé met ainsi AJEAN à
// jour, ce qui est la seule chose qu'on puisse raisonnablement en attendre.
// Silencieux et best-effort : si la copie échoue (application en cours
// d'exécution, donc fichier verrouillé), on démarre simplement la version en
// place, qui remontera l'onglet existant.
func upgradeInstalled(target string) {
	installed := binaryVersion(target)
	if installed == "" || semver.Compare(ensureV(Version), ensureV(installed)) <= 0 {
		return // même version, plus ancienne, ou version illisible : on ne touche à rien
	}
	// L'exe installé peut être en cours d'exécution : on l'écarte d'abord, comme
	// pour une mise à jour classique (voir replaceBinary).
	old := target + ".old"
	_ = os.Remove(old)
	if err := os.Rename(target, old); err != nil {
		return
	}
	if _, err := installSelf(filepath.Dir(target)); err != nil {
		_ = os.Rename(old, target) // rollback
		return
	}
	_ = os.Remove(old) // échoue si l'ancien tourne encore ; nettoyé au prochain lancement
}

// binaryVersion lit la version dans les ressources du fichier (VS_VERSIONINFO,
// posées par goversioninfo, cf cmd/jean/versioninfo.json), comme le fait
// l'onglet « Détails » des propriétés Windows.
//
// ⚠️ NE PAS remplacer par un `jean version` exécuté : le binaire est compilé en
// sous-système GUI, il s'attache à la console du parent et n'écrit RIEN dans un
// tuyau. La sortie capturée est vide, donc la comparaison de versions échouerait
// toujours en silence et la mise à jour ne se ferait jamais (vérifié).
//
// Renvoie "" si la ressource est absente ou illisible : on s'abstient alors de
// remplacer quoi que ce soit.
func binaryVersion(path string) string {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return ""
	}
	size, _, _ := pGetFileVersionInfoSizeW.Call(uintptr(unsafe.Pointer(p)), 0)
	if size == 0 {
		return ""
	}
	buf := make([]byte, size)
	if r, _, _ := pGetFileVersionInfoW.Call(
		uintptr(unsafe.Pointer(p)), 0, size, uintptr(unsafe.Pointer(&buf[0]))); r == 0 {
		return ""
	}
	sub, _ := syscall.UTF16PtrFromString(`\`)
	var info *vsFixedFileInfo
	var length uint32
	if r, _, _ := pVerQueryValueW.Call(
		uintptr(unsafe.Pointer(&buf[0])), uintptr(unsafe.Pointer(sub)),
		uintptr(unsafe.Pointer(&info)), uintptr(unsafe.Pointer(&length))); r == 0 || info == nil {
		return ""
	}
	return fmt.Sprintf("%d.%d.%d",
		info.FileVersionMS>>16, info.FileVersionMS&0xffff, info.FileVersionLS>>16)
}

// vsFixedFileInfo : bloc racine de VS_VERSIONINFO. Seuls les champs de version
// nous intéressent, mais la structure doit être complète pour l'alignement.
type vsFixedFileInfo struct {
	Signature        uint32
	StrucVersion     uint32
	FileVersionMS    uint32
	FileVersionLS    uint32
	ProductVersionMS uint32
	ProductVersionLS uint32
	FileFlagsMask    uint32
	FileFlags        uint32
	FileOS           uint32
	FileType         uint32
	FileSubtype      uint32
	FileDateMS       uint32
	FileDateLS       uint32
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
