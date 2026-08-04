//go:build windows

package ajean

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
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
//  1. AJEAN n'est pas installé. On DEMANDE : c'est le seul moment où
//     l'utilisateur gagne à savoir ce qu'on pose sur sa machine et où. Puis
//     copie + PATH + raccourcis, et on démarre depuis la copie installée. Un
//     refus vaut « pas maintenant » et la question revient au prochain lancement
//     du fichier : mémorisé, il ne laissait AUCUN moyen d'installer AJEAN.
//
//  2. AJEAN est déjà installé. Le fichier téléchargé se comporte en installeur de
//     mise à jour, avec trois situations bien distinctes (voir runAsInstaller) :
//     plus récent et application arrêtée, on remplace et on démarre, sans un mot ;
//     plus récent mais application EN COURS, on demande s'il faut la fermer pour
//     appliquer la mise à jour, sans quoi le nouveau binaire serait écrit sans
//     que rien ne change à l'écran ; plus ancien que la version installée, on
//     avertit et on laisse choisir entre démarrer et fermer, plutôt que d'imposer
//     une régression silencieuse.
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
	target := installedExePath()
	// canonPath des DEUX côtés : si l'égalité rate parce que les chemins sont
	// écrits différemment (forme courte 8.3, casse, lien), l'application installée
	// se prend pour une copie téléchargée et se relance… en boucle infinie.
	if canonPath(exe) == canonPath(target) {
		// On EST l'application installée. On en profite pour garantir que les
		// raccourcis existent : sans ça, quelqu'un qui perd son raccourci ne
		// retrouve plus AJEAN, et n'a aucune raison de relancer le fichier
		// téléchargé (qu'il a souvent supprimé) pour le récupérer. En tâche de
		// fond, l'appel PowerShell ne doit pas retarder le démarrage.
		go ensureShortcuts(target)
		return false
	}
	if _, err := os.Stat(target); err == nil {
		return runAsInstaller(target)
	}

	// Première installation : là, une question. C'est le seul moment où
	// l'utilisateur a intérêt à savoir ce qui va être posé sur sa machine, et
	// c'est l'absence de cette question qui rendait le double-clic illisible.
	//
	// Un refus n'est PAS mémorisé. Il l'a été en 0.6.11 pour éviter de reposer la
	// question à chaque lancement, mais il devenait alors définitif : plus aucun
	// moyen d'installer AJEAN, même en relançant le fichier téléchargé. Un « non »
	// veut dire « pas maintenant », jamais « plus jamais » : c'est le comportement
	// de n'importe quel installeur, qui repropose tant qu'on le relance.
	if messageBox(
		"Installer AJEAN sur cet ordinateur ?\n\n"+
			"• le programme sera copié dans :\n   "+target+"\n"+
			"• un raccourci sera ajouté au menu Démarrer et au Bureau\n"+
			"• vos réglages et modèles seront rangés dans :\n   "+AjeanHome()+"\n\n"+
			"Vous pourrez ensuite supprimer le fichier que vous venez de télécharger.\n\n"+
			"Répondre Non démarre AJEAN sans rien installer. La question reviendra\n"+
			"au prochain lancement de ce fichier.",
		"Installation d'AJEAN", mbYesNo|mbIconQuestion) != idYes {
		return false
	}

	if _, err := installSelf(filepath.Dir(target)); err != nil {
		messageBox("Installation impossible :\n\n"+err.Error()+"\n\nAJEAN va démarrer depuis l'emplacement actuel.", "AJEAN", mbIconInfo)
		return false
	}
	_, _ = addToUserPath(filepath.Dir(target))
	shortcuts := ensureShortcuts(target)

	messageBox("AJEAN est installé.\n\n"+target+"\n\n"+shortcuts+
		"\n\nL'application va démarrer. Vous pouvez supprimer le fichier téléchargé.",
		"AJEAN", mbIconInfo)

	return launch(target)
}

// launch démarre la copie installée et demande à l'appelant de rendre la main.
// Renvoie false si le lancement échoue, auquel cas l'exécutable courant prend le
// relais : mieux vaut démarrer depuis le mauvais dossier que pas du tout.
func launch(target string) bool {
	cmd := exec.Command(target)
	cmd.Dir = filepath.Dir(target)
	return cmd.Start() == nil
}

