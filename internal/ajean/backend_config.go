package ajean

import (
	"bufio"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
)

// readAPIKey renvoie la clé Bearer de llama-server, ou "" si aucune n'est
// définie. Elle est rangée hors de la configuration pour survivre aux
// changements de preset, qui remplacent la configuration en bloc.
func readAPIKey() string { return getStr(bkState, "api_key") }

// authHeader sets the Authorization: Bearer header on req when an API key is
// configured, so AJEAN's own internal calls (chat/web/bench/test) authenticate
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
	return "sk-ajean-" + hex.EncodeToString(buf)
}

// writeAPIKey enregistre (key != "") ou supprime (key == "") la clé Bearer.
// Ne redémarre PAS le service : llama-server ne lit --api-key qu'au lancement,
// c'est à l'appelant de choisir quand appliquer.
func writeAPIKey(key string) error {
	_ = SetConfigKey("API_KEY", "") // aucune ambiguïté avec une valeur résiduelle
	return putStr(bkState, "api_key", key)
}

// maskAPIKey renders a key for display: keep the "sk-ajean-" prefix and last 4
// chars, elide the middle. Empty in → empty out.
func maskAPIKey(k string) string {
	if k == "" {
		return ""
	}
	if len(k) <= 13 {
		return "…" + k[len(k)-2:]
	}
	return k[:9] + "…" + k[len(k)-4:]
}

// cmdSetAPIKey définit (ou supprime) la clé Bearer de llama-server. Exposé sur
// internet, le serveur exige alors « Authorization: Bearer <clé> » à chaque
// appel.
//
//	ajean set-api-key <clé>     définit la clé
//	ajean set-api-key           génère une clé aléatoire
//	ajean set-api-key ""        supprime la protection
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
	if err := writeAPIKey(key); err != nil {
		return err
	}
	if key == "" {
		fmt.Printf("%s API_KEY supprimée — serveur ouvert (pas d'authentification)\n", yellow("[info]"))
	} else {
		fmt.Printf("%s API_KEY enregistrée\n", green("[ok]"))
		fmt.Printf("       les clients doivent envoyer : %s\n", dim("Authorization: Bearer "+key))
	}
	fmt.Print(dim("[info] redémarrer le service pour appliquer ? [Y/n] "))
	sc := bufio.NewScanner(os.Stdin)
	if sc.Scan() && strings.HasPrefix(strings.ToLower(strings.TrimSpace(sc.Text())), "n") {
		fmt.Println(dim("[info] pense à lancer 'ajean restart'"))
		return nil
	}
	return serviceAction("restart")
}

// ReadConfig renvoie la configuration active de llama-server. Passe par le cache
// de lecture (store.go) : elle est relue à chaque itération de la boucle
// d'inférence et après chaque appel d'outil.
func ReadConfig() map[string]string { return cachedKV(bkConfig) }

// SetConfigKey définit une clé de configuration. Une valeur vide la supprime.
func SetConfigKey(key, value string) error { return putStr(bkConfig, key, value) }

// WriteConfig remplace TOUTE la configuration en une transaction. C'est ce
// qu'exige l'application d'un preset : jamais un état mi-ancien mi-nouveau.
func WriteConfig(m map[string]string) error { return replaceKV(bkConfig, m) }

// unquoteValue retire les guillemets ENTOURANTS d'une valeur, et seulement
// eux : une paire ouvrante/fermante du même caractère.
//
// Un simple Trim(v, `"`) mangeait le guillemet d'un argument interne — par
// exemple EXTRA_ARGS=--chat-template-file "/etc/ajean/tpl.jinja" perdait son
// guillemet final et repartait déséquilibré dans le preset (issue #17 : contenu
// tronqué / guillemets non appariés à la création d'un preset).
func unquoteValue(v string) string {
	if len(v) >= 2 {
		if q := v[0]; (q == '"' || q == '\'') && v[len(v)-1] == q {
			return v[1 : len(v)-1]
		}
	}
	return v
}

// parseEnv lit un fichier au format clé=valeur (les presets). Les lignes vides
// et les commentaires sont ignorés, les guillemets entourants retirés.
func parseEnv(text string) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(text, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		k, v, ok := strings.Cut(strings.TrimPrefix(s, "export "), "=")
		if !ok {
			continue
		}
		m[strings.TrimSpace(k)] = unquoteValue(strings.TrimSpace(v))
	}
	return m
}

