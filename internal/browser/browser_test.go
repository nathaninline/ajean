package browser

import (
	"errors"
	"strings"
	"testing"

	"ajean/internal/tool"
)

func TestAddress(t *testing.T) {
	cases := []struct{ in, want string }{
		{"example.com", "https://example.com"},
		{"  example.com/a?b=1 ", "https://example.com/a?b=1"},
		{"example.com:8080", "https://example.com:8080"},
		{"example.com:8080/x", "https://example.com:8080/x"},
		{"localhost:3000", "https://localhost:3000"},
		{"http://example.com", "http://example.com"},
		{"https://example.com", "https://example.com"},
		{"file:///C:/Windows/win.ini", "file:///C:/Windows/win.ini"},
		{"javascript:alert(1)", "javascript:alert(1)"},
		{"about:blank", "about:blank"},
	}
	for _, c := range cases {
		got, err := address(c.in)
		if err != nil || got != c.want {
			t.Errorf("address(%q) = %q, %v ; attendu %q", c.in, got, err, c.want)
		}
	}
	if _, err := address("   "); err == nil {
		t.Error("une adresse vide doit être refusée")
	}
}

// Aucune de ces adresses ne demande de résolution de nom : le test ne touche
// pas au réseau.
func TestCheckURL(t *testing.T) {
	refused := []string{
		"file:///C:/Windows/win.ini",
		"http://127.0.0.1:8090",
		"http://localhost",
		"http://LOCALHOST./",
		"http://app.localhost:3000/",
		"http://[::1]/",
		"http://[::ffff:127.0.0.1]/",
		"http://0.0.0.0/",
		"http://169.254.169.254/latest/meta-data/",
		"http://192.168.1.1/",
		"http://10.0.0.1/",
		"javascript:alert(1)",
		"about:blank",
		"chrome://settings",
		"data:text/html,<p>x</p>",
		"http://",
	}
	for _, u := range refused {
		if checkURL(u) == nil {
			t.Errorf("%s aurait dû être refusée", u)
		}
	}
	for _, u := range []string{"https://93.184.216.34/", "http://8.8.8.8:8080/x"} {
		if err := checkURL(u); err != nil {
			t.Errorf("%s refusée : %v", u, err)
		}
	}
}

func TestCheckURLLocalNetwork(t *testing.T) {
	t.Setenv("AJEAN_TOOLS_LAN", "1")
	if err := checkURL("http://192.168.1.1/"); err != nil {
		t.Errorf("réseau local refusé malgré AJEAN_TOOLS_LAN : %v", err)
	}
	if checkURL("http://127.0.0.1/") == nil {
		t.Error("la machine locale doit rester refusée avec AJEAN_TOOLS_LAN")
	}
}

func TestFindKey(t *testing.T) {
	cases := []struct {
		in         string
		name, code string
		vk         int
		text       string
	}{
		{"Enter", "Enter", "Enter", 13, "\r"},
		{" RETURN ", "Enter", "Enter", 13, "\r"},
		{"esc", "Escape", "Escape", 27, ""},
		{"Space", " ", "Space", 32, " "},
		{"Page Down", "PageDown", "PageDown", 34, ""},
		{"arrowLeft", "ArrowLeft", "ArrowLeft", 37, ""},
	}
	for _, c := range cases {
		k, err := findKey(c.in)
		if err != nil || k != (key{c.name, c.code, c.vk, c.text}) {
			t.Errorf("findKey(%q) = %+v, %v", c.in, k, err)
		}
	}
	for _, in := range []string{"", "F5", "ctrl+a", "a"} {
		if _, err := findKey(in); err == nil {
			t.Errorf("findKey(%q) aurait dû échouer", in)
		}
	}
	// Seules les touches qui produisent un caractère en portent un.
	for name, k := range keys {
		if (k.text != "") != (k.name == "Enter" || k.name == " ") {
			t.Errorf("touche %s : texte %q inattendu", name, k.text)
		}
	}
}

func TestClip(t *testing.T) {
	if got := clip("été", 3); got != "été" {
		t.Errorf("texte court modifié : %q", got)
	}
	if got := clip("éléphant", 3); got != "élé…" {
		t.Errorf("coupe = %q, attendu des caractères entiers", got)
	}
}

