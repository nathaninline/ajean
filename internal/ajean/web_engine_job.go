// web_engine_job.go — persistance du job d'installation du moteur.
//
// Le job vit en mémoire du process web. Au moindre redémarrage (mise à jour du
// binaire, crash, reboot) son état disparaîtrait : la page rechargée
// n'afficherait plus rien, sans dire si l'installation continue ou pas. Or elle
// ne continue pas, elle meurt avec le process. On écrit donc l'état sur disque
// pour pouvoir l'annoncer honnêtement.
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
func lcJobLogPath() string { return filepath.Join(AjeanHome(), "ajean-engine.log") }

// lcPersisted est la forme sur disque : l'entête du job, sans les lignes.
type lcPersisted struct {
	Action    string `json:"action"`
	Phase     string `json:"phase"`
	Running   bool   `json:"running"`
	Err       string `json:"error"`
	StartedAt int64  `json:"started_at"`
	EndedAt   int64  `json:"ended_at"`
}

var (
	lcLastSave    time.Time
	lcRestoreOnce sync.Once
)

// lcSave écrit l'état courant. Appelée sous lcMu. Les changements d'état (début,
// fin, échec) sont écrits tout de suite ; la progression est throttlée à 2 s.
func lcSave(force bool) {
	if lcCur == nil {
		return
	}
	if !force && time.Since(lcLastSave) < 2*time.Second {
		return
	}
	lcLastSave = time.Now()
	b, err := json.Marshal(lcPersisted{
		Action: lcCur.Action, Phase: lcCur.Phase,
		Running: lcCur.Running, Err: lcCur.Err,
		StartedAt: lcCur.StartedAt, EndedAt: lcCur.EndedAt,
	})
	if err == nil {
		_ = putBytes(bkState, "engine_job", b)
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
// tentative ratée était rechargée à CHAQUE démarrage (issue #37). Un job EN
// COURS n'est jamais effacé.
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
	_ = putBytes(bkState, "engine_job", nil)
	lcResetLog()
	return true
}

// lcRestore recharge le dernier job connu au démarrage du process. Un job noté
// « en cours » ne peut pas être le nôtre (on vient de démarrer) : il est mort
// avec le process précédent, on le marque interrompu plutôt que de le faire
// passer pour vivant. Si le moteur manque toujours, engineAutoInstall relance
// l'installation juste après.
func lcRestore() {
	// Job d'une version d'avant la 0.20 (compilation, moteur personnalisé,
	// ExLlama) : il ne correspond plus à rien, on l'oublie.
	_ = putBytes(bkState, "build_job", nil)
	_ = os.Remove(filepath.Join(AjeanHome(), "ajean-build.log"))

	var p lcPersisted
	if !getJSON(bkState, "engine_job", &p) || p.Action == "" {
		return
	}
	j := &lcJob{
		Action: p.Action, Phase: p.Phase,
		Running: false, Err: p.Err,
		StartedAt: p.StartedAt, EndedAt: p.EndedAt,
	}
	if p.Running {
		j.Err = "interrompu : AJEAN a redémarré pendant l'installation du moteur"
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
		j.lines = append(j.lines, "✗ interrompu : AJEAN a redémarré (l'installation ne survit pas au redémarrage)")
	}
	lcMu.Lock()
	if lcCur == nil {
		lcCur = j
	}
	lcMu.Unlock()
}
