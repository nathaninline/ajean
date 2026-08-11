// node_server.go — côté SERVEUR du poste distant : appairage, endpoint
// WebSocket, registre des postes connectés, et exposition de leurs outils à
// l'agent. Le serveur n'exécute JAMAIS rien lui-même ici : il route une demande
// d'outil vers le poste concerné et attend sa réponse.
package ajean

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

const (
	nodePairCodeTTL = 10 * time.Minute
	nodeCallTimeout = 5 * time.Minute // un shell distant peut être long
	nodeHelloWait   = 15 * time.Second
)

// pairedNode est un poste appairé, tel que persisté dans bkState["nodes"].
// On ne garde JAMAIS la clé en clair : seulement son SHA-256. Le poste, lui,
// conserve la clé en local (voir node_client.go).
type pairedNode struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	OS        string   `json:"os"`
	KeyHash   string   `json:"key_hash"`
	Caps      []string `json:"caps"` // capacités AUTORISÉES par le propriétaire
	Root      string   `json:"root,omitempty"`
	CreatedAt int64    `json:"created_at"`
	LastSeen  int64    `json:"last_seen,omitempty"`
}

func loadNodes() []pairedNode {
	var l []pairedNode
	getJSON(bkState, "nodes", &l)
	return l
}

func saveNodes(l []pairedNode) error { return putJSON(bkState, "nodes", l) }

// findNodeByKey retrouve un poste appairé à partir de la clé d'appareil présentée
// (comparaison à temps constant sur le hash). Renvoie nil si aucune ne matche.
func findNodeByKey(key string) *pairedNode {
	if key == "" {
		return nil
	}
	want := nodeHashKey(key)
	nodes := loadNodes()
	for i := range nodes {
		if subtle.ConstantTimeCompare([]byte(nodes[i].KeyHash), []byte(want)) == 1 {
			return &nodes[i]
		}
	}
	return nil
}

func nodeHashKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

// nodeRandHex renvoie n octets aléatoires en hexadécimal (2n caractères).
func nodeRandHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// ─── Code d'appairage (usage unique, TTL court) ────────────────────────────

type nodePairPending struct {
	Code    string   `json:"code"`
	Caps    []string `json:"caps"` // capacités que ce code accordera
	Root    string   `json:"root,omitempty"`
	Expires int64    `json:"expires"`
}

func loadPairPending() (nodePairPending, bool) {
	var p nodePairPending
	if !getJSON(bkState, "node_pair", &p) || p.Code == "" {
		return nodePairPending{}, false
	}
	if time.Now().Unix() > p.Expires {
		return nodePairPending{}, false
	}
	return p, true
}

func savePairPending(p nodePairPending) error { return putJSON(bkState, "node_pair", p) }
func clearPairPending()                       { _ = putJSON(bkState, "node_pair", nodePairPending{}) }

// ─── Registre des postes CONNECTÉS (en mémoire) ────────────────────────────

type nodeConn struct {
	id   string
	slug string
	name string
	os   string
	caps []string // capacités EFFECTIVES (autorisées ∩ déclarées)
	root string

	conn    *websocket.Conn
	ctx     context.Context
	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[string]chan string
}

func (nc *nodeConn) send(m nodeMsg) error {
	nc.writeMu.Lock()
	defer nc.writeMu.Unlock()
	ctx, cancel := context.WithTimeout(nc.ctx, 15*time.Second)
	defer cancel()
	return wsjson.Write(ctx, nc.conn, m)
}

var (
	nodeRegMu sync.Mutex
	nodeReg   = map[string]*nodeConn{} // slug → connexion vivante
)

func nodeRegister(nc *nodeConn) {
	nodeRegMu.Lock()
	defer nodeRegMu.Unlock()
	// Un même slug déjà connecté (reconnexion, ou deux postes de même nom) : on
	// ferme l'ancien pour éviter deux postes indiscernables sous le même outil.
	if old, ok := nodeReg[nc.slug]; ok {
		_ = old.conn.Close(websocket.StatusPolicyViolation, "remplacé par une nouvelle session")
	}
	nodeReg[nc.slug] = nc
}

