package ajean

// openai_api.go : API OpenAI (/v1/chat/completions, /v1/completions,
// /v1/models) sur le port de l'interface, pour brancher un IDE ou un agent en
// ligne de commande (OpenCode, Crush, Aider, Continue, Cline, Zed…) sur le
// preset actif.
//
// Le moteur expose déjà /v1 sur son propre port (8080), mais en direct un IDE
// tombe sur trois écueils :
//
//   - les gabarits stricts (Qwen3.x…) refusent un message system ailleurs qu'en
//     tête ou en plusieurs exemplaires (« System message must be at the
//     beginning », HTTP 500) — or OpenCode en envoie deux, et les clients OpenAI
//     récents envoient le rôle « developer », inconnu des gabarits ;
//   - le nom de modèle : il faut deviner celui que le moteur attend, et il
//     change avec le preset (API externe, GPU cloud) ;
//   - AJEAN ne voit pas ces requêtes : son pré-chauffage et sa révision de fond
//     s'intercalent entre deux requêtes de l'IDE et lui volent le cache KV.
//
// Ici, la requête est nettoyée (un seul system en tête, « developer » → system,
// contenus texte aplatis), dirigée vers le preset actif quel qu'il soit, avec
// l'échantillonnage recommandé du preset pour ce que le client ne fixe pas, et
// comptée comme une génération (doLLM) : rien d'AJEAN ne passe devant elle, et
// la conversation du chat n'est re-chauffée qu'une fois l'IDE resté calme
// (llm_rewarm.go). Le flux SSE est recopié tel quel, sans retraduction.
//
// Authentification : la clé API de la machine (Bearer), comme /v1/messages.

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
)

func oaiError(w http.ResponseWriter, code int, kind, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": msg, "type": kind, "code": code}})
}

// oaiModelID : le nom sous lequel l'IDE voit le modèle. Celui du moteur pour une
// API externe (le client peut le reconnaître), sinon un nom stable : changer de
// preset ne casse pas la configuration de l'IDE.
func oaiModelID(ep chatEndpoint) string {
	if ep.External && ep.Model != "" {
		return ep.Model
	}
	return "ajean"
}

// openaiHandler sert /v1/chat/completions, /v1/completions et /v1/models.
func openaiHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		if !antKeyOK(r) {
			oaiError(w, http.StatusUnauthorized, "invalid_request_error", "clé API invalide (Authorization: Bearer <clé API d'AJEAN>)")
			return
		}
		ep := resolveChatEndpoint()
		if ep.URL == "" {
			oaiError(w, http.StatusServiceUnavailable, "server_error", "aucun moteur actif")
			return
		}
		p := r.URL.Path
		if p == "/v1/models" || strings.HasPrefix(p, "/v1/models/") {
			oaiModels(w, r, ep)
			return
		}
		if r.Method != http.MethodPost {
			oaiError(w, http.StatusMethodNotAllowed, "invalid_request_error", "POST attendu")
			return
		}
		raw, err := io.ReadAll(io.LimitReader(r.Body, 64<<20))
		if err != nil {
			oaiError(w, http.StatusBadRequest, "invalid_request_error", err.Error())
			return
		}
		var in map[string]any
		if err := json.Unmarshal(raw, &in); err != nil {
			oaiError(w, http.StatusBadRequest, "invalid_request_error", "JSON invalide : "+err.Error())
			return
		}
		url := ep.URL
		if p == "/v1/completions" {
			// Complétion brute (autocomplétion FIM des IDE) : même base d'API.
			url = strings.TrimSuffix(ep.URL, "/chat/completions") + "/completions"
		} else {
			normalizeOAIChat(in)
		}
		oaiFillDefaults(in, ep)
		body, _ := json.Marshal(in)
		ctx := withThirdParty(r.Context())
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			oaiError(w, http.StatusInternalServerError, "server_error", err.Error())
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if ep.External {
			if ep.Key != "" {
				req.Header.Set("Authorization", "Bearer "+ep.Key)
			}
		} else {
			authHeader(req)
		}
		resp, err := doLLM(ctx, req, body, ep)
		if err != nil {
			oaiError(w, http.StatusBadGateway, "server_error", "moteur injoignable : "+err.Error())
			return
		}
		defer resp.Body.Close()
		for _, h := range []string{"Content-Type", "Cache-Control"} {
			if v := resp.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(resp.StatusCode)
		oaiCopyFlush(w, resp.Body)
	})
}

