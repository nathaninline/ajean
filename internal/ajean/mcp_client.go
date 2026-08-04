package ajean

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Pool de sessions MCP + exposition des outils au moteur de chat.
//
// Une session est gardée ouverte entre les tours de chat : un serveur stdio garde
// son process vivant d'un message à l'autre — c'est voulu, desktop-commander & co
// ont un coût de démarrage non négligeable.
//
// Les connexions sont établies EN PARALLÈLE (mcpEnsureAll) : en séquence, quatre
// serveurs `npx` faisaient attendre la somme de leurs démarrages (~20 s), là où
// les autres clients MCP paient le plus lent. Elles sont aussi pré-chauffées au
// démarrage du service web/link (MCPPrewarm) pour que le premier message ne paie
// pas le handshake.
//
// Le namespacing des outils suit la convention de l'écosystème :
// mcp__<serveur>__<outil>. On maintient un registre nom-exposé → (serveur, outil)
// pour router les appels sans avoir à re-parser des noms potentiellement
// ambigus.

const (
	// mcpConnectTimeout borne l'établissement d'une session (handshake initialize
	// + tools/list). Un serveur qui rame ne doit pas figer le tour de chat.
	mcpConnectTimeout = 20 * time.Second
	// mcpCallTimeout borne un appel d'outil MCP.
	mcpCallTimeout = 120 * time.Second
	// mcpMaxOutput cap la sortie renvoyée au modèle (cohérent avec toolMaxOutput).
	mcpMaxOutput = 12000
)

// mcpSession est une connexion vivante à un serveur MCP.
//
// L'entrée est publiée dans le pool AVANT que la connexion aboutisse, pour qu'un
// second appelant se mette en attente au lieu de lancer un deuxième process. Les
// champs sess/tools/err ne doivent donc être lus qu'une fois `ready` fermé.
type mcpSession struct {
	name  string
	cfg   MCPServerConfig
	sess  *mcpsdk.ClientSession
	tools []*mcpsdk.Tool
	err   error         // dernière erreur de connexion/liste, pour l'UI
	ready chan struct{} // fermé quand la tentative de connexion est terminée
}

// mcpManager détient le pool de sessions.
type mcpManager struct {
	mu       sync.Mutex
	sessions map[string]*mcpSession
	// registry mappe un nom d'outil exposé au modèle → (serveur, outil réel).
	registry map[string]mcpToolRef
}

type mcpToolRef struct {
	server string
	tool   string
}

var mcpMgr = &mcpManager{
	sessions: map[string]*mcpSession{},
	registry: map[string]mcpToolRef{},
}

// mcpInvalidate ferme et oublie la session d'un serveur (après un changement de
// config), pour qu'elle soit reconstruite avec la nouvelle config au prochain
// usage.
func mcpInvalidate(name string) {
	mcpMgr.mu.Lock()
	s := mcpMgr.sessions[name]
	delete(mcpMgr.sessions, name)
	// Purge le registre des outils de ce serveur.
	for exposed, ref := range mcpMgr.registry {
		if ref.server == name {
			delete(mcpMgr.registry, exposed)
		}
	}
	mcpMgr.mu.Unlock()
	// Une session encore en cours de connexion a sess == nil : c'est sa goroutine
	// qui refermera ce qu'elle vient d'ouvrir, en constatant qu'elle n'est plus
	// dans le pool.
	if s != nil && s.sess != nil {
		_ = s.sess.Close()
	}
}

// mcpCloseAll ferme toutes les sessions (arrêt du service).
func mcpCloseAll() {
	mcpMgr.mu.Lock()
	sessions := mcpMgr.sessions
	mcpMgr.sessions = map[string]*mcpSession{}
	mcpMgr.registry = map[string]mcpToolRef{}
	mcpMgr.mu.Unlock()
	for _, s := range sessions {
		if s.sess != nil {
			_ = s.sess.Close()
		}
	}
}

// headerRoundTripper injecte des en-têtes statiques (auth) sur chaque requête
// HTTP vers un serveur MCP distant.
type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	for k, v := range h.headers {
		req.Header.Set(k, v)
	}
	return h.base.RoundTrip(req)
}

