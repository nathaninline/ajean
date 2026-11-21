package ajean

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// web_external.go — endpoints /api/preset/external/* : lire, enregistrer et
// tester un preset « externe » (API OpenAI-compatible distante). L'UI les pilote
// depuis la modale ouverte par le bouton wifi, à gauche du « + » des presets.

// handlePresetExternal renvoie les champs d'un preset externe pour préremplir la
// modale d'édition. id vide = nouveau preset (champs vides).
func handlePresetExternal(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		sendJSON(w, 200, map[string]any{"id": "", "name": "", "url": "", "model": "", "hasKey": false})
		return
	}
	content, err := ReadPreset(id)
	if err != nil {
		sendJSON(w, 404, map[string]any{"error": "not found"})
		return
	}
	cfg := parseEnv(content)
	if !isExternalConfig(cfg) {
		sendJSON(w, 400, map[string]any{"error": "pas un preset externe"})
		return
	}
	// La clé n'est PAS renvoyée en clair : on signale seulement sa présence, pour
	// que la modale montre « clé enregistrée » sans l'exposer. Un ré-enregistrement
	// sans toucher au champ clé la conserve (voir handlePresetExternalSave).
	sendJSON(w, 200, map[string]any{
		"id":     id,
		"name":   presetDisplayName(content, id),
		"url":    strings.TrimSpace(cfg[extKeyURL]),
		"model":  strings.TrimSpace(cfg[extKeyModel]),
		"ctx":    strings.TrimSpace(cfg["CTX"]),
		"vision": strings.TrimSpace(cfg[extKeyVision]) == "1",
		"hasKey": strings.TrimSpace(cfg[extKeyToken]) != "",
	})
}

// externalSaveReq : payload de la modale externe. `key` vide sur une ÉDITION
// conserve la clé existante (voir keyTouched) ; sur une création, absence de clé
// = pas d'authentification.
type externalSaveReq struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	URL        string `json:"url"`
	Model      string `json:"model"`
	Key        string `json:"key"`
	Ctx        string `json:"ctx"`        // taille de contexte (clé CTX), optionnelle
	Vision     bool   `json:"vision"`     // le modèle distant accepte les images
	KeyTouched bool   `json:"keyTouched"` // l'utilisateur a modifié le champ clé
	Effort     string `json:"effort"`     // niveau de réflexion (test de connexion)
}

func handlePresetExternalSave(w http.ResponseWriter, r *http.Request) {
	var req externalSaveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	req.Model = strings.TrimSpace(req.Model)
	if req.Name == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "nom requis"})
		return
	}
	if req.URL == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "URL requise"})
		return
	}
	if req.Model == "" {
		sendJSON(w, 400, map[string]any{"ok": false, "error": "modèle requis"})
		return
	}
	// Clé : sur une édition sans toucher au champ, on relit la clé déjà stockée
	// pour ne pas l'effacer (la modale ne la reçoit jamais en clair).
	key := req.Key
	if req.ID != "" && !req.KeyTouched {
		if content, err := ReadPreset(req.ID); err == nil {
			key = strings.TrimSpace(parseEnv(content)[extKeyToken])
		}
	}
	content := externalPresetContent(req.URL, req.Model, key, req.Ctx, req.Vision)
	// Réglages de MACHINE repris dans le preset : sans eux, la bascule vers ce
	// preset effaçait BIN de la config vive (voir externalMachineKeys).
	content = externalPresetWithMachine(content, externalMachineKeys(req.ID))
	// EXTERNAL_SERVICE n'a pas de champ dans la modale : une édition le conserve.
	if req.ID != "" {
		if old, err := ReadPreset(req.ID); err == nil {
			if svc := strings.TrimSpace(parseEnv(old)[extKeyService]); svc != "" {
				content = strings.TrimRight(content, "\n") + "\n" + extKeyService + "=" + svc + "\n"
			}
		}
	}
	newID, err := SavePreset(req.ID, req.Name, content)
	if err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "id": newID, "name": req.Name})
}

