package apps

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"
	"time"

	"ajean/internal/config"
	"ajean/internal/home"
	"ajean/internal/llm"
	"ajean/internal/scripts"
)

const (
	maxKeys    = 200     // clés du stockage d'une app
	maxKey     = 200     // octets d'une clé
	maxValue   = 1 << 20 // octets d'une valeur, en JSON
	maxStorage = 5 << 20 // octets du stockage entier
	maxErrors  = 20      // erreurs JavaScript gardées par app
	maxMessage = 2000    // caractères d'une erreur
	maxPrompt  = 20000   // caractères d'une demande au modèle
	maxAsks    = 20      // demandes au modèle par app et par minute
)

// askTimeout : durée maximale d'une réponse du modèle à une app.
var askTimeout = 2 * time.Minute

// askSystem : la consigne du modèle quand c'est une app qui l'interroge.
const askSystem = "You are answering a request sent by a small app, not a person in a chat. " +
	"Reply directly with the answer only, in the format the request asks for: no preamble, no follow-up question. " +
	"You have no tools and no memory here."

// jsError : une erreur JavaScript remontée par une app ouverte.
type jsError struct {
	At      time.Time `json:"at"`
	Version int       `json:"version"` // version de l'app qui l'a produite
	Message string    `json:"message"`
}

// Bridge exécute une demande de l'app ouverte. op : storage.get, storage.set,
// storage.remove, storage.keys, tools.run, ai.ask ou error. L'identifiant est
// celui de l'app que la page d'AJEAN a ouverte, jamais un identifiant donné
// par l'app. Le résultat est sérialisable en JSON.
func Bridge(ctx context.Context, id, op string, args map[string]any) (any, error) {
	switch op {
	case "storage.get", "storage.set", "storage.remove", "storage.keys":
		mu.Lock()
		defer mu.Unlock()
		return storage(id, op, args)
	case "tools.run":
		return runTool(id, args)
	case "ai.ask":
		return ask(ctx, id, args)
	case "error":
		message, _ := args["message"].(string)
		return nil, recordError(id, message)
	}
	return nil, fmt.Errorf("opération inconnue : %q", op)
}

// ---- stockage ---------------------------------------------------------------------

func storage(id, op string, args map[string]any) (any, error) {
	if _, err := load(id); err != nil {
		return nil, err
	}
	data := map[string]any{}
	raw, err := os.ReadFile(dataFile(id, "storage.json"))
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		// Illisible : on s'arrête là plutôt que d'écraser les données de
		// l'utilisateur à la prochaine écriture.
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, fmt.Errorf("données de l'app illisibles (%w)", err)
		}
	}
	if op == "storage.keys" {
		keys := make([]string, 0, len(data))
		for k := range data {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return keys, nil
	}
	key, _ := args["key"].(string)
	if key == "" || len(key) > maxKey {
		return nil, fmt.Errorf("storage : 'key' doit être un texte non vide de %d octets au plus", maxKey)
	}
	switch op {
	case "storage.get":
		return data[key], nil
	case "storage.remove":
		if _, ok := data[key]; !ok {
			return nil, nil
		}
		delete(data, key)
	default:
		value, ok := args["value"]
		if !ok {
			return nil, errors.New("storage.set : 'value' manquante (undefined ne se garde pas, null oui)")
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("storage.set : valeur non sérialisable (%w)", err)
		}
		if len(encoded) > maxValue {
			return nil, fmt.Errorf("storage.set : valeur trop grosse (%d octets, max %d)", len(encoded), maxValue)
		}
		if _, exists := data[key]; !exists && len(data) >= maxKeys {
			return nil, fmt.Errorf("storage.set : trop de clés (max %d)", maxKeys)
		}
		data[key] = value
	}
	raw, err = json.Marshal(data)
	if err != nil {
		return nil, err
	}
	if len(raw) > maxStorage {
		return nil, fmt.Errorf("storage.set : stockage plein (max %d octets en tout)", maxStorage)
	}
	return nil, home.WriteFile(dataFile(id, "storage.json"), raw)
}

// ---- outils et modèle -------------------------------------------------------------

// runTool lance un outil de l'assistant, à condition que l'app le déclare :
// c'est l'assistant, à la création, qui décide de ce qu'une app peut atteindre.
func runTool(id string, args map[string]any) (any, error) {
	mu.Lock()
	m, err := load(id)
	mu.Unlock()
	if err != nil {
		return nil, err
	}
	name, _ := args["name"].(string)
	if !slices.Contains(m.Tools, name) {
		return nil, fmt.Errorf("tools.run : l'outil %q n'est pas déclaré par cette app", name)
	}
	params, _ := args["args"].(map[string]any)
	return scripts.Run(name, params)
}