// connect établit une session pour la config donnée.
func mcpConnect(ctx context.Context, name string, cfg MCPServerConfig) (*mcpsdk.ClientSession, error) {
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "jean", Version: Version}, nil)

	var transport mcpsdk.Transport
	switch cfg.Transport() {
	case "stdio":
		cmd := hideCmd(exec.Command(cfg.Command, cfg.Args...)) // pas de flash console (mode app Windows)
		// Hérite de l'environnement du service + surcharges déclarées.
		cmd.Env = os.Environ()
		for k, v := range cfg.Env {
			cmd.Env = append(cmd.Env, k+"="+v)
		}
		transport = &mcpsdk.CommandTransport{Command: cmd}
	case "http":
		httpClient := &http.Client{}
		if len(cfg.Headers) > 0 {
			httpClient.Transport = headerRoundTripper{base: http.DefaultTransport, headers: cfg.Headers}
		}
		transport = &mcpsdk.StreamableClientTransport{Endpoint: cfg.URL, HTTPClient: httpClient}
	default:
		return nil, fmt.Errorf("serveur MCP '%s' mal configuré (ni command ni url)", name)
	}

	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, err
	}
	return sess, nil
}

// ensure renvoie une session vivante pour un serveur activé, en la (re)créant si
// besoin. Doit être appelé sans détenir mgr.mu (il gère le lock lui-même).
//
// L'entrée est réservée dans le pool avant la connexion : deux appelants
// simultanés (le pré-chauffage et un tour de chat, par exemple) partagent la même
// tentative au lieu de lancer deux process stdio.
func (m *mcpManager) ensure(name string, cfg MCPServerConfig) *mcpSession {
	m.mu.Lock()
	if s, ok := m.sessions[name]; ok {
		m.mu.Unlock()
		<-s.ready // connexion peut-être encore en cours : on attend son issue
		return s
	}
	s := &mcpSession{name: name, cfg: cfg, ready: make(chan struct{})}
	m.sessions[name] = s
	m.mu.Unlock()

	// Connexion hors-lock (peut être lente).
	ctx, cancel := context.WithTimeout(context.Background(), mcpConnectTimeout)
	defer cancel()
	sess, err := mcpConnect(ctx, name, cfg)
	if err != nil {
		s.err = err
	} else {
		s.sess = sess
		lt, lerr := sess.ListTools(ctx, nil)
		if lerr != nil {
			s.err = lerr
		} else {
			s.tools = lt.Tools
		}
	}

	m.mu.Lock()
	// La session a pu être invalidée pendant la connexion (changement de config,
	// arrêt du service) : dans ce cas on ne la republie pas, on referme.
	stale := m.sessions[name] != s
	if !stale {
		// (Re)peuple le registre pour ce serveur.
		for exposed, ref := range m.registry {
			if ref.server == name {
				delete(m.registry, exposed)
			}
		}
		for _, t := range s.tools {
			m.registry[mcpExposedName(name, t.Name)] = mcpToolRef{server: name, tool: t.Name}
		}
	}
	m.mu.Unlock()
	close(s.ready)
	if stale && s.sess != nil {
		_ = s.sess.Close()
		s.sess = nil
	}
	return s
}

// mcpEnsureAll connecte en parallèle tous les serveurs activés et attend qu'ils
// soient tous fixés (connectés ou en erreur). C'est ce qui évite d'additionner
// les temps de démarrage : le coût total est celui du serveur le plus lent.
func mcpEnsureAll(servers map[string]MCPServerConfig) {
	var wg sync.WaitGroup
	for _, name := range sortedServerNames(servers) {
		cfg := servers[name]
		if !cfg.Enabled || cfg.Transport() == "" {
			continue
		}
		wg.Add(1)
		go func(name string, cfg MCPServerConfig) {
			defer wg.Done()
			mcpMgr.ensure(name, cfg)
		}(name, cfg)
	}
	wg.Wait()
}

// MCPPrewarm établit les sessions MCP en tâche de fond, au démarrage du service
// web/link. Sans ça le handshake était payé par le premier message de chat, ce
// qui donnait l'impression d'une IA très lente à démarrer. Sans mode agent les
// outils MCP ne sont de toute façon pas exposés : inutile de lancer les process.
func MCPPrewarm() {
	if !agentEnabled() {
		return
	}
	servers, err := LoadMCPConfig()
	if err != nil || len(servers) == 0 {
		return
	}
	go mcpEnsureAll(servers)
}

var mcpSanitizeRe = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

// mcpSanitize rend une chaîne compatible avec les noms d'outils (^[A-Za-z0-9_-]+$).
func mcpSanitize(s string) string {
	s = mcpSanitizeRe.ReplaceAllString(s, "_")
	return strings.Trim(s, "_")
}

