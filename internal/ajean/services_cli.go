package ajean

// CLI `ajean service …` et API /api/services.

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

func cmdService(args []string) error {
	// --home : passé par l'enveloppe Windows et l'élévation UAC, qui ne voient
	// pas l'environnement de l'utilisateur.
	rest := args[:0:0]
	for i := 0; i < len(args); i++ {
		if args[i] == "--home" && i+1 < len(args) {
			os.Setenv("AJEAN_HOME", args[i+1])
			i++
			continue
		}
		rest = append(rest, args[i])
	}
	args = rest
	if len(args) == 0 {
		args = []string{"list"}
	}
	sub, args := args[0], args[1:]
	need := func() (string, error) {
		if len(args) < 1 {
			return "", fmt.Errorf("usage : ajean service %s <id>", sub)
		}
		return args[0], nil
	}
	switch sub {
	case "list", "ls":
		list, err := ListServiceStatus()
		if err != nil {
			return err
		}
		if len(list) == 0 {
			fmt.Println("Aucun service. Ajouter : ajean service add <fichier.json>")
			return nil
		}
		for _, s := range list {
			ready := ""
			if s.Ready {
				ready = " prêt"
			}
			if s.Chat {
				ready += " (chat)"
			}
			fmt.Printf("%-20s %-11s%s  %s\n", s.ID, s.State, ready, s.URL)
			if s.Message != "" {
				first, _, _ := strings.Cut(s.Message, "\n")
				fmt.Printf("%20s %s\n", "", first)
			}
		}
		return nil
	case "show":
		id, err := need()
		if err != nil {
			return err
		}
		s, err := GetService(id)
		if err != nil {
			return err
		}
		b, _ := json.MarshalIndent(s, "", "  ")
		fmt.Println(string(b))
		return nil
	case "add":
		var r io.Reader = os.Stdin
		if len(args) > 0 && args[0] != "-" {
			f, err := os.Open(args[0])
			if err != nil {
				return err
			}
			defer f.Close()
			r = f
		}
		var s Service
		if err := json.NewDecoder(r).Decode(&s); err != nil {
			return fmt.Errorf("JSON invalide : %w", err)
		}
		if err := PutService(s); err != nil {
			return err
		}
		fmt.Println(green("[ok]") + " service " + s.ID + " enregistré")
		return nil
	case "rm", "remove":
		id, err := need()
		if err != nil {
			return err
		}
		return DeleteService(id)
	case "start", "restart":
		id, err := need()
		if err != nil {
			return err
		}
		opt := StartOptions{Progress: func(l string) { fmt.Println("  " + l) }}
		for _, a := range args[1:] {
			if a == "--force" {
				opt.Force = true
			}
		}
		if sub == "restart" {
			err = RestartService(id, opt)
		} else {
			err = StartService(id, opt)
		}
		if err != nil {
			return err
		}
		fmt.Println(green("[ok]") + " " + id + " prêt")
		return nil
	case "autostart": // ajean service autostart <id> on|off
		if len(args) < 2 {
			return fmt.Errorf("usage : ajean service autostart <id> on|off")
		}
		return SetServiceAutostart(args[0], args[1] == "on")
	case "why":
		id, err := need()
		if err != nil {
			return err
		}
		reason, detail, err := ServiceWhy(id)
		if err != nil {
			return err
		}
		fmt.Println(reason)
		if detail != "" {
			fmt.Println("  " + detail)
		}
		return nil
	case "use":
		id, err := need()
		if err != nil {
			return err
		}
		if err := StartAndUse(id, StartOptions{Progress: func(l string) { fmt.Println("  " + l) }}); err != nil {
			return err
		}
		fmt.Println(green("[ok]") + " le chat utilise " + id)
		return nil
	case "stop":
		id, err := need()
		if err != nil {
			return err
		}
		return StopService(id)
	case "logs":
		id, err := need()
		if err != nil {
			return err
		}
		n := 100
		if len(args) > 1 {
			if v, e := strconv.Atoi(args[1]); e == nil {
				n = v
			}
		}
		out, err := ServiceLogs(id, n)
		if err != nil {
			return err
		}
		fmt.Println(out)
		return nil
	case "convert":
		dry := len(args) > 0 && args[0] == "--dry-run"
		res, err := ConvertPresets(dry)
		if err != nil {
			return err
		}
		printConvert(res, dry)
		return nil
	case "host": // interne : enveloppe de service Windows
		return cmdServiceHost(args)
	case "install": // interne : création de l'unité, relancé en admin sous Windows
		id, err := need()
		if err != nil {
			return err
		}
		s, err := GetService(id)
		if err != nil {
			return err
		}
		return osSvcInstallElevatedOrDirect(&s)
	case "uninstall-unit": // interne
		id, err := need()
		if err != nil {
			return err
		}
		s, err := GetService(id)
		if err != nil {
			return err
		}
		return osSvcUninstall(&s)
	}
	return fmt.Errorf(`sous-commande inconnue %q
usage : ajean service list | show <id> | add <fichier.json> | rm <id>
                      start <id> [--force] | stop <id> | restart <id> | logs <id> [n]
                      use <id>   démarrer si besoin et brancher le chat dessus
                      autostart <id> on|off | why <id>
                      convert [--dry-run]   créer un modèle par ancien preset`, sub)
}

