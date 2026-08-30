package ajean

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

// chat_recall.go — mémoire longue de la conversation.
//
// Le compactage résume les vieux tours pour libérer la fenêtre, mais un résumé
// perd forcément du détail (un gros code, une longue page web). Plutôt que de le
// perdre pour de bon, on ARCHIVE chaque gros bloc AVANT de le compacter, sous un
// identifiant court et stable (r7, r42…). Le résumé cite l'id à la place du bloc
// (« code du jeu fourni → recall:r7 »), et le modèle peut le rappeler VERBATIM
// quand il en a besoin :
//
//	recall(id)            → ramène le bloc exact, tel qu'il était.
//	recall_search(query)  → retrouve un bloc (et son id) par mots-clés, même très
//	                        ancien et disparu du résumé courant.
//
// L'archive est GLOBALE (un compteur monotone via bbolt NextSequence), pas liée
// à une conversation : un id ne pointe jamais que vers un seul bloc, pour
// toujours. Elle vit sur disque (bucket bkRecall) donc survit aux redémarrages
// et au multi-appareils. Le contexte, lui, ne porte que les ids ENCORE cités par
// le résumé courant : sa taille reste plate même sur une conversation infinie,
// pendant que l'archive grossit tranquillement sur disque (bon marché).

const (
	// Un bloc du torse plus long que ça (en caractères) est archivé et remplacé
	// par son id. En dessous, le résumé le porte très bien tout seul : pas la peine
	// de créer un id pour trois lignes.
	recallArchiveMinLen = 800
	// Longueur de l'extrait de tête laissé au résumeur pour un bloc archivé : de
	// quoi le résumer fidèlement, le reste étant récupérable par recall.
	recallSummaryHeadLen = 400
	// Nombre de résultats par défaut / max pour recall_search.
	recallSearchDefault = 5
	recallSearchMax     = 20
)

// recallBlock est un bloc archivé, adressable par son id.
type recallBlock struct {
	ID      string `json:"id"`      // "r<seq>"
	Seq     uint64 `json:"seq"`     // séquence brute (tri, clé)
	Label   string `json:"label"`   // libellé court lisible (rôle + première ligne)
	Role    string `json:"role"`    // rôle d'origine (tool/user/assistant)
	Content string `json:"content"` // contenu VERBATIM
	Created int64  `json:"created"` // unix, pour un éventuel ménage futur
}

// recallKey encode une séquence en clé bbolt triable (big-endian).
func recallKey(seq uint64) []byte {
	k := make([]byte, 8)
	binary.BigEndian.PutUint64(k, seq)
	return k
}

// recallIDToSeq lit "r<n>" → n. Tolère un id sans préfixe et les espaces.
func recallIDToSeq(id string) (uint64, bool) {
	s := strings.TrimSpace(id)
	s = strings.TrimPrefix(s, "r")
	s = strings.TrimPrefix(s, "R")
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// archiveRecallBlock enregistre un bloc et renvoie son id ("r<seq>"). L'id est
// alloué par NextSequence dans la transaction d'écriture : monotone, sans
// collision, même si deux compactages se croisent. En cas d'échec d'écriture
// (base indisponible, ex. tests sans AjeanHome) on renvoie une erreur et
// l'appelant se rabat sur le compactage classique (troncature sans id).
func archiveRecallBlock(label, role, content string) (string, error) {
	var id string
	err := withDB(func(d *bolt.DB) error {
		return d.Update(func(tx *bolt.Tx) error {
			b, err := tx.CreateBucketIfNotExists([]byte(bkRecall))
			if err != nil {
				return err
			}
			seq, err := b.NextSequence()
			if err != nil {
				return err
			}
			blk := recallBlock{
				ID:      "r" + strconv.FormatUint(seq, 10),
				Seq:     seq,
				Label:   label,
				Role:    role,
				Content: content,
				Created: time.Now().Unix(),
			}
			id = blk.ID
			raw, err := json.Marshal(blk)
			if err != nil {
				return err
			}
			return b.Put(recallKey(seq), raw)
		})
	})
	if err != nil {
		return "", err
	}
	cacheBust(bkRecall)
	return id, nil
}

// recallGet ramène un bloc par son id. false si l'id est mal formé ou absent.
func recallGet(id string) (recallBlock, bool) {
	seq, ok := recallIDToSeq(id)
	if !ok {
		return recallBlock{}, false
	}
	var blk recallBlock
	found := false
	_ = view(bkRecall, func(b *bolt.Bucket) error {
		v := b.Get(recallKey(seq))
		if v == nil {
			return nil
		}
		found = json.Unmarshal(v, &blk) == nil
		return nil
	})
	return blk, found
}

// recallSearch cherche dans toute l'archive par recouvrement de mots-clés.
// Score = nombre de termes distincts de la requête présents dans (libellé +
// contenu), le libellé comptant double (un mot du titre est plus signifiant).
// Simple, lexical, zéro dépendance — l'issue de secours pour un bloc sorti du
// résumé courant. À égalité, le plus RÉCENT d'abord (seq décroissante).
func recallSearch(query string, limit int) []recallBlock {
	terms := recallTokens(query)
	if len(terms) == 0 {
		return nil
	}
	if limit <= 0 {
		limit = recallSearchDefault
	}
	if limit > recallSearchMax {
		limit = recallSearchMax
	}
	type scored struct {
		blk   recallBlock
		score int
	}
	var hits []scored
	_ = view(bkRecall, func(b *bolt.Bucket) error {
		return b.ForEach(func(_, v []byte) error {
			var blk recallBlock
			if json.Unmarshal(v, &blk) != nil {
				return nil
			}
			label := strings.ToLower(blk.Label)
			body := strings.ToLower(blk.Content)
			score := 0
			for t := range terms {
				if strings.Contains(label, t) {
					score += 2
				}
				if strings.Contains(body, t) {
					score++
				}
			}
			if score > 0 {
				hits = append(hits, scored{blk, score})
			}
			return nil
		})
	})
	sort.Slice(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].blk.Seq > hits[j].blk.Seq
	})
	if len(hits) > limit {
		hits = hits[:limit]
	}
	out := make([]recallBlock, len(hits))
	for i, h := range hits {
		out[i] = h.blk
	}
	return out
}