// oaiCopyFlush recopie la réponse au fil de l'eau : chaque morceau du flux SSE
// part tout de suite (premier jeton visible sans attendre un tampon plein).
func oaiCopyFlush(w http.ResponseWriter, body io.Reader) {
	fl, _ := w.(http.Flusher)
	buf := make([]byte, 32<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := w.Write(buf[:n]); werr != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
		if err != nil {
			return
		}
	}
}

// oaiModels : la liste (un seul modèle, celui du preset actif) ou sa fiche.
func oaiModels(w http.ResponseWriter, r *http.Request, ep chatEndpoint) {
	m := map[string]any{
		"id":       oaiModelID(ep),
		"object":   "model",
		"created":  time.Now().Unix(),
		"owned_by": "ajean",
	}
	if n := activePresetName(); n != "" {
		m["name"] = n
	}
	if c := ctxWindow(); c > 0 {
		// Champ lu par plusieurs clients pour dimensionner leur fenêtre.
		m["context_length"] = c
	}
	w.Header().Set("Content-Type", "application/json")
	if strings.HasPrefix(r.URL.Path, "/v1/models/") {
		_ = json.NewEncoder(w).Encode(m)
		return
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": []any{m}})
}

// oaiFillDefaults : le modèle du preset (le client ne peut pas le connaître), et
// l'échantillonnage recommandé du preset pour les seuls champs que le client ne
// fixe pas lui-même.
func oaiFillDefaults(in map[string]any, ep chatEndpoint) {
	in["model"] = ep.Model
	if _, ok := in["max_tokens"]; !ok {
		if v, ok := in["max_completion_tokens"]; ok {
			in["max_tokens"] = v
		}
	}
	preset := map[string]any{}
	applySampling(preset)
	for k, v := range preset {
		if _, set := in[k]; !set {
			in[k] = v
		}
	}
	// Appels d'outils parallèles : certains moteurs les sérialisent en deux JSON
	// collés que leur propre analyseur refuse (HTTP 500, voir runChat). Un à la
	// fois, sauf si le client a choisi.
	if _, hasTools := in["tools"]; hasTools {
		if _, set := in["parallel_tool_calls"]; !set {
			in["parallel_tool_calls"] = false
		}
	}
}

// normalizeOAIChat rend les messages acceptables par n'importe quel gabarit :
// un seul system, en tête ; « developer » traité comme system ; contenus faits
// uniquement de texte aplatis en chaîne (les images restent des parts).
func normalizeOAIChat(in map[string]any) {
	list, ok := in["messages"].([]any)
	if !ok {
		return
	}
	var sys []string
	out := make([]any, 0, len(list)+1)
	for _, it := range list {
		m, ok := it.(map[string]any)
		if !ok {
			out = append(out, it)
			continue
		}
		role, _ := m["role"].(string)
		if role == "developer" {
			role = "system"
			m["role"] = role
		}
		if s, ok := oaiFlatText(m["content"]); ok {
			m["content"] = s
		}
		if role == "system" {
			if s, ok := m["content"].(string); ok {
				if strings.TrimSpace(s) != "" {
					sys = append(sys, s)
				}
				continue
			}
		}
		out = append(out, m)
	}
	if len(sys) > 0 {
		out = append([]any{map[string]any{"role": "system", "content": strings.Join(sys, "\n\n")}}, out...)
	}
	in["messages"] = out
}

// oaiFlatText : le contenu en simple chaîne s'il n'est fait que de texte.
// ok=false pour une chaîne déjà simple, un contenu nul ou un contenu avec image.
func oaiFlatText(c any) (string, bool) {
	parts, ok := c.([]any)
	if !ok {
		return "", false
	}
	var texts []string
	for _, p := range parts {
		pm, ok := p.(map[string]any)
		if !ok {
			return "", false
		}
		switch pm["type"] {
		case "text", "input_text":
			if t, _ := pm["text"].(string); t != "" {
				texts = append(texts, t)
			}
		default:
			return "", false
		}
	}
	return strings.Join(texts, "\n\n"), true
}
