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
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/mod/semver"
)

// ajean update — met à jour le binaire depuis les releases GitHub du projet.
// Périmètre minimal : compare la version, télécharge l'asset correspondant à
// l'OS/arch courant, remplace le binaire en place, puis affiche quoi redémarrer.
// AUCUN redémarrage de service automatique (choix volontaire, plus sûr).

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

// updateAssetName est le nom de l'asset de release pour la plateforme courante :
// ajean-linux, ajean-linux-arm, ajean-macos, ajean-macos-arm, ajean-windows.exe,
// ajean-windows-arm.exe. Un seul nom publié, un seul nom cherché.
//
// Ces noms sont volontairement lisibles — « macos » plutôt que « darwin », pas
// de « amd64 » pour le cas courant — et volontairement DIFFÉRENTS du schéma
// ajean-<GOOS>-<GOARCH> qu'utilisaient les versions 0.7 : leur mise à jour
// automatique ne trouve donc aucun asset et échoue sans rien remplacer, au lieu
// d'installer un binaire 0.8 sur une machine encore agencée en 0.7 — ce qui
// laissait le service de lien en boucle d'échec. La 0.7 se met à jour à la main,
// par réinstallation.
func updateAssetName() string { return assetNameFor(runtime.GOOS, runtime.GOARCH) }

// assetNameFor est séparée pour être vérifiable sur les six plateformes, et pas
// seulement sur celle qui exécute les tests.
func assetNameFor(goos, goarch string) string {
	name := "ajean-" + map[string]string{
		"darwin":  "macos",
		"linux":   "linux",
		"windows": "windows",
	}[goos]
	if goarch == "arm64" {
		name += "-arm"
	}
	if goos == "windows" {
		name += ".exe"
	}
	return name
}

