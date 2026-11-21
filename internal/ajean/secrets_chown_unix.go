//go:build !windows

package ajean

import (
	"os"
	"syscall"
)

// chownLikeHome donne path au propriétaire d'AJEAN_HOME (utile quand une
// commande tourne sous sudo alors que le service tourne sous un autre compte).
func chownLikeHome(path string) {
	if os.Geteuid() != 0 {
		return
	}
	info, err := os.Stat(AjeanHome())
	if err != nil {
		return
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		_ = os.Chown(path, int(st.Uid), int(st.Gid))
	}
}
