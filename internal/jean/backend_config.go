package jean

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// readAPIKey returns the trimmed contents of $JEAN_HOME/.api_key, or "" if the
// file is absent/empty. This store is independent of config.env so the key
// survives preset switches (which rewrite config.env wholesale).
func readAPIKey() string {
	b, err := os.ReadFile(apiKeyPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// authHeader sets the Authorization: Bearer header on req when an API key is
// configured, so jean's own internal calls (chat/web/bench/test) authenticate
// against a protected llama-server. No-op when no key is set.
func authHeader(req *http.Request) {
	if k := readAPIKey(); k != "" {
		req.Header.Set("Authorization", "Bearer "+k)
	}
}

// genAPIKey returns a fresh random OpenAI-style completion key.
func genAPIKey() string {
	buf := make([]byte, 24)
	_, _ = rand.Read(buf)
	return "sk-jean-" + hex.EncodeToString(buf)
}

// writeAPIKey persists (key != "") or clears (key == "") the completion API key
// in $JEAN_HOME/.api_key. It also wipes any residual API_KEY in config.env to
// avoid ambiguity. It does NOT restart the service — callers decide when to
// apply (llama-server only reads --api-key at launch).
func writeAPIKey(key string) error {
	_ = SetConfigKey("API_KEY", "")
	if key == "" {
		if err := os.Remove(apiKeyPath()); err != nil && !os.IsNotExist(err) {
			return err
		}
		return nil
	}
	return os.WriteFile(apiKeyPath(), []byte(key+"\n"), 0o600)
}

// maskAPIKey renders a key for display: keep the "sk-jean-" prefix and last 4
// chars, elide the middle. Empty in → empty out.
func maskAPIKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 12 {
		return "…" + k[len(k)-2:]
	}
	return k[:8] + "…" + k[len(k)-4:]
}

// cmdSetAPIKey sets (or clears) the API key stored in $JEAN_HOME/.api_key. When
// exposed on the internet, llama-server requires "Authorization: Bearer <clé>"
// for every call.
//
//	jean set-api-key <clé>     définit la clé
//	jean set-api-key           génère une clé aléatoire
//	jean set-api-key ""        supprime la protection
func cmdSetAPIKey(args []string) error {
	var key string
	switch {
	case len(args) == 0:
		key = genAPIKey()
		fmt.Printf("%s clé générée : %s\n", green("[ok]"), bold(key))
	case args[0] == "" || args[0] == "off" || args[0] == "none":
		key = ""
	default:
		key = strings.TrimSpace(args[0])
	}
	// Stocke dans le fichier dédié (survit aux switches de preset). On nettoie
	// aussi un éventuel API_KEY résiduel dans config.env pour éviter la confusion.
	if err := writeAPIKey(key); err != nil {
		return err
	}
	if key == "" {
		fmt.Printf("%s API_KEY supprimée — serveur ouvert (pas d'authentification)\n", yellow("[info]"))
	} else {
		fmt.Printf("%s API_KEY enregistrée dans %s\n", green("[ok]"), apiKeyPath())
		fmt.Printf("       les clients doivent envoyer : %s\n", dim("Authorization: Bearer "+key))
	}
	fmt.Print(dim("[info] redémarrer le service pour appliquer ? [Y/n] "))
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() && strings.HasPrefix(strings.ToLower(strings.TrimSpace(sc.Text())), "n") {
		fmt.Println(dim("[info] pense à lancer 'jean restart'"))
		return nil
	}
	return serviceAction("restart")
}

// ReadConfig parses config.env into a key/value map.
// Lines starting with '#' and blanks are ignored. Values may be quoted with ".
func ReadConfig() map[string]string {
	m := map[string]string{}
	b, err := os.ReadFile(confPath())
	if err != nil {
		return m
	}
	for _, line := range strings.Split(string(b), "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		i := strings.IndexByte(s, '=')
		if i < 0 {
			continue
		}
		k := strings.TrimSpace(s[:i])
		v := strings.TrimSpace(s[i+1:])
		v = strings.Trim(v, "\"")
		m[k] = v
	}
	return m
}

// SetConfigKey sets key=value in config.env, updating the line in place if the
// key already exists (preserving comments/order) or appending it otherwise.
// An empty value removes the key. The file is created if missing.
func SetConfigKey(key, value string) error {
	b, _ := os.ReadFile(confPath())
	lines := []string{}
	if len(b) > 0 {
		lines = strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	}
	newLine := key + "=" + value
	found := false
	out := []string{}
	for _, line := range lines {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			out = append(out, line)
			continue
		}
		i := strings.IndexByte(s, '=')
		if i >= 0 && strings.TrimSpace(s[:i]) == key {
			found = true
			if value != "" {
				out = append(out, newLine)
			}
			// empty value => drop the line
			continue
		}
		out = append(out, line)
	}
	if !found && value != "" {
		out = append(out, newLine)
	}
	content := strings.Join(out, "\n") + "\n"
	return os.WriteFile(confPath(), []byte(content), 0o644)
}

// toolLimitEnabled reports whether the per-turn tool-call cap is active.
// Default: true (limité). Only an explicit "off"/"false"/"0"/"no" in config.env
// (TOOL_LIMIT) disables it — anything else keeps the safety cap on.
func toolLimitEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(ReadConfig()["TOOL_LIMIT"])) {
	case "off", "false", "0", "no", "non":
		return false
	}
	return true
}

// toolCallLimit returns the max number of agentic tool-call iterations per turn.
// 8 when the limit is on (default), a very high ceiling when the user disabled it
// from the web UI — the repeated-call anti-loop still guards against runaways.
func toolCallLimit() int {
	if toolLimitEnabled() {
		return 8
	}
	return 1000
}

// LLMPort returns the configured server port (config.env PORT), default 8080.
func LLMPort() int {
	if p, ok := ReadConfig()["PORT"]; ok {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			return n
		}
	}
	return 8080
}
