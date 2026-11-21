package ajean

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Une adresse par écran (ui/src/js/26-route.js). Le routeur ne connaît les écrans
// que par des identifiants du gabarit : une fenêtre ou une section renommée
// casserait son adresse en silence (elle retomberait sur la conversation). Ce
// test relit les deux fichiers et exige que chaque adresse mène quelque part.
func TestUIRoutesPointAtRealScreens(t *testing.T) {
	js := readUISource(t, "ui/src/js/26-route.js")
	tmpl := readUISource(t, "ui/src/index.tmpl.html")

	modals := routeTable(t, js, "ROUTE_MODALS")
	for name, id := range modals {
		if !strings.Contains(tmpl, `id="`+id+`"`) {
			t.Errorf("adresse #/%s : la fenêtre %q n'existe plus dans le gabarit", name, id)
		}
	}
	sections := routeTable(t, js, "ROUTE_SECTIONS")
	for name, key := range sections {
		re := regexp.MustCompile(`<details[^>]*><summary><span class="stitle" data-i18n="` + regexp.QuoteMeta(key) + `"`)
		if !re.MatchString(tmpl) {
			t.Errorf("adresse #/%s : aucune section du menu ne porte le titre %q", name, key)
		}
	}
	// Les écrans attendus ont tous leur adresse.
	for _, want := range []string{"files", "memory", "trackers"} {
		if modals[want] == "" {
			t.Errorf("pas d'adresse pour l'écran %q", want)
		}
	}
	for _, want := range []string{"models", "tasks", "settings", "engine"} {
		if sections[want] == "" {
			t.Errorf("pas d'adresse pour la section %q", want)
		}
	}
	for _, id := range []string{"side-main", "side-hist", "side-apps", "app-run"} {
		if !strings.Contains(tmpl, `id="`+id+`"`) {
			t.Errorf("le routeur observe #%s, absent du gabarit", id)
		}
	}
}

// L'adresse d'un écran ne vit QUE dans le fragment. La page est aussi servie sous
// /u/<id> (relais) et dans server.html?m=… (app.ajean.link), et API_BASE se déduit
// du chemin : écrire un chemin dans l'historique casserait les appels /api/… au
// rechargement. Toute écriture d'historique du routeur doit donc commencer par « # ».
func TestUIRoutesOnlyWriteTheFragment(t *testing.T) {
	js := readUISource(t, "ui/src/js/26-route.js")
	calls := regexp.MustCompile(`history\.(?:push|replace)State\(([^)]*)\)`).FindAllStringSubmatch(js, -1)
	if len(calls) == 0 {
		t.Fatal("le routeur n'écrit plus l'adresse")
	}
	for _, c := range calls {
		args := strings.Split(c[1], ",")
		if len(args) != 3 {
			t.Fatalf("appel inattendu : %s", c[0])
		}
		v := strings.TrimSpace(args[2])
		def := regexp.MustCompile(`const ` + regexp.QuoteMeta(v) + ` = '#'\+routeNow\(\);`)
		if !def.MatchString(js) {
			t.Errorf("%s : %q n'est pas une adresse en fragment ('#'+routeNow())", c[0], v)
		}
	}
	for _, banned := range []string{"location.pathname", "location.assign", "location.replace", "location.href ="} {
		if strings.Contains(js, banned) {
			t.Errorf("le routeur touche au chemin de la page (%s)", banned)
		}
	}
}

func readUISource(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// routeTable lit une table « const NOM = {nom:'valeur', …}; » du routeur.
func routeTable(t *testing.T, js, name string) map[string]string {
	t.Helper()
	m := regexp.MustCompile(`const ` + name + ` = \{([^}]*)\};`).FindStringSubmatch(js)
	if m == nil {
		t.Fatalf("table %s introuvable dans le routeur", name)
	}
	out := map[string]string{}
	for _, kv := range regexp.MustCompile(`(\w+):'([^']+)'`).FindAllStringSubmatch(m[1], -1) {
		out[kv[1]] = kv[2]
	}
	if len(out) == 0 {
		t.Fatalf("table %s vide", name)
	}
	return out
}
