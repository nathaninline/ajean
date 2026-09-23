package ajean

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strings"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"
)

// tool_results.go — résultats COMPLETS des outils, pour le bouton « voir plus ».
//
// Le flux n'envoie à l'UI qu'un aperçu (toolPreviewChars) ; le reste se charge au
// clic. On le cherchait dans conv.Messages, ce qui ratait souvent :
//   - PENDANT un tour d'agent, les messages tool ne sont versés dans
//     conv.Messages qu'à la FIN du tour (d'où les bash qui ne se dépliaient pas) ;
//   - après un compactage, les anciens messages tool ont disparu de la vue modèle ;
//   - une session rouverte depuis l'historique n'est plus la conversation vive.
// On garde donc chaque résultat coupé dans un bucket dédié, sous un id PROPRE
// (pas le tool_call_id, que certains parseurs réutilisent d'un tour à l'autre).
//
// Rangement PAR CONVERSATION : la clé est « <id de session>.<aléa> ». Les résultats
// vivent donc aussi longtemps que leur conversation (supprimés avec elle, voir
// deleteToolResultsFor) au lieu d'un plafond global qui faisait disparaître ceux
// des vieilles conversations. Les valeurs passent par le chiffrement de la mémoire
// (putStoreBytes) : un résultat d'outil peut contenir un mail, un fichier…
//
// Les clés « tr_… » (v0.15.5–0.15.6, sans session, en clair) restent lisibles et
// sont plafonnées aux toolResLegacyKeep plus récentes.

const (
	bkToolRes         = "toolres"
	toolResLegacyKeep = 1000
	toolResPruneGap   = 200 // nettoyage tenté toutes les N écritures
)

var toolResWrites atomic.Int64

func toolResID(sid string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	if sid = strings.TrimSpace(sid); sid == "" {
		sid = "nosession"
	}
	return sid + "." + hex.EncodeToString(b[:])
}

// saveToolResult enregistre un résultat complet de la conversation ACTIVE et
// renvoie son id ("" en cas d'échec, mémoire verrouillée comprise : l'appelant
// envoie alors le résultat entier dans le flux).
func saveToolResult(result string) string { return saveToolResultFor(conv.currentID(), result) }

func saveToolResultFor(sid, result string) string {
	id := toolResID(sid)
	if id == "" || putStoreBytes(bkToolRes, id, []byte(result)) != nil {
		return ""
	}
	if toolResWrites.Add(1)%toolResPruneGap == 0 {
		go pruneToolResults()
	}
	return id
}

// loadToolResult relit un résultat enregistré.
func loadToolResult(id string) (string, bool) {
	if strings.HasPrefix(id, "tr_") { // ancien format : 16 hex d'horodatage + résultat, en clair
		v := getBytes(bkToolRes, id)
		if len(v) < 16 {
			return "", false
		}
		return string(v[16:]), true
	}
	b, ok := getStoreBytes(bkToolRes, id)
	return string(b), ok
}

