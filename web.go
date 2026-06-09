package main

import (
	"bufio"
	"embed"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

//go:embed ui/index.html ui/marked.min.js
var uiFS embed.FS

// cmdWeb starts the HTTP server on the given port (default 8090).
func cmdWeb(args []string) error {
	port := 8090
	if len(args) > 0 && args[0] != "" {
		n, err := strconv.Atoi(args[0])
		if err != nil {
			return fmt.Errorf("port invalide: %s", args[0])
		}
		port = n
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", handleIndex)
	mux.HandleFunc("/marked.min.js", func(w http.ResponseWriter, r *http.Request) {
		b, _ := uiFS.ReadFile("ui/marked.min.js")
		w.Header().Set("Content-Type", "application/javascript")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(b)
	})
	mux.HandleFunc("/api/status", handleStatus)
	mux.HandleFunc("/api/vram", handleVram)
	mux.HandleFunc("/api/config", handleConfigEnv)
	mux.HandleFunc("/api/models", handleModels)
	mux.HandleFunc("/api/backends", handleBackends)
	mux.HandleFunc("/api/presets", handlePresets)
	mux.HandleFunc("/api/preset", handlePreset)
	mux.HandleFunc("/api/preset/save", handlePresetSave)
	mux.HandleFunc("/api/preset/delete", handlePresetDelete)
	mux.HandleFunc("/api/skills", handleSkills)
	mux.HandleFunc("/api/skills/toggle", handleSkillsToggle)
	mux.HandleFunc("/api/skill", handleSkill)
	mux.HandleFunc("/api/skill/save", handleSkillSave)
	mux.HandleFunc("/api/skill/delete", handleSkillDelete)
	mux.HandleFunc("/api/tools", handleTools)
	mux.HandleFunc("/api/tools/toggle", handleToolsToggle)
	mux.HandleFunc("/api/switch", handleSwitch)
	mux.HandleFunc("/api/start", svcHandler("start"))
	mux.HandleFunc("/api/stop", svcHandler("stop"))
	mux.HandleFunc("/api/restart", svcHandler("restart"))
	mux.HandleFunc("/api/bench", handleBench)
	mux.HandleFunc("/api/chat", handleChat)
	addr := fmt.Sprintf("0.0.0.0:%d", port)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		// Port occupé : on identifie le process qui le tient et on propose de
		// le terminer pour relancer à sa place.
		if !resolvePortConflict(port) {
			return err
		}
		if ln, err = net.Listen("tcp", addr); err != nil {
			return err
		}
	}
	fmt.Printf("[jean web] http://%s  (Ctrl-C pour arrêter)\n", addr)
	return http.Serve(ln, mux)
}

// resolvePortConflict identifies the process listening on `port`, asks the user
// whether to terminate it, and (on yes) kills it and waits for the port to free.
// Returns true if the caller should retry binding.
func resolvePortConflict(port int) bool {
	pid, name := pidOnPort(port)
	if pid == 0 {
		fmt.Printf("%s port %d déjà utilisé, mais le process n'a pas pu être identifié (essaie en root ?)\n", red("[err]"), port)
		return false
	}
	fmt.Printf("%s le port %d est déjà utilisé par %s (PID %d).\n", yellow("[!]"), port, bold(name), pid)
	fmt.Print(dim("    terminer ce process et relancer ? [Y/n] "))
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() && strings.HasPrefix(strings.ToLower(strings.TrimSpace(sc.Text())), "n") {
		fmt.Println(dim("    annulé."))
		return false
	}
	// SIGTERM d'abord, puis SIGKILL si le port ne se libère pas.
	_ = exec.Command("kill", "-TERM", strconv.Itoa(pid)).Run()
	for i := 0; i < 15; i++ {
		time.Sleep(200 * time.Millisecond)
		if p, _ := pidOnPort(port); p == 0 {
			fmt.Printf("%s process %d terminé, redémarrage…\n", green("[ok]"), pid)
			return true
		}
	}
	_ = exec.Command("kill", "-KILL", strconv.Itoa(pid)).Run()
	time.Sleep(500 * time.Millisecond)
	if p, _ := pidOnPort(port); p != 0 {
		fmt.Printf("%s impossible de libérer le port %d (PID %d toujours présent)\n", red("[err]"), port, p)
		return false
	}
	fmt.Printf("%s process %d terminé (forcé), redémarrage…\n", green("[ok]"), pid)
	return true
}

// pidOnPort returns the PID and command name of the process listening on the
// given TCP port, via `ss` (Linux) with an `lsof` fallback. Returns 0 if none
// is found or if the tools can't see it (e.g. owned by another user).
func pidOnPort(port int) (int, string) {
	redir := regexp.MustCompile(`pid=(\d+)`)
	if out, err := exec.Command("ss", "-ltnHp", fmt.Sprintf("sport = :%d", port)).Output(); err == nil {
		if m := redir.FindStringSubmatch(string(out)); m != nil {
			pid, _ := strconv.Atoi(m[1])
			return pid, processName(pid)
		}
	}
	if out, err := exec.Command("lsof", "-ti", fmt.Sprintf("tcp:%d", port), "-sTCP:LISTEN").Output(); err == nil {
		for _, line := range strings.Fields(string(out)) {
			if pid, err := strconv.Atoi(strings.TrimSpace(line)); err == nil {
				return pid, processName(pid)
			}
		}
	}
	return 0, ""
}

// processName returns a short command name for a PID, or "?" if unknown.
func processName(pid int) string {
	if b, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
		if n := strings.TrimSpace(string(b)); n != "" {
			return n
		}
	}
	if out, err := exec.Command("ps", "-o", "comm=", "-p", strconv.Itoa(pid)).Output(); err == nil {
		if n := strings.TrimSpace(string(out)); n != "" {
			return n
		}
	}
	return "?"
}

func handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	b, err := uiFS.ReadFile("ui/index.html")
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, max-age=0")
	w.Write(b)
}

func sendJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(v)
}

func handleStatus(w http.ResponseWriter, r *http.Request) {
	out, _ := exec.Command("systemctl", "is-active", serviceName()).Output()
	state := strings.TrimSpace(string(out))
	if state == "" {
		state = "unknown"
	}
	active := state == "active"
	health := false
	if active {
		health = healthCheck()
	}
	sendJSON(w, 200, map[string]any{
		"state":  state,
		"active": active,
		"health": health,
		"port":   LLMPort(),
	})
}

func handleVram(w http.ResponseWriter, r *http.Request) {
	out, err := exec.Command("nvidia-smi",
		"--query-gpu=name,memory.used,memory.total,utilization.gpu,temperature.gpu",
		"--format=csv,noheader,nounits").Output()
	gpus := []map[string]any{}
	if err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
			parts := strings.Split(line, ",")
			if len(parts) != 5 {
				continue
			}
			for i := range parts {
				parts[i] = strings.TrimSpace(parts[i])
			}
			used, _ := strconv.Atoi(parts[1])
			total, _ := strconv.Atoi(parts[2])
			util, _ := strconv.Atoi(parts[3])
			temp, _ := strconv.Atoi(parts[4])
			gpus = append(gpus, map[string]any{
				"name": parts[0], "used": used, "total": total, "util": util, "temp": temp,
			})
		}
	}
	sendJSON(w, 200, gpus)
}

func handleConfigEnv(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, ReadConfig())
}

// handleBackends scans JEAN_HOME/backends/<name>/ for a llama-server binary,
// trying common build subpaths (build/bin, build-sm120/bin, bin, .).
// Returns [{name, path}].
func handleBackends(w http.ResponseWriter, r *http.Request) {
	root := JeanHome() + "/backends"
	entries, err := os.ReadDir(root)
	if err != nil {
		sendJSON(w, 200, []map[string]any{})
		return
	}
	subpaths := []string{
		"build/bin/llama-server", "build-sm120/bin/llama-server",
		"build/llama-server", "bin/llama-server", "llama-server",
	}
	out := []map[string]any{}
	for _, e := range entries {
		// e can be a directory or a symlink to one; either is fine.
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		for _, sp := range subpaths {
			p := root + "/" + name + "/" + sp
			if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
				out = append(out, map[string]any{"name": name, "path": p})
				break
			}
		}
	}
	sendJSON(w, 200, out)
}

// handleModels lists *.gguf files in JEAN_HOME (size in bytes) for the preset
// editor's model picker.
func handleModels(w http.ResponseWriter, r *http.Request) {
	entries, err := os.ReadDir(JeanHome())
	if err != nil {
		sendJSON(w, 200, []map[string]any{})
		return
	}
	out := []map[string]any{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".gguf") {
			continue
		}
		info, _ := e.Info()
		size := int64(0)
		if info != nil {
			size = info.Size()
		}
		out = append(out, map[string]any{"name": e.Name(), "size": size})
	}
	sendJSON(w, 200, out)
}

func handlePresets(w http.ResponseWriter, r *http.Request) {
	list, err := ListPresets()
	if err != nil {
		sendJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	out := []map[string]any{}
	for _, p := range list {
		out = append(out, map[string]any{"name": p.Name, "active": p.Active})
	}
	sendJSON(w, 200, out)
}

func handlePreset(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		// new preset → seed from current config.env so users can tweak rather than start blank
		b, _ := os.ReadFile(confPath())
		sendJSON(w, 200, map[string]any{"name": "", "content": string(b)})
		return
	}
	content, err := ReadPreset(name)
	if err != nil {
		sendJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	sendJSON(w, 200, map[string]any{"name": name, "content": content})
}

type saveReq struct {
	Name    string `json:"name"`
	Old     string `json:"old"`
	Content string `json:"content"`
}

func handlePresetSave(w http.ResponseWriter, r *http.Request) {
	var req saveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := SavePreset(req.Name, req.Old, req.Content); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "name": req.Name})
}

