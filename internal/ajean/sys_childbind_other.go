//go:build !windows && !linux

package ajean

import "os/exec"

// macOS n'a pas d'équivalent simple (ni job object, ni Pdeathsig) : rien ici,
// le navigateur est fermé normalement à l'arrêt propre d'AJEAN.
func prepareBoundChild(cmd *exec.Cmd) {}

func bindChild(cmd *exec.Cmd) {}

func killProcessTree(cmd *exec.Cmd) { _ = cmd.Process.Kill() }
