package ajean

// secrets_vault.go — le coffre des clés d'API tierces.
//
// Avant, la clé d'un preset externe (OpenAI, Groq, OpenRouter…) était écrite en
// clair dans son fichier presets/*.env, puis recopiée en clair dans la
// configuration active ; les jetons des serveurs MCP l'étaient dans la base. Or
// un preset est fait pour être lu, édité et PARTAGÉ, /api/config et l'éditeur de
// preset renvoient ces contenus à l'interface, et la sauvegarde les emporte.
//
// Coffre activé (« ajean secrets on », ou Paramètres > Sécurité), ces valeurs
// sont remplacées là où elles étaient par une RÉFÉRENCE « vault:<id> ». La valeur
// vit dans la base, chiffrée (AES-256-GCM, mêmes primitives que la mémoire) par
// une clé propre à la machine : $AJEAN_HOME/.secrets_key (+ .bak).
//
// Ce que ça protège, et ce que ça ne protège pas — sans se raconter d'histoire :
//   - un preset partagé, une copie de la base seule, une réponse d'API, un
//     journal : la clé d'API n'y est plus ;
//   - quelqu'un qui lit TOUT $AJEAN_HOME a la clé du coffre avec le coffre. C'est
//     voulu : le service tourne sans personne devant, il doit pouvoir lire une clé
//     sans qu'on lui tape un mot de passe (contrairement à la mémoire chiffrée,
//     qui attend son déverrouillage).
//
// N'y entrent PAS : la clé de llama-server (api_key), celle du GPU cloud et le
// jeton ajean.link. Ce sont des secrets de la machine elle-même, lus au démarrage
// des services ; la première est de toute façon passée en clair sur la ligne de
// commande du moteur. Les y mettre n'apportait rien et ajoutait un point de panne
// au démarrage.
//
// Règles de prudence :
//   - Une référence se résout TOUJOURS, coffre activé ou non : l'interrupteur ne
//     décide que de l'endroit où vont les NOUVELLES clés. L'éteindre ne casse donc
//     jamais un preset.
//   - Rien n'est remplacé par une référence avant que la valeur chiffrée ait été
//     relue et déchiffrée à l'identique. Dans le doute, l'original reste en place.
//   - L'extinction remet chaque valeur en clair à sa place AVANT d'effacer quoi
//     que ce soit du coffre, et n'efface que ce qui n'est plus référencé.
//   - Une référence illisible est une ERREUR, jamais une clé vide passée en
//     silence.

import (
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	bkSecrets       = "secrets" // id → secretEntry (valeur chiffrée)
	stSecretsOn     = "secrets_on"
	secretRefPrefix = "vault:"
)

type secretEntry struct {
	Label string `json:"label"`        // à quoi sert la clé (affiché, jamais la valeur)
	Box   string `json:"box"`          // base64 de gcmSeal(clé du coffre, valeur, aad)
	At    int64  `json:"at,omitempty"` // epoch s
}

var secretsMu sync.Mutex

func secretsKeyPath() string    { return filepath.Join(AjeanHome(), ".secrets_key") }
func secretsKeyBakPath() string { return filepath.Join(AjeanHome(), ".secrets_key.bak") }

func secretAAD(id string) []byte { return []byte("ajean-secret/" + id) }

// secretsOn : le coffre est-il activé (les nouvelles clés y vont) ?
func secretsOn() bool { return getBool(bkState, stSecretsOn) }

// isSecretRef : cette valeur est-elle une référence au coffre ?
func isSecretRef(v string) bool {
	return strings.HasPrefix(v, secretRefPrefix) && secretIDRe.MatchString(strings.TrimPrefix(v, secretRefPrefix))
}

var secretIDRe = regexp.MustCompile(`^[a-f0-9]{16}$`)

func secretRefID(v string) string { return strings.TrimPrefix(v, secretRefPrefix) }

// readSecretsKeyFile lit une copie de la clé. (nil, nil) = fichier absent.
func readSecretsKeyFile(path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	k, derr := base64.StdEncoding.DecodeString(strings.TrimSpace(string(raw)))
	if derr != nil || len(k) != memDEKLen {
		return nil, fmt.Errorf("%s illisible", filepath.Base(path))
	}
	return k, nil
}

