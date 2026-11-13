// dictation.go : dictée vocale du chat. Le navigateur enregistre le micro, AJEAN
// relaie l'audio vers une API de transcription compatible OpenAI
// (POST {url}/audio/transcriptions), par exemple Whisper chez Groq, et renvoie le
// texte, qui est inséré dans le champ de saisie.
//
// Réglages rangés dans l'état d'AJEAN (bkState), comme ceux de l'ancienne voix :
// la clé ne repart jamais vers le navigateur (seulement « clé enregistrée »).
// L'audio passe en JSON base64 : c'est ce que le chiffrement de bout en bout
// d'app.ajean.link sait transporter.
package ajean

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"sort"
	"strings"
	"time"
)

const (
	sttDefaultURL   = "https://api.groq.com/openai/v1"
	sttDefaultModel = "whisper-large-v3-turbo"
	sttMaxAudio     = 20 << 20 // limite de taille de fichier chez Groq (offre gratuite : 25 Mo)
)

var sttClient = &http.Client{Timeout: 120 * time.Second}

type sttCfg struct {
	url, key, model, lang string
}

func readSTTCfg() sttCfg {
	get := func(k, def string) string {
		if v := strings.TrimSpace(getStr(bkState, k)); v != "" {
			return v
		}
		return def
	}
	return sttCfg{
		url:   strings.TrimRight(get("stt_url", sttDefaultURL), "/"),
		key:   get("stt_key", ""),
		model: get("stt_model", sttDefaultModel),
		lang:  get("stt_lang", ""),
	}
}