// deleteToolResultsFor supprime les résultats d'une conversation supprimée.
func deleteToolResultsFor(sid string) {
	if sid == "" {
		return
	}
	prefix := []byte(sid + ".")
	_ = update(bkToolRes, func(b *bolt.Bucket) error {
		c := b.Cursor()
		var keys [][]byte
		for k, _ := c.Seek(prefix); k != nil && strings.HasPrefix(string(k), string(prefix)); k, _ = c.Next() {
			keys = append(keys, append([]byte(nil), k...))
		}
		for _, k := range keys {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}

// pruneToolResults supprime les résultats de conversations qui n'existent plus
// (orphelins) et plafonne les anciennes clés « tr_… ».
func pruneToolResults() {
	alive := map[string]bool{conv.currentID(): true}
	for _, id := range allKeys(bkChatMeta) {
		alive[id] = true
	}
	for _, id := range allKeys(bkChatHist) {
		alive[id] = true
	}
	_ = update(bkToolRes, func(b *bolt.Bucket) error {
		type kt struct{ k, t string }
		var legacy []kt
		var orphans []string
		_ = b.ForEach(func(k, v []byte) error {
			key := string(k)
			if strings.HasPrefix(key, "tr_") {
				t := ""
				if len(v) >= 16 {
					t = string(v[:16])
				}
				legacy = append(legacy, kt{key, t})
				return nil
			}
			if i := strings.LastIndexByte(key, '.'); i > 0 && key[:i] != "nosession" && !alive[key[:i]] {
				orphans = append(orphans, key)
			}
			return nil
		})
		for _, k := range orphans {
			if err := b.Delete([]byte(k)); err != nil {
				return err
			}
		}
		if len(legacy) > toolResLegacyKeep {
			sort.Slice(legacy, func(i, j int) bool { return legacy[i].t < legacy[j].t })
			for _, e := range legacy[:len(legacy)-toolResLegacyKeep] {
				if err := b.Delete([]byte(e.k)); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

// ===== Allègement des anciennes conversations =================================
//
// Jusqu'à la v0.15.4, chaque résultat d'outil était stocké EN ENTIER deux fois
// dans une conversation : dans la vue du modèle (Messages) et dans le journal
// d'affichage (Log). Sur une longue session d'agent, des dizaines de Mo — relus,
// réécrits et renvoyés à l'écran à chaque ouverture. slimArchives remplace, dans le
// JOURNAL uniquement, chaque long résultat par son aperçu + une référence au texte
// complet rangé dans toolres (chiffré, supprimé avec la conversation) : SANS PERTE.
// Tâche de fond unique, reprise là où elle s'est arrêtée (liste des sessions déjà
// traitées dans bkState), qui n'avance que mémoire déverrouillée.

// v2 : range aussi les images en base64 par référence (chat_images.go).
// v3 : images du journal (ancien mode image) par référence, et « body » (texte en
// cours d'écriture) retiré des outils terminés qui ont leur diff.
const slimStateKey = "slim_v3"

var slimRunning atomic.Bool

func startSlimArchives() {
	if !slimRunning.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer slimRunning.Store(false)
		time.Sleep(20 * time.Second) // laisser le démarrage (moteur, lien) se faire d'abord
		slimArchives()
	}()
}

func slimArchives() {
	// La conversation active aussi : ses images partent au prochain enregistrement.
	// Jamais pendant une génération : runChat lit ces mêmes messages.
	conv.mu.Lock()
	inline := !conv.Generating && hasInlineImages(conv.Messages)
	conv.mu.Unlock()
	if inline {
		conv.persist()
	}
	done := map[string]bool{}
	var list []string
	if getJSON(bkState, slimStateKey, &list) {
		for _, id := range list {
			done[id] = true
		}
	}
	for _, id := range allKeys(bkChatHist) {
		if done[id] || id == conv.currentID() {
			continue
		}
		a, ok := loadArchive(id)
		if !ok {
			if memEncActive() && !memUnlocked() {
				return // chiffrée et verrouillée : on reprendra au déverrouillage
			}
			// Illisible pour une autre raison (entrée corrompue) : on la saute plutôt
			// que de bloquer toute la tâche.
		} else if slimmed := slimArchiveLog(a); slimmed || hasInlineImages(a.Messages) {
			if saveArchive(a) != nil { // saveArchive range les images par référence
				return
			}
		}
		list = append(list, id)
		done[id] = true
		_ = putJSON(bkState, slimStateKey, list)
		time.Sleep(30 * time.Millisecond) // ne pas monopoliser la base
	}
}

// slimArchiveLog allège le journal d'une archive (voir plus haut). Renvoie true si
// quelque chose a changé. Le texte complet part TOUJOURS dans toolres (rangé avec
// la conversation) plutôt que de pointer vers Messages : un message du modèle peut
// disparaître si la conversation est rouverte puis compactée.
func slimArchiveLog(a *convArchive) bool {
	changed := false
	for _, ev := range a.Log {
		// Ancien mode image : l'image générée était gardée en base64 dans le journal.
		if v, ok := ev.Delta["image"]; ok {
			if nv, ch := refDataURLs(v); ch {
				ev.Delta["image"] = nv
				changed = true
			}
		}
		tu, ok := ev.Delta["tool_used"].(map[string]any)
		if !ok {
			continue
		}
		// Texte « en cours d'écriture » resté dans un outil terminé : doublon du diff.
		if _, hasBody := tu["body"]; hasBody && tu["diff"] != nil {
			if done, _ := tu["done"].(bool); done {
				delete(tu, "body")
				changed = true
			}
		}
		if done, _ := tu["done"].(bool); !done {
			continue
		}
		if rid, _ := tu["result_id"].(string); rid != "" {
			continue
		}
		res, _ := tu["result"].(string)
		prev, n, cut := toolResultPreview(res)
		if !cut {
			continue
		}
		ref := saveToolResultFor(a.ID, res)
		if ref == "" {
			continue
		}
		tu["result"] = prev
		tu["result_chars"] = n
		tu["result_id"] = ref
		changed = true
	}
	return changed
}
