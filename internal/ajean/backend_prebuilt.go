// backend_prebuilt.go — backend llama.cpp SANS compilation : télécharge les
// binaires officiels précompilés publiés à chaque release de ggml-org/llama.cpp
// (zip Windows, tar.gz macOS/Linux), choisit l'asset adapté à la machine
// (CUDA / ROCm / Vulkan / CPU), l'extrait dans backends/llama.cpp-prebuilt et
// pointe BIN dessus. ~2 minutes au lieu d'une compilation complète.
//
// Limites assumées : builds génériques (pas de tuning natif), et pas de build
// CUDA officiel pour Linux (on retombe sur Vulkan — la compilation locale
// reste la voie CUDA sous Linux). La compilation locale reste indispensable
// pour les forks (ex. PrismML).
package ajean

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

const llamaReleasesAPI = "https://api.github.com/repos/ggml-org/llama.cpp/releases/latest"

type ghAsset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}

func prebuiltDir() string {
	return filepath.Join(backendsDir(), "llama.cpp-prebuilt")
}

// prebuiltVersion lit le marqueur VERSION du dossier prebuilt : "tag cudaVer"
// (cudaVer vide hors CUDA Windows).
func prebuiltVersion() (tag, cudaVer string) {
	b, err := os.ReadFile(filepath.Join(prebuiltDir(), "VERSION"))
	if err != nil {
		return "", ""
	}
	f := strings.Fields(strings.TrimSpace(string(b)))
	if len(f) > 0 {
		tag = f[0]
	}
	if len(f) > 1 {
		cudaVer = f[1]
	}
	return
}

// prebuiltFormat identifie la façon dont l'archive a été extraite. « fmt2 » =
// extraction qui recrée les liens des archives (indispensable aux .dylib macOS
// et .so Linux). Une installation sans ce marqueur est réputée incomplète et
// sera refaite au lieu d'être déclarée « déjà à jour ».
const prebuiltFormat = "fmt2"

// prebuiltVersionFormat lit le 3e champ du marqueur VERSION ("" si absent).
func prebuiltVersionFormat() string {
	b, err := os.ReadFile(filepath.Join(prebuiltDir(), "VERSION"))
	if err != nil {
		return ""
	}
	f := strings.Fields(strings.TrimSpace(string(b)))
	if len(f) > 2 {
		return f[2]
	}
	return ""
}

// prebuiltServerBin localise llama-server(.exe) sous le dossier prebuilt
// (l'arborescence interne des archives officielles varie : racine, build/bin…).
//
// Chaque release s'extrait dans son propre sous-dossier (llama-b10280/…), donc
// plusieurs versions peuvent cohabiter — notamment des installations anciennes
// et incomplètes. On retient donc EN PRIORITÉ le binaire de la version notée
// dans VERSION, et à défaut le plus récent (numéro de build le plus élevé) :
// prendre « le premier trouvé » renvoyait le plus ANCIEN dossier, donc un
// binaire périmé voire cassé.
func prebuiltServerBin() string {
	all := prebuiltServerBins()
	if len(all) == 0 {
		return ""
	}
	if tag, _ := prebuiltVersion(); tag != "" {
		for _, p := range all {
			if strings.Contains(filepath.ToSlash(p), "/llama-"+tag+"/") {
				return p
			}
		}
	}
	return all[len(all)-1] // ordre lexical = ordre des numéros de build
}

// prebuiltServerBins liste tous les llama-server présents sous le dossier
// prebuilt, triés par chemin (donc par numéro de build croissant).
func prebuiltServerBins() []string {
	want := "llama-server"
	if runtime.GOOS == "windows" {
		want += ".exe"
	}
	var found []string
	_ = filepath.WalkDir(prebuiltDir(), func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() == want {
			found = append(found, p)
		}
		return nil
	})
	sort.Strings(found)
	return found
}

