package ajean

// Réglages propres à un modèle qui ne relèvent pas du moteur : prompt système,
// échantillonnage, niveau de réflexion, benchmark. Ils étaient rangés par
// preset ; un service issu d'un preset garde les mêmes clés (promptKey = id du
// preset), donc rien n'est perdu à la conversion.

import (
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"
)

// cfgKeyServiceLocal : le service du chat est un moteur lancé par AJEAN
// (llama.cpp, ExLlama, MoE…) : on garde les comportements « moteur local »
// (préchauffage du cache, chat_template_kwargs), comme avec les presets.
const cfgKeyServiceLocal = "AJEAN_SERVICE_LOCAL"

// chatParamKeys : réglages envoyés avec chaque requête de chat.
var chatParamKeys = []string{"TEMP", "TOP_P", "TOP_K", "MIN_P", "PRESENCE_PENALTY", "REPEAT_PENALTY", "REASONING_EFFORT"}

// promptKey : clé du prompt système et du bench de ce modèle.
func (s *Service) promptKey() string {
	if s.Preset != "" {
		return s.Preset
	}
	return "svc-" + s.ID
}

// explicitChatService : le service choisi pour le chat (nil tant que le chat
// tourne encore sur un preset).
func explicitChatService() *Service {
	id := strings.TrimSpace(ReadConfig()[cfgKeyService])
	if id == "" {
		return nil
	}
	s, err := GetService(id)
	if err != nil {
		return nil
	}
	return &s
}

// chatParamsFromConfig : les réglages chat d'une config de preset.
func chatParamsFromConfig(cfg map[string]string) map[string]string {
	m := map[string]string{}
	for _, k := range chatParamKeys {
		if v := strings.TrimSpace(cfg[k]); v != "" {
			m[k] = v
		}
	}
	if len(m) == 0 {
		return nil
	}
	return m
}

// setServiceChatParam : retouche à chaud (raccourci « niveau de réflexion »)
// reportée dans la définition du service du chat.
func setServiceChatParam(key, val string) {
	s := explicitChatService()
	if s == nil {
		return
	}
	if s.ChatParams == nil {
		s.ChatParams = map[string]string{}
	}
	if val == "" {
		delete(s.ChatParams, key)
	} else {
		s.ChatParams[key] = val
	}
	_ = PutService(*s)
}

// backfillServicesFromPresets : services convertis avant que le lien au preset
// soit enregistré : on retrouve le preset (même identifiant) et ses réglages chat.
func backfillServicesFromPresets() {
	list, err := LoadServices()
	if err != nil || len(list) == 0 {
		return
	}
	presets, _ := ListPresets()
	changed := false
	// Une fois : ordre de la liste = ordre des presets (la 1re conversion triait
	// par identifiant).
	if !getBool(bkState, "services_order_from_presets") {
		rank := map[string]int{}
		for i, p := range presets {
			rank[serviceSlug(p.ID)] = i
		}
		sort.SliceStable(list, func(i, j int) bool {
			ri, okI := rank[list[i].ID]
			rj, okJ := rank[list[j].ID]
			if okI != okJ {
				return okI
			}
			return okI && ri < rj
		})
		_ = putBool(bkState, "services_order_from_presets", true)
		changed = true
	}
	for i := range list {
		s := &list[i]
		if s.Preset != "" && s.Quant == "" && s.Reasoning == "" {
			if content, err := ReadPreset(s.Preset); err == nil {
				s.Quant, s.Reasoning = presetTags(content, parseEnv(content))
				changed = s.Quant != "" || s.Reasoning != "" || changed
			}
		}
		if s.Preset != "" {
			continue
		}
		for _, p := range presets {
			if serviceSlug(p.ID) != s.ID {
				continue
			}
			content, err := ReadPreset(p.ID)
			if err != nil {
				break
			}
			s.Preset = p.ID
			s.Quant, s.Reasoning = presetTags(content, parseEnv(content))
			if s.ChatParams == nil {
				s.ChatParams = chatParamsFromConfig(parseEnv(content))
			}
			changed = true
			break
		}
	}
	if changed {
		servicesMu.Lock()
		_ = saveServices(list)
		servicesMu.Unlock()
	}
}

// serviceBench : dernier bench mesuré sur ce modèle (nil si aucun).
func serviceBench(s *Service) map[string]any {
	sb, ok := loadBenchStore()[s.promptKey()]
	if !ok || sb.Model != "svc:"+s.ID {
		return nil
	}
	return map[string]any{"prefill": sb.Result.PromptPerSecond, "decode": sb.Result.PredictedPerSec, "at": sb.At}
}

func saveServiceBench(s *Service, res *benchResult) {
	m := loadBenchStore()
	m[s.promptKey()] = savedBench{Result: *res, Model: "svc:" + s.ID, At: time.Now().Unix()}
	_ = putJSON(bkState, "bench_presets", m)
	_ = putJSON(bkState, "last_bench", m[s.promptKey()])
}

// handleServiceSysPrompt : GET/POST /api/services/sysprompt?id=
func handleServiceSysPrompt(w http.ResponseWriter, r *http.Request) {
	s, err := GetService(r.URL.Query().Get("id"))
	if err != nil {
		svcErr(w, err)
		return
	}
	if r.Method == http.MethodPost {
		var body struct {
			Text string `json:"text"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
			svcErr(w, err)
			return
		}
		if err := setPresetSysPrompt(s.promptKey(), body.Text); err != nil {
			svcErr(w, err)
			return
		}
	}
	svcJSON(w, 200, map[string]any{"ok": true, "text": presetSysPrompt(s.promptKey())})
}

// serviceByRef : le modèle désigné par son id, son ancien preset ou sa clé.
func serviceByRef(ref string) *Service {
	list, err := LoadServices()
	if err != nil {
		return nil
	}
	for i := range list {
		s := &list[i]
		if s.ID == ref || s.Preset == ref || s.promptKey() == ref || s.ID == serviceSlug(ref) {
			return s
		}
	}
	return nil
}

// modelChoices : liste des modèles pour les sélecteurs (tâches, sommeil). L'id
// est la clé du modèle, la même que celle des anciens presets.
func modelChoices() []map[string]any {
	out := []map[string]any{}
	list, _ := LoadServices()
	chat := ChatServiceID()
	for i := range list {
		s := &list[i]
		out = append(out, map[string]any{"id": s.promptKey(), "name": s.Name, "active": s.ID == chat})
	}
	return out
}