// --- API -----------------------------------------------------------------

type svcJob struct {
	Lines []string `json:"lines"`
	Done  bool     `json:"done"`
	Error string   `json:"error,omitempty"`
	// Conflict : le port est tenu par un autre service (l'UI propose la bascule).
	Conflict *PortConflict `json:"conflict,omitempty"`
	// Failure : échec immédiat, raison lue dans le journal.
	Failure *StartFailure `json:"failure,omitempty"`
	At      time.Time     `json:"-"`
}

var (
	svcJobsMu sync.Mutex
	svcJobs   = map[string]*svcJob{}
)

func registerServiceRoutes(api func(string, http.HandlerFunc)) {
	StartServiceWatchdog()
	go func() {
		// 0.18 → 0.19 : l'ancien moteur libère le port et la VRAM AVANT que la
		// conversion ne rebranche le chat sur son service.
		retireLegacyEngine()
		AutoConvertPresets()
		legacyAutostartLoop()
	}()
	api("/api/services", handleServices)
	api("/api/services/start", handleServiceStart)
	api("/api/services/stop", handleServiceStop)
	api("/api/services/progress", handleServiceProgress)
	api("/api/services/logs", handleServiceLogs)
	api("/api/services/test", handleServiceTest)
	api("/api/services/convert", handleServiceConvert)
	api("/api/services/sysprompt", handleServiceSysPrompt)
	api("/api/services/use", handleServiceUse)
	api("/api/services/order", handleServiceOrder)
	api("/api/services/form", handleServiceForm)
	api("/api/services/autostart", handleServiceAutostart)
	api("/api/services/why", handleServiceWhy)
	api("/api/services/guard", handleServiceGuard)
}

func svcJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func svcErr(w http.ResponseWriter, err error) {
	svcJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
}

func handleServices(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := ListServiceStatus()
		if err != nil {
			svcErr(w, err)
			return
		}
		if list == nil {
			list = []ServiceStatus{}
		}
		svcJSON(w, 200, list)
	case http.MethodPost:
		var s Service
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&s); err != nil {
			svcErr(w, err)
			return
		}
		// Clé vide à l'édition : on garde l'ancienne (jamais renvoyée au client).
		if old, err := GetService(s.ID); err == nil {
			if s.APIKey == "" {
				s.APIKey = old.APIKey
			}
			if s.Preset == "" {
				s.Preset = old.Preset
			}
			if s.ChatParams == nil {
				s.ChatParams = old.ChatParams
			}
		}
		if err := PutService(s); err != nil {
			svcErr(w, err)
			return
		}
		svcJSON(w, 200, map[string]any{"ok": true, "id": s.ID})
	case http.MethodDelete:
		if err := DeleteService(r.URL.Query().Get("id")); err != nil {
			svcErr(w, err)
			return
		}
		svcJSON(w, 200, map[string]any{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

// handleServiceStart lance le démarrage en fond ; suivi par /progress.
func handleServiceStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	id := r.URL.Query().Get("id")
	if _, err := GetService(id); err != nil {
		svcErr(w, err)
		return
	}
	svcJobsMu.Lock()
	if j := svcJobs[id]; j != nil && !j.Done {
		svcJobsMu.Unlock()
		svcJSON(w, 200, map[string]any{"ok": true, "running": true})
		return
	}
	job := &svcJob{At: time.Now()}
	svcJobs[id] = job
	svcJobsMu.Unlock()
	opt := StartOptions{Force: r.URL.Query().Get("force") == "1", Replace: r.URL.Query().Get("replace") == "1", Progress: func(l string) {
		svcJobsMu.Lock()
		job.Lines = append(job.Lines, l)
		svcJobsMu.Unlock()
	}}
	restart := r.URL.Query().Get("restart") == "1"
	use := r.URL.Query().Get("use") == "1"
	go func() {
		var err error
		switch {
		case restart:
			err = RestartService(id, opt)
		case use:
			err = StartAndUse(id, opt)
		default:
			err = StartService(id, opt)
		}
		svcJobsMu.Lock()
		job.Done = true
		if err != nil {
			job.Error = err.Error()
			var pc *PortConflict
			var sf *StartFailure
			if errors.As(err, &pc) {
				job.Conflict = pc
			}
			if errors.As(err, &sf) {
				job.Failure = sf
			}
		}
		svcJobsMu.Unlock()
	}()
	svcJSON(w, 200, map[string]any{"ok": true})
}

func handleServiceProgress(w http.ResponseWriter, r *http.Request) {
	svcJobsMu.Lock()
	defer svcJobsMu.Unlock()
	j := svcJobs[r.URL.Query().Get("id")]
	if j == nil {
		svcJSON(w, 200, svcJob{Done: true})
		return
	}
	svcJSON(w, 200, j)
}

func handleServiceStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := StopService(r.URL.Query().Get("id")); err != nil {
		svcErr(w, err)
		return
	}
	svcJSON(w, 200, map[string]any{"ok": true})
}

