package ajean

// Service du chat : celui qui répond aux conversations. Le choisir écrit la
// config vivante sous la forme d'un point d'accès externe (EXTERNAL=1, URL,
// modèle, clé, contexte, vision) : tout le reste du code (chat, vision,
// compactage, jauge de contexte) le suit sans changement.

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const cfgKeyService = "AJEAN_SERVICE"

// ChatServiceID : id du service utilisé par le chat ("" = aucun).
// Tant que le chat n'a pas été basculé sur un service, c'est le modèle issu du
// preset actif (conversion) qui l'est en pratique.
func ChatServiceID() string {
	if id := strings.TrimSpace(ReadConfig()[cfgKeyService]); id != "" {
		return id
	}
	presets, _ := ListPresets()
	for _, p := range presets {
		if p.Active {
			if _, err := GetService(serviceSlug(p.ID)); err == nil {
				return serviceSlug(p.ID)
			}
		}
	}
	return ""
}

// UseServiceForChat bascule le chat sur ce service (sans le démarrer).
func UseServiceForChat(id string) error {
	s, err := GetService(id)
	if err != nil {
		return err
	}
	model := strings.TrimSpace(s.Model)
	if model == "" {
		model = firstModelID(&s)
	}
	// On garde les réglages de la machine (mémoire, compactage, Jean, réseau…) et
	// on remplace tout ce qui décrivait le modèle précédent.
	cfg := map[string]string{}
	for k, v := range ReadConfig() {
		if !isModelConfigKey(k) {
			cfg[k] = v
		}
	}
	cfg[cfgKeyService] = s.ID
	cfg[extKeyFlag] = "1"
	cfg[extKeyURL] = s.URL
	cfg[extKeyModel] = model
	if s.APIKey != "" {
		cfg[extKeyToken] = s.APIKey
	}
	if s.Context > 0 {
		cfg["CTX"] = strconv.Itoa(s.Context)
	}
	if s.Vision {
		cfg[extKeyVision] = "1"
	}
	if s.Managed() {
		cfg[cfgKeyServiceLocal] = "1"
	}
	for k, v := range s.ChatParams {
		cfg[k] = v
	}
	if err := WriteConfig(cfg); err != nil {
		return err
	}
	// Le preset chargé avant n'est plus actif (marqueur de ListPresets).
	return putStr(bkState, "active_preset", "")
}

// firstModelID : premier modèle annoncé par /models (repli « ajean »).
func firstModelID(s *Service) string {
	req, err := http.NewRequest("GET", s.URL+"/models", nil)
	if err != nil {
		return "ajean"
	}
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		return "ajean"
	}
	defer resp.Body.Close()
	var body struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if json.NewDecoder(resp.Body).Decode(&body) != nil || len(body.Data) == 0 || body.Data[0].ID == "" {
		return "ajean"
	}
	return body.Data[0].ID
}

// StartAndUse : démarre si besoin, puis branche le chat dessus.
func StartAndUse(id string, opt StartOptions) error {
	if err := StartService(id, opt); err != nil {
		return err
	}
	if err := UseServiceForChat(id); err != nil {
		return fmt.Errorf("service prêt mais bascule du chat impossible : %w", err)
	}
	if opt.Progress != nil {
		opt.Progress("utilisé par le chat")
	}
	return nil
}

// isModelConfigKey : clé qui décrit le modèle (ou l'ancien moteur) et non la
// machine. Remplacées à chaque bascule du chat.
func isModelConfigKey(k string) bool {
	switch k {
	case cfgKeyService, cfgKeyServiceLocal, "CTX", "MMPROJ", "MODEL", "MODEL_DRAFT", "BIN", "ENGINE", "PORT",
		"REASONING", "REASONING_BUDGET", "EXTRA_ARGS", "NGL", "BATCH", "UBATCH", "THREADS", "THREADS_BATCH",
		"KV_TYPE", "KV_TYPE_K", "KV_TYPE_V", "NP", "QUANT":
		return true
	}
	for _, p := range chatParamKeys {
		if k == p {
			return true
		}
	}
	for _, p := range []string{"EXTERNAL", "EXL3_", "MOE_", "CLOUD"} {
		if strings.HasPrefix(k, p) {
			return true
		}
	}
	return false
}

