package ajean

// chat_search.go — recherche PLEIN-TEXTE dans les conversations archivées (#98).
//
// Parcourir le corps des conversations à chaque requête est hors de question :
// chathist pèse 252 Mo pour 537 sessions sur la machine de Alice (constat déjà
// documenté sur la liste de l'historique, chat_history.go). On tient donc un INDEX
// INVERSÉ dans bkChatSearch, à deux familles de clés :
//
//	« d|<id> »     → histSearchDoc : la fiche d'index d'une session (version,
//	                 aperçu, liste des mots distincts). Sert aussi de marqueur de
//	                 reprise du rattrapage et de source d'extrait (sans relire un corps).
//	« p|<token> »  → []string d'ids de sessions triés (sémantique d'ENSEMBLE).
//
// Une requête ne lit donc que les postings des mots demandés, plus les fiches
// chatmeta (déjà légères) — jamais un corps. Le « | » ne peut pas apparaître dans
// un token : recallTokens ne rend que [a-z0-9à-ÿ].
//
// La session VIVE n'est pas dans chathist (son corps vit dans bkChat) : elle est
// cherchée séparément dans la conversation en mémoire, sous verrou, sans jamais
// écrire (voir histSearch).
//
// ACCENTS : l'index stocke des tokens REPLIÉS (« résumé » → « resume »). C'est
// délibérément différent de la sortie de recallTokens, que l'outil recall_search du
// modèle consomme telle quelle — ne pas « corriger » cette apparente incohérence :
// replier l'index (et la requête, symétriquement) est ce qui fait qu'une recherche
// « resume » trouve « résumé » et inversement, exigence d'un produit francophone.

import (
	"encoding/json"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	// histSearchIndexVersion : un doc « d| » dont la version diffère est traité comme
	// non indexé et reconstruit (l'ancien jeu de postings est retiré avant réécriture).
	histSearchIndexVersion = 1
	// histSearchMaxTerms borne le nombre de postings lus par requête : au-delà, les
	// termes surnuméraires sont ignorés (une requête d'une page de texte n'a pas à
	// balayer des centaines de listes).
	histSearchMaxTerms = 16
)

// histSearchDoc = la fiche d'index d'une session. L'aperçu évite de relire un corps
// pour l'extrait renvoyé à la liste.
type histSearchDoc struct {
	V      int      `json:"v"`
	Apercu string   `json:"apercu,omitempty"` // extrait de la tête, borné (recallSnippet)
	Toks   []string `json:"toks"`             // mots DISTINCTS, repliés, triés
}

// convArchiveHit = une fiche de session + l'extrait d'index, telle que renvoyée à
// l'UI (l'embedding aplatit les champs de convArchiveMeta au même niveau JSON).
type convArchiveHit struct {
	convArchiveMeta
	Snippet string `json:"snippet"`
}

// histSearchResult = le résultat complet d'une recherche (état + page + couverture).
type histSearchResult struct {
	State        string
	Hits         []convArchiveHit
	Total        int      // correspondances en portée, AVANT pagination
	Indexed      int      // sessions de la portée réellement indexées
	IndexedTotal int      // sessions de la portée (dénominateur de couverture)
	Terms        []string // termes effectivement cherchés (repliés, triés)
}

func histDocKey(id string) string   { return "d|" + id }
func histPostKey(tok string) string { return "p|" + tok }

