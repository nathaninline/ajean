package ajean

// cli_editor.go : zone de saisie du chat terminal. Édition sur place (flèches,
// mot à mot, début/fin), multi-ligne (Alt+Entrée, Ctrl+J ou « \ » en fin de
// ligne), collage conservé tel quel, historique persistant et complétion des
// commandes « / ». Une ligne d'aide discrète s'affiche sous la saisie.

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type editResult int

const (
	editSubmit    editResult = iota
	editEOF                  // Ctrl-D sur une saisie vide
	editInterrupt            // Ctrl-C sur une saisie vide
)

type lineEditor struct {
	out      io.Writer
	keys     <-chan key
	prompt   string // déjà colorée
	cont     string // préfixe des lignes suivantes
	hint     func(buf string) string
	history  []string
	histMax  int
	histFile string
	// completions : commandes « / » connues (pour Tab et la suggestion grisée).
	completions []string

	buf     []rune
	cur     int
	curRow  int // ligne (dans la zone) où se trouve le curseur à l'écran
	histIdx int
	draft   []rune
}

func newLineEditor(out io.Writer, keys <-chan key) *lineEditor {
	e := &lineEditor{out: out, keys: keys, prompt: accent("› "), cont: "  ", histMax: 500}
	if dir, err := os.UserConfigDir(); err == nil {
		e.histFile = filepath.Join(dir, "ajean", "chat_history")
		e.loadHistory()
	}
	return e
}

func (e *lineEditor) loadHistory() {
	f, err := os.Open(e.histFile)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		var s string
		if json.Unmarshal(sc.Bytes(), &s) == nil && s != "" {
			e.history = append(e.history, s)
		}
	}
	if len(e.history) > e.histMax {
		e.history = e.history[len(e.history)-e.histMax:]
	}
}

func (e *lineEditor) addHistory(s string) {
	if strings.TrimSpace(s) == "" || (len(e.history) > 0 && e.history[len(e.history)-1] == s) {
		return
	}
	e.history = append(e.history, s)
	if e.histFile == "" {
		return
	}
	_ = os.MkdirAll(filepath.Dir(e.histFile), 0o700)
	if f, err := os.OpenFile(e.histFile, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600); err == nil {
		b, _ := json.Marshal(s)
		_, _ = f.Write(append(b, '\n'))
		f.Close()
	}
}

