package ajean

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
)

// llm_prefix_stable_test.go — le PRÉFIXE de la requête reste-t-il identique d'un
// appel au suivant ?
//
// llama-server ne reprend son cache qu'à partir du premier jeton qui diffère :
// tout ce qui change en tête (bloc système, schémas d'outils, premier message)
// fait relire le fil ENTIER. La mémoire (profil de Jean, index des pages, suivis)
// est donc injectée une fois, en tête du premier message utilisateur, et ce qui
// change ensuite est annoncé dans le message du tour (jean_notes.go) — jamais
// réécrit plus haut. Ces tests le MESURENT sur de vrais tours (StartTurn →
// generate → runChat) contre un faux moteur : ils relèvent chaque corps
// réellement envoyé et comparent deux appels successifs.

// scriptedEngine est un faux llama-server qui répond selon un script, une entrée
// par complétion : soit un appel d'outil, soit un texte. Chaque corps reçu est
// gardé, dans l'ordre.
type scriptedEngine struct {
	mu     sync.Mutex
	bodies []map[string]any
	script []scriptedReply
}

type scriptedReply struct {
	tool, args string // appel d'outil (args = JSON)…
	text       string // …ou réponse en texte
}

func newScriptedEngine(t *testing.T, script ...scriptedReply) *scriptedEngine {
	t.Helper()
	e := &scriptedEngine{script: script}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.WriteHeader(http.StatusOK)
		case "/v1/chat/completions":
			var body map[string]any
			b, _ := io.ReadAll(r.Body)
			_ = json.Unmarshal(b, &body)
			if stream, _ := body["stream"].(bool); !stream {
				// Pré-chauffage ou appel interne non streamé : hors mesure.
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":""}}]}`))
				return
			}
			e.mu.Lock()
			n := len(e.bodies)
			e.bodies = append(e.bodies, body)
			rep := scriptedReply{text: "ok"}
			if n < len(e.script) {
				rep = e.script[n]
			}
			e.mu.Unlock()
			w.Header().Set("Content-Type", "text/event-stream")
			send := func(v any) {
				j, _ := json.Marshal(v)
				_, _ = w.Write([]byte("data: " + string(j) + "\n\n"))
			}
			if rep.tool != "" {
				send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"tool_calls": []any{map[string]any{
					"index": 0, "id": "call_test_" + rep.tool, "function": map[string]any{"name": rep.tool, "arguments": rep.args},
				}}}}}})
				send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "tool_calls"}}})
			} else {
				send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{"content": rep.text}}}})
				send(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{}, "finish_reason": "stop"}}})
			}
			_, _ = w.Write([]byte("data: [DONE]\n\n"))
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
	return e
}

// requests : les corps reçus jusqu'ici (copie).
func (e *scriptedEngine) requests() []map[string]any {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]map[string]any(nil), e.bodies...)
}

// promptUnits découpe un corps de requête en unités dans l'ordre où le gabarit de
// chat les rend : les schémas d'outils (avec le bloc système, en tête de prompt),
// puis chaque message.
func promptUnits(body map[string]any) []string {
	units := []string{canon(body["tools"])}
	msgs, _ := body["messages"].([]any)
	for _, m := range msgs {
		units = append(units, canon(m))
	}
	return units
}

// prefixReuse mesure ce que la requête next reprend de la requête prev : le
// nombre d'unités de tête identiques et la part des caractères de prev que cela
// représente (1 = tout le prompt précédent est repris tel quel, le moteur ne
// calcule que la suite).
func prefixReuse(prev, next map[string]any) (same, total int, share float64) {
	a, b := promptUnits(prev), promptUnits(next)
	all, kept := 0, 0
	for _, u := range a {
		all += len(u)
	}
	for same < len(a) && same < len(b) && a[same] == b[same] {
		kept += len(a[same])
		same++
	}
	if same < len(a) && same < len(b) {
		// Unité qui diverge : compter son début commun (le cache reprend au jeton près).
		x, y := a[same], b[same]
		n := 0
		for n < len(x) && n < len(y) && x[n] == y[n] {
			n++
		}
		kept += n
	}
	if all == 0 {
		return same, len(a), 1
	}
	return same, len(a), float64(kept) / float64(all)
}