// mcpExposedName construit le nom d'outil vu par le modèle.
func mcpExposedName(server, tool string) string {
	return "mcp__" + mcpSanitize(server) + "__" + mcpSanitize(tool)
}

// mcpTools connecte les serveurs activés et renvoie leurs outils, prêts à être
// annoncés au modèle. Réservé au mode agent (appelé par EnabledTools).
func mcpTools() []Tool {
	servers, err := LoadMCPConfig()
	if err != nil || len(servers) == 0 {
		return nil
	}
	mcpEnsureAll(servers) // connexions en parallèle, puis lecture du pool (instantanée)
	var out []Tool
	for _, name := range sortedServerNames(servers) {
		cfg := servers[name]
		if !cfg.Enabled || cfg.Transport() == "" {
			continue
		}
		s := mcpMgr.ensure(name, cfg)
		if s.sess == nil {
			continue // connexion échouée : signalé dans l'UI, pas au modèle
		}
		for _, t := range s.tools {
			if cfg.ToolDisabled(t.Name) {
				continue // outil masqué par l'utilisateur : pas annoncé au modèle
			}
			desc := t.Description
			if desc == "" {
				desc = t.Title
			}
			// Préfixe le serveur d'origine dans la description : aide le modèle à
			// choisir entre outils similaires de serveurs différents.
			desc = "[MCP: " + name + "] " + desc
			out = append(out, Tool{
				Type: "function",
				Function: ToolFunction{
					Name:        mcpExposedName(name, t.Name),
					Description: desc,
					Parameters:  mcpNormalizeSchema(t.InputSchema),
				},
			})
		}
	}
	return out
}

// mcpNormalizeSchema convertit le InputSchema du SDK (any → map) en map[string]any
// pour notre type Tool. Un schéma vide devient un objet sans propriété.
func mcpNormalizeSchema(schema any) map[string]any {
	if m, ok := schema.(map[string]any); ok && m != nil {
		return m
	}
	// Le SDK peut renvoyer une struct/RawMessage : passe par un round-trip JSON.
	if schema != nil {
		if b, err := json.Marshal(schema); err == nil {
			var m map[string]any
			if json.Unmarshal(b, &m) == nil && m != nil {
				return m
			}
		}
	}
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

// mcpArgLabel produit un libellé court à partir des arguments d'un appel MCP,
// pour l'affichage « outil utilisé » dans l'UI (les outils MCP n'ont pas de
// champ canonique connu à l'avance).
func mcpArgLabel(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	// Privilégie les clés parlantes courantes.
	for _, k := range []string{"path", "file", "query", "command", "url", "name"} {
		if v, ok := args[k].(string); ok && v != "" {
			return v
		}
	}
	b, _ := json.Marshal(args)
	s := string(b)
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120]) + "…"
	}
	return s
}

// isMCPTool indique si un nom d'outil est routé vers MCP.
func isMCPTool(name string) bool {
	return strings.HasPrefix(name, "mcp__")
}

// mcpCall exécute un outil MCP à partir de son nom exposé et renvoie une chaîne
// résultat (texte aplati), cappée. Reconnecte une fois si la session est morte.
func mcpCall(name string, args map[string]any) string {
	mcpMgr.mu.Lock()
	ref, ok := mcpMgr.registry[name]
	mcpMgr.mu.Unlock()
	if !ok {
		return "[erreur] outil MCP inconnu ou serveur non connecté: " + name
	}

	result, err := mcpCallOnce(ref, args)
	if err != nil {
		// Session peut-être morte (process stdio tombé) : on l'invalide et on
		// retente une fois avec une reconnexion fraîche.
		servers, _ := LoadMCPConfig()
		cfg, exists := servers[ref.server]
		if exists && cfg.Enabled {
			mcpInvalidate(ref.server)
			mcpMgr.ensure(ref.server, cfg)
			if result2, err2 := mcpCallOnce(ref, args); err2 == nil {
				return result2
			} else {
				err = err2
			}
		}
		return "[erreur MCP] " + err.Error()
	}
	return result
}

