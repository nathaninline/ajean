package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"
)

// Usage : ce que la dernière réponse a coûté en tokens, tel que l'API le compte.
// Prompt est la taille du contexte envoyé : c'est elle qui remplit la fenêtre.
type Usage struct {
	Prompt     int `json:"prompt_tokens"`
	Completion int `json:"completion_tokens"`
}

type usageKey struct{}

// WithUsage renvoie un contexte dont les appels à Chat transmettent à f le
// décompte de tokens envoyé par l'API (quand elle en envoie un).
func WithUsage(ctx context.Context, f func(Usage)) context.Context {
	return context.WithValue(ctx, usageKey{}, f)
}

// noUsage : les API qui ont refusé stream_options. On ne le leur redemande plus.
var noUsage sync.Map

// windows : taille de la fenêtre de contexte, par API et modèle (0 : inconnue).
var windows sync.Map

// ContextWindow : la taille de la fenêtre de contexte du modèle, en tokens, lue
// sur /models (llama.cpp : meta.n_ctx ; vLLM : max_model_len ; OpenRouter :
// context_length). 0 si l'API ne la donne pas. Le résultat est gardé en mémoire.
func ContextWindow(ctx context.Context, ep Endpoint) int {
	k := base(ep.URL) + "\x00" + ep.Model
	if v, ok := windows.Load(k); ok {
		return v.(int)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	resp, err := request(ctx, http.MethodGet, base(ep.URL)+"/models", ep.Key, nil)
	if err != nil {
		// Passager peut-être : on réessaiera dans une minute, pas à chaque appel.
		windows.Store(k, 0)
		time.AfterFunc(time.Minute, func() { windows.Delete(k) })
		return 0
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var list struct {
		Data []map[string]any `json:"data"`
	}
	n := 0
	if json.Unmarshal(b, &list) == nil {
		for _, m := range list.Data {
			if id, _ := m["id"].(string); id == ep.Model {
				n = windowOf(m)
				break
			}
		}
	}
	windows.Store(k, n)
	return n
}

func windowOf(m map[string]any) int {
	num := func(v any) int {
		if f, ok := v.(float64); ok {
			return int(f)
		}
		return 0
	}
	if meta, ok := m["meta"].(map[string]any); ok {
		if n := num(meta["n_ctx"]); n > 0 {
			return n
		}
	}
	for _, k := range []string{"max_model_len", "context_length", "context_window", "max_context_length", "n_ctx"} {
		if n := num(m[k]); n > 0 {
			return n
		}
	}
	return 0
}
