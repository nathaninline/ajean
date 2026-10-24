//go:build windows

package ajean

// backend_moe_windows.go : préparation du moteur MoE sous Windows.
//
// La release d'ajean ne publie pas de binaire Windows du moteur (le pack
// moeEngineAsset est un binaire Linux ELF) : le moteur est compilé sur place
// depuis les sources du paquet, auquel ajean a appliqué le patch remote-drop
// (moeApplyPatch — le patch voyage dans le pack Linux, qui le distribue
// appliqué). Prérequis : le toolkit CUDA (nvcc), Visual Studio Build Tools
// (cl.exe), CMake et Ninja.
//
// La détection de l'outillage est réutilisée depuis le reste d'ajean (la
// compilation Windows de llama.cpp) : findNvcc/nvccCandidates pour CUDA,
// vswhere/vcvarsPath/msvcDevEnv pour Visual Studio, et le générateur Ninja
// avec l'environnement MSVC posé via buildEnv — pas de .bat temporaire à un
// chemin fixe ni de liens durs vers des dossiers de VS/CUDA.

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// moeApplyPatch : applique le patch remote-drop aux sources du paquet (une
// fois, marqué par un fichier). Le patch est extrait du pack Linux
// (moeEngineAsset) : pas d'asset supplémentaire à publier. git est vérifié
// avant, et l'idempotence repose sur le marqueur (pas sur le texte d'une
// erreur git).
func moeApplyPatch() error {
	marker := filepath.Join(moeSrcDir(), ".ajean-patch-applied")
	if _, err := os.Stat(marker); err == nil {
		return nil
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git introuvable sur le PATH : requis pour appliquer le patch remote-drop au moteur")
	}
	zp, err := moeFetch(moeEngineAsset)
	if err != nil {
		return fmt.Errorf("pack moteur (source du patch) : %w", err)
	}
	z, err := zip.OpenReader(zp)
	if err != nil {
		return err
	}
	defer z.Close()
	var patchPath string
	for _, f := range z.File {
		if filepath.Base(f.Name) == "remote-drop.patch" {
			rc, err := f.Open()
			if err != nil {
				return err
			}
			patchPath = filepath.Join(moeHome(), "remote-drop.patch")
			if err := os.MkdirAll(moeHome(), 0o755); err != nil {
				return err
			}
			w, err := os.Create(patchPath)
			if err != nil {
				rc.Close()
				return err
			}
			if _, err := io.Copy(w, rc); err != nil {
				rc.Close()
				w.Close()
				return err
			}
			rc.Close()
			w.Close()
			break
		}
	}
	if patchPath == "" {
		return fmt.Errorf("remote-drop.patch absent du pack %s", moeEngineAsset)
	}
	// Les sources du paquet doivent être en place (le patch y est relatif).
	if _, err := os.Stat(filepath.Join(moeSrcDir(), "CMakeLists.txt")); err != nil {
		return fmt.Errorf("sources du moteur absentes (%s) : relancer l'installation", moeSrcDir())
	}
	// git apply DOIT tourner dans les sources (les chemins du patch y sont relatifs).
	git := exec.Command("git", "apply", patchPath)
	git.Dir = moeSrcDir()
	if out, err := git.CombinedOutput(); err != nil {
		return fmt.Errorf("application du patch remote-drop : %s", strings.TrimSpace(string(out)))
	}
	return os.WriteFile(marker, []byte("1"), 0o644)
}

// moeEngineDir : le dossier où setup.py attend le moteur, selon le toolkit CUDA :
// engine/ pour CUDA 13+, engine-cuda12/ pour CUDA 12 (cf. setup.py engine_dir).
func moeEngineDir() string {
	if _, major, err := moeWinCuda(); err == nil && major < 13 {
		return filepath.Join(moeSrcDir(), "engine-cuda12")
	}
	return filepath.Join(moeSrcDir(), "engine")
}

