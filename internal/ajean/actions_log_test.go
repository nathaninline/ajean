package ajean

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// Un tour sans action ne laisse aucune trace ; un tour qui agit laisse
// begin / start / done / end, dans cet ordre.
func TestActionTurnLifecycle(t *testing.T) {
	testHome(t)
	quiet := newActionTurn(context.Background(), Caps{Agent: true})
	quiet.end()
	if _, err := os.Stat(actionsFile("chat")); err == nil {
		t.Fatal("un tour sans action ne doit rien écrire")
	}
	turn := newActionTurn(withActionScope(context.Background(), "chat", "conv-1", ""), Caps{Agent: true})
	turn.start("bash", "ls -la")
	turn.done("bash", "ls -la", "exit: 0\n\nstdout:\nfichier.txt")
	turn.end()
	var phases []string
	for _, e := range readActions("chat") {
		phases = append(phases, e.Phase)
		if e.Task != "conv-1" || e.Turn != turn.id {
			t.Fatalf("entrée mal rattachée : %+v", e)
		}
	}
	if got := strings.Join(phases, ","); got != "begin,start,done,end" {
		t.Fatalf("phases : %s", got)
	}
	list := recentActions(10)
	if len(list) != 1 || list[0].Name != "bash" || !list[0].Done || !strings.Contains(list[0].Result, "exit: 0") {
		t.Fatalf("vue des actions : %+v", list)
	}
	// Sans mode agent ni Jean : aucun outil, aucun journal. Une révision interne
	// de la mémoire de Jean n'en tient pas non plus.
	off := newActionTurn(context.Background(), Caps{})
	off.start("bash", "x")
	if n := len(readActions("chat")); n != 4 {
		t.Fatalf("un tour sans outils a écrit dans le journal (%d lignes)", n)
	}
	// Journal coupé : plus rien ne s'écrit, l'existant reste.
	if err := putBool(bkState, stActionsOff, true); err != nil {
		t.Fatal(err)
	}
	cut := newActionTurn(context.Background(), Caps{Agent: true})
	cut.start("bash", "rm x")
	if n := len(readActions("chat")); n != 4 {
		t.Fatalf("journal coupé mais %d lignes", n)
	}
}

// Ce qui ressemble à un secret n'entre pas dans le journal.
func TestActionLogRedactsSecrets(t *testing.T) {
	home := testHome(t)
	turn := newActionTurn(context.Background(), Caps{Agent: true})
	turn.start("bash", `curl -H "Authorization: Bearer abcdef0123456789xyz" https://api.exemple.test && export API_KEY=sk-live-AAAABBBBCCCCDDDDEEEEFFFF`)
	turn.done("bash", "x", "password: hunter2secret\ntoken=ghp_0123456789abcdefghijklmnopqrstuvwxyz")
	turn.end()
	raw, err := os.ReadFile(actionsFile("chat"))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"abcdef0123456789xyz", "sk-live-AAAABBBBCCCCDDDDEEEEFFFF", "hunter2secret", "ghp_0123456789abcdefghijklmnopqrstuvwxyz"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("secret en clair dans le journal : %s", leak)
		}
	}
	if !strings.Contains(string(raw), "https://api.exemple.test") {
		t.Error("le reste de la commande devait être gardé")
	}
	_ = home
	// Ce qui désigne un secret sans le contenir n'est pas masqué.
	for _, s := range []string{"password: $PASSWORD", "token = os.environ.token", "api_key: vault:0123456789abcdef", "le token a expiré"} {
		if got := redactSecrets(s); got != s {
			t.Errorf("%q masqué à tort : %q", s, got)
		}
	}
	if got := redactSecrets("echo monmotdepasse | sudo -S apt update"); strings.Contains(got, "monmotdepasse") {
		t.Errorf("mot de passe passé à sudo non masqué : %q", got)
	}
}

