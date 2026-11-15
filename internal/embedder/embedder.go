// Package embedder donne à AJEAN ses vecteurs de recherche par le sens, d'où
// qu'ils viennent : d'une API d'embeddings, ou du moteur LOCAL que ce paquet
// installe et fait tourner lui-même. Le moteur local est llama.cpp (son
// serveur, en version épinglée et vérifiée) avec un petit modèle d'embeddings
// pris sur Hugging Face ; il tourne sur le processeur, n'est lancé que quand on
// lui demande un vecteur et s'arrête de lui-même quand il ne sert plus.
//
//	embedder.go   l'état montré à l'interface, le choix entre API et moteur local
//	install.go    installation en arrière-plan, annulation, suppression
//	release.go    la version épinglée de llama.cpp et son installation
//	model.go      le modèle : adresse acceptée, rangement, préfixes
//	download.go   téléchargement repris après une coupure, empreinte vérifiée
//	archive.go    extraction du serveur et de ses bibliothèques
//	process.go    lancement à la demande, arrêt après inactivité
//	sys_*.go      ce qui dépend du système (fenêtre masquée, mémoire vive libre)
//
// Sur le disque, sous .ajean/embedder/ :
//
//	engine/<version>/   llama-server et ses bibliothèques
//	models/<nom>.gguf   le modèle (un seul à la fois)
//
// Un fichier « .part » à côté est un téléchargement interrompu : il reprend où
// il s'était arrêté.
package embedder

import (
	"context"
	"sync"
	"time"

	"ajean/internal/config"
	"ajean/internal/llm"
)

// State : où en est le moteur local.
type State string

const (
	Absent      State = "absent"      // rien d'installé
	Downloading State = "downloading" // installation en cours
	Ready       State = "ready"       // installé, à l'arrêt : il démarrera à la demande
	Running     State = "running"     // en marche
	Failed      State = "error"       // la dernière installation ou le dernier lancement a échoué
)

// Progress : l'avancement d'un téléchargement.
type Progress struct {
	What  string `json:"what"`  // « engine » ou « model »
	Done  int64  `json:"done"`  // octets reçus
	Total int64  `json:"total"` // 0 : taille inconnue
}

// Info : le moteur local tel que l'interface le voit.
type Info struct {
	State    State     `json:"state"`
	Model    string    `json:"model"` // fichier installé, ou en cours de téléchargement
	Size     int64     `json:"size"`  // octets du modèle (0 : inconnu)
	Progress *Progress `json:"progress"`
	Error    string    `json:"error"`
	Active   bool      `json:"active"` // la recherche par le sens passe par lui
}

// Changed est appelée à chaque changement d'état (l'interface se rafraîchit).
// Posée au démarrage du programme.
var Changed func()

func notify() {
	if Changed != nil {
		Changed()
	}
}

// st : l'état vivant du moteur local. Son verrou n'est jamais tenu pendant un
// téléchargement ni pendant l'attente du démarrage.
var st struct {
	sync.Mutex
	job     *job
	proc    *process
	err     string    // dernier échec, tant que rien ne l'a remplacé par un succès
	retryAt time.Time // pas de nouveau lancement avant : un moteur qui ne démarre pas n'est pas relancé à chaque recherche
}

// installed : le modèle en place, si le serveur l'est aussi.
func installed() (file string, size int64, ok bool) {
	file, size = installedModel()
	return file, size, file != "" && engineBinary() != ""
}

// Installed : le moteur local est-il en place, serveur et modèle ?
func Installed() bool {
	_, _, ok := installed()
	return ok
}

// Status : l'état du moteur local.
func Status() Info {
	file, size, ok := installed()
	local := config.Get().Embed.Provider == config.LocalEmbed
	st.Lock()
	defer st.Unlock()
	info := Info{Error: st.err, Active: ok && local}
	if ok {
		info.Model, info.Size = file, size
	}
	switch {
	case st.job != nil:
		p := st.job.progress
		info.State, info.Progress = Downloading, &p
		info.Model, info.Size = st.job.model.file, st.job.model.size
		if info.Size == 0 && p.What == stepModel {
			info.Size = p.Total
		}
	case st.err != "":
		info.State = Failed
	case !ok:
		info.State = Absent
	case st.proc != nil && st.proc.ready:
		info.State = Running
	default:
		info.State = Ready
	}
	return info
}

// Source : d'où viennent les vecteurs de la recherche par le sens.
type Source struct {
	// Space identifie l'espace des vecteurs : deux vecteurs ne se comparent que
	// s'ils en partagent un. En changer (autre modèle, API ou moteur local)
	// invalide ceux qui sont déjà calculés.
	Space string
	api   llm.Endpoint // vide : le moteur local
}

// Active : la source réglée par l'utilisateur. ok=false si la recherche par le
// sens est coupée, ou si le moteur local est choisi mais pas installé.
func Active() (src Source, ok bool) {
	c := config.Get()
	if c.Embed.Provider == config.LocalEmbed {
		file, _, ok := installed()
		return Source{Space: "local:" + file}, ok
	}
	ep := c.Embeddings()
	return Source{Space: ep.Model, api: ep}, ep.Ready()
}

// Embed : un vecteur par texte. query distingue une question d'un texte à
// retrouver : certains modèles locaux attendent une consigne différente pour
// l'une et pour l'autre. Une API reçoit les textes tels quels.
func (s Source) Embed(ctx context.Context, texts []string, query bool) ([][]float32, error) {
	if s.api.Ready() {
		return llm.Embed(ctx, s.api, texts)
	}
	return Embed(ctx, texts, query)
}
