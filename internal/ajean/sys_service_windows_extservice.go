//go:build windows

package ajean

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// sys_service_windows_extservice.go — EXTERNAL_SERVICE sous Windows,
// l'équivalent du pilote systemd de sys_service_linux.go (unitAction,
// waitGPUsReleased, unitPIDs, unitActiveState). Un preset externe peut nommer
// un service local (« ajean-<nom> ») ; AJEAN le démarre à la bascule sur le
// preset, l'arrête quand on en repart, et attend la libération de la VRAM avant
// de charger le modèle suivant — exactement le contrat des unités systemd, sans
// systemd.
//
// Définition : l'équivalent du fichier d'unité vit dans
// $AJEAN_HOME\services\ajean-<nom>.json — le dossier est créé au premier
// service. JSON (pas de .bat) : la commande, ses arguments, le cwd et les
// variables d'environnement sont portés sans ambiguïté de quoting :
//
//	{
//	  "command": "I:\\Strata\\.venv\\Scripts\\python.exe",
//	  "args": ["I:\\Strata\\serve\\server.py", "--engine", "strata", "--port", "8080"],
//	  "cwd": "I:\\Strata",
//	  "env": { "PYTHONUTF8": "1" }
//	}
//
// Le processus est lancé détaché (comme `ajean serve`), suivi par un fichier
// PID, journalisé dans $AJEAN_HOME, arrêté par taskkill /T /F : mêmes
// primitives que le superviseur local (svcStart/svcStop), sans droits admin.

// extServiceDef porte la définition d'un service externe sous Windows.
type extServiceDef struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Cwd     string            `json:"cwd"`
	Env     map[string]string `json:"env"`
}

// servicesDir : le dossier des définitions de services externes
// ($AJEAN_HOME\services), créé à la volée au premier service. Il vit ici (et
// pas dans run.go) car seul ce fichier — tagué windows — l'utilise : une
// fonction partagée inutilisée hors Windows ferait un U1000 de staticcheck sur
// la CI ubuntu (le projet est à zéro avertissement).
func servicesDir() string { return filepath.Join(AjeanHome(), "services") }

// extMu sérialise les démarrages : un double start parallèle lançait deux
// moteurs (même écueil que svcMu pour le moteur local, issue #114).
var extMu sync.Mutex

// extUnitPath : la définition du service, $AJEAN_HOME\services\ajean-<nom>.json.
func extUnitPath(unit string) string {
	return filepath.Join(servicesDir(), unit+".json")
}

// extPidPath / extLogPath : suivi et journal, à côté de ceux du moteur local
// (ajean-engine.pid / ajean-engine.log), un fichier par unité.
func extPidPath(unit string) string { return filepath.Join(AjeanHome(), unit+".pid") }
func extLogPath(unit string) string { return filepath.Join(AjeanHome(), unit+".log") }

// loadExtServiceDef lit et valide la définition. Un chemin relatif est résolu
// depuis le dossier du fichier de définition (comme systemd résout ExecStart
// relatif) — un service reste portable si tout $AJEAN_HOME bouge.
func loadExtServiceDef(unit string) (*extServiceDef, error) {
	p := extUnitPath(unit)
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("définition introuvable : %s — créez ce fichier (command, args, cwd)", p)
		}
		return nil, err
	}
	var def extServiceDef
	if err := json.Unmarshal(b, &def); err != nil {
		return nil, fmt.Errorf("définition illisible (%s) : %w", p, err)
	}
	def.Command = strings.TrimSpace(def.Command)
	if def.Command == "" {
		return nil, fmt.Errorf("définition %s : champ \"command\" manquant", p)
	}
	if !filepath.IsAbs(def.Command) {
		def.Command = filepath.Join(filepath.Dir(p), def.Command)
	}
	if def.Cwd != "" && !filepath.IsAbs(def.Cwd) {
		def.Cwd = filepath.Join(filepath.Dir(p), def.Cwd)
	}
	return &def, nil
}