// prebuiltOwns dit si p désigne un binaire du moteur précompilé, quelle que
// soit la version installée. Les presets enregistrent un chemin versionné :
// après une mise à jour ce chemin n'est plus celui du moteur courant, mais le
// preset utilise bien « le moteur précompilé » et doit être reconnu comme tel.
func prebuiltOwns(p string) bool {
	if p == "" {
		return false
	}
	norm := func(s string) string {
		return strings.ToLower(filepath.ToSlash(filepath.Clean(s)))
	}
	return strings.HasPrefix(norm(p), norm(prebuiltDir())+"/")
}

// prebuiltResolveBin fait suivre un BIN de preset aux mises à jour du moteur :
// un chemin versionné qui n'existe plus (release remplacée) est remplacé par le
// binaire précompilé courant. Tout autre chemin est renvoyé tel quel.
func prebuiltResolveBin(bin string) string {
	if !prebuiltOwns(bin) {
		return bin
	}
	if _, err := os.Stat(bin); err == nil {
		return bin
	}
	if cur := prebuiltServerBin(); cur != "" {
		return cur
	}
	return bin
}

// prebuiltPrune supprime les extractions d'autres releases : elles ne servent
// plus à rien (quelques centaines de Mo chacune) et, laissées en place, elles
// se faisaient élire comme binaire courant.
func prebuiltPrune(keep string, logf func(string)) {
	keepDir := ""
	if keep != "" {
		keepDir = filepath.ToSlash(filepath.Clean(keep))
	}
	entries, err := os.ReadDir(prebuiltDir())
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "llama-b") {
			continue
		}
		d := filepath.Join(prebuiltDir(), e.Name())
		if keepDir != "" && strings.HasPrefix(keepDir, filepath.ToSlash(filepath.Clean(d))+"/") {
			continue
		}
		if err := os.RemoveAll(d); err == nil && logf != nil {
			logf("ancienne version supprimée : " + e.Name())
		}
	}
}

