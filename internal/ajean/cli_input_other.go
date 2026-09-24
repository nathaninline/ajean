//go:build !windows

package ajean

import (
	"io"
	"os"
)

func terminalInput() io.Reader { return os.Stdin }
