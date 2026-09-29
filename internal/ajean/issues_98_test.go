package ajean

// issues_98_test.go — recherche plein-texte dans les conversations archivées
// (issue #98). Écrits AVANT l'implémentation (TDD) : le paquet ne compilait pas,
// puis chaque test a été prouvé mordant par une mutation d'une ligne (voir le détail
// dans la description de PR).

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// --- helpers ------------------------------------------------------------------

// histSetup isole $AJEAN_HOME et remet la conversation globale (process) dans un
// état neutre : chaque test pose explicitement la session « vive » qu'il veut.
// L'état précédent est restauré en fin de test (les tests partagent `conv`).
func histSetup(t *testing.T) {
	t.Helper()
	testHome(t)
	conv.mu.Lock()
	prevID, prevProj, prevTitle, prevFav := conv.ID, conv.Project, conv.ActiveTitle, conv.ActiveFav
	prevLog := append([]LogEvent(nil), conv.Log...)
	prevMsgs := append([]Message(nil), conv.Messages...)
	prevSeq, prevCtx, prevCC := conv.Seq, conv.CtxUsed, conv.CompactCount
	conv.ID, conv.Project, conv.ActiveTitle, conv.ActiveFav = "", "", "", false
	conv.Log, conv.Messages = nil, nil
	conv.Seq, conv.CtxUsed, conv.CompactCount = 0, 0, 0
	conv.Generating = false
	conv.mu.Unlock()
	t.Cleanup(func() {
		conv.mu.Lock()
		conv.ID, conv.Project, conv.ActiveTitle, conv.ActiveFav = prevID, prevProj, prevTitle, prevFav
		conv.Log, conv.Messages = prevLog, prevMsgs
		conv.Seq, conv.CtxUsed, conv.CompactCount = prevSeq, prevCtx, prevCC
		conv.Generating = false
		conv.mu.Unlock()
		clearMemDEK() // au cas où un test a laissé une DEK en RAM
	})
}

// histSetLive pose la session vive du process.
func histSetLive(t *testing.T, id, project, title string, log []LogEvent) {
	t.Helper()
	conv.mu.Lock()
	conv.ID = id
	conv.Project = project
	conv.ActiveTitle = title
	conv.Log = append([]LogEvent(nil), log...)
	conv.Generating = false
	conv.mu.Unlock()
}

func histUser(ts int64, s string) LogEvent {
	return LogEvent{TS: ts, Delta: map[string]any{"user": s}}
}

func histContent(ts int64, s string) LogEvent {
	return LogEvent{TS: ts, Delta: map[string]any{"content": s}}
}

func histHasHit(res histSearchResult, id string) bool {
	for _, h := range res.Hits {
		if h.ID == id {
			return true
		}
	}
	return false
}

// histArchive écrit une archive PAR LE CHEMIN DE PRODUCTION (saveArchive), donc avec
// son entrée d'index d'historique ET son entrée d'index plein-texte.
func histArchive(t *testing.T, id, project, title string, savedAt int64, fav bool, log []LogEvent) *convArchive {
	t.Helper()
	a := &convArchive{ID: id, Project: project, Title: title, SavedAt: savedAt, Fav: fav, Log: log}
	a.Turns = countUserTurns(log)
	if err := saveArchive(a); err != nil {
		t.Fatalf("saveArchive(%s) : %v", id, err)
	}
	return a
}

func histDocExists(t *testing.T, id string) bool {
	t.Helper()
	_, ok := getStoreBytes(bkChatSearch, histDocKey(id))
	return ok
}

func histPosting(t *testing.T, tok string) []string {
	t.Helper()
	b, ok := getStoreBytes(bkChatSearch, histPostKey(tok))
	if !ok {
		return nil
	}
	var ids []string
	if err := json.Unmarshal(b, &ids); err != nil {
		t.Fatalf("posting %q illisible : %v", tok, err)
	}
	return ids
}

// --- 1. corps trouvé même si le titre ne contient rien ------------------------

