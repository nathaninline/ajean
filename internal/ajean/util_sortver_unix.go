//go:build !windows

package ajean

import (
	"os"
	"path/filepath"
	"sort"
)

// sortByVersionDesc trie des chemins par version décroissante (sémantique).
func sortByVersionDesc(paths []string) {
	sort.SliceStable(paths, func(i, j int) bool {
		return cmpVersion(pathVersion(paths[i]), pathVersion(paths[j])) > 0
	})
}

// unixCudaToolkitNvcc : nvcc d'un toolkit installé hors /usr/local, désigné par
// CUDA_PATH / CUDA_HOME ou au chemin d'Arch (/opt/cuda). "" si aucun.
func unixCudaToolkitNvcc() string {
	for _, root := range []string{os.Getenv("CUDA_PATH"), os.Getenv("CUDA_HOME"), "/opt/cuda"} {
		if root == "" {
			continue
		}
		if p := filepath.Join(root, "bin", "nvcc"); isFile(p) {
			return p
		}
	}
	return ""
}