// /api/stt/settings : GET = réglages (sans la clé) ; POST = enregistre.
// Clé : vide = inchangée, "-" = effacée.
func handleSTTSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var b struct {
			URL   string `json:"url"`
			Key   string `json:"key"`
			Model string `json:"model"`
			Lang  string `json:"lang"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&b); err != nil {
			sendJSON(w, 400, map[string]any{"ok": false, "error": "requête invalide"})
			return
		}
		u := strings.TrimRight(strings.TrimSpace(b.URL), "/")
		if u != "" && !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			sendJSON(w, 400, map[string]any{"ok": false, "error": "adresse invalide (http:// ou https://)"})
			return
		}
		_ = putStr(bkState, "stt_url", u)
		_ = putStr(bkState, "stt_model", strings.TrimSpace(b.Model))
		_ = putStr(bkState, "stt_lang", strings.TrimSpace(b.Lang))
		switch k := strings.TrimSpace(b.Key); k {
		case "":
		case "-":
			_ = putStr(bkState, "stt_key", "")
		default:
			_ = putStr(bkState, "stt_key", k)
		}
	}
	c := readSTTCfg()
	sendJSON(w, 200, map[string]any{"ok": true, "url": c.url, "model": c.model, "lang": c.lang,
		"has_key": c.key != "", "default_url": sttDefaultURL, "default_model": sttDefaultModel})
}

// /api/stt : {audio: base64, mime} -> {text}
func handleSTT(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		sendJSON(w, 405, map[string]any{"error": "POST attendu"})
		return
	}
	c := readSTTCfg()
	if c.key == "" && strings.Contains(c.url, "api.groq.com") {
		sendJSON(w, 400, map[string]any{"error": "clé API de transcription manquante (Paramètres > Dictée vocale)"})
		return
	}
	var body struct {
		Audio string `json:"audio"`
		Mime  string `json:"mime"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, sttMaxAudio*4/3+4096)).Decode(&body); err != nil {
		sendJSON(w, 400, map[string]any{"error": "enregistrement trop long ou illisible"})
		return
	}
	raw, err := base64.StdEncoding.DecodeString(body.Audio)
	if err != nil || len(raw) < 100 || len(raw) > sttMaxAudio {
		sendJSON(w, 400, map[string]any{"error": "audio illisible"})
		return
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", c.model)
	_ = mw.WriteField("response_format", "json")
	if c.lang != "" {
		_ = mw.WriteField("language", c.lang)
	}
	fw, _ := mw.CreateFormFile("file", "dictee."+sttExt(body.Mime))
	_, _ = fw.Write(raw)
	_ = mw.Close()
	req, err := http.NewRequestWithContext(r.Context(), "POST", c.url+"/audio/transcriptions", &buf)
	if err != nil {
		sendJSON(w, 400, map[string]any{"error": "adresse de l'API invalide"})
		return
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	if c.key != "" {
		req.Header.Set("Authorization", "Bearer "+c.key)
	}
	resp, err := sttClient.Do(req)
	if err != nil {
		sendJSON(w, 502, map[string]any{"error": "API de transcription injoignable"})
		return
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		msg := strings.TrimSpace(string(out))
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(out, &e) == nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		if len(msg) > 300 {
			msg = msg[:300]
		}
		sendJSON(w, 502, map[string]any{"error": fmt.Sprintf("transcription refusée (%d) : %s", resp.StatusCode, msg)})
		return
	}
	var tr struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(out, &tr)
	sendJSON(w, 200, map[string]any{"text": strings.TrimSpace(tr.Text)})
}

// sttExt : extension attendue par l'API (elle devine le format au nom du fichier).
func sttExt(mime string) string {
	m := strings.ToLower(mime)
	switch {
	case strings.Contains(m, "webm"):
		return "webm"
	case strings.Contains(m, "ogg"):
		return "ogg"
	case strings.Contains(m, "mp4"), strings.Contains(m, "m4a"), strings.Contains(m, "aac"):
		return "m4a"
	case strings.Contains(m, "mpeg"), strings.Contains(m, "mp3"):
		return "mp3"
	case strings.Contains(m, "wav"):
		return "wav"
	}
	return "webm"
}

// /api/stt/models : {url, key} -> {models: [...]}. Lit {url}/models de l'API.
// Clé vide = celle déjà enregistrée. Les modèles audio passent en premier.
func handleSTTModels(w http.ResponseWriter, r *http.Request) {
	var b struct {
		URL string `json:"url"`
		Key string `json:"key"`
	}
	if r.Method == http.MethodPost {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&b)
	}
	c := readSTTCfg()
	u := strings.TrimRight(strings.TrimSpace(b.URL), "/")
	if u == "" {
		u = c.url
	}
	if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
		sendJSON(w, 400, map[string]any{"error": "adresse invalide"})
		return
	}
	key := strings.TrimSpace(b.Key)
	// La clé enregistrée ne repart que vers l'hôte avec lequel elle a été
	// enregistrée, jamais vers une adresse fournie par la requête.
	if key == "" && sameURLHost(u, c.url) {
		key = c.key
	}
	req, err := http.NewRequestWithContext(r.Context(), "GET", u+"/models", nil)
	if err != nil {
		sendJSON(w, 400, map[string]any{"error": "adresse invalide"})
		return
	}
	if key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		sendJSON(w, 502, map[string]any{"error": "API injoignable"})
		return
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		sendJSON(w, 502, map[string]any{"error": fmt.Sprintf("liste refusée (%d)", resp.StatusCode)})
		return
	}
	var lst struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(out, &lst)
	audio, other := []string{}, []string{}
	for _, m := range lst.Data {
		id := strings.TrimSpace(m.ID)
		if id == "" {
			continue
		}
		l := strings.ToLower(id)
		if strings.Contains(l, "whisper") || strings.Contains(l, "transcri") || strings.Contains(l, "voxtral") || strings.Contains(l, "asr") || strings.Contains(l, "stt") {
			audio = append(audio, id)
		} else {
			other = append(other, id)
		}
	}
	sort.Strings(audio)
	sort.Strings(other)
	sendJSON(w, 200, map[string]any{"audio": audio, "other": other})
}
