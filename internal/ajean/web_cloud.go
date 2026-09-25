package ajean

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

// web_cloud.go : comptes Modal (profils de la CLI) et crédit, pour l'éditeur de
// preset « GPU cloud ». Un compte s'ajoute par le flux web de Modal (`modal token
// new`) : AJEAN renvoie le lien, l'utilisateur se connecte dans son navigateur,
// la CLI enregistre le jeton elle-même. Aucune clé ne transite par l'UI.

var modalProfileRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)

// GET /api/cloud/accounts : profils Modal connus sur cette machine.
func handleCloudAccounts(w http.ResponseWriter, r *http.Request) {
	cmd, err := modalCmdFor("", "profile", "list", "--json")
	if err != nil {
		st, step, e := cloudRuntimeStatus()
		sendJSON(w, 200, map[string]any{"installed": false, "runtime": st, "step": step, "error": e, "accounts": []any{}})
		return
	}
	out, err := cmd.Output()
	var list []map[string]any
	if err != nil || json.Unmarshal(out, &list) != nil {
		sendJSON(w, 200, map[string]any{"installed": true, "accounts": []any{}})
		return
	}
	sendJSON(w, 200, map[string]any{"installed": true, "accounts": list})
}

// Connexions en cours, par nom de profil.
var cloudLogins = struct {
	sync.Mutex
	m map[string]*cloudLogin
}{m: map[string]*cloudLogin{}}

type cloudLogin struct {
	URL   string `json:"url"`
	State string `json:"state"` // pending | ok | error
	Error string `json:"error,omitempty"`
}

var tokenFlowRe = regexp.MustCompile(`https://\S*modal\.com/token-flow/\S+`)

