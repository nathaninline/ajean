package apps

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"ajean/internal/home"
	"ajean/internal/tool"
	"ajean/internal/workspace"
)

// bridge appelle le pont comme le ferait la page d'AJEAN, après un passage par
// JSON : les arguments arrivent du navigateur sous cette forme.
func bridge(t *testing.T, id, op, args string) (any, error) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(args), &m); err != nil {
		t.Fatal(err)
	}
	res, err := Bridge(context.Background(), id, op, m)
	if err == nil {
		if _, jerr := json.Marshal(res); jerr != nil {
			t.Errorf("%s : résultat non sérialisable (%v)", op, jerr)
		}
	}
	return res, err
}

// writeTool pose un outil de l'assistant dans TOOLS/.
func writeTool(t *testing.T, name, code string) {
	t.Helper()
	src := "/* TOOL {\"description\":\"outil de test\",\"params\":{}} */\n" + code + "\n"
	if err := home.WriteFile(filepath.Join(home.Tools(), name+".js"), []byte(src)); err != nil {
		t.Fatal(err)
	}
}

func TestBridgeStorage(t *testing.T) {
	dir := setup(t)
	id := mustSave(t, map[string]any{"name": "Données", "html": "<p>x</p>"})
	other := mustSave(t, map[string]any{"name": "Voisine", "html": "<p>y</p>"})

	if got, err := bridge(t, id, "storage.get", `{"key":"absent"}`); err != nil || got != nil {
		t.Errorf("clé absente : %v, %v", got, err)
	}
	if got, err := bridge(t, id, "storage.keys", `{}`); err != nil || !reflect.DeepEqual(got, []string{}) {
		t.Errorf("clés d'un stockage vide : %#v, %v", got, err)
	}
	for key, value := range map[string]string{"todos": `[{"text":"pain","done":false},{"text":"lait","done":true}]`, "count": `3`, "titre": `"Été"`, "rien": `null`} {
		if _, err := bridge(t, id, "storage.set", `{"key":"`+key+`","value":`+value+`}`); err != nil {
			t.Fatalf("set %s : %v", key, err)
		}
	}
	got, err := bridge(t, id, "storage.get", `{"key":"todos"}`)
	if raw, _ := json.Marshal(got); err != nil || string(raw) != `[{"done":false,"text":"pain"},{"done":true,"text":"lait"}]` {
		t.Errorf("todos = %s, %v", raw, err)
	}
	if got, _ := bridge(t, id, "storage.get", `{"key":"count"}`); got != 3.0 {
		t.Errorf("count = %v", got)
	}
	if got, _ := bridge(t, id, "storage.keys", `{}`); !reflect.DeepEqual(got, []string{"count", "rien", "titre", "todos"}) {
		t.Errorf("clés = %v", got)
	}
	// Remplacer, supprimer, supprimer ce qui n'existe pas.
	if _, err := bridge(t, id, "storage.set", `{"key":"count","value":4}`); err != nil {
		t.Fatal(err)
	}
	if _, err := bridge(t, id, "storage.remove", `{"key":"titre"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := bridge(t, id, "storage.remove", `{"key":"jamais vue"}`); err != nil {
		t.Errorf("supprimer une clé absente : %v", err)
	}
	if got, _ := bridge(t, id, "storage.get", `{"key":"count"}`); got != 4.0 {
		t.Errorf("count remplacé = %v", got)
	}
	if got, _ := bridge(t, id, "storage.keys", `{}`); !reflect.DeepEqual(got, []string{"count", "rien", "todos"}) {
		t.Errorf("clés après suppression = %v", got)
	}

	// Chaque app a ses données, et elles survivent à une nouvelle version.
	if got, _ := bridge(t, other, "storage.keys", `{}`); !reflect.DeepEqual(got, []string{}) {
		t.Errorf("l'app voisine voit des clés : %v", got)
	}
	mustSave(t, map[string]any{"id": id, "html": "<p>v2</p>"})
	if got, _ := bridge(t, id, "storage.get", `{"key":"count"}`); got != 4.0 {
		t.Errorf("données perdues par une nouvelle version : %v", got)
	}
	if _, err := os.Stat(filepath.Join(dir, "APPS", id, "data", "storage.json")); err != nil {
		t.Errorf("storage.json : %v", err)
	}

	for label, args := range map[string]string{
		"sans clé":       `{"value":1}`,
		"clé vide":       `{"key":"","value":1}`,
		"clé pas texte":  `{"key":12,"value":1}`,
		"clé trop long":  `{"key":"` + strings.Repeat("k", maxKey+1) + `","value":1}`,
		"valeur absente": `{"key":"k"}`,
	} {
		if _, err := bridge(t, id, "storage.set", args); err == nil {
			t.Errorf("%s : refus attendu", label)
		}
	}
	if _, err := bridge(t, id, "storage.get", `{}`); err == nil {
		t.Error("get sans clé : refus attendu")
	}

	// Des données abîmées ne sont jamais écrasées.
	if err := home.WriteFile(filepath.Join(dir, "APPS", other, "data", "storage.json"), []byte("{abîmé")); err != nil {
		t.Fatal(err)
	}
	if _, err := bridge(t, other, "storage.set", `{"key":"k","value":1}`); err == nil {
		t.Error("écriture sur des données illisibles : refus attendu")
	}
	if raw, _ := os.ReadFile(filepath.Join(dir, "APPS", other, "data", "storage.json")); string(raw) != "{abîmé" {
		t.Errorf("données illisibles écrasées : %q", raw)
	}
}

func TestBridgeStorageLimits(t *testing.T) {
	setup(t)
	id := mustSave(t, map[string]any{"name": "Bornes", "html": "<p>x</p>"})
	ctx := context.Background()

	if _, err := Bridge(ctx, id, "storage.set", map[string]any{"key": "gros", "value": strings.Repeat("x", maxValue)}); err == nil || !strings.Contains(err.Error(), "trop grosse") {
		t.Errorf("valeur de plus de 1 Mo : %v", err)
	}
	// Cinq valeurs sous la borne passent, la sixième fait déborder le total.
	chunk := strings.Repeat("x", maxValue-100)
	for i := range 5 {
		if _, err := Bridge(ctx, id, "storage.set", map[string]any{"key": fmt.Sprint("bloc", i), "value": chunk}); err != nil {
			t.Fatalf("bloc %d : %v", i, err)
		}
	}
	if _, err := Bridge(ctx, id, "storage.set", map[string]any{"key": "bloc5", "value": chunk}); err == nil || !strings.Contains(err.Error(), "plein") {
		t.Errorf("total de plus de 5 Mo : %v", err)
	}
	if got, _ := Bridge(ctx, id, "storage.get", map[string]any{"key": "bloc5"}); got != nil {
		t.Error("la valeur refusée a été gardée")
	}
	for i := range 5 {
		if _, err := Bridge(ctx, id, "storage.remove", map[string]any{"key": fmt.Sprint("bloc", i)}); err != nil {
			t.Fatal(err)
		}
	}

	for i := range maxKeys {
		if _, err := Bridge(ctx, id, "storage.set", map[string]any{"key": fmt.Sprint("k", i), "value": i}); err != nil {
			t.Fatalf("clé %d : %v", i, err)
		}
	}
	if _, err := Bridge(ctx, id, "storage.set", map[string]any{"key": "de trop", "value": 1}); err == nil || !strings.Contains(err.Error(), "trop de clés") {
		t.Errorf("clé de trop : %v", err)
	}
	// Une clé existante reste modifiable quand le stockage est plein de clés.
	if _, err := Bridge(ctx, id, "storage.set", map[string]any{"key": "k0", "value": "autre"}); err != nil {
		t.Errorf("remplacer une clé existante : %v", err)
	}
}

func TestBridgeRunsDeclaredTool(t *testing.T) {
	setup(t)
	if err := workspace.Init(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = workspace.Close() })
	writeTool(t, "carnet", `function run(args) { ws.write("carnet.txt", args.text); return { saved: ws.read("carnet.txt"), size: args.text.length } }`)
	writeTool(t, "secret", `function run() { ws.write("secret.txt", "x"); return "secret" }`)
	writeTool(t, "casse", `function run() { throw new Error("ça casse") }`)

	// Un outil déclaré doit exister.
	got := call(map[string]any{"action": "save", "name": "Carnet", "html": "<p>x</p>", "tools": []any{"carnet", "fantome"}})
	if !tool.IsErr(got) || !strings.Contains(got, "fantome") || len(List()) != 0 {
		t.Fatalf("outil déclaré inexistant : %s", got)
	}
	id := mustSave(t, map[string]any{"name": "Carnet", "html": "<p>x</p>", "tools": []any{"carnet", "casse", "carnet"}})
	if a := List()[0]; !reflect.DeepEqual(a.Tools, []string{"carnet", "casse"}) {
		t.Fatalf("outils déclarés = %v", a.Tools)
	}
	if read := call(map[string]any{"action": "read", "id": id}); !strings.Contains(read, "tools: carnet, casse\n") {
		t.Errorf("read ne donne pas les outils :\n%s", read)
	}

	res, err := bridge(t, id, "tools.run", `{"name":"carnet","args":{"text":"acheter du pain"}}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Saved string
		Size  int
	}
	if text, _ := res.(string); json.Unmarshal([]byte(text), &out) != nil || out.Saved != "acheter du pain" || out.Size != 15 {
		t.Errorf("résultat de l'outil = %v", res)
	}
	if text, err := workspace.Read("carnet.txt"); err != nil || text != "acheter du pain" {
		t.Errorf("l'outil n'a pas écrit dans le workspace : %q, %v", text, err)
	}

	// Un outil qui existe mais que l'app ne déclare pas ne tourne pas.
	for _, args := range []string{`{"name":"secret"}`, `{"name":"../secret"}`, `{"name":""}`, `{}`, `{"name":"Carnet"}`} {
		if _, err := bridge(t, id, "tools.run", args); err == nil || !strings.Contains(err.Error(), "pas déclaré") {
			t.Errorf("tools.run %s : %v, refus attendu", args, err)
		}
	}
	if workspace.Exists("secret.txt") {
		t.Error("un outil non déclaré a tourné")
	}
	if _, err := bridge(t, id, "tools.run", `{"name":"casse"}`); err == nil || !strings.Contains(err.Error(), "ça casse") {
		t.Errorf("erreur de l'outil : %v", err)
	}

	// La liste se remplace à l'enregistrement ; absente, elle est gardée.
	mustSave(t, map[string]any{"id": id, "html": "<p>v2</p>"})
	if _, err := bridge(t, id, "tools.run", `{"name":"carnet","args":{"text":"x"}}`); err != nil {
		t.Errorf("outils perdus par une nouvelle version : %v", err)
	}
	mustSave(t, map[string]any{"id": id, "tools": "secret, carnet"})
	if a := List()[0]; !reflect.DeepEqual(a.Tools, []string{"secret", "carnet"}) {
		t.Errorf("outils après remplacement = %v", a.Tools)
	}
	mustSave(t, map[string]any{"id": id, "tools": []any{}})
	if _, err := bridge(t, id, "tools.run", `{"name":"carnet","args":{"text":"x"}}`); err == nil {
		t.Error("outil lancé après avoir été retiré de la liste")
	}

	// Un outil supprimé après sa déclaration n'empêche pas de corriger l'app.
	mustSave(t, map[string]any{"id": id, "tools": []any{"casse"}})
	if err := os.Remove(filepath.Join(home.Tools(), "casse.js")); err != nil {
		t.Fatal(err)
	}
	got = call(map[string]any{"action": "edit", "id": id, "file": "html", "old": "v2", "new": "v3"})
	if !tool.IsOK(got) || !strings.Contains(got, "n'existe plus") {
		t.Errorf("edit avec un outil disparu : %s", got)
	}
}

