// web_engine.go — le moteur llama.cpp vu de l'interface web.
//
// Il n'y a rien à choisir : l'interface montre la version livrée et son état,
// suit l'installation automatique, et offre un seul geste, « Réparer »
// (retélécharger le moteur, éventuellement en imposant une variante).
//
//	GET  /api/engine             → version épinglée, variante installée, job
//	POST /api/engine/repair      → job de réinstallation {variant?}
//	GET  /api/engine/job?from=   → progression du job (phase, lignes, erreur)
//	POST /api/engine/job/cancel  → arrête l'installation en cours
//	POST /api/engine/job/dismiss → masque un job terminé
//	GET  /api/engine/cmdline     → ligne de commande du dernier lancement
package ajean

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// lcJob est LE job du moteur en cours (un seul à la fois). Les lignes de log
// sont indexées de façon absolue (base = index de lines[0]) pour que le client
// puisse poller « à partir de N » même quand on tronque le début.
type lcJob struct {
	Action    string // "engine" (installation automatique) | "repair"
	Phase     string
	Running   bool
	Err       string
	StartedAt int64
	EndedAt   int64
	lines     []string
	base      int
}

var (
	lcMu  sync.Mutex
	lcCur *lcJob
)

const lcMaxLines = 4000

// lcAppend ajoute une ligne au log du job courant.
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
	lcSaveLine(line)
	lcSave(false)
}

// lcPhase pose la phase affichée à côté du spinner et la trace dans le log.
func lcPhase(phase string) {
	lcMu.Lock()
	if lcCur != nil {
		lcCur.Phase = phase
		lcCur.lines = append(lcCur.lines, "▶ "+phase)
		lcSaveLine("▶ " + phase)
		lcSave(true)
	}
	lcMu.Unlock()
}

