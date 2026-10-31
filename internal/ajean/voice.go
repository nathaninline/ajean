package ajean

// Voix du mode Jean : on lui parle, il répond à voix haute.
//
// AJEAN ne fait pas tourner les modèles audio lui-même : il relaie vers un
// serveur audio.cpp (github.com/0xShug0/audio.cpp) qui expose l'API OpenAI
// audio (/v1/audio/transcriptions, /v1/audio/speech).
//
// Réglages (Paramètres > Voix de Jean), rangés dans l'état d'AJEAN et non dans
// la config : celle-ci est remplacée en entier à chaque bascule de preset.
//
//	voice_on       "0" = coupée ; sinon active dès qu'une adresse est connue
//	voice_url      adresse du serveur audio.cpp (repli : env AJEAN_VOICE_URL)
//	voice_name     voix utilisée (défaut : la première de la bibliothèque)
//	voice_sens     sensibilité du micro : 1 faible, 2 normale, 3 élevée
//	voice_silence  silence (ms) qui termine une phrase dictée
//	voice_stt / voice_tts  ids des modèles dans le serveur audio (défauts asr / voxcpm)
//
// Les voix sont à AJEAN : $AJEAN_HOME/voices/<nom>.wav + <nom>.txt (ce que dit
// l'extrait). Elles partent avec chaque requête (voice_ref en base64), le serveur
// audio n'a donc rien à stocker. Une voix inconnue de la bibliothèque est passée
// telle quelle (voix intégrée au modèle ou au serveur).
//
// Les échanges passent en JSON (audio en base64) : c'est ce que le chiffrement
// de bout en bout d'app.ajean.link sait transporter (comme /api/chat/upload).

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

var voiceClient = &http.Client{Timeout: 180 * time.Second}

type voiceCfg struct {
	on              bool
	url             string
	stt, tts, voice string
	sens, silenceMs int
	urlFromEnv      bool
}

func voiceGet(key, def string) string {
	if v := strings.TrimSpace(getStr(bkState, key)); v != "" {
		return v
	}
	return def
}

func readVoiceCfg() voiceCfg {
	c := voiceCfg{
		url:   strings.TrimRight(voiceGet("voice_url", ""), "/"),
		stt:   voiceGet("voice_stt", "asr"),
		tts:   voiceGet("voice_tts", "voxcpm"),
		voice: voiceGet("voice_name", ""),
	}
	if c.voice == "" {
		if l := voiceList(); len(l) > 0 {
			c.voice = l[0]
		}
	}
	if c.url == "" {
		c.url = strings.TrimRight(strings.TrimSpace(os.Getenv("AJEAN_VOICE_URL")), "/")
		c.urlFromEnv = c.url != ""
	}
	c.sens, _ = strconv.Atoi(voiceGet("voice_sens", "2"))
	if c.sens < 1 || c.sens > 3 {
		c.sens = 2
	}
	c.silenceMs, _ = strconv.Atoi(voiceGet("voice_silence", "800"))
	if c.silenceMs < 300 || c.silenceMs > 4000 {
		c.silenceMs = 800
	}
	c.on = c.url != "" && getStr(bkState, "voice_on") != "0"
	return c
}

// ---- bibliothèque de voix ---------------------------------------------------

func voicesDir() string { return filepath.Join(AjeanHome(), "voices") }

var voiceNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

func voiceList() []string {
	ents, _ := os.ReadDir(voicesDir())
	var out []string
	for _, e := range ents {
		if n := e.Name(); strings.HasSuffix(n, ".wav") && voiceNameRe.MatchString(strings.TrimSuffix(n, ".wav")) {
			out = append(out, strings.TrimSuffix(n, ".wav"))
		}
	}
	sort.Strings(out)
	return out
}

// voiceRef : l'extrait et son texte, si la voix est dans la bibliothèque.
func voiceRef(name string) (wav []byte, text string, ok bool) {
	if !voiceNameRe.MatchString(name) {
		return nil, "", false
	}
	wav, err := os.ReadFile(filepath.Join(voicesDir(), name+".wav"))
	if err != nil {
		return nil, "", false
	}
	t, _ := os.ReadFile(filepath.Join(voicesDir(), name+".txt"))
	return wav, strings.TrimSpace(string(t)), true
}

// ---- appels au serveur audio -------------------------------------------------

func voiceSpeech(ctx context.Context, c voiceCfg, payload map[string]any) ([]byte, error) {
	payload["model"] = c.tts
	payload["response_format"] = "wav"
	b, _ := json.Marshal(payload)
	req, _ := http.NewRequestWithContext(ctx, "POST", c.url+"/v1/audio/speech", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := voiceClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("serveur vocal injoignable")
	}
	defer resp.Body.Close()
	wav, _ := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if resp.StatusCode != 200 {
		msg := strings.TrimSpace(string(wav))
		if len(msg) > 200 {
			msg = msg[:200]
		}
		return nil, fmt.Errorf("synthèse refusée (%d) %s", resp.StatusCode, msg)
	}
	return wav, nil
}

