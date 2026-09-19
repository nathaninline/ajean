package ajean

import "testing"

// TestDedupableTool verrouille le fix du test de navigation : les outils à état vivant /
// à image (cu_*, see_image, bash) ne doivent JAMAIS être dédupliqués, sinon un
// second appel identique renvoie « [déjà fait] » sans rejouer l'action ni
// l'image, et l'IA boucle.
func TestDedupableTool(t *testing.T) {
	never := []string{"bash", "see_image", "browser_open", "browser_snapshot", "browser_click", "browser_type", "browser_key", "browser_scroll", "browser_screenshot"}
	for _, n := range never {
		if dedupableTool(n) {
			t.Errorf("%s ne doit PAS être déduplicable", n)
		}
	}
	always := []string{"write", "edit", "mem_add", "web_open"}
	for _, n := range always {
		if !dedupableTool(n) {
			t.Errorf("%s devrait rester déduplicable", n)
		}
	}
}
