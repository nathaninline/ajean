package ajean

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// findNvcc returns the path to nvcc from PATH or a /usr/local/cuda* install,
// preferring the highest version.
func findNvcc() string {
	if p, err := exec.LookPath("nvcc"); err == nil {
		return p
	}
	if runtime.GOOS == "windows" {
		// CUDA_PATH est posé par l'installeur officiel.
		if cp := os.Getenv("CUDA_PATH"); cp != "" {
			if p := filepath.Join(cp, "bin", "nvcc.exe"); isFile(p) {
				return p
			}
		}
		// Layout standard : …\NVIDIA GPU Computing Toolkit\CUDA\v12.x\bin\nvcc.exe
		for _, base := range []string{os.Getenv("ProgramFiles"), `C:\Program Files`} {
			if base == "" {
				continue
			}
			matches, _ := filepath.Glob(filepath.Join(base, "NVIDIA GPU Computing Toolkit", "CUDA", "v*", "bin", "nvcc.exe"))
			if len(matches) > 0 {
				return highestVersionedPath(matches) // tri sémantique : v12.10 > v12.4, v13.4 > v9.0
			}
		}
		return ""
	}
	return ""
}

// highestVersionedPath renvoie, parmi des chemins porteurs d'un numéro de version,
// celui dont la version est la plus élevée (tri sémantique). Les chemins sans
// version détectable ne l'emportent jamais sur un chemin versionné. "" si la
// liste est vide.
func highestVersionedPath(paths []string) string {
	best, bestVer := "", ""
	for _, p := range paths {
		v := pathVersion(p)
		if best == "" || (v != "" && cmpVersion(v, bestVer) > 0) {
			best, bestVer = p, v
		}
	}
	return best
}
