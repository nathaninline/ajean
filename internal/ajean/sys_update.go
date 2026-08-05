package ajean

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// ajean update — met à jour le binaire depuis les releases GitHub du projet.
// Périmètre minimal : compare la version, télécharge l'asset correspondant à
// l'OS/arch courant, remplace le binaire en place, puis affiche quoi redémarrer.
// AUCUN redémarrage de service automatique (choix volontaire, plus sûr).

// Le dépôt a été renommé jean → ajean ; GitHub redirige les anciennes URL et
// l'API REST, donc les binaires déjà installés qui pointent encore sur
// « nathaninline/jean » continuent de trouver les releases.
const updateRepo = "nathaninline/ajean"

type ghRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// updateAssetNames reconstruit les noms d'asset acceptables pour la plateforme
// courante, par ordre de préférence : ajean-<os>-<arch>[.exe] puis, en repli,
// l'ancien nom jean-<os>-<arch>[.exe].
//
// Le repli n'est pas de la coquetterie : pendant la transition, les releases
// publient les DEUX noms (mêmes binaires) pour que le parc déjà installé — qui
// ne connaît que « jean-* » — continue de se mettre à jour. Ce binaire-ci sait
// lire les deux, il fonctionne donc aussi bien avec une release de transition
// qu'avec une release future qui n'aura plus que « ajean-* ».
func updateAssetNames() []string {
	suffix := runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		suffix += ".exe"
	}
	return []string{"ajean-" + suffix, "jean-" + suffix}
}

// updateAssetName renvoie le nom d'asset préféré (usage : messages à l'écran).
func updateAssetName() string { return updateAssetNames()[0] }

// pickAsset choisit dans la release le premier asset correspondant à la
// plateforme, en respectant l'ordre de préférence. Renvoie son nom, son URL et
// sa taille — le nom retourné est celui qui servira à vérifier le SHA-256, qui
// doit donc être celui réellement téléchargé, pas celui qu'on préférait.
func pickAsset(rel *ghRelease) (name, url string, size int64) {
	for _, want := range updateAssetNames() {
		for _, a := range rel.Assets {
			if a.Name == want {
				return a.Name, a.BrowserDownloadURL, a.Size
			}
		}
	}
	return "", "", 0
}

// ensureV préfixe "v" si absent, pour comparer via golang.org/x/mod/semver.
func ensureV(s string) string {
	s = strings.TrimSpace(s)
	if s != "" && !strings.HasPrefix(s, "v") {
		s = "v" + s
	}
	return s
}

func fetchLatestRelease() (*ghRelease, error) {
	req, _ := http.NewRequest("GET", "https://api.github.com/repos/"+updateRepo+"/releases/latest", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "ajean-update")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub a répondu %s", resp.Status)
	}
	var rel ghRelease
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return nil, err
	}
	return &rel, nil
}

// updateInfo décrit l'état de mise à jour (partagé CLI + UI web).
type updateInfo struct {
	Current   string `json:"current"`
	Latest    string `json:"latest"`
	Available bool   `json:"available"`
	URL       string `json:"url"`
}

// checkForUpdate interroge GitHub et compare à la version courante. Réutilisé
// par `ajean update` (CLI) et par l'endpoint web /api/update.
func checkForUpdate() (updateInfo, error) {
	info := updateInfo{Current: Version}
	rel, err := fetchLatestRelease()
	if err != nil {
		return info, err
	}
	latest := ensureV(rel.TagName)
	if !semver.IsValid(latest) {
		return info, fmt.Errorf("tag de release inattendu : %q", rel.TagName)
	}
	info.Latest = strings.TrimPrefix(latest, "v")
	info.URL = rel.HTMLURL
	info.Available = semver.Compare(latest, ensureV(Version)) > 0
	return info, nil
}