// startLcJob démarre un job si aucun n'est en cours.
func startLcJob(action string, run func()) error {
	lcMu.Lock()
	defer lcMu.Unlock()
	if lcCur != nil && lcCur.Running {
		return fmt.Errorf("une installation du moteur est déjà en cours")
	}
	job := &lcJob{Action: action, Running: true, Phase: "démarrage…", StartedAt: time.Now().Unix()}
	lcCur = job
	lcResetLog()
	lcSave(true)
	go func() {
		defer func() {
			lcMu.Lock()
			job.Running = false
			job.EndedAt = time.Now().Unix()
			lcSave(true)
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
		lcSaveLine("✗ " + err.Error())
		lcSave(true)
	}
	lcMu.Unlock()
}

func lcDone(msg string) {
	lcMu.Lock()
	if lcCur != nil {
		lcCur.Phase = msg
		lcCur.lines = append(lcCur.lines, "✓ "+msg)
		lcSaveLine("✓ " + msg)
		lcSave(true)
	}
	lcMu.Unlock()
}

// lcJobSnapshot construit la vue JSON du job courant. withLines=false renvoie
// juste l'entête (imbriquée dans /api/engine).
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
		"error":      j.Err,
		"started_at": j.StartedAt,
		"ended_at":   j.EndedAt,
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
// Installation automatique
// ---------------------------------------------------------------------------

// engineAutoInstall installe le moteur en tâche de fond au démarrage de
// l'interface s'il manque : première installation d'AJEAN, ou mise à jour qui
// change la version épinglée. L'utilisateur n'a rien à lancer ; il voit
// seulement la progression. Sans effet si le moteur est déjà là.
func engineAutoInstall() {
	if engineDevBin() != "" || engineInstalled() != nil {
		return
	}
	_ = startLcJob("engine", func() { lcRunEngine(false) })
}

// lcRunEngine : corps du job. force=true = réparation (réinstalle par-dessus).
func lcRunEngine(force bool) {
	// Le binaire en cours d'exécution ne peut pas être remplacé (Windows) : on
	// arrête le service le temps d'une réparation, puis on le relance.
	wasUp := force && engineInstalled() != nil && serviceIsActive()
	if wasUp {
		lcPhase("arrêt du moteur le temps de la réinstallation…")
		_ = serviceAction("stop")
	}
	info, err := engineInstall(force, lcAppend, lcPhase)
	if wasUp {
		lcPhase("redémarrage du moteur…")
		if serr := serviceAction("start"); serr != nil && err == nil {
			err = fmt.Errorf("moteur réinstallé, mais le service n'a pas redémarré : %w", serr)
		}
	}
	if err != nil {
		lcFail(err)
		return
	}
	devCacheReset() // les cartes vues par le moteur dépendent de la variante
	// Installation automatique après une mise à jour d'AJEAN : le modèle tourne
	// peut-être encore sur le moteur de la version précédente (le service moteur
	// n'est pas relancé par la mise à jour). On le relance sur le nouveau, sinon
	// l'interface annoncerait une version de llama.cpp qui n'est pas celle en marche.
	if !force && engineRunningIsStale(info) {
		lcPhase("relance du modèle sur le nouveau moteur…")
		if err := serviceAction("restart"); err != nil {
			lcAppend("le modèle n'a pas pu être relancé (" + err.Error() + ") : redémarre le moteur à la main")
		}
	}
	lcDone("llama.cpp " + info.Tag + " prêt (" + info.Label + ")")
}

// engineRunningIsStale : un llama-server local tourne-t-il depuis un autre
// dossier que celui du moteur qu'on vient d'installer ? On le lit dans la
// dernière ligne de commande lancée par « ajean serve ».
func engineRunningIsStale(info *engineInfo) bool {
	cfg := ReadConfig()
	if isExternalConfig(cfg) || removedEngine(cfg) != "" || !serviceIsActive() {
		return false
	}
	return engineCmdlineIsStale(string(getBytes(bkState, engineCmdlineKey)), info.dir)
}

func engineCmdlineIsStale(cmdline, engineDir string) bool {
	if strings.TrimSpace(cmdline) == "" {
		return false // rien de connu : on ne touche pas à un modèle qui tourne
	}
	norm := func(s string) string { return strings.ToLower(strings.ReplaceAll(s, `\`, "/")) }
	return !strings.Contains(norm(cmdline), norm(engineDir))
}

// ---------------------------------------------------------------------------
// Handlers
// ---------------------------------------------------------------------------

// handleEngine renvoie l'état du moteur : version livrée avec AJEAN, variante
// installée, variante imposée, restes d'anciennes versions, job éventuel.
func handleEngine(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"version":   enginePin(),
		"installed": false,
		"forced":    engineForcedVariant(),
		"legacy":    engineLegacyDirs(),
		"job":       lcJobSnapshot(0, false),
		"bin":       engineBin(),
	}
	if e := engineInstalled(); e != nil {
		out["installed"] = true
		out["variant"] = e.Variant
		out["label"] = e.Label
		out["installed_at"] = e.InstalledAt
	}
	if p := engineDevBin(); p != "" {
		out["dev_bin"] = p
	}
	out["variants"] = engineVariantChoices()
	sendJSON(w, 200, out)
}

// engineVariantChoices : les variantes qui existent pour cette machine, dans
// l'ordre où AJEAN les essaie (sans doublon). L'interface ne propose d'en
// imposer une que s'il y en a plusieurs. La détection du matériel lance
// nvidia-smi : on la garde dix minutes, /api/engine est sondé souvent.
var (
	engineChoicesMu sync.Mutex
	engineChoices   []string
	engineChoicesAt time.Time
)

func engineVariantChoices() []string {
	engineChoicesMu.Lock()
	defer engineChoicesMu.Unlock()
	if engineChoices != nil && time.Since(engineChoicesAt) < 10*time.Minute {
		return engineChoices
	}
	out := []string{}
	if m, err := engineAssets(); err == nil {
		if cands, err := engineCandidates(m.Assets, engineMachine(), ""); err == nil {
			seen := map[string]bool{}
			for _, c := range cands {
				if !seen[c.ID] {
					seen[c.ID] = true
					out = append(out, c.ID)
				}
			}
		}
	}
	engineChoices, engineChoicesAt = out, time.Now()
	return out
}

// handleEngineRepair (POST) relance l'installation du moteur épinglé.
// {variant: "auto"|"cuda"|"rocm"|"vulkan"|"cpu"} change au passage la variante
// imposée sur cette machine ; sans ce champ, elle est conservée.
func handleEngineRepair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Variant *string `json:"variant"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if req.Variant != nil {
		if err := setEngineForcedVariant(*req.Variant); err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
			return
		}
	}
	if err := startLcJob("repair", func() { lcRunEngine(true) }); err != nil {
		sendJSON(w, 409, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleEngineJob renvoie l'état du job courant + les lignes de log depuis
// l'offset absolu ?from=N (le client mémorise `next` et enchaîne).
func handleEngineJob(w http.ResponseWriter, r *http.Request) {
	from, _ := strconv.Atoi(r.URL.Query().Get("from"))
	sendJSON(w, 200, lcJobSnapshot(from, true))
}

// handleEngineJobDismiss (POST) efface un job TERMINÉ pour que son résultat
// (surtout une erreur en rouge) cesse d'être réaffiché à chaque démarrage.
func handleEngineJobDismiss(w http.ResponseWriter, r *http.Request) {
	if lcDismiss() {
		sendJSON(w, 200, map[string]any{"ok": true})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": false, "error": "opération en cours — impossible de masquer"})
}

// handleEngineJobCancel (POST) arrête l'installation en cours (téléchargement).
func handleEngineJobCancel(w http.ResponseWriter, r *http.Request) {
	lcMu.Lock()
	running := lcCur != nil && lcCur.Running
	lcMu.Unlock()
	if !running {
		sendJSON(w, 200, map[string]any{"ok": false, "error": "aucune installation en cours"})
		return
	}
	if engineCancelInstall() {
		lcPhase("arrêt demandé…")
		sendJSON(w, 200, map[string]any{"ok": true})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": false, "error": "étape non interruptible pour l'instant — réessaie dans un instant"})
}

// uiBuildID : empreinte de l'interface embarquée dans ce binaire. Deux builds
// peuvent porter le même numéro de version (préversion republiée, build de
// test) : la page compare cette empreinte, pas seulement la version, pour savoir
// qu'elle doit se recharger après un remplacement d'AJEAN.
var (
	uiBuildOnce sync.Once
	uiBuildVal  string
)

func uiBuildID() string {
	uiBuildOnce.Do(func() {
		b, _ := uiFS.ReadFile("ui/index.html")
		sum := sha256.Sum256(b)
		uiBuildVal = hex.EncodeToString(sum[:6])
	})
	return uiBuildVal
}