func nodeUnregister(nc *nodeConn) {
	nodeRegMu.Lock()
	defer nodeRegMu.Unlock()
	if cur, ok := nodeReg[nc.slug]; ok && cur == nc {
		delete(nodeReg, nc.slug)
	}
}

func nodeGet(slug string) *nodeConn {
	nodeRegMu.Lock()
	defer nodeRegMu.Unlock()
	return nodeReg[slug]
}

func nodeConnected() []*nodeConn {
	nodeRegMu.Lock()
	defer nodeRegMu.Unlock()
	out := make([]*nodeConn, 0, len(nodeReg))
	for _, nc := range nodeReg {
		out = append(out, nc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].slug < out[j].slug })
	return out
}

// ─── Endpoint WebSocket : le poste se connecte ici ─────────────────────────

// handleNodeWS accueille la connexion sortante d'un poste. Route PUBLIQUE (pas
// derrière la clé de pilotage) : l'authentification se fait par la clé d'appareil
// remise à l'appairage, présentée en Bearer.
func handleNodeWS(w http.ResponseWriter, r *http.Request) {
	key := bearerToken(r)
	pn := findNodeByKey(key)
	if pn == nil {
		http.Error(w, "poste non appairé", http.StatusUnauthorized)
		return
	}
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	c.SetReadLimit(-1)
	ctx := r.Context()

	// 1er message : le hello où le poste déclare son nom/os et ce qu'il sait faire.
	helloCtx, cancel := context.WithTimeout(ctx, nodeHelloWait)
	var hello nodeMsg
	err = wsjson.Read(helloCtx, c, &hello)
	cancel()
	if err != nil || hello.Type != "hello" {
		_ = c.Close(websocket.StatusProtocolError, "hello attendu")
		return
	}

	name := strings.TrimSpace(hello.Name)
	if name == "" {
		name = pn.Name
	}
	caps := nodeCapIntersect(pn.Caps, hello.Caps)

	nc := &nodeConn{
		id:      pn.ID,
		slug:    nodeSlug(name),
		name:    name,
		os:      strings.TrimSpace(hello.OS),
		caps:    caps,
		root:    pn.Root,
		conn:    c,
		ctx:     ctx,
		pending: map[string]chan string{},
	}
	nodeRegister(nc)
	defer nodeUnregister(nc)
	touchNodeSeen(pn.ID)

	// Boucle de lecture : les seules entrées attendues sont des « result » qui
	// débloquent l'appel en attente correspondant. Le ping/keepalive est géré par
	// coder/websocket.
	for {
		var m nodeMsg
		if err := wsjson.Read(ctx, c, &m); err != nil {
			_ = c.CloseNow()
			return
		}
		if m.Type == "result" {
			nc.mu.Lock()
			ch := nc.pending[m.ID]
			delete(nc.pending, m.ID)
			nc.mu.Unlock()
			if ch != nil {
				ch <- m.Result
			}
		}
	}
}

func touchNodeSeen(id string) {
	nodes := loadNodes()
	for i := range nodes {
		if nodes[i].ID == id {
			nodes[i].LastSeen = time.Now().Unix()
			_ = saveNodes(nodes)
			return
		}
	}
}

// bearerToken extrait le jeton d'un en-tête « Authorization: Bearer … ».
func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if s, ok := strings.CutPrefix(h, "Bearer "); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

// ─── Appel d'un outil de poste (routage serveur→poste→serveur) ─────────────

// nodeCall envoie une demande d'exécution au poste et attend son résultat. Toute
// la sécurité côté serveur tient ici : on ne route que vers un poste connecté et
// une capacité effectivement autorisée.
func nodeCall(slug, cap string, args map[string]any) string {
	nc := nodeGet(slug)
	if nc == nil {
		return "[erreur] poste « " + slug + " » déconnecté"
	}
	if !nodeCapAllowed(nc.caps, cap) {
		return "[erreur] capacité « " + cap + " » non autorisée sur ce poste"
	}
	id := nodeRandHex(8)
	ch := make(chan string, 1)
	nc.mu.Lock()
	nc.pending[id] = ch
	nc.mu.Unlock()
	defer func() {
		nc.mu.Lock()
		delete(nc.pending, id)
		nc.mu.Unlock()
	}()

	if err := nc.send(nodeMsg{Type: "call", ID: id, Cap: cap, Args: args}); err != nil {
		return "[erreur] envoi au poste impossible: " + err.Error()
	}
	select {
	case res := <-ch:
		return res
	case <-time.After(nodeCallTimeout):
		return "[timeout] le poste n'a pas répondu à temps"
	case <-nc.ctx.Done():
		return "[erreur] poste déconnecté pendant l'appel"
	}
}

