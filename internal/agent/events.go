package agent

import (
	"encoding/json"
	"sync"
)

// event : ce qui est diffusé aux interfaces ouvertes (ordinateur, téléphone).
// Les champs vides sont omis.
type event struct {
	Type  string   `json:"type"`            // user, start, delta, tool, done, stopped, error, convs, memory, sleep, tasks, apps, embed
	Conv  string   `json:"conv,omitempty"`  // conversation concernée
	Text  string   `json:"text,omitempty"`  // message ou morceau de réponse
	TS    int64    `json:"ts,omitempty"`    // epoch ms
	Kind  string   `json:"kind,omitempty"`  // « task » : compte rendu d'une tâche
	Files []string `json:"files,omitempty"` // fichiers joints au message de l'utilisateur
	Name  string   `json:"name,omitempty"`  // outil en cours
	Label string   `json:"label,omitempty"` // cible de l'outil
	Desc  string   `json:"desc,omitempty"`  // ce que fait l'appel, écrit par le modèle
	Model string   `json:"model,omitempty"` // done : le modèle qui a répondu
	Used  int      `json:"used,omitempty"`  // usage : tokens du contexte
	Max   int      `json:"max,omitempty"`   // usage : taille de la fenêtre (0 : inconnue)
	Out   int      `json:"out,omitempty"`   // usage : tokens écrits par le modèle
	Error string   `json:"error,omitempty"`
	State string   `json:"state,omitempty"` // sommeil : start, end
}

var bus = struct {
	sync.Mutex
	subs map[chan []byte]struct{}
}{subs: map[chan []byte]struct{}{}}

// Subscribe abonne une interface aux événements (déjà encodés en JSON).
// cancel la désabonne.
func Subscribe() (events <-chan []byte, cancel func()) {
	ch := make(chan []byte, 512)
	bus.Lock()
	bus.subs[ch] = struct{}{}
	bus.Unlock()
	return ch, func() {
		bus.Lock()
		delete(bus.subs, ch)
		bus.Unlock()
	}
}

func emit(ev event) {
	raw, err := json.Marshal(ev)
	if err != nil {
		return
	}
	bus.Lock()
	defer bus.Unlock()
	for ch := range bus.subs {
		select {
		case ch <- raw:
		default: // interface trop lente : elle se resynchronisera en rechargeant l'état
		}
	}
}

// TasksChanged, AppsChanged et EmbedChanged préviennent les interfaces que les
// tâches, les applications ou le moteur local d'embeddings ont changé : elles
// relisent alors l'état concerné. Les paquets qui en ont besoin les reçoivent
// au démarrage, pour ne pas avoir à connaître celui-ci.
func TasksChanged() { emit(event{Type: "tasks"}) }
func AppsChanged()  { emit(event{Type: "apps"}) }
func EmbedChanged() { emit(event{Type: "embed"}); WakeSearchIndex() }
