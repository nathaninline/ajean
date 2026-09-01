package ajean

// tracker.go — les TRACKERS : le 3ᵉ type de mémoire (données datées qui s'accumulent :
// compteurs, relevés, journaux d'événements). Séparé des pages .md (faits +
// procédures) exprès : un tracker grandit sans fin, donc on ne le lit JAMAIS en
// entier. On le consulte par NIVEAUX (vue d'ensemble → année → mois → événements),
// chaque réponse indiquant comment descendre — le contexte n'explose jamais.
//
// Un seul outil `tracker` côté modèle (action view/add/edit/delete). Stockage
// structuré (événements datés) dans le bucket bkTracker, chiffré comme la mémoire et
// cloisonné par projet (clé « <slug-projet>/<slug-tracker> »).

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// TrackerEvent = un point daté d'un tracker. TS = l'instant de l'événement (ms), Text =
// la valeur ou la note (« abonnés : 4210 », « pesée 78,2 kg »…).
type TrackerEvent struct {
	ID   string `json:"id"`
	TS   int64  `json:"ts"`
	Text string `json:"text"`
	// DateOnly = le point n'a PAS d'heure (juste une date). Sans ça, un point saisi
	// sans heure s'enregistrait à 00:00 et s'affichait « minuit », ce qui est faux :
	// il n'a simplement pas d'heure. On ne montre alors que la date.
	DateOnly bool `json:"date_only,omitempty"`
}

// Tracker = un flux d'événements sous un nom (« abonnés », « poids »…). Events triés
// par TS croissant.
type Tracker struct {
	Name   string         `json:"name"`
	Events []TrackerEvent `json:"events"`
}

// --- stockage (bucket bkTracker, clé <projet>/<slug>) ---------------------------

func trackerKey(proj, slug string) string { return proj + "/" + slug }

// trackerLoad lit un tracker du projet actif par son slug. false si absent/verrouillé.
func trackerLoad(slug string) (*Tracker, bool) {
	var s Tracker
	if !getStoreJSON(bkTracker, trackerKey(activeProjectSlug(), slug), &s) {
		return nil, false
	}
	return &s, true
}

func trackerSave(slug string, s *Tracker) error {
	sort.SliceStable(s.Events, func(i, j int) bool { return s.Events[i].TS < s.Events[j].TS })
	return putStoreJSON(bkTracker, trackerKey(activeProjectSlug(), slug), s)
}

// trackerMeta = ligne légère de la vue d'ensemble.
type trackerMeta struct {
	Slug     string        `json:"slug"`
	Name     string        `json:"name"`
	Count    int           `json:"count"`
	FirstTS  int64         `json:"first_ts"`
	LastTS   int64         `json:"last_ts"`
	LastText string        `json:"last_text"` // texte du dernier point (dernière valeur)
	last     *TrackerEvent // dernier événement (pour formater sa date selon DateOnly)
}

// trackerList renvoie les trackers du projet actif (métadonnées seulement), le plus
// récemment alimenté d'abord.
func trackerList() []trackerMeta {
	prefix := activeProjectSlug() + "/"
	var out []trackerMeta
	for k := range allKV(bkTracker) {
		if !strings.HasPrefix(k, prefix) {
			continue
		}
		slug := strings.TrimPrefix(k, prefix)
		if s, ok := trackerLoad(slug); ok {
			m := trackerMeta{Slug: slug, Name: s.Name, Count: len(s.Events)}
			if n := len(s.Events); n > 0 {
				m.FirstTS = s.Events[0].TS
				last := s.Events[n-1]
				m.LastTS = last.TS
				m.LastText = last.Text
				m.last = &last
			}
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].LastTS > out[j].LastTS })
	return out
}

// --- mutations ----------------------------------------------------------------

// trackerAdd ajoute un point à un tracker (créé s'il n'existe pas). `when` vide = maintenant.
func trackerAdd(name, when, text string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("nom de tracker vide")
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("contenu vide")
	}
	ts, dateOnly, err := parseWhen(when)
	if err != nil {
		return "", err
	}
	slug := slugify(name)
	if slug == "" {
		slug = "tracker"
	}
	s, ok := trackerLoad(slug)
	if !ok {
		s = &Tracker{Name: name}
	}
	if strings.TrimSpace(s.Name) == "" {
		s.Name = name
	}
	id := fmt.Sprintf("%d", time.Now().UnixNano())
	s.Events = append(s.Events, TrackerEvent{ID: id, TS: ts, Text: strings.TrimSpace(text), DateOnly: dateOnly})
	if err := trackerSave(slug, s); err != nil {
		return "", err
	}
	return id, nil
}