// runAsInstaller : AJEAN est déjà installé et on exécute une AUTRE copie (le
// fichier fraîchement téléchargé). Le fichier joue alors le rôle d'installeur de
// mise à jour. Trois situations, trois comportements distincts, parce qu'elles
// n'appellent pas la même décision de l'utilisateur.
func runAsInstaller(target string) bool {
	installed := binaryVersion(target)
	// Version illisible (binaire antérieur aux ressources de version, fichier
	// tronqué par une copie interrompue) : on la traite comme ANCIENNE, donc
	// remplaçable. La traiter comme « égale » condamnait ces installations à ne
	// plus jamais se mettre à jour en lançant le fichier téléchargé, sans que
	// rien ne le signale.
	cmp := 1
	if installed != "" {
		cmp = semver.Compare(ensureV(Version), ensureV(installed))
	}
	running := runningPIDs(target)

	switch {
	case cmp < 0:
		// Le fichier lancé est PLUS ANCIEN que la version installée. Le remplacer
		// serait une régression silencieuse, et démarrer sans rien dire laisserait
		// croire qu'on utilise la version qu'on vient de télécharger.
		if messageBox(
			"Une version plus récente d'AJEAN est déjà installée sur cet ordinateur.\n\n"+
				"    installée : "+installed+"\n"+
				"    ce fichier : "+Version+"\n\n"+
				"Rien ne sera remplacé.\n\n"+
				"Voulez-vous démarrer AJEAN (version "+installed+") ?\n"+
				"Répondre Non ferme simplement cette fenêtre.",
			"AJEAN", mbYesNo|mbIconQuestion) != idYes {
			return true // rien à faire, on quitte sans démarrer
		}
		ensureShortcuts(target)
		return launch(target)

	case cmp > 0 && len(running) > 0:
		// Mise à jour disponible, mais AJEAN tourne. Écrire le nouveau binaire
		// sans redémarrer ne changerait RIEN à ce que l'utilisateur a sous les
		// yeux : l'onglet qui s'ouvrirait serait servi par l'ancienne version. On
		// pose donc la seule question qui compte.
		if messageBox(
			"AJEAN est déjà en cours d'exécution.\n\n"+
				"    version en cours : "+verLabel(installed)+"\n"+
				"    ce fichier : "+Version+" (plus récente)\n\n"+
				"Fermer AJEAN et le redémarrer pour appliquer la mise à jour ?\n\n"+
				"Répondre Non ouvre AJEAN dans sa version actuelle, sans rien mettre à jour.",
			"Mise à jour d'AJEAN", mbYesNo|mbIconQuestion) != idYes {
			ensureShortcuts(target)
			return launch(target) // l'instance en cours reprend la main (port occupé)
		}
		stopProcesses(running)
		target = migrateThenResolveTarget(target)
		if err := replaceInstalled(target); err != nil {
			messageBox("La mise à jour a échoué :\n\n"+err.Error()+"\n\nAJEAN va redémarrer dans sa version actuelle.",
				"AJEAN", mbIconInfo)
		}
		ensureShortcuts(target)
		return launch(target)

	case cmp > 0:
		// Mise à jour, application arrêtée : rien à décider, on remplace et on
		// démarre. C'est le cas courant et il doit rester muet.
		target = migrateThenResolveTarget(target)
		_ = replaceInstalled(target)
		ensureShortcuts(target)
		return launch(target)
	}

	// Même version (ou version installée illisible) : on démarre l'installée.
	ensureShortcuts(target)
	return launch(target)
}

// verLabel évite d'afficher un numéro de version vide dans une boîte de dialogue.
func verLabel(v string) string {
	if v == "" {
		return "inconnue"
	}
	return v
}

// replaceInstalled écrase le binaire installé par celui qu'on exécute. Le
// renommage préalable en .old permet de remplacer un exécutable encore ouvert
// (même mécanique que replaceBinary pour `ajean update`).
// migrateThenResolveTarget tente la migration du dossier de données maintenant
// qu'aucune instance ne tient plus de handle, puis renvoie le chemin du binaire
// installé DANS le dossier résultant.
//
// Recalculer le chemin est indispensable et pas cosmétique : `target` a été
// calculé avant la migration, il désigne donc encore …\jean\bin\ajean.exe. Sans
// ce recalcul, on réinstallerait le binaire dans l'ancien dossier qu'on vient
// tout juste de déplacer, en recréant au passage une arborescence fantôme.
func migrateThenResolveTarget(target string) string {
	if !retryHomeMigration() {
		return target
	}
	return installedExePath()
}

func replaceInstalled(target string) error {
	old := target + ".old"
	_ = os.Remove(old)
	if err := os.Rename(target, old); err != nil {
		return err
	}
	if _, err := installSelf(filepath.Dir(target)); err != nil {
		_ = os.Rename(old, target) // rollback
		return err
	}
	_ = os.Remove(old) // échoue tant que l'ancien tourne ; nettoyé au prochain lancement
	return nil
}

// runningPIDs liste les processus qui exécutent exactement ce fichier. On compare
// le CHEMIN, pas le nom : tuer par nom d'image (« jean.exe ») emporterait aussi
// le processus courant et toute autre copie sans rapport.
//
// La comparaison se fait ICI, sur des chemins canonisés, et non dans le script
// PowerShell : Windows expose le même fichier sous plusieurs écritures (forme
// courte 8.3 « ADMINI~1 » contre « Administrateur », casse variable, liens). Un
// simple -ieq entre chaînes rate alors l'instance en cours, et on remplace le
// binaire en croyant l'application arrêtée : elle continue de tourner en
// ancienne version, sans que rien ne l'indique. Constaté sur banc d'essai.
func runningPIDs(target string) []int {
	want := canonPath(target)
	ps := `Get-Process -ErrorAction SilentlyContinue | ForEach-Object {
  try { if ($_.Path) { "$($_.Id)|$($_.Path)" } } catch { }
}`
	out, err := hideCmd(exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)).Output()
	if err != nil {
		return nil
	}
	var pids []int
	self := os.Getpid()
	for _, line := range strings.Split(string(out), "\n") {
		i := strings.IndexByte(line, '|')
		if i < 0 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(line[:i]))
		if err != nil || n == self {
			continue
		}
		if canonPath(strings.TrimSpace(line[i+1:])) == want {
			pids = append(pids, n)
		}
	}
	return pids
}