// fakeAPI : une API compatible OpenAI qui répond en flux et garde la dernière
// requête reçue.
func fakeAPI(t *testing.T, reply func(w http.ResponseWriter, r *http.Request)) (requests *[]map[string]any) {
	t.Helper()
	requests = &[]map[string]any{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		raw, _ := io.ReadAll(r.Body)
		if r.URL.Path != "/chat/completions" || json.Unmarshal(raw, &body) != nil {
			http.Error(w, "requête inattendue", http.StatusBadRequest)
			return
		}
		*requests = append(*requests, body)
		reply(w, r)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("AJEAN_API_URL", srv.URL)
	t.Setenv("AJEAN_API_KEY", "")
	t.Setenv("AJEAN_MODEL", "modele-de-test")
	return requests
}

func TestBridgeAsk(t *testing.T) {
	setup(t)
	id := mustSave(t, map[string]any{"name": "Oracle", "html": "<p>x</p>"})
	requests := fakeAPI(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"  Bonjour\"}}]}\n\n")
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\" Alice \\n\"}}]}\n\ndata: [DONE]\n\n")
	})

	got, err := bridge(t, id, "ai.ask", `{"prompt":"  Salue Alice  "}`)
	if err != nil || got != "Bonjour Alice" {
		t.Fatalf("ai.ask = %q, %v", got, err)
	}
	if len(*requests) != 1 {
		t.Fatalf("%d requêtes envoyées", len(*requests))
	}
	req := (*requests)[0]
	msgs, _ := req["messages"].([]any)
	if req["model"] != "modele-de-test" || req["tools"] != nil || len(msgs) != 2 {
		t.Fatalf("requête = %v", req)
	}
	system, user := msgs[0].(map[string]any), msgs[1].(map[string]any)
	if system["role"] != "system" || system["content"] != askSystem || user["role"] != "user" || user["content"] != "Salue Alice" {
		t.Errorf("messages = %v", msgs)
	}

	for label, args := range map[string]string{
		"sans demande":      `{}`,
		"demande vide":      `{"prompt":"   "}`,
		"demande pas texte": `{"prompt":{"a":1}}`,
		"demande trop long": `{"prompt":"` + strings.Repeat("é", maxPrompt+1) + `"}`,
	} {
		if _, err := bridge(t, id, "ai.ask", args); err == nil {
			t.Errorf("%s : refus attendu", label)
		}
	}
	if _, err := bridge(t, id, "ai.ask", `{"prompt":"`+strings.Repeat("é", maxPrompt)+`"}`); err != nil {
		t.Errorf("demande à la borne : %v", err)
	}
	if len(*requests) != 2 {
		t.Errorf("%d requêtes envoyées, 2 attendues : une demande refusée ne part pas", len(*requests))
	}
}