// trackerEditEvent modifie un événement (texte et/ou date). when/text vides = inchangés.
func trackerEditEvent(slug, id, when, text string) error {
	s, ok := trackerLoad(slug)
	if !ok {
		return fmt.Errorf("tracker introuvable")
	}
	for i := range s.Events {
		if s.Events[i].ID == id {
			if strings.TrimSpace(text) != "" {
				s.Events[i].Text = strings.TrimSpace(text)
			}
			if strings.TrimSpace(when) != "" {
				ts, dateOnly, err := parseWhen(when)
				if err != nil {
					return err
				}
				s.Events[i].TS = ts
				s.Events[i].DateOnly = dateOnly
			}
			return trackerSave(slug, s)
		}
	}
	return fmt.Errorf("événement introuvable")
}

// trackerDeleteEvent retire un événement. trackerDelete supprime le tracker entier.
func trackerDeleteEvent(slug, id string) error {
	s, ok := trackerLoad(slug)
	if !ok {
		return fmt.Errorf("tracker introuvable")
	}
	kept := s.Events[:0]
	found := false
	for _, e := range s.Events {
		if e.ID == id {
			found = true
			continue
		}
		kept = append(kept, e)
	}
	if !found {
		return fmt.Errorf("événement introuvable")
	}
	s.Events = kept
	return trackerSave(slug, s)
}

func trackerDelete(slug string) error {
	return putBytes(bkTracker, trackerKey(activeProjectSlug(), slug), nil)
}

// trackerMoveToProject déplace un tracker du projet actif vers un autre projet. Les
// octets sont déplacés tels quels (la DEK est globale → un blob chiffré reste
// lisible dans le projet cible, déplacement possible même mémoire verrouillée).
func trackerMoveToProject(slug, toSlug string) error {
	from := activeProjectSlug()
	if !projectExists(toSlug) {
		return fmt.Errorf("projet cible introuvable")
	}
	if from == toSlug {
		return nil
	}
	fromKey, toKey := trackerKey(from, slug), trackerKey(toSlug, slug)
	raw := getBytes(bkTracker, fromKey)
	if len(raw) == 0 {
		return fmt.Errorf("tracker introuvable")
	}
	if len(getBytes(bkTracker, toKey)) > 0 {
		return fmt.Errorf("un tracker du même nom existe déjà dans le projet cible")
	}
	if err := putBytes(bkTracker, toKey, raw); err != nil {
		return err
	}
	return putBytes(bkTracker, fromKey, nil)
}

// --- temps --------------------------------------------------------------------

// parseWhen lit une date à précision variable (« 2026 », « 2026-07 »,
// « 2026-07-15 », « 2026-07-15 14:30 ») et renvoie l'instant en ms (heure locale).
// Vide ou « now » = maintenant. Le T de l'ISO est accepté à la place de l'espace.
// dateOnly=true quand aucune heure n'a été fournie (juste une date) → le point
// n'aura pas d'heure d'affichage (pas de faux « minuit »).
func parseWhen(when string) (ts int64, dateOnly bool, err error) {
	w := strings.TrimSpace(when)
	if w == "" || strings.EqualFold(w, "now") || strings.EqualFold(w, "maintenant") {
		return time.Now().UnixMilli(), false, nil
	}
	w = strings.Replace(w, "T", " ", 1)
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02 15"} {
		if t, e := time.ParseInLocation(layout, w, time.Local); e == nil {
			return t.UnixMilli(), false, nil
		}
	}
	for _, layout := range []string{"2006-01-02", "2006-01", "2006"} {
		if t, e := time.ParseInLocation(layout, w, time.Local); e == nil {
			return t.UnixMilli(), true, nil
		}
	}
	return 0, false, fmt.Errorf("date non comprise : %q (attendu AAAA, AAAA-MM, AAAA-MM-JJ ou AAAA-MM-JJ HH:MM)", when)
}

// fmtTS formate un instant en « AAAA-MM-JJ HH:MM » (heure locale), base du
// filtrage par préfixe et du regroupement (toujours complet).
func fmtTS(ts int64) string { return time.UnixMilli(ts).In(time.Local).Format("2006-01-02 15:04") }

// fmtEvent formate un point pour AFFICHAGE : sans heure si le point n'en a pas
// (DateOnly), pour ne pas afficher un faux « minuit ».
func fmtEvent(e TrackerEvent) string {
	if e.DateOnly {
		return time.UnixMilli(e.TS).In(time.Local).Format("2006-01-02")
	}
	return fmtTS(e.TS)
}