// applyUpdate télécharge et installe le binaire de la dernière release pour
// l'OS/arch courant. Renvoie la nouvelle version. Ne redémarre AUCUN service
// (voir printRestartHint / le message renvoyé à l'UI).
func applyUpdate() (string, error) {
	rel, err := fetchLatestRelease()
	if err != nil {
		return "", fmt.Errorf("impossible de contacter GitHub : %w", err)
	}
	latest := ensureV(rel.TagName)
	if !semver.IsValid(latest) {
		return "", fmt.Errorf("tag de release inattendu : %q", rel.TagName)
	}
	if semver.Compare(latest, ensureV(Version)) <= 0 {
		return Version, fmt.Errorf("déjà à jour (%s)", Version)
	}

	want, url, size := pickAsset(rel)
	if url == "" {
		return "", fmt.Errorf("aucun binaire %s dans la release %s (os/arch %s/%s)",
			strings.Join(updateAssetNames(), " ni "), latest, runtime.GOOS, runtime.GOARCH)
	}

	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	// Contrôle des droits AVANT de télécharger : sans ça, l'échec survient sur le
	// os.Create du fichier temporaire et l'utilisateur reçoit un « open
	// /usr/local/bin/.ajean-update.tmp: permission denied » incompréhensible,
	// notamment depuis le bouton de l'UI web (ajean web lancé sans sudo).
	if err := checkUpdateWritable(exe); err != nil {
		return "", err
	}
	tmp := filepath.Join(filepath.Dir(exe), ".ajean-update.tmp")

	if err := downloadTo(url, tmp); err != nil {
		os.Remove(tmp)
		if os.IsPermission(err) {
			return "", updatePermissionError(exe)
		}
		return "", fmt.Errorf("téléchargement : %w", err)
	}
	if err := verifyChecksum(rel, want, tmp); err != nil {
		os.Remove(tmp)
		return "", err
	}
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(exe); err == nil {
		mode = fi.Mode()
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if got := fileSize(tmp); size > 0 && got != size {
		os.Remove(tmp)
		return "", fmt.Errorf("taille inattendue (%d o reçus, %d attendus) — mise à jour annulée", got, size)
	}
	if err := replaceBinary(exe, tmp); err != nil {
		os.Remove(tmp)
		if os.IsPermission(err) {
			return "", updatePermissionError(exe)
		}
		return "", err
	}
	return strings.TrimPrefix(latest, "v"), nil
}

// updatePermissionError formule le seul message utile quand jean n'a pas les
// droits d'écrire son propre binaire : la commande exacte à lancer.
func updatePermissionError(exe string) error {
	if runtime.GOOS == "windows" {
		// Windows renvoie le MÊME code (5, accès refusé) pour « droits
		// insuffisants » et pour « fichier utilisé par un processus » : on nomme
		// les deux causes au lieu d'affirmer la mauvaise.
		return fmt.Errorf("impossible de remplacer %s : soit un autre AJEAN utilise ce fichier (ferme l'application et arrête le service, puis réessaie), soit les droits manquent (relance AJEAN en administrateur)", exe)
	}
	return fmt.Errorf("droits insuffisants pour remplacer %s (le binaire appartient à root) — lance la mise à jour en ligne de commande : sudo ajean update", exe)
}

// checkUpdateWritable vérifie qu'on peut écrire dans le dossier du binaire, en
// créant réellement un fichier témoin (les bits de permission seuls ne suffisent
// pas : root, groupes, ACL, montage en lecture seule…).
func checkUpdateWritable(exe string) error {
	dir := filepath.Dir(exe)
	probe, err := os.CreateTemp(dir, ".ajean-update-probe-*")
	if err != nil {
		if os.IsPermission(err) {
			return updatePermissionError(exe)
		}
		return fmt.Errorf("impossible d'écrire dans %s : %w", dir, err)
	}
	name := probe.Name()
	probe.Close()
	os.Remove(name)
	return nil
}

func cmdUpdate(args []string) error {
	checkOnly := false
	for _, a := range args {
		if a == "--check" || a == "-check" || a == "check" {
			checkOnly = true
		}
	}
	fmt.Println("recherche de la dernière version…")
	info, err := checkForUpdate()
	if err != nil {
		return fmt.Errorf("impossible de contacter GitHub : %w", err)
	}
	if !info.Available {
		fmt.Printf("jean est déjà à jour (%s).\n", Version)
		return nil
	}
	fmt.Printf("nouvelle version disponible : %s  (actuelle : %s)\n", info.Latest, Version)
	fmt.Printf("  %s\n", info.URL)
	if checkOnly {
		fmt.Println("lance 'ajean update' pour l'installer.")
		return nil
	}
	fmt.Printf("téléchargement de %s…\n", updateAssetName())
	newVer, err := applyUpdate()
	if err != nil {
		return err
	}
	fmt.Printf("✓ ajean mis à jour en %s\n", newVer)
	// Sous Windows, c'est ICI qu'on peut enfin migrer le dossier : `update` est
	// le geste délibéré de l'utilisateur, et c'est le seul que la plupart
	// feront. No-op ailleurs (Unix migre dans restartAfterUpdate, déjà en root).
	postUpdateMigrate()
	printRestartHint()
	return nil
}

func downloadTo(url, dst string) error {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "ajean-update")
	req.Header.Set("Accept", "application/octet-stream")
	resp, err := (&http.Client{Timeout: 5 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("GitHub a répondu %s", resp.Status)
	}
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	_, cErr := io.Copy(f, resp.Body)
	if closeErr := f.Close(); cErr == nil {
		cErr = closeErr
	}
	return cErr
}

// verifyChecksum vérifie le SHA-256 du binaire téléchargé contre le fichier
// SHA256SUMS publié dans la release (format `sha256sum` : "<hex>  <nom>").
// Si la release n'en publie pas (anciennes versions), on ne vérifie rien —
// le contrôle de taille reste le seul garde-fou, comme avant.
func verifyChecksum(rel *ghRelease, assetName, path string) error {
	var sumsURL string
	for _, a := range rel.Assets {
		switch a.Name {
		case "SHA256SUMS", "SHA256SUMS.txt", "checksums.txt":
			sumsURL = a.BrowserDownloadURL
		}
	}
	if sumsURL == "" {
		return nil
	}
	req, _ := http.NewRequest("GET", sumsURL, nil)
	req.Header.Set("User-Agent", "ajean-update")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return fmt.Errorf("téléchargement des sommes de contrôle : %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("sommes de contrôle : GitHub a répondu %s", resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return err
	}
	want := ""
	for _, line := range strings.Split(string(b), "\n") {
		f := strings.Fields(line)
		// sha256sum peut préfixer le nom de "*" (mode binaire).
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == assetName {
			want = strings.ToLower(f[0])
			break
		}
	}
	if want == "" {
		return fmt.Errorf("SHA256SUMS présent mais sans entrée pour %q — mise à jour annulée", assetName)
	}
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return err
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fmt.Errorf("somme SHA-256 invalide (%s reçu, %s attendu) — mise à jour annulée", got, want)
	}
	return nil
}