// secretsPruneGrace : âge minimal (s) d'une entrée avant qu'on l'élague (elle
// peut appartenir à un preset en cours d'écriture). Mis à 0 par les tests.
var secretsPruneGrace int64 = 60

func writeSecretsKeyFile(path string, k []byte) error {
	if err := memWriteFileVerified(path, []byte(base64.StdEncoding.EncodeToString(k)+"\n"), 0o600); err != nil {
		return err
	}
	// Après « sudo ajean secrets on », la clé appartiendrait à root et le service
	// ne pourrait plus la lire : on la donne au propriétaire d'AJEAN_HOME.
	chownLikeHome(path)
	return nil
}

// secretsKey renvoie la clé du coffre. Deux copies, chacune suffisante : celle
// qui manque ou diverge est réparée depuis l'autre. create=false ne crée JAMAIS de
// clé : une clé neuve à la place d'une clé perdue rendrait le coffre illisible en
// ayant l'air de marcher.
func secretsKey(create bool) ([]byte, error) {
	a, errA := readSecretsKeyFile(secretsKeyPath())
	b, errB := readSecretsKeyFile(secretsKeyBakPath())
	switch {
	case a != nil:
		if b == nil || subtle.ConstantTimeCompare(a, b) != 1 {
			_ = writeSecretsKeyFile(secretsKeyBakPath(), a)
		}
		return a, nil
	case b != nil:
		_ = writeSecretsKeyFile(secretsKeyPath(), b)
		return b, nil
	}
	if errA != nil || errB != nil {
		return nil, fmt.Errorf("clé du coffre illisible (%s)", secretsKeyPath())
	}
	if !create {
		return nil, fmt.Errorf("clé du coffre introuvable (%s) : les clés d'API qu'il contient ne peuvent pas être lues", secretsKeyPath())
	}
	// Coffre non vide et aucune clé : on ne fabrique surtout pas une clé neuve.
	if n := len(allKeys(bkSecrets)); n > 0 {
		return nil, fmt.Errorf("clé du coffre introuvable alors qu'il contient %d secret(s) : restaure %s avant d'y ajouter quoi que ce soit", n, filepath.Base(secretsKeyPath()))
	}
	k, err := randBytes(memDEKLen)
	if err != nil {
		return nil, err
	}
	if err := writeSecretsKeyFile(secretsKeyPath(), k); err != nil {
		return nil, fmt.Errorf("clé du coffre non écrite : %w", err)
	}
	if err := writeSecretsKeyFile(secretsKeyBakPath(), k); err != nil {
		return nil, fmt.Errorf("copie de la clé du coffre non écrite : %w", err)
	}
	return k, nil
}

