// node_server.go — côté SERVEUR du poste distant : appairage, endpoint
// WebSocket, registre des postes connectés, et exposition de leurs outils à
// l'agent. Le serveur n'exécute JAMAIS rien lui-même ici : il route une demande
// d'outil vers le poste concerné et attend sa réponse.
package ajean

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/nathaninline/ajean/internal/nodewire"
)

const (
	nodePairCodeTTL = 10 * time.Minute
	nodeCallTimeout = 5 * time.Minute // un shell distant peut être long
	nodeHelloWait   = 15 * time.Second
)

// pairedNode est un poste appairé, tel que persisté dans bkState["nodes"].
// L'identité du poste = sa CLÉ PUBLIQUE X25519 (PubHex). Il n'y a plus de « clé
// d'appareil » partagée : le poste prouve son identité en sachant chiffrer le
// canal (ECDH avec sa clé privée), ce que le relais ne peut pas reproduire.
type pairedNode struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	OS        string   `json:"os"`
	PubHex    string   `json:"pub"`  // clé publique X25519 du poste (son identité)
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

// findNodeByPub retrouve un poste appairé à partir de sa clé publique.
func findNodeByPub(pub string) *pairedNode {
	pub = strings.ToLower(strings.TrimSpace(pub))
	if pub == "" {
		return nil
	}
	nodes := loadNodes()
	for i := range nodes {
		if strings.ToLower(nodes[i].PubHex) == pub {
			return &nodes[i]
		}
	}
	return nil
}

// agentPrivHex renvoie la clé privée X25519 de l'agent en hex (pour dériver la
// clé de canal poste↔agent via nodewire). Même clé que le canal navigateur, mais
// domaine séparé par la constante de dérivation.
func agentPrivHex() (string, error) {
	k, err := e2ePrivateKey()
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(k.Bytes()), nil
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
	ch      *nodewire.Chan // canal chiffré poste↔agent (relais aveugle)
	writeMu sync.Mutex

	mu      sync.Mutex
	pending map[string]chan string
}

// send chiffre le message et l'envoie en un frame opaque : le relais ne voit
// que du ciphertext.
func (nc *nodeConn) send(m nodeMsg) error {
	nc.writeMu.Lock()
	defer nc.writeMu.Unlock()
	raw, err := json.Marshal(m)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(nc.ctx, 15*time.Second)
	defer cancel()
	return wsjson.Write(ctx, nc.conn, nc.ch.Seal(raw))
}

