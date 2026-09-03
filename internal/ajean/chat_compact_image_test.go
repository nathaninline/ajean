package ajean

import (
	"strings"
	"testing"
)

// imgMsg reproduit la forme réelle du message injecté après un see_image
// réussi (voir llm_client.go, juste après l'exécution de l'outil) : un
// message user multimodal, légende texte + partie image_url.
func imgMsg(label string) Message {
	return Message{Role: "user", Content: []map[string]any{
		{"type": "text", "text": "Image demandée (" + label + ") :"},
		{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,ZmFrZQ=="}},
	}}
}

// TestMsgTextSurfacesImagePresence est le test qui aurait dû exister avant :
// sans le marqueur, msgText() sur un message see_image ne renvoyait QUE la
// légende ("Image demandée (fichier) :"), quelques dizaines de caractères —
// bien sous recallArchiveMinLen (800). L'image n'était donc jamais archivée
// NI mentionnée au résumeur : au compactage suivant, elle disparaissait sans
// laisser une seule trace dans tout le pipeline de compaction.
func TestMsgTextSurfacesImagePresence(t *testing.T) {
	m := imgMsg("atlas_grid.png")
	got := msgText(m)
	if !strings.Contains(got, "atlas_grid.png") {
		t.Fatalf("msgText a perdu le nom du fichier : %q", got)
	}
	if !strings.Contains(got, "image") {
		t.Fatalf("msgText ne signale plus la présence d'une image : %q", got)
	}
}

// TestMsgTextHandlesReloadedImageContent couvre le second type de Content
// possible pour le même message une fois relu depuis le JSON persisté
// ([]any de map[string]any générique plutôt que []map[string]any typé) — les
// deux chemins de msgText doivent traiter image_url pareil.
func TestMsgTextHandlesReloadedImageContent(t *testing.T) {
	m := Message{Role: "user", Content: []any{
		map[string]any{"type": "text", "text": "Image demandée (verify_map.png) :"},
		map[string]any{"type": "image_url", "image_url": map[string]any{"url": "data:image/png;base64,ZmFrZQ=="}},
	}}
	got := msgText(m)
	if !strings.Contains(got, "verify_map.png") || !strings.Contains(got, "image") {
		t.Fatalf("chemin []any : marqueur image absent ou nom de fichier perdu : %q", got)
	}
}

// TestImageMessageStillNotRecallEligible vérifie que le correctif ne change
// PAS ce point : une image reste sous le seuil d'archivage (le marqueur est
// court exprès) — on ne prétend pas la rendre rappelable via recall(id), on
// rend juste sa perte visible. Voir imageLostMarker.
func TestImageMessageStillNotRecallEligible(t *testing.T) {
	if recallEligible(imgMsg("atlas_grid.png")) {
		t.Fatal("un message-image est devenu éligible à l'archivage — pas l'objectif de ce correctif")
	}
}

// TestRenderTranscriptMentionsLostImage est le test qui compte vraiment pour
// le bug observé : le RÉSUMEUR (qui ne voit que renderTranscript, jamais les
// messages bruts) doit pouvoir savoir qu'une image a été montrée, pour au
// moins le dire dans son résumé — avant ce correctif, cette information
// n'existait nulle part dans le texte qu'on lui envoie.
func TestRenderTranscriptMentionsLostImage(t *testing.T) {
	// Le message-image SEUL, sans le résultat d'outil texte qui le précède
	// d'habitude — celui-là mentionne déjà "image"/le nom de fichier de son
	// côté, ce qui aurait fait passer ce test même sans le correctif (repéré en
	// écrivant ce test : premier jet faux-positif, corrigé pour isoler ce qu'on
	// vérifie vraiment — que la partie image_url ELLE-MÊME laisse une trace).
	torso := []Message{
		um("montre-moi l'atlas"),
		imgMsg("atlas_grid.png"),
		am("je vois les bandes h8 et h3."),
	}
	out := renderTranscript(torso)
	if !strings.Contains(out, "atlas_grid.png") {
		t.Fatalf("le transcript envoyé au résumeur a perdu le nom du fichier vu : %q", out)
	}
	if !strings.Contains(out, "image") {
		t.Fatalf("le transcript envoyé au résumeur ne mentionne plus qu'une image a été montrée : %q", out)
	}
}