// chatModelKey : la clé de l'API du modèle du chat ("" = aucune).
func chatModelKey() string {
	if sv := explicitChatService(); sv != nil {
		return sv.APIKey
	}
	cfg := ReadConfig()
	if isExternalConfig(cfg) {
		return strings.TrimSpace(cfg[extKeyToken])
	}
	return readAPIKey()
}

// chatModelAPI : adresse, clé et nom de modèle de l'API du modèle du chat, tels
// qu'un client OpenAI doit les utiliser (panneau API). Un modèle local qui écoute
// sur toutes les interfaces (--host 0.0.0.0) est annoncé à l'adresse de la
// machine sur le réseau local.
func chatModelAPI() (base, key, model string) {
	if sv := explicitChatService(); sv != nil {
		base, key, model = strings.TrimSpace(sv.URL), sv.APIKey, strings.TrimSpace(sv.Model)
		if p := localPort(sv); p != "" && listensOnLAN(sv) {
			base = "http://" + localIP() + ":" + p + "/v1"
		}
		if model == "" {
			model = strings.TrimSpace(ReadConfig()[extKeyModel])
		}
		if model == "" {
			model = "ajean"
		}
		return base, key, model
	}
	cfg := ReadConfig()
	if isExternalConfig(cfg) {
		return strings.TrimSpace(cfg[extKeyURL]), strings.TrimSpace(cfg[extKeyToken]), strings.TrimSpace(cfg[extKeyModel])
	}
	return fmt.Sprintf("http://%s:%d/v1", localIP(), LLMPort()), readAPIKey(), "ajean"
}

// listensOnLAN : la commande du modèle écoute sur toutes les interfaces.
func listensOnLAN(sv *Service) bool {
	return strings.Contains(sv.commandLine(), "0.0.0.0")
}

// chatModelLoadError : pourquoi le modèle du chat n'est pas prêt, sur une ligne
// ("" s'il n'y a rien à signaler).
func chatModelLoadError() string {
	sv := explicitChatService()
	if sv == nil {
		return ""
	}
	return serviceLoadError(sv)
}

// serviceLoadError : l'erreur du dernier démarrage, sinon une intervention du
// gardien RAM.
func serviceLoadError(s *Service) string {
	if e := strings.SplitN(loadServiceState()[s.ID].LastError, "\n", 2)[0]; e != "" {
		return e
	}
	return guardNote(s)
}

// localModelProxy : reverse-proxy vers l'API locale du modèle du chat (endpoint
// OpenAI du relais et accès public). Le port est relu à chaque requête : le
// modèle du chat peut changer sans redémarrage. Le modèle se voit appelé sous
// son propre nom, certains refusent (403) un Host inconnu (protection DNS
// rebinding). withKey injecte la clé du modèle, que le client distant n'a pas.
func localModelProxy(withKey bool) *httputil.ReverseProxy {
	lp := httputil.NewSingleHostReverseProxy(&url.URL{Scheme: "http", Host: "127.0.0.1"})
	lp.FlushInterval = -1 // streaming SSE des complétions
	base := lp.Director
	lp.Director = func(req *http.Request) {
		base(req)
		host := fmt.Sprintf("127.0.0.1:%d", LLMPort())
		req.URL.Host = host
		req.Host = host
		if withKey {
			if k := chatModelKey(); k != "" {
				req.Header.Set("Authorization", "Bearer "+k)
			}
		}
	}
	lp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		http.Error(w, "modèle injoignable : "+e.Error(), http.StatusBadGateway)
	}
	return lp
}