func newSecretID() (string, error) {
	b, err := randBytes(8)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// secretPutID range (ou remplace) une valeur sous cet id, puis la RELIT et la
// déchiffre : on ne rend la main que si le coffre rend bien ce qu'on y a mis.
func secretPutID(id, label, value string) error {
	if !secretIDRe.MatchString(id) {
		return errors.New("identifiant de secret invalide")
	}
	if value == "" {
		return errors.New("secret vide")
	}
	secretsMu.Lock()
	defer secretsMu.Unlock()
	key, err := secretsKey(true)
	if err != nil {
		return err
	}
	box, err := gcmSeal(key, []byte(value), secretAAD(id))
	if err != nil {
		return err
	}
	e := secretEntry{Label: label, Box: base64.StdEncoding.EncodeToString(box), At: time.Now().Unix()}
	// Ce qu'il y avait sous cet id (restauration d'une sauvegarde) : remis en place
	// si la vérification échoue, pour ne jamais troquer une valeur lisible contre rien.
	prev, err := getBytesErr(bkSecrets, id)
	if err != nil {
		return err
	}
	if err := putJSON(bkSecrets, id, e); err != nil {
		return err
	}
	secretCacheBust()
	got, err := secretGetLocked(id)
	if err != nil || subtle.ConstantTimeCompare([]byte(got), []byte(value)) != 1 {
		_ = putBytes(bkSecrets, id, prev) // nil = l'entrée n'existait pas
		secretCacheBust()
		return fmt.Errorf("le coffre n'a pas rendu la valeur qu'on venait d'y mettre : rien n'a été changé")
	}
	return nil
}

// secretPut range une valeur et rend sa référence « vault:<id> ».
func secretPut(label, value string) (string, error) {
	id, err := newSecretID()
	if err != nil {
		return "", err
	}
	if err := secretPutID(id, label, value); err != nil {
		return "", err
	}
	return secretRefPrefix + id, nil
}

func secretGetLocked(id string) (string, error) {
	var e secretEntry
	raw, err := getBytesErr(bkSecrets, id)
	if err != nil {
		return "", err
	}
	if len(raw) == 0 {
		return "", fmt.Errorf("secret %s absent du coffre", id)
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return "", fmt.Errorf("secret %s illisible", id)
	}
	box, err := base64.StdEncoding.DecodeString(e.Box)
	if err != nil {
		return "", fmt.Errorf("secret %s illisible", id)
	}
	key, err := secretsKey(false)
	if err != nil {
		return "", err
	}
	plain, err := gcmOpen(key, box, secretAAD(id))
	if err != nil {
		return "", fmt.Errorf("secret %s indéchiffrable (clé du coffre changée, ou donnée altérée)", id)
	}
	return string(plain), nil
}

// secretCache : les valeurs déjà déchiffrées, le temps que la base ne bouge pas
// (même règle que cachedKV). resolveChatEndpoint est appelé à chaque complétion :
// sans cache, chacune rouvrait la base et relisait la clé.
var secretCache = struct {
	sync.Mutex
	path  string
	vals  map[string]string
	when  time.Time
	mtime time.Time
	size  int64
}{vals: map[string]string{}}

func secretCacheBust() {
	secretCache.Lock()
	secretCache.vals = map[string]string{}
	secretCache.Unlock()
}

// secretResolve rend la valeur derrière une référence ; toute autre valeur est
// rendue telle quelle (une clé restée en clair continue de marcher). Une
// référence illisible renvoie une erreur.
func secretResolve(v string) (string, error) {
	if !isSecretRef(v) {
		return v, nil
	}
	id := secretRefID(v)
	path := dbPath()
	mtime, size := dbStamp()
	c := &secretCache
	c.Lock()
	if c.path != path || c.size != size || !c.mtime.Equal(mtime) || time.Since(c.when) >= cacheMaxAge {
		c.path, c.size, c.mtime, c.when, c.vals = path, size, mtime, time.Now(), map[string]string{}
	}
	if val, ok := c.vals[id]; ok {
		c.Unlock()
		return val, nil
	}
	c.Unlock()
	secretsMu.Lock()
	val, err := secretGetLocked(id)
	secretsMu.Unlock()
	if err != nil {
		return "", err
	}
	c.Lock()
	c.vals[id] = val
	c.Unlock()
	return val, nil
}

func secretDelete(id string) error {
	secretsMu.Lock()
	defer secretsMu.Unlock()
	secretCacheBust()
	return putBytes(bkSecrets, id, nil)
}

// secretInfo : ce qu'on montre d'un secret — jamais sa valeur.
type secretInfo struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	At    int64  `json:"at,omitempty"`
}

func secretList() []secretInfo {
	out := []secretInfo{}
	_ = view(bkSecrets, func(b *bolt.Bucket) error {
		return b.ForEach(func(k, v []byte) error {
			var e secretEntry
			if json.Unmarshal(v, &e) == nil {
				out = append(out, secretInfo{ID: string(k), Label: e.Label, At: e.At})
			}
			return nil
		})
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Label+out[i].ID < out[j].Label+out[j].ID })
	return out
}

// ---- où vivent les clés : presets, configuration active, serveurs MCP ----------

