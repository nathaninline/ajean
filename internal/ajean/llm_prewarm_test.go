package ajean

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// engineSpy est un faux llama-server : /health répond, et chaque complétion reçue
// est transmise au test. Une complétion en STREAMING (un vrai tour) reste ouverte
// jusqu'à ce que le test interrompe le tour (conv.Stop) ; une complétion non
// streamée (le pré-chauffage) reçoit une réponse vide tout de suite.
type engineSpy struct {
	got chan map[string]any
}

func newEngineSpy(t *testing.T) *engineSpy {
	t.Helper()
	s := &engineSpy{got: make(chan map[string]any, 16)}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/chat/completions":
			var body map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			select {
			case s.got <- body:
			default:
			}
			if stream, _ := body["stream"].(bool); stream {
				select {
				case <-r.Context().Done():
				case <-time.After(10 * time.Second):
				}
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"content":""}}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	_, port, err := net.SplitHostPort(srv.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if err := SetConfigKey("PORT", port); err != nil {
		t.Fatal(err)
	}
	return s
}

// next attend la prochaine complétion reçue par le faux moteur.
func (s *engineSpy) next(t *testing.T) map[string]any {
	t.Helper()
	select {
	case b := <-s.got:
		return b
	case <-time.After(5 * time.Second):
		t.Fatal("aucune requête reçue par le moteur")
		return nil
	}
}

// none vérifie qu'aucune complétion n'arrive pendant d.
func (s *engineSpy) none(t *testing.T, d time.Duration, why string) {
	t.Helper()
	select {
	case b := <-s.got:
		t.Fatalf("%s : le moteur a reçu une requête (%v)", why, b["max_tokens"])
	case <-time.After(d):
	}
}

