package ajean

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
	bolterrors "go.etcd.io/bbolt/errors"
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

// La base n'est PAS gardée ouverte entre deux opérations, et c'est délibéré.
//
// bbolt pose un verrou EXCLUSIF sur son fichier tant qu'il est ouvert. Or une
// machine installée fait tourner en permanence le service de lien, qui sert le
// tunnel et l'UI : s'il gardait la base ouverte, plus une seule commande ne
// fonctionnerait à côté — « ajean status », « ajean switch », « ajean edit »
// échoueraient toutes sur un délai d'attente, sur la machine même où tout est
// censé marcher. On ouvre donc pour la durée d'une opération, puis on referme.
//
// Le coût est celui d'un open+close sur un fichier de quelques dizaines de Ko,
// négligeable devant le moindre appel au modèle. Le délai d'attente absorbe la
// contention entre process ; dbMu la sérialise à l'intérieur du process.
var dbMu sync.Mutex

func dbPath() string { return filepath.Join(AjeanHome(), "ajean.db") }

// withDB ouvre la base, exécute fn, puis referme — toujours, même en erreur.
func withDB(fn func(*bolt.DB) error) error {
	path := dbPath()

	dbMu.Lock()
	defer dbMu.Unlock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	d, err := bolt.Open(path, 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return fmt.Errorf("base %s inaccessible : %w", path, err)
	}
	defer d.Close()
	return fn(d)
}

// view exécute une lecture. Un bucket encore absent (base neuve) est traité
// comme vide : les buckets ne sont créés QU'À l'écriture, pour qu'une simple
// lecture n'ouvre jamais de transaction d'écriture — elle coûterait un fsync,
// et les lectures sont de loin les plus fréquentes.
func view(bucket string, fn func(b *bolt.Bucket) error) error {
	return withDB(func(d *bolt.DB) error {
		return d.View(func(tx *bolt.Tx) error {
			b := tx.Bucket([]byte(bucket))
			if b == nil {
				return nil
			}
			return fn(b)
		})
	})
}

// update exécute une écriture, en créant le bucket au besoin.
func update(bucket string, fn func(b *bolt.Bucket) error) error {
	return withDB(func(d *bolt.DB) error {
		return d.Update(func(tx *bolt.Tx) error {
			b, err := tx.CreateBucketIfNotExists([]byte(bucket))
			if err != nil {
				return err
			}
			return fn(b)
		})
	})
}

// getBytes lit une valeur. Une base inaccessible se comporte comme une base
// vide : les appelants sont des lecteurs de réglages, aucun n'a de recours utile
// face à une erreur d'E/S, et tous ont déjà un défaut.
func getBytes(bucket, key string) []byte {
	var out []byte
	_ = view(bucket, func(b *bolt.Bucket) error {
		if v := b.Get([]byte(key)); v != nil {
			out = append([]byte(nil), v...) // la valeur ne survit pas à la transaction
		}
		return nil
	})
	return out
}

// putBytes écrit une valeur. Une valeur nil supprime la clé.
func putBytes(bucket, key string, val []byte) error {
	return update(bucket, func(b *bolt.Bucket) error {
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
	_ = view(bucket, func(b *bolt.Bucket) error {
		return b.ForEach(func(k, v []byte) error {
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
	return withDB(func(d *bolt.DB) error {
		return d.Update(func(tx *bolt.Tx) error {
			if err := tx.DeleteBucket([]byte(bucket)); err != nil && !errors.Is(err, bolterrors.ErrBucketNotFound) {
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
	})
}
