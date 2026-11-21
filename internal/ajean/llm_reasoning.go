package ajean

import (
	"net/url"
	"strings"
	"sync"
)

// llm_reasoning.go — niveau de réflexion d'un preset API ou Service.
//
// Le preset porte un niveau (REASONING_EFFORT : off, low, medium, high ; vide =
// rien n'est envoyé, le fournisseur décide). Il n'existe pas de paramètre commun :
// chaque fournisseur a le sien. reasoningParams traduit donc le niveau selon
// l'adresse de l'API, et runChat renvoie la requête SANS ces champs si le
// fournisseur les refuse (voir reasoningRejected) : un fournisseur mal classé ne
// casse jamais un tour, le réglage reste seulement sans effet, et l'interface le
// dit sous la réponse.

// reasoningLevels : niveaux acceptés pour une API. xhigh reste accepté pour les
// presets écrits avant la 0.20 (il part tel quel dans reasoning_effort).
var reasoningLevels = map[string]bool{"off": true, "low": true, "medium": true, "high": true, "xhigh": true}

// reasoningProvider classe l'adresse d'une API : "alibaba", "openrouter",
// "thinking" (objet thinking : Z.ai, DeepSeek), "local" (serveur sur cette
// machine : vLLM, llama-server…) ou "" (champ OpenAI reasoning_effort).
func reasoningProvider(base string) string {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Hostname() == "" {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	has := func(suffixes ...string) bool {
		for _, s := range suffixes {
			if h == s || strings.HasSuffix(h, "."+s) {
				return true
			}
		}
		return false
	}
	switch {
	case has("aliyuncs.com", "aliyun.com", "alibabacloud.com"):
		return "alibaba"
	case has("openrouter.ai"):
		return "openrouter"
	case has("z.ai", "bigmodel.cn", "deepseek.com"):
		return "thinking"
	case !remoteEndpoint(base):
		return "local"
	}
	return ""
}

// reasoningParams renvoie les champs à ajouter à une requête de complétion pour
// obtenir ce niveau chez ce fournisseur. nil = rien à envoyer.
func reasoningParams(base, level string) map[string]any {
	level = strings.ToLower(strings.TrimSpace(level))
	if !reasoningLevels[level] {
		return nil
	}
	off := level == "off"
	switch reasoningProvider(base) {
	case "alibaba":
		// Qwen : interrupteur + plafond de tokens de réflexion. « high » ne pose
		// pas de plafond : le modèle prend ce qu'il lui faut.
		if off {
			return map[string]any{"enable_thinking": false}
		}
		p := map[string]any{"enable_thinking": true}
		switch level {
		case "low":
			p["thinking_budget"] = 1024
		case "medium":
			p["thinking_budget"] = 8192
		}
		return p
	case "openrouter":
		// OpenRouter traduit lui-même vers le fournisseur du modèle choisi.
		if off {
			return map[string]any{"reasoning": map[string]any{"effort": "none"}}
		}
		return map[string]any{"reasoning": map[string]any{"effort": level}}
	case "thinking":
		// Interrupteur seul : ces API n'ont pas de niveaux.
		if off {
			return map[string]any{"thinking": map[string]any{"type": "disabled"}}
		}
		return map[string]any{"thinking": map[string]any{"type": "enabled"}}
	case "local":
		// Serveur sur la machine (vLLM, llama-server, TabbyAPI…) : le niveau passe
		// par le gabarit de conversation du modèle.
		if off {
			return map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": false}}
		}
		return map[string]any{"chat_template_kwargs": map[string]any{"enable_thinking": true, "reasoning_effort": level}}
	}
	// Champ OpenAI (OpenAI, Groq, Gemini en mode compatible, Mistral…).
	if off {
		return map[string]any{"reasoning_effort": "none"}
	}
	return map[string]any{"reasoning_effort": level}
}

// reasoningKeys : tous les champs que reasoningParams peut poser.
var reasoningKeys = []string{"reasoning_effort", "reasoning", "thinking", "enable_thinking", "thinking_budget", "chat_template_kwargs"}

// stripReasoning retire d'une requête les champs de réflexion. Renvoie false
// s'il n'y en avait aucun.
func stripReasoning(payload map[string]any) bool {
	found := false
	for _, k := range reasoningKeys {
		if _, ok := payload[k]; ok {
			delete(payload, k)
			found = true
		}
	}
	return found
}

// Fournisseurs qui ont refusé les champs de réflexion depuis le démarrage, par
// adresse et modèle : on ne les leur renvoie plus (une requête perdue par tour
// sinon). Oublié au redémarrage et dès que le niveau du preset change.
var (
	reasoningRejMu sync.Mutex
	reasoningRej   = map[string]string{} // adresse+modèle → niveau refusé
)

func reasoningRejKey(ep chatEndpoint) string { return ep.URL + "\n" + ep.Model }

func reasoningRejected(ep chatEndpoint, level string) bool {
	reasoningRejMu.Lock()
	defer reasoningRejMu.Unlock()
	return level != "" && reasoningRej[reasoningRejKey(ep)] == level
}

func markReasoningRejected(ep chatEndpoint, level string) {
	reasoningRejMu.Lock()
	reasoningRej[reasoningRejKey(ep)] = level
	reasoningRejMu.Unlock()
}

// reasoningRefusal : ce code HTTP peut-il être le refus d'un paramètre ?
// (400 et 422 : requête invalide.)
func reasoningRefusal(code int) bool { return code == 400 || code == 422 }