// moePrepareEngine : compile le moteur (patché) dans le dossier engine du
// toolkit CUDA en place où l'installeur attend le moteur CUDA 12/13. Le même
// build que la release Linux : portable AVX2, sm_75..120 (RTX 20 à 50).
func moePrepareEngine() error {
	eng := moeEngineDir()
	if moeEngineInstalled() == moeEngineAssetName() {
		if _, err := os.Stat(filepath.Join(eng, "strata.exe")); err == nil {
			return nil
		}
	}
	if err := moeApplyPatch(); err != nil {
		return err
	}
	cudaBin, major, err := moeWinCuda()
	if err != nil {
		return err
	}
	if _, err := msvcDevEnv(); err != nil {
		return err
	}
	for _, t := range []string{"cmake", "ninja"} {
		if !hasTool(t) {
			return fmt.Errorf("%s introuvable sur le PATH : requis pour compiler le moteur", t)
		}
	}
	jobs := cudaJobsCap(numJobs())
	bdir := filepath.Join(moeSrcDir(), "build-windows")
	lcPhase("compilation du moteur (CUDA " + strconv.Itoa(major) + ", ~15-40 min au premier passage)…")
	// Le moteur (cible strata) et l'encodeur vision (cible strata-vision, projet
	// tools/vision sur le même checkout llama.cpp) — mêmes réglages que la
	// release Linux : portable AVX2, sm_75..120 (RTX 20 à 50). L'étape tourne
	// DANS l'environnement MSVC (msvcDevEnv) + CUDA, sans .bat intermédiaire.
	if err := moeWinStep(bdir, cudaBin, "cmake",
		"-G", "Ninja", "-S", moeSrcDir(), "-B", bdir, "-DCMAKE_BUILD_TYPE=Release",
		"-DSTRATA_ENABLE_CUDA=ON", "-DSTRATA_PORTABLE=ON", "-DSTRATA_BUILD_TESTS=OFF",
		"-DCMAKE_CUDA_ARCHITECTURES=75;80;86;89;120"); err != nil {
		return fmt.Errorf("configure : %w (voir le journal)", err)
	}
	if err := moeWinStep(bdir, cudaBin, "cmake",
		"--build", bdir, "--target", "strata", "-j", strconv.Itoa(jobs)); err != nil {
		return fmt.Errorf("compilation du moteur : %w (voir le journal)", err)
	}
	// L'encodeur vision : projet tools/vision, sur le checkout llama.cpp du
	// build principal (fetch CMake).
	llama := filepath.Join(bdir, "_deps", "strata_llamacpp-src")
	if _, err := os.Stat(llama); err != nil {
		return fmt.Errorf("checkout llama.cpp introuvable (%s) : le build principal a-t-il échoué ?", llama)
	}
	vbdir := filepath.Join(moeSrcDir(), "build-windows-vision")
	if err := moeWinStep(vbdir, cudaBin, "cmake",
		"-G", "Ninja", "-S", filepath.Join(moeSrcDir(), "tools", "vision"), "-B", vbdir,
		"-DCMAKE_BUILD_TYPE=Release", "-DSTRATA_PORTABLE=ON", "-DSTRATA_VISION_CUDA=ON",
		"-DCMAKE_CUDA_ARCHITECTURES=75;80;86;89;120", "-DLLAMA_DIR="+llama); err != nil {
		return fmt.Errorf("configure vision : %w (voir le journal)", err)
	}
	if err := moeWinStep(vbdir, cudaBin, "cmake",
		"--build", vbdir, "--target", "strata-vision", "-j", strconv.Itoa(jobs)); err != nil {
		return fmt.Errorf("compilation de l'encodeur vision : %w (voir le journal)", err)
	}
	if err := os.MkdirAll(eng, 0o755); err != nil {
		return err
	}
	if err := copyFile(filepath.Join(bdir, "strata.exe"), filepath.Join(eng, "strata.exe")); err != nil {
		return fmt.Errorf("pose du moteur : %w", err)
	}
	if err := copyFile(filepath.Join(vbdir, "bin", "strata-vision.exe"), filepath.Join(eng, "strata-vision.exe")); err != nil {
		return fmt.Errorf("pose de l'encodeur vision : %w", err)
	}
	// BUILD.json : le moteur est COMPILÉ ICI (source "local"), pas un binaire de
	// release. setup.py s'en sert pour savoir s'il doit (re)compiler : on lui
	// pose src/vision_src = l'empreinte des sources POST-patch (voir moeSourceHash,
	// même algorithme que source_hash() de setup.py) pour qu'il voie un moteur
	// local à jour et ne recompile pas. ptx:true pour la compat avant (une carte
	// plus récente que sm_120 tourne en PTX) ; pas d'isa_floor : build AVX2 normal.
	srcs, err := moeSourceHash(moeSrcDir())
	if err != nil {
		return fmt.Errorf("empreinte des sources du moteur : %w", err)
	}
	bj := fmt.Sprintf(`{"version":"%s","source":"local","src":"%s","vision_src":"%s","archs":[75,80,86,89,120],"vision_archs":[75,80,86,89,120],"ptx":true,"cuda":"%d.x","toolkit":%d,"experimental":true,"vision":"gpu","portable":true}
`, moeVersion, srcs["engine"], srcs["vision"], major, major)
	if err := os.WriteFile(filepath.Join(eng, "BUILD.json"), []byte(bj), 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(moeEngineMarker(), []byte(moeEngineAssetName()+"\n"), 0o644); err != nil {
		return err
	}
	lcAppend("moteur compilé : strata.exe + strata-vision.exe (CUDA " + strconv.Itoa(major) + ", remote-drop inclus)")
	return nil
}

// moeEnsureEngine : la porte d'entrée commune (appelée par serveMoe). Le moteur
// est compilé à l'installation ; ici on ne signale son absence que si on doit
// démarrer sans lui.
func moeEnsureEngine() error {
	if moeEngineInstalled() == moeEngineAssetName() {
		return nil
	}
	eng := moeEngineDir()
	if _, err := os.Stat(filepath.Join(eng, "strata.exe")); err == nil {
		return nil
	}
	return fmt.Errorf("moteur MoE absent : installez-le d'abord depuis l'interface (compilation CUDA)")
}

// moeRunInstaller : l'installeur du moteur sous Windows. setup.py s'exécute
// DANS son venv (sys.executable) : ajean crée le .venv, y installe les
// dépendances, puis lance setup.py — qui trouve le moteur compilé dans
// engine-cuda12/ (source "prebuilt") et fait modèle + pack + vision.
func moeRunInstaller(family, quant string, env moeEnv, pre string) error {
	py, err := exec.LookPath("python")
	if err != nil {
		return fmt.Errorf("Python introuvable : installe-le (python.org, option « Add to PATH »)")
	}
	src := moeSrcDir()
	venvPy := filepath.Join(src, ".venv", "Scripts", "python.exe")
	if _, err := os.Stat(venvPy); err != nil {
		lcPhase("environnement Python du moteur…")
		if err := moeRunWinStep(src, "", "venv", py, "-m", "venv", filepath.Join(src, ".venv")); err != nil {
			return fmt.Errorf("création du venv : %w", err)
		}
		if err := moeRunWinStep(src, "", "pip", venvPy, "-m", "pip", "install", "--quiet",
			"--disable-pip-version-check", "-r", filepath.Join(src, "requirements.txt")); err != nil {
			return fmt.Errorf("dépendances Python : %w (voir le journal)", err)
		}
	}
	args := []string{
		"--setup", "--yes", "--no-start", "--no-browser",
		"--family", family, "--model", quant,
		"--context", "131072", "--vision", "gpu",
		"--data-dir", moeDataDir(),
		"--gpu", strconv.Itoa(env.Main),
	}
	if moeNvccMajor(moeWinCudaPath()) < 13 {
		args = append(args, "--cuda", "12")
	}
	// Le mode « peu de RAM » est décidé et appliqué par ajean (MOE_MMAP, voir
	// moeBuildConfig) : l'installeur tourne en mode normal, sans réserver sa
	// propre variante (qui ne voit pas un experts.bin déjà présent).
	args = append(args, "--low-ram", "off")
	return moeRunWinStep(src,
		"CUDA_DEVICE_ORDER=PCI_BUS_ID\x00PYTHONUNBUFFERED=1",
		"setup.py", venvPy, append([]string{filepath.Join(src, "setup.py")}, args...)...)
}

// moeWinCuda : le dossier bin/ du toolkit CUDA le plus récent installé (12+),
// ou nvcc sur le PATH — via la détection partagée (findNvcc/nvccCandidates).
// Renvoie (dossier, version majeure, erreur).
func moeWinCuda() (string, int, error) {
	candidates := nvccCandidates()
	if len(candidates) == 0 {
		return "", 0, fmt.Errorf("nvcc introuvable : installe le toolkit CUDA (12.x ou 13.x)")
	}
	nvcc := highestVersionedPath(candidates)
	cudaBin := filepath.Dir(nvcc)
	return cudaBin, moeNvccMajor(cudaBin), nil
}

func moeWinCudaPath() string {
	p, _, _ := moeWinCuda()
	return p
}

// moeRunWinStep : une étape (cmake, ninja, python…) dont la sortie complète va
// dans un fichier de log (moeHome()/logs/), et dont l'UI ne reçoit qu'une ligne
// de progression périodique — pas les centaines de lignes nvcc/cl (qui
// inonderaient le journal du job). Sur échec, la fin du log est remontée.
// Pas de console (ajean mode application Windows : os.Stdin/os.Stdout invalides).
// Annulable : setCurCmd + cancelCurrentBuild tue l'arbre de process.
func moeRunWinStep(cwd, extraEnv, name, bin string, args ...string) error {
	logPath := filepath.Join(moeHome(), "logs", name+".log")
	_ = os.MkdirAll(filepath.Dir(logPath), 0o755)
	logf, _ := os.Create(logPath)
	if logf != nil {
		defer logf.Close()
	}

	cmd := hideCmd(exec.Command(bin, args...))
	cmd.Dir = cwd
	// Tee : fichier de log + sinkWriter (UI). Le sinkWriter pousse chaque ligne
	// vers emitBuildLine, mais on ne le branche PAS sur la sortie brute : on
	// compte les lignes et on n'envoie à l'UI qu'une ligne de progression
	// toutes les 50 lignes (sinon 30 min de nvcc noient le journal).
	var (
		mu    sync.Mutex
		count int
	)
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	go func() {
		buf := make([]byte, 32<<10)
		var partial []byte
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				if logf != nil {
					_, _ = logf.Write(buf[:n])
				}
				partial = append(partial, buf[:n]...)
				for {
					i := strings.IndexByte(string(partial), '\n')
					if i < 0 {
						break
					}
					line := strings.TrimRight(string(partial[:i]), "\r")
					partial = partial[i+1:]
					if strings.TrimSpace(line) == "" {
						continue
					}
					mu.Lock()
					count++
					c := count
					mu.Unlock()
					// L'UI ne reçoit qu'une ligne de progression périodique
					// + les lignes d'erreur/diagnostic (qui commencent par
					// "error", "Error", "FAILED", "fatal").
					lower := strings.ToLower(line)
					if strings.Contains(lower, "error") ||
						strings.Contains(lower, "fatal") ||
						strings.Contains(lower, "failed") ||
						c%50 == 1 {
						emitBuildLine(name + ": " + line)
					}
				}
			}
			if err != nil {
				if logf != nil && len(partial) > 0 {
					_, _ = logf.Write(partial)
				}
				break
			}
		}
	}()

	if extraEnv != "" {
		env := os.Environ()
		for _, kv := range strings.Split(extraEnv, "\x00") {
			if kv == "" {
				continue
			}
			env = upsertEnv(env, kv)
		}
		cmd.Env = env
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	setCurCmd(cmd)
	err := cmd.Wait()
	setCurCmd(nil)
	pw.Close()
	pr.Close()
	if err != nil {
		// Remonter la fin du log pour que l'échec ne soit pas muet.
		if logf != nil {
			_, _ = logf.Seek(-4096, io.SeekEnd)
			tail := make([]byte, 4096)
			n, _ := logf.Read(tail)
			for _, l := range strings.Split(string(tail[:n]), "\n") {
				if strings.TrimSpace(l) != "" {
					emitBuildLine(l)
				}
			}
		}
	}
	return err
}

