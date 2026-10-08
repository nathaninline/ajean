package ajean

import (
	"time"
)

// MCPTimeouts sont les délais (en secondes) applicables à tous les serveurs
// MCP. Zéro = valeur par défaut (20 s connexion, 120 s appel d'outil).
// Stockés dans bkState sous la clé "mcp_timeouts".
type MCPTimeouts struct {
	// Connect borne l'établissement d'une session (handshake initialize +
	// tools/list). Un serveur qui rame ne doit pas figer le tour de chat.
	Connect int `json:"connect"`
	// Call borne un appel d'outil MCP.
	Call int `json:"call"`
}

// mcpTimeoutsDefaults sont les valeurs par défaut (identiques aux anciennes
// constantes codées en dur).
const (
	mcpConnectTimeoutDefault = 20  // secondes
	mcpCallTimeoutDefault    = 120 // secondes
)

// mcpTimeoutsKey est la clé bkState.
const mcpTimeoutsKey = "mcp_timeouts"

// LoadMCPTimeouts lit les délais MCP configurés. Absents => valeurs par défaut.
func LoadMCPTimeouts() MCPTimeouts {
	var t MCPTimeouts
	if !getJSON(bkState, mcpTimeoutsKey, &t) {
		t = MCPTimeouts{Connect: mcpConnectTimeoutDefault, Call: mcpCallTimeoutDefault}
	}
	if t.Connect <= 0 {
		t.Connect = mcpConnectTimeoutDefault
	}
	if t.Call <= 0 {
		t.Call = mcpCallTimeoutDefault
	}
	return t
}

// SaveMCPTimeouts persiste les délais MCP. Les valeurs <= 0 sont remplacées
// par les défauts.
func SaveMCPTimeouts(t MCPTimeouts) error {
	if t.Connect <= 0 {
		t.Connect = mcpConnectTimeoutDefault
	}
	if t.Call <= 0 {
		t.Call = mcpCallTimeoutDefault
	}
	mcpConfigMu.Lock()
	defer mcpConfigMu.Unlock()
	return putJSON(bkState, mcpTimeoutsKey, t)
}

// effectiveConnectTimeout renvoie le délai de connexion effectif (durée).
func effectiveConnectTimeout() time.Duration {
	return time.Duration(LoadMCPTimeouts().Connect) * time.Second
}

// effectiveCallTimeout renvoie le délai d'appel d'outil effectif (durée).
func effectiveCallTimeout() time.Duration {
	return time.Duration(LoadMCPTimeouts().Call) * time.Second
}
