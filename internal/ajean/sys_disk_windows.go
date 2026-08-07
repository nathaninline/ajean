package ajean

// sys_disk_windows.go — espace libre d'un volume via GetDiskFreeSpaceExW. On
// veut l'espace réellement disponible pour l'utilisateur courant (quotas
// compris), donc le premier paramètre de sortie, pas le total du volume.

import (
	"syscall"
	"unsafe"
)

var procGetDiskFreeSpaceExW = syscall.NewLazyDLL("kernel32.dll").NewProc("GetDiskFreeSpaceExW")

// diskFreeAt renvoie les octets libres du volume contenant dir, ou -1 si la
// mesure échoue. dir doit exister (l'appelant remonte les parents au besoin).
func diskFreeAt(dir string) int64 {
	p, err := syscall.UTF16PtrFromString(dir)
	if err != nil {
		return -1
	}
	var free, total, totalFree uint64
	r, _, _ := procGetDiskFreeSpaceExW.Call(
		uintptr(unsafe.Pointer(p)),
		uintptr(unsafe.Pointer(&free)),
		uintptr(unsafe.Pointer(&total)),
		uintptr(unsafe.Pointer(&totalFree)),
	)
	if r == 0 || free > 1<<62 {
		return -1
	}
	return int64(free)
}
