package agent

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"ajean/internal/config"
	"ajean/internal/home"
	"ajean/internal/llm"
	"ajean/internal/memory"
	"ajean/internal/tasks"
	"ajean/internal/workspace"
)

// fakeAPI : une API compatible OpenAI dont les réponses sont écrites d'avance.
// Chaque requête reçue consomme la réponse suivante.
type fakeAPI struct {
	mu       sync.Mutex
	replies  []llm.Message
	requests [][]llm.Message // messages reçus à chaque requête
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Messages []llm.Message `json:"messages"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	f.mu.Lock()
	f.requests = append(f.requests, req.Messages)
	var m llm.Message
	if len(f.replies) > 0 {
		m, f.replies = f.replies[0], f.replies[1:]
	} else {
		m = llm.Message{Content: "OK"}
	}
	f.mu.Unlock()

	w.Header().Set("Content-Type", "text/event-stream")
	send := func(delta map[string]any) {
		raw, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": delta}}})
		fmt.Fprintf(w, "data: %s\n\n", raw)
	}
	// Le texte part en deux morceaux, pour exercer la lecture du flux.
	if half := len(m.Content) / 2; m.Content != "" {
		send(map[string]any{"content": m.Content[:half]})
		send(map[string]any{"content": m.Content[half:]})
	}
	for i, tc := range m.ToolCalls {
		send(map[string]any{"tool_calls": []any{map[string]any{
			"index": i, "id": tc.ID, "function": map[string]string{"name": tc.Function.Name, "arguments": tc.Function.Arguments},
		}}})
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
}

func (f *fakeAPI) lastRequest() []llm.Message {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.requests[len(f.requests)-1]
}

func toolCall(id, name, args string) llm.ToolCall {
	return llm.ToolCall{ID: id, Type: "function", Function: llm.FunctionCall{Name: name, Arguments: args}}
}

// setup prépare un dossier de données vide et branche l'agent sur une fausse API.
func setup(t *testing.T, replies ...llm.Message) *fakeAPI {
	t.Helper()
	api := &fakeAPI{replies: replies}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	t.Setenv("AJEAN_HOME", t.TempDir())
	t.Setenv("AJEAN_API_URL", srv.URL)
	t.Setenv("AJEAN_MODEL", "test")
	if err := workspace.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workspace.Close() }) // sinon Windows refuse de supprimer le dossier temporaire
	convs.Lock()
	convs.byID = map[string]*conversation{}
	convs.Unlock()
	return api
}

// waitFor attend l'événement de fin de tour d'une conversation et le renvoie.
func waitFor(t *testing.T, events <-chan []byte, conv string) event {
	t.Helper()
	deadline := time.After(10 * time.Second)
	for {
		select {
		case raw := <-events:
			var ev event
			_ = json.Unmarshal(raw, &ev)
			if ev.Conv == conv && (ev.Type == "done" || ev.Type == "error" || ev.Type == "stopped") {
				return ev
			}
		case <-deadline:
			t.Fatal("pas de fin de tour")
		}
	}
}

func TestTurnUsesToolsAndRemembers(t *testing.T) {
	api := setup(t,
		llm.Message{ToolCalls: []llm.ToolCall{toolCall("c1", "memory_remember", `{"key":"prenom","value":"Alice"}`)}},
		llm.Message{Content: "Enchanté Alice, c'est noté."},
	)
	events, cancel := Subscribe()
	defer cancel()

	id := New()
	if err := Send(id, "Bonjour, je m'appelle Alice", nil); err != nil {
		t.Fatal(err)
	}
	ev := waitFor(t, events, id)
	if ev.Type != "done" || ev.Text != "Enchanté Alice, c'est noté." {
		t.Fatalf("fin de tour inattendue : %+v", ev)
	}

	facts, err := memory.Profile()
	if err != nil || len(facts) != 1 || facts[0].Key != "prenom" || facts[0].Value != "Alice" {
		t.Fatalf("profil = %+v, %v", facts, err)
	}
	// Le second appel au modèle voit le résultat de l'outil et le profil à jour.
	req := api.lastRequest()
	if !strings.Contains(req[0].Content, "prenom: Alice") {
		t.Error("le message système ne contient pas le profil mis à jour")
	}
	if last := req[len(req)-1]; last.Role != "tool" || !strings.HasPrefix(last.Content, "[ok]") {
		t.Errorf("dernier message envoyé = %+v", last)
	}
	// Le fil est un fichier Markdown lisible, et l'échange est au journal.
	raw, err := os.ReadFile(filepath.Join(home.Memory(), "conversations", id+".md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"# Bonjour, je m'appelle Alice", "· user", "· assistant", "Enchanté Alice"} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("le fichier de la conversation ne contient pas %q", want)
		}
	}
	if entries, _ := memory.Journal("enchante", 10); len(entries) != 1 {
		t.Errorf("journal : %d entrée(s) pour « enchante », 1 attendue", len(entries))
	}
}

func TestConversationSurvivesRestart(t *testing.T) {
	setup(t, llm.Message{Content: "Paris."})
	events, cancel := Subscribe()
	defer cancel()
	id := New()
	if err := Send(id, "Quelle est la capitale de la France ?", []string{"uploads/carte.pdf"}); err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, id)

	convs.Lock()
	convs.byID = map[string]*conversation{}
	convs.Unlock()
	Load()

	info, log, ok := Thread(id)
	if !ok || info.Title != "Quelle est la capitale de la France ?" || len(log) != 2 {
		t.Fatalf("après relecture : ok=%v info=%+v log=%+v", ok, info, log)
	}
	if log[0].Text != "Quelle est la capitale de la France ?" || len(log[0].Files) != 1 || log[0].Files[0] != "uploads/carte.pdf" {
		t.Errorf("message de l'utilisateur relu = %+v", log[0])
	}
	if log[1].Role != "assistant" || log[1].Text != "Paris." {
		t.Errorf("réponse relue = %+v", log[1])
	}
	if c := get(id); len(c.msgs) != 2 || !strings.Contains(c.msgs[0].Content, "capitale") {
		t.Errorf("contexte reconstruit = %+v", c.msgs)
	}
}

// Après un tour raté, le modèle doit encore savoir ce que l'utilisateur a
// demandé (sinon « réessaie » ne veut rien dire), et la conversation est libre.
func TestFailedTurnKeepsTheRequest(t *testing.T) {
	setup(t)
	t.Setenv("AJEAN_API_URL", "http://127.0.0.1:1") // personne n'écoute
	events, cancel := Subscribe()
	defer cancel()
	id := New()
	if err := Send(id, "Allô ?", nil); err != nil {
		t.Fatal(err)
	}
	if ev := waitFor(t, events, id); ev.Type != "error" {
		t.Fatalf("événement = %+v, erreur attendue", ev)
	}
	c := get(id)
	if c.busy || len(c.msgs) != 2 || !strings.Contains(c.msgs[0].Content, "Allô ?") || c.msgs[1].Role != "assistant" {
		t.Errorf("après un tour raté : busy=%v msgs=%+v", c.busy, c.msgs)
	}
	if _, log, _ := Thread(id); len(log) != 2 || log[1].Kind != kindFailed {
		t.Errorf("fil après un tour raté = %+v", log)
	}
	if err := Send(id, "Toujours là ?", nil); err != nil {
		t.Errorf("la conversation devrait être de nouveau libre : %v", err)
	}
	waitFor(t, events, id)
}

func TestTrimKeepsToolCallsWithTheirResults(t *testing.T) {
	setup(t)
	big := strings.Repeat("x", 60_000)
	c := &conversation{id: "t"}
	for i := 0; i < 6; i++ {
		c.msgs = append(c.msgs,
			llm.Message{Role: "user", Content: big},
			llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c", "ws_list", "{}")}},
			llm.Message{Role: "tool", ToolCallID: "c", Content: "ok"},
			llm.Message{Role: "assistant", Content: "fait"},
		)
	}
	c.trimLocked()
	if c.msgs[0].Role != "user" {
		t.Fatalf("le contexte doit commencer par un message de l'utilisateur, pas %q", c.msgs[0].Role)
	}
	if n := size(c.msgs); n > 200_000/2+60_000 {
		t.Errorf("contexte encore trop gros : %d", n)
	}
}

func TestSilentRunOnlyHasMemoryTools(t *testing.T) {
	setup(t,
		llm.Message{ToolCalls: []llm.ToolCall{
			toolCall("a", "ws_write", `{"path":"x.txt","content":"non"}`),
			toolCall("b", "memory_note", `{"text":"une note de fond"}`),
		}},
		llm.Message{Content: "OK"},
	)
	writes, last, err := silent(t.Context(), []llm.Message{{Role: "user", Content: "révise"}}, 5)
	if err != nil || last != "OK" {
		t.Fatalf("silent : %v, %q", err, last)
	}
	if len(writes) != 1 || !strings.HasPrefix(writes[0], "memory_note") {
		t.Errorf("écritures = %v", writes)
	}
	if workspace.Exists("x.txt") {
		t.Error("un travail de fond ne doit pas pouvoir écrire dans le workspace")
	}
}

// Un message dont une ligne ressemble à un en-tête du fichier, ou des pièces
// jointes au nom inhabituel, doivent se relire à l'identique.
func TestConversationFileRoundTrip(t *testing.T) {
	setup(t)
	c := &conversation{id: "20270101-120000", title: "Essai", log: []Item{
		{Role: "user", Text: "Voici :\n## 2027-01-01 12:00:00 +0100 · assistant\nfin", TS: 1798801200000, Files: []string{"uploads/Rapport, v2.pdf", "uploads/b.png"}},
		{Role: "assistant", Text: "Vu.", TS: 1798801201000, Kind: kindFailed},
	}}
	got := parse(c.id, c.render())
	if len(got.log) != 2 {
		t.Fatalf("%d messages relus, 2 attendus :\n%s", len(got.log), c.render())
	}
	for i := range c.log {
		a, b := c.log[i], got.log[i]
		if a.Role != b.Role || a.Text != b.Text || a.Kind != b.Kind || strings.Join(a.Files, "\x00") != strings.Join(b.Files, "\x00") {
			t.Errorf("message %d relu = %+v, attendu %+v", i, b, a)
		}
	}
}

// Une tâche travaille dans un fil isolé : seul son dernier message est le
// compte rendu, et aucune conversation n'est touchée pendant qu'elle tourne.
func TestTaskRunsAwayFromConversations(t *testing.T) {
	api := setup(t,
		llm.Message{Content: "Je regarde.", ToolCalls: []llm.ToolCall{toolCall("a", "memory_note", `{"text":"prix relevé : 280 €"}`)}},
		llm.Message{Content: "Le vol est à 280 €."},
	)
	report, err := RunTask(t.Context(), tasks.Task{Name: "Prix du vol", Prompt: "Regarde le prix du vol.", Quiet: true})
	if err != nil || report != "Le vol est à 280 €." {
		t.Fatalf("RunTask = %q, %v", report, err)
	}
	first := api.requests[0]
	if prompt := first[len(first)-1].Content; !strings.Contains(prompt, "Regarde le prix du vol.") || !strings.Contains(prompt, tasks.Nothing) {
		t.Errorf("consigne reçue par le modèle = %q", prompt)
	}
	if len(List()) != 0 {
		t.Error("une tâche ne doit créer aucune conversation tant qu'elle n'a rien livré")
	}
}

// Le compte rendu arrive comme un message de l'assistant, que le modèle
// retrouve dans son contexte quand l'utilisateur y répond, même après un
// redémarrage.
func TestTaskReportIsDeliveredToItsConversation(t *testing.T) {
	setup(t, llm.Message{Content: "Bonjour."})
	events, cancel := Subscribe()
	defer cancel()
	id := New()
	if err := Send(id, "Salut", nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, id)

	DeliverTask(tasks.Task{Name: "Linge", Conv: id}, "C'est l'heure de sortir le linge.")
	if ev := waitFor(t, events, id); ev.Kind != kindTask || ev.Text != "C'est l'heure de sortir le linge." {
		t.Fatalf("événement de livraison = %+v", ev)
	}
	check := func(when string) {
		t.Helper()
		_, log, _ := Thread(id)
		if len(log) != 3 || log[2].Kind != kindTask || log[2].Role != "assistant" {
			t.Fatalf("%s : fil = %+v", when, log)
		}
		msgs := get(id).msgs
		if n := len(msgs); n != 4 || msgs[n-2].Role != "user" || msgs[n-1].Content != "C'est l'heure de sortir le linge." {
			t.Errorf("%s : contexte = %+v", when, msgs)
		}
	}
	check("après livraison")

	convs.Lock()
	convs.byID = map[string]*conversation{}
	convs.Unlock()
	Load()
	check("après redémarrage")
}

// Sans conversation où livrer, le compte rendu en ouvre une.
func TestTaskReportOpensAConversationWhenNoneExists(t *testing.T) {
	setup(t)
	DeliverTask(tasks.Task{Name: "Veille"}, "Rien de neuf aujourd'hui.")
	l := List()
	if len(l) != 1 || l[0].Title != taskTitle {
		t.Fatalf("conversations = %+v", l)
	}
}

// L'effort de réflexion choisi part avec chaque demande ; sur « auto », le
// paramètre n'est pas envoyé du tout (certaines API le refusent).
func TestEffortIsSentOnlyWhenChosen(t *testing.T) {
	var bodies []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var b map[string]any
		_ = json.NewDecoder(r.Body).Decode(&b)
		bodies = append(bodies, b)
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
	}))
	defer srv.Close()
	ep := llm.Endpoint{URL: srv.URL, Model: "m"}
	msgs := []llm.Message{{Role: "user", Content: "salut"}}
	if _, err := llm.Chat(t.Context(), ep, msgs, nil, nil); err != nil {
		t.Fatal(err)
	}
	ep.Effort = "high"
	if _, err := llm.Chat(t.Context(), ep, msgs, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, sent := bodies[0]["reasoning_effort"]; sent {
		t.Error("sur auto, reasoning_effort ne doit pas être envoyé")
	}
	if got := bodies[1]["reasoning_effort"]; got != "high" {
		t.Errorf("reasoning_effort envoyé = %v, attendu high", got)
	}
}

func TestVisibleAnswer(t *testing.T) {
	long := strings.Repeat("Voici le tableau complet. ", 20)
	cases := []struct {
		name  string
		texts []string
		want  string
	}{
		{"rien", nil, ""},
		{"une seule réponse", []string{"Paris."}, "Paris."},
		{"commentaires en chemin puis la réponse", []string{"Je cherche.", "Ce site bloque, j'essaie ailleurs.", long}, long},
		{"réponse puis un mot de la fin", []string{"Je regarde.", long, "C'est noté."}, long + "\n\nC'est noté."},
	}
	for _, c := range cases {
		if got := visibleAnswer(c.texts); got != c.want {
			t.Errorf("%s : %q, attendu %q", c.name, got, c.want)
		}
	}
}

func TestClipResultKeepsBothEnds(t *testing.T) {
	short := "court"
	if clipResult(short) != short {
		t.Error("un résultat court ne doit pas être touché")
	}
	long := "DEBUT" + strings.Repeat("é", maxToolResult*2) + "FIN"
	got := clipResult(long)
	if n := len([]rune(got)); n > maxToolResult+120 {
		t.Errorf("résultat écourté : %d caractères", n)
	}
	if !strings.HasPrefix(got, "DEBUT") || !strings.HasSuffix(got, "FIN") || !strings.Contains(got, "caractères retirés") {
		t.Error("le début, la fin et la mention de la coupe doivent rester")
	}
}

func TestLightenEmptiesOldToolResultsOnly(t *testing.T) {
	big := strings.Repeat("x", 10_000)
	var msgs []llm.Message
	msgs = append(msgs, llm.Message{Role: "user", Content: "fais le travail"})
	for i := 0; i < 10; i++ {
		msgs = append(msgs,
			llm.Message{Role: "assistant", ToolCalls: []llm.ToolCall{toolCall("c", "ws_read", "{}")}},
			llm.Message{Role: "tool", ToolCallID: "c", Content: big})
	}
	lighten(msgs, 50_000)
	if n := size(msgs); n > 70_000 {
		t.Errorf("fil encore trop gros : %d", n)
	}
	if msgs[2].Content != emptiedResult {
		t.Error("le plus vieux résultat aurait dû être vidé")
	}
	for i := len(msgs) - 1; i > len(msgs)-2*keepRecentResults; i -= 2 {
		if msgs[i].Content != big {
			t.Fatalf("un résultat récent (message %d) a été vidé", i)
		}
	}
	if msgs[0].Content != "fais le travail" {
		t.Error("le message de l'utilisateur ne doit pas être touché")
	}
}

func TestTooLong(t *testing.T) {
	yes := []string{
		"This model's maximum context length is 131072 tokens. However, your messages resulted in 140000 tokens.",
		"Input is too long for requested model",
		"context window exceeded",
		"prompt is too long: 210000 tokens > 200000 maximum",
	}
	for _, m := range yes {
		if !tooLong(&llm.APIError{Status: 400, Message: m}) {
			t.Errorf("devrait être reconnu comme un dépassement : %q", m)
		}
	}
	if tooLong(&llm.APIError{Status: 400, Message: "unknown parameter: reasoning_effort"}) {
		t.Error("un autre refus n'est pas un dépassement")
	}
	if tooLong(&llm.APIError{Status: 429, Message: "too many tokens per minute"}) {
		t.Error("un quota dépassé n'est pas un dépassement de contexte")
	}
}

// Quand une conversation dépasse le budget, les vieux échanges sortent du
// contexte et sont remplacés par un résumé, que le modèle reçoit ensuite en
// tête de chaque tour et qui survit à un redémarrage.
func TestOldExchangesAreSummarized(t *testing.T) {
	api := setup(t)
	t.Cleanup(func() { _ = config.Update(func(c *config.Config) error { c.MaxContextChars = 200_000; return nil }) })
	if err := config.Update(func(c *config.Config) error { c.MaxContextChars = 2_000; return nil }); err != nil {
		t.Fatal(err)
	}
	events, cancel := Subscribe()
	defer cancel()
	id := New()
	say := func(text, answer string) {
		t.Helper()
		api.mu.Lock()
		api.replies = append(api.replies, llm.Message{Content: answer})
		api.mu.Unlock()
		if err := Send(id, text, nil); err != nil {
			t.Fatal(err)
		}
		waitFor(t, events, id)
	}
	say("Mon chien s'appelle Pixel. "+strings.Repeat("blabla ", 150), "Noté. "+strings.Repeat("ok ", 150))

	// Le tour suivant fait déborder : le premier échange sort, la fausse API reçoit la demande de résumé.
	api.mu.Lock()
	api.replies = append(api.replies, llm.Message{Content: "Suite."}, llm.Message{Content: "RÉSUMÉ : le chien de l'utilisateur s'appelle Pixel."})
	api.mu.Unlock()
	if err := Send(id, "Autre sujet. "+strings.Repeat("blabla ", 150), nil); err != nil {
		t.Fatal(err)
	}
	waitFor(t, events, id)
	c := get(id)
	deadline := time.Now().Add(5 * time.Second)
	for {
		c.mu.Lock()
		summary := c.summary
		c.mu.Unlock()
		if summary != "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("pas de résumé")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if req := api.lastRequest(); !strings.Contains(req[len(req)-1].Content, "Pixel") || !strings.Contains(req[len(req)-1].Content, "PART LEAVING THE CONTEXT") {
		t.Errorf("la demande de résumé ne contient pas l'échange sorti : %.200s", req[len(req)-1].Content)
	}

	say("Comment s'appelle mon chien ?", "Pixel.")
	req := api.lastRequest()
	if len(req) < 3 || !strings.Contains(req[1].Content, "RÉSUMÉ : le chien") || req[2].Content != summaryAck {
		t.Fatalf("le résumé n'est pas en tête du contexte : %+v", req[:min(3, len(req))])
	}
	c.mu.Lock()
	stored := c.msgs
	c.mu.Unlock()
	for _, m := range stored {
		if strings.HasPrefix(m.Content, summaryNote) {
			t.Error("le résumé ne doit pas s'accumuler dans les messages gardés")
		}
	}

	convs.Lock()
	convs.byID = map[string]*conversation{}
	convs.Unlock()
	Load()
	if got := get(id).summary; !strings.Contains(got, "Pixel") {
		t.Errorf("résumé après redémarrage = %q", got)
	}
}