// extKeyLineRe : la ligne EXTERNAL_KEY=… d'un preset (valeur en groupe 2).
var extKeyLineRe = regexp.MustCompile(`(?m)^([ \t]*(?:export[ \t]+)?` + extKeyToken + `[ \t]*=[ \t]*)([^\r\n]*)`)

// presetSetExtKey remplace la valeur d'EXTERNAL_KEY dans le TEXTE d'un preset,
// sans toucher au reste (commentaires, ordre, autres clés). Renvoie false si le
// résultat ne se relit pas comme attendu : l'appelant garde alors l'original.
func presetSetExtKey(content, value string) (string, bool) {
	before := parseEnv(content)
	out := extKeyLineRe.ReplaceAllStringFunc(content, func(line string) string {
		m := extKeyLineRe.FindStringSubmatch(line)
		return m[1] + quoteValue(value)
	})
	after := parseEnv(out)
	if after[extKeyToken] != value || len(after) != len(before) {
		return content, false
	}
	for k, v := range before {
		if k != extKeyToken && after[k] != v {
			return content, false
		}
	}
	return out, true
}

// sealPresetContent : coffre activé, remplace une clé en clair par sa référence
// dans le texte d'un preset. reuse évite de ranger deux fois la même clé (deux
// presets du même fournisseur partagent une entrée).
func sealPresetContent(content, label string, reuse map[string]string) (string, error) {
	plain := strings.TrimSpace(parseEnv(content)[extKeyToken])
	if plain == "" || isSecretRef(plain) {
		return content, nil
	}
	ref, ok := reuse[plain]
	if !ok {
		var err error
		if ref, err = secretPut(label, plain); err != nil {
			return content, err
		}
		if reuse != nil {
			reuse[plain] = ref
		}
	}
	out, ok := presetSetExtKey(content, ref)
	if !ok {
		return content, errors.New("ligne " + extKeyToken + " non reconnue : laissée en clair")
	}
	return out, nil
}

// mcpSecretName : ce nom d'en-tête ou de variable porte-t-il un secret ?
var mcpSecretNameRe = regexp.MustCompile(`(?i)(key|token|secret|passw|pwd|credential|auth|cookie|bearer)`)

// sealMCPConfig range dans le coffre les valeurs sensibles d'un serveur MCP
// (en-têtes d'authentification, variables d'environnement à nom de secret).
func sealMCPConfig(name string, cfg MCPServerConfig, reuse map[string]string) (MCPServerConfig, int, error) {
	n := 0
	seal := func(m map[string]string, kind string) (map[string]string, error) {
		if len(m) == 0 {
			return m, nil
		}
		out := make(map[string]string, len(m))
		for k, v := range m {
			out[k] = v
			if v == "" || isSecretRef(v) || !mcpSecretNameRe.MatchString(k) {
				continue
			}
			ref, ok := reuse[v]
			if !ok {
				var err error
				if ref, err = secretPut("MCP "+name+" · "+kind+" "+k, v); err != nil {
					return m, err
				}
				if reuse != nil {
					reuse[v] = ref
				}
			}
			out[k] = ref
			n++
		}
		return out, nil
	}
	h, err := seal(cfg.Headers, "en-tête")
	if err != nil {
		return cfg, 0, err
	}
	e, err := seal(cfg.Env, "variable")
	if err != nil {
		return cfg, 0, err
	}
	cfg.Headers, cfg.Env = h, e
	return cfg, n, nil
}

// resolveMCPSecrets rend les en-têtes et variables d'un serveur MCP avec leurs
// références résolues, au moment de s'y connecter.
func resolveMCPSecrets(cfg MCPServerConfig) (MCPServerConfig, error) {
	res := func(m map[string]string) (map[string]string, error) {
		if len(m) == 0 {
			return m, nil
		}
		out := make(map[string]string, len(m))
		for k, v := range m {
			val, err := secretResolve(v)
			if err != nil {
				return nil, fmt.Errorf("%s : %w", k, err)
			}
			out[k] = val
		}
		return out, nil
	}
	h, err := res(cfg.Headers)
	if err != nil {
		return cfg, err
	}
	e, err := res(cfg.Env)
	if err != nil {
		return cfg, err
	}
	cfg.Headers, cfg.Env = h, e
	return cfg, nil
}