// quoteValue rend une valeur telle que parseEnv la relise à l'identique. On
// n'entoure de guillemets que ce qui en a besoin (espaces), et jamais une valeur
// qui contient déjà un guillemet : elle est écrite telle quelle, unquoteValue ne
// touchant qu'à une paire entourante.
func quoteValue(v string) string {
	if v == "" || strings.ContainsRune(v, '"') {
		return v
	}
	if strings.ContainsAny(v, " \t") || unquoteValue(v) != v {
		return `"` + v + `"`
	}
	return v
}

// formatEnv rend une configuration au format des presets, clés triées pour que
// deux écritures du même contenu donnent le même fichier.
func formatEnv(m map[string]string) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%s\n", k, quoteValue(m[k]))
	}
	return b.String()
}

// splitArgs découpe EXTRA_ARGS comme le ferait un shell : sur les espaces, mais
// en respectant les guillemets, pour qu'un chemin qui en contient reste UN seul
// argument (--chat-template-file "/mes modèles/tpl.jinja"). Le découpage
// précédent, sur le seul caractère espace, le coupait en deux et llama-server
// refusait de démarrer.
func splitArgs(s string) []string {
	out := []string{}
	var cur strings.Builder
	var quote rune
	flush := func() {
		if cur.Len() > 0 {
			out = append(out, cur.String())
			cur.Reset()
		}
	}
	for _, r := range s {
		switch {
		case quote != 0:
			if r == quote {
				quote = 0
				// Guillemets vides ("" ) : on garde l'argument vide explicite.
				if cur.Len() == 0 {
					out = append(out, "")
				}
			} else {
				cur.WriteRune(r)
			}
		case r == '"' || r == '\'':
			quote = r
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return out
}

// samplingKeys : les paramètres d'échantillonnage réglables par preset. Ils sont
// injectés dans CHAQUE requête de complétion (applySampling), et non passés à
// llama-server au lancement — une valeur présente dans la requête l'emporte de
// toute façon sur le défaut du serveur, donc c'est le seul endroit où le réglage
// prend vraiment effet. Sans ça, seule la température était transmise (codée en
// dur), et top_k/min_p/… retombaient sur les défauts de llama.cpp, rarement ceux
// que recommande le modèle (Qwen3.8 veut top_k 20 / min_p 0, pas 40 / 0.05).
//
// Correspondance clé de config -> champ du corps OpenAI. Une valeur vide vaut
// « clé absente » : llama-server garde alors son propre défaut pour ce champ.
var samplingKeys = []struct {
	cfg, field string
	integer    bool
}{
	{"TEMP", "temperature", false},
	{"TOP_P", "top_p", false},
	{"TOP_K", "top_k", true},
	{"MIN_P", "min_p", false},
	{"PRESENCE_PENALTY", "presence_penalty", false},
	{"REPEAT_PENALTY", "repeat_penalty", false},
}

// applySampling superpose les paramètres d'échantillonnage du preset au corps
// d'une requête de complétion. Les clés vides sont ignorées (le serveur garde son
// défaut) ; TEMP, s'il est défini, l'emporte sur la température déjà posée dans le
// payload — c'est le réglage recommandé du modèle, et l'UI n'a de toute façon pas
// de curseur de température qui pourrait le contredire.
//
// REASONING_EFFORT (Qwen3.8 : low/medium/high/xhigh…) passe par le mécanisme des
// gabarits jinja de llama.cpp (chat_template_kwargs) : il n'a d'effet que si le
// serveur tourne avec --jinja, mais on ne l'envoie que lorsqu'un preset le
// définit explicitement, donc jamais à l'aveugle.
func applySampling(payload map[string]any) { applySamplingFrom(payload, ReadConfig()) }

// applySamplingFrom est la partie pure d'applySampling : testable sans base.
func applySamplingFrom(payload map[string]any, cfg map[string]string) {
	for _, s := range samplingKeys {
		v := strings.TrimSpace(cfg[s.cfg])
		if v == "" {
			continue
		}
		if s.integer {
			if n, err := strconv.Atoi(v); err == nil {
				payload[s.field] = n
			}
			continue
		}
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			payload[s.field] = f
		}
	}
	if eff := strings.TrimSpace(cfg["REASONING_EFFORT"]); eff != "" {
		payload["chat_template_kwargs"] = map[string]any{"reasoning_effort": eff}
	}
}

// Le plafond d'appels d'outils par tour (TOOL_LIMIT) et l'anti-boucle ont été
// retirés : ils coupaient surtout des tours légitimes. Le bouton stop est le
// seul frein.

// LLMPort renvoie le port du serveur (clé PORT), 8080 par défaut.
func LLMPort() int {
	if p, ok := ReadConfig()["PORT"]; ok {
		if n, err := strconv.Atoi(p); err == nil && n > 0 {
			return n
		}
	}
	return 8080
}
