package agent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"ajean/internal/home"
	"ajean/internal/secrets"
)

// Le journal des actions : chaque outil qui agit est noté AVANT et APRÈS son
// exécution, en ajout seul. Après un échec, un arrêt ou une coupure, le modèle
// sait ainsi ce qui a déjà été fait et ne le refait pas à « réessaie ».

const (
	actionsMaxBytes  = 2 << 20
	actionResultChar = 240
)

type actionEntry struct {
	TS     int64  `json:"ts"`
	Turn   int64  `json:"turn"`
	Phase  string `json:"phase"` // begin, start, done, end
	Name   string `json:"name,omitempty"`
	Label  string `json:"label,omitempty"`
	Result string `json:"result,omitempty"`
}

var actionsMu sync.Mutex

func actionsFile(conv string) string {
	if conv == "" {
		conv = "tasks"
	}
	return home.State("actions", filepath.Base(conv)+".jsonl")
}

func logAction(conv string, e actionEntry) {
	e.TS = time.Now().UnixMilli()
	e.Label, e.Result = secrets.Redact(e.Label), secrets.Redact(clip(e.Result, actionResultChar))
	raw, err := json.Marshal(e)
	if err != nil {
		return
	}
	actionsMu.Lock()
	defer actionsMu.Unlock()
	path := actionsFile(conv)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	if st, err := os.Stat(path); err == nil && st.Size() > actionsMaxBytes {
		_ = os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(raw, '\n'))
	_ = f.Sync() // l'entrée doit survivre à une coupure juste après
}

func readActions(conv string) []actionEntry {
	actionsMu.Lock()
	defer actionsMu.Unlock()
	f, err := os.Open(actionsFile(conv))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []actionEntry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		var e actionEntry
		if json.Unmarshal(sc.Bytes(), &e) == nil {
			out = append(out, e)
		}
	}
	return out
}

// describeActions : ce qu'un tour a réellement fait, pour le modèle.
func describeActions(entries []actionEntry, turn int64) string {
	var lines []string
	open := map[string]int{}
	for _, e := range entries {
		if e.Turn != turn {
			continue
		}
		key := e.Name + "|" + e.Label
		switch e.Phase {
		case "start":
			open[key] = len(lines)
			lines = append(lines, "- "+strings.TrimSpace(e.Name+" "+e.Label)+" : résultat inconnu (coupé pendant l'exécution, vérifie avant de refaire)")
		case "done":
			if i, ok := open[key]; ok {
				lines[i] = "- " + strings.TrimSpace(e.Name+" "+e.Label) + " : " + e.Result
				delete(open, key)
			}
		}
	}
	if len(lines) == 0 {
		return ""
	}
	return "[Actions already carried out during this interrupted turn. Do not repeat them without checking:\n" + strings.Join(lines, "\n") + "]"
}

// interruptedNote : le dernier tour de cette conversation n'a jamais fini
// (programme coupé) ? Alors ce qu'il avait fait.
func interruptedNote(conv string) string {
	entries := readActions(conv)
	var last int64
	for _, e := range entries {
		switch e.Phase {
		case "begin":
			last = e.Turn
		case "end":
			if e.Turn == last {
				last = 0
			}
		}
	}
	if last == 0 {
		return ""
	}
	logAction(conv, actionEntry{Turn: last, Phase: "end"})
	return describeActions(entries, last)
}

// mutating : l'appel peut-il changer quelque chose ? Les lectures ne sont pas notées.
func (c call) mutating() bool {
	switch c.name {
	case "memory_search", "fiche_read", "ws_read", "ws_list":
		return false
	case "tools":
		a, _ := c.args["action"].(string)
		return a == "run" || a == "save" || a == "delete" || a == "restore"
	}
	return true
}

// journalLabel : la cible de l'appel et, pour un outil, ses paramètres.
func (c call) journalLabel() string {
	label := c.label()
	if args, ok := c.args["args"]; ok {
		if raw, err := json.Marshal(args); err == nil {
			label = fmt.Sprintf("%s %s", label, clip(string(raw), actionResultChar))
		}
	}
	return label
}
