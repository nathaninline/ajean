package ajean

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// chat_export.go — sortir la conversation de la base.
//
// Jusqu'à la 0.7, l'historique vivait dans un `conversation.json` qu'on pouvait
// ouvrir, lire et copier pour archive. La 0.8 l'a rangé dans ajean.db (bbolt) :
// un fichier binaire, et qui porte de surcroît un verrou EXCLUSIF tant que le
// service tourne — donc impossible ne serait-ce que de le copier sans arrêter
// AJEAN. Le fil de discussion et les raisonnements sont toujours là, mais plus
// personne ne peut les récupérer. C'est une régression pour qui relisait ses
// échanges, et ce fichier la répare.
//
// Deux formats, un même contenu : le JSON est fidèle (vue modèle + journal
// d'affichage complet, rejouable), le Markdown est fait pour être lu.

// exportPayload est la forme du fichier JSON exporté. `messages` est la vue
// envoyée au modèle, `log` le journal d'affichage (celui qui porte les
// raisonnements, les outils et les vitesses).
type exportPayload struct {
	Version    string     `json:"ajean_version"`
	ExportedAt string     `json:"exported_at"`
	CtxUsed    int        `json:"ctx_used"`
	Messages   []Message  `json:"messages"`
	Log        []LogEvent `json:"log"`
}

// ExportJSON rend la conversation entière en JSON indenté.
func (c *Conversation) ExportJSON() ([]byte, error) {
	c.mu.Lock()
	p := exportPayload{
		Version:    Version,
		ExportedAt: time.Now().Format(time.RFC3339),
		CtxUsed:    c.CtxUsed,
		Messages:   append([]Message(nil), c.Messages...),
		Log:        append([]LogEvent(nil), c.Log...),
	}
	c.mu.Unlock()
	return json.MarshalIndent(p, "", "  ")
}

// ExportMarkdown rend la conversation en Markdown lisible : un bloc par tour,
// le raisonnement replié dans un <details> (il est souvent plus long que la
// réponse), et les outils appelés en liste.
//
// On repart du JOURNAL D'AFFICHAGE et non de la vue modèle : c'est lui qui porte
// les raisonnements et la trace des outils, précisément ce qu'on vient chercher
// dans un export. coalesceReplay est réutilisé tel quel pour recoller les
// milliers de deltas d'un tour en un bloc de texte par bulle.
func (c *Conversation) ExportMarkdown() string {
	c.mu.Lock()
	snapshot := append([]LogEvent(nil), c.Log...)
	c.mu.Unlock()

	var b strings.Builder
	fmt.Fprintf(&b, "# Conversation AJEAN\n\nExportée le %s par AJEAN %s.\n",
		time.Now().Format("02/01/2006 à 15:04"), Version)

	// openBubble : une bulle assistant est ouverte (au moins un bout de réponse a
	// été écrit). Sert à ne poser l'en-tête « AJEAN » qu'une fois par tour, même
	// quand la réponse est entrecoupée d'appels d'outils.
	openBubble := false
	for _, ev := range coalesceReplay(snapshot, 0) {
		switch {
		case ev["user"] != nil:
			openBubble = false
			fmt.Fprintf(&b, "\n---\n\n## Vous\n\n%s\n", mdText(ev["user"]))
		case ev["reasoning_content"] != nil:
			if !openBubble {
				b.WriteString("\n## AJEAN\n")
				openBubble = true
			}
			fmt.Fprintf(&b, "\n<details>\n<summary>Raisonnement</summary>\n\n%s\n\n</details>\n",
				mdText(ev["reasoning_content"]))
		case ev["content"] != nil:
			if !openBubble {
				b.WriteString("\n## AJEAN\n")
				openBubble = true
			}
			fmt.Fprintf(&b, "\n%s\n", mdText(ev["content"]))
		case ev["tool_used"] != nil:
			tu, _ := ev["tool_used"].(map[string]any)
			if tu == nil {
				break
			}
			if !openBubble {
				b.WriteString("\n## AJEAN\n")
				openBubble = true
			}
			label, _ := tu["label"].(string)
			name, _ := tu["name"].(string)
			if label == "" {
				label = name
			}
			fmt.Fprintf(&b, "\n> 🔧 **%s** — %s\n", mdInline(name), mdInline(label))
			if res, _ := tu["result"].(string); strings.TrimSpace(res) != "" {
				fmt.Fprintf(&b, "\n```\n%s\n```\n", strings.TrimRight(res, "\n"))
			}
		case ev["compacted"] != nil:
			fmt.Fprintf(&b, "\n_(contexte compacté à cet endroit : les tours précédents ont été résumés pour le modèle, le fil ci-dessus reste complet)_\n")
		}
	}
	if !openBubble && len(snapshot) == 0 {
		b.WriteString("\n_Conversation vide._\n")
	}
	return b.String()
}