// handlePresetExternalTest tente un appel de complétion minimal vers l'endpoint
// saisi, pour valider URL + modèle + clé avant d'enregistrer. Sur une édition
// sans clé fournie, on réutilise la clé stockée.
func handlePresetExternalTest(w http.ResponseWriter, r *http.Request) {
	var req externalSaveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	url := completionsURL(req.URL)
	model := strings.TrimSpace(req.Model)
	if url == "" || model == "" {
		sendJSON(w, 200, map[string]any{"ok": false, "error": "URL et modèle requis"})
		return
	}
	key := strings.TrimSpace(req.Key)
	if key == "" && req.ID != "" && !req.KeyTouched {
		if content, err := ReadPreset(req.ID); err == nil {
			key = strings.TrimSpace(parseEnv(content)[extKeyToken])
		}
	}
	// Clé rangée dans le coffre : la référence se résout ici, pour l'essai.
	if k, err := secretResolve(key); err != nil {
		sendJSON(w, 200, map[string]any{"ok": false, "error": "clé illisible dans le coffre : " + err.Error()})
		return
	} else {
		key = k
	}
	payload := map[string]any{
		"model":      model,
		"messages":   []map[string]any{{"role": "user", "content": "ping"}},
		"max_tokens": 1,
		"stream":     false,
	}
	// Le niveau de réflexion du preset part avec le test : c'est le moyen de
	// savoir, avant de discuter, si ce fournisseur l'accepte.
	withReasoning := false
	for k, v := range reasoningParams(req.URL, req.Effort) {
		payload[k] = v
		withReasoning = true
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	post := func() (int, string, error) {
		body, _ := json.Marshal(payload)
		hreq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
		if err != nil {
			return 0, "", err
		}
		hreq.Header.Set("Content-Type", "application/json")
		if key != "" {
			hreq.Header.Set("Authorization", "Bearer "+key)
		}
		resp, err := http.DefaultClient.Do(hreq)
		if err != nil {
			return 0, "", extModelsError("injoignable : " + err.Error())
		}
		defer resp.Body.Close()
		rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
		msg := strings.TrimSpace(string(rb))
		if m := extractAPIError(rb); m != "" {
			msg = m
		}
		if msg == "" {
			msg = resp.Status
		}
		return resp.StatusCode, msg, nil
	}
	code, msg, err := post()
	reasoningIgnored := false
	if err == nil && withReasoning && reasoningRefusal(code) {
		// Refus : la même requête sans le niveau de réflexion dit si c'est lui
		// que le fournisseur n'accepte pas.
		refusal := msg
		stripReasoning(payload)
		if code, msg, err = post(); err == nil && code == http.StatusOK {
			reasoningIgnored = true
			msg = refusal
		}
	}
	if err != nil {
		sendJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	if code != http.StatusOK {
		sendJSON(w, 200, map[string]any{"ok": false, "status": code, "error": msg})
		return
	}
	if reasoningIgnored {
		sendJSON(w, 200, map[string]any{"ok": true, "reasoningIgnored": true, "reasoningError": msg})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true})
}

// extractAPIError sort le champ error.message d'une réponse d'erreur au format
// OpenAI ({"error":{"message":"…"}}), pour un message lisible plutôt que le JSON
// brut. Renvoie "" si le format ne correspond pas.
func extractAPIError(b []byte) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &e) == nil {
		return strings.TrimSpace(e.Error.Message)
	}
	return ""
}

// handlePresetExternalModels : les modèles que l'API saisie annonce (GET
// <base>/models), pour que l'éditeur de preset propose une liste plutôt qu'un
// identifiant à recopier. Liste vide ou erreur : l'éditeur garde la saisie libre.
func handlePresetExternalModels(w http.ResponseWriter, r *http.Request) {
	var req externalSaveReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		sendJSON(w, 400, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// Édition sans toucher au champ clé : la clé enregistrée sert, comme pour le
	// test de connexion (la modale ne la reçoit jamais en clair).
	key := strings.TrimSpace(req.Key)
	if key == "" && req.ID != "" && !req.KeyTouched {
		if content, err := ReadPreset(req.ID); err == nil {
			key = strings.TrimSpace(parseEnv(content)[extKeyToken])
		}
	}
	models, err := externalModels(r.Context(), req.URL, key)
	if err != nil {
		sendJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"ok": true, "models": models})
}

type extModelsError string

func (e extModelsError) Error() string { return string(e) }

// externalModels interroge <base>/models et renvoie les identifiants, triés et
// sans doublon. Accepte la forme OpenAI ({"data":[{"id":…}]}), un tableau nu,
// et la forme {"models":[{"id"|"name":…}]} de quelques serveurs.
func externalModels(ctx context.Context, base, key string) ([]string, error) {
	api := strings.TrimSuffix(completionsURL(base), "/chat/completions")
	if api == "" {
		return nil, extModelsError("URL requise")
	}
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	hreq, err := http.NewRequestWithContext(ctx, "GET", api+"/models", nil)
	if err != nil {
		return nil, err
	}
	if key = strings.TrimSpace(key); key != "" {
		hreq.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(hreq)
	if err != nil {
		return nil, extModelsError("injoignable : " + err.Error())
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		if m := extractAPIError(rb); m != "" {
			return nil, extModelsError(m)
		}
		return nil, extModelsError(resp.Status)
	}
	type entry struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	var obj struct {
		Data   []entry `json:"data"`
		Models []entry `json:"models"`
	}
	var entries []entry
	if json.Unmarshal(rb, &obj) == nil {
		entries = append(obj.Data, obj.Models...)
	} else if json.Unmarshal(rb, &entries) != nil {
		return nil, extModelsError("réponse illisible")
	}
	seen := map[string]bool{}
	out := []string{}
	for _, e := range entries {
		id := strings.TrimSpace(e.ID)
		if id == "" {
			id = strings.TrimSpace(e.Name)
		}
		if id != "" && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}
