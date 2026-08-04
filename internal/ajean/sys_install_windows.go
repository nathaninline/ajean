//go:build windows

package ajean

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// On Windows there's no systemd unit, sudoers, or /usr/local/bin to populate.
// `ajean install` simply provisions the data directory and a starter config; the
// service itself is managed by the PID-file supervisor in sys_service_windows.go
// (ajean start / stop / status), which needs no admin rights.

const configTemplate = `# Configuration JEAN — édite-moi puis: ajean restart
# ajean serve lit ce fichier et lance ton binaire llama.cpp.

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
	// Lancé en administrateur, `install` est le moment où la migration peut
	// enfin aboutir sur un poste où l'utilisateur courant n'a pas les droits sur
	// %ProgramData%. On la retente donc AVANT de résoudre le dossier : l'inverse
	// laisserait la variable pointer sur l'ancien chemin et installerait à côté.
	if retryHomeMigration() {
		fmt.Printf("%s dossier de données migré vers %s\n", green("✓"), AjeanHome())
	} else if notice := homeMigrationNotice(); notice != "" {
		fmt.Printf("%s %s\n", yellow("[info]"), notice)
	}
	ajeanHome := AjeanHome()

	fmt.Printf("Installation (Windows)\n")
	fmt.Printf("  AJEAN_HOME = %s\n", ajeanHome)
	fmt.Printf("  service   = %s\n", serviceName())

	// 1-2. Dossiers + config.env de départ (partagé avec le premier lancement de
	// l'app, voir provisionDataDir dans sys_firstrun_windows.go).
	conf := filepath.Join(ajeanHome, "config.env")
	_, statErr := os.Stat(conf)
	alreadyThere := statErr == nil
	if err := provisionDataDir(); err != nil {
		return err
	}
	if alreadyThere {
		fmt.Printf("  %s config.env déjà présent — inchangé\n", dim("•"))
	} else {
		fmt.Printf("  %s écrit %s\n", green("✓"), conf)
	}

	// 3. Install the binary into JEAN_HOME\bin and put that dir on the user PATH,
	//    so `ajean` is callable from any shell (this is the Windows analogue of the
	//    /usr/local/bin symlink the Unix installer creates).
	binDir := filepath.Join(ajeanHome, "bin")
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
	fmt.Printf("  1. édite la config :   %s   (renseigne BIN, MODEL)\n", bold("ajean edit"))
	fmt.Printf("  2. démarre le service: %s\n", bold("ajean start"))
	fmt.Printf("  3. UI web :            %s\n", bold("ajean web"))
	if onPath {
		fmt.Printf("\n%s ouvre un NOUVEAU terminal pour que 'jean' soit reconnu (le PATH n'est lu qu'au démarrage du shell).\n", dim("[info]"))
	} else {
		fmt.Printf("\n%s pour exécuter 'jean' depuis n'importe où, ajoute son dossier au PATH.\n", dim("[info]"))
	}
	return nil
}

// installSelf copies the currently running executable into binDir as ajean.exe
// and returns the destination path. If the running exe already lives there
// (re-install), it's a no-op. Une copie « jean.exe » est posée à côté pour que
// l'ancienne commande reste tapable (voir installLegacyAlias).
//
// binDir est créé ici, et pas seulement par l'appelant : sur une machine vierge,
// le premier lancement passait par appFirstRun, qui ne créait que le dossier de
// données et pas son sous-dossier bin. L'utilisateur recevait alors « open
// C:\ProgramData\ajean\bin\ajean.exe: The system cannot find the path specified »
// et AJEAN démarrait depuis le fichier téléchargé, sans jamais s'installer.
func installSelf(binDir string) (string, error) {
	src, err := os.Executable()
	if err != nil {
		return "", err
	}
	src, _ = filepath.EvalSymlinks(src)
	dst := filepath.Join(binDir, "ajean.exe")
	if strings.EqualFold(src, dst) {
		installLegacyAlias(binDir, src)
		return dst, nil
	}
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		return "", err
	}
	// replaceExe et non copyExe : ajean.exe peut etre en cours d'execution
	// (service en tache de fond), auquel cas Windows refuse de l'ecraser.
	if err := replaceExe(src, dst); err != nil {
		return "", err
	}
	installLegacyAlias(binDir, dst)
	return dst, nil
}

// installLegacyAlias pose une copie « jean.exe » à côté de « ajean.exe ».
//
// Une copie et pas un lien : créer un lien symbolique sous Windows demande des
// droits particuliers qu'on n'a pas toujours, et un binaire de quelques dizaines
// de Mo dupliqué est un prix dérisoire pour ne casser ni les raccourcis du menu
// Démarrer, ni les tâches planifiées, ni les scripts que les utilisateurs ont
// écrits eux-mêmes. Best-effort : un échec ne compromet pas l'installation,
// « ajean » reste disponible.
func installLegacyAlias(binDir, src string) {
	alias := filepath.Join(binDir, "jean.exe")
	if strings.EqualFold(src, alias) {
		return
	}
	_ = replaceExe(src, alias)
}

// replaceExe copie src vers dst, y compris quand dst est un exécutable EN COURS.
//
// Windows refuse d'écraser un .exe en cours d'exécution, mais accepte de le
// RENOMMER : on décale l'ancien puis on écrit le nouveau à sa place. Sans ça,
// l'alias « jean.exe » restait figé sur une version périmée dès qu'il tournait
// au moment de la mise à jour — et comme les raccourcis existants le visent
// encore, chaque lancement relançait l'ancienne version, qui constatait qu'une
// plus récente était installée et le disait. À chaque fois. Constaté en usage.
func replaceExe(src, dst string) error {
	if err := copyExe(src, dst); err == nil {
		return nil
	}
	old := dst + ".old"
	_ = os.Remove(old) // reliquat d'un remplacement précédent
	if err := os.Rename(dst, old); err != nil {
		return err // ni écrasable ni renommable : on laisse la place en l'état
	}
	if err := copyExe(src, dst); err != nil {
		_ = os.Rename(old, dst) // rien ne doit disparaître
		return err
	}
	_ = os.Remove(old) // échoue tant que l'ancien tourne ; nettoyé plus tard
	return nil
}

func copyExe(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
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
	cmd := hideCmd(exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps))
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
	cmd := hideCmd(exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps))
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
	binDir := filepath.Join(AjeanHome(), "bin")
	if removed, err := removeFromUserPath(binDir); err == nil && removed {
		fmt.Printf("  %s %s retiré du PATH utilisateur\n", green("✓"), binDir)
	}

	// Raccourcis posés à l'installation (menu Démarrer + Bureau).
	if removeShortcuts() {
		fmt.Printf("  %s raccourcis « AJEAN » supprimés\n", green("✓"))
	}

	if !keepData {
		ajeanHome := AjeanHome()
		if err := os.RemoveAll(ajeanHome); err != nil {
			return fmt.Errorf("suppression de %s: %w", ajeanHome, err)
		}
		fmt.Printf("  %s %s supprimé\n", green("✓"), ajeanHome)
	} else {
		fmt.Println(dim("(données utilisateur conservées — relance avec --purge pour tout supprimer)"))
	}
	fmt.Println(green("[ok]") + " désinstallé")
	return nil
}
