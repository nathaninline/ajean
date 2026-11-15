package apps

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/dop251/goja"
)

// Les vérifications se font sans navigateur, à l'enregistrement : elles
// attrapent ce qui casserait l'app à coup sûr dans son bac à sable, pendant
// que l'assistant a encore le code sous les yeux.

// rule : un usage qui ne marche pas dans le bac à sable, et quoi faire à la place.
type rule struct {
	re     *regexp.Regexp
	inHTML bool // cherché dans index.html
	inCSS  bool // cherché dans style.css
	inJS   bool // cherché dans app.js
	advice string
}

var rules = []rule{
	{
		re:     regexp.MustCompile(`\b(?:localStorage|sessionStorage|indexedDB)\b|document\.cookie`),
		inHTML: true, inJS: true,
		advice: "localStorage, sessionStorage, indexedDB et les cookies n'existent pas dans le bac à sable (y toucher lève une exception) : garde les données avec ajean.storage.get/set/remove/keys.",
	},
	{
		re:     regexp.MustCompile(`\bfetch\s*\(|\b(?:XMLHttpRequest|WebSocket|EventSource)\b|\bsendBeacon\b`),
		inHTML: true, inJS: true,
		advice: "fetch, XMLHttpRequest et WebSocket sont bloqués : l'app n'a pas de réseau. Pour des données du web, écris un outil (outil tools, avec http.get), déclare-le dans 'tools', puis appelle-le avec ajean.tools.run(name, args).",
	},
	{
		re:     regexp.MustCompile(`(?:^|[^.\w$]|\bwindow\.)(?:alert|confirm|prompt)\s*\(`),
		inHTML: true, inJS: true,
		advice: "alert, confirm et prompt ne s'affichent pas dans le bac à sable : montre le message ou pose la question dans la page elle-même.",
	},
	{
		re:     regexp.MustCompile(`\beval\s*\(|\bnew\s+Function\s*\(`),
		inHTML: true, inJS: true,
		advice: "eval et new Function sont bloqués par la politique de sécurité de l'app.",
	},
	{
		re:     regexp.MustCompile(`(?i)<script\b[^>]*\bsrc\s*=`),
		inHTML: true,
		advice: "<script src=…> ne charge rien (aucun fichier externe, aucun CDN) : tout le JavaScript va dans 'js'.",
	},
	{
		re:     regexp.MustCompile(`(?i)<link\b[^>]*\bhref\s*=`),
		inHTML: true,
		advice: "<link href=…> ne charge rien (aucun fichier externe, aucune police distante) : tout le style va dans 'css'.",
	},
	{
		re:     regexp.MustCompile(`(?i)\b(?:src|href|action|poster)\s*=\s*["']?\s*(?:https?:)?//|url\(\s*["']?\s*(?:https?:)?//|@import\b`),
		inHTML: true, inCSS: true,
		advice: "Adresse externe (http…) : images, polices et feuilles distantes sont bloquées, et un lien externe ne s'ouvre pas. Embarque les images (SVG en ligne, emoji, data:) et affiche les adresses comme du texte à copier.",
	},
	{
		re:     regexp.MustCompile(`(?i)<form\b|\bonsubmit\b|["']submit["']`),
		inHTML: true, inJS: true,
		advice: "La soumission d'un <form> est bloquée et l'événement submit n'arrive jamais : réagis au clic du bouton et à la touche Entrée (keydown).",
	},
	{
		re:     regexp.MustCompile(`(?i)<(?:!doctype|html|head|body)\b`),
		inHTML: true,
		advice: "'html' doit être un fragment de corps : d'un document complet, seuls le contenu de <body> et les <style> de <head> sont gardés (le reste de <head>, scripts compris, est ignoré).",
	},
}

var (
	// Identifiants que le script va chercher par leur nom.
	lookupRe = regexp.MustCompile("getElementById\\(\\s*[\"'`]([^\"'`$]+)[\"'`]\\s*\\)|querySelector(?:All)?\\(\\s*[\"'`]#([\\w-]+)[\"'`]\\s*\\)")
	// Identifiants posés : dans le HTML, ou par le script (gabarits, .id = …).
	definedRe = regexp.MustCompile(`\bid\s*=\s*\\?["']?([^"'\s>\\]+)|setAttribute\(\s*["']id["']\s*,\s*["']([^"']+)`)

	// Syntaxes que les navigateurs acceptent et que le vérificateur ne connaît pas.
	recentRe = regexp.MustCompile(`\b(?:accessor|using)\s+[#\w$]`)
)

// check passe les fichiers d'une app en revue. Une erreur est un refus (l'app
// ne marcherait pas, à coup sûr) ; les avertissements sont rendus au modèle
// avec le résultat de l'enregistrement.
func check(s source) (warnings []string, err error) {
	for name, text := range map[string]string{"index.html": s.html, "style.css": s.css, "app.js": s.js} {
		if len(text) > maxFile {
			return nil, fmt.Errorf("%s trop gros (%d octets, max %d) : allège-le, une app reste petite", name, len(text), maxFile)
		}
	}
	if _, err := goja.Compile("app.js", s.js, false); err != nil {
		if !undecidable(s.js, err) {
			return nil, fmt.Errorf("app.js ne se compile pas : %v. (C'est un script classique : ni import ni export, et await seulement dans une fonction async.)", err)
		}
		warnings = append(warnings, fmt.Sprintf("app.js n'a pas pu être vérifié (%v) : cette syntaxe est trop récente pour le vérificateur. Relis-la, ou écris-la autrement.", err))
	}
	for _, r := range rules {
		if (r.inHTML && r.re.MatchString(s.html)) || (r.inCSS && r.re.MatchString(s.css)) || (r.inJS && r.re.MatchString(s.js)) {
			warnings = append(warnings, r.advice)
		}
	}
	if missing := missingIDs(s); len(missing) > 0 {
		warnings = append(warnings, "Identifiants cherchés par le script mais absents du HTML : "+strings.Join(missing, ", ")+" (getElementById et querySelector rendront null).")
	}
	return warnings, nil
}

// undecidable : le refus du compilateur peut-il venir d'une syntaxe valide
// qu'il ne connaît pas encore, plutôt que d'une faute ? Dans le doute, on
// avertit sans refuser : le navigateur tranchera.
func undecidable(js string, err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "not supported yet") || strings.Contains(msg, "RegExp") ||
		strings.Contains(msg, "regular expression") || recentRe.MatchString(js)
}

// missingIDs : les identifiants que le script cherche et que rien ne pose.
func missingIDs(s source) []string {
	defined := map[string]bool{}
	for _, m := range definedRe.FindAllStringSubmatch(s.html+"\n"+s.js, -1) {
		defined[m[1]+m[2]] = true
	}
	seen := map[string]bool{}
	var missing []string
	for _, m := range lookupRe.FindAllStringSubmatch(s.js, -1) {
		if id := m[1] + m[2]; !defined[id] && !seen[id] {
			seen[id] = true
			missing = append(missing, id)
		}
	}
	sort.Strings(missing)
	return missing
}
