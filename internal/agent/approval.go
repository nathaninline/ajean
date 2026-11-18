package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"

	"ajean/internal/scripts"
	"ajean/internal/tasks"
)

// La validation de fin de réponse : tout ce que le modèle a créé, modifié ou
// supprimé parmi ses outils et ses tâches est présenté en une seule fois.

// ReviewItem : un changement à valider.
type ReviewItem struct {
	Type   string `json:"type"`   // tool, task
	Change string `json:"change"` // new, changed, deleted
	Name   string `json:"name"`
	Detail string `json:"detail"`
}

// Review : la liste à valider. ID change dès que la liste change.
type Review struct {
	ID    string       `json:"id"`
	Items []ReviewItem `json:"items"`
}

// ErrReviewChanged : la liste a changé depuis qu'elle a été affichée.
var ErrReviewChanged = errors.New("la liste à valider a changé : relis-la")

var reviewNote struct {
	sync.Mutex
	text string
}

func currentReview() *Review {
	var items []ReviewItem
	for _, c := range scripts.Changes() {
		items = append(items, ReviewItem{"tool", c.Kind, c.Name, c.Detail})
	}
	for _, c := range tasks.Changes() {
		items = append(items, ReviewItem{"task", c.Kind, c.Name, c.Detail})
	}
	if len(items) == 0 {
		return nil
	}
	raw, _ := json.Marshal(items)
	sum := sha256.Sum256(raw)
	return &Review{ID: hex.EncodeToString(sum[:8]), Items: items}
}

// PendingReview : ce qui attend, une fois le travail du modèle terminé.
func PendingReview() *Review {
	if busyAnywhere() {
		return nil // il modifie peut-être encore : on valide tout à la fin
	}
	return currentReview()
}

// AnswerReview applique la réponse de l'utilisateur à toute la liste.
func AnswerReview(id string, ok bool) error {
	r := PendingReview()
	if r == nil || r.ID != id {
		return ErrReviewChanged
	}
	var err error
	if ok {
		err = errors.Join(scripts.Accept(), tasks.Accept())
	} else {
		err = errors.Join(scripts.Reject(), tasks.Reject())
		reviewNote.Lock()
		reviewNote.text = "[The user refused your latest changes to tools and scheduled tasks: they were put back as before. Do not redo them without asking.]"
		reviewNote.Unlock()
	}
	emit(event{Type: "tasks"})
	emit(event{Type: "review"})
	return err
}

// takeReviewNote : ce que le modèle doit savoir d'un refus, une seule fois.
func takeReviewNote() string {
	reviewNote.Lock()
	defer reviewNote.Unlock()
	s := reviewNote.text
	reviewNote.text = ""
	return s
}
