package ajean

import (
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestCDPSmoke pilote un vrai Chrome de bout en bout : lancement, navigation,
// snapshot d'accessibilité, click, screenshot. Coûteux et dépendant d'un
// navigateur installé → ne tourne QUE si AJEAN_CU_SMOKE=1.
func TestCDPSmoke(t *testing.T) {
	if os.Getenv("AJEAN_CU_SMOKE") != "1" {
		t.Skip("smoke test désactivé (AJEAN_CU_SMOKE=1 pour l'activer)")
	}
	if chromePath() == "" {
		t.Skip("aucun navigateur détecté")
	}
	s, err := cdpGet()
	if err != nil {
		t.Fatalf("cdpGet: %v", err)
	}
	defer cdpShutdown()

	if err := s.navigate("https://example.com"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	snap, err := s.snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	t.Logf("snapshot:\n%s", snap)
	if !strings.Contains(strings.ToLower(snap), "example") {
		t.Errorf("titre 'example' attendu dans le snapshot, absent")
	}
	if !strings.Contains(snap, "link") {
		t.Errorf("le lien 'More information' devrait apparaître comme [n] link")
	}
	// browser_find doit retrouver le lien par son texte.
	found, err := s.find("more")
	if err != nil {
		t.Fatalf("find: %v", err)
	}
	if !strings.Contains(strings.ToLower(found), "link") {
		t.Errorf("browser_find('more') devrait trouver le lien, got: %s", found)
	}
	// Le screenshot doit produire des octets PNG non vides.
	png, err := s.screenshot()
	if err != nil {
		t.Fatalf("screenshot: %v", err)
	}
	if len(png) < 100 {
		t.Errorf("screenshot suspicieusement petit: %d octets", len(png))
	}
	t.Logf("screenshot: %d octets", len(png))
}

// TestCDPTypeReplacesField vérifie le fix du test de navigation #4 : browser_type(ref) cible le
// BON champ et REMPLACE son contenu, même avec deux champs et une valeur existante.
func TestCDPTypeReplacesField(t *testing.T) {
	if os.Getenv("AJEAN_CU_SMOKE") != "1" {
		t.Skip("smoke test désactivé (AJEAN_CU_SMOKE=1 pour l'activer)")
	}
	if chromePath() == "" {
		t.Skip("aucun navigateur détecté")
	}
	s, err := cdpGet()
	if err != nil {
		t.Fatalf("cdpGet: %v", err)
	}
	defer cdpShutdown()

	// Deux champs texte injectés dans la page courante ; le 2e contient « ancien ».
	if _, err := s.eval(`document.body.innerHTML='<input id=a placeholder=prenom><input id=b value=ancien>';'ok'`); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if _, err := s.snapshot(); err != nil { // pose les data-ajean-ref
		t.Fatalf("snapshot: %v", err)
	}
	// Retrouve le numéro du 2e champ (celui à remplacer).
	val, err := s.eval(`document.querySelector('#b').getAttribute('data-ajean-ref')`)
	if err != nil {
		t.Fatalf("eval ref: %v", err)
	}
	ref, _ := strconv.Atoi(strings.Trim(string(val), `"`))
	if ref == 0 {
		t.Fatalf("pas de ref sur #b (snapshot: %s)", val)
	}
	if err := s.typeInto(ref, "nouveau"); err != nil {
		t.Fatalf("typeInto: %v", err)
	}
	got, _ := s.eval(`document.querySelector('#b').value`)
	if v := strings.Trim(string(got), `"`); v != "nouveau" {
		t.Errorf("champ #b = %q, attendu \"nouveau\" (remplacement raté)", v)
	}
	// Le premier champ ne doit PAS avoir été touché.
	got2, _ := s.eval(`document.querySelector('#a').value`)
	if v := strings.Trim(string(got2), `"`); v != "" {
		t.Errorf("champ #a = %q, attendu vide (mauvais ciblage)", v)
	}
}

// TestCDPSnapshotHygiene vérifie que le snapshot (1) masque la valeur d'un champ
// mot de passe et (2) ne liste pas les conteneurs non-actionnables (un <ul>/<li>).
func TestCDPSnapshotHygiene(t *testing.T) {
	if os.Getenv("AJEAN_CU_SMOKE") != "1" {
		t.Skip("smoke test désactivé (AJEAN_CU_SMOKE=1 pour l'activer)")
	}
	if chromePath() == "" {
		t.Skip("aucun navigateur détecté")
	}
	s, err := cdpGet()
	if err != nil {
		t.Fatalf("cdpGet: %v", err)
	}
	defer cdpShutdown()

	inject := `document.body.innerHTML='` +
		`<label for=pw>Mot de passe</label><input id=pw type=password value=SECRET123>` +
		`<label for=em>Email</label><input id=em type=text value="a@b.co">` +
		`<ul><li>bloc conteneur</li></ul>` +
		`<a href="#x">un lien</a>';'ok'`
	if _, err := s.eval(inject); err != nil {
		t.Fatalf("inject: %v", err)
	}
	snap, err := s.snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	t.Logf("snapshot:\n%s", snap)
	if strings.Contains(snap, "SECRET123") {
		t.Error("la valeur du champ mot de passe ne doit PAS apparaître dans le snapshot")
	}
	if strings.Contains(snap, "bloc conteneur") {
		t.Error("un <li> conteneur ne doit pas figurer dans la liste des éléments actionnables")
	}
	if !strings.Contains(snap, "un lien") {
		t.Error("le lien actionnable devrait figurer dans le snapshot")
	}
	// Le champ mot de passe lui-même reste listé (par son label), juste sans valeur.
	if !strings.Contains(strings.ToLower(snap), "mot de passe") {
		t.Error("le champ mot de passe devrait apparaître par son label")
	}
	// Un champ texte non-secret DOIT montrer sa valeur (observabilité du formulaire).
	if !strings.Contains(snap, "a@b.co") {
		t.Error("la valeur d'un champ texte non-secret devrait apparaître dans le snapshot")
	}
	// Un <input type=text> doit être annoncé « textbox », pas « text » (qui ressemble
	// à du texte statique et faisait croire au modèle que c'était une étiquette).
	if !strings.Contains(snap, "textbox") {
		t.Error("un champ de saisie devrait avoir le rôle 'textbox'")
	}
}

// TestCDPRefUniqueness reproduit le bug du test de navigation #9 : un snapshot doit
// EFFACER les anciens data-ajean-ref, sinon deux éléments partagent un numéro et
// browser_type/browser_click visent le mauvais champ (querySelector prend le 1er du DOM).
func TestCDPRefUniqueness(t *testing.T) {
	if os.Getenv("AJEAN_CU_SMOKE") != "1" {
		t.Skip("smoke test désactivé (AJEAN_CU_SMOKE=1 pour l'activer)")
	}
	if chromePath() == "" {
		t.Skip("aucun navigateur détecté")
	}
	s, err := cdpGet()
	if err != nil {
		t.Fatalf("cdpGet: %v", err)
	}
	defer cdpShutdown()

	if _, err := s.eval(`document.body.innerHTML='<a href=#>A</a><a href=#>B</a><a href=#>C</a>';'ok'`); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if _, err := s.snapshot(); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	// Simule un attribut périmé laissé par un snapshot antérieur sur un AUTRE élément.
	if _, err := s.eval(`document.querySelectorAll('a')[2].setAttribute('data-ajean-ref','1');'ok'`); err != nil {
		t.Fatalf("stale: %v", err)
	}
	// Un nouveau snapshot doit repartir propre : chaque numéro sur UN SEUL élément.
	if _, err := s.snapshot(); err != nil {
		t.Fatalf("snapshot2: %v", err)
	}
	dup, _ := s.eval(`document.querySelectorAll('[data-ajean-ref="1"]').length`)
	if strings.TrimSpace(string(dup)) != "1" {
		t.Errorf("data-ajean-ref=\"1\" devrait être unique, trouvé %s occurrence(s)", dup)
	}
}

// TestCDPSettleWaitsAsync vérifie que settle() attend le contenu injecté en
// asynchrone (cas du test #10 où le modèle devait « bash sleep » avant snapshot).
func TestCDPSettleWaitsAsync(t *testing.T) {
	if os.Getenv("AJEAN_CU_SMOKE") != "1" {
		t.Skip("smoke test désactivé (AJEAN_CU_SMOKE=1 pour l'activer)")
	}
	if chromePath() == "" {
		t.Skip("aucun navigateur détecté")
	}
	s, err := cdpGet()
	if err != nil {
		t.Fatalf("cdpGet: %v", err)
	}
	defer cdpShutdown()

	// Page réelle (réseau réel), puis on déclenche un fetch AJAX qui n'ajoute son
	// lien qu'une fois la requête terminée. settle() doit attendre l'inactivité
	// réseau et donc voir le lien.
	if err := s.navigate("https://example.com"); err != nil {
		t.Fatalf("navigate: %v", err)
	}
	if _, err := s.eval(`fetch(location.href,{cache:'no-store'}).then(function(){var a=document.createElement('a');a.href='#';a.textContent='AjaxDone';document.body.appendChild(a);});'ok'`); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	time.Sleep(60 * time.Millisecond) // laisse requestWillBeSent s'enregistrer
	s.settle()                        // doit attendre la fin du fetch
	snap, err := s.snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !strings.Contains(snap, "AjaxDone") {
		t.Errorf("settle() aurait dû attendre la fin du fetch AJAX; snapshot:\n%s", snap)
	}
}

// TestCDPTypeSelectsOption vérifie le fix du test #14 : browser_type sur un <select>
// choisit l'option par son texte au lieu de taper des caractères.
func TestCDPTypeSelectsOption(t *testing.T) {
	if os.Getenv("AJEAN_CU_SMOKE") != "1" {
		t.Skip("smoke test désactivé (AJEAN_CU_SMOKE=1 pour l'activer)")
	}
	if chromePath() == "" {
		t.Skip("aucun navigateur détecté")
	}
	s, err := cdpGet()
	if err != nil {
		t.Fatalf("cdpGet: %v", err)
	}
	defer cdpShutdown()

	if _, err := s.eval(`document.body.innerHTML='<select id=civ><option value="">--</option><option>Madame</option><option>Monsieur</option><option>Autre</option></select>';'ok'`); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if _, err := s.snapshot(); err != nil { // pose les data-ajean-ref
		t.Fatalf("snapshot: %v", err)
	}
	ref, _ := s.eval(`+document.querySelector('#civ').getAttribute('data-ajean-ref')`)
	n, _ := strconv.Atoi(strings.TrimSpace(string(ref)))
	if n == 0 {
		t.Fatalf("pas de ref sur le select (%s)", ref)
	}
	if err := s.typeInto(n, "Monsieur"); err != nil {
		t.Fatalf("typeInto select: %v", err)
	}
	got, _ := s.eval(`document.querySelector('#civ').value`)
	if v := strings.Trim(string(got), `"`); v != "Monsieur" {
		t.Errorf("le select devrait valoir « Monsieur », got %q", v)
	}
}

// TestCDPSnapshotDedup vérifie qu'un état identique renvoie une ligne courte au
// lieu de re-lister tous les éléments (économie de contexte, test #12).
func TestCDPSnapshotDedup(t *testing.T) {
	if os.Getenv("AJEAN_CU_SMOKE") != "1" {
		t.Skip("smoke test désactivé (AJEAN_CU_SMOKE=1 pour l'activer)")
	}
	if chromePath() == "" {
		t.Skip("aucun navigateur détecté")
	}
	s, err := cdpGet()
	if err != nil {
		t.Fatalf("cdpGet: %v", err)
	}
	defer cdpShutdown()

	if _, err := s.eval(`document.body.innerHTML='<a href=#>Un lien</a>';'ok'`); err != nil {
		t.Fatalf("inject: %v", err)
	}
	first, err := s.snapshotDedup()
	if err != nil {
		t.Fatalf("snapshotDedup1: %v", err)
	}
	if !strings.Contains(first, "Un lien") {
		t.Fatalf("1er snapshot devrait lister le lien, got: %s", first)
	}
	second, err := s.snapshotDedup()
	if err != nil {
		t.Fatalf("snapshotDedup2: %v", err)
	}
	if strings.Contains(second, "Un lien") || !strings.Contains(second, "inchang") {
		t.Errorf("2e snapshot identique devrait être compact (inchangé), got: %s", second)
	}
	// Après un vrai changement, on redonne la liste complète.
	if _, err := s.eval(`document.body.innerHTML+='<a href=#>Autre</a>';'ok'`); err != nil {
		t.Fatalf("change: %v", err)
	}
	third, _ := s.snapshotDedup()
	if !strings.Contains(third, "Autre") {
		t.Errorf("après changement, le snapshot complet devrait revenir, got: %s", third)
	}
}

// TestCDPFindKeepsRefs vérifie le fix du test #11 : browser_find ne doit PAS effacer
// les data-ajean-ref du dernier snapshot (sinon un browser_click ensuite échoue).
func TestCDPFindKeepsRefs(t *testing.T) {
	if os.Getenv("AJEAN_CU_SMOKE") != "1" {
		t.Skip("smoke test désactivé (AJEAN_CU_SMOKE=1 pour l'activer)")
	}
	if chromePath() == "" {
		t.Skip("aucun navigateur détecté")
	}
	s, err := cdpGet()
	if err != nil {
		t.Fatalf("cdpGet: %v", err)
	}
	defer cdpShutdown()

	if _, err := s.eval(`document.body.innerHTML='<a href=#>Alpha</a><a href=#>Beta</a><a href=#>Connexion</a>';'ok'`); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if _, err := s.snapshot(); err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	before, _ := s.eval(`document.querySelectorAll('a')[2].getAttribute('data-ajean-ref')`)
	// Recherche d'un AUTRE élément : ne doit pas toucher au ref de « Connexion ».
	if _, err := s.find("Alpha"); err != nil {
		t.Fatalf("find: %v", err)
	}
	after, _ := s.eval(`document.querySelectorAll('a')[2].getAttribute('data-ajean-ref')`)
	if string(before) != string(after) || strings.Trim(string(after), `"`) == "" {
		t.Errorf("browser_find a modifié/effacé le ref de « Connexion » : avant=%s après=%s", before, after)
	}
}

// TestCDPClickXY vérifie le clic par coordonnées (pour iframe cross-origin/canvas).
func TestCDPClickXY(t *testing.T) {
	if os.Getenv("AJEAN_CU_SMOKE") != "1" {
		t.Skip("smoke test désactivé (AJEAN_CU_SMOKE=1 pour l'activer)")
	}
	if chromePath() == "" {
		t.Skip("aucun navigateur détecté")
	}
	s, err := cdpGet()
	if err != nil {
		t.Fatalf("cdpGet: %v", err)
	}
	defer cdpShutdown()

	// Un bouton à une position connue qui note son propre clic.
	inject := `document.body.innerHTML='<button id=b style="position:fixed;left:40px;top:30px;width:120px;height:40px">X</button>';` +
		`window.__hit=false;document.getElementById('b').addEventListener('click',function(){window.__hit=true;});'ok'`
	if _, err := s.eval(inject); err != nil {
		t.Fatalf("inject: %v", err)
	}
	if err := s.clickXY(100, 50); err != nil { // dans le rectangle du bouton
		t.Fatalf("clickXY: %v", err)
	}
	hit, _ := s.eval(`window.__hit`)
	if strings.TrimSpace(string(hit)) != "true" {
		t.Errorf("le clic par coordonnées aurait dû atteindre le bouton, got %s", hit)
	}
}

// TestCDPShadowDOM vérifie le fix du test Lady Sushi : les éléments d'un shadow
// DOM (bandeaux cookies, web components) sont vus par le snapshot ET cliquables.
func TestCDPShadowDOM(t *testing.T) {
	if os.Getenv("AJEAN_CU_SMOKE") != "1" {
		t.Skip("smoke test désactivé (AJEAN_CU_SMOKE=1 pour l'activer)")
	}
	if chromePath() == "" {
		t.Skip("aucun navigateur détecté")
	}
	s, err := cdpGet()
	if err != nil {
		t.Fatalf("cdpGet: %v", err)
	}
	defer cdpShutdown()

	// Un host avec un bouton « TOUT REFUSER » DANS un shadow root ouvert.
	inject := `document.body.innerHTML='<div id=host></div>';` +
		`var sr=document.getElementById('host').attachShadow({mode:'open'});` +
		`sr.innerHTML='<button id=b>TOUT REFUSER</button>';'ok'`
	if _, err := s.eval(inject); err != nil {
		t.Fatalf("inject: %v", err)
	}
	snap, err := s.snapshot()
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	if !strings.Contains(snap, "TOUT REFUSER") {
		t.Fatalf("le bouton du shadow DOM devrait apparaître dans le snapshot; got:\n%s", snap)
	}
	// browser_find doit aussi le trouver, et browser_click le viser (via ajByRef).
	found, err := s.find("refuser")
	if err != nil || !strings.Contains(strings.ToLower(found), "refuser") {
		t.Errorf("find n'a pas trouvé le bouton shadow: %v / %s", err, found)
	}
}

// TestCDPNavigateError vérifie qu'une URL injoignable renvoie une ERREUR claire
// (et pas un faux snapshot avec un bouton « Actualiser » que le modèle re-tente).
func TestCDPNavigateError(t *testing.T) {
	if os.Getenv("AJEAN_CU_SMOKE") != "1" {
		t.Skip("smoke test désactivé (AJEAN_CU_SMOKE=1 pour l'activer)")
	}
	if chromePath() == "" {
		t.Skip("aucun navigateur détecté")
	}
	s, err := cdpGet()
	if err != nil {
		t.Fatalf("cdpGet: %v", err)
	}
	defer cdpShutdown()
	err = s.navigate("https://nonexistent-host-ajean-test-99999.invalid")
	if err == nil {
		t.Error("une URL injoignable devrait renvoyer une erreur, pas nil")
	} else {
		t.Logf("erreur attendue: %v", err)
	}
}
