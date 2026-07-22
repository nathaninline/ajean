// web_llamacpp.go — pilotage du backend llama.cpp depuis l'UI web :
// statut (commit, retard, binaire, plan de build), vérification des mises à
// jour (git fetch), et jobs asynchrones install / update / rebuild dont la
// progression et les logs sont pollés par le client.
//
//	GET  /api/llamacpp           → statut complet (dépôt, commit, binaire, plan)
//	POST /api/llamacpp/check     → git fetch + retard sur origin (réseau)
//	POST /api/llamacpp/install   → job d'installation {force?}
//	POST /api/llamacpp/update    → job de mise à jour {clean?}
//	GET  /api/llamacpp/job?from= → progression du job (phase, lignes, erreur)
package jean

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// lcJob est LE job llama.cpp en cours (un seul à la fois). Les lignes de log
// sont indexées de façon absolue (base = index de Lines[0]) pour que le client
// puisse poller « à partir de N » même quand on tronque le début.
type lcJob struct {
	Action    string // "install" | "update"
	Phase     string
	Compiled  int // fichiers compilés (progression du build)
	Running   bool
	Err       string
	StartedAt int64
	EndedAt   int64
	OldCommit string
	NewCommit string
	lines     []string
	base      int
}

var (
	lcMu  sync.Mutex
	lcCur *lcJob
)

const lcMaxLines = 4000

// lcAppend ajoute une ligne au log du job courant et met à jour la phase / le
// compteur de fichiers compilés à partir du contenu (mêmes heuristiques que le
// spinner du CLI). Appelée par le sink de build (emitBuildLine).
func lcAppend(line string) {
	lcMu.Lock()
	defer lcMu.Unlock()
	if lcCur == nil {
		return
	}
	lcCur.lines = append(lcCur.lines, line)
	if len(lcCur.lines) > lcMaxLines {
		drop := len(lcCur.lines) - lcMaxLines
		lcCur.lines = append([]string(nil), lcCur.lines[drop:]...)
		lcCur.base += drop
	}
	if f := compiledFile(line); f != "" {
		lcCur.Compiled++
		lcCur.Phase = fmt.Sprintf("compilation… %d fichiers", lcCur.Compiled)
	} else if p := phaseLabel(line); p != "" {
		lcCur.Phase = p
	}
}

// lcPhase pose une phase explicite (étapes hors build : clone, fetch, service…)
// et la trace aussi dans le log.
func lcPhase(phase string) {
	lcMu.Lock()
	if lcCur != nil {
		lcCur.Phase = phase
		lcCur.lines = append(lcCur.lines, "▶ "+phase)
	}
	lcMu.Unlock()
}

// startLcJob démarre un job (install ou update) si aucun n'est en cours.
func startLcJob(action string, run func()) error {
	lcMu.Lock()
	defer lcMu.Unlock()
	if lcCur != nil && lcCur.Running {
		return fmt.Errorf("un job %s est déjà en cours", lcCur.Action)
	}
	lcCur = &lcJob{Action: action, Running: true, Phase: "démarrage…", StartedAt: time.Now().Unix()}
	setBuildSink(lcAppend)
	go func() {
		defer func() {
			setBuildSink(nil)
			lcMu.Lock()
			lcCur.Running = false
			lcCur.EndedAt = time.Now().Unix()
			lcMu.Unlock()
		}()
		run()
	}()
	return nil
}

func lcFail(err error) {
	lcMu.Lock()
	if lcCur != nil {
		lcCur.Err = err.Error()
		lcCur.Phase = "échec"
		lcCur.lines = append(lcCur.lines, "✗ "+err.Error())
	}
	lcMu.Unlock()
}

