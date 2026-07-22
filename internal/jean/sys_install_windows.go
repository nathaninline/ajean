//go:build windows

package jean

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// On Windows there's no systemd unit, sudoers, or /usr/local/bin to populate.
// `jean install` simply provisions the data directory and a starter config; the
// service itself is managed by the PID-file supervisor in sys_service_windows.go
// (jean start / stop / status), which needs no admin rights.

const configTemplate = `# Configuration JEAN — édite-moi puis: jean restart
# jean serve lit ce fichier et lance ton binaire llama.cpp.

# Chemins (utilise des chemins Windows ; les antislashs ou slashs marchent)
BIN="C:/llama.cpp/build/bin/Release/llama-server.exe"
MODEL="C:/models/your-model.gguf"

# Serveur
PORT="8080"
HOST="127.0.0.1"

# Inference
CTX="32768"
BATCH="2048"
UBATCH="512"
NGL="999"

# Args supplémentaires passés à llama-server
EXTRA_ARGS=""
`

func cmdInstall(args []string) error {
	jeanHome := JeanHome()

	fmt.Printf("Installation (Windows)\n")
	fmt.Printf("  JEAN_HOME = %s\n", jeanHome)
	fmt.Printf("  service   = %s\n", serviceName())

	// 1. Create directories
	for _, d := range []string{jeanHome, filepath.Join(jeanHome, "configs"), filepath.Join(jeanHome, "SKILLS")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			return err
		}
	}

	// 2. Drop a config.env if none exists
	conf := filepath.Join(jeanHome, "config.env")
	if _, err := os.Stat(conf); os.IsNotExist(err) {
		if err := os.WriteFile(conf, []byte(configTemplate), 0o644); err != nil {
			return err
		}
		fmt.Printf("  %s écrit %s\n", green("✓"), conf)
	} else {
		fmt.Printf("  %s config.env déjà présent — inchangé\n", dim("•"))
	}

	// 3. Install the binary into JEAN_HOME\bin and put that dir on the user PATH,
	//    so `jean` is callable from any shell (this is the Windows analogue of the
	//    /usr/local/bin symlink the Unix installer creates).
	binDir := filepath.Join(jeanHome, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return err
	}
	onPath := false
	if dst, err := installSelf(binDir); err != nil {
		fmt.Printf("  %s copie du binaire impossible (%v) — ajoute-le au PATH à la main\n", dim("•"), err)
	} else {
		fmt.Printf("  %s binaire installé %s\n", green("✓"), dst)
		added, err := addToUserPath(binDir)
		switch {
		case err != nil:
			fmt.Printf("  %s mise à jour du PATH impossible (%v)\n", dim("•"), err)
		case added:
			fmt.Printf("  %s %s ajouté au PATH utilisateur\n", green("✓"), binDir)
			onPath = true
		default:
			fmt.Printf("  %s %s déjà dans le PATH\n", dim("•"), binDir)
			onPath = true
		}
	}

	fmt.Println()
	fmt.Printf("%s installation terminée.\n", green("[ok]"))
	fmt.Printf("\nProchaines étapes :\n")
	fmt.Printf("  1. édite la config :   %s   (renseigne BIN, MODEL)\n", bold("jean edit"))
	fmt.Printf("  2. démarre le service: %s\n", bold("jean start"))
	fmt.Printf("  3. UI web :            %s\n", bold("jean web"))
	if onPath {
		fmt.Printf("\n%s ouvre un NOUVEAU terminal pour que 'jean' soit reconnu (le PATH n'est lu qu'au démarrage du shell).\n", dim("[info]"))
	} else {
		fmt.Printf("\n%s pour exécuter 'jean' depuis n'importe où, ajoute son dossier au PATH.\n", dim("[info]"))
	}
	return nil
}