// waitConvIdle attend la fin du tour en cours sur la conversation vive.
func waitConvIdle(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for conv.isGenerating() {
		if time.Now().After(deadline) {
			t.Fatal("le tour ne s'est pas terminé")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// resetConvEmpty remet la conversation vive (globale au process) dans l'état d'une
// conversation neuve. mode "jean" = la conversation unique de Jean.
func resetConvEmpty(mode string) {
	conv.mu.Lock()
	defer conv.mu.Unlock()
	conv.Messages, conv.Log, conv.queued = nil, nil, nil
	conv.Seq, conv.CtxUsed = 0, 0
	conv.Generating = false
	conv.cancel = nil
	conv.epoch++
	conv.ID, conv.Mode = newSessionID(), ""
	if mode == "jean" {
		conv.ID, conv.Mode = jeanConvID, "jean"
	}
}

// setConvEmpty : conversation neuve pour ce test, et remise à zéro à sa fin (les
// tests partagent le process, donc la conversation globale).
func setConvEmpty(t *testing.T, mode string) {
	t.Helper()
	resetConvEmpty(mode)
	t.Cleanup(func() {
		conv.Stop()
		waitConvIdle(t)
		resetConvEmpty("")
	})
}

// resetWarmState réinitialise l'état global du pré-chauffage (idem : partagé).
func resetWarmState(t *testing.T) {
	t.Helper()
	reset := func() {
		llmWarm.mu.Lock()
		llmWarm.healthy, llmWarm.sig, llmWarm.warmed, llmWarm.inflight = false, "", false, false
		llmWarm.attempts, llmWarm.nextTry, llmWarm.lastCheck, llmWarm.cancel = 0, time.Time{}, time.Time{}, nil
		llmWarm.mu.Unlock()
	}
	reset()
	t.Cleanup(reset)
}

// canon normalise une valeur JSON (clés triées) pour comparer des corps de requête
// venus de deux chemins différents.
func canon(v any) string {
	b, _ := json.Marshal(v)
	var x any
	_ = json.Unmarshal(b, &x)
	b, _ = json.Marshal(x)
	return string(b)
}

// Le pré-chauffage ne vaut que si son préfixe est EXACTEMENT celui du vrai tour.
// Sinon le cache KV de llama-server ne se raccorde pas et on paie un préfill pour
// rien. Ce test ne compare donc PAS le pré-chauffage à ses propres fonctions : il
// lance un vrai premier message (StartTurn → generate → runChat) contre un faux
// moteur, relève le corps réellement envoyé, et exige que le bloc système et les
// outils du pré-chauffage du même mode lui soient identiques — pour chaque mode.
func TestPrewarmPrefixMatchesGenerate(t *testing.T) {
	// Prompt de preset : c'est le cas le plus courant en Projet et Jean, et il est
	// fusionné APRÈS le préambule par InjectSkills. Rapide ne l'utilise pas.
	const presetPrompt = "Réponds en français, sans préambule."

	for _, mode := range []string{"project", "fast", "jean"} {
		t.Run(mode, func(t *testing.T) {
			testHome(t)
			if err := setAgentEnabled(true); err != nil {
				t.Fatal(err)
			}
			if err := SetConfigKey("MEM_MODE", "always"); err != nil {
				t.Fatal(err)
			}
			if err := putStr(bkState, "sysprompt", presetPrompt); err != nil {
				t.Fatal(err)
			}
			spy := newEngineSpy(t)
			setConvEmpty(t, mode)
			rememberChatMode(mode)

			// Ce que le pré-chauffage enverrait pour la conversation vide ouverte.
			target, ok := prewarmTarget()
			if !ok || target != mode {
				t.Fatalf("prewarmTarget = %q, %v ; attendu %q", target, ok, mode)
			}
			system, tools, ok := prewarmPrefix(prewarmCaps(target))
			if !ok || system == "" || len(tools) == 0 {
				t.Fatalf("préfixe vide en mode %s : system=%q tools=%d", mode, system, len(tools))
			}
			want := prewarmPayload(resolveChatEndpoint(), system, tools)

			// Le vrai premier message, tel qu'handleChatSend le lance.
			body := chatReq{Mode: mode}
			m, _ := conv.lockMode(requestedMode(body))
			applyChatMode(&body, m)
			if err := conv.StartTurn("bonjour", nil, capsFromBody(body), 0.7); err != nil {
				t.Fatal(err)
			}
			sent := spy.next(t)
			conv.Stop() // on a le corps : le tour n'a plus rien à faire
			waitConvIdle(t)

			msgs, _ := sent["messages"].([]any)
			if len(msgs) < 2 {
				t.Fatalf("messages du vrai tour : %v", sent["messages"])
			}
			first, _ := msgs[0].(map[string]any)
			if first["role"] != "system" {
				t.Fatalf("le vrai tour ne commence pas par un system : %v", first)
			}
			wantMsgs, _ := want["messages"].([]Message)
			if got, exp := first["content"], wantMsgs[0].Content; got != exp {
				t.Fatalf("bloc système différent du vrai tour\n--- pré-chauffé ---\n%v\n--- réel ---\n%v", exp, got)
			}
			if got, exp := canon(sent["tools"]), canon(want["tools"]); got != exp {
				t.Fatalf("outils différents du vrai tour\n--- pré-chauffés ---\n%s\n--- réels ---\n%s", exp, got)
			}
		})
	}
}

// Les modes ne partagent pas leur préfixe : c'est ce qu'ignorait un préfixe bâti
// avec les réglages globaux de la machine. Rapide n'a ni mémoire ni prompt de
// preset, Jean a ses outils et son préambule.
func TestPrewarmPrefixDiffersByMode(t *testing.T) {
	testHome(t)
	if err := setAgentEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := SetConfigKey("MEM_MODE", "always"); err != nil {
		t.Fatal(err)
	}
	if err := putStr(bkState, "sysprompt", "Prompt de preset."); err != nil {
		t.Fatal(err)
	}
	sig := map[string]string{}
	for _, mode := range []string{"project", "fast", "jean"} {
		system, tools, ok := prewarmPrefix(prewarmCaps(mode))
		if !ok {
			t.Fatalf("mode %s : rien à chauffer", mode)
		}
		sig[mode] = prewarmSignature(chatEndpoint{URL: "u", Model: "m"}, system, tools)
	}
	if sig["project"] == sig["fast"] || sig["project"] == sig["jean"] || sig["fast"] == sig["jean"] {
		t.Fatalf("des modes différents donnent le même préfixe : %v", sig)
	}
}

// Modèle de base (ni agent ni mémoire) : aucun préfixe à chauffer, on ne doit pas
// envoyer de requête inutile au moteur — même si l'agent de la machine est actif.
func TestPrewarmPrefixEmptyForRawModel(t *testing.T) {
	testHome(t)
	if err := setAgentEnabled(true); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := prewarmPrefix(prewarmCaps("base")); ok {
		t.Fatal("pré-chauffage proposé pour le modèle de base, sans système ni outil")
	}
	// Agent coupé sur la machine : plus rien à chauffer non plus, quel que soit le mode.
	if err := setAgentEnabled(false); err != nil {
		t.Fatal(err)
	}
	if err := SetConfigKey("MEM_MODE", "off"); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := prewarmPrefix(prewarmCaps("project")); ok {
		t.Fatal("pré-chauffage proposé alors que l'agent est coupé")
	}
}

// La requête envoyée doit être minimale (un seul jeton) et porter le préfixe.
func TestPrewarmPayload(t *testing.T) {
	ep := chatEndpoint{URL: "http://127.0.0.1:1/v1/chat/completions", Model: "ajean"}
	tools := []Tool{bashTool()}
	p := prewarmPayload(ep, "SYSTEM", tools)
	if p["max_tokens"] != 1 {
		t.Fatalf("max_tokens = %v, attendu 1", p["max_tokens"])
	}
	if p["stream"] != false {
		t.Fatal("le pré-chauffage doit être non streamé")
	}
	if p["cache_prompt"] != true {
		t.Fatal("cache_prompt doit être vrai pour réutiliser le préfixe")
	}
	msgs, _ := p["messages"].([]Message)
	if len(msgs) != 2 || msgs[0].Role != "system" || msgs[0].Content != "SYSTEM" || msgs[1].Role != "user" {
		t.Fatalf("messages inattendus : %+v", msgs)
	}
	if _, ok := p["tools"].([]Tool); !ok {
		t.Fatalf("outils absents du corps : %T", p["tools"])
	}

	// Sans système ni outils, on n'envoie que le message utilisateur minimal.
	p = prewarmPayload(ep, "", nil)
	msgs, _ = p["messages"].([]Message)
	if len(msgs) != 1 || msgs[0].Role != "user" {
		t.Fatalf("messages inattendus sans préfixe : %+v", msgs)
	}
	if _, ok := p["tools"]; ok {
		t.Fatal("la clé tools ne doit pas être envoyée quand il n'y a aucun outil")
	}
}

// Seule une conversation active VIDE peut être pré-chauffée : avec un seul créneau
// de cache (-np 1), chauffer pendant une conversation écraserait son cache et le
// message suivant repaierait tout l'historique.
func TestPrewarmTarget(t *testing.T) {
	testHome(t)
	rememberChatMode("fast")

	// Conversation neuve, dernier mode connu : OK.
	setConvEmpty(t, "")
	if m, ok := prewarmTarget(); !ok || m != "fast" {
		t.Fatalf("conversation vide : %q, %v", m, ok)
	}

	// Conversation commencée : jamais.
	conv.mu.Lock()
	conv.Messages = []Message{{Role: "user", Content: "salut"}}
	conv.mu.Unlock()
	if m, ok := prewarmTarget(); ok {
		t.Fatalf("conversation en cours pré-chauffée (%q)", m)
	}

	// Tour en cours (ou tâche de fond qui tient le verrou de génération) : jamais.
	resetConvEmpty("")
	conv.mu.Lock()
	conv.Generating = true
	conv.mu.Unlock()
	if m, ok := prewarmTarget(); ok {
		t.Fatalf("génération en cours pré-chauffée (%q)", m)
	}

	// Le mode de la conversation prime sur le dernier mode envoyé.
	resetConvEmpty("")
	conv.mu.Lock()
	conv.Mode = "project"
	conv.mu.Unlock()
	if m, ok := prewarmTarget(); !ok || m != "project" {
		t.Fatalf("mode de la conversation : %q, %v", m, ok)
	}

	// Jean : sa conversation, vide, est chauffée avec SON préfixe…
	resetConvEmpty("jean")
	if m, ok := prewarmTarget(); !ok || m != "jean" {
		t.Fatalf("Jean vide : %q, %v", m, ok)
	}
	// …mais pas quand Jean est le dernier mode et qu'une conversation ordinaire est
	// ouverte (le premier message ouvrira la conversation de Jean, d'historique inconnu)…
	rememberChatMode("jean")
	resetConvEmpty("")
	if m, ok := prewarmTarget(); ok {
		t.Fatalf("conversation ordinaire pré-chauffée avec le mode Jean (%q)", m)
	}
	// …ni l'inverse : la conversation de Jean ouverte, un autre mode demandé.
	rememberChatMode("fast")
	resetConvEmpty("jean")
	conv.mu.Lock()
	conv.Mode = ""
	conv.mu.Unlock()
	if m, ok := prewarmTarget(); ok {
		t.Fatalf("conversation de Jean pré-chauffée avec le mode %q", m)
	}

	// Mode inconnu (rien envoyé encore) : on ne devine pas.
	if err := putStr(bkState, lastChatModeKey, ""); err != nil {
		t.Fatal(err)
	}
	resetConvEmpty("")
	if m, ok := prewarmTarget(); ok {
		t.Fatalf("mode inconnu pré-chauffé (%q)", m)
	}
}

// Bout en bout : LLMPrewarm n'envoie RIEN tant que la conversation est en cours,
// puis une requête quand une conversation vide est ouverte — et ne la rejoue pas
// au sondage suivant.
func TestLLMPrewarmWaitsForEmptyConversation(t *testing.T) {
	testHome(t)
	if err := setAgentEnabled(true); err != nil {
		t.Fatal(err)
	}
	if err := SetConfigKey("MEM_MODE", "on"); err != nil {
		t.Fatal(err)
	}
	spy := newEngineSpy(t)
	resetWarmState(t)
	rememberChatMode("project")
	setConvEmpty(t, "")

	// Conversation en cours : le moteur ne doit rien recevoir, même moteur sain.
	conv.mu.Lock()
	conv.Messages = []Message{{Role: "user", Content: "salut"}, {Role: "assistant", Content: "bonjour"}}
	conv.mu.Unlock()
	LLMPrewarm(true)
	spy.none(t, 300*time.Millisecond, "conversation en cours")

	// Redémarrage de l'UI / du moteur pendant la conversation : toujours rien.
	LLMPrewarm(false)
	LLMPrewarm(true)
	spy.none(t, 300*time.Millisecond, "conversation en cours, moteur redémarré")

	// Nouvelle conversation, vide : cette fois on chauffe, avec le préfixe du mode.
	resetConvEmpty("")
	LLMPrewarm(true)
	body := spy.next(t)
	if body["max_tokens"] != float64(1) {
		t.Fatalf("max_tokens = %v, attendu 1", body["max_tokens"])
	}
	system, tools, _ := prewarmPrefix(prewarmCaps("project"))
	msgs, _ := body["messages"].([]any)
	first, _ := msgs[0].(map[string]any)
	if first["role"] != "system" || first["content"] != system {
		t.Fatalf("préfixe chauffé inattendu : %v", first)
	}
	if canon(body["tools"]) != canon(tools) {
		t.Fatal("outils chauffés différents de ceux du mode")
	}

	// Laisse la goroutine refermer son état (inflight) avant le second appel.
	deadline := time.Now().Add(2 * time.Second)
	for {
		llmWarm.mu.Lock()
		inflight := llmWarm.inflight
		llmWarm.mu.Unlock()
		if !inflight || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	LLMPrewarm(true)
	spy.none(t, 300*time.Millisecond, "même préfixe déjà chauffé")
}
