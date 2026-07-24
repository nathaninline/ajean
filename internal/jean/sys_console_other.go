//go:build !windows

package jean

// setupConsole : hors Windows, le process a toujours une vraie console/tty
// standard (stdout hérité). Rien à faire → on signale simplement « on a une
// console » pour que le double-clic Windows soit le seul cas « mode application ».
func setupConsole() bool { return true }
