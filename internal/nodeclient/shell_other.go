//go:build !windows

package nodeclient

import (
	"context"
	"os/exec"
)

// shellCmd construit la commande shell hors Windows : bash -c.
func shellCmd(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "bash", "-c", command)
}