func fileSize(p string) int64 {
	if fi, err := os.Stat(p); err == nil {
		return fi.Size()
	}
	return -1
}

// replaceBinary échange le binaire en place. Sous Unix, rename() dans le même
// dossier est atomique et fonctionne même si l'ancien binaire tourne encore
// (l'inode ouvert reste valide). Sous Windows on ne peut pas écraser un .exe en
// cours : on renomme l'ancien en .old (supprimé au prochain lancement).
func replaceBinary(exe, tmp string) error {
	if runtime.GOOS == "windows" {
		old, err := renameAside(exe)
		if err != nil {
			return err
		}
		if err := os.Rename(tmp, exe); err != nil {
			_ = os.Rename(old, exe) // rollback
			return err
		}
		_ = os.Remove(old) // échoue si l'exe tourne encore → nettoyé au prochain run
		return nil
	}
	return os.Rename(tmp, exe)
}

// renameAside écarte un fichier en le renommant sous un nom UNIQUE.
//
// Un nom FIXE (« .old ») est un piège sous Windows : si un processus tourne
// ENCORE depuis le .old d'une mise à jour précédente, ce fichier ne peut être ni
// supprimé ni écrasé. Le renommage échoue alors avec « Accès refusé » (errno 5),
// que os.IsPermission rapporte comme un problème de droits — d'où le message
// « relance AJEAN en administrateur », un conseil FAUX : aucun privilège ne
// permet de remplacer l'image d'un exécutable en cours d'exécution. Reproduit
// puis vérifié corrigé sur banc d'essai, deux processus vivants sur les deux
// noms : avec un suffixe unique le renommage passe.
func renameAside(path string) (string, error) {
	aside := fmt.Sprintf("%s.old-%d", path, time.Now().UnixNano())
	if err := os.Rename(path, aside); err != nil {
		return "", err
	}
	return aside, nil
}

// removeOldBinaries supprime les écartements laissés par les remplacements
// précédents (nom fixe hérité ET noms uniques). Best-effort : ceux dont le
// processus tourne encore résistent, on réessaiera au prochain lancement.
func removeOldBinaries(path string) {
	_ = os.Remove(path + ".old")
	matches, _ := filepath.Glob(path + ".old-*")
	for _, m := range matches {
		_ = os.Remove(m)
	}
}

