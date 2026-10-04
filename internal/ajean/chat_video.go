package ajean

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// chat_video.go — vidéos d'une conversation stockées PAR RÉFÉRENCE.
//
// Mêmes raisons que chat_images.go : une vidéo en base64 dans l'historique
// pèse des Mo (et l'historique est réécrit à chaque fin de tour). On la range
// UNE fois sur disque, sous AJEAN_HOME/chatvid/<empreinte>.<ext>, et
// l'historique n'en garde que l'adresse « ajean-vid:<nom> ». Juste avant
// l'envoi au modèle, expandVideoRefs remet les octets exacts : pour le
// modèle, rien ne change. Mêmes octets, même empreinte → aucune copie en double.

const vidRefScheme = "ajean-vid:"

func chatVidDir() string { return filepath.Join(AjeanHome(), "chatvid") }

var vidExtByMime = map[string]string{
	"video/mp4":        ".mp4",
	"video/webm":       ".webm",
	"video/quicktime":  ".mov",
	"video/x-matroska": ".mkv",
	"video/x-msvideo":  ".avi",
}

// storeChatVideo range les octets d'une vidéo et renvoie sa référence.
func storeChatVideo(b []byte, mime string) (string, error) {
	ext := vidExtByMime[mime]
	if ext == "" {
		ext = ".vid"
	}
	sum := sha256.Sum256(b)
	name := hex.EncodeToString(sum[:16]) + ext
	dir := chatVidDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, name)
	if _, err := os.Stat(p); err != nil {
		tmp := p + ".tmp"
		if err := os.WriteFile(tmp, b, 0o600); err != nil {
			return "", err
		}
		if err := os.Rename(tmp, p); err != nil {
			return "", err
		}
	}
	return vidRefScheme + name, nil
}

// Cache des data-URL reconstituées : un tour d'agent renvoie tout l'historique à
// CHAQUE étape, on ne relit pas le disque à chaque fois. Borné en taille.
var (
	vidCacheMu    sync.Mutex
	vidCache      = map[string]string{}
	vidCacheOrder []string
	vidCacheBytes int
)

const vidCacheMax = 64 << 20

func chatVideoDataURL(ref string) (string, bool) {
	name := strings.TrimPrefix(ref, vidRefScheme)
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return "", false
	}
	vidCacheMu.Lock()
	if u, ok := vidCache[name]; ok {
		vidCacheMu.Unlock()
		return u, true
	}
	vidCacheMu.Unlock()
	b, err := os.ReadFile(filepath.Join(chatVidDir(), name))
	if err != nil {
		return "", false
	}
	mime := "video/mp4"
	for m, e := range vidExtByMime {
		if strings.HasSuffix(name, e) {
			mime = m
		}
	}
	u := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
	vidCacheMu.Lock()
	if _, ok := vidCache[name]; !ok {
		vidCache[name] = u
		vidCacheOrder = append(vidCacheOrder, name)
		vidCacheBytes += len(u)
		for vidCacheBytes > vidCacheMax && len(vidCacheOrder) > 1 {
			old := vidCacheOrder[0]
			vidCacheOrder = vidCacheOrder[1:]
			vidCacheBytes -= len(vidCache[old])
			delete(vidCache, old)
		}
	}
	vidCacheMu.Unlock()
	return u, true
}

// partVideoURL extrait la partie input_video d'un contenu multimodal.
func partVideoURL(p map[string]any) (map[string]any, string) {
	if p["type"] != "input_video" {
		return nil, ""
	}
	iv, _ := p["input_video"].(map[string]any)
	if iv == nil {
		return nil, ""
	}
	u, _ := iv["url"].(string)
	if u == "" {
		u, _ = iv["data"].(string)
	}
	return iv, u
}

// refVideosInMessages remplace EN PLACE les vidéos en base64 des messages par
// des références (voir plus haut). Renvoie true si quelque chose a changé.
func refVideosInMessages(msgs []Message) bool {
	changed := false
	for _, m := range msgs {
		for _, p := range contentParts(m.Content) {
			iv, u := partVideoURL(p)
			if !strings.HasPrefix(u, "data:video/") {
				continue
			}
			i := strings.Index(u, ";base64,")
			if i < 0 {
				continue
			}
			b, err := base64.StdEncoding.DecodeString(u[i+8:])
			if err != nil {
				continue
			}
			ref, err := storeChatVideo(b, u[5:i])
			if err != nil {
				continue
			}
			iv["url"] = ref
			changed = true
		}
	}
	return changed
}

// hasInlineVideos : au moins une vidéo encore en base64 dans ces messages ?
func hasInlineVideos(msgs []Message) bool {
	for _, m := range msgs {
		for _, p := range contentParts(m.Content) {
			if _, u := partVideoURL(p); strings.HasPrefix(u, "data:video/") {
				return true
			}
		}
	}
	return false
}

// expandVideoRefs renvoie une COPIE des messages prête pour le modèle : chaque
// référence de vidéo redevient sa data-URL. Les messages sans référence sont
// partagés tels quels. Une vidéo introuvable devient une mention textuelle.
func expandVideoRefs(msgs []Message) []Message {
	out := msgs
	copied := false
	for i, m := range msgs {
		parts := contentParts(m.Content)
		has := false
		for _, p := range parts {
			if _, u := partVideoURL(p); strings.HasPrefix(u, vidRefScheme) {
				has = true
				break
			}
		}
		if !has {
			continue
		}
		if !copied {
			out = append([]Message(nil), msgs...)
			copied = true
		}
		np := make([]map[string]any, 0, len(parts))
		for _, p := range parts {
			_, u := partVideoURL(p)
			if !strings.HasPrefix(u, vidRefScheme) {
				np = append(np, p)
				continue
			}
			if d, ok := chatVideoDataURL(u); ok {
				np = append(np, map[string]any{"type": "input_video", "input_video": map[string]any{"url": d}})
			} else {
				np = append(np, map[string]any{"type": "text", "text": "[vidéo indisponible : fichier supprimé]"})
			}
		}
		m.Content = np
		out[i] = m
	}
	return out
}