// pickAsset trouve dans la release l'asset de cette plateforme. Renvoie son nom
// (celui qui servira à vérifier le SHA-256), son URL et sa taille.
func pickAsset(rel *ghRelease) (name, url string, size int64) {
	want := updateAssetName()
	for _, a := range rel.Assets {
		if a.Name == want {
			return a.Name, a.BrowserDownloadURL, a.Size
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

// Cache du check de MAJ : l'UI appelle /api/update à CHAQUE chargement de page
// (bandeau + vérification serveur), et l'API GitHub anonyme est limitée à ~60
// requêtes/h → sans cache, quelques rechargements suffisaient à déclencher
// « GitHub a répondu 403 Forbidden » et à casser la vérification de MAJ.
var (
	updCacheMu   sync.Mutex
	updCacheAt   time.Time
	updCacheInfo updateInfo
	updCacheOK   bool
)

const updCacheTTL = 30 * time.Minute

// checkForUpdate interroge GitHub (au plus une fois par updCacheTTL) et compare à
// la version courante. Réutilisé par `ajean update` (CLI) et par l'endpoint web
// /api/update. Un résultat frais en cache est renvoyé sans appel réseau ; en cas
// d'erreur (réseau/quota), on sert le dernier résultat connu plutôt qu'une erreur
// visible à l'écran.
func checkForUpdate() (updateInfo, error) {
	updCacheMu.Lock()
	if updCacheOK && time.Since(updCacheAt) < updCacheTTL {
		info := updCacheInfo
		info.Current = Version
		updCacheMu.Unlock()
		return info, nil
	}
	updCacheMu.Unlock()

	info := updateInfo{Current: Version}
	rel, err := fetchLatestRelease()
	if err != nil {
		updCacheMu.Lock()
		defer updCacheMu.Unlock()
		if updCacheOK { // quota/réseau KO → dernier résultat connu, sans erreur
			cached := updCacheInfo
			cached.Current = Version
			return cached, nil
		}
		return info, err
	}
	latest := ensureV(rel.TagName)
	if !semver.IsValid(latest) {
		return info, fmt.Errorf("tag de release inattendu : %q", rel.TagName)
	}
	info.Latest = strings.TrimPrefix(latest, "v")
	info.URL = rel.HTMLURL
	info.Available = semver.Compare(latest, ensureV(Version)) > 0
	updCacheMu.Lock()
	updCacheInfo = info
	updCacheAt = time.Now()
	updCacheOK = true
	updCacheMu.Unlock()
	return info, nil
}

// applyUpdate télécharge et installe le binaire de la dernière release pour
// l'OS/arch courant. Renvoie la nouvelle version. Ne redémarre AUCUN service
// (voir printRestartHint / le message renvoyé à l'UI).
// updateMu empêche deux mises à jour simultanées (double clic, UI locale ET
// app.ajean.link, bouton pendant un `ajean update`…). Elles écrivaient dans le
// MÊME fichier temporaire : le binaire obtenu mélangeait les deux téléchargements
// et la vérification échouait sur « somme SHA-256 invalide ».
var updateMu sync.Mutex

func applyUpdate() (string, error) {
	if !updateMu.TryLock() {
		return "", fmt.Errorf("une mise à jour est déjà en cours, patiente quelques instants")
	}
	defer updateMu.Unlock()
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
			updateAssetName(), latest, runtime.GOOS, runtime.GOARCH)
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
	// Nom de fichier temporaire UNIQUE (même dossier que le binaire, pour que le
	// remplacement final reste un simple rename) : un autre process ajean (CLI
	// pendant que le service tourne) ne peut plus écrire dans le même fichier.
	tf, err := os.CreateTemp(filepath.Dir(exe), ".ajean-update-*.tmp")
	if err != nil {
		if os.IsPermission(err) {
			return "", updatePermissionError(exe)
		}
		return "", err
	}
	tmp := tf.Name()
	tf.Close()

	// Deux essais : un téléchargement abîmé en route (proxy, coupure, antivirus
	// qui intercepte) se rattrape souvent en recommençant, plutôt que de laisser
	// l'utilisateur face à une erreur de somme de contrôle.
	for attempt := 1; ; attempt++ {
		if err := downloadTo(url, tmp); err != nil {
			os.Remove(tmp)
			if os.IsPermission(err) {
				return "", updatePermissionError(exe)
			}
			return "", fmt.Errorf("téléchargement : %w", err)
		}
		err := verifyChecksum(rel, want, tmp)
		if err == nil {
			break
		}
		if attempt >= 2 {
			os.Remove(tmp)
			return "", err
		}
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

// updatePermissionError formule le seul message utile quand ajean n'a pas les
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
		fmt.Printf("ajean est déjà à jour (%s).\n", Version)
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
// asideSeq départage deux écartements rapprochés. UnixNano seul ne suffit PAS :
// la granularité de l'horloge Windows peut rendre la même valeur à deux appels
// consécutifs, et les deux écartements se disputaient alors le même nom — le
// second renommage écrasant le premier, donc le binaire précédent perdu.
var asideSeq atomic.Uint64

func renameAside(path string) (string, error) {
	aside := fmt.Sprintf("%s.old-%d-%d", path, time.Now().UnixNano(), asideSeq.Add(1))
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
	exe, err := os.Executable()
	if err != nil {
		return
	}
	// Téléchargements de MAJ abandonnés (process tué en route) : ~16 Mo chacun.
	// Plus d'une heure = forcément orphelin, jamais une MAJ en cours ailleurs.
	if left, _ := filepath.Glob(filepath.Join(filepath.Dir(exe), ".ajean-update*.tmp")); len(left) > 0 {
		for _, f := range left {
			if fi, err := os.Stat(f); err == nil && time.Since(fi.ModTime()) > time.Hour {
				_ = os.Remove(f)
			}
		}
	}
	if runtime.GOOS != "windows" {
		return
	}
	removeOldBinaries(exe)
	// L'alias herite laisse le meme reliquat quand il etait en cours d'execution
	// au moment ou on l'a remplace (voir replaceExe).
	removeOldBinaries(filepath.Join(filepath.Dir(exe), "ajean.exe"))
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
// d'UI (ajean-ui : il sert l'UI, le chat et le tunnel) pour appliquer la MAJ sans
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

// restartAfterUpdate relance le service d'UI (uiServiceName, « ajean-ui » : il sert
// l'interface, le chat et le tunnel) juste après une MAJ déclenchée depuis l'UI, pour
// éviter le SSH manuel. Conditions : Linux/systemd + service actif. On NE touche PAS à
// ajean-engine (llama-server) pour ne pas recharger le modèle (long et inattendu depuis
// un bouton « mettre à jour »). Le redémarrage est DIFFÉRÉ (la réponse HTTP doit partir
// d'abord) et lancé en --no-block : systemd enregistre le job puis nous arrête/relance ;
// la clé E2E (.e2e_key) survit au restart, donc pas de ré-appairage et l'UI se reconnecte
// toute seule (boucle de reconnexion du flux). Renvoie (déclenché, message). Non-serveur
// (poste client, Windows) → (false, "").
func restartAfterUpdate() (bool, string) {
	// Windows : pas de superviseur pour nous relancer, on delegue a un
	// accompagnateur detache (voir sys_restart_windows.go). C'est aussi ce qui
	// cree la fenetre ou plus rien ne tient le dossier de donnees, donc ou la
	// migration peut aboutir.
	if runtime.GOOS == "windows" {
		return scheduleAppRestart()
	}
	if runtime.GOOS != "linux" || !uiServiceActive() {
		return false, ""
	}
	go func() {
		time.Sleep(1500 * time.Millisecond) // laisser la réponse HTTP atteindre le client
		// Le service tourne souvent en User=nathan (pas root) : systemctl brut
		// échouerait alors (polkit). On repli sur « sudo -n systemctl » comme
		// uiServiceCtl, les sudoers /etc/sudoers.d/ajean-ui autorisant le restart.
		//
		// SURTOUT PAS de --no-block : la règle sudoers (« systemctl restart <unit> »)
		// matche les arguments À L'IDENTIQUE, donc « --no-block restart <unit> » n'est
		// PAS couvert → sudo exige un mot de passe → échec silencieux, le service ne
		// redémarrait jamais et l'utilisateur restait sur l'ancienne version tout en
		// voyant « installé ✓ ». Le restart bloquant est fiable : systemd tue tout le
		// cgroup (ce process ET le client systemctl) d'un coup, le job de restart est
		// déjà enregistré côté PID 1 et aboutit. C'est la MÊME invocation que le switch
		// de preset et « ajean ui restart », qui eux marchaient déjà.
		bin, args := "systemctl", []string{"restart", uiServiceName()}
		if os.Geteuid() != 0 {
			bin, args = "sudo", append([]string{"-n", "systemctl"}, args...)
		}
		_ = exec.Command(bin, args...).Run()
	}()
	return true, "Service " + uiServiceName() + " redémarré automatiquement — la page va se reconnecter seule (le modèle n'est pas rechargé)."
}

func restartHintText() string {
	if runtime.GOOS == "windows" {
		return "Redémarre AJEAN (quitte puis relance) pour appliquer la mise à jour."
	}
	return "Redémarre pour appliquer : sudo systemctl restart " + uiServiceName() +
		" (ajoute " + serviceName() + " si la mise à jour touche le moteur)."
}

func printRestartHint() { fmt.Println(restartHintText()) }
