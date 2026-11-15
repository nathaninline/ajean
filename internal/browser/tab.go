package browser

import (
	"errors"
	"time"
)

const tabPoll = 20 * time.Millisecond

// syncTarget suit l'onglet qui compte : celui qui vient de s'ouvrir (clic sur un
// lien « nouvelle fenêtre »), sinon l'onglet courant, sinon un autre s'il a été fermé.
func (b *instance) syncTarget() error {
	var r struct {
		TargetInfos []struct{ TargetID, Type string }
	}
	if err := b.conn.call("", "Target.getTargets", nil, &r, callTimeout); err != nil {
		return err
	}
	pick, seen, current := "", map[string]bool{}, false
	for _, t := range r.TargetInfos {
		if t.Type != "page" {
			continue
		}
		seen[t.TargetID] = true
		current = current || t.TargetID == b.target
		if !b.known[t.TargetID] {
			pick = t.TargetID
		}
	}
	b.known = seen
	if pick == "" && current {
		return nil
	}
	for id := range seen {
		if pick == "" {
			pick = id
		}
	}
	if pick == "" { // plus aucun onglet
		var n struct{ TargetID string }
		if err := b.conn.call("", "Target.createTarget", map[string]any{"url": blank}, &n, callTimeout); err != nil {
			return err
		}
		pick, b.known[n.TargetID] = n.TargetID, true
	}
	// L'onglet est pilotable dès que equip a fini de le préparer.
	for deadline := time.Now().Add(callTimeout); ; time.Sleep(tabPoll) {
		b.mu.Lock()
		session := b.sessions[pick]
		b.mu.Unlock()
		if session != "" {
			b.target, b.session = pick, session
			break
		}
		if time.Now().After(deadline) {
			return errors.New("onglet impossible à piloter")
		}
	}
	// Au premier plan : un onglet d'arrière-plan tourne au ralenti.
	return b.conn.call("", "Target.activateTarget", map[string]any{"targetId": pick}, nil, callTimeout)
}