func TestBridgeAskFailures(t *testing.T) {
	setup(t)
	id := mustSave(t, map[string]any{"name": "Oracle", "html": "<p>x</p>"})

	// Aucune API configurée.
	t.Setenv("AJEAN_API_URL", "")
	t.Setenv("AJEAN_MODEL", "")
	if _, err := bridge(t, id, "ai.ask", `{"prompt":"x"}`); err == nil {
		t.Error("sans API : refus attendu")
	}

	mode := "vide"
	fakeAPI(t, func(w http.ResponseWriter, r *http.Request) {
		switch mode {
		case "vide":
			io.WriteString(w, "data: [DONE]\n\n")
		case "refus":
			http.Error(w, `{"error":{"message":"quota dépassé"}}`, http.StatusTooManyRequests)
		case "lent":
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
	})
	if _, err := bridge(t, id, "ai.ask", `{"prompt":"x"}`); err == nil || !strings.Contains(err.Error(), "rien répondu") {
		t.Errorf("réponse vide : %v", err)
	}
	mode = "refus"
	if _, err := bridge(t, id, "ai.ask", `{"prompt":"x"}`); err == nil || !strings.Contains(err.Error(), "quota dépassé") {
		t.Errorf("refus de l'API : %v", err)
	}

	// Un modèle qui ne répond pas ne retient pas l'app indéfiniment.
	mode = "lent"
	askTimeout = 150 * time.Millisecond
	t.Cleanup(func() { askTimeout = 2 * time.Minute })
	start := time.Now()
	if _, err := bridge(t, id, "ai.ask", `{"prompt":"x"}`); err == nil || !strings.Contains(err.Error(), "pas répondu") {
		t.Errorf("modèle muet : %v", err)
	}
	if d := time.Since(start); d > 10*time.Second {
		t.Errorf("attente de %s", d)
	}
}

func TestBridgeErrors(t *testing.T) {
	dir := setup(t)
	id := mustSave(t, map[string]any{"name": "Fragile", "html": "<p>x</p>"})
	if got := call(map[string]any{"action": "errors", "id": id}); !strings.Contains(got, "aucune erreur") {
		t.Errorf("sans erreur : %s", got)
	}

	for i := 1; i <= maxErrors+5; i++ {
		if res, err := bridge(t, id, "error", fmt.Sprintf(`{"message":"erreur %d"}`, i)); err != nil || res != nil {
			t.Fatalf("erreur %d : %v, %v", i, res, err)
		}
	}
	// La même erreur, répétée, ne compte qu'une fois.
	for range 3 {
		if _, err := bridge(t, id, "error", fmt.Sprintf(`{"message":"erreur %d"}`, maxErrors+5)); err != nil {
			t.Fatal(err)
		}
	}
	if a := List()[0]; a.Errors != maxErrors {
		t.Errorf("%d erreurs en attente, %d attendues", a.Errors, maxErrors)
	}
	if _, err := os.Stat(filepath.Join(dir, "APPS", id, "data", "errors.json")); err != nil {
		t.Errorf("errors.json : %v", err)
	}
	if list := call(map[string]any{"action": "list"}); !strings.Contains(list, "20 erreur(s) JavaScript en attente") {
		t.Errorf("list ne signale pas les erreurs : %s", list)
	}
	// Un enregistrement rappelle qu'elles attendent, sans les effacer.
	if got := call(map[string]any{"action": "edit", "id": id, "file": "html", "old": "x", "new": "y"}); !strings.Contains(got, "20 erreur(s) JavaScript en attente") {
		t.Errorf("edit ne rappelle pas les erreurs : %s", got)
	}
	if _, err := bridge(t, id, "error", `{"message":"`+strings.Repeat("long ", 1000)+`"}`); err != nil {
		t.Fatal(err)
	}

	got := call(map[string]any{"action": "errors", "id": id})
	if !strings.HasPrefix(got, "20 erreur(s)") || strings.Contains(got, "erreur 6\n") || !strings.Contains(got, "version 1 : erreur 7\n") || !strings.Contains(got, "version 1 : erreur 25\n") || !strings.Contains(got, "version 2 : long long") {
		t.Errorf("errors :\n%s", got)
	}
	if len(got) > 2*maxMessage+3000 {
		t.Errorf("une erreur longue n'est pas tronquée : %d octets", len(got))
	}
	if strings.Contains(got, "effacées") || List()[0].Errors != maxErrors {
		t.Error("lire les erreurs les a effacées")
	}

	got = call(map[string]any{"action": "errors", "id": id, "clear": true})
	if !strings.Contains(got, "erreur 25") || !strings.Contains(got, "(effacées)") {
		t.Errorf("errors clear :\n%s", got)
	}
	if a := List()[0]; a.Errors != 0 {
		t.Errorf("%d erreurs après effacement", a.Errors)
	}
	if got := call(map[string]any{"action": "errors", "id": id, "clear": true}); !strings.Contains(got, "aucune erreur") {
		t.Errorf("après effacement : %s", got)
	}

	for _, args := range []string{`{}`, `{"message":"  "}`, `{"message":42}`} {
		if _, err := bridge(t, id, "error", args); err == nil {
			t.Errorf("error %s : refus attendu", args)
		}
	}
}

func TestBridgeUnknownOperation(t *testing.T) {
	setup(t)
	id := mustSave(t, map[string]any{"name": "Curieuse", "html": "<p>x</p>"})
	for _, op := range []string{"", "storage", "storage.clear", "fs.read", "tools.save", "STORAGE.GET", "apps.delete"} {
		if _, err := Bridge(context.Background(), id, op, map[string]any{"key": "k"}); err == nil || !strings.Contains(err.Error(), "inconnue") {
			t.Errorf("opération %q : %v, refus attendu", op, err)
		}
	}
	if _, err := Bridge(context.Background(), id, "storage.keys", nil); err != nil {
		t.Errorf("arguments absents : %v", err)
	}
	if _, err := Bridge(context.Background(), "absente", "storage.keys", nil); err == nil {
		t.Error("app absente : refus attendu")
	}
}

// Une app qui interroge le modèle en boucle est freinée : la limite se compte
// par app, sur une minute glissante.
func TestAskIsRateLimited(t *testing.T) {
	now := time.Now()
	mu.Lock()
	defer mu.Unlock()
	delete(asked, "limite")
	for i := 0; i < maxAsks; i++ {
		if !allowAsk("limite", now) {
			t.Fatalf("demande %d refusée avant la limite", i+1)
		}
	}
	if allowAsk("limite", now.Add(30*time.Second)) {
		t.Error("la demande au-delà de la limite aurait dû être refusée")
	}
	if !allowAsk("autre", now) {
		t.Error("la limite d'une app ne doit pas toucher les autres")
	}
	if !allowAsk("limite", now.Add(61*time.Second)) {
		t.Error("une minute plus tard, l'app doit pouvoir redemander")
	}
}