// secretRefsInUse : toutes les références encore posées quelque part (presets,
// configuration active, serveurs MCP) → où. Une référence qui n'y figure plus
// peut quitter le coffre.
//
// La moindre lecture ratée remonte en ERREUR : un preset illisible un instant
// ferait passer sa clé pour inutilisée, et le ménage l'effacerait pour de bon.
func secretRefsInUse() (map[string][]string, error) {
	used := map[string][]string{}
	add := func(v, where string) {
		if isSecretRef(v) {
			used[secretRefID(v)] = append(used[secretRefID(v)], where)
		}
	}
	entries, err := os.ReadDir(presetsDir())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".env") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(presetsDir(), e.Name()))
		if err != nil {
			return nil, err
		}
		add(strings.TrimSpace(parseEnv(string(b))[extKeyToken]), "preset "+strings.TrimSuffix(e.Name(), ".env"))
	}
	cfgKey, err := getBytesErr(bkConfig, extKeyToken)
	if err != nil {
		return nil, err
	}
	add(strings.TrimSpace(string(cfgKey)), "configuration active")
	rawMCP, err := getBytesErr(bkState, "mcp")
	if err != nil {
		return nil, err
	}
	if len(rawMCP) > 0 {
		servers := map[string]MCPServerConfig{}
		if err := json.Unmarshal(rawMCP, &servers); err != nil {
			return nil, fmt.Errorf("serveurs MCP illisibles : %w", err)
		}
		for name, cfg := range servers {
			for _, v := range cfg.Headers {
				add(v, "MCP "+name)
			}
			for _, v := range cfg.Env {
				add(v, "MCP "+name)
			}
		}
	}
	return used, nil
}

// secretsPrune retire du coffre ce qui n'est plus référencé nulle part. Dans le
// doute (une source illisible), il ne retire RIEN.
func secretsPrune() int {
	list := secretList()
	if len(list) == 0 {
		return 0
	}
	used, err := secretRefsInUse()
	if err != nil {
		return 0
	}
	n := 0
	for _, s := range list {
		// Une entrée toute neuve peut appartenir à un preset en cours d'écriture.
		if secretsPruneGrace > 0 && time.Now().Unix()-s.At < secretsPruneGrace {
			continue
		}
		if _, ok := used[s.ID]; !ok {
			if secretDelete(s.ID) == nil {
				n++
			}
		}
	}
	return n
}

// secretsReport : ce qu'une bascule a fait, pour le dire à l'utilisateur.
type secretsReport struct {
	Moved   int      `json:"moved"`   // valeurs déplacées (vers le coffre, ou remises en clair)
	Skipped []string `json:"skipped"` // ce qui est resté tel quel, et pourquoi
}