// readLine affiche la zone de saisie (pré-remplie avec initial) et rend le texte.
func (e *lineEditor) readLine(initial string) (string, editResult) {
	e.buf = []rune(initial)
	e.cur = len(e.buf)
	e.curRow = 0
	e.histIdx = len(e.history)
	e.draft = nil
	e.render(false)
	for k := range e.keys {
		switch k.kind {
		case keyEnter:
			// « \ » en fin de ligne : continuation, comme un shell.
			if e.cur == len(e.buf) && e.cur > 0 && e.buf[e.cur-1] == '\\' {
				e.buf[e.cur-1] = '\n'
				break
			}
			s := string(e.buf)
			e.render(true)
			fmt.Fprint(e.out, "\n")
			e.addHistory(s)
			return s, editSubmit
		case keyNewline:
			e.insert([]rune{'\n'})
		case keyPaste:
			t := strings.ReplaceAll(strings.ReplaceAll(k.text, "\r\n", "\n"), "\r", "\n")
			t = strings.ReplaceAll(t, "\t", "    ")
			e.insert([]rune(t))
		case keyRune:
			e.insert([]rune{k.r})
		case keyBackspace:
			if e.cur > 0 {
				e.buf = append(e.buf[:e.cur-1], e.buf[e.cur:]...)
				e.cur--
			}
		case keyDelete:
			if e.cur < len(e.buf) {
				e.buf = append(e.buf[:e.cur], e.buf[e.cur+1:]...)
			}
		case keyLeft:
			if e.cur > 0 {
				e.cur--
			}
		case keyRight:
			if e.cur < len(e.buf) {
				e.cur++
			} else if g := e.ghost(); g != "" {
				e.insert([]rune(g))
			}
		case keyWordLeft:
			e.cur = wordLeft(e.buf, e.cur)
		case keyWordRight:
			e.cur = wordRight(e.buf, e.cur)
		case keyHome, keyCtrlA:
			e.cur = lineStart(e.buf, e.cur)
		case keyEnd, keyCtrlE:
			e.cur = lineEnd(e.buf, e.cur)
		case keyCtrlK:
			e.buf = append(e.buf[:e.cur], e.buf[lineEnd(e.buf, e.cur):]...)
		case keyCtrlU:
			s := lineStart(e.buf, e.cur)
			e.buf = append(e.buf[:s], e.buf[e.cur:]...)
			e.cur = s
		case keyCtrlW:
			s := wordLeft(e.buf, e.cur)
			e.buf = append(e.buf[:s], e.buf[e.cur:]...)
			e.cur = s
		case keyUp:
			if s := lineStart(e.buf, e.cur); s > 0 {
				e.cur = moveVertical(e.buf, e.cur, -1)
			} else {
				e.historyStep(-1)
			}
		case keyDown:
			if lineEnd(e.buf, e.cur) < len(e.buf) {
				e.cur = moveVertical(e.buf, e.cur, 1)
			} else {
				e.historyStep(1)
			}
		case keyTab:
			if g := e.ghost(); g != "" {
				e.insert([]rune(g))
			} else if strings.HasPrefix(string(e.buf), "/") && !strings.ContainsAny(string(e.buf), " \n") {
				e.listCompletions()
			} else {
				e.insert([]rune("    "))
			}
		case keyCtrlL:
			fmt.Fprint(e.out, "\x1b[H\x1b[2J")
			e.curRow = 0
		case keyCtrlC:
			if len(e.buf) == 0 {
				e.render(true)
				fmt.Fprint(e.out, "\n")
				return "", editInterrupt
			}
			// Ctrl-C sur une saisie non vide : on l'efface (comme un shell).
			e.buf, e.cur = nil, 0
		case keyCtrlD:
			if len(e.buf) == 0 {
				e.render(true)
				fmt.Fprint(e.out, "\n")
				return "", editEOF
			}
			if e.cur < len(e.buf) {
				e.buf = append(e.buf[:e.cur], e.buf[e.cur+1:]...)
			}
		case keyEsc:
			// Échap : rien à annuler en saisie ; on retire juste la suggestion.
		}
		e.render(false)
	}
	return "", editEOF
}

func (e *lineEditor) insert(rs []rune) {
	nb := make([]rune, 0, len(e.buf)+len(rs))
	nb = append(nb, e.buf[:e.cur]...)
	nb = append(nb, rs...)
	nb = append(nb, e.buf[e.cur:]...)
	e.buf = nb
	e.cur += len(rs)
}

func (e *lineEditor) historyStep(d int) {
	if len(e.history) == 0 {
		return
	}
	if e.histIdx == len(e.history) {
		e.draft = append([]rune(nil), e.buf...)
	}
	n := e.histIdx + d
	if n < 0 || n > len(e.history) {
		return
	}
	e.histIdx = n
	if n == len(e.history) {
		e.buf = append([]rune(nil), e.draft...)
	} else {
		e.buf = []rune(e.history[n])
	}
	e.cur = len(e.buf)
}

// ghost : complément unique de la commande « / » en cours (affiché grisé,
// accepté par Tab ou →).
func (e *lineEditor) ghost() string {
	s := string(e.buf)
	if e.cur != len(e.buf) || !strings.HasPrefix(s, "/") || strings.ContainsAny(s, " \n") {
		return ""
	}
	match := ""
	for _, c := range e.completions {
		if strings.HasPrefix(c, s) && c != s {
			if match != "" {
				return commonPrefix(match, c)[len(s):]
			}
			match = c
		}
	}
	if match == "" {
		return ""
	}
	return match[len(s):]
}