// sendTurn envoie un message comme handleChatSend et attend la fin du tour.
func sendTurn(t *testing.T, mode, text string) {
	t.Helper()
	body := chatReq{Mode: mode}
	m, _ := conv.lockMode(requestedMode(body))
	applyChatMode(&body, m)
	if err := conv.StartTurn(text, nil, capsFromBody(body), 0.7); err != nil {
		t.Fatal(err)
	}
	waitConvIdle(t)
}

// requireFullReuse exige que la requête next reprenne TOUTE la requête prev.
func requireFullReuse(t *testing.T, what string, prev, next map[string]any) {
	t.Helper()
	same, total, share := prefixReuse(prev, next)
	t.Logf("%s : %d/%d unités de tête identiques, %.1f %% du prompt précédent repris", what, same, total, share*100)
	if same != total {
		a, b := promptUnits(prev), promptUnits(next)
		t.Fatalf("%s : le préfixe diverge à l'unité %d (0 = outils, 1 = système)\n--- avant ---\n%s\n--- après ---\n%s",
			what, same, clipRunes(a[same], 1500), clipRunes(b[same], 1500))
	}
}

// Mode Projet, mémoire injectée : le modèle AJOUTE une page de mémoire pendant le
// premier tour (l'index MEMORY.md change donc sur le disque), puis l'utilisateur
// envoie un second message. Rien de ce qui précède ne doit avoir bougé.
func TestPrefixStableAcrossTurnsProject(t *testing.T) {
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
	if err := MemAdd("gouts.md", "# Goûts\n\nAime le thé vert."); err != nil {
		t.Fatal(err)
	}
	eng := newScriptedEngine(t,
		scriptedReply{tool: "mem_add", args: `{"file":"prenom.md","content":"# Prénom\n\nAlice."}`},
		scriptedReply{text: "C'est noté, Alice."},
		scriptedReply{text: "Tu aimes le thé vert."},
	)
	setConvEmpty(t, "project")

	sendTurn(t, "project", "je m'appelle Alice")
	sendTurn(t, "project", "qu'est-ce que j'aime boire ?")

	reqs := eng.requests()
	if len(reqs) != 3 {
		t.Fatalf("%d requêtes reçues par le moteur, 3 attendues", len(reqs))
	}
	// La page a bien été écrite : l'index sur disque n'est plus celui du début.
	if m, ok := memIndexMessage(); !ok || !strings.Contains(m.Content.(string), "prenom.md") {
		t.Fatalf("la page ajoutée n'est pas dans l'index : %v", m.Content)
	}
	requireFullReuse(t, "projet, étape d'outil → suite du tour", reqs[0], reqs[1])
	requireFullReuse(t, "projet, tour 1 → tour 2", reqs[1], reqs[2])

	// Même contenu, autre place : la mémoire est bien sous les yeux du modèle, dans
	// le premier message utilisateur et pas dans le bloc système.
	msgs, _ := reqs[2]["messages"].([]any)
	sys, _ := msgs[0].(map[string]any)["content"].(string)
	first, _ := msgs[1].(map[string]any)["content"].(string)
	if strings.Contains(sys, "gouts.md") || strings.Contains(sys, memIndexPrefix) {
		t.Fatal("l'index mémoire est dans le bloc système")
	}
	if !strings.Contains(first, "gouts.md") {
		t.Fatalf("l'index mémoire a disparu du contexte : %q", first)
	}
}