// mdText rend une valeur d'événement en texte de bloc Markdown.
func mdText(v any) string {
	s, _ := v.(string)
	return strings.TrimRight(s, "\n")
}

// mdInline neutralise ce qui casserait une ligne Markdown (un label d'outil peut
// contenir un chemin, des astérisques, un retour à la ligne).
func mdInline(v any) string {
	s, _ := v.(string)
	s = strings.ReplaceAll(s, "\n", " ")
	r := strings.NewReplacer("*", "\\*", "_", "\\_", "`", "\\`", "[", "\\[", "]", "\\]")
	return strings.TrimSpace(r.Replace(s))
}

// exportFilename : nom proposé au téléchargement, horodaté pour que deux exports
// ne s'écrasent pas dans le dossier de téléchargements.
func exportFilename(ext string) string {
	return "ajean-conversation-" + time.Now().Format("2006-01-02-1504") + "." + ext
}

// cmdExport écrit la conversation dans un fichier (ou sur la sortie standard).
//
//	ajean export                  → ajean-conversation-<date>.md dans le dossier courant
//	ajean export --json           → idem en JSON
//	ajean export mon-fil.md       → nom de fichier imposé (l'extension choisit le format)
//	ajean export -                → sur la sortie standard, pour enchaîner un tube
//
// La conversation est relue depuis la base à chaque appel : la commande marche
// pendant que le service tourne (bbolt n'est jamais gardé ouvert, voir store.go).
func cmdExport(args []string) error {
	format, out := "md", ""
	for _, a := range args {
		switch a {
		case "--json", "-j":
			format = "json"
		case "--md", "--markdown", "-m":
			format = "md"
		default:
			if strings.HasPrefix(a, "-") && a != "-" {
				return fmt.Errorf("option inconnue : %s (attendu --json ou --md)", a)
			}
			out = a
		}
	}
	// Une extension explicite l'emporte sur le drapeau : `ajean export fil.json`
	// qui produirait du Markdown serait un piège.
	if strings.HasSuffix(strings.ToLower(out), ".json") {
		format = "json"
	} else if strings.HasSuffix(strings.ToLower(out), ".md") {
		format = "md"
	}

	LoadConversation()
	var body []byte
	if format == "json" {
		b, err := conv.ExportJSON()
		if err != nil {
			return err
		}
		body = b
	} else {
		body = []byte(conv.ExportMarkdown())
	}

	if out == "-" {
		_, err := os.Stdout.Write(body)
		return err
	}
	if out == "" {
		out = exportFilename(format)
	}
	if err := os.WriteFile(out, body, 0o644); err != nil {
		return err
	}
	conv.mu.Lock()
	n := len(conv.Messages)
	conv.mu.Unlock()
	fmt.Printf("%s %s (%d messages, %s)\n", green("[ok]"), out, n, humanBytes(int64(len(body))))
	return nil
}

// handleChatExport sert la conversation en téléchargement. `format=md` (défaut)
// ou `format=json`.
func handleChatExport(w http.ResponseWriter, r *http.Request) {
	var body []byte
	var ctype, ext string
	if r.URL.Query().Get("format") == "json" {
		b, err := conv.ExportJSON()
		if err != nil {
			sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
			return
		}
		body, ctype, ext = b, "application/json; charset=utf-8", "json"
	} else {
		body, ctype, ext = []byte(conv.ExportMarkdown()), "text/markdown; charset=utf-8", "md"
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", `attachment; filename="`+exportFilename(ext)+`"`)
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	_, _ = w.Write(body)
}