// moeWinStep : une commande cmake/ninja exécutée DANS l'environnement MSVC
// (msvcDevEnv → INCLUDE, LIB, PATH avec cl.exe) étendu du dossier bin du
// toolkit CUDA. Même mécanique que la compilation Windows de llama.cpp : pas de
// .bat temporaire ni de console (ajean mode application Windows). La sortie
// complète va dans un log (moeRunWinStep).
func moeWinStep(cwd, cudaBin, bin string, args ...string) error {
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		return fmt.Errorf("création du dossier de build %s : %w", cwd, err)
	}
	be := newBuildEnv()
	devEnv, err := msvcDevEnv()
	if err != nil {
		return err
	}
	for _, kv := range devEnv {
		if i := strings.IndexByte(kv, '='); i > 0 {
			be.set(kv[:i], kv[i+1:])
		}
	}
	if cudaBin != "" {
		be.prependPath(cudaBin)
		be.set("CUDACXX", filepath.Join(cudaBin, "nvcc.exe"))
	}
	return moeRunWinStep(cwd, be.serialize(), filepath.Base(bin), bin, args...)
}

// moeVenvPython : le Python de l'environnement du moteur, dans ses sources.
func moeVenvPython(srcDir string) string {
	return filepath.Join(srcDir, ".venv", "Scripts", "python.exe")
}

