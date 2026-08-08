package ajean

import "testing"

func ptrBool(b bool) *bool { return &b }

// Une surcharge portée par la requête ne doit JAMAIS rallumer ce que la machine
// a éteint : sans ça, {"agent":true} redonnait bash/write/edit (et les outils
// MCP) alors que l'interrupteur agent était sur OFF, sur une API qui n'est pas
// protégée par défaut.
func TestCapsFromBodyNePeutPasRallumerLAgent(t *testing.T) {
	testHome(t)
	if err := setAgentEnabled(false); err != nil {
		t.Fatal(err)
	}
	for _, body := range []chatReq{
		{Agent: ptrBool(true)},
		{Tools: ptrBool(true)},
		{Skills: ptrBool(true)},
		{Internet: ptrBool(true)},
	} {
		if caps := capsFromBody(body); caps.Agent || caps.Internet {
			t.Errorf("agent coupé sur la machine, mais %+v a donné %+v", body, caps)
		}
	}
	// Et les outils ne doivent pas non plus être servis au modèle.
	if tools := EnabledTools(capsFromBody(chatReq{Agent: ptrBool(true)})); len(tools) > 0 {
		t.Errorf("agent coupé : aucun outil ne devrait être proposé, %d le sont", len(tools))
	}
}

// L'inverse reste vrai : une surcharge peut RESTREINDRE un agent actif.
func TestCapsFromBodyPeutRestreindre(t *testing.T) {
	testHome(t)
	if err := setAgentEnabled(true); err != nil {
		t.Fatal(err)
	}
	if caps := capsFromBody(chatReq{Agent: ptrBool(false)}); caps.Agent {
		t.Error("agent:false doit couper l'agent pour ce tour")
	}
	if caps := capsFromBody(chatReq{}); !caps.Agent {
		t.Error("sans surcharge, on doit hériter de la configuration machine")
	}
}