// speakWith : texte lu avec une voix (bibliothèque d'AJEAN, sinon nom passé tel quel).
func speakWith(ctx context.Context, c voiceCfg, voice, text string) ([]byte, error) {
	p := map[string]any{"input": text}
	if wav, ref, ok := voiceRef(voice); ok {
		p["voice_ref"] = map[string]any{"type": "base64", "data": base64.StdEncoding.EncodeToString(wav)}
		if ref != "" {
			p["reference_text"] = ref
		}
	} else if voice != "" {
		p["voice"] = voice
	}
	return voiceSpeech(ctx, c, p)
}

func voiceReachable(url string) bool {
	if url == "" {
		return false
	}
	cl := &http.Client{Timeout: 2 * time.Second}
	resp, err := cl.Get(url + "/health")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode < 500
}

// ---- routes ------------------------------------------------------------------

// /api/voice/status : ce dont le mode vocal a besoin (le micro n'apparaît que si on).
func handleVoiceStatus(w http.ResponseWriter, r *http.Request) {
	c := readVoiceCfg()
	sendJSON(w, 200, map[string]any{"enabled": c.on, "voice": c.voice, "sens": c.sens, "silence_ms": c.silenceMs})
}

// /api/voice/settings : GET = réglages + voix + état du serveur ; POST = enregistre.
func handleVoiceSettings(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" {
		var body map[string]any
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
			sendJSON(w, 400, map[string]any{"error": "requête invalide"})
			return
		}
		set := func(key, val string) { _ = putStr(bkState, key, val) }
		if v, ok := body["on"].(bool); ok {
			if v {
				set("voice_on", "1")
			} else {
				set("voice_on", "0")
			}
		}
		if v, ok := body["url"].(string); ok {
			v = strings.TrimRight(strings.TrimSpace(v), "/")
			if v != "" && !strings.HasPrefix(v, "http://") && !strings.HasPrefix(v, "https://") {
				v = "http://" + v
			}
			set("voice_url", v)
		}
		if v, ok := body["voice"].(string); ok && (v == "" || voiceNameRe.MatchString(v)) {
			set("voice_name", v)
		}
		if v, ok := body["sens"].(float64); ok && v >= 1 && v <= 3 {
			set("voice_sens", strconv.Itoa(int(v)))
		}
		if v, ok := body["silence_ms"].(float64); ok && v >= 300 && v <= 4000 {
			set("voice_silence", strconv.Itoa(int(v)))
		}
		for _, k := range []string{"stt", "tts"} {
			if v, ok := body[k].(string); ok {
				set("voice_"+k, strings.TrimSpace(v))
			}
		}
	}
	c := readVoiceCfg()
	url := c.url
	if c.urlFromEnv {
		url = ""
	}
	sendJSON(w, 200, map[string]any{
		"on": c.on, "url": url, "url_default": os.Getenv("AJEAN_VOICE_URL"),
		"voice": c.voice, "voices": voiceList(), "sens": c.sens, "silence_ms": c.silenceMs,
		"stt": c.stt, "tts": c.tts, "reachable": voiceReachable(c.url),
	})
}

// voiceMaxAudio borne un enregistrement (WAV 16 kHz mono : ~2 min).
const voiceMaxAudio = 4 << 20

// /api/voice/stt : {wav: base64} -> {text}
func handleVoiceSTT(w http.ResponseWriter, r *http.Request) {
	c := readVoiceCfg()
	if c.url == "" {
		sendJSON(w, 400, map[string]any{"error": "voix non configurée"})
		return
	}
	var body struct {
		Wav string `json:"wav"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, voiceMaxAudio*2)).Decode(&body); err != nil {
		sendJSON(w, 400, map[string]any{"error": "requête invalide"})
		return
	}
	raw, err := base64.StdEncoding.DecodeString(body.Wav)
	if err != nil || len(raw) < 44 || len(raw) > voiceMaxAudio {
		sendJSON(w, 400, map[string]any{"error": "audio illisible"})
		return
	}
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	_ = mw.WriteField("model", c.stt)
	fw, _ := mw.CreateFormFile("file", "voix.wav")
	_, _ = fw.Write(raw)
	_ = mw.Close()
	req, _ := http.NewRequestWithContext(r.Context(), "POST", c.url+"/v1/audio/transcriptions", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := voiceClient.Do(req)
	if err != nil {
		sendJSON(w, 502, map[string]any{"error": "serveur vocal injoignable"})
		return
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != 200 {
		sendJSON(w, 502, map[string]any{"error": fmt.Sprintf("transcription refusée (%d)", resp.StatusCode)})
		return
	}
	var tr struct {
		Text string `json:"text"`
	}
	_ = json.Unmarshal(out, &tr)
	sendJSON(w, 200, map[string]any{"text": strings.TrimSpace(tr.Text)})
}

// /api/voice/tts : {text, voice?} -> {wav: base64}. Sans voice : la voix réglée.
func handleVoiceTTS(w http.ResponseWriter, r *http.Request) {
	c := readVoiceCfg()
	if c.url == "" {
		sendJSON(w, 400, map[string]any{"error": "voix non configurée"})
		return
	}
	var body struct {
		Text  string `json:"text"`
		Voice string `json:"voice"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10)).Decode(&body); err != nil {
		sendJSON(w, 400, map[string]any{"error": "requête invalide"})
		return
	}
	text := strings.TrimSpace(body.Text)
	if text == "" {
		sendJSON(w, 400, map[string]any{"error": "texte vide"})
		return
	}
	if len([]rune(text)) > 1200 {
		text = string([]rune(text)[:1200])
	}
	voice := c.voice
	if v := strings.TrimSpace(body.Voice); v != "" {
		voice = v
	}
	wav, err := speakWith(r.Context(), c, voice, text)
	if err != nil {
		sendJSON(w, 502, map[string]any{"error": err.Error()})
		return
	}
	sendJSON(w, 200, map[string]any{"wav": base64.StdEncoding.EncodeToString(wav)})
}