// moeWriteLauncher : sous Windows, pas de lanceur .bat (CreateProcess ne le
// prend pas) : ajean serve exécute directement le python de SON venv (en place,
// dans les sources — il retrouve pyvenv.cfg et ses dépendances à côté) sur
// serve/server.py. Renvoie (programme, args) — args SANS le programme (c'est
// serveMoe qui le pose en tête, comme pour llama-server).
func moeWriteLauncher(srcDir, py string) (string, []string, error) {
	if _, err := os.Stat(py); err != nil {
		return "", nil, fmt.Errorf("python du venv du moteur absent (%s) : réinstaller le modèle", py)
	}
	return py, []string{filepath.Join(srcDir, "serve", "server.py"), "--engine", "strata"}, nil
}

// moeAlias : Windows ne crée pas de lien symbolique sans privilèges : une
// copie du binaire dans moeHome()/bin/, le process tourne sous ce nom. Uniquement
// utilisé pour les binaires auto-contenus du moteur (strata.exe, strata-vision.exe,
// BUILD.json) — jamais pour le python du venv, qui lui reste en place dans ses
// sources (moeWriteLauncher) et conserve ainsi pyvenv.cfg et ses dépendances.
func moeAlias(target, name string) string {
	dir := filepath.Join(moeHome(), "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return target
	}
	dst := filepath.Join(dir, name)
	if err := copyFile(target, dst); err != nil {
		return target
	}
	return dst
}

// copyFile : copie binaire src -> dst. Sous Windows, moeAlias ne sait mettre un
// binaire en avant que par une copie (pas de lien symbolique sans privileges) :
// ce helper n'existe que pour ce chemin, d'ou sa place ici et non dans le code
// partage, ou il serait mort a la compilation Linux (staticcheck U1000).
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

var reNvccMajor = regexp.MustCompile(`release (\d+)\.\d+`)

// moeNvccMajor : la version majeure du nvcc d'un dossier toolkit (12 ou 13).
func moeNvccMajor(cudaBin string) int {
	if out, err := exec.Command(filepath.Join(cudaBin, "nvcc.exe"), "--version").CombinedOutput(); err == nil {
		if m := reNvccMajor.FindStringSubmatch(string(out)); m != nil {
			v, _ := strconv.Atoi(m[1])
			return v
		}
	}
	return 13
}
