package ajean

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// backend_service.go — presets « Service système ».
//
// Un preset tourne de l'une de ces façons :
//
//	Local            un llama.cpp (moteur AJEAN, compilé, précompilé, fork)
//	                 charge un .gguf (MODEL=… ; BIN désigne le moteur)
//	API              une API OpenAI-compatible distante (EXTERNAL=1)
//	Service système  une COMMANDE au choix de l'utilisateur (vLLM, un autre
//	                 llama-server, TabbyAPI, un script…) qui expose une API
//	                 OpenAI sur cette machine (EXTERNAL=1 + SERVICE_CMD=…)
//	ExLlama, MoE,    les moteurs spécialisés de la 0.18 (ENGINE=exl3, ENGINE=moe,
//	GPU cloud        CLOUD=modal), inchangés
//
// À ne pas confondre avec le preset externe à unité système de la 0.18
// (EXTERNAL_SERVICE=ajean-…, backend_extservice.go), qui démarre une unité
// systemd tierce À CÔTÉ du service moteur : il reste tel quel.
//
// Le service système reprend l'idée de la 0.19 (une ligne de commande, lancée
// comme service, dont AJEAN utilise l'API) sans sa machinerie : la commande est
// lancée par le service moteur d'AJEAN (ajean-engine → « ajean serve »), à la
// place de llama-server. Démarrage avec la machine, relance en cas d'échec,
// journal, boutons démarrer / arrêter : tout ce que ce service offre déjà sur
// chaque système s'applique tel quel, et il n'y a toujours qu'un modèle en
// mémoire à la fois. Côté chat, c'est une API comme une autre (EXTERNAL_URL,
// EXTERNAL_MODEL, EXTERNAL_KEY), sauf qu'elle est locale : AJEAN attend qu'elle
// réponde avant d'annoncer le modèle prêt.
const (
	svcKeyCmd     = "SERVICE_CMD"     // la commande, sur une ligne, comme dans un terminal
	svcKeyWorkdir = "SERVICE_WORKDIR" // dossier de travail (vide = AJEAN_HOME)
	svcKeyEnv     = "SERVICE_ENV"     // variables d'environnement : CLE=valeur CLE2="val eur"
)

// serviceCmdOf renvoie la commande du preset « Service système » ("" sinon).
func serviceCmdOf(cfg map[string]string) string {
	if !isExternalConfig(cfg) {
		return ""
	}
	return strings.TrimSpace(cfg[svcKeyCmd])
}

// presetServiceAction adapte une action du service moteur au preset actif.
// Elle COMPOSE avec la règle de la 0.18 (cloudServiceAction) au lieu de la
// remplacer :
//
//	Service système  la commande du preset tourne dans le service moteur :
//	                 démarrer et redémarrer gardent leur sens
//	API              rien à lancer : démarrer revient à arrêter (0.18)
//	GPU cloud        (re)déploiement, puis le service lance le relais (0.18)
//	Local, ExLlama,  action inchangée (0.18)
//	MoE
//
// Un preset Service est « externe » pour le chat (EXTERNAL=1) : sans ce
// premier test, cloudServiceAction l'arrêterait comme une simple API.
func presetServiceAction(action string) string {
	if action == "start" || action == "restart" {
		// GPU cloud d'abord, comme partout (resolveChatEndpoint, cmdServe).
		if cfg := ReadConfig(); !isCloudConfig(cfg) && serviceCmdOf(cfg) != "" {
			return action
		}
	}
	return cloudServiceAction(action)
}

var rePortFlag = regexp.MustCompile(`^(?:--port|-p)(?:=(\d+))?$`)

// portFromCommand lit le port d'écoute dans la commande (--port N, --port=N,
// -p N). "" si elle n'en donne pas.
func portFromCommand(cmd string) string {
	toks := splitArgs(cmd)
	for i, a := range toks {
		m := rePortFlag.FindStringSubmatch(a)
		if m == nil {
			continue
		}
		if m[1] != "" {
			return m[1]
		}
		if i+1 < len(toks) {
			if _, err := fmt.Sscanf(toks[i+1], "%d", new(int)); err == nil {
				return toks[i+1]
			}
		}
	}
	return ""
}

// serviceURLFromCommand : adresse de l'API déduite de la commande, comme en
// 0.19 (« vide = déduite de la commande »).
func serviceURLFromCommand(cmd string) string {
	if p := portFromCommand(cmd); p != "" {
		return "http://127.0.0.1:" + p + "/v1"
	}
	return ""
}

// extBaseURL : l'adresse de l'API d'un preset externe. Pour un Service système
// qui n'en donne pas, elle est déduite du port de sa commande : l'éditeur de
// preset écrit les clés telles quelles, la déduction se fait donc à la lecture.
func extBaseURL(cfg map[string]string) string {
	if u := strings.TrimSpace(cfg[extKeyURL]); u != "" {
		return u
	}
	if cmd := serviceCmdOf(cfg); cmd != "" {
		return serviceURLFromCommand(cmd)
	}
	return ""
}

// serviceKeyFromCommand : la clé passée au serveur par --api-key (ou
// --api-key=…) dans la commande. "" si elle n'en donne pas.
func serviceKeyFromCommand(cmd string) string {
	toks := splitArgs(cmd)
	for i, a := range toks {
		if v, ok := strings.CutPrefix(a, "--api-key="); ok {
			return v
		}
		if a == "--api-key" && i+1 < len(toks) {
			return toks[i+1]
		}
	}
	return ""
}

