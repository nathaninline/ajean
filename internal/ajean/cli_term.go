package ajean

// cli_term.go : primitives terminal du chat en ligne de commande (ajean chat) :
// passage en mode brut, lecture et décodage des touches, largeur d'affichage.
//
// Le chat reste « en ligne » (façon pi / Claude Code) : rien ne prend l'écran,
// le texte défile dans le terminal et garde scrollback, copier-coller et
// recherche natifs. On ne pilote que la zone de saisie et la ligne d'état.

import (
	"io"
	"os"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

// keyKind : type de touche décodée depuis le flux d'entrée.
type keyKind int

const (
	keyRune keyKind = iota
	keyEnter
	keyNewline // Alt+Entrée / Ctrl+J : saut de ligne sans envoyer
	keyBackspace
	keyDelete
	keyLeft
	keyRight
	keyUp
	keyDown
	keyHome
	keyEnd
	keyWordLeft
	keyWordRight
	keyTab
	keyEsc
	keyCtrlC
	keyCtrlD
	keyCtrlA
	keyCtrlE
	keyCtrlK
	keyCtrlU
	keyCtrlW
	keyCtrlL
	keyPaste // texte collé (bracketed paste ou rafale contenant des retours ligne)
	keyUnknown
)

type key struct {
	kind keyKind
	r    rune
	text string // keyPaste
}

// parseKeys décode un tampon d'octets en touches. Renvoie les touches reconnues
// et le reste (séquence d'échappement incomplète, à compléter au prochain read).
// final=true : aucune donnée ne suit, un ESC isolé est donc la touche Échap.
func parseKeys(b []byte, final bool) ([]key, []byte) {
	var out []key
	// Rafale lue d'un coup avec un retour ligne AU MILIEU : c'est un collage sur
	// un terminal sans bracketed paste (conhost Windows). Un CR n'y vaut pas
	// « envoyer » : on garde le texte entier comme un collage.
	if len(b) > 1 && !strings.Contains(string(b), "\x1b") {
		s := string(b)
		// Un retour ligne ENTRE du texte (pas « \r\r » d'un Entrée répété).
		if strings.ContainsAny(strings.Trim(s, "\r\n"), "\r\n") && utf8.ValidString(s) {
			return []key{{kind: keyPaste, text: s}}, nil
		}
	}
	for len(b) > 0 {
		c := b[0]
		switch {
		case c == 0x1b:
			if len(b) == 1 {
				if final {
					out = append(out, key{kind: keyEsc})
					return out, nil
				}
				return out, b
			}
			k, n, ok := parseEscape(b)
			if !ok {
				if final {
					out = append(out, key{kind: keyEsc})
					b = b[1:]
					continue
				}
				return out, b
			}
			if k.kind == keyPaste {
				// Bracketed paste : tout jusqu'à ESC[201~.
				rest := b[n:]
				end := strings.Index(string(rest), "\x1b[201~")
				if end < 0 {
					return out, b // collage pas encore reçu en entier
				}
				out = append(out, key{kind: keyPaste, text: string(rest[:end])})
				b = rest[end+len("\x1b[201~"):]
				continue
			}
			out = append(out, k)
			b = b[n:]
		case c == '\r':
			out = append(out, key{kind: keyEnter})
			b = b[1:]
			// CR LF (certains terminaux) : un seul Entrée.
			if len(b) > 0 && b[0] == '\n' {
				b = b[1:]
			}
		case c == '\n':
			out = append(out, key{kind: keyNewline})
			b = b[1:]
		case c == 0x7f || c == 0x08:
			out = append(out, key{kind: keyBackspace})
			b = b[1:]
		case c == '\t':
			out = append(out, key{kind: keyTab})
			b = b[1:]
		case c < 0x20:
			k := map[byte]keyKind{0x01: keyCtrlA, 0x03: keyCtrlC, 0x04: keyCtrlD, 0x05: keyCtrlE,
				0x0b: keyCtrlK, 0x0c: keyCtrlL, 0x15: keyCtrlU, 0x17: keyCtrlW, 0x02: keyLeft, 0x06: keyRight,
				0x10: keyUp, 0x0e: keyDown}[c]
			if k == 0 {
				k = keyUnknown
			}
			out = append(out, key{kind: k})
			b = b[1:]
		default:
			r, n := utf8.DecodeRune(b)
			if r == utf8.RuneError && n <= 1 {
				if !utf8.FullRune(b) && !final {
					return out, b
				}
				b = b[1:]
				continue
			}
			out = append(out, key{kind: keyRune, r: r})
			b = b[n:]
		}
	}
	return out, nil
}

// parseEscape décode une séquence commençant par ESC. ok=false : incomplète.
func parseEscape(b []byte) (key, int, bool) {
	if len(b) < 2 {
		return key{}, 0, false
	}
	switch b[1] {
	case '\r', '\n':
		return key{kind: keyNewline}, 2, true // Alt+Entrée
	case 'b':
		return key{kind: keyWordLeft}, 2, true
	case 'f':
		return key{kind: keyWordRight}, 2, true
	case 0x7f, 0x08:
		return key{kind: keyCtrlW}, 2, true // Alt+Retour arrière : mot précédent
	case 'O':
		if len(b) < 3 {
			return key{}, 0, false
		}
		switch b[2] {
		case 'H':
			return key{kind: keyHome}, 3, true
		case 'F':
			return key{kind: keyEnd}, 3, true
		case 'A':
			return key{kind: keyUp}, 3, true
		case 'B':
			return key{kind: keyDown}, 3, true
		case 'C':
			return key{kind: keyRight}, 3, true
		case 'D':
			return key{kind: keyLeft}, 3, true
		}
		return key{kind: keyUnknown}, 3, true
	case '[':
		// CSI : paramètres (chiffres, ;) puis un octet final 0x40..0x7e.
		i := 2
		for i < len(b) && (b[i] >= '0' && b[i] <= '9' || b[i] == ';') {
			i++
		}
		if i >= len(b) {
			return key{}, 0, false
		}
		params, fin := string(b[2:i]), b[i]
		n := i + 1
		mod := ""
		if j := strings.IndexByte(params, ';'); j >= 0 {
			mod = params[j+1:]
			params = params[:j]
		}
		ctrl := mod == "5" || mod == "3" // Ctrl ou Alt + flèche : mot par mot
		switch fin {
		case 'A':
			return key{kind: keyUp}, n, true
		case 'B':
			return key{kind: keyDown}, n, true
		case 'C':
			if ctrl {
				return key{kind: keyWordRight}, n, true
			}
			return key{kind: keyRight}, n, true
		case 'D':
			if ctrl {
				return key{kind: keyWordLeft}, n, true
			}
			return key{kind: keyLeft}, n, true
		case 'H':
			return key{kind: keyHome}, n, true
		case 'F':
			return key{kind: keyEnd}, n, true
		case '~':
			switch params {
			case "1", "7":
				return key{kind: keyHome}, n, true
			case "4", "8":
				return key{kind: keyEnd}, n, true
			case "3":
				return key{kind: keyDelete}, n, true
			case "200":
				return key{kind: keyPaste}, n, true
			case "13":
				return key{kind: keyNewline}, n, true // Shift+Entrée (kitty/xterm modifyOtherKeys)
			}
		case 'u':
			// CSI u (kitty) : 13;2u = Shift+Entrée.
			if params == "13" {
				return key{kind: keyNewline}, n, true
			}
		}
		return key{kind: keyUnknown}, n, true
	}
	// ESC + caractère : Alt+caractère, ignoré (on consomme juste l'ESC).
	return key{kind: keyEsc}, 1, true
}

// keyReader lit l'entrée en continu dans une goroutine et publie les touches.
// Il tourne pendant toute la session : pendant une réponse, Ctrl-C / Échap
// l'interrompent, et le reste de la frappe est gardé pour la saisie suivante.
type keyReader struct {
	ch   chan key
	once sync.Once
}

func newKeyReader(in io.Reader) *keyReader {
	kr := &keyReader{ch: make(chan key, 256)}
	go func() {
		buf := make([]byte, 4096)
		var pending []byte
		for {
			n, err := in.Read(buf)
			if n > 0 {
				pending = append(pending, buf[:n]...)
				var keys []key
				keys, pending = parseKeys(pending, false)
				// Un ESC resté seul en fin de lecture est la touche Échap (les
				// séquences arrivent d'un bloc) ; une séquence partielle attend.
				if len(pending) == 1 && pending[0] == 0x1b {
					keys = append(keys, key{kind: keyEsc})
					pending = nil
				}
				for _, k := range keys {
					kr.ch <- k
				}
			}
			if err != nil {
				close(kr.ch)
				return
			}
		}
	}()
	return kr
}

// rawTerm gère le mode brut de l'entrée standard.
type rawTerm struct {
	fd    int
	state *term.State
}

func enterRaw() (*rawTerm, error) {
	fd := int(os.Stdin.Fd())
	st, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	return &rawTerm{fd: fd, state: st}, nil
}

func (r *rawTerm) restore() {
	if r != nil && r.state != nil {
		_ = term.Restore(r.fd, r.state)
	}
}

func stdinIsTerminal() bool  { return term.IsTerminal(int(os.Stdin.Fd())) }
func stdoutIsTerminal() bool { return term.IsTerminal(int(os.Stdout.Fd())) }

// termWidth : largeur du terminal (80 par défaut, bornée pour rester lisible
// sur les très grands écrans n'a pas de sens ici : on suit le terminal).
func termWidth() int {
	w, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || w < 20 {
		return 80
	}
	return w
}

// crlfWriter : en mode brut (Unix), « \n » ne ramène plus en début de ligne.
// Toute sortie du chat passe par là pour rester correcte dans les deux modes.
type crlfWriter struct{ w io.Writer }

func (c crlfWriter) Write(p []byte) (int, error) {
	s := strings.ReplaceAll(strings.ReplaceAll(string(p), "\r\n", "\n"), "\n", "\r\n")
	if _, err := io.WriteString(c.w, s); err != nil {
		return 0, err
	}
	return len(p), nil
}

// runeWidth : colonnes occupées par un caractère (0 combinant, 2 large/emoji).
func runeWidth(r rune) int {
	switch {
	case r == 0 || r < 32 || r == 0x7f:
		return 0
	case unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) || r == 0x200d || (r >= 0xfe00 && r <= 0xfe0f):
		return 0
	case r >= 0x1100 && (r <= 0x115f || r == 0x2329 || r == 0x232a ||
		(r >= 0x2e80 && r <= 0xa4cf && r != 0x303f) ||
		(r >= 0xac00 && r <= 0xd7a3) ||
		(r >= 0xf900 && r <= 0xfaff) ||
		(r >= 0xfe30 && r <= 0xfe6f) ||
		(r >= 0xff00 && r <= 0xff60) ||
		(r >= 0xffe0 && r <= 0xffe6) ||
		(r >= 0x1f300 && r <= 0x1f64f) ||
		(r >= 0x1f900 && r <= 0x1f9ff) ||
		(r >= 0x1f680 && r <= 0x1f6ff) ||
		(r >= 0x20000 && r <= 0x3fffd)):
		return 2
	}
	return 1
}