func lcDone(msg string) {
	lcMu.Lock()
	if lcCur != nil {
		lcCur.Phase = msg
		lcCur.lines = append(lcCur.lines, "✓ "+msg)
	}
	lcMu.Unlock()
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handleLlamacpp renvoie le statut complet du backend llama.cpp : dépôt,
// commit courant, retard connu (sans fetch réseau), binaire compilé et son
// usage dans config.env, plan de build détecté, et le job éventuel.
func handleLlamacpp(w http.ResponseWriter, r *http.Request) {
	repo := llamacppRepoDir()
	out := map[string]any{
		"repo":      repo,
		"installed": isDir(filepath.Join(repo, ".git")),
	}
	if out["installed"].(bool) {
		out["branch"] = gitOutput(repo, "rev-parse", "--abbrev-ref", "HEAD")
		out["commit"] = gitOutput(repo, "rev-parse", "--short", "HEAD")
		out["commit_date"] = gitOutput(repo, "log", "-1", "--format=%ci")
		out["commit_msg"] = gitOutput(repo, "log", "-1", "--format=%s")
		if br, _ := out["branch"].(string); br != "" && br != "HEAD" {
			if behind := gitOutput(repo, "rev-list", "--count", "HEAD..origin/"+br); behind != "" {
				n, _ := strconv.Atoi(behind)
				out["behind"] = n
			}
		}
	}
	bin := llamaServerBin(repo)
	out["bin"] = bin
	cfgBin := ReadConfig()["BIN"]
	out["config_bin"] = cfgBin
	out["in_use"] = bin != "" && samePath(bin, cfgBin)

	plan := detectBuildPlan()
	out["plan"] = map[string]any{
		"backend": plan.backend,
		"arch":    plan.cudaArch,
		"jobs":    plan.jobs,
	}
	// Mode « binaires précompilés » : installé ? utilisé par la config ?
	pbTag, _ := prebuiltVersion()
	pbBin := prebuiltServerBin()
	out["prebuilt"] = map[string]any{
		"tag":    pbTag,
		"bin":    pbBin,
		"in_use": pbBin != "" && samePath(pbBin, cfgBin),
	}
	out["backends_dir"] = filepath.Join(JeanHome(), "backends")
	out["job"] = lcJobSnapshot(0, false)
	sendJSON(w, 200, out)
}

// samePath compare deux chemins en neutralisant séparateurs, symlinks et casse
// (Windows).
func samePath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	norm := func(p string) string {
		if real, err := filepath.EvalSymlinks(p); err == nil {
			p = real
		}
		p = filepath.Clean(p)
		return strings.ToLower(filepath.ToSlash(p))
	}
	return norm(a) == norm(b)
}

