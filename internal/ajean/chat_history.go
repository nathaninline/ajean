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
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"
)

// convArchive = une conversation archivée. Contient de quoi la restaurer à
// l'identique (Messages = vue modèle, Log = vue UI rejouable) + des métadonnées
// d'affichage pour la liste de l'historique.
type convArchive struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Fav      bool       `json:"fav,omitempty"` // épinglée en favori (remonte en tête)
	SavedAt  int64      `json:"saved_at"`      // ms
	Turns    int        `json:"turns"`
	Messages []Message  `json:"messages"`
	Log      []LogEvent `json:"log"`
	Seq      int        `json:"seq"`
	CtxUsed  int        `json:"ctx_used"`
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

func saveArchive(a *convArchive) error { return putJSON(bkChatHist, a.ID, a) }

func loadArchive(id string) (*convArchive, bool) {
	var a convArchive
	if !getJSON(bkChatHist, id, &a) || a.ID == "" {
		return nil, false
	}
	return &a, true
}

func deleteArchive(id string) error { return putBytes(bkChatHist, id, nil) }

// renameArchive donne un nom personnalisé à une conversation archivée. Un nom
// vide re-dérive le titre automatique du premier message.
func renameArchive(id, title string) error {
	a, ok := loadArchive(id)
	if !ok {
		return fmt.Errorf("conversation introuvable")
	}
	title = strings.TrimSpace(title)
	if title == "" {
		title = archiveTitle(a.Log)
	} else if len([]rune(title)) > 120 {
		title = string([]rune(title)[:120])
	}
	a.Title = title
	return saveArchive(a)
}

// deleteNonFavArchives supprime toutes les conversations archivées SAUF les
// favoris. Renvoie le nombre supprimé.
func deleteNonFavArchives() int {
	n := 0
	for _, m := range listArchives() {
		if m.Fav {
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
	kv := allKV(bkChatHist)
	out := make([]convArchiveMeta, 0, len(kv))
	for _, v := range kv {
		var m convArchiveMeta
		if json.Unmarshal([]byte(v), &m) == nil && m.ID != "" {
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

// snapshotForArchive prend une copie archivable de la conversation courante, ou
// nil si elle est vide (rien à archiver). Prend le verrou lui-même.
func (c *Conversation) snapshotForArchive() *convArchive {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.Log) == 0 && len(c.Messages) == 0 {
		return nil
	}
	a := &convArchive{
		ID:       fmt.Sprintf("%d", time.Now().UnixNano()),
		SavedAt:  time.Now().UnixMilli(),
		Messages: append([]Message(nil), c.Messages...),
		Log:      append([]LogEvent(nil), c.Log...),
		Seq:      c.Seq,
		CtxUsed:  c.CtxUsed,
	}
	a.Turns = countUserTurns(c.Log)
	a.Title = archiveTitle(c.Log)
	return a
}

// ArchiveAndReset archive la conversation courante (si non vide) puis en démarre
// une vierge — c'est le « clear chat ». Renvoie l'id archivé, vide si rien à
// archiver.
func (c *Conversation) ArchiveAndReset() string {
	a := c.snapshotForArchive()
	if a != nil {
		_ = saveArchive(a)
	}
	c.Reset()
	if a != nil {
		return a.ID
	}
	return ""
}

// RestoreArchive remplace la conversation courante par l'archive `id`. La
// courante, si elle n'est pas vide, est archivée d'abord (clear chat implicite).
// L'archive restaurée est retirée de l'historique (elle redevient active). Les
// abonnés reçoivent un reset puis le rejeu du fil restauré (bump d'epoch).
func (c *Conversation) RestoreArchive(id string) error {
	a, ok := loadArchive(id)
	if !ok {
		return fmt.Errorf("conversation introuvable")
	}
	// 1. Ne rien perdre : la conversation en cours part elle-même à l'historique.
	if cur := c.snapshotForArchive(); cur != nil {
		_ = saveArchive(cur)
	}
	// 2. Charger l'archive dans la conversation active.
	c.Stop()
	c.mu.Lock()
	c.Messages = append([]Message(nil), a.Messages...)
	c.Log = append([]LogEvent(nil), a.Log...)
	c.Seq = a.Seq
	c.CtxUsed = a.CtxUsed
	c.epoch++              // invalide les abonnés → ils nettoient et rejouent le fil restauré
	c.pendingReplay = true // un fil complet suit : les abonnés le rejouent REPLIÉ (comme au chargement de page)
	c.Generating = false
	c.cancel = nil
	c.cond.Broadcast()
	c.mu.Unlock()
	c.persist()
	// 3. L'archive est désormais la conversation active : on la retire de la liste.
	_ = deleteArchive(id)
	return nil
}
