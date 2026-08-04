//go:build !windows

package ajean

// Pendant non-Windows de sys_postupdate_windows.go : la migration d'agencement
// se fait dans restartAfterUpdate (voir sys_layout.go), qui tourne deja en root.
func postUpdateMigrate() {}
