package ajean

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
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
// Deux formats pour un même contenu : le Markdown est fait pour être LU, le
// JSON pour être RETRAITÉ. Les options de contenu (raisonnements, outils,
// portée) sont les MÊMES pour les deux — le format ne choisit que le
// contenant. Un fil relu pour retrouver une réponse n'a que faire des mille
// lignes de raisonnement qui l'ont produite, et ça vaut dans les deux formats.

// exportOpts décrit ce qu'on veut sortir. Le zéro de la structure n'est PAS un
// défaut utilisable (tout serait à false) : passer par defaultExportOpts.
type exportOpts struct {
	Format    string // "md" | "json"
	Reasoning bool   // inclure les blocs de raisonnement
	Tools     bool   // inclure les appels d'outils
	Results   bool   // inclure la sortie des outils (exige Tools)
	Turns     int    // 0 = tout le fil ; N = les N derniers échanges
	SessionID string // vide = conversation active ; sinon exporte cette session archivée
}

func defaultExportOpts() exportOpts {
	return exportOpts{Format: "md", Reasoning: true, Tools: true, Results: true}
}

// exportOptsFromQuery lit les options d'une requête. Toute clé absente garde sa
// valeur par défaut : `/api/chat/export` tout court reste l'export complet.
func exportOptsFromQuery(q map[string][]string) exportOpts {
	o := defaultExportOpts()
	get := func(k string) (string, bool) {
		v, ok := q[k]
		if !ok || len(v) == 0 {
			return "", false
		}
		return v[0], true
	}
	flag := func(k string, cur bool) bool {
		v, ok := get(k)
		if !ok {
			return cur
		}
		return v == "1" || v == "true" || v == "on"
	}
	if v, ok := get("format"); ok && v == "json" {
		o.Format = "json"
	}
	o.Reasoning = flag("reasoning", o.Reasoning)
	o.Tools = flag("tools", o.Tools)
	o.Results = flag("results", o.Results)
	if v, ok := get("turns"); ok {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			o.Turns = n
		}
	}
	if v, ok := get("id"); ok {
		o.SessionID = v
	}
	// Sans les bulles d'outils, leur sortie n'a nulle part où aller.
	if !o.Tools {
		o.Results = false
	}
	return o
}

// exportPayload est la forme du fichier JSON exporté. `messages` est la vue
// envoyée au modèle, `log` le journal d'affichage (celui qui porte les
// raisonnements, les outils et les vitesses).
type exportPayload struct {
	Version    string     `json:"ajean_version"`
	ExportedAt string     `json:"exported_at"`
	CtxUsed    int        `json:"ctx_used"`
	Messages   []Message  `json:"messages"`
	Log        []LogEvent `json:"log,omitempty"`
}

// ExportJSON rend la conversation en JSON indenté.
func (c *Conversation) ExportJSON(o exportOpts) ([]byte, error) {
	c.mu.Lock()
	msgs := append([]Message(nil), c.Messages...)
	log := append([]LogEvent(nil), c.Log...)
	ctxUsed := c.CtxUsed
	c.mu.Unlock()

	if o.Turns > 0 {
		msgs = lastTurnsMessages(msgs, o.Turns)
		log = lastTurnsLog(log, o.Turns)
	}
	p := exportPayload{
		Version:    Version,
		ExportedAt: time.Now().Format(time.RFC3339),
		CtxUsed:    ctxUsed,
		Messages:   stripInlineImages(msgs),
		Log:        filterLog(log, o),
	}
	return json.MarshalIndent(p, "", "  ")
}

// stripInlineImages remplace les images en base64 (data URI) portées par les
// messages multimodaux (vision : pièces jointes images + outil see_image) par un
// court marqueur. Sans ça, l'export JSON d'une conversation avec images embarquait
// plusieurs Mo de base64 par image — un fichier énorme et illisible. Le Markdown,
// lui, n'était pas concerné : il ne cite que les NOMS de fichiers. Le journal
// d'affichage non plus (il ne porte pas les images). On ne touche donc qu'ici, et
// sur une COPIE : les messages appartiennent à la conversation vivante.
func stripInlineImages(msgs []Message) []Message {
	out := make([]Message, len(msgs))
	for i, m := range msgs {
		out[i] = m
		out[i].Content = stripInlineImagesValue(m.Content)
	}
	return out
}