// secretsEnable active le coffre et y déplace les clés qui dorment en clair.
// Chaque déplacement est indépendant : un preset qu'on ne peut pas réécrire
// reste en clair (et continue de marcher), il est seulement signalé.
func secretsEnable() (secretsReport, error) {
	rep := secretsReport{Skipped: []string{}}
	// Auto-test avant de toucher à quoi que ce soit : le coffre rend-il ce qu'on y met ?
	probe, err := secretPut("auto-test", "ajean-coffre-ok")
	if err != nil {
		return rep, fmt.Errorf("coffre inutilisable, rien n'a été changé : %w", err)
	}
	if v, err := secretResolve(probe); err != nil || v != "ajean-coffre-ok" {
		_ = secretDelete(secretRefID(probe))
		return rep, errors.New("coffre inutilisable (auto-test), rien n'a été changé")
	}
	_ = secretDelete(secretRefID(probe))

	reuse := map[string]string{}
	// Presets.
	if entries, err := os.ReadDir(presetsDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".env") {
				continue
			}
			id := strings.TrimSuffix(e.Name(), ".env")
			p := filepath.Join(presetsDir(), e.Name())
			b, err := os.ReadFile(p)
			if err != nil {
				rep.Skipped = append(rep.Skipped, "preset "+id+" : "+err.Error())
				continue
			}
			out, err := sealPresetContent(string(b), "preset "+presetDisplayName(string(b), id), reuse)
			if err != nil {
				rep.Skipped = append(rep.Skipped, "preset "+id+" : "+err.Error())
				continue
			}
			if out == string(b) {
				continue
			}
			mode := os.FileMode(0o644)
			if fi, err := os.Stat(p); err == nil {
				mode = fi.Mode().Perm()
			}
			if err := memWriteFileVerified(p, []byte(out), mode); err != nil {
				rep.Skipped = append(rep.Skipped, "preset "+id+" : "+err.Error())
				continue
			}
			rep.Moved++
		}
	}
	// Configuration active : la même clé que le preset actif reprend SA référence,
	// pour que le preset reste reconnu comme actif (empreintes égales).
	if plain := strings.TrimSpace(allKV(bkConfig)[extKeyToken]); plain != "" && !isSecretRef(plain) {
		ref, ok := reuse[plain]
		if !ok {
			ref, err = secretPut("configuration active", plain)
		}
		if err != nil {
			rep.Skipped = append(rep.Skipped, "configuration active : "+err.Error())
		} else if err := SetConfigKey(extKeyToken, ref); err != nil {
			rep.Skipped = append(rep.Skipped, "configuration active : "+err.Error())
		} else {
			rep.Moved++
		}
	}
	// Serveurs MCP.
	mcpConfigMu.Lock()
	if servers, err := loadMCPConfigLocked(); err == nil {
		changed := false
		for name, cfg := range servers {
			sealed, n, err := sealMCPConfig(name, cfg, reuse)
			if err != nil {
				rep.Skipped = append(rep.Skipped, "MCP "+name+" : "+err.Error())
				continue
			}
			if n > 0 {
				servers[name] = sealed
				rep.Moved += n
				changed = true
			}
		}
		if changed {
			if err := saveMCPConfigLocked(servers); err != nil {
				rep.Skipped = append(rep.Skipped, "serveurs MCP : "+err.Error())
			}
		}
	}
	mcpConfigMu.Unlock()
	// Une entrée rangée pour un endroit qu'on n'a finalement pas pu réécrire ne
	// sert à personne.
	secretsPrune()
	if err := putBool(bkState, stSecretsOn, true); err != nil {
		return rep, err
	}
	return rep, nil
}