func mcpCallOnce(ref mcpToolRef, args map[string]any) (string, error) {
	mcpMgr.mu.Lock()
	s := mcpMgr.sessions[ref.server]
	mcpMgr.mu.Unlock()
	if s == nil || s.sess == nil {
		return "", fmt.Errorf("serveur %s non connecté", ref.server)
	}
	ctx, cancel := context.WithTimeout(context.Background(), mcpCallTimeout)
	defer cancel()
	res, err := s.sess.CallTool(ctx, &mcpsdk.CallToolParams{Name: ref.tool, Arguments: args})
	if err != nil {
		return "", err
	}
	out := flattenMCPContent(res)
	if r := []rune(out); len(r) > mcpMaxOutput {
		out = string(r[:mcpMaxOutput]) + "\n…[tronqué]"
	}
	if res.IsError {
		return "[l'outil a renvoyé une erreur]\n" + out, nil
	}
	if strings.TrimSpace(out) == "" {
		return "[ok] (aucune sortie)", nil
	}
	return out, nil
}

// flattenMCPContent aplatit le contenu d'un CallToolResult en texte. Les blocs
// texte sont concaténés ; le contenu structuré est rendu en JSON s'il n'y a pas
// de texte ; les autres types (image/audio) sont résumés.
func flattenMCPContent(res *mcpsdk.CallToolResult) string {
	var parts []string
	for _, c := range res.Content {
		switch v := c.(type) {
		case *mcpsdk.TextContent:
			parts = append(parts, v.Text)
		case *mcpsdk.ImageContent:
			parts = append(parts, "[image "+v.MIMEType+"]")
		case *mcpsdk.AudioContent:
			parts = append(parts, "[audio "+v.MIMEType+"]")
		default:
			if b, err := json.Marshal(c); err == nil {
				parts = append(parts, string(b))
			}
		}
	}
	text := strings.TrimSpace(strings.Join(parts, "\n"))
	if text == "" && res.StructuredContent != nil {
		if b, err := json.MarshalIndent(res.StructuredContent, "", "  "); err == nil {
			return string(b)
		}
	}
	return text
}

// mcpPromptLine renvoie une ligne système listant les serveurs MCP connectés et
// leur nombre d'outils, pour situer le modèle. Ne force PAS de connexion : lit
// seulement l'état déjà établi par mcpTools() (appelé par EnabledTools sur le
// même tour), pour ne pas payer deux fois le handshake.
func mcpPromptLine() string {
	mcpMgr.mu.Lock()
	defer mcpMgr.mu.Unlock()
	var names []string
	for name, s := range mcpMgr.sessions {
		if s.sess != nil && len(s.tools) > 0 {
			names = append(names, fmt.Sprintf("%s (%d)", name, len(s.tools)))
		}
	}
	if len(names) == 0 {
		return ""
	}
	sort.Strings(names)
	return "\n\nMCP servers connected (their tools are prefixed mcp__<server>__<tool>): " + strings.Join(names, ", ") + "."
}

// MCPServerStatus est l'état d'un serveur pour l'UI web.
type MCPServerStatus struct {
	Name      string   `json:"name"`
	Transport string   `json:"transport"`
	Enabled   bool     `json:"enabled"`
	Connected bool     `json:"connected"`
	Error     string   `json:"error,omitempty"`
	Tools     []string `json:"tools"`    // tous les outils découverts sur le serveur
	Disabled  []string `json:"disabled"` // sous-ensemble masqué (non exposé à l'IA)
	// Détail de config (pour pré-remplir le formulaire d'édition).
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
}

// MCPStatus renvoie l'état de tous les serveurs configurés, en tentant de
// connecter ceux qui sont activés (pour refléter l'état réel dans l'UI).
func MCPStatus() ([]MCPServerStatus, error) {
	servers, err := LoadMCPConfig()
	if err != nil {
		return nil, err
	}
	mcpEnsureAll(servers) // en parallèle : le panneau MCP de l'UI n'attend plus la somme
	var out []MCPServerStatus
	for _, name := range sortedServerNames(servers) {
		cfg := servers[name]
		st := MCPServerStatus{
			Name:      name,
			Transport: cfg.Transport(),
			Enabled:   cfg.Enabled,
			Command:   cfg.Command,
			Args:      cfg.Args,
			Env:       cfg.Env,
			URL:       cfg.URL,
			Headers:   cfg.Headers,
			Tools:     []string{},
			Disabled:  cfg.DisabledTools,
		}
		if st.Disabled == nil {
			st.Disabled = []string{}
		}
		if cfg.Enabled && cfg.Transport() != "" {
			s := mcpMgr.ensure(name, cfg)
			if s.sess != nil {
				st.Connected = true
				for _, t := range s.tools {
					st.Tools = append(st.Tools, t.Name)
				}
			}
			if s.err != nil {
				st.Error = s.err.Error()
			}
		}
		out = append(out, st)
	}
	return out, nil
}