// fetchLlamaLatest interroge l'API GitHub pour la dernière release officielle.
func fetchLlamaLatest() (string, []ghAsset, error) {
	req, err := http.NewRequest("GET", llamaReleasesAPI, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", nil, fmt.Errorf("GitHub API : HTTP %d", resp.StatusCode)
	}
	var rel struct {
		TagName string    `json:"tag_name"`
		Assets  []ghAsset `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&rel); err != nil {
		return "", nil, err
	}
	if rel.TagName == "" {
		return "", nil, fmt.Errorf("release invalide (tag vide)")
	}
	return rel.TagName, rel.Assets, nil
}

// driverCudaVersion renvoie la version CUDA max supportée par le pilote NVIDIA
// (bandeau de nvidia-smi : « CUDA Version: 12.8 »), ou 0 si inconnue.
func driverCudaVersion() float64 {
	out, err := hideCmd(exec.Command("nvidia-smi")).Output()
	if err != nil {
		return 0
	}
	m := regexp.MustCompile(`CUDA Version:\s*([0-9]+\.[0-9]+)`).FindSubmatch(out)
	if m == nil {
		return 0
	}
	v, _ := strconv.ParseFloat(string(m[1]), 64)
	return v
}

// assetMatch garde les assets dont le nom contient TOUS les fragments.
func assetMatch(assets []ghAsset, frags ...string) []ghAsset {
	var out []ghAsset
	for _, a := range assets {
		ok := true
		for _, f := range frags {
			if !strings.Contains(a.Name, f) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, a)
		}
	}
	return out
}

var reCudaAssetVer = regexp.MustCompile(`cuda-([0-9]+\.[0-9]+)`)

// pickPrebuilt choisit l'asset principal (+ cudart pour CUDA Windows) adapté à
// la machine, et renvoie un label lisible du variant retenu.
func pickPrebuilt(assets []ghAsset) (main *ghAsset, cudart *ghAsset, label, cudaVer string, err error) {
	pickOne := func(list []ghAsset) *ghAsset {
		if len(list) == 0 {
			return nil
		}
		return &list[0]
	}
	switch runtime.GOOS {
	case "windows":
		if runtime.GOARCH == "arm64" {
			main = pickOne(assetMatch(assets, "llama-", "bin-win-cpu-arm64"))
			label = "CPU (Windows arm64)"
			break
		}
		if hasNvidiaGPU() {
			// Plusieurs versions CUDA publiées (ex. 12.4 et 13.3) : on prend la plus
			// haute supportée par le pilote (sinon la plus basse, la plus compatible).
			// NB : les archives cudart-llama-bin-win-cuda-… contiennent aussi ces
			// fragments — on les écarte explicitement du choix du binaire principal.
			var cand []ghAsset
			for _, a := range assetMatch(assets, "llama-", "bin-win-cuda-", "-x64.zip") {
				if !strings.HasPrefix(a.Name, "cudart") {
					cand = append(cand, a)
				}
			}
			maxV := driverCudaVersion()
			var best *ghAsset
			bestV := 0.0
			for i := range cand {
				m := reCudaAssetVer.FindStringSubmatch(cand[i].Name)
				if m == nil {
					continue
				}
				v, _ := strconv.ParseFloat(m[1], 64)
				ok := maxV == 0 && (best == nil || v < bestV) || // pilote inconnu → la plus basse
					maxV > 0 && v <= maxV && v > bestV // sinon la plus haute compatible
				if ok {
					best = &cand[i]
					bestV = v
					cudaVer = m[1]
				}
			}
			if best != nil {
				main = best
				cudart = pickOne(assetMatch(assets, "cudart-", "win-cuda-"+cudaVer))
				label = "CUDA " + cudaVer + " (Windows x64)"
				break
			}
		}
		if a := pickOne(assetMatch(assets, "llama-", "bin-win-vulkan-x64")); a != nil {
			main, label = a, "Vulkan (Windows x64)"
			break
		}
		main = pickOne(assetMatch(assets, "llama-", "bin-win-cpu-x64"))
		label = "CPU (Windows x64)"
	case "darwin":
		arch := "x64"
		if runtime.GOARCH == "arm64" {
			arch = "arm64"
		}
		main = pickOne(assetMatch(assets, "llama-", "bin-macos-"+arch))
		label = "Metal (macOS " + arch + ")"
	default: // linux
		arch := "x64"
		if runtime.GOARCH == "arm64" {
			arch = "arm64"
		}
		if hasTool("hipcc") || isDir("/opt/rocm") {
			if a := pickOne(assetMatch(assets, "llama-", "bin-ubuntu-rocm-", arch)); a != nil {
				main, label = a, "ROCm (Linux "+arch+")"
				break
			}
		}
		// Pas de build CUDA officiel pour Linux : sur GPU NVIDIA le variant Vulkan
		// fonctionne via le pilote (moins optimal que le build CUDA local).
		if hasNvidiaGPU() || hasTool("vulkaninfo") {
			if a := pickOne(assetMatch(assets, "llama-", "bin-ubuntu-vulkan-"+arch)); a != nil {
				main, label = a, "Vulkan (Linux "+arch+")"
				if hasNvidiaGPU() {
					label += " — pas de build CUDA officiel Linux ; compile localement pour du CUDA natif"
				}
				break
			}
		}
		main = pickOne(assetMatch(assets, "llama-", "bin-ubuntu-"+arch+".tar.gz"))
		label = "CPU (Linux " + arch + ")"
	}
	if main == nil {
		return nil, nil, "", "", fmt.Errorf("aucun binaire précompilé adapté à cette machine dans la release officielle")
	}
	return main, cudart, label, cudaVer, nil
}

// recommendedMode dit laquelle des deux installations conseiller sur CETTE
// machine, et pourquoi. Le précompilé convient presque partout — sauf dans un
// cas qui concerne beaucoup de monde : **Linux avec une carte NVIDIA**.
// llama.cpp ne publie AUCUN binaire CUDA pour Linux (vérifié release b10299 :
// seuls Windows a bin-win-cuda-*), donc pickPrebuilt y retombe sur Vulkan, qui
// marche mais laisse une bonne part de la carte inexploitée. Conseiller le
// précompilé dans ce cas revenait à pousser vers l'option la plus lente.
//
// `backend` est celui qu'une compilation LOCALE produirait (detectBuildPlan).
// Renvoie {mode: "fast"|"opt", why: "…"} — le libellé est affiché sous la carte
// conseillée, pour que le choix soit justifié plutôt qu'imposé.
func recommendedMode(backend string) map[string]any {
	if runtime.GOOS == "linux" && backend == "cuda" {
		return map[string]any{
			"mode": "opt",
			"why":  "seule voie vers du CUDA natif : llama.cpp ne publie aucun binaire CUDA pour Linux, le précompilé ne donnerait que du Vulkan. Compte plusieurs minutes de compilation.",
		}
	}
	return map[string]any{"mode": "fast", "why": ""}
}

// prebuiltInstall télécharge et installe (ou met à jour) les binaires
// précompilés. logf reçoit chaque ligne de log ; phasef la phase courante.
// Renvoie le chemin du binaire installé.
func prebuiltInstall(logf, phasef func(string)) (string, error) {
	phasef("récupération de la dernière release llama.cpp…")
	tag, assets, err := fetchLlamaLatest()
	if err != nil {
		return "", fmt.Errorf("impossible d'interroger les releases llama.cpp : %w", err)
	}
	curTag, curCuda := prebuiltVersion()
	main, cudart, label, cudaVer, err := pickPrebuilt(assets)
	if err != nil {
		return "", err
	}
	logf(fmt.Sprintf("release %s — variant retenu : %s", tag, label))

	// Réinstaller à l'identique est inutile SAUF si l'extraction date d'une
	// version de AJEAN qui ignorait les liens des archives (backend installé mais
	// bibliothèques introuvables au lancement) : le marqueur de format force alors
	// une ré-extraction propre au lieu d'un « déjà à jour » trompeur.
	if cur := prebuiltServerBin(); curTag == tag && prebuiltVersionFormat() == prebuiltFormat && cur != "" {
		logf("déjà à jour (" + tag + ")")
		prebuiltPrune(cur, logf) // ménage des versions laissées par les installs précédentes
		return cur, nil
	}

	dir := prebuiltDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}

	// cudart (DLLs runtime CUDA, ~400 Mo) : seulement si absent ou si la version
	// CUDA du variant a changé depuis la dernière installation.
	if cudart != nil {
		haveDLL, _ := filepath.Glob(filepath.Join(dir, "**", "cudart64*.dll"))
		if len(haveDLL) == 0 {
			haveDLL, _ = filepath.Glob(filepath.Join(dir, "cudart64*.dll"))
		}
		if len(haveDLL) > 0 && curCuda == cudaVer {
			logf("cudart " + cudaVer + " déjà présent — téléchargement évité")
			cudart = nil
		}
	}

	for _, a := range []*ghAsset{main, cudart} {
		if a == nil {
			continue
		}
		phasef(fmt.Sprintf("téléchargement de %s (%d Mo)…", a.Name, a.Size/1_000_000))
		tmp := filepath.Join(dir, a.Name+".part")
		if err := downloadWithProgress(a.URL, tmp, a.Size, logf); err != nil {
			_ = os.Remove(tmp)
			return "", fmt.Errorf("téléchargement de %s : %w", a.Name, err)
		}
		phasef("extraction de " + a.Name + "…")
		if err := extractArchive(tmp, dir); err != nil {
			_ = os.Remove(tmp)
			return "", fmt.Errorf("extraction de %s : %w", a.Name, err)
		}
		_ = os.Remove(tmp)
	}

	// Le marqueur est écrit AVANT de localiser le binaire : prebuiltServerBin
	// s'en sert pour élire la version fraîchement extraite s'il en reste d'autres.
	if err := os.WriteFile(filepath.Join(dir, "VERSION"), []byte(tag+" "+cudaVer+" "+prebuiltFormat+"\n"), 0o644); err != nil {
		return "", err
	}
	bin := prebuiltServerBin()
	if bin == "" {
		return "", fmt.Errorf("archives extraites mais llama-server introuvable sous %s", dir)
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(bin, 0o755)
	}
	prebuiltPrune(bin, logf)
	logf("binaire installé : " + bin + " (release " + tag + ")")
	return bin, nil
}

// downloadWithProgress télécharge url vers dest en journalisant la progression
// par tranches de ~25 Mo.
func downloadWithProgress(url, dest string, total int64, logf func(string)) error {
	client := &http.Client{Timeout: 0}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if total <= 0 {
		total = resp.ContentLength
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 1<<20)
	var done, lastLog int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			if done-lastLog >= 25<<20 {
				lastLog = done
				if total > 0 {
					logf(fmt.Sprintf("⬇ %d / %d Mo (%d%%)", done/1_000_000, total/1_000_000, done*100/total))
				} else {
					logf(fmt.Sprintf("⬇ %d Mo", done/1_000_000))
				}
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// extractArchive extrait un .zip ou un .tar.gz dans dir, en refusant toute
// entrée qui s'échapperait du dossier (zip-slip).
func extractArchive(path, dir string) error {
	safe := func(name string) (string, error) {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if rel, err := filepath.Rel(dir, p); err != nil || strings.HasPrefix(rel, "..") {
			return "", fmt.Errorf("entrée d'archive suspecte : %s", name)
		}
		return p, nil
	}
	if strings.HasSuffix(path, ".zip") || strings.HasSuffix(path, ".zip.part") {
		zr, err := zip.OpenReader(path)
		if err != nil {
			return err
		}
		defer zr.Close()
		for _, f := range zr.File {
			p, err := safe(f.Name)
			if err != nil {
				return err
			}
			if f.FileInfo().IsDir() {
				if err := os.MkdirAll(p, 0o755); err != nil {
					return err
				}
				continue
			}
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				return err
			}
			rc, err := f.Open()
			if err != nil {
				return err
			}
			w, err := os.Create(p)
			if err != nil {
				rc.Close()
				return err
			}
			_, err = io.Copy(w, rc)
			rc.Close()
			w.Close()
			if err != nil {
				return err
			}
		}
		return nil
	}
	// tar.gz
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	// Les liens sont appliqués APRÈS coup : leur cible n'est pas forcément déjà
	// extraite au moment où on croise l'entrée.
	var links []archiveLink
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return applyLinks(links)
		}
		if err != nil {
			return err
		}
		p, err := safe(h.Name)
		if err != nil {
			return err
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
			w, err := os.Create(p)
			if err != nil {
				return err
			}
			if _, err := io.Copy(w, tr); err != nil {
				w.Close()
				return err
			}
			w.Close()
			_ = os.Chmod(p, os.FileMode(h.Mode)&0o777)
		case tar.TypeSymlink, tar.TypeLink:
			// Indispensable sur macOS : les archives llama.cpp livrent les .dylib
			// sous leur nom versionné (libllama-common.0.0.10107.dylib) PLUS un lien
			// portant le nom que cherche le binaire (libllama-common.0.dylib). Ignorer
			// ces entrées donnait un backend installé mais impossible à lancer :
			// « dyld: Library not loaded: @rpath/libllama-common.0.dylib ».
			target := h.Linkname
			if h.Typeflag == tar.TypeLink {
				// Lien dur : la cible est un chemin dans l'archive, pas un chemin relatif.
				t, err := safe(h.Linkname)
				if err != nil {
					return err
				}
				target = t
			}
			links = append(links, archiveLink{path: p, target: target})
		}
	}
}

// archiveLink : un lien (symbolique ou dur) relevé dans une archive.
type archiveLink struct{ path, target string }

// applyLinks crée les liens relevés pendant l'extraction. Sur les systèmes où la
// création de liens symboliques est refusée (Windows sans mode développeur), on
// copie le fichier cible : moins élégant, mais fonctionnel.
func applyLinks(links []archiveLink) error {
	for _, l := range links {
		_ = os.Remove(l.path)
		if err := os.MkdirAll(filepath.Dir(l.path), 0o755); err != nil {
			return err
		}
		if err := os.Symlink(l.target, l.path); err == nil {
			continue
		}
		// Repli par copie. La cible peut être relative au dossier du lien.
		src := l.target
		if !filepath.IsAbs(src) {
			src = filepath.Join(filepath.Dir(l.path), l.target)
		}
		data, err := os.ReadFile(src)
		if err != nil {
			continue // cible absente de l'archive : on n'échoue pas l'installation pour ça
		}
		if err := os.WriteFile(l.path, data, 0o755); err != nil {
			return err
		}
	}
	return nil
}