// secretsDisable remet chaque clé en clair là où elle était, PUIS vide le
// coffre de ce qui n'est plus référencé. Tout est d'abord résolu à blanc : si
// une seule référence est illisible, rien n'est touché.
func secretsDisable() (secretsReport, error) {
	rep := secretsReport{Skipped: []string{}}
	inUse, err := secretRefsInUse()
	if err != nil {
		return rep, fmt.Errorf("lecture des presets et réglages impossible (%w) — rien n'a été changé", err)
	}
	for id, where := range inUse {
		if _, err := secretResolve(secretRefPrefix + id); err != nil {
			return rep, fmt.Errorf("%s : %w — rien n'a été changé", strings.Join(where, ", "), err)
		}
	}
	// Presets.
	if entries, err := os.ReadDir(presetsDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".env") {
				continue
			}
			id := strings.TrimSuffix(e.Name(), ".env")
			p := filepath.Join(presetsDir(), e.Name())
			b, err := os.ReadFile(p)
			if err != nil {
				continue
			}
			ref := strings.TrimSpace(parseEnv(string(b))[extKeyToken])
			if !isSecretRef(ref) {
				continue
			}
			plain, err := secretResolve(ref)
			if err != nil {
				rep.Skipped = append(rep.Skipped, "preset "+id+" : "+err.Error())
				continue
			}
			out, ok := presetSetExtKey(string(b), plain)
			if !ok {
				rep.Skipped = append(rep.Skipped, "preset "+id+" : ligne "+extKeyToken+" non reconnue")
				continue
			}
			mode := os.FileMode(0o644)
			if fi, err := os.Stat(p); err == nil {
				mode = fi.Mode().Perm()
			}
			if err := memWriteFileVerified(p, []byte(out), mode); err != nil {
				rep.Skipped = append(rep.Skipped, "preset "+id+" : "+err.Error())
				continue
			}
			rep.Moved++
		}
	}
	// Configuration active.
	if ref := strings.TrimSpace(allKV(bkConfig)[extKeyToken]); isSecretRef(ref) {
		if plain, err := secretResolve(ref); err != nil {
			rep.Skipped = append(rep.Skipped, "configuration active : "+err.Error())
		} else if err := SetConfigKey(extKeyToken, plain); err != nil {
			rep.Skipped = append(rep.Skipped, "configuration active : "+err.Error())
		} else {
			rep.Moved++
		}
	}
	// Serveurs MCP.
	mcpConfigMu.Lock()
	if servers, err := loadMCPConfigLocked(); err == nil {
		changed := false
		for name, cfg := range servers {
			open, err := resolveMCPSecrets(cfg)
			if err != nil {
				rep.Skipped = append(rep.Skipped, "MCP "+name+" : "+err.Error())
				continue
			}
			n := 0
			for _, m := range []map[string]string{cfg.Headers, cfg.Env} {
				for _, v := range m {
					if isSecretRef(v) {
						n++
					}
				}
			}
			if n > 0 {
				servers[name] = open
				rep.Moved += n
				changed = true
			}
		}
		if changed {
			if err := saveMCPConfigLocked(servers); err != nil {
				rep.Skipped = append(rep.Skipped, "serveurs MCP : "+err.Error())
			}
		}
	}
	mcpConfigMu.Unlock()
	if len(rep.Skipped) > 0 {
		// Tout n'est pas revenu en clair : le coffre reste activé et garde ce qui est
		// encore référencé.
		secretsPrune()
		return rep, fmt.Errorf("%d élément(s) non remis en clair : le coffre reste activé", len(rep.Skipped))
	}
	secretsPrune()
	if err := putBool(bkState, stSecretsOn, false); err != nil {
		return rep, err
	}
	return rep, nil
}

// secretsStatus : l'état du coffre, sans aucune valeur.
func secretsStatus() map[string]any {
	used, _ := secretRefsInUse()
	list := secretList()
	type item struct {
		Label string   `json:"label"`
		Used  []string `json:"used"`
	}
	items := []item{}
	for _, s := range list {
		w := used[s.ID]
		if w == nil {
			w = []string{}
		}
		sort.Strings(w)
		items = append(items, item{Label: s.Label, Used: w})
	}
	// Clés encore en clair, pour dire ce qu'une activation déplacerait.
	plainN := 0
	if entries, err := os.ReadDir(presetsDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".env") {
				continue
			}
			if b, err := os.ReadFile(filepath.Join(presetsDir(), e.Name())); err == nil {
				if v := strings.TrimSpace(parseEnv(string(b))[extKeyToken]); v != "" && !isSecretRef(v) {
					plainN++
				}
			}
		}
	}
	if servers, err := LoadMCPConfig(); err == nil {
		for _, cfg := range servers {
			for _, m := range []map[string]string{cfg.Headers, cfg.Env} {
				for k, v := range m {
					if v != "" && !isSecretRef(v) && mcpSecretNameRe.MatchString(k) {
						plainN++
					}
				}
			}
		}
	}
	keyOK := true
	if len(list) > 0 {
		secretsMu.Lock()
		_, err := secretsKey(false)
		secretsMu.Unlock()
		keyOK = err == nil
	}
	return map[string]any{"on": secretsOn(), "count": len(list), "items": items, "plain": plainN, "key_ok": keyOK}
}

// ---- sauvegarde ----------------------------------------------------------------