// recallTokens éclate une chaîne en un ensemble de mots-clés minuscules (>= 2
// caractères), en coupant sur tout ce qui n'est ni lettre ni chiffre.
func recallTokens(s string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, w := range strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r >= 'à' && r <= 'ÿ')
	}) {
		if len([]rune(w)) >= 2 {
			out[w] = struct{}{}
		}
	}
	return out
}

// recallLabel fabrique un libellé court et lisible pour un bloc, à partir de son
// rôle (préfixé du nom d'outil quand on le connaît) et de sa première ligne.
func recallLabel(role, toolName, content string) string {
	head := ""
	for _, line := range strings.Split(content, "\n") {
		if s := strings.TrimSpace(line); s != "" {
			head = s
			break
		}
	}
	if r := []rune(head); len(r) > 70 {
		head = strings.TrimSpace(string(r[:70])) + "…"
	}
	tag := role
	if role == "tool" && toolName != "" {
		tag = toolName
	}
	if head == "" {
		return tag
	}
	return tag + ": " + head
}

// toolRecall ramène un bloc archivé pour le modèle (outil recall).
func toolRecall(args map[string]any) string {
	id, _ := args["id"].(string)
	if strings.TrimSpace(id) == "" {
		return "[erreur] id manquant"
	}
	blk, ok := recallGet(id)
	if !ok {
		return fmt.Sprintf("[erreur] bloc %q introuvable — vérifie l'id, ou utilise recall_search pour le retrouver par mots-clés", id)
	}
	return fmt.Sprintf("[%s — %s, rappelé verbatim]\n%s", blk.ID, blk.Label, blk.Content)
}

// toolRecallSearch retrouve des blocs par mots-clés (outil recall_search).
func toolRecallSearch(args map[string]any) string {
	q, _ := args["query"].(string)
	if strings.TrimSpace(q) == "" {
		return "[erreur] query manquante"
	}
	lim := 0
	if v, ok := args["limit"].(float64); ok {
		lim = int(v)
	}
	hits := recallSearch(q, lim)
	if len(hits) == 0 {
		return "[aucun bloc archivé ne correspond]"
	}
	var b strings.Builder
	b.WriteString("Blocs trouvés (rappelle le bon avec recall(id)) :\n")
	for _, h := range hits {
		snippet := recallSnippet(h.Content, 160)
		fmt.Fprintf(&b, "- %s — %s\n  %s\n", h.ID, h.Label, snippet)
	}
	return strings.TrimRight(b.String(), "\n")
}

// recallSnippet renvoie un extrait d'une ligne, borné, pour l'aperçu de recherche.
func recallSnippet(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > n {
		return strings.TrimSpace(string(r[:n])) + "…"
	}
	return s
}

// recallTool / recallSearchTool : schémas annoncés au modèle en mode agent.
func recallTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "recall",
			Description: "Bring back the full, verbatim content of an earlier block that was archived when the conversation was compacted. Use the id shown as recall:rN in the compacted summary (e.g. r7).",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"id": map[string]any{"type": "string", "description": "The block id, e.g. \"r7\"."},
				},
				"required": []string{"id"},
			},
		},
	}
}

func recallSearchTool() Tool {
	return Tool{
		Type: "function",
		Function: ToolFunction{
			Name:        "recall_search",
			Description: "Search the archived earlier history (turns removed during compaction) by keywords, to find a block and its recall id when it is not listed in the current summary. Then use recall(id) to bring it back.",
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string", "description": "Keywords describing the block you're looking for."},
					"limit": map[string]any{"type": "integer", "description": fmt.Sprintf("Default %d, max %d", recallSearchDefault, recallSearchMax)},
				},
				"required": []string{"query"},
			},
		},
	}
}
