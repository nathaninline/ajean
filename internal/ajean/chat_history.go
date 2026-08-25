package ajean

// chat_history.go — historique des conversations.
//
// AJEAN n'a qu'UNE conversation active (voir chat_conversation.go). « clear chat »
// la vidait pour de bon. Ici, au lieu de la jeter, on l'ARCHIVE : chaque « clear
// chat » range la conversation courante dans l'historique (bucket bkChatHist, une
// entrée JSON par conversation), d'où on peut la RECHARGER plus tard ou la
// SUPPRIMER définitivement (issue #33).
//
// Recharger une archive la remet comme conversation active. Si une conversation
// est déjà en cours à ce moment-là, elle est elle-même archivée d'abord (un clear
// chat implicite) : on ne perd jamais rien. L'archive rechargée est retirée de
// l'historique — elle EST redevenue la conversation active, la garder en double
// ferait réapparaître un doublon au prochain clear.

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

// convArchive = une conversation archivée. Contient de quoi la restaurer à
// l'identique (Messages = vue modèle, Log = vue UI rejouable) + des métadonnées
// d'affichage pour la liste de l'historique.
type convArchive struct {
	ID           string     `json:"id"`
	Title        string     `json:"title"`
	Fav          bool       `json:"fav,omitempty"` // épinglée en favori (remonte en tête)
	SavedAt      int64      `json:"saved_at"`      // ms
	Turns        int        `json:"turns"`
	Messages     []Message  `json:"messages"`
	Log          []LogEvent `json:"log"`
	Seq          int        `json:"seq"`
	CtxUsed      int        `json:"ctx_used"`
	CompactCount int        `json:"compact_count,omitempty"` // nb de compactages (issue #47)
}

// convArchiveMeta = la partie légère (sans Messages/Log) pour lister sans charger
// le fil entier de chaque conversation.
type convArchiveMeta struct {
	ID      string `json:"id"`
	Title   string `json:"title"`
	Fav     bool   `json:"fav"`
	SavedAt int64  `json:"saved_at"`
	Turns   int    `json:"turns"`
}

// archiveTitle dérive un titre lisible du premier message utilisateur du fil,
// tronqué. Faute de message utilisateur (ne devrait pas arriver), un libellé
// générique.
func archiveTitle(log []LogEvent) string {
	for _, ev := range log {
		if u, ok := ev.Delta["user"].(string); ok {
			t := strings.TrimSpace(u)
			t = strings.ReplaceAll(t, "\n", " ")
			if t == "" {
				continue
			}
			const max = 80
			if len([]rune(t)) > max {
				t = string([]rune(t)[:max]) + "…"
			}
			return t
		}
	}
	return "Conversation"
}

// countUserTurns compte les tours (bulles utilisateur) d'un journal.
func countUserTurns(log []LogEvent) int {
	n := 0
	for _, ev := range log {
		if _, ok := ev.Delta["user"]; ok {
			n++
		}
	}
	return n
}

// --- persistance --------------------------------------------------------------

func saveArchive(a *convArchive) error {
	// Chiffré si le chiffrement est actif ET déverrouillé (le fil ET son titre
	// d'index contiennent des infos). Verrouillé : putStoreJSON refuse d'écrire du
	// clair (errStoreLocked) — on ne dégrade jamais un blob chiffré.
	if err := putStoreJSON(bkChatHist, a.ID, a); err != nil {
		return err
	}
	// Index léger tenu à jour en parallèle : lister ne relit alors que ces petites
	// métadonnées, pas le fil complet de chaque session.
	return putStoreJSON(bkChatMeta, a.ID, convArchiveMeta{ID: a.ID, Title: a.Title, Fav: a.Fav, SavedAt: a.SavedAt, Turns: a.Turns})
}

func loadArchive(id string) (*convArchive, bool) {
	var a convArchive
	if !getStoreJSON(bkChatHist, id, &a) || a.ID == "" {
		return nil, false
	}
	return &a, true
}

func deleteArchive(id string) error {
	_ = putBytes(bkChatMeta, id, nil)
	return putBytes(bkChatHist, id, nil)
}

// renameArchive donne un nom personnalisé à une conversation archivée. Un nom
// vide re-dérive le titre automatique du premier message.
func renameArchive(id, title string) error {
	a, ok := loadArchive(id)
	if !ok {
		return fmt.Errorf("conversation introuvable")
	}
	custom := strings.TrimSpace(title)
	if len([]rune(custom)) > 120 {
		custom = string([]rune(custom)[:120])
	}
	if custom == "" {
		a.Title = archiveTitle(a.Log)
	} else {
		a.Title = custom
	}
	return saveArchive(a)
}

// deleteNonFavArchives supprime toutes les sessions SAUF les favoris ET la
// session active `activeID`. Renvoie le nombre supprimé.
func deleteNonFavArchives(activeID string) int {
	n := 0
	for _, m := range listArchives() {
		if m.Fav || m.ID == activeID {
			continue
		}
		if deleteArchive(m.ID) == nil {
			n++
		}
	}
	return n
}

// setArchiveFav épingle/dépingle une conversation archivée en favori.
func setArchiveFav(id string, fav bool) error {
	a, ok := loadArchive(id)
	if !ok {
		return fmt.Errorf("conversation introuvable")
	}
	a.Fav = fav
	return saveArchive(a)
}

