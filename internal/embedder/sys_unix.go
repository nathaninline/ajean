//go:build !windows

package embedder

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// prepare et adopt : rien à faire hors de Windows (pas de fenêtre de console à
// masquer).
func prepare(*exec.Cmd) {}
func adopt(*exec.Cmd)   {}

// freeRAM : la mémoire vive disponible, en Mo. known=false là où le système ne
// la donne pas dans /proc/meminfo (macOS) : on ne bloque alors rien.
func freeRAM() (mb int, known bool) {
	raw, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, false
	}
	for line := range strings.Lines(string(raw)) {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "MemAvailable:" {
			kb, err := strconv.Atoi(fields[1])
			return kb / 1024, err == nil
		}
	}
	return 0, false
}