func stripInlineImagesValue(v any) any {
	// Une partie image (format OpenAI) : {"type":"image_url","image_url":{"url":"data:…"}}.
	// On copie la partie et on élide l'URL data: en gardant l'en-tête (type MIME) lisible.
	elide := func(part map[string]any) map[string]any {
		cp := make(map[string]any, len(part))
		for k, val := range part {
			cp[k] = val
		}
		if iu, ok := cp["image_url"].(map[string]any); ok {
			c2 := make(map[string]any, len(iu))
			for k, val := range iu {
				c2[k] = val
			}
			if url, ok := c2["url"].(string); ok && strings.HasPrefix(url, "data:") {
				c2["url"] = elideDataURI(url)
			}
			cp["image_url"] = c2
		}
		return cp
	}
	switch parts := v.(type) {
	case []map[string]any: // message fraîchement produit
		out := make([]map[string]any, len(parts))
		for i, p := range parts {
			out[i] = elide(p)
		}
		return out
	case []any: // message relu depuis la base (JSON) : []any de maps
		out := make([]any, len(parts))
		for i, p := range parts {
			if mp, ok := p.(map[string]any); ok {
				out[i] = elide(mp)
			} else {
				out[i] = p
			}
		}
		return out
	}
	return v // simple chaîne (cas courant) : rien à faire
}

// elideDataURI garde l'en-tête « data:<mime>;base64, » (lisible) et remplace le
// corps base64 par sa taille, pour que l'export reste informatif sans le poids.
func elideDataURI(url string) string {
	if i := strings.Index(url, ","); i >= 0 {
		return url[:i+1] + fmt.Sprintf("<élidé — %s>", humanBytes(int64(len(url)-i-1)))
	}
	return "<image élidée>"
}

// filterLog applique au journal les MÊMES cases que le Markdown. Les deux
// formats avaient auparavant des options distinctes (le JSON proposait un
// obscur « journal d'affichage » à la place des trois autres) : personne ne
// pouvait deviner ce que ça changeait, et cocher une case n'avait pas le même
// sens selon le format choisi juste au-dessus. Le format ne décide plus que du
// contenant ; ce qu'on emporte se règle une fois pour toutes.
func filterLog(log []LogEvent, o exportOpts) []LogEvent {
	if o.Reasoning && o.Tools && o.Results {
		return log
	}
	out := make([]LogEvent, 0, len(log))
	for _, ev := range log {
		if _, ok := ev.Delta["reasoning_content"]; ok && !o.Reasoning {
			continue
		}
		if tu, ok := ev.Delta["tool_used"].(map[string]any); ok {
			if !o.Tools {
				continue
			}
			if !o.Results {
				// Copie : l'événement appartient à la conversation vivante, le
				// modifier en place amputerait le fil affiché dans les navigateurs.
				cp := make(map[string]any, len(tu))
				for k, v := range tu {
					if k != "result" {
						cp[k] = v
					}
				}
				d := make(map[string]any, len(ev.Delta))
				for k, v := range ev.Delta {
					d[k] = v
				}
				d["tool_used"] = cp
				ev.Delta = d
			}
		}
		out = append(out, ev)
	}
	return out
}

// lastTurnsMessages garde les n derniers échanges de la vue modèle, un échange
// commençant à un message `user`.
func lastTurnsMessages(msgs []Message, n int) []Message {
	seen := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}
		seen++
		if seen == n {
			return msgs[i:]
		}
	}
	return msgs
}

// lastTurnsLog fait de même sur le journal d'affichage, où un échange commence à
// un événement `user`.
func lastTurnsLog(log []LogEvent, n int) []LogEvent {
	seen := 0
	for i := len(log) - 1; i >= 0; i-- {
		if _, ok := log[i].Delta["user"]; !ok {
			continue
		}
		seen++
		if seen == n {
			return log[i:]
		}
	}
	return log
}

