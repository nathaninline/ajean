package ajean

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"
)

// resetEngineSlot réinitialise l'état global du re-chauffage (partagé par les
// tests du process).
func resetEngineSlot(t *testing.T) {
	t.Helper()
	reset := func() {
		engineSlot.mu.Lock()
		engineSlot.displaced, engineSlot.external, engineSlot.inflight = false, false, false
		engineSlot.lastUse, engineSlot.nextTry = time.Time{}, time.Time{}
		engineSlot.slotTask, engineSlot.warmSig, engineSlot.warmStamp = -1, "", ""
		engineSlot.cancel, engineSlot.failSig, engineSlot.attempts = nil, "", 0
		engineSlot.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// Le re-chauffage ne sert que si son préfixe est EXACTEMENT celui du tour
// suivant. Comme pour le pré-chauffage, on ne le compare pas à ses propres
// fonctions : on lance le vrai tour suivant d'une conversation commencée contre
// un faux moteur, et on exige que tout ce qui précède le nouveau message
// utilisateur, outils compris, soit identique.
func TestRewarmPrefixMatchesNextTurn(t *testing.T) {
	for _, mode := range []string{"project", "fast", "jean"} {
		t.Run(mode, func(t *testing.T) {
			testHome(t)
			if err := setAgentEnabled(true); err != nil {
				t.Fatal(err)
			}
			if err := SetConfigKey("MEM_MODE", "always"); err != nil {
				t.Fatal(err)
			}
			if err := putStr(bkState, "sysprompt", "Réponds en français."); err != nil {
				t.Fatal(err)
			}
			spy := newEngineSpy(t)
			setConvEmpty(t, mode)
			resetEngineSlot(t)

			body := chatReq{Mode: mode}
			m, _ := conv.lockMode(requestedMode(body))
			applyChatMode(&body, m)
			caps := capsFromBody(body)
			conv.mu.Lock()
			conv.Messages = []Message{
				{Role: "user", Content: "Salut, tu peux m'aider ?"},
				{Role: "assistant", Content: "Bien sûr, dis-moi."},
			}
			conv.lastCaps, conv.hasLastCaps = caps, true
			conv.mu.Unlock()

			ep := resolveChatEndpoint()
			p, ok := buildRewarmPrefix(ep)
			if !ok {
				t.Fatal("pas de préfixe pour une conversation commencée")
			}
			want := rewarmPayload(ep, p)

			if err := conv.StartTurn("Et maintenant ?", nil, caps, 0.7); err != nil {
				t.Fatal(err)
			}
			sent := spy.next(t)
			conv.Stop()
			waitConvIdle(t)

			got, _ := sent["messages"].([]any)
			wmsgs := canonList(want["messages"])
			if len(got) != len(wmsgs) {
				t.Fatalf("%d messages envoyés, %d chauffés", len(got), len(wmsgs))
			}
			for i := 0; i < len(got)-1; i++ {
				if canon(got[i]) != wmsgs[i] {
					t.Fatalf("message %d différent :\n tour   : %s\n chauffé : %s", i, canon(got[i]), wmsgs[i])
				}
			}
			if canon(sent["tools"]) != canon(want["tools"]) {
				t.Fatal("outils du re-chauffage différents de ceux du tour")
			}
		})
	}
}

func canonList(v any) []string {
	var out []string
	switch l := v.(type) {
	case []Message:
		for _, m := range l {
			out = append(out, canon(m))
		}
	case []any:
		for _, m := range l {
			out = append(out, canon(m))
		}
	}
	return out
}

// Qui a pris le créneau : la conversation le garde, un travail de fond ou un
// client tiers le lui prend.
func TestNoteEngineUse(t *testing.T) {
	resetEngineSlot(t)
	noteEngineUse(engineUserOf(context.Background()))
	if !engineSlot.displaced || engineSlot.external {
		t.Fatal("un travail de fond doit déplacer le cache (sans être un tiers)")
	}
	noteEngineUse(engineUserOf(withConvTurn(context.Background())))
	if engineSlot.displaced {
		t.Fatal("un tour de la conversation lui rend le cache")
	}
	noteEngineUse(engineUserOf(withThirdParty(context.Background())))
	if !engineSlot.displaced || !engineSlot.external {
		t.Fatal("un client tiers doit être reconnu comme tel")
	}
}

// Le compteur « moteur occupé » tient jusqu'à la fin du flux, et ne se libère
// qu'une fois même si le corps est lu jusqu'au bout PUIS fermé.
func TestEngineBodyReleasesOnce(t *testing.T) {
	resetEngineSlot(t)
	before := llmInFlight.Load()
	llmInFlight.Add(1)
	b := &engineBody{ReadCloser: io.NopCloser(strings.NewReader("data: x\n\n")), ctx: context.Background()}
	if llmInFlight.Load() != before+1 {
		t.Fatal("compteur non posé")
	}
	_, _ = io.ReadAll(b)
	if llmInFlight.Load() != before {
		t.Fatal("la fin du flux doit libérer le compteur")
	}
	_ = b.Close()
	if llmInFlight.Load() != before {
		t.Fatal("libéré deux fois")
	}
}

// Requêtes d'IDE : un seul system en tête, « developer » compris, contenus
// texte aplatis, images laissées en parts.
func TestNormalizeOAIChat(t *testing.T) {
	in := map[string]any{"messages": []any{
		map[string]any{"role": "system", "content": "A"},
		map[string]any{"role": "system", "content": []any{map[string]any{"type": "text", "text": "B"}}},
		map[string]any{"role": "user", "content": []any{map[string]any{"type": "text", "text": "q"}}},
		map[string]any{"role": "developer", "content": "C"},
		map[string]any{"role": "user", "content": []any{
			map[string]any{"type": "text", "text": "vois"},
			map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:x"}},
		}},
	}}
	normalizeOAIChat(in)
	msgs := in["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("%d messages, attendu 3 : %v", len(msgs), msgs)
	}
	first := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != "A\n\nB\n\nC" {
		t.Fatalf("system fusionné : %v", first)
	}
	if msgs[1].(map[string]any)["content"] != "q" {
		t.Fatalf("texte non aplati : %v", msgs[1])
	}
	if _, ok := msgs[2].(map[string]any)["content"].([]any); !ok {
		t.Fatalf("l'image doit rester en parts : %v", msgs[2])
	}
}

// Le client garde ses réglages ; le preset ne comble que les manques.
func TestOAIFillDefaults(t *testing.T) {
	testHome(t)
	if err := SetConfigKey("TOP_K", "20"); err != nil {
		t.Fatal(err)
	}
	if err := SetConfigKey("TEMP", "0.6"); err != nil {
		t.Fatal(err)
	}
	in := map[string]any{"model": "gpt-4o", "temperature": 0.1, "max_completion_tokens": 50, "tools": []any{}}
	oaiFillDefaults(in, chatEndpoint{Model: "ajean"})
	if in["model"] != "ajean" || in["temperature"] != 0.1 || in["top_k"] != 20 || in["max_tokens"] != 50 {
		t.Fatalf("défauts mal appliqués : %v", in)
	}
	if in["parallel_tool_calls"] != false {
		t.Fatal("appels d'outils parallèles non bornés")
	}
}