// Formes courantes en ligne de commande, que le premier masquage ratait.
func TestRedactShellSecrets(t *testing.T) {
	for in, leak := range map[string]string{
		"export OPENAI_API_KEY=sk-proj-abc123":                          "sk-proj-abc123",
		"DB_PASSWORD=Tr0ub4dor ./migrate":                               "Tr0ub4dor",
		"GITHUB_TOKEN=abcdef123456 gh pr list":                          "abcdef123456",
		"AWS_SECRET_ACCESS_KEY=wJalrXUtnFEMIK7MDENGbPxRfiCY aws s3 ls":  "wJalrXUtnFEMIK7MDENGbPxRfiCY",
		"HF_TOKEN=hf_abcdefghijklmnopqrstuvwxyz012345 python dl.py":     "hf_abcdefghijklmnopqrstuvwxyz012345",
		"huggingface-cli login --token hf_abcdefghijklmnopqrstuvwxyz01": "hf_abcdefghijklmnopqrstuvwxyz01",
		"curl -u alice:s3cr3tpass https://api.exemple.test":             "s3cr3tpass",
		"curl --user alice:s3cr3tpass https://api.exemple.test":         "s3cr3tpass",
		"mysql -h db -u root -pS3cretPw base":                           "S3cretPw",
		"sshpass -p S3cretPw ssh hote":                                  "S3cretPw",
		"cat <<EOF\n-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\nAAAAB3NzaC1yc2E\n-----END OPENSSH PRIVATE KEY-----\nEOF": "b3BlbnNzaC1rZXktdjEAAAAA",
		"-----BEGIN RSA PRIVATE KEY-----\nMIIEowIBAAKCAQEA (bloc coupé avant sa fin":                                                        "MIIEowIBAAKCAQEA",
	} {
		got := redactSecrets(in)
		if strings.Contains(got, leak) {
			t.Errorf("non masqué : %q → %q", in, got)
		}
		if !strings.Contains(got, secretMaskedText) {
			t.Errorf("aucun masque posé : %q → %q", in, got)
		}
	}
	// Le reste de la commande reste lisible.
	if got := redactSecrets("curl -u alice:s3cr3tpass https://api.exemple.test"); !strings.Contains(got, "alice") || !strings.Contains(got, "https://api.exemple.test") {
		t.Errorf("trop masqué : %q", got)
	}
	// Ce qui parle d'un secret sans en être un n'est pas touché.
	for _, s := range []string{"max_tokens=4096", "TOKEN_FILE=/run/secrets/tok", "password_length=16", "mysql -P 3306 -u root base", "mysql -p -u root", "API_KEY=$OPENAI_API_KEY"} {
		if got := redactSecrets(s); got != s {
			t.Errorf("%q masqué à tort : %q", s, got)
		}
	}
	// Un jeton que la coupe du journal aurait tranché est masqué en entier.
	testHome(t)
	long := strings.Repeat("x", actionLabelMax-12) + " sk-live-AAAABBBBCCCCDDDDEEEEFFFFGGGG"
	turn := newActionTurn(context.Background(), Caps{Agent: true})
	turn.start("bash", long)
	if raw, _ := os.ReadFile(actionsFile("chat")); strings.Contains(string(raw), "sk-live") {
		t.Error("jeton tranché par la coupe resté en partie en clair")
	}
}

// Tour coupé net : le suivant apprend ce qui avait été fait, une seule fois.
func TestActionsInterruptedNote(t *testing.T) {
	testHome(t)
	if note := actionsInterruptedNote("chat", "conv-1"); note != "" {
		t.Fatalf("journal vide : %q", note)
	}
	// Tour terminé normalement : rien à dire.
	ok := newActionTurn(withActionScope(context.Background(), "chat", "conv-1", ""), Caps{Agent: true})
	ok.start("write", "a.txt")
	ok.done("write", "a.txt", "[ok] a.txt créé")
	ok.end()
	if note := actionsInterruptedNote("chat", "conv-1"); note != "" {
		t.Fatalf("tour fini : %q", note)
	}
	// Tour coupé : une action finie, une en cours — et pas de clôture.
	cut := newActionTurn(withActionScope(context.Background(), "chat", "conv-1", ""), Caps{Agent: true})
	cut.start("bash", "git push")
	cut.done("bash", "git push", "exit: 0")
	cut.start("bash", "rm -rf build")
	// (le process meurt ici)
	if note := actionsInterruptedNote("chat", "autre-conv"); note != "" {
		t.Fatalf("une autre conversation n'est pas concernée : %q", note)
	}
	note := actionsInterruptedNote("chat", "conv-1")
	if !strings.Contains(note, "git push : exit: 0") || !strings.Contains(note, "rm -rf build : result unknown") {
		t.Fatalf("note du tour coupé :\n%s", note)
	}
	if strings.Contains(note, "a.txt") {
		t.Fatal("la note ne doit porter que le tour coupé")
	}
	if again := actionsInterruptedNote("chat", "conv-1"); again != "" {
		t.Fatalf("la note ne doit être donnée qu'une fois : %q", again)
	}
	// Une tâche planifiée a son propre fil.
	task := newActionTurn(withActionScope(context.Background(), "tasks", "t1", "Veille"), Caps{Agent: true})
	task.start("bash", "./veille.sh")
	if note := actionsInterruptedNote("tasks", "t2"); note != "" {
		t.Fatal("une autre tâche n'est pas concernée")
	}
	if note := actionsInterruptedNote("tasks", "t1"); !strings.Contains(note, "./veille.sh") {
		t.Fatalf("note de la tâche coupée : %q", note)
	}
	if list := recentActions(10); len(list) == 0 || list[0].Task != "Veille" || list[0].Done {
		t.Fatalf("la tâche coupée doit apparaître sans résultat : %+v", list)
	}
}

