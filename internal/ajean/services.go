package ajean

// Services : AJEAN ne gère plus de moteur, il pilote des services système qui
// exposent une API OpenAI. Un service est soit une commande dont AJEAN écrit
// l'unité pour l'OS (systemd, service Windows, agent launchd), soit une API
// sans processus local (pas de commande). AJEAN ne pilote que les unités qu'il
// a écrites. Le garde-fou est dans services_guard.go.

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// Service : définition stockée dans services.json.
type Service struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	// Command : la commande sur une ligne, comme tapée dans un terminal.
	// Exec/Args : forme ancienne (0.19), gardée pour la compatibilité.
	Command string   `json:"command,omitempty"`
	Exec    string   `json:"exec,omitempty"`
	Args    []string `json:"args,omitempty"`
	User    string   `json:"user,omitempty"`
	// NoRestart : ne pas relancer le service s'il s'arrête sur une erreur.
	NoRestart bool `json:"no_restart,omitempty"`
	// MemoryMaxGB : plafond de RAM du service (Linux, cgroup, sans swap).
	MemoryMaxGB float64 `json:"memory_max_gb,omitempty"`
	// AutostartWanted : démarrer avec la machine (Windows, macOS ; sous Linux
	// c'est l'état « enabled » de l'unité qui fait foi).
	AutostartWanted bool              `json:"autostart_wanted,omitempty"`
	Workdir         string            `json:"workdir,omitempty"`
	Env             map[string]string `json:"env,omitempty"`
	URL             string            `json:"url"`
	APIKey          string            `json:"api_key,omitempty"`
	Model           string            `json:"model,omitempty"`
	Vision          bool              `json:"vision,omitempty"`
	Context         int               `json:"context,omitempty"`
	Need            ServiceNeed       `json:"need,omitempty"`
	// Exclusive : démarrer ce service arrête les autres services exclusifs.
	Exclusive    bool `json:"exclusive,omitempty"`
	ReadyTimeout int  `json:"ready_timeout_s,omitempty"`
	// Preset d'origine (conversion) : son prompt système et son bench restent
	// rangés sous son id. ChatParams : TEMP, TOP_P… et REASONING_EFFORT.
	Preset     string            `json:"preset,omitempty"`
	ChatParams map[string]string `json:"chat_params,omitempty"`
	// Étiquettes de la liste : quantification et réflexion (vide = déduites).
	Quant     string `json:"quant,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
}

// ServiceNeed : besoin déclaré. VRAM par index de carte (ordre de nvidia-smi).
type ServiceNeed struct {
	RAMGB  float64            `json:"ram_gb,omitempty"`
	VRAMGB map[string]float64 `json:"vram_gb,omitempty"`
}

// ServiceStatus : état renvoyé au CLI et à l'UI.
type ServiceStatus struct {
	Service
	State     string         `json:"state"` // active, activating, inactive, failed, remote
	Autostart bool           `json:"autostart"`
	Form      *ServiceForm   `json:"form,omitempty"`
	Ready     bool           `json:"ready"`
	Chat      bool           `json:"chat"`
	Bench     map[string]any `json:"bench,omitempty"`
	Peak      ServiceNeed    `json:"peak,omitempty"`
	Message   string         `json:"message,omitempty"`
}

var serviceIDRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,40}$`)

func (s *Service) Managed() bool {
	return strings.TrimSpace(s.Command) != "" || strings.TrimSpace(s.Exec) != ""
}

// commandLine : la commande sur une ligne (Command, ou Exec/Args recollés).
func (s *Service) commandLine() string {
	if c := strings.TrimSpace(s.Command); c != "" {
		return c
	}
	if s.Exec == "" {
		return ""
	}
	parts := []string{shellQuote(s.Exec)}
	for _, a := range s.Args {
		parts = append(parts, shellQuote(a))
	}
	return strings.Join(parts, " ")
}

func shellQuote(a string) string {
	if a != "" && !strings.ContainsAny(a, " \t\"'\\;&|<>()$`*?![]{}#~") {
		return a
	}
	return "'" + strings.ReplaceAll(a, "'", `'"'"'`) + "'"
}

// deriveFromCommand complète l'adresse et la clé d'un service local à partir de
// sa commande (port, --api-key) : rien à déclarer à la main.
func (s *Service) deriveFromCommand(cmd, workdir string) {
	if strings.TrimSpace(s.URL) == "" {
		if p := portFromCommand(cmd, workdir); p != "" {
			s.URL = "http://127.0.0.1:" + p + "/v1"
		}
	}
	if s.APIKey == "" {
		s.APIKey = keyFromCommand(cmd)
	}
}
func (s *Service) Local() bool { return s.Managed() }
func (s *Service) readyTimeout() time.Duration {
	if s.ReadyTimeout > 0 {
		return time.Duration(s.ReadyTimeout) * time.Second
	}
	return 10 * time.Minute
}