// cleanupOldBinary supprime silencieusement le .old laissé par une MAJ Windows
// précédente (le fichier n'était pas supprimable tant que l'exe tournait).
func cleanupOldBinary() {
	if runtime.GOOS != "windows" {
		return
	}
	exe, err := os.Executable()
	if err != nil {
		return
	}
	removeOldBinaries(exe)
	// L'alias herite laisse le meme reliquat quand il etait en cours d'execution
	// au moment ou on l'a remplace (voir replaceExe).
	removeOldBinaries(filepath.Join(filepath.Dir(exe), "jean.exe"))
}

// handleUpdateCheck (GET /api/update) : renvoie l'état de mise à jour pour l'UI.
func handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	info, err := checkForUpdate()
	if err != nil {
		sendJSON(w, 200, map[string]any{"current": Version, "available": false, "error": err.Error()})
		return
	}
	sendJSON(w, 200, info)
}

// handleUpdateApply (POST /api/update/apply) : télécharge et installe la dernière
// version. Sur un serveur Linux/systemd, relance ensuite AUTOMATIQUEMENT le service
// jean-link (celui qui sert l'UI, le chat et le tunnel) pour appliquer la MAJ sans
// avoir à SSH — voir restartAfterUpdate. Renvoie la version + le message de statut.
func handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	newVer, err := applyUpdate()
	if err != nil {
		// 500 : un client script peut tester le statut HTTP ; l'UI, elle, lit le JSON.
		sendJSON(w, 500, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	restarting, msg := restartAfterUpdate()
	if !restarting {
		msg = restartHintText()
	}
	sendJSON(w, 200, map[string]any{"ok": true, "version": newVer, "restart": msg, "restarting": restarting})
}

// restartAfterUpdate relance le service jean-link juste après une MAJ déclenchée
// depuis l'UI, pour éviter le SSH manuel. Conditions : Linux/systemd + jean-link
// actif (le service tourne en root → systemctl passe sans sudo). On NE touche PAS à
// jean.service (llama-server) pour ne pas recharger le modèle (long et inattendu
// depuis un bouton « mettre à jour »). Le redémarrage est DIFFÉRÉ (la réponse HTTP
// doit partir d'abord) et lancé en --no-block : systemd enregistre le job puis nous
// arrête/relance ; la clé E2E (.e2e_key) survit au restart, donc pas de ré-appairage
// et l'UI se reconnecte toute seule (boucle de reconnexion du flux). Renvoie
// (déclenché, message). Non-serveur (poste client, Windows) → (false, "").
func restartAfterUpdate() (bool, string) {
	// Windows : pas de superviseur pour nous relancer, on delegue a un
	// accompagnateur detache (voir sys_restart_windows.go). C'est aussi ce qui
	// cree la fenetre ou plus rien ne tient le dossier de donnees, donc ou la
	// migration peut aboutir.
	if runtime.GOOS == "windows" {
		return scheduleAppRestart()
	}
	if runtime.GOOS != "linux" || !linkServiceActive() {
		return false, ""
	}
	// Le seul moment où l'on peut migrer l'agencement d'une machine déjà
	// installée : on est root (jean-link tourne en root), le binaire vient
	// d'être remplacé, et le service va être relancé dans la foulée. Les
	// utilisateurs ne relancent jamais `install`, donc ne rien faire ici
	// reviendrait à ne jamais migrer un serveur Linux. Sans effet si la machine
	// est déjà en ajean ou si un chemin a été imposé à la main.
	if err := migrateLayout(updateLayoutPlan()); err != nil {
		fmt.Fprintf(os.Stderr, "[warn] migration de l'agencement non effectuée : %v\n", err)
	}
	go func() {
		time.Sleep(1500 * time.Millisecond) // laisser la réponse HTTP atteindre le client
		// --no-block : on enregistre le job puis on rend la main ; systemd exécute le
		// stop/start même si ce process (et le client systemctl) sont tués entre-temps.
		_ = exec.Command("systemctl", "--no-block", "restart", linkServiceName()).Run()
	}()
	return true, "Service " + linkServiceName() + " redémarré automatiquement — la page va se reconnecter seule (le modèle n'est pas rechargé)."
}

func restartHintText() string {
	if runtime.GOOS == "windows" {
		return "Redémarre AJEAN (quitte puis relance) pour appliquer la mise à jour."
	}
	return "Redémarre les services pour appliquer : sudo systemctl restart jean jean-link"
}

func printRestartHint() { fmt.Println(restartHintText()) }