// ExportMarkdown rend la conversation en Markdown lisible : un bloc par tour,
// le raisonnement replié dans un <details> (il est souvent plus long que la
// réponse), et les outils appelés en liste.
//
// On repart du JOURNAL D'AFFICHAGE et non de la vue modèle : c'est lui qui porte
// les raisonnements et la trace des outils, précisément ce qu'on vient chercher
// dans un export. coalesceReplay est réutilisé tel quel pour recoller les
// milliers de deltas d'un tour en un bloc de texte par bulle.
func (c *Conversation) ExportMarkdown(o exportOpts) string {
	c.mu.Lock()
	snapshot := append([]LogEvent(nil), c.Log...)
	c.mu.Unlock()
	if o.Turns > 0 {
		snapshot = lastTurnsLog(snapshot, o.Turns)
	}

	var b strings.Builder
	fmt.Fprintf(&b, "# Conversation AJEAN\n\nExportée le %s par AJEAN %s.\n",
		time.Now().Format("02/01/2006 à 15:04"), Version)
	// Un export tronqué ou allégé doit le DIRE : relu six mois plus tard, un fil
	// sans ses raisonnements ne doit pas passer pour le fil complet.
	if note := exportNote(o); note != "" {
		fmt.Fprintf(&b, "\n%s\n", note)
	}

	// openBubble : une bulle assistant est ouverte (au moins un bout de réponse a
	// été écrit). Sert à ne poser l'en-tête « AJEAN » qu'une fois par tour, même
	// quand la réponse est entrecoupée d'appels d'outils.
	openBubble := false
	head := func() {
		if !openBubble {
			b.WriteString("\n## AJEAN\n")
			openBubble = true
		}
	}
	for _, ev := range coalesceReplay(snapshot, 0) {
		switch {
		case ev["user"] != nil:
			openBubble = false
			fmt.Fprintf(&b, "\n---\n\n## Vous\n\n%s\n", mdText(ev["user"]))
			// Pièces jointes du tour. Sans elles, un message envoyé SANS texte
			// (juste un fichier) donnait une section « Vous » entièrement vide.
			if names := exportFileNames(ev["files"]); len(names) > 0 {
				fmt.Fprintf(&b, "\nFichiers joints : %s\n", strings.Join(names, ", "))
			}
		case ev["reasoning_content"] != nil:
			if !o.Reasoning {
				break
			}
			head()
			fmt.Fprintf(&b, "\n<details>\n<summary>Raisonnement</summary>\n\n%s\n\n</details>\n",
				mdText(ev["reasoning_content"]))
		case ev["content"] != nil:
			head()
			fmt.Fprintf(&b, "\n%s\n", mdText(ev["content"]))
		case ev["tool_used"] != nil:
			if !o.Tools {
				break
			}
			tu, _ := ev["tool_used"].(map[string]any)
			if tu == nil {
				break
			}
			head()
			label, _ := tu["label"].(string)
			name, _ := tu["name"].(string)
			if label == "" {
				label = name
			}
			fmt.Fprintf(&b, "\n> 🔧 **%s** — %s\n", mdInline(name), mdInline(label))
			if res, _ := tu["result"].(string); o.Results && strings.TrimSpace(res) != "" {
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

// exportNote résume en une ligne ce que l'export ne contient PAS.
func exportNote(o exportOpts) string {
	var parts []string
	if o.Turns > 0 {
		parts = append(parts, fmt.Sprintf("%d derniers échanges seulement", o.Turns))
	}
	if !o.Reasoning {
		parts = append(parts, "raisonnements retirés")
	}
	if !o.Tools {
		parts = append(parts, "appels d'outils retirés")
	} else if !o.Results {
		parts = append(parts, "sorties d'outils retirées")
	}
	if len(parts) == 0 {
		return ""
	}
	return "_Export partiel : " + strings.Join(parts, ", ") + "._"
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

// exportBody rend la conversation selon les options. Renvoie le corps, son type
// MIME et l'extension de fichier.
func exportBody(o exportOpts) ([]byte, string, string, error) {
	// Cible : la conversation active, ou une session ARCHIVÉE si un id est fourni
	// (export depuis le menu ⋮ d'une conversation, sans avoir à l'ouvrir). On monte
	// une Conversation temporaire à partir de l'archive et on réutilise le même code.
	target := conv
	if o.SessionID != "" {
		a, ok := loadArchive(o.SessionID)
		if !ok {
			return nil, "", "", fmt.Errorf("session introuvable")
		}
		target = &Conversation{Messages: a.Messages, Log: a.Log, Seq: a.Seq, CtxUsed: a.CtxUsed}
	}
	if o.Format == "json" {
		b, err := target.ExportJSON(o)
		return b, "application/json; charset=utf-8", "json", err
	}
	return []byte(target.ExportMarkdown(o)), "text/markdown; charset=utf-8", "md", nil
}

// cmdExport écrit la conversation dans un fichier (ou sur la sortie standard).
//
//	ajean export                  → ajean-conversation-<date>.md dans le dossier courant
//	ajean export --json           → idem en JSON
//	ajean export mon-fil.md       → nom de fichier imposé (l'extension choisit le format)
//	ajean export -                → sur la sortie standard, pour enchaîner un tube
//
// Options de contenu, les mêmes que la fenêtre d'export de l'interface :
//
//	--no-reasoning  --no-tools  --no-results  --last N
//
// La conversation est relue depuis la base à chaque appel : la commande marche
// pendant que le service tourne (bbolt n'est jamais gardé ouvert, voir store.go).
func cmdExport(args []string) error {
	o := defaultExportOpts()
	out := ""
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch a {
		case "--json", "-j":
			o.Format = "json"
		case "--md", "--markdown", "-m":
			o.Format = "md"
		case "--no-reasoning":
			o.Reasoning = false
		case "--no-tools":
			o.Tools, o.Results = false, false
		case "--no-results":
			o.Results = false
		case "--last":
			if i+1 >= len(args) {
				return fmt.Errorf("--last attend un nombre d'échanges")
			}
			i++
			n, err := strconv.Atoi(args[i])
			if err != nil || n <= 0 {
				return fmt.Errorf("--last : nombre d'échanges invalide (%s)", args[i])
			}
			o.Turns = n
		default:
			if strings.HasPrefix(a, "-") && a != "-" {
				return fmt.Errorf("option inconnue : %s (voir « ajean help »)", a)
			}
			out = a
		}
	}
	// Une extension explicite l'emporte sur le drapeau : `ajean export fil.json`
	// qui produirait du Markdown serait un piège.
	if strings.HasSuffix(strings.ToLower(out), ".json") {
		o.Format = "json"
	} else if strings.HasSuffix(strings.ToLower(out), ".md") {
		o.Format = "md"
	}

	LoadConversation()
	body, _, ext, err := exportBody(o)
	if err != nil {
		return err
	}

	if out == "-" {
		_, err := os.Stdout.Write(body)
		return err
	}
	if out == "" {
		out = exportFilename(ext)
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

func handleChatExport(w http.ResponseWriter, r *http.Request) {
	o := exportOptsFromQuery(r.URL.Query())
	body, ctype, ext, err := exportBody(o)
	if err != nil {
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", ctype)
	w.Header().Set("Content-Disposition", `attachment; filename="`+exportFilename(ext)+`"`)
	w.Header().Set("Content-Length", fmt.Sprint(len(body)))
	_, _ = w.Write(body)
}

// exportFileNames lit les noms des pièces jointes portées par un événement `user`.
//
// Deux formes possibles pour la même donnée : []attachInfo quand l'événement
// vient d'être produit, et []any de maps quand il a été relu depuis le journal
// persisté (JSON). L'export travaille sur les deux, d'où le double cas.
func exportFileNames(v any) []string {
	var out []string
	switch files := v.(type) {
	case []attachInfo:
		for _, f := range files {
			out = append(out, f.Name)
		}
	case []any:
		for _, it := range files {
			if m, ok := it.(map[string]any); ok {
				if n, ok := m["name"].(string); ok && n != "" {
					out = append(out, n)
				}
			}
		}
	}
	return out
}