// #98 : un terme présent dans le CORPS de la conversation doit la faire ressortir
// même quand son titre ne le porte pas.
func TestHistSearchBodyMatch(t *testing.T) {
	histSetup(t)
	histArchive(t, "A", "generale", "Voyage en Bretagne", 100, false,
		[]LogEvent{histUser(1, "parlons du phare de Brest")})
	histArchive(t, "B", "generale", "Recette", 200, false,
		[]LogEvent{histUser(1, "préparons une tarte")})

	res := histSearch("phare", "", 0, 0)
	if res.State != "ok" {
		t.Fatalf("état attendu ok, obtenu %q", res.State)
	}
	if res.Total != 1 {
		t.Fatalf("total attendu 1, obtenu %d (%+v)", res.Total, res.Hits)
	}
	if len(res.Hits) != 1 || res.Hits[0].ID != "A" {
		t.Fatalf("A attendu en résultat, obtenu %+v", res.Hits)
	}
	if res.Hits[0].Snippet == "" {
		t.Fatal("extrait (aperçu) attendu non vide pour A")
	}
	for _, h := range res.Hits {
		if h.ID == "B" {
			t.Fatal("B ne devait pas ressortir (ni titre ni corps)")
		}
	}
}

// --- 2. un mot du titre pèse plus qu'un mot du corps --------------------------

// #98 : le titre compte double (même ratio +2/+1 que recall_search). B est plus
// RÉCENT que A : un tri par date seule inverserait l'ordre.
func TestHistSearchTitleBeatsBody(t *testing.T) {
	histSetup(t)
	histArchive(t, "A", "generale", "Le phare de Brest", 100, false,
		[]LogEvent{histUser(1, "rien de spécial")})
	histArchive(t, "B", "generale", "Discussion diverse", 200, false,
		[]LogEvent{histUser(1, "on a vu un phare hier")})

	res := histSearch("phare", "", 0, 0)
	if res.Total != 2 || len(res.Hits) != 2 {
		t.Fatalf("2 résultats attendus, obtenu total=%d hits=%+v", res.Total, res.Hits)
	}
	if res.Hits[0].ID != "A" || res.Hits[1].ID != "B" {
		t.Fatalf("ordre attendu [A B], obtenu %s %s", res.Hits[0].ID, res.Hits[1].ID)
	}
}

// --- 3. pertinence puis date puis id ------------------------------------------

// #98 : tri score DESC, puis plus récent d'abord, puis id pour départager.
// B et C de même score, C plus récent ; A a un score plus faible.
func TestHistSearchRelevanceThenDate(t *testing.T) {
	histSetup(t)
	histArchive(t, "A", "generale", "Sans rapport", 200, false,
		[]LogEvent{histUser(1, "un phare dans le corps")})
	histArchive(t, "B", "generale", "Phare ancien", 100, false,
		[]LogEvent{histUser(1, "corps neutre")})
	histArchive(t, "C", "generale", "Phare récent", 300, false,
		[]LogEvent{histUser(1, "corps neutre")})

	res := histSearch("phare", "", 0, 0)
	got := []string{}
	for _, h := range res.Hits {
		got = append(got, h.ID)
	}
	want := []string{"C", "B", "A"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("ordre attendu %v, obtenu %v", want, got)
	}
}

// --- 4. la session VIVE est cherchée aussi ------------------------------------

// #98 : la session active n'est pas dans chathist (son corps vit dans bkChat). Un
// utilisateur qui cherche le fil EN COURS doit le trouver. Ici la seule archive
// porte un AUTRE terme, et la session vive n'a AUCUNE fiche chatmeta.
func TestHistSearchIncludesActiveSession(t *testing.T) {
	histSetup(t)
	histArchive(t, "A", "generale", "Autre sujet", 100, false,
		[]LogEvent{histUser(1, "on parle boussole")})
	histSetLive(t, "live1", "generale", "", []LogEvent{histUser(1, "le phare en direct")})

	res := histSearch("phare", "", 0, 0)
	if res.Total != 1 || len(res.Hits) != 1 {
		t.Fatalf("la session vive devait ressortir seule : total=%d hits=%+v", res.Total, res.Hits)
	}
	if res.Hits[0].ID != "live1" {
		t.Fatalf("id attendu live1, obtenu %q", res.Hits[0].ID)
	}
	if res.Hits[0].Title != "le phare en direct" {
		t.Fatalf("titre dérivé du fil vif attendu, obtenu %q", res.Hits[0].Title)
	}
}

// --- 5. chercher n'écrit RIEN --------------------------------------------------

