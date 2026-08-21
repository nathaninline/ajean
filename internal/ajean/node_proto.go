// node_proto.go — côté serveur, alias vers le protocole partagé (internal/nodewire)
// + les helpers de nommage d'outils propres à ajean.
//
// Le format du fil (Msg, capacités, confinement de chemin) vit dans nodewire,
// paquet SANS dépendance, pour que le client léger ajean-node le partage sans
// tirer tout ajean. Ici on ne fait qu'exposer ces symboles sous les noms
// historiques du paquet, pour ne pas toucher le reste du code serveur.
package ajean

import (
	"strings"

	"github.com/nathaninline/ajean/internal/nodewire"
)

// Capacités (alias des constantes partagées).
const (
	nodeCapShell = nodewire.CapShell
	nodeCapRead  = nodewire.CapRead
	nodeCapWrite = nodewire.CapWrite
	nodeCapList  = nodewire.CapList
	nodeCapFetch = nodewire.CapFetch
)

// nodeAllCaps : ordre canonique partagé.
var nodeAllCaps = nodewire.AllCaps

// nodeMsg : enveloppe du fil (alias de type → interop garantie avec le client).
type nodeMsg = nodewire.Msg

func nodeCapAllowed(allowed []string, cap string) bool { return nodewire.CapAllowed(allowed, cap) }
func nodeCapIntersect(allowed, declared []string) []string {
	return nodewire.CapIntersect(allowed, declared)
}
func nodeSanitizeCaps(caps []string) []string        { return nodewire.SanitizeCaps(caps) }
func nodeResolvePath(root, p string) (string, error) { return nodewire.ResolvePath(root, p) }

// ── Helpers de nommage d'outils, propres au serveur ────────────────────────
// (conservés pour les tests ; l'IA ne reçoit plus d'outils node__* — voir la
// refonte « cible d'exécution » dans llm_client.go / node_server.go.)

// nodeSlug transforme un nom de poste en identifiant sûr ([A-Za-z0-9_-]).
func nodeSlug(name string) string {
	s := mcpSanitize(name)
	if s == "" {
		s = "poste"
	}
	return s
}

func nodeToolName(slug, cap string) string { return "node__" + slug + "__" + cap }

func isNodeTool(name string) bool { return strings.HasPrefix(name, "node__") }

func parseNodeToolName(name string) (slug, cap string, ok bool) {
	rest, found := strings.CutPrefix(name, "node__")
	if !found {
		return "", "", false
	}
	i := strings.LastIndex(rest, "__")
	if i < 0 {
		return "", "", false
	}
	slug, cap = rest[:i], rest[i+2:]
	if slug == "" || cap == "" {
		return "", "", false
	}
	return slug, cap, true
}