// Mode Jean : le modèle RETIENT un fait pendant le premier tour (le profil change
// sur le disque), puis l'arrière-plan apprend autre chose avant le second message.
// Le préfixe ne bouge pas ; le changement arrive dans le message du tour.
func TestPrefixStableAcrossTurnsJean(t *testing.T) {
	testHome(t)
	if err := setAgentEnabled(true); err != nil {
		t.Fatal(err)
	}
	// Pas de révision programmée en fin de tour : son minuteur survivrait au test.
	if err := SetConfigKey("JEAN_REFLECT", "off"); err != nil {
		t.Fatal(err)
	}
	if _, err := JeanRemember("ville", "Lyon"); err != nil {
		t.Fatal(err)
	}
	eng := newScriptedEngine(t,
		scriptedReply{tool: "jean_remember", args: `{"description":"Je retiens son prénom.","key":"prenom","value":"Alice"}`},
		scriptedReply{text: "Enchanté, Alice."},
		scriptedReply{text: "Tu habites Lyon."},
	)
	setConvEmpty(t, "jean")
	jeanTakeNotes(true) // file des notes d'arrière-plan : globale au process

	sendTurn(t, "jean", "je m'appelle Alice")
	// Ce que la révision ou le sommeil auraient appris entre les deux messages.
	if _, err := JeanRemember("metier", "boulangère"); err != nil {
		t.Fatal(err)
	}
	jeanQueueNotes([]string{jeanNoteFor("jean_remember", "metier")})
	sendTurn(t, "jean", "j'habite où ?")

	reqs := eng.requests()
	if len(reqs) != 3 {
		t.Fatalf("%d requêtes reçues par le moteur, 3 attendues", len(reqs))
	}
	if ctx := jeanContextMessage().Content.(string); !strings.Contains(ctx, "prenom: Alice") || !strings.Contains(ctx, "metier: boulangère") {
		t.Fatalf("le profil n'a pas été mis à jour : %q", ctx)
	}
	requireFullReuse(t, "Jean, étape d'outil → suite du tour", reqs[0], reqs[1])
	requireFullReuse(t, "Jean, tour 1 → tour 2", reqs[1], reqs[2])

	msgs, _ := reqs[2]["messages"].([]any)
	sys, _ := msgs[0].(map[string]any)["content"].(string)
	first, _ := msgs[1].(map[string]any)["content"].(string)
	last, _ := msgs[len(msgs)-1].(map[string]any)["content"].(string)
	if strings.Contains(sys, "ville: Lyon") || strings.Contains(sys, "prenom: Alice") {
		t.Fatal("le profil de Jean est dans le bloc système")
	}
	if !strings.Contains(first, "ville: Lyon") {
		t.Fatalf("le profil a disparu du contexte : %q", first)
	}
	if !strings.Contains(last, "profile metier updated") || !strings.HasSuffix(last, "j'habite où ?") {
		t.Fatalf("le changement de mémoire n'est pas annoncé dans le message du tour : %q", last)
	}
}

// Garde-fou de la mesure elle-même : un changement en tête doit être vu.
func TestPrefixReuseMeasure(t *testing.T) {
	mk := func(sys string, rest ...string) map[string]any {
		msgs := []any{map[string]any{"role": "system", "content": sys}}
		for _, r := range rest {
			msgs = append(msgs, map[string]any{"role": "user", "content": r})
		}
		return map[string]any{"tools": []any{}, "messages": msgs}
	}
	if same, total, share := prefixReuse(mk("S", "a"), mk("S", "a", "b")); same != total || share != 1 {
		t.Fatalf("préfixe identique mal mesuré : %d/%d, %.2f", same, total, share)
	}
	same, total, share := prefixReuse(mk("S profil: A", strings.Repeat("x", 1000)), mk("S profil: B", strings.Repeat("x", 1000), "b"))
	if same != 1 || total != 3 || share > 0.1 {
		t.Fatalf("changement du bloc système non vu : %d/%d, %.2f", same, total, share)
	}
}