// canonPath ramène un chemin Windows à une écriture unique et comparable :
// résolution des liens et de la forme courte 8.3 quand le fichier existe, puis
// minuscules (le système de fichiers est insensible à la casse).
func canonPath(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	if long, err := filepath.EvalSymlinks(p); err == nil {
		p = long
	}
	return strings.ToLower(filepath.Clean(p))
}

// stopProcesses arrête les instances listées, puis laisse le port se libérer :
// sans cette attente, l'instance qu'on relance trouve :8090 encore occupé et se
// contente d'ouvrir le navigateur sur une application en train de mourir.
func stopProcesses(pids []int) {
	for _, pid := range pids {
		if p, err := os.FindProcess(pid); err == nil {
			_ = p.Kill()
		}
	}
	for i := 0; i < 40; i++ { // jusqu'à ~4 s
		if !portBusy(appPort) {
			return
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func portBusy(port int) bool {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return true
	}
	_ = ln.Close()
	return false
}

// binaryVersion lit la version dans les ressources du fichier (VS_VERSIONINFO,
// posées par goversioninfo, cf cmd/ajean/versioninfo.json), comme le fait
// l'onglet « Détails » des propriétés Windows.
//
// ⚠️ NE PAS remplacer par un `ajean version` exécuté : le binaire est compilé en
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
	jeanHome := AjeanHome()
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

// ensureShortcuts garantit qu'un raccourci « AJEAN » existe dans le menu
// Démarrer et sur le Bureau, et qu'il pointe sur le bon exécutable. Appelée à
// CHAQUE lancement du fichier téléchargé, pas seulement à l'installation :
// un raccourci perdu (nettoyage, migration de profil, dossier Démarrer
// réorganisé) rendait l'application introuvable alors qu'elle était installée.
// Les raccourcis manquants sont recréés ; ceux qui existent sont laissés tels
// quels, pour ne pas défaire un déplacement volontaire.
//
// Le raccourci du menu Démarrer va dans « Programmes », l'endroit où Windows
// range les applications installées et où la recherche du menu Démarrer va
// chercher.
func ensureShortcuts(target string) string {
	ps := fmt.Sprintf(`$t=%s
$w=New-Object -ComObject WScript.Shell
$done=@()
$progs=Join-Path ([Environment]::GetFolderPath('StartMenu')) 'Programs'
foreach ($d in @($progs, [Environment]::GetFolderPath('Desktop'))) {
  if (-not $d) { continue }
  if (-not (Test-Path $d)) { continue }
  $lnk=Join-Path $d 'AJEAN.lnk'
  # Un raccourci existant est REPOINTÉ s'il vise autre chose que le binaire
  # canonique — typiquement l'ancien « jean.exe », qui n'est plus qu'un alias.
  # Le laisser en l'état condamnait l'utilisateur à relancer indéfiniment une
  # version périmée par son propre raccourci.
  if (Test-Path $lnk) {
    try {
      $s=$w.CreateShortcut($lnk)
      if ($s.TargetPath -and $s.TargetPath -ne $t -and (Test-Path $s.TargetPath)) {
        $s.TargetPath=$t
        $s.WorkingDirectory=(Split-Path $t)
        $s.Save()
      }
    } catch {}
    $done+=$d; continue
  }
  try {
    $s=$w.CreateShortcut($lnk)
    $s.TargetPath=$t
    $s.WorkingDirectory=(Split-Path $t)
    $s.Description='AJEAN, votre IA locale'
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

// removeShortcuts efface les raccourcis posés par ensureShortcuts. Renvoie true
// si au moins un a été supprimé. Balaie aussi l'ancien emplacement (racine du
// menu Démarrer, utilisé jusqu'en 0.6.11) pour ne pas laisser d'orphelin.
func removeShortcuts() bool {
	ps := `$n=0
$sm=[Environment]::GetFolderPath('StartMenu')
foreach ($d in @($sm, (Join-Path $sm 'Programs'), [Environment]::GetFolderPath('Desktop'))) {
  if (-not $d) { continue }
  $p=Join-Path $d 'AJEAN.lnk'
  if (Test-Path $p) { try { Remove-Item $p -Force; $n++ } catch {} }
}
Write-Output $n`
	out, err := hideCmd(exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)).CombinedOutput()
	return err == nil && strings.TrimSpace(string(out)) != "0"
}
