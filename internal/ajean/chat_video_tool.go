package ajean

// chat_video_tool.go — l'outil see_video : Jean charge lui-même un fichier vidéo
// du disque dans sa VISION, sans que l'utilisateur ait à le joindre. Le résultat
// de l'outil reste un simple texte (accusé) ; la vidéo, elle, est réinjectée juste
// après sous forme d'un message utilisateur multimodal (input_video), le format
// que llama-server comprend une fois --mmproj chargé (même chemin que les pièces
// jointes, voir userMessageContent). Réservé au mode agent ET à la vision active.

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

// maxVideoBytes borne la taille d'une vidéo chargée dans la vision : le moteur
// décode les frames à ~4 fps, donc une vidéo longue = des dizaines de milliers de
// tokens visuels. Une borne généreuse mais réelle — au-delà, le contexte 262k
// sature sans que le modèle puisse raisonner dessus.
const maxVideoBytes = 200 << 20 // 200 Mio

// toolSeeVideo lit un fichier vidéo et renvoie (accusé texte, partie input_video).
// La partie vidéo vaut nil en cas d'erreur : l'appelant n'injecte alors rien.
func toolSeeVideo(ctx context.Context, path string) (string, map[string]any) {
	if !visionEnabled() {
		return "[erreur] la vision n'est pas active sur ce modèle — impossible de voir une vidéo", nil
	}
	if path == "" {
		return "[erreur] chemin de fichier manquant", nil
	}
	if !videoInputSupported() {
		return "[erreur] " + videoFallbackNote, nil
	}
	abs := resolveSpacePath(ctx, path)
	if msg := guardSpacePath(ctx, abs); msg != "" {
		return msg, nil
	}
	mime := videoMime(abs)
	if mime == "" {
		return "[erreur] format non reconnu comme vidéo (attendu : mp4, webm, mov, mkv, avi)", nil
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "[erreur] fichier introuvable : " + path, nil
	}
	if st.IsDir() {
		return "[erreur] c'est un dossier, pas une vidéo : " + path, nil
	}
	if st.Size() > maxVideoBytes {
		return fmt.Sprintf("[erreur] vidéo trop lourde (%s, max %s) — coupe-la en un clip plus court", humanBytes(st.Size()), humanBytes(maxVideoBytes)), nil
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "[erreur] lecture impossible : " + err.Error(), nil
	}
	vidPart := map[string]any{
		"type": "input_video",
		"input_video": map[string]any{
			"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b),
		},
	}
	return "[ok] vidéo chargée : " + filepath.Base(abs), vidPart
}
