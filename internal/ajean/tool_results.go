package ajean

import (
	"crypto/rand"
	"encoding/hex"
	"sort"
	"strconv"
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

const (
	bkToolRes       = "toolres"
	toolResKeep     = 1000 // nombre de résultats gardés (les plus anciens sont purgés)
	toolResPruneGap = 50   // purge tentée toutes les N écritures
)

var toolResWrites atomic.Int64

// saveToolResult enregistre un résultat complet et renvoie son id ("" en cas
// d'échec : l'appelant enverra alors le résultat entier dans le flux).
func saveToolResult(result string) string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	id := "tr_" + hex.EncodeToString(b[:])
	// Valeur = horodatage (16 hex, pour la purge) + résultat.
	val := strconv.FormatInt(time.Now().UnixNano(), 16)
	for len(val) < 16 {
		val = "0" + val
	}
	if err := putBytes(bkToolRes, id, []byte(val+result)); err != nil {
		return ""
	}
	if toolResWrites.Add(1)%toolResPruneGap == 0 {
		go pruneToolResults()
	}
	return id
}

// loadToolResult relit un résultat enregistré par saveToolResult.
func loadToolResult(id string) (string, bool) {
	v := getBytes(bkToolRes, id)
	if len(v) < 16 {
		return "", false
	}
	return string(v[16:]), true
}

// pruneToolResults ne garde que les toolResKeep résultats les plus récents.
func pruneToolResults() {
	_ = update(bkToolRes, func(b *bolt.Bucket) error {
		if b.Stats().KeyN <= toolResKeep {
			return nil
		}
		type kt struct{ k, t string }
		var all []kt
		_ = b.ForEach(func(k, v []byte) error {
			t := ""
			if len(v) >= 16 {
				t = string(v[:16])
			}
			all = append(all, kt{string(k), t})
			return nil
		})
		if len(all) <= toolResKeep {
			return nil
		}
		sort.Slice(all, func(i, j int) bool { return all[i].t < all[j].t })
		for _, e := range all[:len(all)-toolResKeep] {
			if err := b.Delete([]byte(e.k)); err != nil {
				return err
			}
		}
		return nil
	})
}
