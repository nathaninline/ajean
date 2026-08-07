package ajean

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

// store.go — l'unique endroit où AJEAN écrit son état.
//
// Avant, chaque réglage avait son fichier : config.env, webprefs.json,
// conversation.json, .api_key, .link_token, .agent_enabled, model_dirs.json…
// Une douzaine de formats, une douzaine de façons de rater une écriture
// concurrente, et un dossier de données illisible. Tout ça tient désormais dans
// une seule base bbolt — pur Go, un seul fichier, transactionnelle.
//
// Ce qui N'EST PAS en base, et pourquoi : les presets (presets/*.env) et les
// pages de mémoire (memory/*.md) restent des fichiers, parce qu'ils sont faits
// pour être lus, édités et sauvegardés à la main. Les modèles (.gguf) et les
// backends compilés restent des fichiers, évidemment.

// Buckets. Un par nature de donnée : ça garde les itérations bornées et rend le
// contenu de la base lisible au débogage.
const (
	bkConfig = "config" // configuration de llama-server (ex-config.env)
	bkPrefs  = "prefs"  // préférences de l'UI web
	bkState  = "state"  // clés, jetons, drapeaux, listes de dossiers, MCP
	bkChat   = "chat"   // conversation partagée
)

// Les connexions sont mises en cache PAR CHEMIN, et non dans un singleton.
// $AJEAN_HOME peut désigner un autre dossier d'un appel à l'autre — c'est le cas
// dans les tests, qui donnent à chacun son dossier temporaire. Un singleton
// figerait la toute première base et tous les appels suivants écriraient dans le
// mauvais fichier, sans erreur et sans bruit.
var (
	dbMu    sync.Mutex
	dbConns = map[string]*bolt.DB{}
)

func dbPath() string { return filepath.Join(AjeanHome(), "ajean.db") }

// db ouvre la base (ou renvoie celle déjà ouverte pour ce chemin) et la garde
// ouverte pour la durée du process. bbolt prend un verrou exclusif sur le
// fichier : le délai d'attente évite qu'un second process (l'app lancée deux
// fois) se bloque indéfiniment.
func db() (*bolt.DB, error) {
	path := dbPath()

	dbMu.Lock()
	defer dbMu.Unlock()
	if d, ok := dbConns[path]; ok {
		return d, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	d, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("base %s inaccessible : %w", path, err)
	}
	err = d.Update(func(tx *bolt.Tx) error {
		for _, b := range []string{bkConfig, bkPrefs, bkState, bkChat} {
			if _, err := tx.CreateBucketIfNotExists([]byte(b)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		d.Close()
		return nil, err
	}
	dbConns[path] = d
	return d, nil
}

// getBytes lit une valeur. Une base inaccessible se comporte comme une base
// vide : les appelants sont des lecteurs de réglages, aucun n'a de recours utile
// face à une erreur d'E/S, et tous ont déjà un défaut.
// closeDB ferme la base ouverte pour le dossier courant, s'il y en a une. bbolt
// garde un verrou exclusif sur son fichier tant qu'il est ouvert : sans ça,
// impossible de supprimer ou déplacer le dossier de données sous Windows.
func closeDB() {
	path := dbPath()
	dbMu.Lock()
	defer dbMu.Unlock()
	if d, ok := dbConns[path]; ok {
		_ = d.Close()
		delete(dbConns, path)
	}
}

func getBytes(bucket, key string) []byte {
	d, err := db()
	if err != nil {
		return nil
	}
	var out []byte
	_ = d.View(func(tx *bolt.Tx) error {
		if v := tx.Bucket([]byte(bucket)).Get([]byte(key)); v != nil {
			out = append([]byte(nil), v...) // la valeur ne survit pas à la transaction
		}
		return nil
	})
	return out
}

// putBytes écrit une valeur. Une valeur nil supprime la clé.
func putBytes(bucket, key string, val []byte) error {
	d, err := db()
	if err != nil {
		return err
	}
	return d.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		if val == nil {
			return b.Delete([]byte(key))
		}
		return b.Put([]byte(key), val)
	})
}

func getStr(bucket, key string) string { return string(getBytes(bucket, key)) }

// putStr écrit une chaîne ; une chaîne vide supprime la clé, pour que « absent »
// et « vide » ne soient jamais deux états distincts à distinguer.
func putStr(bucket, key, val string) error {
	if val == "" {
		return putBytes(bucket, key, nil)
	}
	return putBytes(bucket, key, []byte(val))
}

func getBool(bucket, key string) bool { return getStr(bucket, key) == "1" }

func putBool(bucket, key string, on bool) error {
	if !on {
		return putBytes(bucket, key, nil)
	}
	return putStr(bucket, key, "1")
}

// getJSON décode une valeur JSON dans dst. Renvoie false si la clé est absente
// ou illisible — dans les deux cas l'appelant garde son zéro.
func getJSON(bucket, key string, dst any) bool {
	b := getBytes(bucket, key)
	if len(b) == 0 {
		return false
	}
	return json.Unmarshal(b, dst) == nil
}

func putJSON(bucket, key string, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return putBytes(bucket, key, b)
}

// allKV renvoie tout le contenu d'un bucket. Utilisé par la configuration, dont
// les clés ne sont pas connues à l'avance (EXTRA_ARGS et consorts).
func allKV(bucket string) map[string]string {
	m := map[string]string{}
	d, err := db()
	if err != nil {
		return m
	}
	_ = d.View(func(tx *bolt.Tx) error {
		return tx.Bucket([]byte(bucket)).ForEach(func(k, v []byte) error {
			m[string(k)] = string(v)
			return nil
		})
	})
	return m
}

// replaceKV remplace tout le contenu d'un bucket en une seule transaction.
// C'est ce qu'exige l'application d'un preset : à aucun instant la config ne
// doit être un mélange de l'ancienne et de la nouvelle.
func replaceKV(bucket string, m map[string]string) error {
	d, err := db()
	if err != nil {
		return err
	}
	return d.Update(func(tx *bolt.Tx) error {
		if err := tx.DeleteBucket([]byte(bucket)); err != nil {
			return err
		}
		b, err := tx.CreateBucket([]byte(bucket))
		if err != nil {
			return err
		}
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