// #98 : la recherche ne doit pas persister la session vive (ni l'archiver, ni la
// réécrire). On crée d'abord un vrai blob (persist), puis on compare AVANT/APRÈS.
func TestHistSearchDoesNotPersistLive(t *testing.T) {
	histSetup(t)
	histSetLive(t, "live1", "generale", "", []LogEvent{histUser(1, "le phare en direct")})
	conv.mu.Lock()
	conv.Messages = []Message{{Role: "user", Content: "le phare en direct"}}
	conv.mu.Unlock()
	conv.persist() // crée un blob bkChat réel à comparer

	beforeChat := getBytes(bkChat, "conversation")
	beforeMeta := getBytes(bkChatMeta, "live1")

	_ = histSearch("phare", "", 0, 0)

	if v := getBytes(bkChatHist, "live1"); v != nil {
		t.Fatal("la recherche a archivé la session vive dans chathist")
	}
	if v := getBytes(bkChat, "conversation"); string(v) != string(beforeChat) {
		t.Fatal("la recherche a réécrit le blob de la conversation vive")
	}
	if v := getBytes(bkChatMeta, "live1"); string(v) != string(beforeMeta) {
		t.Fatal("la recherche a modifié la fiche chatmeta de la session vive")
	}
	if histDocExists(t, "live1") {
		t.Fatal("la recherche a indexé la session vive (elle ne doit rien écrire)")
	}
}

// --- 6. cloisonnement par projet ----------------------------------------------

// #98 : la recherche ne sort pas du projet demandé. Même terme des deux côtés,
// seul le projet change.
func TestHistSearchProjectScope(t *testing.T) {
	histSetup(t)
	histArchive(t, "A", "alpha", "Projet alpha", 100, false, []LogEvent{histUser(1, "le phare")})
	histArchive(t, "B", "beta", "Projet beta", 100, false, []LogEvent{histUser(1, "le phare")})

	ra := histSearch("phare", "alpha", 0, 0)
	if len(ra.Hits) != 1 || ra.Hits[0].ID != "A" {
		t.Fatalf("projet alpha : A attendu seul, obtenu %+v", ra.Hits)
	}
	rb := histSearch("phare", "beta", 0, 0)
	if len(rb.Hits) != 1 || rb.Hits[0].ID != "B" {
		t.Fatalf("projet beta : B attendu seul, obtenu %+v", rb.Hits)
	}
}

// --- 7. mémoire verrouillée = état explicite, pas « rien trouvé » --------------

// #98 : verrouillé, une recherche ne peut pas distinguer « pas de résultat » de
// « illisible ». Elle le DIT (state=locked) au lieu de mentir par un vide.
func TestHistSearchLockedState(t *testing.T) {
	histSetup(t)
	histArchive(t, "A", "generale", "Secret", 100, false, []LogEvent{histUser(1, "le phare secret")})
	if err := MemAdd("note.md", "# note\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := EnableMemEncryption("pw"); err != nil {
		t.Fatal(err)
	}
	clearMemDEK() // verrouillé

	res := histSearch("phare", "", 0, 0)
	if res.State != "locked" {
		t.Fatalf("état attendu locked, obtenu %q", res.State)
	}
	if res.State == "ok" {
		t.Fatal("un résultat vide ne doit PAS être présenté comme un succès")
	}
	if len(res.Hits) != 0 || res.Total != 0 {
		t.Fatalf("aucun résultat attendu verrouillé, obtenu %+v", res.Hits)
	}
}

// --- 8. requête sans terme exploitable ----------------------------------------

// #98 : « a! » ne produit aucun terme. Le dire (state=no_terms) plutôt que renvoyer
// un « ok » vide qui laisserait croire à l'absence de correspondance.
func TestHistSearchNoUsableTerms(t *testing.T) {
	histSetup(t)
	histArchive(t, "A", "generale", "Phare", 100, false, []LogEvent{histUser(1, "le phare")})

	res := histSearch("a!", "", 0, 0)
	if res.State != "no_terms" {
		t.Fatalf("état attendu no_terms, obtenu %q", res.State)
	}
	if res.Terms == nil {
		t.Fatal("Terms doit être non-nil (tableau vide, jamais null)")
	}
	if len(res.Terms) != 0 || res.Total != 0 {
		t.Fatalf("aucun terme/résultat attendu, obtenu terms=%v total=%d", res.Terms, res.Total)
	}
}

// --- 9. cycle de vie de l'index (saveArchive / deleteArchive) ------------------

