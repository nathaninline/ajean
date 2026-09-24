//go:build windows

package ajean

import (
	"io"
	"os"
	"syscall"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"
)

// consoleInput lit la console en UTF-16 (ReadConsoleW). Un ReadFile en page de
// code UTF-8 rend des octets nuls pour tout caractère non ASCII sur une partie
// des consoles Windows : « é », « à », « ç » disparaissaient de la saisie.
// En mode brut avec ENABLE_VIRTUAL_TERMINAL_INPUT, les touches spéciales
// arrivent en séquences VT comme ailleurs, donc parseKeys s'applique tel quel.
type consoleInput struct {
	h       syscall.Handle
	pending []byte
	hi      uint16 // moitié haute d'une paire de substitution en attente
}

var procReadConsoleW = syscall.NewLazyDLL("kernel32.dll").NewProc("ReadConsoleW")

func (c *consoleInput) Read(p []byte) (int, error) {
	for len(c.pending) == 0 {
		var buf [512]uint16
		var n uint32
		r, _, err := procReadConsoleW.Call(uintptr(c.h), uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)), uintptr(unsafe.Pointer(&n)), 0)
		if r == 0 {
			return 0, err
		}
		if n == 0 {
			return 0, io.EOF
		}
		u := buf[:n]
		if c.hi != 0 {
			u = append([]uint16{c.hi}, u...)
			c.hi = 0
		}
		if last := u[len(u)-1]; last >= 0xd800 && last < 0xdc00 {
			c.hi = last
			u = u[:len(u)-1]
		}
		for _, r := range utf16.Decode(u) {
			c.pending = utf8.AppendRune(c.pending, r)
		}
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

func terminalInput() io.Reader {
	return &consoleInput{h: syscall.Handle(os.Stdin.Fd())}
}
