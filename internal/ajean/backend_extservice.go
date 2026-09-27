package ajean

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

// backend_extservice.go — un preset externe peut nommer le service système qui
// sert son API (clé EXTERNAL_SERVICE, ex. « ajean-strata » : un moteur tiers
// comme Strata, lancé par sa propre unité systemd). ajean le démarre quand on
// bascule sur ce preset et l'arrête quand on en repart, pour qu'un seul moteur
// occupe les GPU à la fois. L'unité déclare en plus Conflicts=ajean-engine :
// systemd arrête l'un quand l'autre démarre, quel que soit l'ordre des appels.

const extKeyService = "EXTERNAL_SERVICE"

// stateExtService : dans bkState, le service externe qu'ajean a démarré en
// dernier (pour l'arrêter à la bascule suivante, même après un redémarrage).
const stateExtService = "ext_service"

// Le nom est lu dans un preset, modifiable à distance : on n'accepte que des
// unités « ajean-… », et sudoers ne doit autoriser que celles-là.
var extServiceRe = regexp.MustCompile(`^ajean-[a-z0-9][a-z0-9-]{0,40}$`)

// externalServiceOf renvoie le service externe que la config demande ("" = aucun).
func externalServiceOf(cfg map[string]string) string {
	if !isExternalConfig(cfg) {
		return ""
	}
	return strings.TrimSpace(cfg[extKeyService])
}

func validExternalService(name string) bool { return extServiceRe.MatchString(name) }

// extUnitAction et extWaitGPUs sont remplaçables par les tests (aucun vrai
// systemctl ni nvidia-smi).
var (
	extUnitAction = unitAction
	extWaitGPUs   = waitGPUsReleased
	extUnitPIDs   = unitPIDs
	extUnitState  = unitActiveState
)

// syncExternalService aligne les services externes sur la config active :
// arrête celui démarré pour un preset précédent, démarre celui du preset
// actuel. Sans effet pour un preset qui n'en déclare pas.
func syncExternalService(cfg map[string]string) error {
	want := externalServiceOf(cfg)
	if want != "" && !validExternalService(want) {
		return fmt.Errorf("%s invalide : %q (attendu : ajean-<nom>)", extKeyService, want)
	}
	prev := getStr(bkState, stateExtService)
	if prev != "" && prev != want && validExternalService(prev) {
		pids := extUnitPIDs(prev) // relevés AVANT l'arrêt : après, le cgroup est vide
		if err := extUnitAction(prev, "stop"); err != nil {
			return fmt.Errorf("arrêt de %s : %w", prev, err)
		}
		_ = putStr(bkState, stateExtService, "")
		// le moteur suivant ne doit pas démarrer avant que la VRAM soit rendue
		extWaitGPUs(pids, 30*time.Second)
	}
	if want == "" {
		return nil
	}
	if err := extUnitAction(want, "start"); err != nil {
		return fmt.Errorf("démarrage de %s : %w", want, err)
	}
	return putStr(bkState, stateExtService, want)
}

// extHealthClient : sondes courtes, appelées à chaque rafraîchissement de /status.
var extHealthClient = &http.Client{Timeout: 1500 * time.Millisecond}

// externalServiceReady dit si le moteur tiers du preset est prêt à répondre :
// son unité tourne ET son API répond (GET <racine>/health, ou /v1/models pour
// un serveur sans /health). Le second retour explique un échec franc (unité
// tombée), vide pendant un simple chargement.
func externalServiceReady(cfg map[string]string) (bool, string) {
	svc := externalServiceOf(cfg)
	if svc == "" {
		return true, ""
	}
	switch extUnitState(svc) {
	case "active":
	case "activating", "reloading":
		return false, ""
	case "failed":
		return false, svc + " s'est arrêté en erreur (journalctl -u " + svc + ")"
	default:
		return false, "" // pas (encore) démarré : la bascule le lance
	}
	api := strings.TrimSuffix(completionsURL(cfg[extKeyURL]), "/chat/completions")
	root := strings.TrimSuffix(api, "/v1")
	for _, u := range []string{root + "/health", api + "/models"} {
		resp, err := extHealthClient.Get(u)
		if err != nil {
			return false, "" // le port n'écoute pas encore : chargement
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return true, ""
		}
	}
	return false, ""
}
