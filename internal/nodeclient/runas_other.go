//go:build !windows

package nodeclient

// runas_other.go — hors Windows, « agir comme l'utilisateur connecté » n'a pas
// lieu d'être : le poste tourne déjà dans un compte utilisateur ordinaire (pas
// de service LocalSystem à contourner). Les deux hooks sont donc neutres.

import "os/exec"

func applyRunAsUser(cmd *exec.Cmd) func() { return func() {} }

func withActiveUser(fn func() string) string { return fn() }
