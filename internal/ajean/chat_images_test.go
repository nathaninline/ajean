package ajean

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
)

// Une image en base64 part sur disque et revient identique à l'envoi au modèle ;
// l'historique ne garde qu'une référence de quelques octets.
func TestImageRefsRoundTrip(t *testing.T) {
	testHome(t)
	raw := []byte(strings.Repeat("\x89PNG-fake-image-bytes", 5000))
	url := "data:image/png;base64," + base64.StdEncoding.EncodeToString(raw)
	msgs := []Message{
		{Role: "user", Content: []map[string]any{
			{"type": "text", "text": "regarde"},
			{"type": "image_url", "image_url": map[string]any{"url": url}},
		}},
		{Role: "assistant", Content: "vu"},
	}
	// Relu depuis le JSON (forme []any), comme après un redémarrage.
	var reread []Message
	b, _ := json.Marshal(msgs)
	_ = json.Unmarshal(b, &reread)

	for _, set := range [][]Message{msgs, reread} {
		if !hasInlineImages(set) || !refImagesInMessages(set) || hasInlineImages(set) {
			t.Fatal("l'image aurait dû être rangée par référence")
		}
		js, _ := json.Marshal(set)
		if len(js) > 500 || !strings.Contains(string(js), imgRefScheme) {
			t.Fatalf("historique encore lourd : %d octets", len(js))
		}
		out := expandImageRefs(set)
		_, got := partImageURL(contentParts(out[0].Content)[1])
		if got != url {
			t.Fatal("l'image renvoyée au modèle diffère de l'originale")
		}
		// L'historique lui-même n'est pas modifié par l'expansion.
		if _, u := partImageURL(contentParts(set[0].Content)[1]); !strings.HasPrefix(u, imgRefScheme) {
			t.Fatal("expandImageRefs ne doit pas toucher l'historique")
		}
	}
}