// nodeEditRemote applique une édition (old→new, old unique) à un fichier d'un
// poste : on le lit, on remplace localement, on réécrit — via les capacités
// read + write du poste. Reproduit fileEdit mais à distance.
func nodeEditRemote(slug, path, oldText, newText string) string {
	if oldText == "" {
		return "[erreur] old vide"
	}
	content := nodeCall(slug, nodeCapRead, map[string]any{"path": path})
	if isNodeErr(content) {
		return content
	}
	n := strings.Count(content, oldText)
	if n == 0 {
		if newText != "" && strings.Contains(content, newText) {
			return "[ok] déjà à jour — le fichier contient déjà cette modification"
		}
		return "[erreur] old introuvable dans le fichier"
	}
	if n > 1 {
		return fmt.Sprintf("[erreur] old apparaît %d fois — ajoute du contexte pour le rendre unique", n)
	}
	updated := strings.Replace(content, oldText, newText, 1)
	return nodeCall(slug, nodeCapWrite, map[string]any{"path": path, "content": updated})
}

// isNodeErr indique si un résultat d'appel de poste est un échec (à ne pas
// traiter comme du contenu utile).
func isNodeErr(s string) bool {
	return strings.HasPrefix(s, "[erreur]") || strings.HasPrefix(s, "[refusé]") || strings.HasPrefix(s, "[timeout]")
}

// ─── Cible d'exécution de l'agent (quel PC l'IA pilote) ────────────────────

// agentTargetSlug renvoie le slug du poste sur lequel l'agent agit, ou "" pour
// le serveur local (comportement historique). Persisté dans bkState.
func agentTargetSlug() string { return getStr(bkState, "agent_target") }

func setAgentTargetSlug(slug string) error { return putStr(bkState, "agent_target", slug) }

// nodeTargetMeta décrit la cible d'exécution courante pour le prompt système.
type nodeTargetMeta struct {
	slug, name, os, root string
	connected            bool
}

// nodeTargetMetaGet renvoie la cible sélectionnée (ok=false si c'est le serveur
// local). Complète nom/os/dossier depuis le registre si le poste est connecté,
// sinon depuis l'enregistrement d'appairage.
func nodeTargetMetaGet() (nodeTargetMeta, bool) {
	slug := agentTargetSlug()
	if slug == "" {
		return nodeTargetMeta{}, false
	}
	m := nodeTargetMeta{slug: slug}
	if nc := nodeGet(slug); nc != nil {
		m.name, m.os, m.root, m.connected = nc.name, nc.os, nc.root, true
		return m, true
	}
	for _, n := range loadNodes() {
		if nodeSlug(n.Name) == slug {
			m.name, m.os, m.root = n.Name, n.OS, n.Root
			break
		}
	}
	if m.name == "" {
		m.name = slug
	}
	return m, true
}

// agentTargetShellName renvoie le shell de la MACHINE CIBLE : cmd.exe si le poste
// ciblé tourne sous Windows, bash sinon. Sans cible → le shell du serveur local.
// Indispensable : le serveur peut être sous Linux (bash) alors que le poste est
// sous Windows (cmd.exe) — annoncer le mauvais shell fait écrire au modèle une
// syntaxe que la machine cible ne comprend pas.
func agentTargetShellName() string {
	if tgt, ok := nodeTargetMetaGet(); ok {
		if strings.HasPrefix(strings.ToLower(tgt.os), "windows") {
			return "cmd.exe"
		}
		return "bash"
	}
	return shellName()
}