// #98 : saveArchive pose l'entrée d'index et la posting-list ; deleteArchive la
// retire. Deux sessions partagent le terme → la décrémentation est observable.
func TestHistSearchIndexLifecycle(t *testing.T) {
	histSetup(t)
	histArchive(t, "A", "generale", "A", 100, false, []LogEvent{histUser(1, "le phare")})
	histArchive(t, "B", "generale", "B", 200, false, []LogEvent{histUser(1, "le phare")})

	if !histDocExists(t, "A") || !histDocExists(t, "B") {
		t.Fatal("les deux entrées d| devaient exister après saveArchive")
	}
	if ids := histPosting(t, "phare"); len(ids) != 2 {
		t.Fatalf("posting phare attendu de 2, obtenu %v", ids)
	}

	if err := deleteArchive("A"); err != nil {
		t.Fatal(err)
	}
	if histDocExists(t, "A") {
		t.Fatal("d|A devait disparaître après suppression")
	}
	if ids := histPosting(t, "phare"); len(ids) != 1 || ids[0] != "B" {
		t.Fatalf("posting phare attendu [B], obtenu %v", ids)
	}
}

// --- 10. le bucket d'index est chiffré ----------------------------------------

// #98 : un sac de mots en clair à côté de conversations chiffrées est une fuite.
// On indexe AVANT d'activer le chiffrement, puis on vérifie que le magic est posé.
func TestHistSearchBucketEncrypted(t *testing.T) {
	histSetup(t)
	histArchive(t, "A", "generale", "Titre secret", 100, false, []LogEvent{histUser(1, "le phare")})
	if raw := getBytes(bkChatSearch, histDocKey("A")); looksEncrypted(raw) {
		t.Fatal("avant activation, la valeur ne devait pas être chiffrée")
	}
	if err := MemAdd("note.md", "# note\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := EnableMemEncryption("pw"); err != nil {
		t.Fatal(err)
	}
	raw := getBytes(bkChatSearch, histDocKey("A"))
	if !looksEncrypted(raw) {
		t.Fatal("l'index plein-texte devait être chiffré (bucket manquant d'encryptedBuckets ?)")
	}
	// Verrouillé : plus relisible → la recherche rendra locked.
	clearMemDEK()
	if _, ok := getStoreBytes(bkChatSearch, histDocKey("A")); ok {
		t.Fatal("index relisible alors que la mémoire est verrouillée")
	}
}

// --- 11. couverture : jamais présenter un index partiel comme complet ----------

// #98 : 3 archives, une seule indexée au départ. indexed < indexed_total ; après
// le rattrapage, indexed == indexed_total et les 3 sont trouvables.
func TestHistSearchCoverageContract(t *testing.T) {
	histSetup(t)
	log := []LogEvent{histUser(1, "le phare")}
	// Écrites à la main SANS passer par l'index, pour simuler des sessions
	// antérieures à la fonctionnalité.
	for _, id := range []string{"a", "b", "c"} {
		putArchiveRaw(t, &convArchive{ID: id, Project: "generale", Title: id, SavedAt: 100, Log: log})
	}
	indexArchive(&convArchive{ID: "a", Project: "generale", Title: "a", SavedAt: 100, Log: log})

	res := histSearch("phare", "", 0, 0)
	if res.IndexedTotal != 3 {
		t.Fatalf("indexed_total attendu 3, obtenu %d", res.IndexedTotal)
	}
	if res.Indexed >= res.IndexedTotal {
		t.Fatalf("index partiel attendu (%d/%d)", res.Indexed, res.IndexedTotal)
	}
	if res.Total != 1 {
		t.Fatalf("1 seule trouvable avant rattrapage, obtenu %d", res.Total)
	}

	histSearchIndexBackfill()

	res = histSearch("phare", "", 0, 0)
	if res.Indexed != res.IndexedTotal || res.IndexedTotal != 3 {
		t.Fatalf("après rattrapage : indexed=%d indexed_total=%d (3 attendus)", res.Indexed, res.IndexedTotal)
	}
	if res.Total != 3 {
		t.Fatalf("les 3 sessions devaient être trouvables, obtenu %d", res.Total)
	}
}

// --- 12. le classement vient de l'index, pas des corps -------------------------