// listArchives renvoie les métadonnées de toutes les conversations archivées, la
// plus récente d'abord.
func listArchives() []convArchiveMeta {
	// Les valeurs d'index peuvent être chiffrées : on les décode via getStoreJSON.
	// Mémoire verrouillée = index illisible = liste vide (les sessions réapparaissent
	// au déverrouillage), jamais une erreur.
	metaKeys := allKV(bkChatMeta)
	out := make([]convArchiveMeta, 0, len(metaKeys))
	seen := map[string]bool{}
	for id := range metaKeys {
		var m convArchiveMeta
		if getStoreJSON(bkChatMeta, id, &m) && m.ID != "" {
			out = append(out, m)
			seen[m.ID] = true
		}
	}
	// Sessions d'avant l'index (migration) : blob complet présent sans entrée
	// d'index. On le reconstruit une fois (respecte le chiffrement via load/save).
	for id := range allKV(bkChatHist) {
		if seen[id] {
			continue
		}
		if a, ok := loadArchive(id); ok && a.ID != "" {
			m := convArchiveMeta{ID: a.ID, Title: a.Title, Fav: a.Fav, SavedAt: a.SavedAt, Turns: a.Turns}
			_ = putStoreJSON(bkChatMeta, a.ID, m)
			out = append(out, m)
		}
	}
	// Favoris épinglés en tête, puis les plus récentes d'abord.
	sort.Slice(out, func(i, j int) bool {
		if out[i].Fav != out[j].Fav {
			return out[i].Fav
		}
		return out[i].SavedAt > out[j].SavedAt
	})
	return out
}

// --- opérations sur la conversation active ------------------------------------

// snapshotForSession prend une copie de la conversation courante sous SON id de
// session, ou nil si elle est vide (rien à enregistrer). Prend le verrou.
func (c *Conversation) snapshotForSession() *convArchive {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.Log) == 0 && len(c.Messages) == 0 {
		return nil
	}
	a := &convArchive{
		ID:           c.ID, // id STABLE de la session (pas un nouvel id à chaque fois)
		SavedAt:      time.Now().UnixMilli(),
		Messages:     append([]Message(nil), c.Messages...),
		Log:          append([]LogEvent(nil), c.Log...),
		Seq:          c.Seq,
		CtxUsed:      c.CtxUsed,
		CompactCount: c.CompactCount,
	}
	a.Turns = countUserTurns(c.Log)
	// Nom personnalisé si défini, sinon titre dérivé du premier message.
	if strings.TrimSpace(c.ActiveTitle) != "" {
		a.Title = c.ActiveTitle
	} else {
		a.Title = archiveTitle(c.Log)
	}
	a.Fav = c.ActiveFav
	return a
}

// upsertSession reflète la conversation active dans la liste des sessions (sous
// son id stable). No-op si elle est vide (pas de session fantôme).
func (c *Conversation) upsertSession() {
	if a := c.snapshotForSession(); a != nil {
		_ = saveArchive(a)
	}
}

// NewSession sauvegarde la conversation courante dans SA session puis démarre une
// session vierge (nouvel id) — c'est le « clear chat » / « nouvelle session ».
func (c *Conversation) NewSession() string {
	c.upsertSession()
	c.Reset()
	return c.ID
}

// OpenSession charge la session `id` comme conversation active. La conversation
// courante est d'abord sauvegardée dans SA propre session (elle RESTE dans la
// liste, aucun doublon). La session ouverte n'est PAS retirée : elle devient
// l'active, toujours listée et marquée « en cours ».
func (c *Conversation) OpenSession(id string) error {
	a, ok := loadArchive(id)
	if !ok {
		return fmt.Errorf("session introuvable")
	}
	// 1. Sauver la conversation en cours dans sa session (rien ne se perd).
	c.upsertSession()
	// 2. Charger la session demandée dans l'active.
	c.Stop()
	c.mu.Lock()
	c.ID = a.ID
	c.Messages = append([]Message(nil), a.Messages...)
	c.Log = append([]LogEvent(nil), a.Log...)
	c.Seq = a.Seq
	c.CtxUsed = a.CtxUsed
	c.CompactCount = a.CompactCount
	c.ActiveTitle = a.Title
	c.ActiveFav = a.Fav
	c.epoch++              // invalide les abonnés → ils nettoient et rejouent le fil
	c.pendingReplay = true // un fil complet suit : rejeu REPLIÉ (comme au chargement de page)
	c.Generating = false
	c.cancel = nil
	c.cond.Broadcast()
	c.mu.Unlock()
	c.persist()
	return nil
}

// currentID renvoie l'id de la session active (sous verrou).
func (c *Conversation) currentID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.ID
}

// setActiveTitleIfMatch met à jour le nom de la conversation active si c'est la
// session `id` qui vient d'être renommée (title vide = dérivation auto reprend).
func (c *Conversation) setActiveTitleIfMatch(id, title string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ID == id {
		c.ActiveTitle = strings.TrimSpace(title)
	}
}

// setActiveFavIfMatch met à jour le favori de la conversation active si c'est la
// session `id` qui vient d'être (dé)favorisée.
func (c *Conversation) setActiveFavIfMatch(id string, fav bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ID == id {
		c.ActiveFav = fav
	}
}

// DeleteSession supprime définitivement la session `id`. Si c'est la session
// active, on repart aussitôt sur une session vierge (pas de fil orphelin qui
// pointerait vers une session effacée).
func (c *Conversation) DeleteSession(id string) error {
	if err := deleteArchive(id); err != nil {
		return err
	}
	if c.currentID() == id {
		c.Reset()
	}
	return nil
}