// whenParts compte les composantes datées d'un `when` normalisé (0 = rien,
// 1 = année, 2 = mois, 3 = jour, 4+ = heure). Sert à décider le NIVEAU de la vue.
func whenParts(when string) int {
	w := strings.TrimSpace(when)
	if w == "" {
		return 0
	}
	w = strings.Replace(w, "T", " ", 1)
	date := w
	if sp := strings.IndexByte(w, ' '); sp >= 0 {
		date = w[:sp]
		if strings.TrimSpace(w[sp:]) != "" {
			return 4
		}
	}
	return len(strings.Split(date, "-"))
}

// --- vue par niveaux ----------------------------------------------------------

// trackerView construit la réponse texte de l'outil `tracker` en lecture. name vide =
// vue d'ensemble des trackers ; sinon on descend selon `when` (année → mois →
// événements). Chaque réponse dit comment zoomer, pour que le modèle sache
// qu'il y a un avant/un dessous sans jamais tout charger.
func trackerView(name, when string) string {
	if strings.TrimSpace(name) == "" {
		return trackerOverview()
	}
	slug := slugify(name)
	s, ok := trackerLoad(slug)
	if !ok {
		return fmt.Sprintf("[aucun tracker « %s »] Trackers existants : appelle tracker() sans argument pour la liste, ou ajoute un point avec action:\"add\".", name)
	}
	if len(s.Events) == 0 {
		return fmt.Sprintf("Tracker « %s » : vide pour l'instant.", s.Name)
	}
	prefix := strings.Replace(strings.TrimSpace(when), "T", " ", 1)
	// Filtre les événements dont l'horodatage commence par le préfixe demandé.
	var match []TrackerEvent
	for _, e := range s.Events {
		if prefix == "" || strings.HasPrefix(fmtTS(e.TS), prefix) {
			match = append(match, e)
		}
	}
	level := whenParts(when)
	switch {
	case level == 0:
		return trackerSkeleton(s)
	case level == 1: // une année → les mois
		if len(match) == 0 {
			return fmt.Sprintf("Tracker « %s » : rien en %s.", s.Name, when)
		}
		return trackerGroup(s.Name, when, match, "2006-01", "un mois", `when:"`+when+`-07"`)
	default: // mois, jour, heure → on liste les événements
		return trackerEvents(s.Name, when, match)
	}
}

// trackerOverview : la liste des trackers du projet (aucun contenu).
func trackerOverview() string {
	list := trackerList()
	if len(list) == 0 {
		return "Aucun tracker dans ce projet. Crée-en un en ajoutant un point : action:\"add\", name:\"…\", text:\"…\" (when facultatif = maintenant)."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Trackers du projet (%d) :\n", len(list))
	for _, m := range list {
		span := ""
		if m.Count > 0 {
			span = fmt.Sprintf(" — %d points, du %s au %s", m.Count, fmtDay(m.FirstTS), fmtDay(m.LastTS))
		}
		fmt.Fprintf(&b, "- %s%s\n", m.Name, span)
	}
	b.WriteString("Zoom : tracker(name:\"<nom>\") pour le détail d'un tracker.")
	return b.String()
}

// trackerSkeleton : les années présentes (avec compte) + l'année en cours détaillée
// par mois. C'est la vue d'entrée d'un tracker.
func trackerSkeleton(s *Tracker) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Tracker « %s » — %d points, du %s au %s\n", s.Name, len(s.Events), fmtDay(s.Events[0].TS), fmtDay(s.Events[len(s.Events)-1].TS))
	// Années (compte).
	years := map[string]int{}
	var order []string
	for _, e := range s.Events {
		y := time.UnixMilli(e.TS).In(time.Local).Format("2006")
		if years[y] == 0 {
			order = append(order, y)
		}
		years[y]++
	}
	sort.Strings(order)
	parts := make([]string, 0, len(order))
	for _, y := range order {
		parts = append(parts, fmt.Sprintf("%s (%d)", y, years[y]))
	}
	fmt.Fprintf(&b, "Années : %s\n", strings.Join(parts, " · "))
	// Année en cours détaillée par mois.
	cur := time.Now().In(time.Local).Format("2006")
	var curEv []TrackerEvent
	for _, e := range s.Events {
		if strings.HasPrefix(fmtTS(e.TS), cur) {
			curEv = append(curEv, e)
		}
	}
	if len(curEv) > 0 {
		fmt.Fprintf(&b, "%s (année en cours) : %s\n", cur, monthsLine(curEv))
	}
	fmt.Fprintf(&b, "Zoom : tracker(name:\"%s\", when:\"2025\") pour une année · when:\"%s-07\" pour un mois.", s.Name, cur)
	return b.String()
}