// ask demande une réponse au modèle : un seul appel, sans outils ni mémoire.
// asked : les instants des dernières demandes au modèle de chaque app (protégé
// par mu). Une app qui boucle ne doit pas vider le crédit de l'utilisateur.
var asked = map[string][]time.Time{}

// allowAsk dit si l'app peut encore interroger le modèle, et le note. mu tenu.
func allowAsk(id string, now time.Time) bool {
	recent := asked[id][:0]
	for _, t := range asked[id] {
		if now.Sub(t) < time.Minute {
			recent = append(recent, t)
		}
	}
	if len(recent) >= maxAsks {
		asked[id] = recent
		return false
	}
	asked[id] = append(recent, now)
	return true
}

func ask(ctx context.Context, id string, args map[string]any) (any, error) {
	mu.Lock()
	_, err := load(id)
	allowed := err == nil && allowAsk(id, time.Now())
	mu.Unlock()
	if err != nil {
		return nil, err
	}
	if !allowed {
		return nil, fmt.Errorf("ai.ask : trop de demandes (%d par minute au plus), réessaie dans un instant", maxAsks)
	}
	prompt, _ := args["prompt"].(string)
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return nil, errors.New("ai.ask : 'prompt' vide")
	}
	if n := len([]rune(prompt)); n > maxPrompt {
		return nil, fmt.Errorf("ai.ask : demande trop longue (%d caractères, max %d)", n, maxPrompt)
	}
	ctx, cancel := context.WithTimeout(ctx, askTimeout)
	defer cancel()
	reply, err := llm.Chat(ctx, config.Get().Chat(), []llm.Message{
		{Role: "system", Content: askSystem},
		{Role: "user", Content: prompt},
	}, nil, nil)
	if errors.Is(err, context.DeadlineExceeded) {
		return nil, fmt.Errorf("ai.ask : le modèle n'a pas répondu en %s", askTimeout)
	}
	if err != nil {
		return nil, err
	}
	answer := strings.TrimSpace(reply.Content)
	if answer == "" {
		return nil, errors.New("ai.ask : le modèle n'a rien répondu")
	}
	return answer, nil
}

// ---- erreurs JavaScript -----------------------------------------------------------

// loadErrors : les erreurs en attente d'une app. Un fichier absent ou abîmé
// vaut « aucune » : ce ne sont que des indices pour l'assistant.
func loadErrors(id string) []jsError {
	var list []jsError
	if raw, err := os.ReadFile(dataFile(id, "errors.json")); err == nil {
		_ = json.Unmarshal(raw, &list)
	}
	return list
}

// recordError garde une erreur remontée par l'app, pour que l'assistant
// puisse la lire et corriger. Seules les dernières restent.
func recordError(id, message string) (err error) {
	changed := false
	mu.Lock()
	defer func() {
		mu.Unlock()
		if changed {
			notify()
		}
	}()
	m, err := load(id)
	if err != nil {
		return err
	}
	message = strings.TrimSpace(message)
	if message == "" {
		return errors.New("error : 'message' vide")
	}
	if r := []rune(message); len(r) > maxMessage {
		message = string(r[:maxMessage]) + "…"
	}
	list := loadErrors(id)
	// Une erreur qui se répète à chaque image d'une animation ne doit pas
	// chasser les autres.
	if n := len(list); n > 0 && list[n-1].Message == message && list[n-1].Version == m.Version {
		return nil
	}
	list = append(list, jsError{At: now(), Version: m.Version, Message: message})
	if extra := len(list) - maxErrors; extra > 0 {
		list = list[extra:]
	}
	raw, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := home.WriteFile(dataFile(id, "errors.json"), raw); err != nil {
		return err
	}
	changed = true
	return nil
}

// takeErrors rend les erreurs en attente d'une app et, si clear, les efface.
func takeErrors(id string, clear bool) (app App, list []jsError, err error) {
	changed := false
	mu.Lock()
	defer func() {
		mu.Unlock()
		if changed {
			notify()
		}
	}()
	m, err := load(id)
	if err != nil {
		return app, nil, err
	}
	list = loadErrors(id)
	if clear && len(list) > 0 {
		if err := os.Remove(dataFile(id, "errors.json")); err != nil {
			return app, nil, err
		}
		changed = true
	}
	return m.app(id), list, nil
}
