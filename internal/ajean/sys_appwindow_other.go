//go:build !windows

package ajean

import "errors"

var errWindowsOnly = errors.New("commande réservée à Windows")

// Hors Windows, la fenêtre de l'application n'est pas pilotée : rouvrir reste
// sûr (voir appWindowAlive) et l'icône vient du bundle ou du navigateur.

func focusAppWindow() bool { return false }

func keepAppWindowIcon() {}

func cmdWindowIcon(args []string) error { return errWindowsOnly }

func cmdDialogTest(args []string) error { return errWindowsOnly }