// Mémoire chiffrée : le journal l'est aussi, et rien n'est écrit verrouillé.
func TestActionLogFollowsMemoryEncryption(t *testing.T) {
	testHome(t)
	if err := SetConfigKey("MEM_ENCRYPTED", "1"); err != nil {
		t.Fatal(err)
	}
	clearMemDEK()
	t.Cleanup(clearMemDEK)
	locked := newActionTurn(context.Background(), Caps{Agent: true})
	locked.start("bash", "commande-secrete-verrouillee")
	if _, err := os.Stat(actionsFile("chat")); err == nil {
		t.Fatal("mémoire verrouillée : rien ne doit être écrit")
	}
	dek, _ := randBytes(memDEKLen)
	setMemDEK(dek)
	turn := newActionTurn(context.Background(), Caps{Agent: true})
	turn.start("bash", "commande-tres-privee")
	turn.done("bash", "commande-tres-privee", "exit: 0")
	turn.end()
	raw, err := os.ReadFile(actionsFile("chat"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "commande-tres-privee") || !strings.HasPrefix(string(raw), actionEncPrefix) {
		t.Fatalf("le journal devait être chiffré :\n%s", raw)
	}
	if list := recentActions(10); len(list) != 1 || list[0].Label != "commande-tres-privee" {
		t.Fatalf("relecture déverrouillée : %+v", list)
	}
	clearMemDEK()
	if list := recentActions(10); len(list) != 0 {
		t.Fatalf("verrouillé, le journal chiffré ne doit pas se lire : %+v", list)
	}
}

// Le journal tourne au lieu de grossir sans fin.
func TestActionLogRotation(t *testing.T) {
	testHome(t)
	if err := os.MkdirAll(actionsDir(), 0o700); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("x", actionsMaxBytes+10)
	if err := os.WriteFile(actionsFile("chat"), []byte(big+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	turn := newActionTurn(context.Background(), Caps{Agent: true})
	turn.start("bash", "après rotation")
	if fi, err := os.Stat(actionsFile("chat") + ".old"); err != nil || fi.Size() < actionsMaxBytes {
		t.Fatalf("l'ancien journal devait être mis de côté : %v", err)
	}
	if fi, err := os.Stat(actionsFile("chat")); err != nil || fi.Size() > 4096 {
		t.Fatalf("le journal courant devait repartir petit : %v", err)
	}
}

func TestActionMutating(t *testing.T) {
	for name, want := range map[string]bool{
		"bash": true, "write": true, "edit": true, "mem_add": true, "mem_delete": true,
		"task_create": true, "task_delete": true, "browser_click": true, "mcp__srv__send_mail": true,
		"mem_search": false, "mem_read": false, "web_open": false, "task_list": false, "see_image": false, "jean_read": false,
	} {
		if got := actionMutating(name, map[string]any{}); got != want {
			t.Errorf("%s : %v, attendu %v", name, got, want)
		}
	}
	if actionMutating("tracker", map[string]any{"action": "view"}) || !actionMutating("tracker", map[string]any{"action": "add"}) {
		t.Error("tracker : seules add/edit/delete agissent")
	}
	if actionMutating("app", map[string]any{"action": "check"}) || !actionMutating("app", map[string]any{"action": "create"}) {
		t.Error("app : check ne fait que regarder")
	}
	if actionsFile("../../etc/passwd") != actionsFile("chat") {
		t.Error("un nom d'espace hostile doit retomber sur l'espace par défaut")
	}
}

// Bout en bout : le modèle appelle un outil d'écriture, runChat le note avant et
// après, puis clôt le tour.
func TestRunChatJournalsToolCalls(t *testing.T) {
	testHome(t)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(200)
		if calls.Add(1) == 1 {
			// task_delete sur un id inconnu : un outil qui AGIT, donc journalisé, mais
			// sans aucun effet hors de la base de ce test.
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"c1","type":"function","function":{"name":"task_delete","arguments":"{\"id\":\"inconnue\"}"}}]}}]}` + "\n\n" +
				`data: {"choices":[{"delta":{},"finish_reason":"tool_calls"}]}` + "\n\n" + "data: [DONE]\n\n"))
			return
		}
		_, _ = w.Write([]byte(sseChunk("fait") + "data: [DONE]\n\n"))
	}))
	t.Cleanup(srv.Close)
	u, _ := url.Parse(srv.URL)
	if err := SetConfigKey("PORT", u.Port()); err != nil {
		t.Fatal(err)
	}
	ctx := withActionScope(context.Background(), "tasks", "t-essai", "Essai")
	if _, err := runChat(ctx, []Message{{Role: "user", Content: "supprime la tâche"}}, 0.7, Caps{Agent: true}, func(StreamEvent) bool { return true }, nil); err != nil {
		t.Fatal(err)
	}
	var phases []string
	for _, e := range readActions("tasks") {
		phases = append(phases, e.Phase+":"+e.Name)
		if e.Task != "t-essai" {
			t.Fatalf("entrée hors de la tâche : %+v", e)
		}
		if e.Phase == "done" && !strings.Contains(e.Result, "introuvable") {
			t.Fatalf("résultat de l'outil non noté : %+v", e)
		}
	}
	if got := strings.Join(phases, " "); got != "begin: start:task_delete done:task_delete end:" {
		t.Fatalf("journal du tour : %q", got)
	}
	if note := actionsInterruptedNote("tasks", "t-essai"); note != "" {
		t.Fatalf("tour terminé normalement, aucune note attendue : %q", note)
	}
}
