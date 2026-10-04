package ajean

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Une vidéo en base64 part sur disque et revient identique à l'envoi au modèle ;
// l'historique ne garde qu'une référence de quelques octets.
func TestVideoRefsRoundTrip(t *testing.T) {
	testHome(t)
	raw := []byte(strings.Repeat("\x00\x00\x00\x18ftypmp42-fake-video-bytes", 5000))
	url := "data:video/mp4;base64," + base64.StdEncoding.EncodeToString(raw)
	msgs := []Message{
		{Role: "user", Content: []map[string]any{
			{"type": "text", "text": "regarde la vidéo"},
			{"type": "input_video", "input_video": map[string]any{"url": url}},
		}},
		{Role: "assistant", Content: "vu"},
	}
	// Relu depuis le JSON (forme []any), comme après un redémarrage.
	var reread []Message
	b, _ := json.Marshal(msgs)
	_ = json.Unmarshal(b, &reread)

	for _, set := range [][]Message{msgs, reread} {
		if !hasInlineVideos(set) || !refVideosInMessages(set) || hasInlineVideos(set) {
			t.Fatal("la vidéo aurait dû être rangée par référence")
		}
		js, _ := json.Marshal(set)
		if len(js) > 500 || !strings.Contains(string(js), vidRefScheme) {
			t.Fatalf("historique encore lourd : %d octets", len(js))
		}
		out := expandVideoRefs(set)
		_, got := partVideoURL(contentParts(out[0].Content)[1])
		if got != url {
			t.Fatal("la vidéo renvoyée au modèle diffère de l'originale")
		}
		// L'historique lui-même n'est pas modifié par l'expansion.
		if _, u := partVideoURL(contentParts(set[0].Content)[1]); !strings.HasPrefix(u, vidRefScheme) {
			t.Fatal("expandVideoRefs ne doit pas toucher l'historique")
		}
	}
}

// userMessageContent : une vidéo jointe devient une partie input_video (et une
// image jointe reste une partie image_url), quand la vision est active.
func TestUserMessageContentVideo(t *testing.T) {
	videoEngine(t, true)
	home := testHome(t)
	// Active la vision (clé MMPROJ) dans le $AJEAN_HOME du test.
	if err := WriteConfig(map[string]string{"MMPROJ": "mmproj.gguf"}); err != nil {
		t.Fatal(err)
	}
	if !visionEnabled() {
		t.Fatal("visionEnabled() devrait être vrai avec MMPROJ configuré")
	}

	dir, err := uploadsDir()
	if err != nil {
		t.Fatal(err)
	}
	vname := "clip-test.mp4"
	vpath := filepath.Join(dir, vname)
	if err := os.WriteFile(vpath, []byte("fake-mp4-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(vpath)
	_ = home

	in := []attachInfo{{Name: vname, Path: "uploads/" + vname, Size: 15}}

	content := userMessageContent(in, "décris ce clip")
	parts, ok := content.([]map[string]any)
	if !ok {
		t.Fatalf("attendu un contenu multimodal, obtenu %T", content)
	}
	var hasVideo, hasText bool
	for _, p := range parts {
		switch p["type"] {
		case "input_video":
			iv, _ := p["input_video"].(map[string]any)
			u, _ := iv["url"].(string)
			if !strings.HasPrefix(u, "data:video/mp4;base64,") {
				t.Fatalf("data URI vidéo inattendue : %q", u[:40])
			}
			hasVideo = true
		case "text":
			hasText = true
		}
	}
	if !hasVideo || !hasText {
		t.Fatalf("parties manquantes : video=%v text=%v", hasVideo, hasText)
	}
}

// toolSeeVideo : refuse un format non vidéo et un fichier absent, accepte un mp4.
func TestToolSeeVideo(t *testing.T) {
	videoEngine(t, true)
	home := testHome(t)
	if err := WriteConfig(map[string]string{"MMPROJ": "mmproj.gguf"}); err != nil {
		t.Fatal(err)
	}
	// Format non vidéo.
	if msg, part := toolSeeVideo(context.Background(), "notes.txt"); part != nil || !strings.HasPrefix(msg, "[erreur]") {
		t.Fatalf("attendu une erreur pour un non-vidéo : %q / %v", msg, part)
	}
	// Fichier absent.
	if msg, part := toolSeeVideo(context.Background(), "nulle-part.mp4"); part != nil || !strings.HasPrefix(msg, "[erreur]") {
		t.Fatalf("attendu une erreur pour un fichier absent : %q / %v", msg, part)
	}
	// mp4 présent.
	p := filepath.Join(home, "clip.mp4")
	if err := os.WriteFile(p, []byte("fake-mp4"), 0o600); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(p)
	msg, part := toolSeeVideo(context.Background(), p)
	if part == nil || !strings.HasPrefix(msg, "[ok]") {
		t.Fatalf("attendu un [ok] : %q / %v", msg, part)
	}
	if part["type"] != "input_video" {
		t.Fatalf("partie inattendue : %v", part["type"])
	}
}

// videoEngine simule un moteur qui sait (ou non) lire la vidéo.
func videoEngine(t *testing.T, ok bool) {
	prev := videoInputSupported
	videoInputSupported = func() bool { return ok }
	t.Cleanup(func() { videoInputSupported = prev })
}

// Moteur sans vidéo (API externe, Strata) : il ignorait la partie input_video
// sans erreur et le modèle ne recevait que le texte. La vidéo doit alors être
// annoncée comme fichier, avec la marche à suivre (ffmpeg puis see_image), et
// see_video doit le dire au lieu de charger le fichier.
func TestVideoUnsupportedEngine(t *testing.T) {
	videoEngine(t, false)
	testHome(t)
	if err := WriteConfig(map[string]string{"MMPROJ": "mmproj.gguf"}); err != nil {
		t.Fatal(err)
	}
	dir, err := uploadsDir()
	if err != nil {
		t.Fatal(err)
	}
	vname := "clip-test.mp4"
	if err := os.WriteFile(filepath.Join(dir, vname), []byte("fake-mp4-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	content := userMessageContent([]attachInfo{{Name: vname, Path: "uploads/" + vname, Size: 15}}, "décris ce clip")
	s, ok := content.(string)
	if !ok {
		t.Fatalf("attendu du texte seul (pas de partie input_video), obtenu %T", content)
	}
	if !strings.Contains(s, "uploads/"+vname) || !strings.Contains(s, "ffmpeg") {
		t.Fatalf("chemin et consigne ffmpeg attendus : %q", s)
	}
	if msg, part := toolSeeVideo(context.Background(), filepath.Join(dir, vname)); part != nil || !strings.Contains(msg, "ffmpeg") {
		t.Fatalf("see_video doit refuser avec la consigne ffmpeg : %q", msg)
	}
}
