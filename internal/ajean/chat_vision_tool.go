package ajean

// chat_vision_tool.go — l'outil see_image : Jean charge lui-même un fichier image
// du disque dans sa VISION, sans que l'utilisateur ait à le joindre. Le résultat
// de l'outil reste un simple texte (accusé) ; l'image, elle, est réinjectée juste
// après sous forme d'un message utilisateur multimodal (image_url), le SEUL format
// que llama-server comprend une fois --mmproj chargé (même chemin que les pièces
// jointes, voir userMessageContent). Réservé au mode agent ET à la vision active.

import (
	"encoding/base64"
	"fmt"
	"os"
	"path/filepath"
)

// maxVisionBytes borne la taille d'une image chargée dans la vision : une image
// géante gonflerait le contexte (base64 + tokens visuels) sans intérêt, le moteur
// la redimensionne de toute façon.
const maxVisionBytes = 12 << 20 // 12 Mio

// toolSeeImage lit un fichier image et renvoie (accusé texte, partie image_url).
// La partie image vaut nil en cas d'erreur : l'appelant n'injecte alors rien.
func toolSeeImage(path string) (string, map[string]any) {
	if !visionEnabled() {
		return "[erreur] la vision n'est pas active sur ce modèle (aucun projecteur MMPROJ configuré) — impossible de voir une image", nil
	}
	// Cible = un poste distant : le fichier est LÀ-BAS, pas lisible localement.
	if agentTargetSlug() != "" {
		return "[erreur] voir une image n'est pas encore possible sur un poste distant (le fichier est sur l'autre machine)", nil
	}
	if path == "" {
		return "[erreur] chemin de fichier manquant", nil
	}
	abs := resolveAgentPath(path)
	mime := imageMime(abs)
	if mime == "" {
		return "[erreur] format non reconnu comme image (attendu : png, jpg, gif, webp, bmp)", nil
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "[erreur] fichier introuvable : " + path, nil
	}
	if st.IsDir() {
		return "[erreur] c'est un dossier, pas une image : " + path, nil
	}
	if st.Size() > maxVisionBytes {
		return fmt.Sprintf("[erreur] image trop lourde (%s, max %s)", humanBytes(st.Size()), humanBytes(maxVisionBytes)), nil
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "[erreur] lecture impossible : " + err.Error(), nil
	}
	imgPart := map[string]any{
		"type": "image_url",
		"image_url": map[string]any{
			"url": "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(b),
		},
	}
	return "[ok] image chargée : " + filepath.Base(abs), imgPart
}