// #98 : une session dont le CORPS n'est plus lisible (ou absent) doit rester
// trouvable tant que sa fiche et son entrée d'index existent. Zéro chargement de
// corps par requête.
func TestHistSearchRanksFromIndexNotBodies(t *testing.T) {
	histSetup(t)
	a := &convArchive{ID: "A", Project: "generale", Title: "Titre", SavedAt: 100,
		Log: []LogEvent{histUser(1, "le phare")}}
	// Fiche présente, entrée d'index présente, mais AUCUN corps dans chathist.
	putStoreJSON(bkChatMeta, "A", convArchiveMeta{ID: "A", Project: "generale", Title: "Titre", SavedAt: 100})
	indexArchive(a)

	res := histSearch("phare", "", 0, 0)
	if res.Total != 1 || len(res.Hits) != 1 || res.Hits[0].ID != "A" {
		t.Fatalf("le résultat devait venir de l'index, obtenu %+v", res.Hits)
	}
}

// --- 13. la session vive n'apparaît qu'une fois -------------------------------

// #98 : OpenSession NE retire PAS l'archive : la session est à la fois vive et
// dans chathist. La recherche doit la dédupliquer par id.
func TestHistSearchDeduplicatesActiveAgainstArchive(t *testing.T) {
	histSetup(t)
	histArchive(t, "A", "generale", "Titre", 100, false, []LogEvent{histUser(1, "le phare")})
	histSetLive(t, "", "", "", nil)
	if err := conv.OpenSession("A"); err != nil {
		t.Fatalf("OpenSession(A) : %v", err)
	}

	res := histSearch("phare", "", 0, 0)
	n := 0
	for _, h := range res.Hits {
		if h.ID == "A" {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("la session active devait apparaître une fois, apparue %d fois (%+v)", n, res.Hits)
	}
	if res.Total != 1 {
		t.Fatalf("total attendu 1, obtenu %d", res.Total)
	}
}

// --- 14. pagination : total AVANT découpage -----------------------------------

// #98 : total = nombre de correspondances en portée, avant pagination.
func TestHistSearchPagination(t *testing.T) {
	histSetup(t)
	histArchive(t, "A", "generale", "Phare A", 100, false, []LogEvent{histUser(1, "le phare")})
	histArchive(t, "B", "generale", "Phare B", 200, false, []LogEvent{histUser(1, "le phare")})
	histArchive(t, "C", "generale", "Phare C", 300, false, []LogEvent{histUser(1, "le phare")})

	page := histSearch("phare", "", 1, 1)
	if len(page.Hits) != 1 {
		t.Fatalf("1 résultat par page attendu, obtenu %d", len(page.Hits))
	}
	if page.Total != 3 {
		t.Fatalf("total attendu 3 (avant découpage), obtenu %d", page.Total)
	}
	if page.Hits[0].ID != "B" { // C(300) puis B(200) puis A(100) → offset 1 = B
		t.Fatalf("second résultat attendu B, obtenu %s", page.Hits[0].ID)
	}

	beyond := histSearch("phare", "", 10, 5)
	if len(beyond.Hits) != 0 || beyond.Total != 3 {
		t.Fatalf("au-delà de la fin : 0 hit et total 3 attendus, obtenu hits=%d total=%d", len(beyond.Hits), beyond.Total)
	}
}

// --- 15. requête vide : comportement historique inchangé -----------------------

// #98 : sans q, on garde EXACTEMENT l'ancien chemin (non filtré, favoris d'abord).
// Ici A est favori mais plus ANCIEN que B : l'ordre favoris-d'abord doit primer.
func TestHistSearchEmptyQueryUnchanged(t *testing.T) {
	histSetup(t)
	histArchive(t, "A", "generale", "Favori ancien", 100, true, []LogEvent{histUser(1, "rien")})
	histArchive(t, "B", "generale", "Récent", 200, false, []LogEvent{histUser(1, "rien")})

	rr := httptest.NewRecorder()
	handleChatHistory(rr, httptest.NewRequest("GET", "/api/chat/history", nil))
	var got struct {
		OK            bool             `json:"ok"`
		State         string           `json:"state"`
		Conversations []convArchiveHit `json:"conversations"`
		Total         int              `json:"total"`
		Indexed       int              `json:"indexed"`
		IndexedTotal  int              `json:"indexed_total"`
		Terms         []string         `json:"terms"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("réponse illisible : %v (%s)", err, rr.Body.String())
	}
	if !got.OK || got.State != "ok" {
		t.Fatalf("état attendu ok, obtenu ok=%v state=%q", got.OK, got.State)
	}
	if got.Total != 2 || len(got.Conversations) != 2 {
		t.Fatalf("les 2 sessions attendues, obtenu total=%d len=%d", got.Total, len(got.Conversations))
	}
	if got.Conversations[0].ID != "A" {
		t.Fatalf("favori d'abord attendu (A), obtenu %s", got.Conversations[0].ID)
	}
	if got.Indexed != 0 || got.IndexedTotal != 0 {
		t.Fatalf("couverture attendue à 0 sans q, obtenu %d/%d", got.Indexed, got.IndexedTotal)
	}
	if got.Terms == nil || len(got.Terms) != 0 {
		t.Fatalf("terms attendu non-nil vide, obtenu %#v", got.Terms)
	}
	if got.Conversations == nil {
		t.Fatal("conversations doit être un tableau (jamais null)")
	}
}

// --- repli des accents, dans les DEUX sens ------------------------------------

// #98 : « resume » doit trouver « résumé » et réciproquement. L'index est replié
// (contrairement à recallTokens), donc la requête l'est aussi.
func TestHistSearchAccentFolding(t *testing.T) {
	histSetup(t)
	histArchive(t, "A", "generale", "Notes diverses", 100, false,
		[]LogEvent{histUser(1, "voici le résumé du rapport")})
	histArchive(t, "B", "generale", "Notes diverses", 200, false,
		[]LogEvent{histUser(1, "voici le resume du dossier")})

	// Requête SANS accent trouve le corps accentué (A).
	ra := histSearch("resume", "", 0, 0)
	if len(ra.Hits) != 2 {
		t.Fatalf("resume devait trouver les 2, obtenu %+v", ra.Hits)
	}
	// Requête AVEC accent trouve le corps non accentué (B).
	rb := histSearch("résumé", "", 0, 0)
	if len(rb.Hits) != 2 {
		t.Fatalf("résumé devait trouver les 2, obtenu %+v", rb.Hits)
	}
}

// #98 : l'index couvre le texte VISIBLE (réponse assistant « content » et libellé
// d'outil « tool_used.label ») mais EXCLUT le raisonnement et le CORPS des sorties
// d'outil (qui peut être volumineux et sensible).
func TestHistSearchIndexesVisibleTextOnly(t *testing.T) {
	histSetup(t)
	histArchive(t, "A", "generale", "Titre neutre", 100, false,
		[]LogEvent{histContent(1, "voici un zèbre dans la réponse")})
	histArchive(t, "B", "generale", "Titre neutre", 200, false,
		[]LogEvent{{TS: 1, Delta: map[string]any{"tool_used": map[string]any{
			"name": "bash", "label": "rg zèbre", "result": "peu importe", "done": true}}}})
	histArchive(t, "C", "generale", "Titre neutre", 300, false,
		[]LogEvent{{TS: 1, Delta: map[string]any{"reasoning_content": "je pense au zèbre"}}})
	histArchive(t, "D", "generale", "Titre neutre", 400, false,
		[]LogEvent{{TS: 1, Delta: map[string]any{"tool_used": map[string]any{
			"name": "bash", "label": "ls", "result": "le zèbre est dans le corps", "done": true}}}})

	res := histSearch("zèbre", "", 0, 0)
	if !histHasHit(res, "A") {
		t.Fatalf("la réponse assistant (content) devait être indexée : %+v", res.Hits)
	}
	if !histHasHit(res, "B") {
		t.Fatalf("le libellé d'outil (tool_used.label) devait être indexé : %+v", res.Hits)
	}
	if histHasHit(res, "C") {
		t.Fatalf("le raisonnement NE devait PAS être indexé : %+v", res.Hits)
	}
	if histHasHit(res, "D") {
		t.Fatalf("le CORPS d'une sortie d'outil NE devait PAS être indexé : %+v", res.Hits)
	}
}

// putArchiveRaw écrit une archive dans l'historique SANS passer par l'index// plein-texte — pour simuler des sessions antérieures à #98.
func putArchiveRaw(t *testing.T, a *convArchive) {
	t.Helper()
	if err := putStoreJSON(bkChatHist, a.ID, a); err != nil {
		t.Fatal(err)
	}
	m := convArchiveMeta{ID: a.ID, Project: a.Project, Title: a.Title, Fav: a.Fav,
		SavedAt: a.SavedAt, Turns: countUserTurns(a.Log)}
	if err := putStoreJSON(bkChatMeta, a.ID, m); err != nil {
		t.Fatal(err)
	}
}