// installSelf copies the currently running executable into binDir as jean.exe
// and returns the destination path. If the running exe already lives there
// (re-install), it's a no-op.
func installSelf(binDir string) (string, error) {
	src, err := os.Executable()
	if err != nil {
		return "", err
	}
	src, _ = filepath.EvalSymlinks(src)
	dst := filepath.Join(binDir, "jean.exe")
	if strings.EqualFold(src, dst) {
		return dst, nil
	}
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	// Can't overwrite a running exe, but jean.exe in binDir isn't the one we're
	// running (src != dst here), so a plain create is fine.
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return "", err
	}
	if err := out.Close(); err != nil {
		return "", err
	}
	return dst, nil
}

// addToUserPath appends dir to the per-user PATH (HKCU\Environment) persistently,
// without admin rights and without setx's 1024-char truncation. Returns false if
// dir was already present. The change applies to newly launched shells.
func addToUserPath(dir string) (bool, error) {
	// Read the *user* PATH (not the process PATH, which is User+Machine merged),
	// edit it, and write it back, all via PowerShell's environment API which
	// handles the registry REG_EXPAND_SZ type and the WM_SETTINGCHANGE broadcast.
	ps := fmt.Sprintf(`$d=%s
$p=[Environment]::GetEnvironmentVariable('Path','User')
if (-not $p) { $p='' }
$parts=$p.Split(';') | Where-Object { $_ -ne '' }
if ($parts -contains $d) { Write-Output 'present'; exit 0 }
$new=(@($parts) + $d) -join ';'
[Environment]::SetEnvironmentVariable('Path',$new,'User')
Write-Output 'added'`, psQuote(dir))
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	outBytes, err := cmd.CombinedOutput()
	out := strings.TrimSpace(string(outBytes))
	if err != nil {
		return false, fmt.Errorf("%v: %s", err, out)
	}
	return strings.Contains(out, "added"), nil
}

// removeFromUserPath drops dir from the per-user PATH if present. Returns false
// if it wasn't there.
func removeFromUserPath(dir string) (bool, error) {
	ps := fmt.Sprintf(`$d=%s
$p=[Environment]::GetEnvironmentVariable('Path','User')
if (-not $p) { Write-Output 'absent'; exit 0 }
$parts=$p.Split(';') | Where-Object { $_ -ne '' -and $_ -ne $d }
if (($p.Split(';') | Where-Object { $_ -eq $d }).Count -eq 0) { Write-Output 'absent'; exit 0 }
[Environment]::SetEnvironmentVariable('Path',($parts -join ';'),'User')
Write-Output 'removed'`, psQuote(dir))
	cmd := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)
	outBytes, err := cmd.CombinedOutput()
	out := strings.TrimSpace(string(outBytes))
	if err != nil {
		return false, fmt.Errorf("%v: %s", err, out)
	}
	return strings.Contains(out, "removed"), nil
}

// psQuote wraps s in a PowerShell single-quoted string literal (doubling any
// embedded single quotes), safe against spaces and metacharacters in the path.
func psQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func cmdUninstall(args []string) error {
	keepData := true
	for _, a := range args {
		if a == "--purge" {
			keepData = false
		}
		if a == "--keep-data" {
			keepData = true
		}
	}
	// Stop the background server if it's running.
	_ = svcStop(false)

	// Pull JEAN_HOME\bin off the user PATH (best-effort).
	binDir := filepath.Join(JeanHome(), "bin")
	if removed, err := removeFromUserPath(binDir); err == nil && removed {
		fmt.Printf("  %s %s retiré du PATH utilisateur\n", green("✓"), binDir)
	}

	if !keepData {
		jeanHome := JeanHome()
		if err := os.RemoveAll(jeanHome); err != nil {
			return fmt.Errorf("suppression de %s: %w", jeanHome, err)
		}
		fmt.Printf("  %s %s supprimé\n", green("✓"), jeanHome)
	} else {
		fmt.Println(dim("(données utilisateur conservées — relance avec --purge pour tout supprimer)"))
	}
	fmt.Println(green("[ok]") + " désinstallé")
	return nil
}
