package jean

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
)

// Configuration des serveurs MCP (Model Context Protocol).
//
// jean peut se connecter à des serveurs MCP tiers pour enrichir la palette
// d'outils de l'IA (accès fichiers rapide type desktop-commander, bases de
// données, APIs métier…). Deux transports :
//
//   - stdio : jean lance un process local (command + args) et parle en
//     JSON-RPC sur stdin/stdout. C'est le cas de desktop-commander, du serveur
//     filesystem officiel, etc. — lancés via npx/uvx/un binaire.
//   - http  : jean parle à un serveur MCP distant en Streamable HTTP (url +
//     éventuels en-têtes d'auth).
//
// Le format du fichier mcp.json reprend celui de Claude Desktop (clé
// "mcpServers" indexée par nom) pour que les utilisateurs puissent copier-coller
// leurs configs existantes. On ajoute un champ "enabled" par serveur.
//
// IMPORTANT (sécurité) : un serveur MCP stdio exécute un process arbitraire sur
// la machine où tourne jean — même niveau de confiance que l'outil `bash` du
// mode agent. La configuration MCP est donc réservée au propriétaire local de la
// machine et ne doit JAMAIS être pilotable depuis le relais/accès distant.

// MCPServerConfig décrit un serveur MCP configuré. Un seul des deux transports
// est renseigné : Command => stdio, URL => http.
type MCPServerConfig struct {
	// Transport stdio.
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`

	// Transport http (Streamable HTTP).
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`

	// Enabled : le serveur n'est connecté et ses outils exposés que s'il est
	// activé. Un serveur nouvellement ajouté est actif par défaut (voir
	// UnmarshalJSON) pour coller à l'intuition « je l'ajoute, il marche ».
	Enabled bool `json:"enabled"`

	// DisabledTools : outils du serveur à NE PAS exposer à l'IA (par nom réel,
	// non namespacé). Permet de garder un serveur connecté tout en masquant
	// certains de ses outils. Vide = tous les outils exposés.
	DisabledTools []string `json:"disabledTools,omitempty"`
}

// ToolDisabled indique si un outil (nom réel) est masqué pour ce serveur.
func (c MCPServerConfig) ToolDisabled(tool string) bool {
	for _, t := range c.DisabledTools {
		if t == tool {
			return true
		}
	}
	return false
}

// enabledDefaultTrue est un alias utilisé pour appliquer enabled=true par défaut
// quand le champ est absent du JSON (compat configs Claude Desktop sans
// "enabled").
type mcpServerConfigAlias MCPServerConfig

// UnmarshalJSON applique enabled=true par défaut lorsque la clé est absente,
// pour rester compatible avec les fichiers mcp.json qui ne connaissent pas ce
// champ (Claude Desktop, etc.).
func (c *MCPServerConfig) UnmarshalJSON(b []byte) error {
	// Sonde la présence de la clé "enabled".
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(b, &probe); err != nil {
		return err
	}
	alias := mcpServerConfigAlias{}
	if err := json.Unmarshal(b, &alias); err != nil {
		return err
	}
	if _, ok := probe["enabled"]; !ok {
		alias.Enabled = true
	}
	*c = MCPServerConfig(alias)
	return nil
}

// Transport renvoie "stdio", "http" ou "" (mal configuré : ni command ni url).
func (c MCPServerConfig) Transport() string {
	switch {
	case strings.TrimSpace(c.Command) != "":
		return "stdio"
	case strings.TrimSpace(c.URL) != "":
		return "http"
	default:
		return ""
	}
}

// Validate vérifie qu'exactement un transport est renseigné.
func (c MCPServerConfig) Validate() error {
	hasCmd := strings.TrimSpace(c.Command) != ""
	hasURL := strings.TrimSpace(c.URL) != ""
	switch {
	case hasCmd && hasURL:
		return fmt.Errorf("un serveur MCP ne peut avoir à la fois 'command' (stdio) et 'url' (http)")
	case !hasCmd && !hasURL:
		return fmt.Errorf("un serveur MCP doit avoir soit 'command' (stdio) soit 'url' (http)")
	}
	return nil
}

// mcpConfigFile est la représentation sur disque de mcp.json.
type mcpConfigFile struct {
	MCPServers map[string]MCPServerConfig `json:"mcpServers"`
}

// mcpConfigMu sérialise les accès concurrents au fichier mcp.json (l'UI web et
// les tours de chat peuvent lire/écrire en parallèle).
var mcpConfigMu sync.Mutex