// trackerGroup regroupe des événements par un format de date (mois) avec comptes.
func trackerGroup(name, when string, ev []TrackerEvent, layout, unit, example string) string {
	groups := map[string]int{}
	var order []string
	for _, e := range ev {
		g := time.UnixMilli(e.TS).In(time.Local).Format(layout)
		if groups[g] == 0 {
			order = append(order, g)
		}
		groups[g]++
	}
	sort.Strings(order)
	parts := make([]string, 0, len(order))
	for _, g := range order {
		parts = append(parts, fmt.Sprintf("%s (%d)", g, groups[g]))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Tracker « %s » en %s — %d points :\n%s\n", name, when, len(ev), strings.Join(parts, " · "))
	fmt.Fprintf(&b, "Zoom : %s pour %s.", example, unit)
	return b.String()
}

// trackerEvents liste les événements (id, date, texte), plafonné pour ne pas gonfler
// le contexte — un préfixe plus précis affine.
func trackerEvents(name, when string, ev []TrackerEvent) string {
	const cap = 60
	var b strings.Builder
	shown := ev
	trimmed := false
	if len(ev) > cap {
		shown = ev[len(ev)-cap:] // les plus récents
		trimmed = true
	}
	fmt.Fprintf(&b, "Tracker « %s »", name)
	if strings.TrimSpace(when) != "" {
		fmt.Fprintf(&b, " en %s", when)
	}
	if trimmed {
		fmt.Fprintf(&b, " — %d points (les %d plus récents ; affine le when pour les plus anciens) :\n", len(ev), cap)
	} else {
		fmt.Fprintf(&b, " — %d points :\n", len(ev))
	}
	for _, e := range shown {
		fmt.Fprintf(&b, "- [%s] %s — %s\n", e.ID, fmtEvent(e), e.Text)
	}
	b.WriteString("Modifier/supprimer : action:\"edit\"|\"delete\", name, id (l'identifiant entre crochets).")
	return b.String()
}

// monthsLine résume des événements en « AAAA-MM (n) · … ».
func monthsLine(ev []TrackerEvent) string {
	groups := map[string]int{}
	var order []string
	for _, e := range ev {
		g := time.UnixMilli(e.TS).In(time.Local).Format("2006-01")
		if groups[g] == 0 {
			order = append(order, g)
		}
		groups[g]++
	}
	sort.Strings(order)
	parts := make([]string, 0, len(order))
	for _, g := range order {
		parts = append(parts, fmt.Sprintf("%s (%d)", g, groups[g]))
	}
	return strings.Join(parts, " · ")
}

// fmtDay formate un instant en « AAAA-MM-JJ » (pour les étendues).
func fmtDay(ts int64) string { return time.UnixMilli(ts).In(time.Local).Format("2006-01-02") }

// --- index injecté dans le contexte -------------------------------------------

// trackerIndexPrefix marque le message d'index des trackers injecté dans l'historique
// (détection / anti-doublon), comme l'index mémoire.
const trackerIndexPrefix = "Trackers (tracker tool)"

// trackerIndexMessage construit le message système listant les trackers du projet avec
// LEUR DERNIER POINT, injecté UNE FOIS en début de conversation (et après un
// compactage). L'IA sait alors qu'ils existent ET a souvent la réponse directe (la
// dernière valeur) SANS appeler l'outil — elle ne l'appelle que pour fouiller
// l'historique ou pour ajouter/modifier. Quelques lignes, pas le contenu.
func trackerIndexMessage() (Message, bool) {
	if memMode() != MemAlways {
		return Message{}, false
	}
	list := trackerList()
	if len(list) == 0 {
		return Message{}, false
	}
	var b strings.Builder
	b.WriteString(trackerIndexPrefix + " — this is the COMPLETE list of your trackers, each with its LATEST point. To say which trackers exist or give a latest value, answer straight from this — do NOT call tracker with no arguments to list them, it would only repeat this. Call tracker(name[, when]) only to look further back in history, or with action \"add\"/\"edit\"/\"delete\" to change data.\n")
	for _, m := range list {
		last := ""
		if m.last != nil {
			last = " — " + m.last.Text + " (" + fmtEvent(*m.last) + ")"
		}
		fmt.Fprintf(&b, "- %s%s\n", m.Name, last)
	}
	return Message{Role: "system", Content: strings.TrimRight(b.String(), "\n")}, true
}

func hasTrackerIndex(msgs []Message) bool {
	for _, m := range msgs {
		if s, ok := m.Content.(string); ok && strings.HasPrefix(s, trackerIndexPrefix) {
			return true
		}
	}
	return false
}

// ensureTrackerIndexFront préfixe l'index des trackers s'il n'est pas déjà présent
// (après un compactage qui l'aurait résumé/retiré).
func ensureTrackerIndexFront(msgs []Message) []Message {
	if hasTrackerIndex(msgs) {
		return msgs
	}
	if m, ok := trackerIndexMessage(); ok {
		return append([]Message{m}, msgs...)
	}
	return msgs
}
