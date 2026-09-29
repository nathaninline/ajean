package ajean

import (
	"strings"
	"testing"
)

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

// Mode rapide : le tour web tourne comme « ajean chat » (bash/write/edit, prompt
// court, ni mémoire ni web), sans jamais rallumer un agent éteint.
func TestCapsFromBodyModeRapide(t *testing.T) {
	testHome(t)
	if err := setAgentEnabled(true); err != nil {
		t.Fatal(err)
	}
	caps := capsFromBody(chatReq{Fast: true})
	if !caps.Terminal || !caps.Web || caps.Mem != MemOff || caps.Internet || caps.ComputerUse {
		t.Fatalf("mode rapide : caps inattendues %+v", caps)
	}
	var names []string
	for _, tl := range EnabledTools(caps) {
		names = append(names, tl.Function.Name)
	}
	for _, n := range names {
		if n != "bash" && n != "write" && n != "edit" && n != "see_image" {
			t.Errorf("outil %q proposé en mode rapide (%v)", n, names)
		}
	}
	if p := baseSystemPrompt(caps); strings.Contains(p, "terminal") {
		t.Errorf("le prompt du mode rapide web ne doit pas parler de terminal : %q", p)
	}
	if err := setAgentEnabled(false); err != nil {
		t.Fatal(err)
	}
	if caps := capsFromBody(chatReq{Fast: true}); caps.Agent || len(EnabledTools(caps)) > 0 {
		t.Errorf("agent coupé : le mode rapide ne doit pas le rallumer (%+v)", caps)
	}
}

// Modèle de base : rien, pas même la mémoire (elle seule réinjectait le
// préambule « Jean + mémoire persistante »).
func TestCapsFromBodyModeleDeBase(t *testing.T) {
	testHome(t)
	if err := setAgentEnabled(true); err != nil {
		t.Fatal(err)
	}
	caps := capsFromBody(chatReq{Raw: true})
	if caps.Agent || caps.Mem != MemOff || caps.Internet || caps.ComputerUse || caps.Terminal {
		t.Fatalf("modèle de base : caps inattendues %+v", caps)
	}
	if p := baseSystemPrompt(caps); p != "" {
		t.Errorf("modèle de base : aucun prompt système attendu, eu %q", p)
	}
	if n := len(EnabledTools(caps)); n > 0 {
		t.Errorf("modèle de base : %d outils proposés", n)
	}
}

func TestFoldSearch(t *testing.T) {
	if got := foldSearch("  Écrire le RÉSUMÉ "); got != "ecrire le resume" {
		t.Fatalf("foldSearch = %q", got)
	}
}