func TestRender(t *testing.T) {
	got := snapshot{Title: "Accueil", URL: "https://example.com/", Text: "# Titre\n[1] link: Suite"}.render()
	want := "Page : Accueil\nURL : https://example.com/\n\n# Titre\n[1] link: Suite"
	if got != want {
		t.Errorf("vue simple :\n%s\nattendu :\n%s", got, want)
	}

	got = snapshot{Title: "T", URL: "https://example.com/", Truncated: true, Y: 900, H: 4000}.render()
	for _, part := range []string{"(vue à partir de 900 px sur 4000 : action=scroll direction=up pour remonter)\n", "\n(page vide)", "[vue tronquée : action=scroll direction=down pour lire la suite]"} {
		if !strings.Contains(got, part) {
			t.Errorf("vue défilée sans %q :\n%s", part, got)
		}
	}

	long := snapshot{Title: strings.Repeat("t", 500), URL: "https://example.com/", Text: strings.Repeat("é", 2*viewMax)}.render()
	if n := len([]rune(long)); n != viewMax+1 {
		t.Errorf("vue longue de %d caractères, attendu %d", n, viewMax+1)
	}
	if !strings.HasPrefix(long, "Page : "+strings.Repeat("t", 200)+"…\n") {
		t.Error("un titre trop long doit être coupé")
	}
}

func TestReport(t *testing.T) {
	notes := []string{"téléchargement lancé : a.pdf", "dialogue alert"}
	got := report("Page : T", nil, notes)
	if want := "[note] téléchargement lancé : a.pdf\n[note] dialogue alert\nPage : T"; got != want {
		t.Errorf("vue avec notes = %q, attendu %q", got, want)
	}
	if got := report("Page : T", nil, nil); got != "Page : T" {
		t.Errorf("vue sans note = %q", got)
	}
	got = report("", errors.New("pas de page précédente"), notes[:1])
	if want := "[erreur] pas de page précédente\n[note] téléchargement lancé : a.pdf"; got != want {
		t.Errorf("erreur avec note = %q, attendu %q", got, want)
	}
	if !tool.IsErr(got) {
		t.Error("une erreur doit garder son préfixe en tête")
	}
	if got := report("", errors.New("x"), nil); got != "[erreur] x" {
		t.Errorf("erreur sans note = %q", got)
	}
}

func TestRefArg(t *testing.T) {
	cases := []struct {
		ref  any
		want int
	}{
		{float64(12), 12},
		{"12", 12},
		{" [12] ", 12},
		{"douze", 0},
		{nil, 0},
	}
	for _, c := range cases {
		if got := refArg(tool.Call{Args: map[string]any{"ref": c.ref}}); got != c.want {
			t.Errorf("refArg(%#v) = %d, attendu %d", c.ref, got, c.want)
		}
	}
}

func TestTools(t *testing.T) {
	defs := Tools()
	if len(defs) != 1 || defs[0].Name != "browser" || defs[0].Run == nil {
		t.Fatalf("un seul outil « browser » attendu : %+v", defs)
	}
	props := defs[0].Params["properties"].(map[string]any)
	for _, name := range []string{"action", "url", "ref", "text", "submit", "direction"} {
		if _, ok := props[name]; !ok {
			t.Errorf("paramètre %s manquant", name)
		}
	}
}

// Un appel mal formé est refusé avant tout lancement de navigateur.
func TestRunRejectsBadCalls(t *testing.T) {
	cases := []struct {
		args map[string]any
		want string
	}{
		{map[string]any{}, "action inconnue"},
		{map[string]any{"action": "upload"}, "action inconnue"},
		{map[string]any{"action": "click"}, "ref manquant"},
		{map[string]any{"action": "type", "ref": float64(0), "text": "x"}, "ref manquant"},
		{map[string]any{"action": "select", "ref": float64(3), "text": "  "}, "text manquant"},
	}
	for _, c := range cases {
		got := run(tool.Call{Args: c.args})
		if !tool.IsErr(got) || !strings.Contains(got, c.want) {
			t.Errorf("run(%v) = %q, attendu une erreur « %s »", c.args, got, c.want)
		}
	}
	if current != nil {
		t.Error("aucun navigateur ne devait être lancé")
	}
}

func TestShutdownWithoutBrowser(t *testing.T) {
	Shutdown()
	if got := run(tool.Call{Args: map[string]any{"action": "close"}}); !tool.IsOK(got) {
		t.Errorf("close sans navigateur = %q", got)
	}
}
