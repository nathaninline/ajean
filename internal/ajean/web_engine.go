// web_engine.go — le moteur épinglé (« Moteur AJEAN ») vu de l'interface web.
//
// Un moteur de plus dans la section Moteur, à côté du llama.cpp compilé, du
// précompilé et des forks : l'interface montre la version livrée et son état,
// et propose de l'installer, de le réparer (le retélécharger) ou d'imposer une
// variante. L'installation est un job comme les autres : elle passe par le
// suivi de web_llamacpp.go (un seul job de moteur à la fois, même journal, même
// bouton d'arrêt), donc par /api/llamacpp/job.
//
//	GET  /api/engine          → version épinglée, variante installée, usage, job
//	POST /api/engine/install  → job : installe le moteur s'il manque
//	POST /api/engine/repair   → job : le retélécharge {variant?}
package ajean

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Installation automatique
// ---------------------------------------------------------------------------

// engineAutoInstall installe le moteur épinglé en tâche de fond au démarrage de
// l'interface, mais seulement là où il sert : la configuration active tourne
// dessus (BIN vide ou dans backends/engine/), ou aucun moteur n'est installé
// (voir engineWanted). Chez quelqu'un qui a déjà son moteur de la 0.18, rien
// n'est téléchargé : le moteur AJEAN s'installe par son bouton.
func engineAutoInstall() {
	if !engineWanted() {
		return
	}
	_ = startLcJob("engine", func() { lcRunEngine(false) })
}

// lcRunEngine : corps du job. force=true = réparation (réinstalle par-dessus).
func lcRunEngine(force bool) {
	// Le binaire en cours d'exécution ne peut pas être remplacé (Windows) : si le
	// service tourne sur le moteur épinglé, on l'arrête le temps d'une
	// réparation, puis on le relance. Un service qui tourne sur un autre moteur
	// n'est pas touché.
	wasUp := force && enginePinnedRunning()
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
	// peut-être encore sur le moteur épinglé de la version précédente (le service
	// moteur n'est pas relancé par la mise à jour). On le relance sur le nouveau,
	// sinon l'interface annoncerait une version de llama.cpp qui n'est pas celle
	// en marche.
	if !force && engineRunningIsStale(info) {
		lcPhase("relance du modèle sur le nouveau moteur…")
		if err := serviceAction("restart"); err != nil {
			lcAppend("le modèle n'a pas pu être relancé (" + err.Error() + ") : redémarre le moteur à la main")
		}
	}
	lcDone("llama.cpp " + info.Tag + " prêt (" + info.Label + ")")
}

// engineRunningIsStale : le service fait-il tourner le moteur épinglé depuis un
// autre dossier que celui qu'on vient d'installer (ancien tag) ? On le lit dans
// la dernière ligne de commande lancée par « ajean serve ». Faux dès que la
// configuration active désigne un autre moteur.
func engineRunningIsStale(info *engineInfo) bool {
	if !usesPinnedEngine(ReadConfig()) || !serviceIsActive() {
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

// handleEngine renvoie l'état du moteur épinglé : version livrée avec AJEAN,
// variante installée, variante imposée, usage par la configuration active, job
// éventuel.
func handleEngine(w http.ResponseWriter, r *http.Request) {
	cfg := ReadConfig()
	out := map[string]any{
		"version":   enginePin(),
		"installed": false,
		"forced":    engineForcedVariant(),
		"job":       lcJobSnapshot(0, false),
		"bin":       engineBin(),
		// La configuration active tourne-t-elle sur ce moteur ? (BIN vide ou dans
		// backends/engine/, pour un preset qui lance llama.cpp)
		"in_use": usesPinnedEngine(cfg),
		// Dossier du moteur : un BIN de preset qui y pointe désigne ce moteur,
		// quelle que soit la version inscrite dans le chemin.
		"dir": engineRoot(),
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

// handleEngineInstall (POST) installe le moteur épinglé s'il manque. Sans effet
// (et sans job) s'il est déjà en place : pour le retélécharger, c'est repair.
func handleEngineInstall(w http.ResponseWriter, r *http.Request) {
	if e := engineInstalled(); e != nil {
		sendJSON(w, 200, map[string]any{"ok": true, "already": true})
		return
	}
	if err := startLcJob("engine", func() { lcRunEngine(false) }); err != nil {
		sendJSON(w, 409, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
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
	if err := startLcJob("engine-repair", func() { lcRunEngine(true) }); err != nil {
		sendJSON(w, 409, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}