// foldHistAccents replie les diacritiques d'un token d'index. Appliqué SYMÉTRIQUEMENT
// à l'indexation et à la requête (voir le commentaire de tête).
func foldHistAccents(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case 'à', 'â', 'ä', 'á', 'ã', 'å':
			b.WriteByte('a')
		case 'é', 'è', 'ê', 'ë':
			b.WriteByte('e')
		case 'î', 'ï', 'í', 'ì':
			b.WriteByte('i')
		case 'ô', 'ö', 'ó', 'ò', 'õ':
			b.WriteByte('o')
		case 'ù', 'û', 'ü', 'ú':
			b.WriteByte('u')
		case 'ç':
			b.WriteByte('c')
		case 'ÿ':
			b.WriteByte('y')
		case 'œ':
			b.WriteString("oe")
		case 'æ':
			b.WriteString("ae")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// histTokens dérive les mots d'index d'une chaîne : recallTokens (réutilisé), puis
// repli des accents et dédoublonnage, triés. NE PAS confondre avec recallTokens SEUL
// (voir le commentaire de tête). Attention : deux tokens distincts peuvent se replier
// sur le même (« cote » et « côté ») — d'où le dédoublonnage.
func histTokens(s string) []string {
	set := recallTokens(s)
	seen := make(map[string]bool, len(set))
	out := make([]string, 0, len(set))
	for t := range set {
		f := foldHistAccents(t)
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	sort.Strings(out)
	return out
}

// histTokenSet est la variante « ensemble » de histTokens, pour la comparaison aux
// termes de la requête (scoring du titre).
func histTokenSet(s string) map[string]bool {
	out := map[string]bool{}
	for _, t := range histTokens(s) {
		out[t] = true
	}
	return out
}

// histTermMatches est LA règle unique de correspondance (#98) : un token correspond à
// un terme quand il COMMENCE par ce terme (préfixe depuis le début), jamais au milieu
// du mot. Elle est servie aux TROIS consommateurs — corps (scan des postings), titre
// (chatmeta) et session vive — et ne doit pas être réécrite ailleurs : trois
// implémentations divergentes seraient trois comportements à dériver.
//
// Le repli des accents est fait en amont, symétriquement à l'indexation et à la
// requête (voir le commentaire de tête) : la comparaison ici est purement lexicale.
// Préfixe DEPUIS LE DÉBUT seulement : « eepseek » ne doit pas trouver « deepseek »
// (garde-fou testé — c'est ce qui empêcherait une future « amélioration » en
// sous-chaîne de passer inaperçue).
func histTermMatches(token, term string) bool {
	return strings.HasPrefix(token, term)
}

// histSetMatches : un terme correspond-il à l'un des tokens d'un ENSEMBLE ? Rend vrai
// au plus UNE fois par terme, quel que soit le nombre de tokens qui portent le préfixe
// (le score compte les TERMES, pas les tokens : une session riche en « compil* » ne
// doit pas doubler son score pour un seul mot cherché). Sert au titre et à la session
// vive, sur la même règle que le scan des postings du corps.
func histSetMatches(set map[string]bool, term string) bool {
	for tok := range set {
		if histTermMatches(tok, term) {
			return true
		}
	}
	return false
}

// histQueryTerms replie et trie les termes de la requête, puis les plafonne. Il
// réutilise le tokenizer de recall_search (recallTokens), jamais modifié.
func histQueryTerms(q string) []string {
	terms := histTokens(q)
	if len(terms) > histSearchMaxTerms {
		terms = terms[:histSearchMaxTerms]
	}
	return terms
}

// histVisibleText concatène le texte VISIBLE d'un journal d'affichage, seule source
// indexée. On part de convArchive.Log (vue UI) et NON de Messages : Message.Content
// est un `any` (peut être un tableau multimodal) et porte le CORPS des sorties d'outil,
// qu'on ne veut pas indexer. On retient :
//   - « user »    : le message de l'utilisateur ;
//   - « content » : la réponse de l'assistant ;
//   - « tool_used.label » : déjà « nom d'outil + première ligne » (recallLabel).
//
// Exclus volontairement : reasoning_content, tool_used.result/body, files, et tous
// les deltas de contrôle (error, stats, turn_done, compacting, ctx_used…).
func histVisibleText(log []LogEvent) string {
	var b strings.Builder
	for _, ev := range log {
		if s, ok := ev.Delta["user"].(string); ok {
			b.WriteString(s)
			b.WriteByte('\n')
		}
		if s, ok := ev.Delta["content"].(string); ok {
			b.WriteString(s)
			b.WriteByte('\n')
		}
		if tu, ok := ev.Delta["tool_used"].(map[string]any); ok {
			if l, ok := tu["label"].(string); ok {
				b.WriteString(l)
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

// histFirstVisible renvoie le premier texte visible d'un journal (aperçu de l'index).
func histFirstVisible(log []LogEvent) string {
	for _, ev := range log {
		if s, ok := ev.Delta["user"].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
		if s, ok := ev.Delta["content"].(string); ok && strings.TrimSpace(s) != "" {
			return s
		}
		if tu, ok := ev.Delta["tool_used"].(map[string]any); ok {
			if l, ok := tu["label"].(string); ok && strings.TrimSpace(l) != "" {
				return l
			}
		}
	}
	return ""
}

// --- accès bas niveau au bucket (chiffré comme les autres) --------------------

// histWriteKey capture la clé de chiffrement AVANT d'ouvrir la transaction bbolt.
// Indispensable : encodeMemContent (utilisé par putStoreBytes) lit la config via
// memEncActive → ReadConfig, qui ROUVRIRAIT la base et se bloquerait sur dbMu — le
// verrou que tient déjà la transaction en cours (interblocage observé au premier
// run des tests). nil = écrire en clair (chiffrement inactif).
func histWriteKey() ([]byte, error) {
	if !memEncActive() {
		return nil, nil
	}
	return currentDEK() // erreur si verrouillé : on refuse d'écrire du clair
}

// histTxGetJSON lit et déchiffre une valeur dans une transaction bbolt ouverte.
// decodeMemContent est sûr ici : sur du clair il ne lit pas la config, et sur du
// chiffré il n'appelle que currentDEK (mutex RAM, jamais la base).
func histTxGetJSON(b *bolt.Bucket, key string, dst any) bool {
	raw := b.Get([]byte(key))
	if raw == nil {
		return false
	}
	plain, err := decodeMemContent(raw)
	if err != nil {
		return false // chiffré + verrouillé : illisible, pas « absent »
	}
	return json.Unmarshal(plain, dst) == nil
}

// histTxPutJSON sérialise, CHIFFRE (avec la clé capturée hors transaction, voir
// histWriteKey) et écrit dans la transaction.
func histTxPutJSON(b *bolt.Bucket, key string, v any, dek []byte) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if dek == nil {
		return b.Put([]byte(key), raw)
	}
	enc, err := encPage(dek, raw)
	if err != nil {
		return err
	}
	return b.Put([]byte(key), enc)
}

// histPost ajoute `sid` à la posting-list de `tok` (ensemble, trié). Idempotent :
// un double index de la même session ne crée pas de doublon.
func histPost(b *bolt.Bucket, tok, sid string, dek []byte) error {
	var ids []string
	_ = histTxGetJSON(b, histPostKey(tok), &ids)
	for _, x := range ids {
		if x == sid {
			return nil
		}
	}
	ids = append(ids, sid)
	sort.Strings(ids)
	return histTxPutJSON(b, histPostKey(tok), ids, dek)
}

// histUnpost retire `sid` d'une posting-list ; la clé disparaît si elle se vide.
func histUnpost(b *bolt.Bucket, tok, sid string, dek []byte) error {
	var ids []string
	if !histTxGetJSON(b, histPostKey(tok), &ids) {
		return nil
	}
	out := make([]string, 0, len(ids))
	removed := false
	for _, x := range ids {
		if x == sid {
			removed = true
			continue
		}
		out = append(out, x)
	}
	if !removed {
		return nil
	}
	if len(out) == 0 {
		return b.Delete([]byte(histPostKey(tok)))
	}
	return histTxPutJSON(b, histPostKey(tok), out, dek)
}

// --- maintenance : indexation / désindexation ---------------------------------

// indexArchive (re)construit l'index plein-texte d'une session en UNE transaction :
// retrait des anciens postings, écriture de la fiche, ajout des nouveaux. Les
// sémantiques d'ensemble rendent l'opération idempotente — un saveArchive croisant
// un rattrapage ne peut pas produire de doublon. Erreur avalée par les appelants de
// saveArchive : l'archive reste sauvée, la couverture (indexed/indexed_total) le dit
// honnêtement et le rattrapage reprend l'entrée manquante.
func indexArchive(a *convArchive) error {
	if a == nil || a.ID == "" {
		return nil
	}
	dek, err := histWriteKey() // capturée AVANT la transaction (voir histWriteKey)
	if err != nil {
		return err
	}
	doc := histSearchDoc{
		V:      histSearchIndexVersion,
		Apercu: recallSnippet(histFirstVisible(a.Log), 200),
		Toks:   histTokens(histVisibleText(a.Log)),
	}
	return update(bkChatSearch, func(b *bolt.Bucket) error {
		var prev histSearchDoc
		if histTxGetJSON(b, histDocKey(a.ID), &prev) {
			for _, t := range prev.Toks {
				if err := histUnpost(b, t, a.ID, dek); err != nil {
					return err
				}
			}
		}
		if err := histTxPutJSON(b, histDocKey(a.ID), doc, dek); err != nil {
			return err
		}
		for _, t := range doc.Toks {
			if err := histPost(b, t, a.ID, dek); err != nil {
				return err
			}
		}
		return nil
	})
}

// unindexArchive retire une session de l'index. Si sa fiche est lisible, on retire
// ses postings ; sinon (verrouillé) histTxGetJSON échoue, on ne retire rien et on
// supprime seulement la clé « d|<id> », en laissant des ids ORPHELINS dans les
// postings — toléré : la requête écarte tout id sans fiche chatmeta, et le rattrapage
// les récolte. deleteArchive peut donc appeler ceci avant ses putBytes bruts, qui,
// eux, réussissent même verrouillé.
func unindexArchive(id string) {
	if id == "" {
		return
	}
	// Clé capturée AVANT la transaction (voir histWriteKey). Verrouillé, elle est
	// nil : peu importe, histTxGetJSON n'aura rien pu lire, donc aucune écriture
	// chiffrable n'aura lieu — seule la suppression de « d| » s'exécute.
	dek, _ := histWriteKey()
	_ = update(bkChatSearch, func(b *bolt.Bucket) error {
		var prev histSearchDoc
		if histTxGetJSON(b, histDocKey(id), &prev) {
			for _, t := range prev.Toks {
				if err := histUnpost(b, t, id, dek); err != nil {
					return err
				}
			}
		}
		return b.Delete([]byte(histDocKey(id)))
	})
}

// histIndexedCurrent indique que l'id a une fiche d'index à la version courante
// (lire : une entrée exploitable). Sert au rattrapage ET au calcul de couverture.
func histIndexedCurrent(id string) bool {
	b, ok := getStoreBytes(bkChatSearch, histDocKey(id))
	if !ok {
		return false
	}
	var d histSearchDoc
	if json.Unmarshal(b, &d) != nil {
		return false
	}
	return d.V == histSearchIndexVersion
}

// --- rattrapage des sessions antérieures à #98 --------------------------------

var histBackfillRunning atomic.Bool

// startHistSearchBackfill lance le rattrapage une fois, en tâche de fond, après le
// démarrage (même forme que startSlimArchives).
func startHistSearchBackfill() {
	if !histBackfillRunning.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer histBackfillRunning.Store(false)
		time.Sleep(20 * time.Second) // laisser moteur/lien démarrer d'abord
		histSearchIndexBackfill()
	}()
}

// histSearchIndexBackfill indexe les sessions archivées qui n'ont pas encore de
// fiche d'index. Le marqueur de reprise est la PRÉSENCE de « d|<id> » (à la version
// courante) : aucun bucket d'état supplémentaire. On itère allKeys (jamais allKV :
// le bucket porte le corps des conversations), on saute la session vive, on
// s'arrête net verrouillé (corps illisibles) — il reprendra au déverrouillage.
func histSearchIndexBackfill() {
	if memLocked() {
		return
	}
	activeID := conv.currentID()
	for _, id := range allKeys(bkChatHist) {
		if id == activeID {
			continue // cherchée en direct, pas besoin de fiche d'index
		}
		if histIndexedCurrent(id) {
			continue
		}
		a, ok := loadArchive(id)
		if !ok {
			if memLocked() {
				return // chiffré + verrouillé : on reprendra au déverrouillage
			}
			continue // illisible pour une autre raison (corrompue) : on saute
		}
		if err := indexArchive(a); err != nil {
			return
		}
		time.Sleep(30 * time.Millisecond) // ne pas monopoliser la base
	}
}

// --- recherche ----------------------------------------------------------------

// histScopeMetas renvoie les fiches chatmeta du projet `scope`. Une seule lecture
// de bucket (allKV → decode), pas une lecture par clé.
func histScopeMetas(scope string) []convArchiveMeta {
	kv := allKV(bkChatMeta)
	out := make([]convArchiveMeta, 0, len(kv))
	for id, v := range kv {
		plain, err := decodeMemContent([]byte(v))
		if err != nil {
			continue
		}
		var m convArchiveMeta
		if json.Unmarshal(plain, &m) != nil {
			continue
		}
		if m.ID == "" {
			m.ID = id
		}
		if archiveProject(m) == scope {
			out = append(out, m)
		}
	}
	return out
}

// histSearch est le cœur de la recherche. Pertinence d'abord (score = 2*titre + corps,
// même ratio que recall_search), puis plus récent, puis id (départage totalement
// déterministe). DIVERGENCE VOULUE du tri de listAllArchives (favoris d'abord) :
// une conversation favorite qui ne correspond PAS ne doit pas apparaître, et un
// favori qui correspond faiblement ne doit pas passer devant une correspondance
// forte. La requête VIDE, elle, garde le chemin historique (traitée par l'appelant).
func histSearch(q, project string, offset, limit int) histSearchResult {
	res := histSearchResult{State: "ok", Hits: make([]convArchiveHit, 0), Terms: make([]string, 0)}
	terms := histQueryTerms(q)
	if len(terms) == 0 {
		res.State = "no_terms"
		return res
	}
	// Verrouillé décidé AVANT toute lecture : au fond, « illisible » et « absent »
	// sont indiscernables (getStoreBytes rend false dans les deux cas). Pour une
	// recherche, un vide serait un mensonge — on le dit.
	if memLocked() {
		res.State = "locked"
		return res
	}
	res.Terms = terms
	scope := project
	if scope == "" {
		scope = activeProjectSlug()
	}

	metas := histScopeMetas(scope)
	metaByID := make(map[string]convArchiveMeta, len(metas)+1)
	for _, m := range metas {
		metaByID[m.ID] = m
	}

	// Session VIVE (piège #1) : on copie son journal SOUS conv.mu puis on RELÂCHE,
	// et on tokenise HORS du verrou — ne jamais bloquer une génération en cours.
	conv.mu.Lock()
	liveID := conv.ID
	liveProj := conv.Project
	if liveProj == "" {
		liveProj = activeProjectSlug()
	}
	liveLog := append([]LogEvent(nil), conv.Log...)
	liveTitle := conv.ActiveTitle
	if strings.TrimSpace(liveTitle) == "" {
		liveTitle = archiveTitle(liveLog)
	}
	liveFav := conv.ActiveFav
	conv.mu.Unlock()
	liveAt := int64(0)
	if n := len(liveLog); n > 0 {
		liveAt = liveLog[n-1].TS
	}
	liveInScope := liveID != "" && len(liveLog) > 0 && liveProj == scope

	// Titres : scoring à partir de chatmeta (léger, toujours là). Les tokens de
	// titre NE sont PAS indexés (chatmeta porte déjà tous les titres à bon compte).
	// MÊME règle de préfixe que le corps (histSetMatches → histTermMatches).
	bodyHits := map[string]int{}
	titleHits := map[string]int{}
	for _, m := range metas {
		set := histTokenSet(m.Title)
		n := 0
		for _, t := range terms {
			if histSetMatches(set, t) {
				n++
			}
		}
		if n > 0 {
			titleHits[m.ID] = n
		}
	}

	// UNE vue sur bkChatSearch : postings des termes demandés + (au même passage)
	// fiches d'index à jour (couverture) et aperçus (extraits). Zéro corps chargé.
	indexedIDs := map[string]bool{}
	snippets := map[string]string{}
	_ = view(bkChatSearch, func(b *bolt.Bucket) error {
		for _, t := range terms {
			// SCAN PRÉFIXE, pas une lecture exacte : les clés « p|<token> » sont
			// stockées TRIÉES, donc toutes celles qui portent le préfixe « p|<terme> »
			// se suivent — on Seek sur ce préfixe et on avance tant qu'il tient.
			// C'est la règle UNIQUE (histTermMatches) vue au niveau de la clé : « | »
			// ne peut pas figurer dans un token, donc « préfixe de clé » équivaut à
			// « le token commence par le terme ». Aucun changement d'index ni de
			// stockage (défaut n°1 : « compil » ne trouvait pas « compiling »).
			prefix := []byte(histPostKey(t))
			matched := map[string]bool{} // un terme ne compte qu'UNE fois par session
			c := b.Cursor()
			for k, v := c.Seek(prefix); k != nil; k, v = c.Next() {
				if !histTermMatches(string(k[len("p|"):]), t) {
					break // triées : plus aucune clé ultérieure ne peut porter le préfixe
				}
				plain, err := decodeMemContent(v)
				if err != nil {
					continue
				}
				var ids []string
				if json.Unmarshal(plain, &ids) != nil {
					continue
				}
				for _, sid := range ids {
					matched[sid] = true
				}
			}
			for sid := range matched {
				bodyHits[sid]++
			}
		}
		return b.ForEach(func(k, v []byte) error {
			ks := string(k)
			if !strings.HasPrefix(ks, "d|") {
				return nil
			}
			plain, err := decodeMemContent(v)
			if err != nil {
				return nil
			}
			var d histSearchDoc
			if json.Unmarshal(plain, &d) != nil || d.V != histSearchIndexVersion {
				return nil
			}
			id := ks[len("d|"):]
			indexedIDs[id] = true
			snippets[id] = d.Apercu
			return nil
		})
	})

	// Fusion de la session vive. max() plutôt que += : elle ÉCRASE la copie
	// éventuellement périmée de son archive (OpenSession la laisse dans chathist) —
	// une seule entrée par id, jamais de double comptage.
	if liveInScope {
		bset := histTokenSet(histVisibleText(liveLog))
		nb := 0
		for _, t := range terms {
			if histSetMatches(bset, t) {
				nb++
			}
		}
		tset := histTokenSet(liveTitle)
		nt := 0
		for _, t := range terms {
			if histSetMatches(tset, t) {
				nt++
			}
		}
		if nb > bodyHits[liveID] {
			bodyHits[liveID] = nb
		}
		if nt > titleHits[liveID] {
			titleHits[liveID] = nt
		}
		metaByID[liveID] = convArchiveMeta{ID: liveID, Project: liveProj, Title: liveTitle,
			Fav: liveFav, SavedAt: liveAt, Turns: countUserTurns(liveLog)}
	}

	cands := map[string]bool{}
	for id := range bodyHits {
		cands[id] = true
	}
	for id := range titleHits {
		cands[id] = true
	}
	// Score = 2*titre + 1*corps (même ratio que recall_search). Défini UNE fois :
	// l'inclusion (score > 0) et le tri s'appuient dessus.
	score := func(id string) int { return 2*titleHits[id] + bodyHits[id] }
	hits := make([]convArchiveHit, 0, len(cands))
	for id := range cands {
		m, ok := metaByID[id]
		if !ok {
			continue // id orphelin (session supprimée ; posting resté après une suppression verrouillée)
		}
		if score(id) <= 0 {
			continue
		}
		hits = append(hits, convArchiveHit{convArchiveMeta: m, Snippet: snippets[id]})
	}
	sort.Slice(hits, func(i, j int) bool {
		si, sj := score(hits[i].ID), score(hits[j].ID)
		if si != sj {
			return si > sj
		}
		if hits[i].SavedAt != hits[j].SavedAt {
			return hits[i].SavedAt > hits[j].SavedAt
		}
		return hits[i].ID < hits[j].ID
	})
	res.Total = len(hits) // AVANT pagination
	if limit > 0 {
		off := max(0, min(offset, len(hits)))
		hits = hits[off:min(off+limit, len(hits))]
	}
	res.Hits = hits

	// Couverture : le dénominateur est l'ensemble des sessions RÉELLEMENT indexables
	// de la portée. Une fiche chatmeta SANS corps dans chathist ne le sera jamais : le
	// rattrapage itère allKeys(bkChatHist), donc une telle session (incohérence de
	// données pré-existante, du même ordre que le cas miroir traité par listAllArchives)
	// ne peut recevoir aucune fiche d'index. La compter au dénominateur rendait
	// indexed < indexed_total à JAMAIS — constaté sur la base réelle (13/14 stable
	// pendant des minutes, /api/chat/peek de l'id manquant rendant 404), l'UI affichant
	// « indexation en cours » en permanence. Les DEUX côtés doivent ranger sur le MÊME
	// ensemble ; invariant conservé : Indexed <= IndexedTotal. allKeys ne copie que les
	// CLÉS (jamais allKV, qui chargerait les corps de conversation).
	histIDs := map[string]bool{}
	for _, id := range allKeys(bkChatHist) {
		histIDs[id] = true
	}
	scopeSet := map[string]bool{}
	for _, m := range metas {
		if histIDs[m.ID] {
			scopeSet[m.ID] = true
		}
	}
	if liveInScope {
		scopeSet[liveID] = true
	}
	res.IndexedTotal = len(scopeSet)
	idx := 0
	for id := range scopeSet {
		if liveInScope && id == liveID {
			idx++
			continue
		}
		if indexedIDs[id] {
			idx++
		}
	}
	res.Indexed = idx
	return res
}

// histBodyScores sert la recherche de l'historique (handleChatHistory) : pour
// chaque session, le nombre de termes de la requête trouvés dans son TEXTE
// (index + session vive). La portée (projet, mode, tous projets) reste celle de
// la liste déjà calculée par l'appelant, qui ne garde que ses propres ids.
// state vaut "ok", "locked" (mémoire chiffrée verrouillée : corps illisibles) ou
// "no_terms" (aucun terme cherchable).
func histBodyScores(q string) (scores map[string]int, terms []string, state string) {
	terms = histQueryTerms(q)
	if len(terms) == 0 {
		return nil, []string{}, "no_terms"
	}
	if memLocked() {
		return nil, terms, "locked"
	}
	scores = map[string]int{}
	_ = view(bkChatSearch, func(b *bolt.Bucket) error {
		for _, t := range terms {
			prefix := []byte(histPostKey(t))
			matched := map[string]bool{}
			c := b.Cursor()
			for k, v := c.Seek(prefix); k != nil; k, v = c.Next() {
				if !histTermMatches(string(k[len("p|"):]), t) {
					break
				}
				plain, err := decodeMemContent(v)
				if err != nil {
					continue
				}
				var ids []string
				if json.Unmarshal(plain, &ids) != nil {
					continue
				}
				for _, sid := range ids {
					matched[sid] = true
				}
			}
			for sid := range matched {
				scores[sid]++
			}
		}
		return nil
	})
	// Session vive : son archive peut être périmée, on cherche dans le journal en
	// RAM (copié sous verrou, tokenisé hors verrou) et on garde le meilleur score.
	conv.mu.Lock()
	liveID := conv.ID
	liveLog := append([]LogEvent(nil), conv.Log...)
	conv.mu.Unlock()
	if liveID != "" && len(liveLog) > 0 {
		set := histTokenSet(histVisibleText(liveLog))
		n := 0
		for _, t := range terms {
			if histSetMatches(set, t) {
				n++
			}
		}
		if n > scores[liveID] {
			scores[liveID] = n
		}
	}
	return scores, terms, "ok"
}