// extUnitPID : le PID du service, 0 s'il ne tourne pas (fichier absent ou
// process mort). Le PID ne sert qu'au kill (taskkill /T) et au suivi : le
// status vient de processAlive, pas du fichier.
func extUnitPID(unit string) int {
	b, err := os.ReadFile(extPidPath(unit))
	if err != nil {
		return 0
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid <= 0 || !processAlive(pid) {
		return 0
	}
	return pid
}

// unitAction : start/stop d'une unité externe. Même contrat que la version
// Linux — syncExternalService (backend_extservice.go) ne connaît que ces deux
// actions.
func unitAction(unit, action string) error {
	switch action {
	case "start":
		return extUnitStart(unit)
	case "stop":
		return extUnitStop(unit)
	}
	return fmt.Errorf("action inconnue : %s", action)
}

func extUnitStart(unit string) error {
	extMu.Lock()
	defer extMu.Unlock()
	if pid := extUnitPID(unit); pid > 0 {
		return nil // idempotent : syncExternalService relance start à chaque bascule
	}
	def, err := loadExtServiceDef(unit)
	if err != nil {
		return err
	}
	// Journal : best-effort, comme openEngineLog — un log inaccessible ne doit
	// PAS faire échouer le démarrage (issue #88).
	var logf *os.File
	if f, err := os.OpenFile(extLogPath(unit), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); err == nil {
		logf = f
	}
	defer func() {
		if logf != nil {
			logf.Close()
		}
	}()

	cmd := exec.Command(def.Command, def.Args...)
	if logf != nil {
		cmd.Stdout = logf
		cmd.Stderr = logf
	}
	if def.Cwd != "" {
		cmd.Dir = def.Cwd
	}
	// Environnement : celui du parent, complété par la définition.
	env := os.Environ()
	for k, v := range def.Env {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	// Détaché, sans console : le service survit à la fermeture de l'UI et ne
	// doit jamais faire clignoter de fenêtre noire (même attr que svcStart).
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: createNewProcessGroup | detachedProcess | createNoWindow,
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("démarrage de %s : %w", unit, err)
	}
	pid := cmd.Process.Pid
	// PID en best-effort : un échec d'écriture dégrade stop/status mais ne
	// casse pas un moteur déjà lancé.
	if err := os.WriteFile(extPidPath(unit), []byte(strconv.Itoa(pid)), 0o644); err != nil {
		fmt.Printf("%s PID de %s non enregistré (%v)\n", yellow("[avert]"), unit, err)
	}
	_ = cmd.Process.Release()

	// checkStarted (2 s) du moteur local : trop court pour un gros modèle
	// (Strata charge en une minute ou plus), trop long pour une vraie erreur.
	// Compromis : 2 s, comme svcStart — externalServiceReady est le juge de
	// paix (unité active + API qui répond), il distingue « chargement »
	// (silence) de « échec » (unité morte).
	time.Sleep(2 * time.Second)
	if !processAlive(pid) {
		fmt.Printf("%s %s : le processus s'est arrêté — derniers logs :\n", red("[ERREUR]"), unit)
		fmt.Println("------------------------------------------------")
		fmt.Print(tailFile(extLogPath(unit), 20))
		fmt.Println("------------------------------------------------")
		_ = os.Remove(extPidPath(unit))
		return fmt.Errorf("service %s non démarré", unit)
	}
	return nil
}

func extUnitStop(unit string) error {
	pid := extUnitPID(unit)
	if pid == 0 {
		_ = os.Remove(extPidPath(unit))
		return nil // rien à arrêter : idempotent
	}
	// taskkill /T : le process ET ses enfants (python → moteur → workers).
	cmd := hideCmd(exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F"))
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("arrêt de %s (PID %d): %w\n%s", unit, pid, err, string(out))
	}
	// taskkill /F rend la main AVANT la destruction effective : la VRAM reste
	// verrouillée par le pilote un instant. waitGPUsReleased (appelé juste
	// après par syncExternalService) couvre l'intervalle ; on ne bloque pas
	// ici, comme svcStop.
	_ = os.Remove(extPidPath(unit))
	return nil
}

// unitActiveState : ActiveState équivalent — « active » si le process tourne,
// « failed » s'il s'est arrêté (PID connu mais mort), « inactive » sinon.
func unitActiveState(unit string) string {
	p := extPidPath(unit)
	b, err := os.ReadFile(p)
	if err != nil {
		return "inactive"
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid > 0 {
		if processAlive(pid) {
			return "active"
		}
		return "failed"
	}
	return "inactive"
}

// unitPIDs : le PID du service (nil s'il ne tourne pas). syncExternalService
// les relève AVANT le stop pour que waitGPUsReleased sache quoi attendre.
func unitPIDs(unit string) []int {
	pid := extUnitPID(unit)
	if pid == 0 {
		return nil
	}
	return []int{pid}
}

// waitGPUsReleased : porté de sys_service_linux.go — nvidia-smi existe sous
// Windows, même interrogation. On ne regarde QUE les pids donnés : un autre
// programme GPU permanent ne doit pas faire attendre le délai entier.
func waitGPUsReleased(pids []int, max time.Duration) {
	if _, err := exec.LookPath("nvidia-smi"); err != nil {
		return
	}
	watch := map[string]bool{}
	for _, p := range pids {
		watch[strconv.Itoa(p)] = true
	}
	deadline := time.Now().Add(max)
	for time.Now().Before(deadline) {
		out, err := exec.Command("nvidia-smi", "--query-compute-apps=pid", "--format=csv,noheader").Output()
		if err != nil {
			return
		}
		busy := false
		for _, l := range strings.Split(string(out), "\n") {
			l = strings.TrimSpace(l)
			if l != "" && (len(watch) == 0 || watch[l]) {
				busy = true
				break
			}
		}
		if !busy {
			return
		}
		time.Sleep(500 * time.Millisecond)
	}
}
