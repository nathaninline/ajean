package ajean

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// TestNodeEndToEnd exerce tout le chemin serveur : enrôlement via code, connexion
// WebSocket, négociation des capacités (intersection propriétaire ∩ poste), et
// routage d'un appel d'outil jusqu'au poste et retour.
func TestNodeEndToEnd(t *testing.T) {
	testHome(t)

	// Le propriétaire n'autorise que read + shell (pas write).
	if err := savePairPending(nodePairPending{
		Code:    "ABCD1234",
		Caps:    []string{nodeCapRead, nodeCapShell},
		Expires: time.Now().Add(time.Minute).Unix(),
	}); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/node/enroll", handleNodeEnroll)
	mux.HandleFunc("/api/node/ws", handleNodeWS)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// 1) Enrôlement : le code est échangé contre une clé d'appareil.
	body, _ := json.Marshal(map[string]string{"code": "abcd1234", "name": "testpc", "os": "linux/amd64"})
	resp, err := http.Post(srv.URL+"/api/node/enroll", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	var enr struct {
		OK   bool     `json:"ok"`
		Key  string   `json:"key"`
		Caps []string `json:"caps"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&enr)
	resp.Body.Close()
	if !enr.OK || enr.Key == "" {
		t.Fatalf("enrôlement échoué: %+v", enr)
	}
	// Le code est à usage unique : un second enrôlement doit échouer.
	resp2, _ := http.Post(srv.URL+"/api/node/enroll", "application/json", strings.NewReader(string(body)))
	if resp2.StatusCode == 200 {
		t.Fatal("le code d'appairage aurait dû être consommé (usage unique)")
	}
	resp2.Body.Close()

	// 2) Le poste se connecte et sert les appels.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/node/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + enr.Key}},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()
	// Le poste déclare pouvoir read/shell/write ; l'effectif sera ∩ = read/shell.
	if err := wsjson.Write(ctx, c, nodeMsg{Type: "hello", Name: "testpc", OS: "linux/amd64",
		Caps: []string{nodeCapRead, nodeCapShell, nodeCapWrite}}); err != nil {
		t.Fatalf("hello: %v", err)
	}
	// Boucle client : répond à chaque call par "RESULT:<cap>".
	go func() {
		for {
			var m nodeMsg
			if err := wsjson.Read(context.Background(), c, &m); err != nil {
				return
			}
			if m.Type == "call" {
				_ = wsjson.Write(context.Background(), c, nodeMsg{Type: "result", ID: m.ID, Result: "RESULT:" + m.Cap})
			}
		}
	}()

	// Attend l'enregistrement du poste.
	slug := nodeSlug("testpc")
	var nc *nodeConn
	for i := 0; i < 100; i++ {
		if nc = nodeGet(slug); nc != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if nc == nil {
		t.Fatal("le poste ne s'est pas enregistré")
	}

	// 3) Capacités effectives = intersection : read + shell, PAS write.
	if len(nc.caps) != 2 || nc.caps[0] != nodeCapShell || nc.caps[1] != nodeCapRead {
		t.Fatalf("capacités effectives inattendues: %v", nc.caps)
	}

	// 4) Un appel autorisé traverse jusqu'au poste et revient.
	if got := nodeCall(slug, nodeCapRead, map[string]any{"path": "x"}); got != "RESULT:read" {
		t.Fatalf("appel read: attendu RESULT:read, obtenu %q", got)
	}

	// 5) Un appel NON autorisé (write) est refusé côté serveur, sans atteindre le poste.
	if got := nodeCall(slug, nodeCapWrite, map[string]any{"path": "x", "content": "y"}); !strings.Contains(got, "non autorisée") {
		t.Fatalf("appel write aurait dû être refusé, obtenu %q", got)
	}

	// 6) Sélection de la cible : l'agent agit sur ce poste. La méta reflète un
	// poste connecté, et un appel shell (autorisé) est bien routé jusqu'au poste.
	if err := setAgentTargetSlug(slug); err != nil {
		t.Fatal(err)
	}
	meta, ok := nodeTargetMetaGet()
	if !ok || !meta.connected || meta.slug != slug {
		t.Fatalf("cible inattendue: ok=%v %+v", ok, meta)
	}
	if got := nodeCall(slug, nodeCapShell, map[string]any{"command": "echo hi"}); got != "RESULT:shell" {
		t.Fatalf("routage shell: attendu RESULT:shell, obtenu %q", got)
	}

	// 7) nodeEditRemote lit puis réécrit via le poste (le client fictif renvoie
	// "RESULT:read" à la lecture → "old" introuvable, donc erreur explicite, pas
	// un plantage : on vérifie juste que le chemin read→write est emprunté).
	if got := nodeEditRemote(slug, "f.txt", "absent", "x"); !strings.Contains(got, "introuvable") {
		t.Fatalf("nodeEditRemote: attendu 'introuvable', obtenu %q", got)
	}
	setAgentTargetSlug("") // ne pas fuiter la cible sur d'autres tests
}
