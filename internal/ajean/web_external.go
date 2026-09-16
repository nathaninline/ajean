package ajean

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
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
	payload := map[string]any{
		"model":      model,
		"messages":   []map[string]any{{"role": "user", "content": "ping"}},
		"max_tokens": 1,
		"stream":     false,
	}
	body, _ := json.Marshal(payload)
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	hreq, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		sendJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	hreq.Header.Set("Content-Type", "application/json")
	if key != "" {
		hreq.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := http.DefaultClient.Do(hreq)
	if err != nil {
		sendJSON(w, 200, map[string]any{"ok": false, "error": "injoignable : " + err.Error()})
		return
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(io.LimitReader(resp.Body, 2000))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(rb))
		if m := extractAPIError(rb); m != "" {
			msg = m
		}
		if msg == "" {
			msg = resp.Status
		}
		sendJSON(w, 200, map[string]any{"ok": false, "status": resp.StatusCode, "error": msg})
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