// voiceDesignSample : ce que dit une voix créée ; il devient aussi le texte de
// référence de l'extrait (le modèle s'en sert pour cloner la voix ensuite).
const voiceDesignSample = "Bonjour, je suis ravi de vous parler. Voici à quoi ressemble ma voix, et je serai là pour vous aider au quotidien."

// /api/voice/design : {name, description} -> crée (ou recrée) une voix de la
// bibliothèque à partir d'une description, et renvoie l'extrait {wav}.
func handleVoiceDesign(w http.ResponseWriter, r *http.Request) {
	c := readVoiceCfg()
	if c.url == "" {
		sendJSON(w, 400, map[string]any{"error": "voix non configurée"})
		return
	}
	var body struct {
		Name        string `json:"name"`
		Description string `json:"description"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&body); err != nil {
		sendJSON(w, 400, map[string]any{"error": "requête invalide"})
		return
	}
	name := strings.ToLower(strings.TrimSpace(body.Name))
	desc := strings.Trim(strings.TrimSpace(body.Description), "()")
	if !voiceNameRe.MatchString(name) {
		sendJSON(w, 400, map[string]any{"error": "nom invalide (lettres minuscules, chiffres, - ou _)"})
		return
	}
	if desc == "" || len([]rune(desc)) > 400 {
		sendJSON(w, 400, map[string]any{"error": "description vide ou trop longue"})
		return
	}
	// VoxCPM2 : la description entre parenthèses en tête du texte crée la voix ;
	// sans extrait de référence (ultimate_clone coupé).
	wav, err := voiceSpeech(r.Context(), c, map[string]any{
		"input":   "(" + desc + ") " + voiceDesignSample,
		"options": map[string]any{"ultimate_clone": false},
	})
	if err != nil {
		sendJSON(w, 502, map[string]any{"error": err.Error()})
		return
	}
	if err := os.MkdirAll(voicesDir(), 0o755); err == nil {
		err = os.WriteFile(filepath.Join(voicesDir(), name+".wav"), wav, 0o644)
		if err == nil {
			err = os.WriteFile(filepath.Join(voicesDir(), name+".txt"), []byte(voiceDesignSample+"\n"), 0o644)
		}
		if err != nil {
			sendJSON(w, 500, map[string]any{"error": "enregistrement impossible : " + err.Error()})
			return
		}
	}
	sendJSON(w, 200, map[string]any{"name": name, "wav": base64.StdEncoding.EncodeToString(wav)})
}

// /api/voice/delete : {name} -> retire une voix de la bibliothèque.
func handleVoiceDelete(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10)).Decode(&body); err != nil || !voiceNameRe.MatchString(body.Name) {
		sendJSON(w, 400, map[string]any{"error": "nom invalide"})
		return
	}
	_ = os.Remove(filepath.Join(voicesDir(), body.Name+".wav"))
	_ = os.Remove(filepath.Join(voicesDir(), body.Name+".txt"))
	sendJSON(w, 200, map[string]any{"ok": true, "voices": voiceList()})
}

// voiceTurnNote : ajouté (côté modèle seulement) à un message dicté. La réponse
// sera lue à voix haute : elle doit sonner comme une phrase dite, pas un texte.
const voiceTurnNote = "\n\n[Message dicté à voix haute. Ta réponse sera lue par synthèse vocale : réponds comme à l'oral, " +
	"en phrases courtes et naturelles, deux à quatre en général, sans listes, titres, tableaux, emojis, liens ni Markdown. " +
	"Si la demande exige un travail (outils, recherche), fais-le normalement puis résume le résultat à l'oral.]"