func handlePresetDelete(w http.ResponseWriter, r *http.Request) {
	var req saveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := DeletePreset(req.Name); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

func handleSkills(w http.ResponseWriter, r *http.Request) {
	sk := ListSkills()
	out := []map[string]any{}
	for _, s := range sk {
		out = append(out, map[string]any{"name": s.Name, "desc": s.Desc})
	}
	sendJSON(w, 200, map[string]any{"enabled": skillsEnabled(), "skills": out})
}

func handleSkillsToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := setSkillsEnabled(req.On); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "enabled": skillsEnabled()})
}

func handleSkill(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	if name == "" {
		sendJSON(w, 200, map[string]any{"name": "", "content": "# nouveau skill\n\nDécris ici quand et comment l'IA doit utiliser ce skill.\n"})
		return
	}
	c := SkillContent(name)
	if c == "" {
		sendJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	sendJSON(w, 200, map[string]any{"name": name, "content": c})
}

func handleSkillSave(w http.ResponseWriter, r *http.Request) {
	var req saveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := SaveSkill(req.Name, req.Old, req.Content); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "name": req.Name})
}

func handleSkillDelete(w http.ResponseWriter, r *http.Request) {
	var req saveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if err := DeleteSkill(req.Name); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

func handleTools(w http.ResponseWriter, r *http.Request) {
	sendJSON(w, 200, map[string]any{"enabled": toolsEnabled()})
}

func handleToolsToggle(w http.ResponseWriter, r *http.Request) {
	var req struct {
		On bool `json:"on"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	if err := setToolsEnabled(req.On); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "enabled": toolsEnabled()})
}

func handleSwitch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		N int `json:"n"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	list, err := ListPresets()
	if err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if req.N < 1 || req.N > len(list) {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "index hors limites"})
		return
	}
	target := list[req.N-1]
	if err := SwitchToPreset(target.Path); err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "preset": target.Name})
}

// svcHandler returns an HTTP handler that triggers a systemctl action via the
// passwordless sudo rule installed by `jean install`. Falls back to no-sudo
// when the web server itself runs as root.
func svcHandler(action string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var cmd *exec.Cmd
		if os.Geteuid() == 0 {
			cmd = exec.Command("systemctl", action, serviceName())
		} else {
			cmd = exec.Command("sudo", "-n", "systemctl", action, serviceName())
		}
		out, err := cmd.CombinedOutput()
		sendJSON(w, 200, map[string]any{"ok": err == nil, "out": string(out)})
	}
}

// handleChat is the SSE proxy with tool-calling. The HTTP handler writes raw
// data: lines matching what the embedded JS expects (delta.content,
// delta.reasoning_content, delta.tool_used).
// handleBench runs `runBench` synchronously. Long enough (~30-60s) that we
// rely on the client side to show a spinner / disable the button.
func handleBench(w http.ResponseWriter, r *http.Request) {
	nPrompt, nPredict := 2000, 300
	if v := r.URL.Query().Get("prompt"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			nPrompt = parsed
		}
	}
	if v := r.URL.Query().Get("n"); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil && parsed > 0 {
			nPredict = parsed
		}
	}
	res, err := runBench(nPrompt, nPredict)
	if err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "result": res})
}

func handleChat(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Messages    []Message `json:"messages"`
		Temperature float64   `json:"temperature"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if body.Temperature == 0 {
		body.Temperature = 0.7
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	flusher, _ := w.(http.Flusher)
	emit := func(obj map[string]any) bool {
		b, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": obj}}})
		if _, err := w.Write([]byte("data: " + string(b) + "\n\n")); err != nil {
			return false
		}
		if flusher != nil {
			flusher.Flush()
		}
		return true
	}
	msgs := InjectSkills(body.Messages)
	_ = runChat(msgs, body.Temperature, func(ev StreamEvent) bool {
		if ev.Err != nil {
			emit(map[string]any{"error": ev.Err.Error()})
			return true
		}
		if ev.ToolUsed != nil {
			emit(map[string]any{"tool_used": map[string]any{"name": ev.ToolUsed.Name, "label": ev.ToolUsed.Label}})
			return true
		}
		if ev.Stats != nil {
			emit(map[string]any{"stats": ev.Stats})
			return true
		}
		if ev.Reasoning != "" {
			return emit(map[string]any{"reasoning_content": ev.Reasoning})
		}
		if ev.Content != "" {
			return emit(map[string]any{"content": ev.Content})
		}
		return true
	})
}

