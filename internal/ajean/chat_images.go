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

// chat_images.go — images d'une conversation stockées PAR RÉFÉRENCE.
//
// Une image vue par le modèle (pièce jointe, see_image, capture du navigateur) est
// une partie `image_url` en base64 dans un message. Gardée telle quelle dans
// l'historique, elle y pesait des Mo (une conversation de 14 messages : 36 Mo), et
// l'historique entier est réécrit à chaque fin de tour et relu à chaque ouverture.
//
// On la range donc UNE fois sur disque, sous AJEAN_HOME/chatimg/<empreinte>.<ext>
// (hors du dossier de travail, que l'IA peut vider), et l'historique n'en garde
// que l'adresse « ajean-img:<nom> ». Juste avant l'envoi au modèle,
// expandImageRefs remet les octets exacts (cache mémoire) : pour le modèle, rien
// ne change. Mêmes octets, même empreinte → aucune copie en double.

const imgRefScheme = "ajean-img:"

func chatImgDir() string { return filepath.Join(AjeanHome(), "chatimg") }

var imgExtByMime = map[string]string{"image/jpeg": ".jpg", "image/png": ".png", "image/gif": ".gif", "image/webp": ".webp"}

// storeChatImage range les octets d'une image et renvoie sa référence.
func storeChatImage(b []byte, mime string) (string, error) {
	ext := imgExtByMime[mime]
	if ext == "" {
		ext = ".img"
	}
	sum := sha256.Sum256(b)
	name := hex.EncodeToString(sum[:16]) + ext
	dir := chatImgDir()
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
	return imgRefScheme + name, nil
}

// Cache des data-URL reconstituées : un tour d'agent renvoie tout l'historique à
// CHAQUE étape, on ne relit pas le disque à chaque fois. Borné en taille.
var (
	imgCacheMu    sync.Mutex
	imgCache      = map[string]string{}
	imgCacheOrder []string
	imgCacheBytes int
)

const imgCacheMax = 64 << 20

func chatImageDataURL(ref string) (string, bool) {
	name := strings.TrimPrefix(ref, imgRefScheme)
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return "", false
	}
	imgCacheMu.Lock()
	if u, ok := imgCache[name]; ok {
		imgCacheMu.Unlock()
		return u, true
	}
	imgCacheMu.Unlock()
	b, err := os.ReadFile(filepath.Join(chatImgDir(), name))
	if err != nil {
		return "", false
	}
	mime := "image/jpeg"
	for m, e := range imgExtByMime {
		if strings.HasSuffix(name, e) {
			mime = m
		}
	}
	u := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b)
	imgCacheMu.Lock()
	if _, ok := imgCache[name]; !ok {
		imgCache[name] = u
		imgCacheOrder = append(imgCacheOrder, name)
		imgCacheBytes += len(u)
		for imgCacheBytes > imgCacheMax && len(imgCacheOrder) > 1 {
			old := imgCacheOrder[0]
			imgCacheOrder = imgCacheOrder[1:]
			imgCacheBytes -= len(imgCache[old])
			delete(imgCache, old)
		}
	}
	imgCacheMu.Unlock()
	return u, true
}

// contentParts renvoie les parties d'un contenu multimodal, sous l'une ou l'autre
// forme (fraîche []map, ou relue du JSON []any).
func contentParts(content any) []map[string]any {
	switch v := content.(type) {
	case []map[string]any:
		return v
	case []any:
		out := make([]map[string]any, 0, len(v))
		for _, p := range v {
			if m, ok := p.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	}
	return nil
}

func partImageURL(p map[string]any) (map[string]any, string) {
	if p["type"] != "image_url" {
		return nil, ""
	}
	iu, _ := p["image_url"].(map[string]any)
	if iu == nil {
		return nil, ""
	}
	u, _ := iu["url"].(string)
	return iu, u
}

// refImagesInMessages remplace EN PLACE les images en base64 des messages par des
// références (voir plus haut). Renvoie true si quelque chose a changé. Une image
// qui ne peut pas être rangée (disque plein…) reste en base64 : rien ne se perd.
func refImagesInMessages(msgs []Message) bool {
	changed := false
	for _, m := range msgs {
		for _, p := range contentParts(m.Content) {
			iu, u := partImageURL(p)
			if !strings.HasPrefix(u, "data:image/") {
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
			ref, err := storeChatImage(b, u[5:i])
			if err != nil {
				continue
			}
			iu["url"] = ref
			changed = true
		}
	}
	return changed
}

// refDataURLs remplace, dans une valeur JSON quelconque (chaîne, objet, tableau),
// chaque data-URL d'image par sa référence. Renvoie la valeur et true si modifiée.
func refDataURLs(v any) (any, bool) {
	switch x := v.(type) {
	case string:
		if !strings.HasPrefix(x, "data:image/") {
			return v, false
		}
		i := strings.Index(x, ";base64,")
		if i < 0 {
			return v, false
		}
		b, err := base64.StdEncoding.DecodeString(x[i+8:])
		if err != nil {
			return v, false
		}
		ref, err := storeChatImage(b, x[5:i])
		if err != nil {
			return v, false
		}
		return ref, true
	case map[string]any:
		ch := false
		for k, e := range x {
			if ne, c := refDataURLs(e); c {
				x[k] = ne
				ch = true
			}
		}
		return x, ch
	case []any:
		ch := false
		for i, e := range x {
			if ne, c := refDataURLs(e); c {
				x[i] = ne
				ch = true
			}
		}
		return x, ch
	}
	return v, false
}

// hasInlineImages : au moins une image encore en base64 dans ces messages ?
func hasInlineImages(msgs []Message) bool {
	for _, m := range msgs {
		for _, p := range contentParts(m.Content) {
			if _, u := partImageURL(p); strings.HasPrefix(u, "data:image/") {
				return true
			}
		}
	}
	return false
}

// expandImageRefs renvoie une COPIE des messages prête pour le modèle : chaque
// référence d'image redevient sa data-URL. Les messages sans référence sont
// partagés tels quels. Une image introuvable (dossier supprimé à la main) devient
// une mention textuelle plutôt qu'une requête qui échouerait.
func expandImageRefs(msgs []Message) []Message {
	out := msgs
	copied := false
	for i, m := range msgs {
		parts := contentParts(m.Content)
		has := false
		for _, p := range parts {
			if _, u := partImageURL(p); strings.HasPrefix(u, imgRefScheme) {
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
			_, u := partImageURL(p)
			if !strings.HasPrefix(u, imgRefScheme) {
				np = append(np, p)
				continue
			}
			if d, ok := chatImageDataURL(u); ok {
				np = append(np, map[string]any{"type": "image_url", "image_url": map[string]any{"url": d}})
			} else {
				np = append(np, map[string]any{"type": "text", "text": "[image indisponible : fichier supprimé]"})
			}
		}
		m.Content = np
		out[i] = m
	}
	return out
}
