// node_api.go — endpoints HTTP du poste distant.
//
// Deux familles :
//   - GESTION (propriétaire, derrière la clé de pilotage) : générer un code
//     d'appairage, lister/configurer/révoquer les postes.
//   - ENRÔLEMENT (public) : le poste échange un code d'appairage à usage unique
//     contre sa clé d'appareil. Authentifié par le code, pas par la clé de
//     pilotage — que le poste ne possède pas.
package ajean

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// handleNodePair (authed) génère un code d'appairage à usage unique valable
// nodePairCodeTTL. Le propriétaire choisit les capacités accordées et le dossier
// racine ; le poste ne pourra jamais dépasser ça.
func handleNodePair(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Caps []string `json:"caps"`
		Root string   `json:"root"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	caps := nodeSanitizeCaps(req.Caps)
	if len(caps) == 0 {
		// Défaut prudent : lecture seule. Le propriétaire élargit ensuite.
		caps = []string{nodeCapRead, nodeCapList}
	}
	p := nodePairPending{
		Code:    strings.ToUpper(nodeRandHex(4)), // 8 caractères hex lisibles
		Caps:    caps,
		Root:    strings.TrimSpace(req.Root),
		Expires: time.Now().Add(nodePairCodeTTL).Unix(),
	}
	if err := savePairPending(p); err != nil {
		sendJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{
		"ok":      true,
		"code":    p.Code,
		"caps":    p.Caps,
		"root":    p.Root,
		"expires": p.Expires,
		"ttl_min": int(nodePairCodeTTL.Minutes()),
	})
}

// handleNodeEnroll (PUBLIC) échange un code d'appairage contre une clé
// d'appareil. Le code est à usage unique : consommé dès qu'il sert.
func handleNodeEnroll(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
		Name string `json:"name"`
		OS   string `json:"os"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"error": "requête invalide"})
		return
	}
	pending, ok := loadPairPending()
	if !ok {
		sendJSON(w, 403, map[string]any{"error": "aucun code d'appairage actif — générez-en un dans l'interface"})
		return
	}
	if !strings.EqualFold(strings.TrimSpace(req.Code), pending.Code) {
		sendJSON(w, 403, map[string]any{"error": "code incorrect"})
		return
	}
	// Consomme le code immédiatement (usage unique), même si la suite échoue.
	clearPairPending()

	key := nodeRandHex(24)
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = "poste"
	}
	node := pairedNode{
		ID:        nodeRandHex(8),
		Name:      name,
		OS:        strings.TrimSpace(req.OS),
		KeyHash:   nodeHashKey(key),
		Caps:      pending.Caps,
		Root:      pending.Root,
		CreatedAt: time.Now().Unix(),
	}
	nodes := append(loadNodes(), node)
	if err := saveNodes(nodes); err != nil {
		sendJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	// La clé n'est renvoyée QU'ICI, une seule fois : le poste la stocke en local.
	sendJSON(w, 200, map[string]any{
		"ok":   true,
		"id":   node.ID,
		"key":  key,
		"caps": node.Caps,
		"root": node.Root,
		"name": node.Name,
	})
}

// handleNodes (authed) liste les postes appairés avec leur état de connexion.
func handleNodes(w http.ResponseWriter, r *http.Request) {
	connected := map[string]bool{}
	for _, nc := range nodeConnected() {
		connected[nc.id] = true
	}
	nodes := loadNodes()
	out := make([]map[string]any, 0, len(nodes))
	for _, n := range nodes {
		out = append(out, map[string]any{
			"id":         n.ID,
			"name":       n.Name,
			"slug":       nodeSlug(n.Name),
			"os":         n.OS,
			"caps":       n.Caps,
			"root":       n.Root,
			"created_at": n.CreatedAt,
			"last_seen":  n.LastSeen,
			"connected":  connected[n.ID],
		})
	}
	_, pending := loadPairPending()
	sendJSON(w, 200, map[string]any{"ok": true, "nodes": out, "all_caps": nodeAllCaps, "pairing": pending, "target": agentTargetSlug()})
}

// handleNodeTarget (authed) choisit la machine sur laquelle l'agent agit :
// slug d'un poste, ou "" pour le serveur local.
func handleNodeTarget(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Slug string `json:"slug"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"error": "requête invalide"})
		return
	}
	if err := setAgentTargetSlug(strings.TrimSpace(req.Slug)); err != nil {
		sendJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "target": agentTargetSlug()})
}

// handleNodeCaps (authed) met à jour les capacités autorisées et/ou le dossier
// racine d'un poste. Prend effet à la prochaine (re)connexion du poste.
func handleNodeCaps(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID   string   `json:"id"`
		Caps []string `json:"caps"`
		Root *string  `json:"root"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"error": "requête invalide"})
		return
	}
	nodes := loadNodes()
	found := false
	for i := range nodes {
		if nodes[i].ID == req.ID {
			nodes[i].Caps = nodeSanitizeCaps(req.Caps)
			if req.Root != nil {
				nodes[i].Root = strings.TrimSpace(*req.Root)
			}
			found = true
			break
		}
	}
	if !found {
		sendJSON(w, 404, map[string]any{"error": "poste inconnu"})
		return
	}
	if err := saveNodes(nodes); err != nil {
		sendJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	// Coupe la session en cours pour que les nouvelles capacités s'appliquent
	// (le poste se reconnecte et re-négocie l'intersection).
	for _, nc := range nodeConnected() {
		if nc.id == req.ID {
			_ = nc.conn.Close(websocket.StatusNormalClosure, "capacités mises à jour")
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// handleNodeRevoke (authed) révoque un poste : oublie sa clé et le déconnecte.
func handleNodeRevoke(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"error": "requête invalide"})
		return
	}
	nodes := loadNodes()
	kept := nodes[:0]
	removed := false
	for _, n := range nodes {
		if n.ID == req.ID {
			removed = true
			continue
		}
		kept = append(kept, n)
	}
	if !removed {
		sendJSON(w, 404, map[string]any{"error": "poste inconnu"})
		return
	}
	if err := saveNodes(kept); err != nil {
		sendJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	for _, nc := range nodeConnected() {
		if nc.id == req.ID {
			_ = nc.conn.Close(websocket.StatusPolicyViolation, "poste révoqué")
		}
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}