// POST /api/cloud/accounts/add {name} : lance `modal token new --profile name`,
// renvoie le lien de connexion. GET ?name= : état de la connexion.
func handleCloudAccountAdd(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		cloudLogins.Lock()
		l := cloudLogins.m[r.URL.Query().Get("name")]
		cloudLogins.Unlock()
		if l == nil {
			sendJSON(w, 404, map[string]any{"error": "aucune connexion en cours"})
			return
		}
		sendJSON(w, 200, l)
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	_ = json.NewDecoder(r.Body).Decode(&req)
	name := strings.TrimSpace(req.Name)
	if !modalProfileRe.MatchString(name) {
		sendJSON(w, 400, map[string]any{"error": "nom invalide : lettres, chiffres, - et _ seulement"})
		return
	}
	cmd, err := modalCmdFor("", "token", "new", "--profile", name, "--no-activate")
	if err != nil {
		sendJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	// Pas de navigateur ouvert sur la machine AJEAN : le lien part vers l'UI,
	// qui peut être sur un autre appareil.
	cmd.Env = append(cmd.Env, "BROWSER=none")
	stdout, _ := cmd.StdoutPipe()
	cmd.Stderr = cmd.Stdout
	if err := cmd.Start(); err != nil {
		sendJSON(w, 500, map[string]any{"error": err.Error()})
		return
	}
	l := &cloudLogin{State: "pending"}
	cloudLogins.Lock()
	cloudLogins.m[name] = l
	cloudLogins.Unlock()
	urlCh := make(chan string, 1)
	go func() {
		sc := bufio.NewScanner(stdout)
		var tail []string
		for sc.Scan() {
			line := sc.Text()
			tail = append(tail, line)
			if u := tokenFlowRe.FindString(line); u != "" {
				select {
				case urlCh <- u:
				default:
				}
			}
		}
		err := cmd.Wait()
		cloudLogins.Lock()
		defer cloudLogins.Unlock()
		if err != nil {
			l.State, l.Error = "error", strings.TrimSpace(strings.Join(tail, " "))
		} else {
			l.State = "ok"
		}
	}()
	// Le flux expire côté Modal ; on ne laisse pas un process traîner indéfiniment.
	go func() {
		time.Sleep(15 * time.Minute)
		_ = cmd.Process.Kill() // sans effet si déjà terminé
	}()
	select {
	case u := <-urlCh:
		cloudLogins.Lock()
		l.URL = u
		cloudLogins.Unlock()
		sendJSON(w, 200, map[string]any{"ok": true, "url": u})
	case <-time.After(30 * time.Second):
		sendJSON(w, 500, map[string]any{"error": "Modal n'a pas renvoyé de lien de connexion"})
	}
}

// ---- Crédit ----------------------------------------------------------------

// Offre gratuite Starter de Modal : 30 $ de crédit par mois.
const modalStarterCredit = 30.0

var cloudBills = struct {
	sync.Mutex
	m map[string]*cloudBill
}{m: map[string]*cloudBill{}}

type cloudBill struct {
	at      time.Time
	loading bool
	data    map[string]any
}

// cloudBillingCached : dernier résumé connu du profil (nil si jamais mesuré),
// rafraîchi en tâche de fond au plus toutes les 5 min.
func cloudBillingCached(profile string) map[string]any {
	cloudBills.Lock()
	defer cloudBills.Unlock()
	b := cloudBills.m[profile]
	if b == nil {
		b = &cloudBill{}
		cloudBills.m[profile] = b
	}
	if !b.loading && time.Since(b.at) > 5*time.Minute {
		b.loading = true
		go func() {
			d, _ := fetchCloudBilling(profile)
			cloudBills.Lock()
			b.loading, b.at = false, time.Now()
			if d != nil {
				b.data = d
			}
			cloudBills.Unlock()
		}()
	}
	return b.data
}

// fetchCloudBilling lit le résumé du mois. En cas d'échec, l'erreur porte la
// raison donnée par Modal (droits, compte, réseau…) pour que l'UI l'affiche au
// lieu d'un « indisponible » muet.
func fetchCloudBilling(profile string) (map[string]any, error) {
	cmd, err := modalCmdFor(profile, "billing", "summary", "--json")
	if err != nil {
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("%s", modalErrorText(stderr.String()+string(out)))
	}
	var s struct {
		Metered     string            `json:"metered_cost"`
		Billed      string            `json:"billed_cost"`
		Adjustments map[string]string `json:"adjustments"`
	}
	if json.Unmarshal(out, &s) != nil {
		return nil, fmt.Errorf("réponse de Modal illisible")
	}
	num := func(v string) float64 { f, _ := strconv.ParseFloat(strings.TrimSpace(v), 64); return f }
	used := num(s.Metered)
	credits := -num(s.Adjustments["credits"])
	d := map[string]any{
		"month_cost":   used,    // consommé ce mois
		"credits_used": credits, // part couverte par les crédits
		"billed":       num(s.Billed),
	}
	// Offre Starter (pas d'abonnement) : 30 $ de crédit mensuel, le reste s'en déduit.
	if num(s.Adjustments["plan_cost"]) == 0 {
		left := modalStarterCredit - credits
		if left < 0 {
			left = 0
		}
		d["credit_left"] = left
		d["credit_month"] = modalStarterCredit
	}
	return d, nil
}

// modalErrorText extrait le message d'une erreur de la CLI Modal (encadrée de
// caractères de boîte) et traduit les cas connus en une phrase courte.
func modalErrorText(raw string) string {
	var parts []string
	for _, l := range strings.Split(raw, "\n") {
		l = strings.Trim(strings.TrimSpace(l), "│┌┐└┘─ ")
		if l == "" || l == "Error" || strings.HasPrefix(l, "Traceback") {
			continue
		}
		parts = append(parts, l)
	}
	msg := strings.Join(parts, " ")
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "spend limit"):
		return "limite de dépense atteinte : ajouter une carte ou relever la limite sur modal.com"
	case strings.Contains(low, "permission") || strings.Contains(low, "not authorized") ||
		strings.Contains(low, "forbidden") || strings.Contains(low, "owner") || strings.Contains(low, "manager"):
		return "facturation réservée au propriétaire du workspace Modal"
	case strings.Contains(low, "token") && (strings.Contains(low, "invalid") || strings.Contains(low, "expired")):
		return "connexion au compte expirée : reconnecter le compte"
	case strings.Contains(low, "not found in") && strings.Contains(low, "profile"):
		return "compte introuvable sur cette machine : le reconnecter"
	}
	if r := []rune(msg); len(r) > 140 {
		msg = string(r[:140]) + "…"
	}
	if msg == "" {
		msg = "crédit indisponible"
	}
	return msg
}

// GET /api/cloud/billing?profile= : résumé du mois (attend la 1re mesure).
func handleCloudBilling(w http.ResponseWriter, r *http.Request) {
	profile := strings.TrimSpace(r.URL.Query().Get("profile"))
	if profile != "" && !modalProfileRe.MatchString(profile) {
		sendJSON(w, 400, map[string]any{"error": "profil invalide"})
		return
	}
	if d := cloudBillingCached(profile); d != nil {
		sendJSON(w, 200, d)
		return
	}
	d, err := fetchCloudBilling(profile)
	if err != nil {
		sendJSON(w, 200, map[string]any{"error": err.Error()})
		return
	}
	cloudBills.Lock()
	cloudBills.m[profile] = &cloudBill{at: time.Now(), data: d}
	cloudBills.Unlock()
	sendJSON(w, 200, d)
}

// GET /api/cloud/runtime : état du composant GPU cloud. POST : (ré)installe.
func handleCloudRuntime(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		startCloudRuntimeInstall()
	}
	st, step, e := cloudRuntimeStatus()
	sendJSON(w, 200, map[string]any{"state": st, "step": step, "error": e})
}
