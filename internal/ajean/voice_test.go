package ajean

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeAudio simule un serveur audio.cpp et retient ce qu'il a reçu.
type fakeAudio struct {
	model, voice, refText, input string
	hasRef                       bool
	opts                         map[string]any
}

func newFakeAudio(t *testing.T, f *fakeAudio) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/health":
			w.Write([]byte("ok"))
		case "/v1/audio/transcriptions":
			_ = r.ParseMultipartForm(1 << 20)
			f.model = r.FormValue("model")
			file, _, err := r.FormFile("file")
			if err != nil {
				http.Error(w, "pas de fichier", 400)
				return
			}
			b, _ := io.ReadAll(file)
			if !bytes.HasPrefix(b, []byte("RIFF")) {
				http.Error(w, "pas un wav", 400)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"text": " Bonjour Jean. "})
		case "/v1/audio/speech":
			var req map[string]any
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.voice, _ = req["voice"].(string)
			f.refText, _ = req["reference_text"].(string)
			f.input, _ = req["input"].(string)
			_, f.hasRef = req["voice_ref"]
			f.opts, _ = req["options"].(map[string]any)
			_, _ = w.Write([]byte("RIFFfauxwav"))
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func call(h http.HandlerFunc, method, path, body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h(rec, httptest.NewRequest(method, path, strings.NewReader(body)))
	return rec
}

// Relais transcription / synthèse, réglages, bibliothèque de voix.
func TestVoiceRelay(t *testing.T) {
	home := testHome(t)
	var f fakeAudio
	srv := newFakeAudio(t, &f)
	t.Setenv("AJEAN_VOICE_URL", srv.URL)

	// Statut : actif grâce à la variable d'environnement.
	if rec := call(handleVoiceStatus, "GET", "/", ""); !strings.Contains(rec.Body.String(), `"enabled":true`) {
		t.Fatalf("statut : %s", rec.Body.String())
	}

	wav := append([]byte("RIFF"), make([]byte, 60)...)
	body, _ := json.Marshal(map[string]string{"wav": base64.StdEncoding.EncodeToString(wav)})
	rec := call(handleVoiceSTT, "POST", "/", string(body))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"text":"Bonjour Jean."`) || f.model != "asr" {
		t.Fatalf("stt : %d %s (modèle %q)", rec.Code, rec.Body.String(), f.model)
	}

	// Voix hors bibliothèque : son nom part tel quel.
	rec = call(handleVoiceTTS, "POST", "/", `{"text":"Bonsoir monsieur.","voice":"narrateur"}`)
	var out struct{ Wav string }
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	got, _ := base64.StdEncoding.DecodeString(out.Wav)
	if rec.Code != 200 || string(got) != "RIFFfauxwav" || f.voice != "narrateur" || f.hasRef {
		t.Fatalf("tts : %d %s (voix %q, ref %v)", rec.Code, rec.Body.String(), f.voice, f.hasRef)
	}

	// Voix créée par description : enregistrée, puis envoyée en extrait de référence.
	rec = call(handleVoiceDesign, "POST", "/", `{"name":"majordome","description":"A calm British butler"}`)
	if rec.Code != 200 || !strings.HasPrefix(f.input, "(A calm British butler) ") || f.opts["ultimate_clone"] != false {
		t.Fatalf("design : %d %s (input %q)", rec.Code, rec.Body.String(), f.input)
	}
	if _, err := os.Stat(filepath.Join(home, "voices", "majordome.wav")); err != nil {
		t.Fatalf("voix non enregistrée : %v", err)
	}
	call(handleVoiceSettings, "POST", "/", `{"voice":"majordome","sens":3,"silence_ms":1200}`)
	rec = call(handleVoiceTTS, "POST", "/", `{"text":"Bonjour."}`)
	if rec.Code != 200 || !f.hasRef || f.voice != "" || f.refText != voiceDesignSample {
		t.Fatalf("tts bibliothèque : ref %v voix %q texte %q", f.hasRef, f.voice, f.refText)
	}
	rec = call(handleVoiceSettings, "GET", "/", "")
	for _, want := range []string{`"voice":"majordome"`, `"sens":3`, `"silence_ms":1200`, `"voices":["majordome"]`, `"reachable":true`} {
		if !strings.Contains(rec.Body.String(), want) {
			t.Fatalf("réglages : %s (manque %s)", rec.Body.String(), want)
		}
	}

	// Coupée depuis les réglages : le micro disparaît.
	call(handleVoiceSettings, "POST", "/", `{"on":false}`)
	if rec := call(handleVoiceStatus, "GET", "/", ""); !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatalf("voix coupée : %s", rec.Body.String())
	}

	// Garde-fous : texte vide, nom de voix douteux.
	if rec := call(handleVoiceTTS, "POST", "/", `{"text":"  "}`); rec.Code != 400 {
		t.Fatalf("texte vide accepté : %d", rec.Code)
	}
	if rec := call(handleVoiceDesign, "POST", "/", `{"name":"../x","description":"a"}`); rec.Code != 400 {
		t.Fatalf("nom douteux accepté : %d", rec.Code)
	}
	if rec := call(handleVoiceDelete, "POST", "/", `{"name":"majordome"}`); !strings.Contains(rec.Body.String(), `"voices":null`) {
		t.Fatalf("suppression : %s", rec.Body.String())
	}
}