// extAPIKey : la clé d'un preset externe. Pour un Service sans clé saisie, celle
// de sa commande : l'éditeur ne demande ni adresse ni clé pour un Service.
func extAPIKey(cfg map[string]string) string {
	if k := strings.TrimSpace(cfg[extKeyToken]); k != "" {
		return k
	}
	if cmd := serviceCmdOf(cfg); cmd != "" {
		return serviceKeyFromCommand(cmd)
	}
	return ""
}

// parseServiceEnv découpe SERVICE_ENV en paires CLE=valeur.
func parseServiceEnv(s string) ([]string, error) {
	var out []string
	for _, kv := range splitArgs(s) {
		k, _, ok := strings.Cut(kv, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return nil, fmt.Errorf("variable d'environnement invalide : %q (attendu CLE=valeur)", kv)
		}
		out = append(out, kv)
	}
	return out, nil
}

// serviceProgram résout le programme de la commande (chemin absolu ou PATH).
func serviceProgram(cmd string) (string, []string, error) {
	args := splitArgs(cmd)
	if len(args) == 0 {
		return "", nil, fmt.Errorf("commande du service vide")
	}
	bin, err := exec.LookPath(args[0])
	if err != nil {
		return "", nil, fmt.Errorf("programme introuvable : %s (chemin complet, ou présent dans le PATH du service)", args[0])
	}
	return bin, args, nil
}

// serveServiceCmd lance la commande du preset à la place de llama-server. Elle
// remplace « ajean serve » (Unix) ou tourne comme son enfant (Windows) : le
// service moteur la supervise exactement comme il supervise llama-server.
func serveServiceCmd(cfg map[string]string, cmd string) error {
	bin, args, err := serviceProgram(cmd)
	if err != nil {
		return err
	}
	if extBaseURL(cfg) == "" {
		return fmt.Errorf("adresse de l'API du service inconnue : la commande n'indique pas de port (--port), renseigne l'adresse dans le preset")
	}
	env, err := parseServiceEnv(cfg[svcKeyEnv])
	if err != nil {
		return err
	}
	for _, kv := range env {
		k, v, _ := strings.Cut(kv, "=")
		_ = os.Setenv(k, v)
	}
	if v := cfg["CUDA_VISIBLE_DEVICES"]; v != "" {
		_ = os.Setenv("CUDA_VISIBLE_DEVICES", v)
		_ = os.Setenv("CUDA_DEVICE_ORDER", "PCI_BUS_ID")
	}
	dir := strings.TrimSpace(cfg[svcKeyWorkdir])
	if dir == "" {
		dir = AjeanHome()
	}
	if err := os.Chdir(dir); err != nil {
		return fmt.Errorf("dossier de travail du service : %w", err)
	}
	if port := portFromCommand(cmd); port != "" {
		if err := waitPortFree("127.0.0.1", port, 5*time.Second); err != nil {
			return err
		}
	}
	fmt.Fprintf(os.Stderr, "[ajean serve] service système : %s\n", engineCmdline(args))
	_ = putBytes(bkState, engineCmdlineKey, []byte(engineCmdline(args)))
	args[0] = bin
	return execServer(bin, args)
}

// serviceAPIAnswers : l'API du preset répond-elle (GET <racine>/health, ou
// /v1/models pour un serveur sans /health) ?
func serviceAPIAnswers(cfg map[string]string) bool {
	api := strings.TrimSuffix(completionsURL(extBaseURL(cfg)), "/chat/completions")
	if api == "" {
		return false
	}
	root := strings.TrimSuffix(api, "/v1")
	key := extAPIKey(cfg)
	for _, u := range []string{root + "/health", api + "/models"} {
		req, err := http.NewRequest("GET", u, nil)
		if err != nil {
			return false
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := extHealthClient.Do(req)
		if err != nil {
			return false // le port n'écoute pas encore : chargement
		}
		resp.Body.Close()
		if resp.StatusCode == http.StatusOK {
			return true
		}
	}
	return false
}

// serviceCmdReady : le preset Service système est prêt quand son API répond.
func serviceCmdReady(cfg map[string]string) bool {
	return serviceCmdOf(cfg) != "" && serviceAPIAnswers(cfg)
}

// --- Modèle annoncé par le service -----------------------------------------

var (
	svcModelMu    sync.Mutex
	svcModelCache = map[string]string{} // URL de l'API → premier modèle annoncé
)

// serviceFirstModel renvoie le premier modèle annoncé par l'API (/v1/models),
// pour un preset qui n'en nomme pas (« vide = le premier annoncé »). Gardé en
// mémoire une fois obtenu ; "default" tant que l'API ne répond pas.
func serviceFirstModel(cfg map[string]string) string {
	api := strings.TrimSuffix(completionsURL(extBaseURL(cfg)), "/chat/completions")
	if api == "" {
		return "default"
	}
	svcModelMu.Lock()
	m := svcModelCache[api]
	svcModelMu.Unlock()
	if m != "" {
		return m
	}
	req, err := http.NewRequest("GET", api+"/models", nil)
	if err != nil {
		return "default"
	}
	if key := extAPIKey(cfg); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := extHealthClient.Do(req)
	if err != nil {
		return "default"
	}
	defer resp.Body.Close()
	var list struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if resp.StatusCode != http.StatusOK || json.NewDecoder(resp.Body).Decode(&list) != nil || len(list.Data) == 0 || list.Data[0].ID == "" {
		return "default"
	}
	svcModelMu.Lock()
	svcModelCache[api] = list.Data[0].ID
	svcModelMu.Unlock()
	return list.Data[0].ID
}
