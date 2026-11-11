package ajean

// Conversion des presets en services (refonte 0.19). Chaque preset devient un
// service qui lance EXACTEMENT la commande qu'AJEAN construisait (même
// moteur, mêmes arguments, mêmes variables). Les fichiers de presets ne sont
// ni modifiés ni supprimés. Faite une seule fois automatiquement (marqueur
// services-converted.json), relançable à la main : `ajean service convert`.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

type convertResult struct {
	Preset  string `json:"preset"`
	Name    string `json:"name"`
	Service string `json:"service,omitempty"`
	Kind    string `json:"kind,omitempty"` // exec, remote
	Note    string `json:"note,omitempty"` // ce que la conversion n'a pas repris
	Status  string `json:"status"`         // ok, exists, skipped, error
	Detail  string `json:"detail,omitempty"`
}

type convertReport struct {
	Results []convertResult `json:"results"`
	Seen    bool            `json:"seen"`
}

func convertMarkerPath() string { return filepath.Join(AjeanHome(), "services-converted.json") }

var (
	slugRe    = regexp.MustCompile(`[^a-z0-9]+`)
	envMu     sync.Mutex
	convertMu sync.Mutex
)

func serviceSlug(s string) string {
	s = strings.ToLower(s)
	r := strings.NewReplacer("à", "a", "â", "a", "ä", "a", "é", "e", "è", "e", "ê", "e", "ë", "e",
		"î", "i", "ï", "i", "ô", "o", "ö", "o", "ù", "u", "û", "u", "ü", "u", "ç", "c")
	s = strings.Trim(slugRe.ReplaceAllString(r.Replace(s), "-"), "-")
	if len(s) > 40 {
		s = strings.Trim(s[:40], "-")
	}
	if s == "" {
		s = "modele"
	}
	return s
}

// captureLaunch exécute f puis relève les variables d'environnement qu'il a
// posées, et remet l'environnement du process dans son état d'origine.
func captureLaunch(f func() (launchSpec, error)) (launchSpec, error) {
	envMu.Lock()
	defer envMu.Unlock()
	before := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			before[k] = v
		}
	}
	wd, _ := os.Getwd()
	spec, err := f()
	after := map[string]string{}
	for _, kv := range os.Environ() {
		if k, v, ok := strings.Cut(kv, "="); ok {
			after[k] = v
		}
	}
	if spec.Env == nil {
		spec.Env = map[string]string{}
	}
	for k, v := range after {
		if old, ok := before[k]; !ok || old != v {
			spec.Env[k] = v
			if ok {
				_ = os.Setenv(k, old)
			} else {
				_ = os.Unsetenv(k)
			}
		}
	}
	for k, v := range before {
		if _, ok := after[k]; !ok {
			_ = os.Setenv(k, v)
		}
	}
	if wd != "" {
		_ = os.Chdir(wd)
	}
	return spec, err
}

