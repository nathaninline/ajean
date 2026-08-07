//go:build !windows

package ajean

// sys_disk_other.go — espace libre d'un système de fichiers via statfs(2).
// Bavail (et pas Bfree) : les blocs réservés à root ne sont pas utilisables pour
// poser un .gguf de 40 Go.

import "syscall"

// diskFreeAt renvoie les octets libres du système de fichiers contenant dir, ou
// -1 si la mesure échoue. dir doit exister (l'appelant remonte les parents au
// besoin).
func diskFreeAt(dir string) int64 {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return -1
	}
	// Les types de Bavail/Bsize diffèrent selon l'OS (int64 sous Linux, uint64
	// et uint32 sous macOS) : on passe par uint64 avant de multiplier.
	free := uint64(st.Bavail) * uint64(st.Bsize)
	if free > 1<<62 {
		return -1
	}
	return int64(free)
}
