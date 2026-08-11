package ajean

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/nathaninline/ajean/internal/nodewire"
)

// TestNodeE2E exerce tout le chemin : enrôlement SCELLÉ (le sceau du client est
// ouvert par e2eOpenSeal de l'agent → interop crypto validée), puis canal
// CHIFFRÉ poste↔agent (hello + appel d'outil + résultat), et refus d'une
// capacité non autorisée.
func TestNodeE2E(t *testing.T) {
	testHome(t)

	agentPub := e2ePubHex()
	if agentPub == "" {
		t.Fatal("clé publique agent indisponible")
	}

	// Le propriétaire autorise read + shell (pas write).
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

	// 1) Le poste génère sa paire et SCELLE {pub, code, name, os} vers l'agent.
	priv, pub, err := nodewire.GenKeyPair()
	if err != nil {
		t.Fatal(err)
	}
	inner, _ := json.Marshal(map[string]string{"pub": pub, "code": "abcd1234", "name": "testpc", "os": "linux/amd64"})
	blob, err := nodewire.SealTo(agentPub, inner)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"sealed": base64.StdEncoding.EncodeToString(blob)})
	resp, err := http.Post(srv.URL+"/api/node/enroll", "application/json", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	var enr struct {
		OK bool `json:"ok"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&enr)
	resp.Body.Close()
	if !enr.OK {
		t.Fatalf("enrôlement échoué: %+v", enr)
	}
	if findNodeByPub(pub) == nil {
		t.Fatal("la clé publique du poste n'a pas été enregistrée")
	}

	// 2) Connexion WS chiffrée.
	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/node/ws"
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()

	// hello_pub en clair, puis canal chiffré côté client.
	if err := wsjson.Write(ctx, c, map[string]string{"type": "hello_pub", "pub": pub}); err != nil {
		t.Fatal(err)
	}
	key, _ := nodewire.ChannelKey(priv, agentPub)
	ch, _ := nodewire.NewChan(key, true)
	sendEnc := func(m nodewire.Msg) { _ = wsjson.Write(ctx, c, ch.Seal(mustJSON(m))) }
	sendEnc(nodewire.Msg{Type: "hello", Name: "testpc", OS: "linux/amd64", Caps: []string{nodeCapRead, nodeCapShell, nodeCapWrite}})

	// Boucle client : répond à chaque call chiffré par "RESULT:<cap>".
	go func() {
		for {
			var fr nodewire.Frame
			if err := wsjson.Read(context.Background(), c, &fr); err != nil {
				return
			}
			plain, err := ch.Open(fr)
			if err != nil {
				return
			}
			var m nodewire.Msg
			if json.Unmarshal(plain, &m) == nil && m.Type == "call" {
				sendEnc(nodewire.Msg{Type: "result", ID: m.ID, Result: "RESULT:" + m.Cap})
			}
		}
	}()

	// Attend l'enregistrement.
	slug := nodeSlug("testpc")
	var nc *nodeConn
	for i := 0; i < 200; i++ {
		if nc = nodeGet(slug); nc != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if nc == nil {
		t.Fatal("le poste ne s'est pas enregistré (canal chiffré ?)")
	}
	if len(nc.caps) != 2 || nc.caps[0] != nodeCapShell || nc.caps[1] != nodeCapRead {
		t.Fatalf("capacités effectives inattendues: %v", nc.caps)
	}

	// 3) Appel autorisé routé jusqu'au poste et retour, à travers le chiffrement.
	if got := nodeCall(slug, nodeCapRead, map[string]any{"path": "x"}); got != "RESULT:read" {
		t.Fatalf("appel read: attendu RESULT:read, obtenu %q", got)
	}
	// 4) Appel non autorisé (write) refusé côté serveur.
	if got := nodeCall(slug, nodeCapWrite, map[string]any{"path": "x"}); !strings.Contains(got, "non autorisée") {
		t.Fatalf("appel write aurait dû être refusé, obtenu %q", got)
	}
}

func mustJSON(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