// serviceFromPreset construit le service d'un preset (sans l'enregistrer). Il
// rend aussi sa sorte (exec, remote) et une note sur ce qui n'a pas été repris.
func serviceFromPreset(p Preset) (Service, string, string, error) {
	content, err := ReadPreset(p.ID)
	if err != nil {
		return Service{}, "", "", err
	}
	cfg := presetEffectiveConfig(content)
	s := Service{ID: serviceSlug(p.ID), Name: p.Name, Preset: p.ID, ChatParams: chatParamsFromConfig(cfg)}
	if v, err := strconv.Atoi(strings.TrimSpace(cfg["CTX"])); err == nil && v > 0 {
		s.Context = v
	}
	s.Quant, s.Reasoning = presetTags(content, cfg)
	if isCloudConfig(cfg) {
		return Service{}, "", "", fmt.Errorf("GPU cloud Modal : n'existe plus, à recréer en API distante si besoin")
	}
	if isExternalConfig(cfg) {
		// Même règle que completionsURL : une base qui ne finit pas par /v1 reçoit /v1.
		raw := strings.TrimRight(strings.TrimSpace(cfg[extKeyURL]), "/")
		if strings.HasSuffix(raw, "/chat/completions") {
			s.URL = strings.TrimSuffix(raw, "/chat/completions")
		} else if strings.HasSuffix(raw, "/v1") {
			s.URL = raw
		} else {
			s.URL = raw + "/v1"
		}
		s.Model = strings.TrimSpace(cfg[extKeyModel])
		s.APIKey = strings.TrimSpace(cfg[extKeyToken])
		s.Vision = strings.TrimSpace(cfg[extKeyVision]) == "1"
		if u := externalServiceOf(cfg); u != "" {
			// AJEAN ne pilote que les unités qu'il écrit : le modèle reste
			// joignable, mais ce service n'est plus démarré ni arrêté d'ici.
			return s, "remote", "le service système " + u + " n'est plus démarré par AJEAN : recréer ce modèle « Sur cette machine » avec sa ligne de commande", nil
		}
		return s, "remote", "", nil
	}
	var launch func(map[string]string) (launchSpec, error)
	switch {
	case isMoeConfig(cfg):
		return Service{}, "", "", fmt.Errorf("moteur MoE intégré : à recréer comme modèle « Sur cette machine » (voir docs/moteurs.md)")
	case isExl3Config(cfg):
		launch = exl3Launch
		s.Vision = cfg["EXL3_VISION"] == "1"
	default:
		launch = llamaLaunch
		s.Vision = strings.TrimSpace(cfg["MMPROJ"]) != ""
	}
	spec, err := captureLaunch(func() (launchSpec, error) { return launch(cfg) })
	if err != nil {
		return Service{}, "", "", err
	}
	// Fichiers de config partagés entre presets (TabbyAPI, MoE) : copie propre
	// au service, pour qu'un autre preset ne l'écrase pas.
	own := filepath.Join(AjeanHome(), "services", s.ID)
	for i, a := range spec.Args {
		if (strings.HasSuffix(a, ".yml") || strings.HasSuffix(a, ".json")) && svcIsFile(a) &&
			strings.HasPrefix(a, AjeanHome()) {
			b, err := os.ReadFile(a)
			if err != nil {
				return Service{}, "", "", err
			}
			if err := os.MkdirAll(own, 0o755); err != nil {
				return Service{}, "", "", err
			}
			dst := filepath.Join(own, filepath.Base(a))
			if err := os.WriteFile(dst, b, 0o600); err != nil {
				return Service{}, "", "", err
			}
			spec.Args[i] = dst
		}
	}
	s.Exec, s.Args, s.Workdir, s.Env = spec.Bin, spec.Args, spec.Dir, spec.Env
	if s.Context == 0 && !isMoeConfig(cfg) && !isExl3Config(cfg) {
		s.Context = 32768 // défaut de llama-server quand CTX est absent
	}
	port := spec.Port
	if port == "" || port == "<nil>" {
		port = "8080"
	}
	s.URL = "http://127.0.0.1:" + port + "/v1"
	s.APIKey = readAPIKey()
	if s.APIKey == "" {
		s.APIKey = strings.TrimSpace(cfg["API_KEY"])
	}
	// Avant, un seul preset tournait à la fois : on garde ce comportement.
	s.Exclusive = true
	return s, "exec", "", nil
}

// ConvertPresets crée un service par preset. Un service du même id déjà
// présent n'est pas touché. dry = ne rien enregistrer.
func ConvertPresets(dry bool) ([]convertResult, error) {
	convertMu.Lock()
	defer convertMu.Unlock()
	presets, err := ListPresets()
	if err != nil {
		return nil, err
	}
	var out []convertResult
	for _, p := range presets {
		r := convertResult{Preset: p.ID, Name: p.Name}
		s, kind, note, err := serviceFromPreset(p)
		switch {
		case err != nil:
			r.Status, r.Detail = "error", err.Error()
		default:
			r.Service, r.Kind, r.Note = s.ID, kind, note
			if _, e := GetService(s.ID); e == nil {
				r.Status, r.Detail = "exists", "un service de ce nom existe déjà"
			} else if dry {
				r.Status = "ok"
			} else if e := PutService(s); e != nil {
				r.Status, r.Detail = "error", e.Error()
			} else {
				r.Status = "ok"
				if p.Active && ChatServiceID() == "" {
					r.Detail = "preset actif"
				}
			}
		}
		out = append(out, r)
	}
	if !dry {
		_ = writeJSONAtomic(convertMarkerPath(), convertReport{Results: out})
	}
	return out, nil
}

// legacyAutostartKey : l'ancien moteur (0.18) démarrait avec la machine.
const legacyAutostartKey = "legacy_engine_autostart"