func (e *lineEditor) listCompletions() {
	s := string(e.buf)
	var m []string
	for _, c := range e.completions {
		if strings.HasPrefix(c, s) {
			m = append(m, c)
		}
	}
	if len(m) == 0 {
		return
	}
	e.render(true)
	fmt.Fprint(e.out, "\n"+dim(strings.Join(m, "  "))+"\n")
	e.curRow = 0
}

func commonPrefix(a, b string) string {
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	return a[:i]
}

// layout découpe la saisie en lignes d'écran et situe le curseur.
func (e *lineEditor) layout(width int, withGhost bool) (rows []string, curRow, curCol int) {
	row := strings.Builder{}
	row.WriteString(e.prompt)
	col := visibleWidth(e.prompt)
	placed := false
	for i, r := range e.buf {
		if col >= width {
			rows = append(rows, row.String())
			row.Reset()
			col = 0
		}
		if i == e.cur && !placed {
			curRow, curCol, placed = len(rows), col, true
		}
		if r == '\n' {
			rows = append(rows, row.String())
			row.Reset()
			row.WriteString(e.cont)
			col = visibleWidth(e.cont)
			continue
		}
		rw := runeWidth(r)
		if col+rw > width {
			rows = append(rows, row.String())
			row.Reset()
			col = 0
			if i == e.cur {
				curRow, curCol = len(rows), 0
			}
		}
		row.WriteRune(r)
		col += rw
	}
	if !placed {
		if col >= width {
			rows = append(rows, row.String())
			row.Reset()
			col = 0
		}
		curRow, curCol = len(rows), col
	}
	if withGhost {
		if g := e.ghost(); g != "" && col+visibleWidth(g) < width {
			row.WriteString(dim(g))
		}
	}
	rows = append(rows, row.String())
	return rows, curRow, curCol
}

// render redessine la zone de saisie. final=true : dernier rendu avant envoi
// (sans suggestion ni ligne d'aide, curseur laissé en fin de texte).
func (e *lineEditor) render(final bool) {
	width := termWidth()
	rows, curRow, curCol := e.layout(width, !final)
	var b strings.Builder
	if e.curRow > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", e.curRow)
	}
	b.WriteString("\r\x1b[J")
	b.WriteString(strings.Join(rows, "\r\n"))
	if final {
		e.curRow = 0
		fmt.Fprint(e.out, b.String())
		return
	}
	below := len(rows) - 1 - curRow
	if e.hint != nil {
		if h := e.hint(string(e.buf)); h != "" {
			b.WriteString("\r\n" + dim(truncateWidth(h, width-1)))
			below++
		}
	}
	if below > 0 {
		fmt.Fprintf(&b, "\x1b[%dA", below)
	}
	b.WriteString("\r")
	if curCol > 0 {
		fmt.Fprintf(&b, "\x1b[%dC", curCol)
	}
	e.curRow = curRow
	fmt.Fprint(e.out, b.String())
}

func lineStart(b []rune, i int) int {
	for i > 0 && b[i-1] != '\n' {
		i--
	}
	return i
}

func lineEnd(b []rune, i int) int {
	for i < len(b) && b[i] != '\n' {
		i++
	}
	return i
}

func moveVertical(b []rune, i, d int) int {
	col := i - lineStart(b, i)
	var s int
	if d < 0 {
		s = lineStart(b, lineStart(b, i)-1)
	} else {
		s = lineEnd(b, i) + 1
	}
	return min(s+col, lineEnd(b, s))
}

func isWordRune(r rune) bool {
	return r != ' ' && r != '\n' && r != '\t' && !strings.ContainsRune("/.,;:-_()[]{}\"'`", r)
}

func wordLeft(b []rune, i int) int {
	for i > 0 && !isWordRune(b[i-1]) {
		i--
	}
	for i > 0 && isWordRune(b[i-1]) {
		i--
	}
	return i
}

func wordRight(b []rune, i int) int {
	for i < len(b) && !isWordRune(b[i]) {
		i++
	}
	for i < len(b) && isWordRune(b[i]) {
		i++
	}
	return i
}
