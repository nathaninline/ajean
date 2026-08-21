// web_llamacpp_job.go — persistance du job llama.cpp (install / update /
// backend custom).
//
// Le job vivait UNIQUEMENT en mémoire du process web. Conséquence : au moindre
// redémarrage du service (mise à jour du binaire, crash, reboot) l'état
// disparaissait — la page rechargée n'affichait plus rien du tout, sans dire si
// la compilation continuait ou pas. Or elle ne continue pas : le build est un
// processus ENFANT, il meurt avec le service. On écrit donc l'état sur disque
// pour pouvoir l'annoncer honnêtement au lieu de laisser l'utilisateur devant
// un écran muet.
package ajean

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// L'ENTÊTE du job vit en base ; ses LIGNES restent un fichier, écrit en append.
// Une compilation en produit des milliers : les faire transiter par une
// transaction chacune coûterait un fsync par ligne.
func lcJobLogPath() string { return filepath.Join(AjeanHome(), "ajean-build.log") }

// lcPersisted est la forme sur disque : l'entête du job, sans les lignes (elles
// vivent dans le .log à côté, en append).
type lcPersisted struct {
	Action    string `json:"action"`
	Phase     string `json:"phase"`
	Compiled  int    `json:"compiled"`
	Running   bool   `json:"running"`
	Err       string `json:"error"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at"`
	OldCommit string `json:"old"`
	NewCommit string `json:"new"`
}

var (
	lcLastSave    time.Time
	lcRestoreOnce sync.Once
)

// lcSave écrit l'état courant. Appelée sous lcMu. Les changements d'état (début,
// fin, échec) sont écrits tout de suite ; la progression est throttlée à 2 s
// pour ne pas marteler le disque pendant une compilation (des milliers de
// lignes).
func lcSave(force bool) {
	if lcCur == nil {
		return
	}
	if !force && time.Since(lcLastSave) < 2*time.Second {
		return
	}
	lcLastSave = time.Now()
	b, err := json.Marshal(lcPersisted{
		Action: lcCur.Action, Phase: lcCur.Phase, Compiled: lcCur.Compiled,
		Running: lcCur.Running, Err: lcCur.Err,
		StartedAt: lcCur.StartedAt, EndedAt: lcCur.EndedAt,
		OldCommit: lcCur.OldCommit, NewCommit: lcCur.NewCommit,
	})
	if err == nil {
		_ = putBytes(bkState, "build_job", b)
	}
}

// lcSaveLine ajoute une ligne au journal sur disque (append, pas de réécriture).
func lcSaveLine(line string) {
	f, err := os.OpenFile(lcJobLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return
	}
	_, _ = f.WriteString(line + "\n")
	_ = f.Close()
}

// lcResetLog repart d'un journal vide au démarrage d'un nouveau job.
func lcResetLog() { _ = os.Remove(lcJobLogPath()) }

// lcDismiss efface un job TERMINÉ (l'entête persistée + le journal + l'état en
// mémoire). Sert au bouton « masquer » : sans ça, l'erreur en rouge d'une
// tentative ratée (ex. moteur personnalisé impossible à compiler sous Windows
// 10) était rechargée à CHAQUE démarrage et ne disparaissait jamais (issue #37).
// Un job EN COURS n'est jamais effacé — on ne masque pas une compilation vivante.
func lcDismiss() bool {
	lcMu.Lock()
	defer lcMu.Unlock()
	if lcCur == nil {
		return true
	}
	if lcCur.Running {
		return false
	}
	lcCur = nil
	_ = putBytes(bkState, "build_job", nil)
	lcResetLog()
	return true
}

// lcRestore recharge le dernier job connu au démarrage du process. Un job noté
// « en cours » ne peut pas être le nôtre (on vient de démarrer) : sa
// compilation est morte avec le process précédent, on le marque interrompu
// plutôt que de le faire passer pour vivant ou de l'effacer en silence.
func lcRestore() {
	var p lcPersisted
	if !getJSON(bkState, "build_job", &p) || p.Action == "" {
		return
	}
	j := &lcJob{
		Action: p.Action, Phase: p.Phase, Compiled: p.Compiled,
		Running: false, Err: p.Err,
		StartedAt: p.StartedAt, EndedAt: p.EndedAt,
		OldCommit: p.OldCommit, NewCommit: p.NewCommit,
	}
	if p.Running {
		j.Err = "interrompu : le service a redémarré pendant l'opération — relancez-la"
		j.Phase = "interrompu"
		j.EndedAt = time.Now().Unix()
	}
	if logb, err := os.ReadFile(lcJobLogPath()); err == nil {
		lines := strings.Split(strings.TrimRight(string(logb), "\n"), "\n")
		if len(lines) == 1 && lines[0] == "" {
			lines = nil
		}
		if len(lines) > lcMaxLines {
			j.base = len(lines) - lcMaxLines
			lines = lines[j.base:]
		}
		j.lines = lines
	}
	if p.Running {
		j.lines = append(j.lines, "✗ interrompu : le service a redémarré (la compilation ne survit pas au redémarrage)")
	}
	lcMu.Lock()
	if lcCur == nil {
		lcCur = j
	}
	lcMu.Unlock()
	if p.Running {
		lcMu.Lock()
		lcSave(true)
		lcMu.Unlock()
	}
}