func handleServiceLogs(w http.ResponseWriter, r *http.Request) {
	n, _ := strconv.Atoi(r.URL.Query().Get("n"))
	if n <= 0 || n > 2000 {
		n = 200
	}
	out, err := ServiceLogs(r.URL.Query().Get("id"), n)
	if err != nil {
		svcErr(w, err)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = io.WriteString(w, strings.TrimSpace(out)+"\n")
}

// handleServiceTest : la définition envoyée (pas encore enregistrée) répond-elle ?
func handleServiceTest(w http.ResponseWriter, r *http.Request) {
	var s Service
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&s); err != nil {
		svcErr(w, err)
		return
	}
	if s.APIKey == "" {
		if old, err := GetService(s.ID); err == nil {
			s.APIKey = old.APIKey
		}
	}
	s.URL = strings.TrimRight(strings.TrimSpace(s.URL), "/")
	if err := probeReady(&s, 8*time.Second); err != nil {
		svcErr(w, err)
		return
	}
	svcJSON(w, 200, map[string]any{"ok": true, "model": firstModelID(&s)})
}

// handleServiceUse réapplique la config du chat (réglages modifiés) sans démarrer.
func handleServiceUse(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := UseServiceForChat(r.URL.Query().Get("id")); err != nil {
		svcErr(w, err)
		return
	}
	svcJSON(w, 200, map[string]any{"ok": true})
}

// handleServiceOrder : POST {ids} (glisser-déposer de la liste).
func handleServiceOrder(w http.ResponseWriter, r *http.Request) {
	var body struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		svcErr(w, err)
		return
	}
	if err := SetServiceOrder(body.IDs); err != nil {
		svcErr(w, err)
		return
	}
	svcJSON(w, 200, map[string]any{"ok": true})
}

// handleServiceForm : GET le formulaire d'un service local, POST l'enregistre.
func handleServiceForm(w http.ResponseWriter, r *http.Request) {
	id := r.URL.Query().Get("id")
	switch r.Method {
	case http.MethodGet:
		s, err := GetService(id)
		if err != nil {
			svcErr(w, err)
			return
		}
		f, ok := ServiceFormOf(&s)
		if !ok {
			svcErr(w, errors.New("ce service n'a pas de commande"))
			return
		}
		svcJSON(w, 200, f)
	case http.MethodPost:
		var f ServiceForm
		if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&f); err != nil {
			svcErr(w, err)
			return
		}
		if err := SaveServiceForm(id, f); err != nil {
			svcErr(w, err)
			return
		}
		svcJSON(w, 200, map[string]any{"ok": true})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func handleServiceAutostart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := SetServiceAutostart(r.URL.Query().Get("id"), r.URL.Query().Get("on") == "1"); err != nil {
		svcErr(w, err)
		return
	}
	svcJSON(w, 200, map[string]any{"ok": true})
}

func handleServiceWhy(w http.ResponseWriter, r *http.Request) {
	reason, detail, err := ServiceWhy(r.URL.Query().Get("id"))
	if err != nil {
		svcErr(w, err)
		return
	}
	svcJSON(w, 200, map[string]string{"reason": reason, "detail": detail})
}

func handleServiceGuard(w http.ResponseWriter, r *http.Request) {
	k := GuardKills()
	if k == nil {
		k = []GuardKill{}
	}
	svcJSON(w, 200, map[string]any{"active": osHasRootGuard(), "kills": k})
}
