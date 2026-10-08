package ajean

import (
	"testing"
	"time"
)

// TestMCPTimeoutsDefautsSansConfig vérifie qu'une base vide renvoie les
// valeurs par défaut (20 s connexion, 120 s appel) et que les durées
// effectives correspondent.
func TestMCPTimeoutsDefautsSansConfig(t *testing.T) {
	testHome(t)

	got := LoadMCPTimeouts()
	if got.Connect != mcpConnectTimeoutDefault {
		t.Errorf("connect = %d, attendu %d", got.Connect, mcpConnectTimeoutDefault)
	}
	if got.Call != mcpCallTimeoutDefault {
		t.Errorf("call = %d, attendu %d", got.Call, mcpCallTimeoutDefault)
	}
	if d := effectiveConnectTimeout(); d != 20*time.Second {
		t.Errorf("effectiveConnectTimeout = %v, attendu 20s", d)
	}
	if d := effectiveCallTimeout(); d != 120*time.Second {
		t.Errorf("effectiveCallTimeout = %v, attendu 120s", d)
	}
}

// TestMCPTimeoutsAllerRetour vérifie la persistance : sauvegarde, relecture,
// et application aux durées effectives.
func TestMCPTimeoutsAllerRetour(t *testing.T) {
	testHome(t)

	if err := SaveMCPTimeouts(MCPTimeouts{Connect: 5, Call: 300}); err != nil {
		t.Fatalf("SaveMCPTimeouts: %v", err)
	}
	got := LoadMCPTimeouts()
	if got.Connect != 5 || got.Call != 300 {
		t.Fatalf("relecture = %+v, attendu {5 300}", got)
	}
	if d := effectiveConnectTimeout(); d != 5*time.Second {
		t.Errorf("effectiveConnectTimeout = %v, attendu 5s", d)
	}
	if d := effectiveCallTimeout(); d != 300*time.Second {
		t.Errorf("effectiveCallTimeout = %v, attendu 300s", d)
	}
}

// TestMCPTimeoutsZeroRetombeAuxDefauts vérifie qu'une valeur nulle (champ vide
// dans l'UI, ou clé absente) retombe sur la valeur par défaut.
func TestMCPTimeoutsZeroRetombeAuxDefauts(t *testing.T) {
	testHome(t)

	if err := SaveMCPTimeouts(MCPTimeouts{Connect: 0, Call: 0}); err != nil {
		t.Fatalf("SaveMCPTimeouts: %v", err)
	}
	got := LoadMCPTimeouts()
	if got.Connect != mcpConnectTimeoutDefault || got.Call != mcpCallTimeoutDefault {
		t.Fatalf("relecture = %+v, attendu les défauts %+v", got,
			MCPTimeouts{Connect: mcpConnectTimeoutDefault, Call: mcpCallTimeoutDefault})
	}
}