// LoadMCPConfig lit mcp.json. Fichier absent => config vide (pas d'erreur).
func LoadMCPConfig() (map[string]MCPServerConfig, error) {
	mcpConfigMu.Lock()
	defer mcpConfigMu.Unlock()
	return loadMCPConfigLocked()
}

func loadMCPConfigLocked() (map[string]MCPServerConfig, error) {
	b, err := os.ReadFile(mcpConfigPath())
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]MCPServerConfig{}, nil
		}
		return nil, err
	}
	var f mcpConfigFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("mcp.json invalide: %w", err)
	}
	if f.MCPServers == nil {
		f.MCPServers = map[string]MCPServerConfig{}
	}
	return f.MCPServers, nil
}

// saveMCPConfigLocked écrit atomiquement mcp.json (write temp + rename).
func saveMCPConfigLocked(servers map[string]MCPServerConfig) error {
	_ = os.MkdirAll(JeanHome(), 0o755)
	f := mcpConfigFile{MCPServers: servers}
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	tmp := mcpConfigPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, mcpConfigPath())
}

// SetMCPServer ajoute ou remplace un serveur nommé, puis invalide le pool de
// sessions pour que le changement prenne effet au prochain tour.
func SetMCPServer(name string, cfg MCPServerConfig) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("nom de serveur vide")
	}
	if strings.Contains(name, "__") {
		return fmt.Errorf("le nom ne peut pas contenir '__' (réservé au namespacing des outils)")
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	mcpConfigMu.Lock()
	servers, err := loadMCPConfigLocked()
	if err != nil {
		mcpConfigMu.Unlock()
		return err
	}
	servers[name] = cfg
	err = saveMCPConfigLocked(servers)
	mcpConfigMu.Unlock()
	if err != nil {
		return err
	}
	mcpInvalidate(name)
	return nil
}

// DeleteMCPServer retire un serveur et ferme sa session si ouverte.
func DeleteMCPServer(name string) error {
	mcpConfigMu.Lock()
	servers, err := loadMCPConfigLocked()
	if err != nil {
		mcpConfigMu.Unlock()
		return err
	}
	if _, ok := servers[name]; !ok {
		mcpConfigMu.Unlock()
		return fmt.Errorf("serveur MCP inconnu: %s", name)
	}
	delete(servers, name)
	err = saveMCPConfigLocked(servers)
	mcpConfigMu.Unlock()
	if err != nil {
		return err
	}
	mcpInvalidate(name)
	return nil
}

// SetMCPServerEnabled active/désactive un serveur existant.
func SetMCPServerEnabled(name string, on bool) error {
	mcpConfigMu.Lock()
	servers, err := loadMCPConfigLocked()
	if err != nil {
		mcpConfigMu.Unlock()
		return err
	}
	cfg, ok := servers[name]
	if !ok {
		mcpConfigMu.Unlock()
		return fmt.Errorf("serveur MCP inconnu: %s", name)
	}
	cfg.Enabled = on
	servers[name] = cfg
	err = saveMCPConfigLocked(servers)
	mcpConfigMu.Unlock()
	if err != nil {
		return err
	}
	mcpInvalidate(name)
	return nil
}

// SetMCPToolEnabled masque/démasque un outil précis d'un serveur (via sa liste
// DisabledTools), puis invalide la session pour recalculer les outils exposés.
func SetMCPToolEnabled(server, tool string, on bool) error {
	mcpConfigMu.Lock()
	servers, err := loadMCPConfigLocked()
	if err != nil {
		mcpConfigMu.Unlock()
		return err
	}
	cfg, ok := servers[server]
	if !ok {
		mcpConfigMu.Unlock()
		return fmt.Errorf("serveur MCP inconnu: %s", server)
	}
	// Reconstruit la liste sans l'outil concerné, puis l'ajoute si on désactive.
	next := cfg.DisabledTools[:0:0]
	for _, t := range cfg.DisabledTools {
		if t != tool {
			next = append(next, t)
		}
	}
	if !on {
		next = append(next, tool)
	}
	cfg.DisabledTools = next
	servers[server] = cfg
	err = saveMCPConfigLocked(servers)
	mcpConfigMu.Unlock()
	if err != nil {
		return err
	}
	mcpInvalidate(server)
	return nil
}

// sortedServerNames renvoie les noms triés, pour un ordre d'affichage/itération
// stable.
func sortedServerNames(servers map[string]MCPServerConfig) []string {
	names := make([]string, 0, len(servers))
	for n := range servers {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}
