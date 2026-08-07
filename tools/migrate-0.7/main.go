// migrate-0.7 — reprend une installation AJEAN 0.7.x dans la base de la 0.8.
//
// USAGE UNIQUE, et volontairement HORS du binaire ajean. La 0.8 ne contient
// aucun code de compatibilité : c'est tout l'intérêt de la version. Mais une
// machine déjà installée ne doit pas pour autant perdre ses réglages, sa
// conversation et ses clés. D'où cet outil séparé, qu'on lance une fois puis
// qu'on oublie — le produit reste propre, la migration reste possible.
//
//	go run ./tools/migrate-0.7 [$AJEAN_HOME]      (défaut : /etc/ajean)
//
// Il lit l'ancienne disposition (config.env, webprefs.json, conversation.json,
// .api_key, .link_token…) et la recopie dans ajean.db, aux mêmes buckets et aux
// mêmes clés que internal/ajean/store.go.
//
// Il ne SUPPRIME rien : les anciens fichiers restent en place, intacts. Si la
// migration s'avère fausse, il suffit d'effacer ajean.db et de la relancer.
//
// Ce qu'il ne fait PAS, parce que ça se fait mieux au shell (voir le README) :
// déplacer configs/ vers presets/, MEMORY/ vers memory/, les .gguf vers
// models/, et remplacer les unités systemd.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	bkConfig = "config"
	bkPrefs  = "prefs"
	bkState  = "state"
	bkChat   = "chat"
)

var home = "/etc/ajean"

func main() {
	if len(os.Args) > 1 {
		home = os.Args[1]
	}
	db, err := bolt.Open(filepath.Join(home, "ajean.db"), 0o600, &bolt.Options{Timeout: 5 * time.Second})
	must(err)
	defer db.Close()
	must(db.Update(func(tx *bolt.Tx) error {
		for _, b := range []string{bkConfig, bkPrefs, bkState, bkChat} {
			if _, err := tx.CreateBucketIfNotExists([]byte(b)); err != nil {
				return err
			}
		}
		return nil
	}))

	// Configuration du moteur.
	if cfg := parseEnv(read("config.env")); len(cfg) > 0 {
		must(putAll(db, bkConfig, cfg))
		fmt.Printf("config      : %d clés\n", len(cfg))
	} else {
		fmt.Println("config      : ABSENTE — le service ne démarrera pas")
	}

	// Préférences de l'interface web.
	var prefs map[string]string
	if b := read("webprefs.json"); b != "" && json.Unmarshal([]byte(b), &prefs) == nil {
		must(putAll(db, bkPrefs, prefs))
		fmt.Printf("prefs       : %d clés\n", len(prefs))
	}

	// Clés, jetons et interrupteurs.
	for key, file := range map[string]string{
		"api_key":      ".api_key",
		"web_key":      ".web_key",
		"crawl_key":    ".crawl4ai_key",
		"link_token":   ".link_token",
		"link_machine": ".link_machine",
	} {
		if v := strings.TrimSpace(read(file)); v != "" {
			must(put(db, bkState, key, []byte(v)))
			fmt.Printf("%-12s: repris\n", key)
		}
	}
	// Les drapeaux étaient des fichiers dont seule l'EXISTENCE comptait.
	for key, file := range map[string]string{
		"agent":      ".agent_enabled",
		"internet":   ".internet_enabled",
		"oai_public": ".oai_public",
	} {
		if _, err := os.Stat(filepath.Join(home, file)); err == nil {
			must(put(db, bkState, key, []byte("1")))
			fmt.Printf("%-12s: actif\n", key)
		}
	}

	// Serveurs MCP : on ne garde que la map interne, la clé « mcpServers » du
	// fichier n'a plus de raison d'être.
	if b := read("mcp.json"); b != "" {
		var f struct {
			MCPServers json.RawMessage `json:"mcpServers"`
		}
		if json.Unmarshal([]byte(b), &f) == nil && len(f.MCPServers) > 0 {
			must(put(db, bkState, "mcp", f.MCPServers))
			fmt.Println("mcp         : repris")
		}
	}

	// Benchmarks et dossiers de modèles : recopiés tels quels (même forme JSON).
	for key, file := range map[string]string{
		"last_bench":    ".last_bench.json",
		"bench_presets": ".bench_presets.json",
		"model_dirs":    "model_dirs.json",
	} {
		if b := read(file); b != "" {
			must(put(db, bkState, key, []byte(b)))
			fmt.Printf("%-12s: repris\n", key)
		}
	}

	// Conversation partagée.
	if b := read("conversation.json"); b != "" {
		must(put(db, bkChat, "conversation", []byte(b)))
		fmt.Printf("conversation: %d octets\n", len(b))
	}

	fmt.Println("\nTerminé — aucun ancien fichier n'a été supprimé.")
}

func read(name string) string {
	b, err := os.ReadFile(filepath.Join(home, name))
	if err != nil {
		return ""
	}
	return string(b)
}

func put(db *bolt.DB, bucket, key string, val []byte) error {
	return db.Update(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucket)).Put([]byte(key), val)
	})
}

func putAll(db *bolt.DB, bucket string, m map[string]string) error {
	return db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		for k, v := range m {
			if v == "" {
				continue
			}
			if err := b.Put([]byte(k), []byte(v)); err != nil {
				return err
			}
		}
		return nil
	})
}

// parseEnv reprend à l'identique la lecture de backend_config.go.
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
		m[strings.TrimSpace(k)] = strings.Trim(strings.TrimSpace(v), "\"")
	}
	return m
}

func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "migration:", err)
		os.Exit(1)
	}
}