// readMsg lit un frame chiffré et le déchiffre en nodeMsg.
func (nc *nodeConn) readMsg(ctx context.Context) (nodeMsg, error) {
	var fr nodewire.Frame
	if err := wsjson.Read(ctx, nc.conn, &fr); err != nil {
		return nodeMsg{}, err
	}
	plain, err := nc.ch.Open(fr)
	if err != nil {
		return nodeMsg{}, err
	}
	var m nodeMsg
	if err := json.Unmarshal(plain, &m); err != nil {
		return nodeMsg{}, err
	}
	return m, nil
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

// handleNodeWS accueille la connexion sortante d'un poste. Route PUBLIQUE : elle
// peut arriver en direct (LAN) OU tunnelée par le relais ajean.link. Dans les
// deux cas le canal est chiffré de BOUT EN BOUT (poste↔agent) : le relais ne voit
// que de l'opaque. L'authentification = la capacité du poste à chiffrer avec la
// clé de canal, que seul le détenteur de la clé privée appairée peut calculer.
func handleNodeWS(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	c.SetReadLimit(-1)
	ctx := r.Context()

	// Poignée de main : 1er frame EN CLAIR = la clé publique du poste (elle est
	// publique, aucun secret). Elle nous dit QUI se connecte et permet de dériver
	// la clé de canal. Le poste ne pourra chiffrer/déchiffrer que s'il détient la
	// clé privée correspondante — c'est ça, l'authentification.
	helloCtx, cancel := context.WithTimeout(ctx, nodeHelloWait)
	var hp struct {
		Type string `json:"type"`
		Pub  string `json:"pub"`
	}
	err = wsjson.Read(helloCtx, c, &hp)
	cancel()
	if err != nil || hp.Type != "hello_pub" || hp.Pub == "" {
		_ = c.Close(websocket.StatusProtocolError, "hello_pub attendu")
		return
	}
	pn := findNodeByPub(hp.Pub)
	if pn == nil {
		_ = c.Close(websocket.StatusPolicyViolation, "poste non appairé")
		return
	}
	privHex, err := agentPrivHex()
	if err != nil {
		_ = c.Close(websocket.StatusInternalError, "clé agent indisponible")
		return
	}
	key, err := nodewire.ChannelKey(privHex, hp.Pub)
	if err != nil {
		_ = c.Close(websocket.StatusInternalError, "canal")
		return
	}
	ch, err := nodewire.NewChan(key, false) // agent = côté d'envoi 2
	if err != nil {
		_ = c.Close(websocket.StatusInternalError, "canal")
		return
	}

	nc := &nodeConn{
		id:      pn.ID,
		conn:    c,
		ctx:     ctx,
		ch:      ch,
		pending: map[string]chan string{},
		root:    pn.Root,
	}

	// 1er frame CHIFFRÉ : le hello (nom/os/capacités déclarées). S'il ne déchiffre
	// pas, le poste n'a pas la bonne clé privée → connexion refusée.
	helloCtx2, cancel2 := context.WithTimeout(ctx, nodeHelloWait)
	hello, err := nc.readMsg(helloCtx2)
	cancel2()
	if err != nil || hello.Type != "hello" {
		_ = c.Close(websocket.StatusPolicyViolation, "hello chiffré attendu")
		return
	}
	name := strings.TrimSpace(hello.Name)
	if name == "" {
		name = pn.Name
	}
	nc.name = name
	nc.slug = nodeSlug(name)
	nc.os = strings.TrimSpace(hello.OS)
	nc.caps = nodeCapIntersect(pn.Caps, hello.Caps)

	nodeRegister(nc)
	defer nodeUnregister(nc)
	touchNodeSeen(pn.ID)

	// Boucle de lecture : des « result » chiffrés qui débloquent l'appel en attente.
	for {
		m, err := nc.readMsg(ctx)
		if err != nil {
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

// ─── Appel d'un outil de poste (routage serveur→poste→serveur) ─────────────

// nodeCall envoie une demande d'exécution au poste et attend son résultat. Toute
// la sécurité côté serveur tient ici : on ne route que vers un poste connecté et
// une capacité effectivement autorisée.
func nodeCall(slug, cap string, args map[string]any) string {
	nc := nodeGet(slug)
	if nc == nil {
		return "[erreur] poste « " + slug + " » déconnecté"
	}
	// fetch (téléchargement) est autorisé partout où read l'est (CapAuthFor) : pas
	// besoin de re-pairer un poste pour lui permettre de te livrer un fichier.
	if !nodeCapAllowed(nc.caps, nodewire.CapAuthFor(cap)) {
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

// nodeFetchedFile = une tranche de fichier rapatriée d'un poste (miroir de ce
// que fetchFile encode côté client).
type nodeFetchedFile struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	Offset int64  `json:"offset"`
	Data   string `json:"data"` // base64 de la tranche
	EOF    bool   `json:"eof"`
}

// nodeFetchFile rapatrie une tranche binaire d'un fichier du poste `slug`
// (offset/length en octets), pour le téléchargement. `length<=0` = requête de
// métadonnées (nom+taille sans données). Renvoie l'erreur telle que rapportée
// par le poste (chaîne « [erreur] … ») le cas échéant.
func nodeFetchFile(slug, path string, offset, length int64) (nodeFetchedFile, error) {
	res := nodeCall(slug, nodeCapFetch, map[string]any{
		"path": path, "offset": offset, "len": length,
	})
	if isNodeErr(res) {
		return nodeFetchedFile{}, fmt.Errorf("%s", strings.TrimSpace(res))
	}
	var f nodeFetchedFile
	if err := json.Unmarshal([]byte(res), &f); err != nil {
		return nodeFetchedFile{}, fmt.Errorf("réponse du poste illisible")
	}
	return f, nil
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

// toolMachinesList rend une vue lisible des postes appairés et de la cible
// d'exécution courante, pour l'outil machines_list. Tourne dans le process
// serveur, donc l'état de connexion vif (nodeReg) est disponible.
func toolMachinesList() string {
	connected := map[string]bool{}
	for _, nc := range nodeConnected() {
		connected[nc.slug] = true
	}
	target := agentTargetSlug()
	nodes := loadNodes()
	var b strings.Builder
	if target == "" {
		b.WriteString("Current target: local (this server)\n")
	} else {
		b.WriteString("Current target: " + target + "\n")
	}
	if len(nodes) == 0 {
		b.WriteString("\nNo poste paired yet. To add one, run `ajean postes pair` with bash to get a pairing code, then on the target machine (reachable by ssh) run the printed `ajean remote install ...` command.")
		return b.String()
	}
	b.WriteString("\nPostes:")
	for _, n := range nodes {
		slug := nodeSlug(n.Name)
		state := "offline"
		if connected[slug] {
			state = "online"
		}
		mark := ""
		if slug == target {
			mark = " (current target)"
		}
		fmt.Fprintf(&b, "\n- %s [slug: %s] — %s, %s", n.Name, slug, state, n.OS)
		if len(n.Caps) > 0 {
			b.WriteString(", caps: " + strings.Join(n.Caps, ","))
		}
		if n.Root != "" {
			b.WriteString(", folder: " + n.Root)
		}
		b.WriteString(mark)
	}
	b.WriteString("\n\nUse machines_use with a slug to operate on that poste, or \"local\" for this server.")
	return b.String()
}

// toolMachinesUse bascule la cible d'exécution de l'agent (outil machines_use).
// "local"/"" = serveur local ; sinon un slug de poste appairé (validé).
func toolMachinesUse(args map[string]any) string {
	raw, _ := args["machine"].(string)
	slug := strings.TrimSpace(raw)
	if slug == "" || strings.EqualFold(slug, "local") || strings.EqualFold(slug, "serveur") || strings.EqualFold(slug, "server") {
		if err := setAgentTargetSlug(""); err != nil {
			return "[erreur] " + err.Error()
		}
		return "[ok] cible = local (ce serveur). Tes prochains bash/write/edit s'exécutent ici."
	}
	// Validation : le slug doit correspondre à un poste appairé.
	found := ""
	for _, n := range loadNodes() {
		if nodeSlug(n.Name) == slug {
			found = n.Name
			break
		}
	}
	if found == "" {
		return "[erreur] aucun poste avec le slug « " + slug + " » — utilise machines_list pour voir les slugs disponibles"
	}
	if err := setAgentTargetSlug(slug); err != nil {
		return "[erreur] " + err.Error()
	}
	warn := ""
	if nodeGet(slug) == nil {
		warn = " ⚠ Ce poste est actuellement hors ligne : tes outils échoueront jusqu'à sa reconnexion."
	}
	return "[ok] cible = « " + found + " » (slug " + slug + "). Tes prochains bash/write/edit s'exécutent sur cette machine." + warn
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
