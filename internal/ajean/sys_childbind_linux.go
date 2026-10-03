//go:build linux

package ajean

// Lier le Chrome du computer use à la vie d'AJEAN (voir sys_childbind_windows.go) :
// le noyau lui envoie SIGKILL quand AJEAN meurt, même tué brutalement. Ses
// sous-processus suivent la mort du processus principal de Chrome.

import (
	"os/exec"
	"syscall"
)

func prepareBoundChild(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}

func bindChild(cmd *exec.Cmd) {}

func killProcessTree(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