// secretsExport : les secrets en clair, pour le paquet de sauvegarde (dont le
// corps est chiffré). Sans eux, un preset restauré sur une autre machine
// porterait une référence vers un coffre qui n'existe pas : la clé serait perdue.
func secretsExport() ([]byte, error) {
	list := secretList()
	if len(list) == 0 {
		return nil, nil
	}
	type item struct {
		Label string `json:"label"`
		Value string `json:"value"`
	}
	out := map[string]item{}
	for _, s := range list {
		v, err := secretResolve(secretRefPrefix + s.ID)
		if err != nil {
			// Un secret abîmé ne doit pas faire échouer toute la sauvegarde.
			fmt.Fprintf(os.Stderr, "sauvegarde : secret %s ignoré : %v\n", s.ID, err)
			continue
		}
		out[s.ID] = item{Label: s.Label, Value: v}
	}
	if len(out) == 0 {
		return nil, nil
	}
	return json.Marshal(out)
}

// secretsImport range dans le coffre local les secrets d'une sauvegarde, sous
// leurs identifiants d'origine (les presets restaurés les désignent par là).
func secretsImport(raw []byte) error {
	var in map[string]struct {
		Label string `json:"label"`
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return fmt.Errorf("secrets de la sauvegarde illisibles : %w", err)
	}
	for id, it := range in {
		if !secretIDRe.MatchString(id) || it.Value == "" {
			continue
		}
		if err := secretPutID(id, it.Label, it.Value); err != nil {
			return err
		}
	}
	return nil
}

// ---- commande et API -----------------------------------------------------------

// cmdSecrets : « ajean secrets [status|on|off] ».
func cmdSecrets(args []string) error {
	sub := "status"
	if len(args) > 0 && args[0] != "" {
		sub = strings.ToLower(args[0])
	}
	printRep := func(rep secretsReport, verb string) {
		fmt.Printf("       %d valeur(s) %s\n", rep.Moved, verb)
		for _, s := range rep.Skipped {
			fmt.Printf("       %s laissé tel quel — %s\n", yellow("[!]"), s)
		}
	}
	switch sub {
	case "on":
		rep, err := secretsEnable()
		if err != nil {
			return err
		}
		fmt.Printf("%s coffre des clés activé\n", green("[ok]"))
		printRep(rep, "déplacée(s) dans le coffre")
		fmt.Printf("       clé du coffre : %s (à garder avec vos sauvegardes)\n", secretsKeyPath())
		fmt.Println(dim("       avant de revenir à une version antérieure à la 0.21 : ajean secrets off"))
		return nil
	case "off":
		rep, err := secretsDisable()
		printRep(rep, "remise(s) en clair")
		if err != nil {
			return err
		}
		fmt.Printf("%s coffre des clés désactivé — les clés sont de nouveau dans les presets\n", yellow("[info]"))
		return nil
	case "status":
		st := secretsStatus()
		if st["on"] == true {
			fmt.Printf("%s coffre des clés activé — %d secret(s)\n", green("[ok]"), st["count"])
		} else {
			fmt.Printf("%s coffre des clés désactivé — %d secret(s) dedans\n", yellow("[info]"), st["count"])
		}
		if st["key_ok"] != true {
			fmt.Printf("%s clé du coffre introuvable ou illisible : %s\n", red("[ERREUR]"), secretsKeyPath())
		}
		for _, s := range secretList() {
			fmt.Printf("       • %s\n", s.Label)
		}
		if n, _ := st["plain"].(int); n > 0 {
			fmt.Printf("       %d clé(s) encore en clair (presets, serveurs MCP)\n", n)
		}
		return nil
	default:
		return fmt.Errorf("usage: ajean secrets [status|on|off]")
	}
}

// handleSecrets : GET → état du coffre (aucune valeur) ; POST {on} → bascule.
func handleSecrets(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var req struct {
			On *bool `json:"on"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.On == nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": "requête illisible"})
			return
		}
		var rep secretsReport
		var err error
		if *req.On {
			rep, err = secretsEnable()
		} else {
			rep, err = secretsDisable()
		}
		st := secretsStatus()
		st["ok"] = err == nil
		st["report"] = rep
		if err != nil {
			st["error"] = err.Error()
			sendJSON(w, 400, st)
			return
		}
		sendJSON(w, 200, st)
		return
	}
	st := secretsStatus()
	st["ok"] = true
	sendJSON(w, 200, st)
}
