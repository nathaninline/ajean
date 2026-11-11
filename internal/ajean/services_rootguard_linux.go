//go:build linux

package ajean

// Gardien RAM, lancé en root par ajean-guard.service : si la
// mémoire disponible passe sous le seuil, il tue d'un coup (cgroup.kill) le
// service géré le plus gourmand, sinon le plus gros service non protégé, avant
// que la machine ne fige. Seuil : AJEAN_GUARD_MIB (défaut 2048).

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	guardEvery    = 50 * time.Millisecond
	guardCooldown = 10 * time.Second
	guardDefault  = 2048 // Mio
)

type GuardKill struct {
	Name  string `json:"name"`
	Bytes uint64 `json:"bytes"`
	At    int64  `json:"at"`
}

func guardKillsPath() string { return filepath.Join(rootDir, "guard.json") }

// GuardKills : les dernières interventions (lisible sans droits).
func GuardKills() []GuardKill {
	var k []GuardKill
	if b, err := os.ReadFile(guardKillsPath()); err == nil {
		_ = json.Unmarshal(b, &k)
	}
	return k
}

func osHasRootGuard() bool {
	out, _ := systemctlRead("is-active", "--", guardUnit)
	return out == "active"
}

func statValue(stat []byte, key string) uint64 {
	k := []byte(key + " ")
	i := bytes.Index(stat, k)
	if i < 0 || (i > 0 && stat[i-1] != '\n') {
		return 0
	}
	rest := stat[i+len(k):]
	if j := bytes.IndexByte(rest, '\n'); j >= 0 {
		rest = rest[:j]
	}
	v, _ := strconv.ParseUint(string(rest), 10, 64)
	return v
}

// biggestUnit : le service qui pèse le plus (hors cache libérable et unités
// protégées). managedOnly limite aux services gérés par AJEAN.
func biggestUnit(managedOnly bool) (name, path string, size uint64) {
	managed := map[string]bool{}
	for _, u := range rootLoadManaged() {
		managed[u] = true
	}
	var best uint64
	walk := func(root string) {
		filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			base := filepath.Base(p)
			if !strings.HasSuffix(base, ".service") && !strings.HasSuffix(base, ".scope") {
				return nil
			}
			if base != "user@.service" && strings.HasPrefix(base, "user@") {
				return nil // on descend : ses services sont examinés un par un
			}
			if protectedUnit(base) || (managedOnly && !managed[base]) {
				return filepath.SkipDir
			}
			cur, _ := strconv.ParseUint(strings.TrimSpace(readFileStr(p+"/memory.current")), 10, 64)
			stat, _ := os.ReadFile(p + "/memory.stat")
			if inactive := statValue(stat, "inactive_file"); inactive < cur {
				cur -= inactive
			}
			if cur > best {
				best, name, path = cur, base, p
			}
			return filepath.SkipDir
		})
	}
	walk("/sys/fs/cgroup/system.slice")
	if !managedOnly {
		walk("/sys/fs/cgroup/user.slice")
	}
	return name, path, best
}

func readFileStr(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

func parseMemAvail(buf []byte) int64 {
	i := bytes.Index(buf, []byte("MemAvailable:"))
	if i < 0 {
		return -1
	}
	var v int64
	seen := false
	for _, c := range buf[i+13:] {
		if c >= '0' && c <= '9' {
			v = v*10 + int64(c-'0')
			seen = true
		} else if seen {
			return v
		}
	}
	return -1
}

// cmdGuard : boucle du gardien (root).
func cmdGuard(args []string) error {
	mib, _ := strconv.ParseUint(os.Getenv("AJEAN_GUARD_MIB"), 10, 64)
	if mib == 0 {
		mib = guardDefault
	}
	// Reste en mémoire vive : le gardien doit répondre même si la machine sature.
	if err := syscall.Mlockall(syscall.MCL_CURRENT | syscall.MCL_FUTURE); err != nil {
		log.Printf("gardien : mlockall : %v", err)
	}
	log.Printf("gardien RAM actif : seuil %d Mio", mib)
	runtime.LockOSThread()
	limitKB := int64(mib) * 1024
	buf := make([]byte, 4096)
	fd, err := syscall.Open("/proc/meminfo", syscall.O_RDONLY, 0)
	if err != nil {
		return err
	}
	var quietUntil time.Time
	for {
		time.Sleep(guardEvery)
		n, err := syscall.Pread(fd, buf, 0)
		if err != nil || n <= 0 {
			continue
		}
		avail := parseMemAvail(buf[:n])
		if avail < 0 || avail >= limitKB || time.Now().Before(quietUntil) {
			continue
		}
		quietUntil = time.Now().Add(guardCooldown)
		name, path, size := biggestUnit(true)
		if path == "" {
			name, path, size = biggestUnit(false)
		}
		if path == "" {
			log.Printf("gardien : %d Ko disponibles, aucune cible", avail)
			continue
		}
		if err := os.WriteFile(path+"/cgroup.kill", []byte("1"), 0o644); err != nil {
			log.Printf("gardien : échec sur %s : %v", name, err)
			continue
		}
		log.Printf("gardien : %d Ko disponibles, %s tué (%d octets)", avail, name, size)
		kills := append(GuardKills(), GuardKill{strings.TrimSuffix(name, ".service"), size, time.Now().UnixMilli()})
		if len(kills) > 5 {
			kills = kills[len(kills)-5:]
		}
		if b, err := json.Marshal(kills); err == nil {
			_ = os.WriteFile(guardKillsPath(), b, 0o644)
		}
	}
}