// visibleWidth : largeur affichée d'une chaîne, séquences ANSI ignorées.
func visibleWidth(s string) int {
	w := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			i = skipANSI(s, i)
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		w += runeWidth(r)
		i += n
	}
	return w
}

// skipANSI renvoie l'indice juste après la séquence d'échappement en s[i].
func skipANSI(s string, i int) int {
	j := i + 1
	if j < len(s) && s[j] == '[' {
		j++
		for j < len(s) && (s[j] < 0x40 || s[j] > 0x7e) {
			j++
		}
		return j + 1
	}
	if j < len(s) && s[j] == ']' { // OSC ... BEL
		for j < len(s) && s[j] != 0x07 {
			j++
		}
		return j + 1
	}
	return j + 1
}

// truncateWidth coupe s (sans ANSI) à w colonnes, avec « … » si coupé.
func truncateWidth(s string, w int) string {
	if w <= 1 || visibleWidth(s) <= w {
		return s
	}
	out, cw := strings.Builder{}, 0
	for _, r := range s {
		rw := runeWidth(r)
		if cw+rw > w-1 {
			break
		}
		out.WriteRune(r)
		cw += rw
	}
	return out.String() + "…"
}

// rowsFor : nombre de lignes d'écran occupées par un texte de largeur w.
func rowsFor(w, width int) int {
	if w <= 0 || width <= 0 {
		return 1
	}
	return (w + width - 1) / width
}
