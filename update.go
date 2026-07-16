package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

// jean update — met à jour le binaire depuis les releases GitHub du projet.
// Périmètre minimal : compare la version, télécharge l'asset correspondant à
// l'OS/arch courant, remplace le binaire en place, puis affiche quoi redémarrer.
// AUCUN redémarrage de service automatique (choix volontaire, plus sûr).

const updateRepo = "nathaninline/jean"

type ghRelease struct {
	TagName string `json:"tag_name"`
	HTMLURL string `json:"html_url"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
		Size               int64  `json:"size"`
	} `json:"assets"`
}

// updateAssetName reconstruit le nom d'asset attendu pour la plateforme courante,
// suivant la convention des releases : jean-<os>-<arch>[.exe].
func updateAssetName() string {
	n := "jean-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		n += ".exe"
	}
	return n
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
	req.Header.Set("User-Agent", "jean-update")
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

func cmdUpdate(args []string) error {
	checkOnly := false
	for _, a := range args {
		if a == "--check" || a == "-check" || a == "check" {
			checkOnly = true
		}
	}
	fmt.Println("recherche de la dernière version…")
	rel, err := fetchLatestRelease()
	if err != nil {
		return fmt.Errorf("impossible de contacter GitHub : %w", err)
	}
	latest := ensureV(rel.TagName)
	cur := ensureV(Version)
	if !semver.IsValid(latest) {
		return fmt.Errorf("tag de release inattendu : %q", rel.TagName)
	}
	if semver.Compare(latest, cur) <= 0 {
		fmt.Printf("jean est déjà à jour (%s).\n", Version)
		return nil
	}
	fmt.Printf("nouvelle version disponible : %s  (actuelle : %s)\n", strings.TrimPrefix(latest, "v"), Version)
	fmt.Printf("  %s\n", rel.HTMLURL)
	if checkOnly {
		fmt.Println("lance 'jean update' pour l'installer.")
		return nil
	}

	want := updateAssetName()
	var url string
	var size int64
	for _, a := range rel.Assets {
		if a.Name == want {
			url, size = a.BrowserDownloadURL, a.Size
			break
		}
	}
	if url == "" {
		return fmt.Errorf("aucun binaire %q dans la release %s (os/arch %s/%s)", want, latest, runtime.GOOS, runtime.GOARCH)
	}

	exe, err := os.Executable()
	if err != nil {
		return err
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	tmp := filepath.Join(dir, ".jean-update.tmp")

	fmt.Printf("téléchargement de %s (%.1f Mo)…\n", want, float64(size)/1e6)
	if err := downloadTo(url, tmp); err != nil {
		return fmt.Errorf("téléchargement : %w", err)
	}
	// Conserver les permissions du binaire existant (sinon 0755 par défaut).
	mode := os.FileMode(0o755)
	if fi, err := os.Stat(exe); err == nil {
		mode = fi.Mode()
	}
	if err := os.Chmod(tmp, mode); err != nil {
		os.Remove(tmp)
		return err
	}
	if got := fileSize(tmp); size > 0 && got != size {
		os.Remove(tmp)
		return fmt.Errorf("taille inattendue (%d o reçus, %d attendus) — mise à jour annulée", got, size)
	}

	if err := replaceBinary(exe, tmp); err != nil {
		os.Remove(tmp)
		if os.IsPermission(err) {
			return fmt.Errorf("droits insuffisants pour écrire %s — relance avec privilèges (ex : sudo jean update)", exe)
		}
		return err
	}
	fmt.Printf("✓ jean mis à jour en %s\n", strings.TrimPrefix(latest, "v"))
	printRestartHint()
	return nil
}

func downloadTo(url, dst string) error {
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "jean-update")
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
		old := exe + ".old"
		_ = os.Remove(old) // nettoyage d'une éventuelle MAJ précédente
		if err := os.Rename(exe, old); err != nil {
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

// cleanupOldBinary supprime silencieusement le .old laissé par une MAJ Windows
// précédente (le fichier n'était pas supprimable tant que l'exe tournait).
func cleanupOldBinary() {
	if runtime.GOOS != "windows" {
		return
	}
	if exe, err := os.Executable(); err == nil {
		_ = os.Remove(exe + ".old")
	}
}

func printRestartHint() {
	if runtime.GOOS == "windows" {
		fmt.Println("redémarre les process jean en cours (jean web, etc.) pour appliquer la mise à jour.")
		return
	}
	fmt.Println("pense à redémarrer les services : sudo systemctl restart jean jean-link  (+ relancer 'jean web' si utilisé).")
}