// Validate vérifie la définition avant enregistrement.
func (s *Service) Validate() error {
	s.ID = strings.TrimSpace(s.ID)
	if !serviceIDRe.MatchString(s.ID) {
		return fmt.Errorf("identifiant invalide %q (minuscules, chiffres, tirets)", s.ID)
	}
	if strings.TrimSpace(s.Name) == "" {
		s.Name = s.ID
	}
	if s.Managed() {
		s.deriveFromCommand(s.commandLine(), s.Workdir)
	}
	// Un service local qui n'expose pas d'API (pas un modèle) n'a pas d'adresse.
	if strings.TrimSpace(s.URL) == "" && s.Managed() {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(s.URL))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("URL invalide %q (ex. http://127.0.0.1:8080/v1)", s.URL)
	}
	s.URL = strings.TrimRight(strings.TrimSpace(s.URL), "/")
	return nil
}

// fill complète l'adresse et la clé d'après la commande.
func (s *Service) fill() {
	if s.Managed() {
		s.deriveFromCommand(s.commandLine(), s.Workdir)
	}
}

// --- Stockage --------------------------------------------------------------

var servicesMu sync.Mutex

func servicesPath() string      { return filepath.Join(AjeanHome(), "services.json") }
func servicesStatePath() string { return filepath.Join(AjeanHome(), "services-state.json") }

func LoadServices() ([]Service, error) {
	b, err := os.ReadFile(servicesPath())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Service
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("services.json illisible : %w", err)
	}
	for i := range list {
		list[i].fill()
	}
	return list, nil
}

// saveServices garde l'ordre de la liste : c'est l'ordre d'affichage.
func saveServices(list []Service) error {
	return writeJSONAtomic(servicesPath(), list)
}

// SetServiceOrder range les services dans l'ordre donné (glisser-déposer de la
// liste) ; les ids absents gardent leur ordre relatif, à la fin.
func SetServiceOrder(ids []string) error {
	servicesMu.Lock()
	defer servicesMu.Unlock()
	list, err := LoadServices()
	if err != nil {
		return err
	}
	rank := map[string]int{}
	for i, id := range ids {
		rank[id] = i
	}
	sort.SliceStable(list, func(i, j int) bool {
		ri, okI := rank[list[i].ID]
		rj, okJ := rank[list[j].ID]
		if okI != okJ {
			return okI
		}
		return okI && ri < rj
	})
	return saveServices(list)
}

// serviceCmdTokens : la commande d'un modèle, découpée en mots. Lue là où elle
// se trouve : la ligne de commande du service, ou l'ancienne forme programme +
// arguments (modèles convertis depuis un preset).
func serviceCmdTokens(s *Service) []string {
	if c := strings.TrimSpace(s.Command); c != "" {
		return splitArgs(c)
	}
	if s.Exec != "" {
		return append([]string{s.Exec}, s.Args...)
	}
	return nil
}

// serviceModelFile : fichier du modèle dans la commande (-m / --model).
func serviceModelFile(s *Service) string {
	toks := serviceCmdTokens(s)
	for i, a := range toks {
		if (a == "-m" || a == "--model") && i+1 < len(toks) {
			return toks[i+1]
		}
		if strings.HasPrefix(a, "--model=") {
			return strings.TrimPrefix(a, "--model=")
		}
	}
	return ""
}

// serviceLlamaServer : chemin du llama-server que lance ce modèle ("" si c'est
// un autre programme).
func serviceLlamaServer(s *Service) string {
	for _, a := range serviceCmdTokens(s) {
		if strings.Contains(filepath.Base(a), "llama-server") {
			return a
		}
		if strings.HasPrefix(a, "-") {
			break // les options commencent : le programme est déjà passé
		}
	}
	return ""
}

