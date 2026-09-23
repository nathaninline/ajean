package main

// strip.go — retire les commentaires de développement de l'UI publiée.
//
// Les sources (ui/src/) sont abondamment commentées pour les développeurs ; ces
// commentaires n'ont rien à faire dans la page servie aux utilisateurs (visibles
// dans « afficher le code source », et ~40 % du poids). On ne MINIFIE pas pour
// autant : le code reste tel quel, ligne pour ligne, parce que
// ajean-app/build-server-ui.ps1 repère certaines fonctions (jfetch…) au caractère
// près dans index.html.
//
// Le JS est découpé par un petit lexer qui connaît les chaînes ('…', "…"), les
// gabarits (`…${…}…`, imbrications comprises), les regex littérales (/…/ distinguée
// de la division d'après le jeton précédent) et les deux formes de commentaire.
// Le résultat est VÉRIFIÉ : l'analyseur JS complet de tdewolff doit produire le même
// programme avant et après (voir verifySameJS), sinon l'assemblage échoue.

import (
	"bytes"
	"fmt"
	"regexp"
	"strings"

	"github.com/tdewolff/parse/v2"
	"github.com/tdewolff/parse/v2/js"
)

// Jetons après lesquels un « / » ouvre une regex (et non une division).
var regexAfterWord = map[string]bool{
	"return": true, "typeof": true, "instanceof": true, "in": true, "of": true, "new": true,
	"delete": true, "void": true, "throw": true, "case": true, "do": true, "else": true,
	"yield": true, "await": true,
}

func isIdentByte(c byte) bool {
	return c == '_' || c == '$' || c >= 0x80 || (c >= '0' && c <= '9') || (c|0x20 >= 'a' && c|0x20 <= 'z')
}

// stripJSComments renvoie le code sans ses commentaires. Un commentaire qui
// contenait un saut de ligne est remplacé par un saut de ligne (pour ne pas
// souder deux lignes et changer l'insertion automatique des « ; »), sinon par une
// espace. Les lignes devenues vides sont retirées.
func stripJSComments(src string) (string, error) {
	var out strings.Builder
	out.Grow(len(src))
	n := len(src)
	// prevSig : dernier caractère significatif émis ; prevWord : dernier mot.
	var prevSig byte
	prevWord := ""
	// Pile des gabarits : pour chaque `…${ ouvert, profondeur d'accolades.
	var tmpl []int
	i := 0
	emit := func(s string) {
		out.WriteString(s)
		for j := len(s) - 1; j >= 0; j-- {
			if c := s[j]; c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				prevSig = c
				break
			}
		}
	}
	readString := func(q byte) error {
		j := i + 1
		for j < n {
			c := src[j]
			if c == '\\' {
				j += 2
				continue
			}
			if c == q {
				j++
				emit(src[i:j])
				i = j
				return nil
			}
			if c == '\n' {
				return fmt.Errorf("chaîne non fermée (octet %d)", i)
			}
			j++
		}
		return fmt.Errorf("chaîne non fermée (octet %d)", i)
	}
	// readTemplate lit depuis i (sur ` ou sur la } qui referme un ${) jusqu'à la
	// fin du gabarit ou jusqu'au prochain ${ (qu'il empile).
	readTemplate := func() error {
		j := i + 1
		for j < n {
			c := src[j]
			if c == '\\' {
				j += 2
				continue
			}
			if c == '`' {
				j++
				emit(src[i:j])
				i = j
				return nil
			}
			if c == '$' && j+1 < n && src[j+1] == '{' {
				j += 2
				emit(src[i:j])
				i = j
				tmpl = append(tmpl, 0)
				prevSig = '{'
				return nil
			}
			j++
		}
		return fmt.Errorf("gabarit non fermé (octet %d)", i)
	}
	for i < n {
		c := src[i]
		switch {
		case c == '/' && i+1 < n && src[i+1] == '/':
			j := strings.IndexByte(src[i:], '\n')
			if j < 0 {
				i = n
			} else {
				i += j // garde le saut de ligne
			}
		case c == '/' && i+1 < n && src[i+1] == '*':
			j := strings.Index(src[i+2:], "*/")
			if j < 0 {
				return "", fmt.Errorf("commentaire /* non fermé (octet %d)", i)
			}
			body := src[i : i+2+j+2]
			// /*! … */ : mention de licence (bibliothèques embarquées, MIT…) à
			// conserver telle quelle.
			if strings.HasPrefix(body, "/*!") {
				out.WriteString(body)
				i += len(body)
				continue
			}
			if strings.Contains(body, "\n") {
				out.WriteString("\n")
			} else {
				out.WriteString(" ")
			}
			i += len(body)
		case c == '\'' || c == '"':
			if err := readString(c); err != nil {
				return "", err
			}
			prevWord = ""
		case c == '`':
			if err := readTemplate(); err != nil {
				return "", err
			}
			prevWord = ""
		case c == '{':
			if len(tmpl) > 0 {
				tmpl[len(tmpl)-1]++
			}
			emit("{")
			i++
			prevWord = ""
		case c == '}':
			if len(tmpl) > 0 && tmpl[len(tmpl)-1] == 0 {
				tmpl = tmpl[:len(tmpl)-1] // fin d'un ${…} : on reprend le gabarit
				if err := readTemplate(); err != nil {
					return "", err
				}
				prevWord = ""
				continue
			}
			if len(tmpl) > 0 {
				tmpl[len(tmpl)-1]--
			}
			emit("}")
			i++
			prevWord = ""
		case c == '/':
			// Regex si rien de significatif avant, ou après un opérateur/ponctuation
			// ouvrante, ou après un mot-clé qui attend une expression.
			isRe := prevSig == 0 || strings.IndexByte("(,=:[!&|?{};+-*%<>~^", prevSig) >= 0 || regexAfterWord[prevWord]
			if !isRe {
				emit("/")
				i++
				prevWord = ""
				continue
			}
			j := i + 1
			inClass := false
			for j < n {
				d := src[j]
				if d == '\\' {
					j += 2
					continue
				}
				if d == '\n' {
					return "", fmt.Errorf("regex non fermée (octet %d)", i)
				}
				if d == '[' {
					inClass = true
				} else if d == ']' {
					inClass = false
				} else if d == '/' && !inClass {
					break
				}
				j++
			}
			j++ // le / final
			for j < n && isIdentByte(src[j]) {
				j++ // drapeaux (gimsuy)
			}
			emit(src[i:j])
			i = j
			prevWord = ""
		case isIdentByte(c):
			j := i
			for j < n && isIdentByte(src[j]) {
				j++
			}
			prevWord = src[i:j]
			emit(prevWord)
			i = j
		default:
			if c != ' ' && c != '\t' && c != '\n' && c != '\r' {
				prevWord = ""
				emit(src[i : i+1])
			} else {
				out.WriteByte(c)
			}
			i++
		}
	}
	return dropBlankLines(out.String()), nil
}