// handleLlamacppCheck fait un vrai git fetch puis renvoie le retard sur origin
// et le dernier commit distant. Synchrone (quelques secondes réseau).
func handleLlamacppCheck(w http.ResponseWriter, r *http.Request) {
	repo := llamacppRepoDir()
	if !isDir(filepath.Join(repo, ".git")) {
		sendJSON(w, 200, map[string]any{"ok": false, "error": "llama.cpp n'est pas installé"})
		return
	}
	branch := gitOutput(repo, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "" || branch == "HEAD" {
		branch = "master"
	}
	if err := runStep("git fetch", repo, "git", "fetch", "origin", "--quiet"); err != nil {
		sendJSON(w, 200, map[string]any{"ok": false, "error": "git fetch a échoué : " + err.Error()})
		return
	}
	behind := 0
	if b := gitOutput(repo, "rev-list", "--count", "HEAD..origin/"+branch); b != "" {
		behind, _ = strconv.Atoi(b)
	}
	sendJSON(w, 200, map[string]any{
		"ok":            true,
		"behind":        behind,
		"local":         gitOutput(repo, "rev-parse", "--short", "HEAD"),
		"remote":        gitOutput(repo, "rev-parse", "--short", "origin/"+branch),
		"remote_date":   gitOutput(repo, "log", "-1", "--format=%ci", "origin/"+branch),
		"remote_msg":    gitOutput(repo, "log", "-1", "--format=%s", "origin/"+branch),
		"branch":        branch,
		"has_binary":    llamaServerBin(repo) != "",
		"needs_rebuild": behind > 0 || llamaServerBin(repo) == "",
	})
}

// handleLlamacppInstall lance le job d'installation (clone + build + BIN).
func handleLlamacppInstall(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Force bool `json:"force"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := startLcJob("install", func() { lcRunInstall(req.Force) }); err != nil {
		sendJSON(w, 409, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleLlamacppUpdate lance le job de mise à jour (pull + rebuild + restart).
// {clean:true} force une recompilation from scratch même sans nouveau commit.
func handleLlamacppUpdate(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Clean bool `json:"clean"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := startLcJob("update", func() { lcRunUpdate(req.Clean) }); err != nil {
		sendJSON(w, 409, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleLlamacppPrebuiltCheck interroge la dernière release officielle de
// llama.cpp et la compare à la version précompilée installée. Synchrone.
func handleLlamacppPrebuiltCheck(w http.ResponseWriter, r *http.Request) {
	tag, assets, err := fetchLlamaLatest()
	if err != nil {
		sendJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	main, _, label, _, err := pickPrebuilt(assets)
	if err != nil {
		sendJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	cur, _ := prebuiltVersion()
	sendJSON(w, 200, map[string]any{
		"ok":      true,
		"latest":  tag,
		"current": cur,
		"variant": label,
		"size_mb": main.Size / 1_000_000,
		"update":  cur != tag || prebuiltServerBin() == "",
	})
}

// handleLlamacppPrebuilt lance le job de téléchargement / mise à jour des
// binaires précompilés officiels (pas de compilation).
func handleLlamacppPrebuilt(w http.ResponseWriter, r *http.Request) {
	if err := startLcJob("prebuilt", lcRunPrebuilt); err != nil {
		sendJSON(w, 409, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// lcRunPrebuilt : télécharge les binaires officiels, pointe BIN dessus, en
// stoppant le service pendant le remplacement (binaire verrouillé en cours
// d'exécution) puis en le relançant.
func lcRunPrebuilt() {
	svcWasUp := serviceIsActive()
	if svcWasUp {
		lcPhase("arrêt du service le temps de l'installation…")
		if err := serviceAction("stop"); err != nil {
			lcAppend("[warn] impossible d'arrêter le service : " + err.Error())
		}
	}
	bin, err := prebuiltInstall(lcAppend, lcPhase)
	if err == nil {
		if serr := SetConfigKey("BIN", bin); serr != nil {
			err = fmt.Errorf("binaires installés mais échec écriture BIN dans config.env : %w", serr)
		} else {
			lcAppend("BIN mis à jour dans " + confPath())
		}
	}
	if svcWasUp {
		lcPhase("redémarrage du service…")
		if serr := serviceAction("start"); serr != nil {
			lcAppend("[warn] redémarrage du service échoué : " + serr.Error())
		}
	}
	if err != nil {
		lcFail(err)
		return
	}
	tag, _ := prebuiltVersion()
	lcDone("binaires précompilés installés (" + tag + ")")
}

// handleLlamacppUse bascule BIN entre deux versions DÉJÀ installées, sans
// rien recompiler : "fast" = binaires précompilés, "opt" = build local. Le
// service redémarre pour prendre le nouveau binaire. L'UI n'appelle ceci que
// quand la version cible existe déjà (sinon elle lance un job d'installation).
func handleLlamacppUse(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Mode string `json:"mode"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	var bin string
	switch req.Mode {
	case "fast":
		bin = prebuiltServerBin()
	case "opt":
		bin = llamaServerBin(llamacppRepoDir())
	default:
		sendJSON(w, 400, map[string]any{"ok": false, "error": "mode inconnu"})
		return
	}
	if bin == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "cette version n'est pas installée"})
		return
	}
	if err := SetConfigKey("BIN", bin); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if serviceIsActive() {
		_ = serviceAction("restart")
	}
	sendJSON(w, 200, map[string]any{"ok": true, "bin": bin})
}

// handleLlamacppJob renvoie l'état du job courant + les lignes de log depuis
// l'offset absolu ?from=N (le client mémorise `next` et enchaîne).
func handleLlamacppJob(w http.ResponseWriter, r *http.Request) {
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	sendJSON(w, 200, lcJobSnapshot(from, true))
}

// lcJobSnapshot construit la vue JSON du job courant. withLines=false renvoie
// juste l'entête (imbriquée dans /api/llamacpp).
func lcJobSnapshot(from int, withLines bool) map[string]any {
	lcMu.Lock()
	defer lcMu.Unlock()
	if lcCur == nil {
		return map[string]any{"exists": false}
	}
	j := lcCur
	out := map[string]any{
		"exists":     true,
		"action":     j.Action,
		"running":    j.Running,
		"phase":      j.Phase,
		"compiled":   j.Compiled,
		"error":      j.Err,
		"started_at": j.StartedAt,
		"ended_at":   j.EndedAt,
		"old":        j.OldCommit,
		"new":        j.NewCommit,
	}
	if withLines {
		start := from - j.base
		if start < 0 {
			start = 0
		}
		if start > len(j.lines) {
			start = len(j.lines)
		}
		out["lines"] = append([]string(nil), j.lines[start:]...)
		out["next"] = j.base + len(j.lines)
	}
	return out
}

// ---------------------------------------------------------------------------
// Corps des jobs (miroir web de llamacppInstall / llamacppUpdate, sans stdout
// interactif : tout passe par lcPhase / le sink de build)
// ---------------------------------------------------------------------------

func lcRunInstall(force bool) {
	repo := defaultRepoDir()

	lcPhase("vérification des outils (git, cmake, compilateur)…")
	if err := requireTools("git", "cmake"); err != nil {
		lcFail(err)
		return
	}
	if err := ensureCompiler(); err != nil {
		lcFail(err)
		return
	}
	ensureAccelerator()

	if isDir(filepath.Join(repo, ".git")) {
		if !force {
			// Dépôt déjà là : on bascule sur une mise à jour (même intention).
			lcPhase("dépôt déjà présent — bascule en mise à jour")
			lcRunUpdate(false)
			return
		}
		lcPhase("suppression du dépôt existant (--force)…")
		if err := os.RemoveAll(repo); err != nil {
			lcFail(err)
			return
		}
	}
	if err := os.MkdirAll(filepath.Dir(repo), 0o755); err != nil {
		lcFail(err)
		return
	}

	lcPhase("clone de llama.cpp…")
	if err := runStep("git clone", "", "git", "clone", "--depth=1", llamacppRepoURL, repo); err != nil {
		lcFail(fmt.Errorf("git clone a échoué : %w", err))
		return
	}

	if !lcBuildAndSwitch(repo, true) {
		return
	}
	lcDone("installation terminée")
}

func lcRunUpdate(clean bool) {
	lcPhase("vérification des outils (git, cmake, compilateur)…")
	if err := requireTools("git", "cmake"); err != nil {
		lcFail(err)
		return
	}
	if err := ensureCompiler(); err != nil {
		lcFail(err)
		return
	}
	ensureAccelerator()

	repo := llamacppRepoDir()
	if !isDir(filepath.Join(repo, ".git")) {
		lcFail(fmt.Errorf("aucun dépôt llama.cpp (%s) — lance d'abord l'installation", repo))
		return
	}

	oldCommit := gitOutput(repo, "rev-parse", "--short", "HEAD")
	lcMu.Lock()
	lcCur.OldCommit = oldCommit
	lcMu.Unlock()

	branch := gitOutput(repo, "rev-parse", "--abbrev-ref", "HEAD")
	if branch == "" || branch == "HEAD" {
		branch = "master"
	}

	lcPhase("git fetch origin…")
	if err := runStep("git fetch", repo, "git", "fetch", "origin", "--quiet"); err != nil {
		lcFail(fmt.Errorf("git fetch a échoué : %w", err))
		return
	}
	localRev := gitOutput(repo, "rev-parse", "HEAD")
	remoteRev := gitOutput(repo, "rev-parse", "origin/"+branch)
	if localRev != "" && localRev == remoteRev && !clean && llamaServerBin(repo) != "" {
		lcDone("déjà à jour (" + oldCommit + ") — rien à faire")
		return
	}
	if localRev != remoteRev {
		lcPhase("git pull origin/" + branch + "…")
		if err := runStep("git pull --ff-only", repo, "git", "pull", "--ff-only", "origin", branch); err != nil {
			lcFail(fmt.Errorf("git pull a échoué (modifs locales ?) : %w", err))
			return
		}
	}
	newCommit := gitOutput(repo, "rev-parse", "--short", "HEAD")
	lcMu.Lock()
	lcCur.NewCommit = newCommit
	lcMu.Unlock()

	// Le binaire en cours d'exécution ne peut pas être réécrit → stop du service
	// pendant le build, redémarrage après (même en échec).
	svcWasUp := serviceIsActive()
	if svcWasUp {
		lcPhase("arrêt du service le temps du build…")
		if err := serviceAction("stop"); err != nil {
			lcAppend("[warn] impossible d'arrêter le service : " + err.Error())
		}
	}

	ok := lcBuildAndSwitch(repo, clean)
	if svcWasUp {
		lcPhase("redémarrage du service…")
		if err := serviceAction("start"); err != nil {
			lcAppend("[warn] redémarrage du service échoué : " + err.Error())
		}
	}
	if !ok {
		return
	}
	if oldCommit == newCommit {
		lcDone("recompilé (" + newCommit + ")")
	} else {
		lcDone("mis à jour : " + oldCommit + " → " + newCommit)
	}
}

// lcBuildAndSwitch détecte le plan, compile llama-server et pointe BIN dessus.
// Renvoie false (job en échec) si une étape casse.
func lcBuildAndSwitch(repo string, clean bool) bool {
	plan := detectBuildPlan()
	lcAppend(fmt.Sprintf("plan de build : backend=%s arch=%s jobs=%d", plan.backend, plan.cudaArch, plan.jobs))
	lcPhase("configuration CMake…")
	if err := buildLlamacpp(repo, plan, clean); err != nil {
		lcAppendLogTail(filepath.Join(repo, "configure.log"), filepath.Join(repo, "build.log"))
		lcFail(err)
		return false
	}
	bin := llamaServerBin(repo)
	if bin == "" {
		lcFail(fmt.Errorf("build terminé mais binaire introuvable sous %s", filepath.Join(repo, "build")))
		return false
	}
	lcAppend("binaire compilé : " + bin)
	if err := SetConfigKey("BIN", bin); err != nil {
		lcFail(fmt.Errorf("build ok mais échec écriture BIN dans config.env : %w", err))
		return false
	}
	lcAppend("BIN mis à jour dans " + confPath())
	return true
}

// lcAppendLogTail remonte la fin des logs de build dans le job pour que
// l'erreur réelle soit visible dans l'UI sans aller chercher les fichiers.
func lcAppendLogTail(paths ...string) {
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
		if len(lines) > 30 {
			lines = lines[len(lines)-30:]
		}
		lcAppend("--- fin de " + filepath.Base(p) + " ---")
		for _, l := range lines {
			lcAppend(l)
		}
	}
}