func writeJSONAtomic(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func GetService(id string) (Service, error) {
	list, err := LoadServices()
	if err != nil {
		return Service{}, err
	}
	for _, s := range list {
		if s.ID == id {
			return s, nil
		}
	}
	return Service{}, fmt.Errorf("service inconnu : %s", id)
}

// PutService ajoute ou remplace une définition. L'unité de l'OS est réécrite au
// prochain démarrage (svcInstall est idempotent).
func PutService(s Service) error {
	if err := s.Validate(); err != nil {
		return err
	}
	servicesMu.Lock()
	defer servicesMu.Unlock()
	list, err := LoadServices()
	if err != nil {
		return err
	}
	for i := range list {
		if list[i].ID == s.ID {
			list[i] = s
			return saveServices(list)
		}
	}
	return saveServices(append(list, s))
}

// DeleteService arrête le service, retire son unité et la définition.
func DeleteService(id string) error {
	s, err := GetService(id)
	if err != nil {
		return err
	}
	if s.Managed() {
		_ = StopService(id)
		if err := osSvcUninstall(&s); err != nil {
			return err
		}
	}
	if s.Preset == "" {
		deletePresetSysPrompt(s.promptKey())
		deletePresetBench(s.promptKey())
	}
	servicesMu.Lock()
	defer servicesMu.Unlock()
	list, err := LoadServices()
	if err != nil {
		return err
	}
	out := list[:0]
	for _, x := range list {
		if x.ID != id {
			out = append(out, x)
		}
	}
	return saveServices(out)
}

// --- État mémorisé (pics mesurés, ordre de démarrage) ------------------------

type serviceRuntime struct {
	Peak      ServiceNeed `json:"peak"`
	StartedAt time.Time   `json:"started_at,omitempty"`
	LastError string      `json:"last_error,omitempty"`
}

var svcStateMu sync.Mutex

func loadServiceState() map[string]serviceRuntime {
	m := map[string]serviceRuntime{}
	if b, err := os.ReadFile(servicesStatePath()); err == nil {
		_ = json.Unmarshal(b, &m)
	}
	return m
}

func updateServiceState(id string, f func(*serviceRuntime)) {
	svcStateMu.Lock()
	defer svcStateMu.Unlock()
	m := loadServiceState()
	r := m[id]
	f(&r)
	m[id] = r
	_ = writeJSONAtomic(servicesStatePath(), m)
}

// --- Actions ---------------------------------------------------------------

// ServiceUnitName : nom de l'unité côté OS.
func ServiceUnitName(s *Service) string { return "ajean-svc-" + s.ID }

// StatusOf renvoie l'état courant (sans attendre).
func StatusOf(s Service) ServiceStatus {
	st := ServiceStatus{Service: s, Peak: loadServiceState()[s.ID].Peak}
	st.APIKey = ""
	st.Chat = ChatServiceID() == s.ID
	st.Bench = serviceBench(&s)
	if st.Quant == "" {
		if m := serviceModelFile(&s); m != "" {
			st.Quant = quantFromName(baseName(m))
		}
	}
	if !s.Local() {
		st.State = "remote"
	} else {
		st.State = osSvcState(&s)
		st.Autostart = osSvcAutostart(&s)
		if f, ok := ServiceFormOf(&s); ok {
			st.Form = &f
		}
	}
	if (st.State == "active" || st.State == "remote") && s.URL != "" {
		st.Ready = probeReady(&s, 2*time.Second) == nil
	}
	// Une ancienne erreur n'a plus de sens une fois le service prêt.
	if !st.Ready {
		st.Message = loadServiceState()[s.ID].LastError
		if st.Message == "" {
			st.Message = guardNote(&s)
		}
	}
	return st
}

func ListServiceStatus() ([]ServiceStatus, error) {
	list, err := LoadServices()
	if err != nil {
		return nil, err
	}
	out := make([]ServiceStatus, len(list))
	var wg sync.WaitGroup
	for i := range list {
		wg.Add(1)
		go func(i int) { defer wg.Done(); out[i] = StatusOf(list[i]) }(i)
	}
	wg.Wait()
	return out, nil
}

// StartOptions : progress reçoit des lignes lisibles (CLI, UI).
type StartOptions struct {
	Progress func(string)
	// Force : passe outre la vérification RAM/VRAM (le gardien reste actif).
	Force bool
	// Replace : si un autre service géré tient le port, l'arrêter (bascule).
	Replace bool
}

// PortConflict : le port du service est tenu par un autre service géré ;
// l'interface demande avant de basculer (StartOptions.Replace).
type PortConflict struct {
	Port    string `json:"port"`
	Owner   string `json:"conflict"`
	OwnerID string `json:"owner_id"`
}

func (e *PortConflict) Error() string {
	return fmt.Sprintf("le port %s est utilisé par %s", e.Port, e.Owner)
}

// StartFailure : échec immédiat, avec la raison lue dans le journal.
type StartFailure struct {
	Reason   string `json:"failed"`
	Detail   string `json:"detail"`
	Restored string `json:"restored"`
}

func (e *StartFailure) Error() string {
	m := e.Reason
	if e.Detail != "" {
		m += " (" + e.Detail + ")"
	}
	if e.Restored != "" {
		m += " " + e.Restored + " a été relancé."
	}
	return m
}

// serviceByUnit : le service géré qui correspond à une unité de l'OS.
func serviceByUnit(unit string) *Service {
	list, _ := LoadServices()
	for i := range list {
		if list[i].Local() && strings.TrimSuffix(ServiceUnitName(&list[i]), ".service") == strings.TrimSuffix(unit, ".service") {
			return &list[i]
		}
	}
	return nil
}

// StartService : libère le port si un autre service géré le
// tient (avec l'accord de l'utilisateur), démarre, attend un échec immédiat et
// remet alors l'ancien en service, puis suit le chargement jusqu'à ce que l'API
// réponde. Le démarrage automatique suit la bascule.
func StartService(id string, opt StartOptions) error {
	say := opt.Progress
	if say == nil {
		say = func(string) {}
	}
	s, err := GetService(id)
	if err != nil {
		return err
	}
	if !s.Local() {
		say("API distante : vérification")
		return probeReady(&s, 10*time.Second)
	}
	if osSvcState(&s) == "active" && (s.URL == "" || probeReady(&s, 2*time.Second) == nil) {
		say("déjà en marche")
		return nil
	}
	if s.Exclusive {
		if err := stopOtherExclusive(s.ID, say); err != nil {
			return err
		}
	}
	var replaced *Service
	moved := false
	if p := localPort(&s); p != "" && svcPortBusy(p) {
		unit, who := osPortOwner(p)
		if other := serviceByUnit(unit); other != nil && other.ID != s.ID {
			if !opt.Replace {
				return &PortConflict{Port: p, Owner: other.Name, OwnerID: other.ID}
			}
			say("arrêt de " + other.Name + " (port " + p + ")")
			if err := osSvcStop(other); err != nil {
				return fmt.Errorf("impossible d'arrêter %s : %w", other.Name, err)
			}
			replaced = other
			// Un seul des deux peut démarrer avec la machine sans conflit.
			if osSvcAutostart(other) && osSvcSetAutostart(other, false) == nil {
				moved = true
			}
			for i := 0; i < 20 && svcPortBusy(p); i++ {
				time.Sleep(500 * time.Millisecond)
			}
		} else if unit == "" || other == nil {
			if who == "" {
				who = "un autre programme"
			}
			return fmt.Errorf("le port %s est utilisé par %s, qu'AJEAN ne gère pas", p, who)
		}
	}
	restore := func() string {
		if replaced == nil {
			return ""
		}
		if moved {
			_ = osSvcSetAutostart(replaced, true)
		}
		if osSvcStart(replaced) == nil {
			return replaced.Name
		}
		return ""
	}
	if err := preflightService(&s, opt.Force); err != nil {
		updateServiceState(s.ID, func(r *serviceRuntime) { r.LastError = err.Error() })
		restore()
		return err
	}
	if err := osSvcInstall(&s); err != nil {
		restore()
		return fmt.Errorf("création du service : %w", err)
	}
	base := sampleResources()
	say("démarrage")
	if err := osSvcStart(&s); err != nil {
		restore()
		return fmt.Errorf("démarrage : %w", err)
	}
	started := time.Now()
	updateServiceState(s.ID, func(r *serviceRuntime) { r.StartedAt = started; r.LastError = "" })
	if !osSvcWaitOutcome(&s) {
		reason, detail := osSvcFailure(&s)
		if k, hit := strikeFor(GuardKills(), ServiceUnitName(&s), started, time.Now().Add(time.Minute)); hit {
			reason, detail = "Arrêté "+guardStrikeText(k)+".", ""
		}
		// Sans cela, il se relancerait en boucle et reprendrait le port.
		_ = osSvcStop(&s)
		f := &StartFailure{Reason: reason, Detail: detail, Restored: restore()}
		updateServiceState(s.ID, func(r *serviceRuntime) { r.LastError = f.Error() })
		return f
	}
	if moved {
		_ = osSvcSetAutostart(&s, true)
	}
	err = watchStartup(&s, base, say)
	if err != nil {
		updateServiceState(s.ID, func(r *serviceRuntime) { r.LastError = err.Error() })
	}
	return err
}

func StopService(id string) error {
	s, err := GetService(id)
	if err != nil {
		return err
	}
	if !s.Local() {
		return nil
	}
	return osSvcStop(&s)
}

func RestartService(id string, opt StartOptions) error {
	if err := StopService(id); err != nil {
		return err
	}
	return StartService(id, opt)
}

func ServiceLogs(id string, n int) (string, error) {
	s, err := GetService(id)
	if err != nil {
		return "", err
	}
	if !s.Local() {
		return "", nil
	}
	return osSvcLogTail(&s, n), nil
}

func stopOtherExclusive(id string, say func(string)) error {
	list, err := LoadServices()
	if err != nil {
		return err
	}
	for i := range list {
		o := &list[i]
		if o.ID == id || !o.Exclusive || !o.Local() {
			continue
		}
		if st := osSvcState(o); st == "active" || st == "activating" {
			say("arrêt de " + o.Name + " (exclusif)")
			if err := osSvcStop(o); err != nil {
				return fmt.Errorf("arrêt de %s : %w", o.ID, err)
			}
		}
	}
	return nil
}

// probeReady : GET <url>/models doit répondre 200.
func probeReady(s *Service, timeout time.Duration) error {
	req, err := http.NewRequest("GET", s.URL+"/models", nil)
	if err != nil {
		return err
	}
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}
	resp, err := (&http.Client{Timeout: timeout}).Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("%s/models : HTTP %d", s.URL, resp.StatusCode)
	}
	return nil
}