// legacyAutostartLoop reporte ce démarrage automatique sur le modèle du chat
// converti. Sous Linux il faut l'assistant root : on réessaie jusqu'à ce que
// l'utilisateur l'ait installé (toutes les 20 s, sans bruit).
func legacyAutostartLoop() {
	for getStr(bkState, legacyAutostartKey) == "1" {
		if id := ChatServiceID(); id != "" {
			s, err := GetService(id)
			if err != nil || !s.Local() {
				_ = putStr(bkState, legacyAutostartKey, "")
				return
			}
			if err := SetServiceAutostart(id, true); err == nil {
				_ = putStr(bkState, legacyAutostartKey, "")
				// Le modèle tournait avant la mise à jour : on le relance tout de
				// suite, sans attendre le prochain redémarrage de la machine.
				if osSvcStateOf(&s) != "active" {
					if err := StartService(id, StartOptions{}); err != nil {
						fmt.Fprintf(os.Stderr, "[ajean] démarrage de %s : %v\n", id, err)
					}
				}
				fmt.Fprintf(os.Stderr, "[ajean] %s démarre avec la machine, comme l'ancien moteur\n", id)
				return
			}
		}
		time.Sleep(20 * time.Second)
	}
}

// AutoConvertPresets : une fois, au démarrage du serveur web.
func AutoConvertPresets() {
	defer adoptChatService()
	if svcIsFile(convertMarkerPath()) {
		backfillServicesFromPresets()
		return
	}
	if p, _ := ListPresets(); len(p) == 0 {
		_ = writeJSONAtomic(convertMarkerPath(), convertReport{Seen: true})
		return
	}
	if _, err := ConvertPresets(false); err != nil {
		fmt.Fprintf(os.Stderr, "[ajean] conversion des presets : %v\n", err)
	}
}

func printConvert(res []convertResult, dry bool) {
	for _, r := range res {
		mark := map[string]string{"ok": green("[ok]"), "exists": "[=]", "error": "[err]", "skipped": "[-]"}[r.Status]
		line := fmt.Sprintf("%s %s", mark, r.Name)
		if r.Service != "" {
			line += " → " + r.Service + " (" + r.Kind + ")"
		}
		if r.Detail != "" {
			line += " : " + r.Detail
		}
		fmt.Println(line)
		if r.Note != "" {
			fmt.Println("     " + r.Note)
		}
	}
	if dry {
		fmt.Println("(essai à blanc : rien n'a été enregistré)")
	}
}

func handleServiceConvert(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		var rep convertReport
		if b, err := os.ReadFile(convertMarkerPath()); err == nil {
			_ = json.Unmarshal(b, &rep)
		}
		if rep.Results == nil {
			rep.Results = []convertResult{}
		}
		svcJSON(w, 200, rep)
	case http.MethodPost:
		if r.URL.Query().Get("seen") == "1" {
			var rep convertReport
			if b, err := os.ReadFile(convertMarkerPath()); err == nil {
				_ = json.Unmarshal(b, &rep)
			}
			rep.Seen = true
			_ = writeJSONAtomic(convertMarkerPath(), rep)
			svcJSON(w, 200, map[string]any{"ok": true})
			return
		}
		res, err := ConvertPresets(false)
		if err != nil {
			svcErr(w, err)
			return
		}
		svcJSON(w, 200, map[string]any{"ok": true, "results": res})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// presetTags : étiquettes quant / réflexion d'un preset, comme l'ancienne liste.
func presetTags(content string, cfg map[string]string) (quant, reasoning string) {
	switch {
	case isExternalConfig(cfg), isCloudConfig(cfg), isMoeConfig(cfg):
	case isExl3Config(cfg):
		quant = exl3QuantLabel(cfg)
	default:
		quant = detectQuant(content)
	}
	if r := presetReasoning(content); reasoningActive(r) && !isExternalConfig(cfg) {
		reasoning = strings.ToLower(r)
	}
	return
}

// adoptChatService : le chat qui tournait encore sur un preset passe sur le
// modèle issu de ce preset (même point d'accès), une fois pour toutes.
func adoptChatService() {
	if strings.TrimSpace(ReadConfig()[cfgKeyService]) != "" {
		return
	}
	if id := ChatServiceID(); id != "" {
		if err := UseServiceForChat(id); err != nil {
			fmt.Fprintf(os.Stderr, "[ajean] bascule du chat sur %s : %v\n", id, err)
		}
	}
}
