package ajean

import (
	"archive/tar"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// cloud_runtime.go : le client Modal n'existe qu'en Python. Pour que les presets
// GPU cloud marchent sans rien installer, AJEAN télécharge au premier usage un
// Python portable (python-build-standalone, somme SHA-256 vérifiée) dans
// AJEAN_HOME/cloud/runtime, puis y installe le paquet `modal`. Une CLI Modal
// déjà présente sur la machine reste utilisée tant que ce runtime n'existe pas.

const pbsRelease = "20260924"
const pbsPython = "3.12.14"

// Archives « install_only_stripped » et leurs sommes (SHA256SUMS de la release).
var pbsBuilds = map[string][2]string{
	"windows/amd64": {"x86_64-pc-windows-msvc", "c5bf8edfe858c1df9891be498b5bbc8761d383df5b9790658b088fea4870433a"},
	"linux/amd64":   {"x86_64-unknown-linux-gnu", "269b2c99e4db15b242bf01832f4fea1e8f1a664f273cff519393f296e9820b41"},
	"linux/arm64":   {"aarch64-unknown-linux-gnu", "c8499b61252c433280f134df954464d19811527b31cb920c35fc6967c1222e35"},
	"darwin/amd64":  {"x86_64-apple-darwin", "7ea9761b9069c10b9a20531d568645849d604c59e9c7f11f6659f1e1790c968e"},
	"darwin/arm64":  {"aarch64-apple-darwin", "c2edb321cd32ec2b170df208db0446dccc4398db602ca27cf2079098fb1f7d9d"},
}

func cloudRuntimeDir() string { return filepath.Join(AjeanHome(), "cloud", "runtime") }

func cloudRuntimePython() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(cloudRuntimeDir(), "python", "python.exe")
	}
	return filepath.Join(cloudRuntimeDir(), "python", "bin", "python3")
}

// État de l'installation, lu par /api/cloud/runtime.
var cloudRT struct {
	sync.Mutex
	state string // "" | installing | ready | error
	step  string
	err   string
}

func cloudRuntimeStatus() (state, step, errMsg string) {
	cloudRT.Lock()
	defer cloudRT.Unlock()
	if cloudRT.state == "" && cloudRuntimeReady() {
		cloudRT.state = "ready"
	}
	return cloudRT.state, cloudRT.step, cloudRT.err
}

// cloudRuntimeReady : le Python privé est là et le paquet modal s'importe.
// Le marqueur évite de relancer Python à chaque appel.
func cloudRuntimeReady() bool {
	_, err := os.Stat(filepath.Join(cloudRuntimeDir(), ".ready"))
	return err == nil
}

// startCloudRuntimeInstall lance l'installation en tâche de fond (sans effet si
// elle tourne déjà ou si le runtime est prêt).
func startCloudRuntimeInstall() {
	cloudRT.Lock()
	if cloudRT.state == "installing" || cloudRuntimeReady() {
		cloudRT.Unlock()
		return
	}
	cloudRT.state, cloudRT.step, cloudRT.err = "installing", "téléchargement de Python", ""
	cloudRT.Unlock()
	go func() {
		err := installCloudRuntime(func(step string) {
			cloudRT.Lock()
			cloudRT.step = step
			cloudRT.Unlock()
		})
		cloudRT.Lock()
		defer cloudRT.Unlock()
		if err != nil {
			cloudRT.state, cloudRT.err = "error", err.Error()
			fmt.Printf("%s composant GPU cloud : %v\n", red("[ERREUR]"), err)
			return
		}
		cloudRT.state, cloudRT.step = "ready", ""
		fmt.Printf("%s composant GPU cloud installé\n", green("[ok]"))
	}()
}

func installCloudRuntime(progress func(string)) error {
	b, ok := pbsBuilds[runtime.GOOS+"/"+runtime.GOARCH]
	if !ok {
		return fmt.Errorf("GPU cloud non pris en charge sur %s/%s", runtime.GOOS, runtime.GOARCH)
	}
	dir := cloudRuntimeDir()
	_ = os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	name := fmt.Sprintf("cpython-%s+%s-%s-install_only_stripped.tar.gz", pbsPython, pbsRelease, b[0])
	url := "https://github.com/astral-sh/python-build-standalone/releases/download/" + pbsRelease + "/" + strings.ReplaceAll(name, "+", "%2B")
	archive := filepath.Join(dir, "python.tar.gz")
	if err := downloadVerified(url, archive, b[1]); err != nil {
		return fmt.Errorf("téléchargement de Python : %w", err)
	}
	progress("extraction")
	if err := extractTarGz(archive, dir); err != nil {
		return fmt.Errorf("extraction de Python : %w", err)
	}
	_ = os.Remove(archive)
	progress("installation du client Modal")
	cmd := hideCmd(exec.Command(cloudRuntimePython(), "-m", "pip", "install", "--disable-pip-version-check", "--no-warn-script-location", "modal"))
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		msg := strings.TrimSpace(string(out))
		if len(msg) > 400 {
			msg = msg[len(msg)-400:]
		}
		return fmt.Errorf("installation du client Modal : %s", msg)
	}
	return os.WriteFile(filepath.Join(dir, ".ready"), []byte(pbsPython+"\n"), 0o644)
}

func downloadVerified(url, dst, sum string) error {
	resp, err := http.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %s", resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), resp.Body)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != sum {
		_ = os.Remove(dst)
		return fmt.Errorf("somme SHA-256 invalide")
	}
	return nil
}

// extractTarGz extrait l'archive dans dir, sans jamais écrire hors de dir.
func extractTarGz(src, dir string) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	root := filepath.Clean(dir) + string(os.PathSeparator)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		p := filepath.Join(dir, filepath.FromSlash(h.Name))
		if !strings.HasPrefix(p, root) {
			return fmt.Errorf("chemin hors dossier : %s", h.Name)
		}
		switch h.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode)&0o777|0o600)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		case tar.TypeSymlink:
			if runtime.GOOS == "windows" {
				continue // l'archive Windows n'en contient pas
			}
			_ = os.MkdirAll(filepath.Dir(p), 0o755)
			_ = os.Remove(p)
			if err := os.Symlink(h.Linkname, p); err != nil {
				return err
			}
		}
	}
}