// localPort : port de l'URL si elle pointe sur cette machine, sinon "".
func localPort(s *Service) string {
	u, err := url.Parse(s.URL)
	if err != nil {
		return ""
	}
	h := u.Hostname()
	if h != "127.0.0.1" && h != "localhost" && h != "::1" && h != "0.0.0.0" {
		return ""
	}
	if p := u.Port(); p != "" {
		return p
	}
	if u.Scheme == "https" {
		return "443"
	}
	return "80"
}

func svcPortBusy(port string) bool {
	c, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), 300*time.Millisecond)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func svcIsFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

// osSvcStateOf : état sans attendre ("remote" pour une API distante).
func osSvcStateOf(s *Service) string {
	if !s.Local() {
		return "remote"
	}
	return osSvcState(s)
}

// ServiceFormOf : les champs modifiables d'un service local.
func ServiceFormOf(s *Service) (ServiceForm, bool) {
	if !s.Managed() {
		return ServiceForm{}, false
	}
	return ServiceForm{Command: s.commandLine(), WorkDir: s.Workdir, User: s.User, Description: s.Name, Restart: !s.NoRestart}, true
}

// SaveServiceForm enregistre le formulaire ; un service en marche est relancé
// pour que la modification prenne effet.
func SaveServiceForm(id string, f ServiceForm) error {
	if err := f.Validate(); err != nil {
		return err
	}
	s, err := GetService(id)
	if err != nil {
		return err
	}
	old, ok := ServiceFormOf(&s)
	if !ok {
		return fmt.Errorf("ce service n'a pas de commande à modifier")
	}
	if old == f {
		return nil
	}
	s.Command, s.Exec, s.Args = f.Command, "", nil
	s.Workdir, s.User, s.NoRestart = f.WorkDir, f.User, !f.Restart
	if f.Description != "" {
		s.Name = f.Description
	}
	if old.Command != f.Command {
		s.URL, s.APIKey = "", "" // relus dans la nouvelle commande
	}
	if err := PutService(s); err != nil {
		return err
	}
	s, _ = GetService(id)
	if err := osSvcInstall(&s); err != nil {
		return err
	}
	if st := osSvcState(&s); st == "active" || st == "activating" {
		if err := osSvcStop(&s); err != nil {
			return fmt.Errorf("enregistré, mais la relance a échoué : %w", err)
		}
		if err := osSvcStart(&s); err != nil {
			return fmt.Errorf("enregistré, mais la relance a échoué : %w", err)
		}
	}
	return nil
}

// SetServiceAutostart : démarrer (ou non) le service avec la machine.
func SetServiceAutostart(id string, on bool) error {
	s, err := GetService(id)
	if err != nil {
		return err
	}
	if !s.Local() {
		return fmt.Errorf("une API distante ne démarre pas avec la machine")
	}
	if on {
		if err := osSvcInstall(&s); err != nil {
			return err
		}
	}
	return osSvcSetAutostart(&s, on)
}

// ServiceWhy : pourquoi le service s'est arrêté (dernier lancement).
func ServiceWhy(id string) (reason, detail string, err error) {
	s, err := GetService(id)
	if err != nil {
		return "", "", err
	}
	if !s.Local() {
		return "", "", nil
	}
	// Un modèle qui tourne n'a pas de raison d'arrêt à donner.
	if st := osSvcState(&s); st == "active" || st == "activating" {
		return "Le service est en marche.", "", nil
	}
	reason, detail = osSvcFailure(&s)
	if k, ok := guardKillOf(&s); ok {
		reason = "Arrêté " + guardStrikeText(k) + "."
	}
	return reason, detail, nil
}