// stripCSSComments : /* … */ hors chaînes.
func stripCSSComments(src string) (string, error) {
	var out strings.Builder
	n := len(src)
	for i := 0; i < n; {
		c := src[i]
		switch {
		case c == '/' && i+1 < n && src[i+1] == '*':
			j := strings.Index(src[i+2:], "*/")
			if j < 0 {
				return "", fmt.Errorf("commentaire CSS non fermé (octet %d)", i)
			}
			if src[i+2] == '!' { // mention de licence : conservée
				out.WriteString(src[i : i+2+j+2])
			}
			i += 2 + j + 2
		case c == '"' || c == '\'':
			j := i + 1
			for j < n && src[j] != c {
				if src[j] == '\\' {
					j++
				}
				j++
			}
			out.WriteString(src[i:min(j+1, n)])
			i = j + 1
		default:
			out.WriteByte(c)
			i++
		}
	}
	return dropBlankLines(out.String()), nil
}

var htmlComment = regexp.MustCompile(`(?s)<!--.*?-->`)

// stripHTMLComments : le gabarit ne contient que du balisage (le CSS et le JS y
// sont insérés APRÈS), ses commentaires <!-- … --> s'enlèvent donc sans risque.
func stripHTMLComments(src string) string {
	return dropBlankLines(htmlComment.ReplaceAllString(src, ""))
}

var blankLines = regexp.MustCompile(`(?m)^[ \t]*\r?\n`)

func dropBlankLines(s string) string { return blankLines.ReplaceAllString(s, "") }

// verifySameJS garantit que retirer les commentaires n'a rien changé au programme :
// les deux versions, analysées par tdewolff, doivent donner le même JS normalisé.
func verifySameJS(before, after string) error {
	norm := func(s string) (string, error) {
		ast, err := js.Parse(parse.NewInputString(s), js.Options{})
		if err != nil {
			return "", err
		}
		var b bytes.Buffer
		ast.JS(&b)
		return b.String(), nil
	}
	a, err := norm(before)
	if err != nil {
		return fmt.Errorf("analyse du JS source : %w", err)
	}
	b, err := norm(after)
	if err != nil {
		return fmt.Errorf("analyse du JS sans commentaires : %w", err)
	}
	if a != b {
		i := 0
		for i < len(a) && i < len(b) && a[i] == b[i] {
			i++
		}
		lo := max(0, i-80)
		return fmt.Errorf("le retrait des commentaires a changé le programme près de : %q", a[lo:min(len(a), i+80)])
	}
	return nil
}
