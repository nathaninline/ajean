//go:build !windows

package ajean

// Pendant non-Windows de sys_restart_windows.go : sous Linux le redemarrage
// passe par systemd (restartAfterUpdate), qui sait deja relancer proprement un
// service. Il n'y a pas d'accompagnateur a lancer.

const restartArg = "restart-after-update"

func scheduleAppRestart() (bool, string) { return false, "" }

func cmdRestartAfterUpdate([]string) error { return nil }
