package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// backend_llamacpp.go — gestion du backend llama.cpp (clone, build, mise à jour).
//
// `jean llamacpp install`  installe un build neuf, détecte automatiquement
//                          l'accélérateur (CUDA / ROCm / Metal / CPU) et la
//                          compute capability du GPU, puis pointe BIN dessus.
// `jean llamacpp update`   met à jour le dépôt existant (git pull) et recompile
//                          avec la bonne config, sans intervention.
// `jean llamacpp status`   montre le commit courant, le backend détecté et le
//                          retard éventuel sur origin.

const llamacppRepoURL = "https://github.com/ggml-org/llama.cpp.git"

// buildPlan capture les flags CMake adaptés à la machine courante.
type buildPlan struct {
	backend  string   // "cuda" | "hip" | "metal" | "vulkan" | "cpu"
	cudaArch string   // ex. "120" ou "86;89" (vide => détection native par CMake)
	cudaCXX  string   // chemin de nvcc quand backend == cuda
	flags    []string // flags -D… passés à `cmake -B build`
	jobs     int      // parallélisme du build
	gen      string   // générateur CMake (-G), vide => défaut de la plateforme
	genArch  string   // architecture du générateur (-A), ex. "x64" (VS uniquement)
}

func cmdLlamacpp(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "install":
		return llamacppInstall(args)
	case "update", "upgrade":
		return llamacppUpdate(args)
	case "status", "info", "":
		return llamacppStatus(args)
	default:
		return fmt.Errorf("sous-commande inconnue: %s (install | update | status)", sub)
	}
}

// ---------------------------------------------------------------------------
// Localisation du dépôt
// ---------------------------------------------------------------------------

// llamacppRepoDir resolves the llama.cpp checkout: derived from config BIN when
// possible (so `update` targets whatever build the service actually runs),
// otherwise the default under $JEAN_HOME/backends/llama.cpp.
func llamacppRepoDir() string {
	if bin := ReadConfig()["BIN"]; bin != "" {
		if real, err := filepath.EvalSymlinks(bin); err == nil {
			bin = real
		}
		if root := findRepoRoot(bin); root != "" {
			return root
		}
	}
	return defaultRepoDir()
}

func defaultRepoDir() string {
	return filepath.Join(JeanHome(), "backends", "llama.cpp")
}

// findRepoRoot walks up from a binary path (…/build/bin/llama-server) looking
// for the llama.cpp source root (a dir holding .git or CMakeLists.txt).
func findRepoRoot(binPath string) string {
	d := filepath.Dir(binPath)
	for i := 0; i < 6; i++ {
		if isDir(filepath.Join(d, ".git")) || isFile(filepath.Join(d, "CMakeLists.txt")) {
			return d
		}
		parent := filepath.Dir(d)
		if parent == d {
			break
		}
		d = parent
	}
	return ""
}

// ---------------------------------------------------------------------------
// install
// ---------------------------------------------------------------------------

func llamacppInstall(args []string) error {
	repo := defaultRepoDir()
	ref := ""
	force := false
	noSwitch := false
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--dir="):
			repo = strings.TrimPrefix(a, "--dir=")
		case strings.HasPrefix(a, "--ref="):
			ref = strings.TrimPrefix(a, "--ref=")
		case a == "--force":
			force = true
		case a == "--no-switch":
			noSwitch = true
		default:
			return fmt.Errorf("option inconnue: %s", a)
		}
	}

	if err := requireTools("git", "cmake"); err != nil {
		return err
	}
	if err := ensureCompiler(); err != nil {
		return err
	}
	ensureAccelerator() // best-effort : installe le toolkit GPU si une carte est détectée

	// Dépôt déjà présent ? On bascule sur update plutôt que de re-cloner.
	if isDir(filepath.Join(repo, ".git")) {
		if !force {
			fmt.Printf("%s dépôt déjà présent dans %s\n", yellow("[info]"), repo)
			fmt.Printf("       → %s pour le mettre à jour, ou --force pour repartir de zéro\n", bold("jean llamacpp update"))
			return nil
		}
		fmt.Printf("%s --force : suppression de %s\n", yellow("[info]"), repo)
		if err := os.RemoveAll(repo); err != nil {
			return err
		}
	}

	if err := os.MkdirAll(filepath.Dir(repo), 0o755); err != nil {
		return err
	}

	fmt.Printf("%s clone de llama.cpp dans %s\n", cyan("▶"), repo)
	if err := runStep("git clone", "", "git", "clone", "--depth=1", llamacppRepoURL, repo); err != nil {
		return err
	}
	if ref != "" {
		// --depth=1 ne récupère que HEAD ; on approfondit pour atteindre le ref.
		_ = runStep("git fetch", repo, "git", "fetch", "--unshallow", "origin")
		if err := runStep("git checkout", repo, "git", "checkout", ref); err != nil {
			return err
		}
	}

	plan := detectBuildPlan()
	printPlan(plan, repo)

	if err := buildLlamacpp(repo, plan, true); err != nil {
		return err
	}

	bin := llamaServerBin(repo)
	if bin == "" {
		return fmt.Errorf("build terminé mais binaire introuvable sous %s", filepath.Join(repo, "build"))
	}
	fmt.Printf("\n%s binaire compilé : %s\n", green("✓"), bin)

	if noSwitch {
		fmt.Printf("%s --no-switch : config.env inchangée (BIN à régler manuellement)\n", dim("[info]"))
		return nil
	}
	if err := SetConfigKey("BIN", bin); err != nil {
		return fmt.Errorf("build ok mais échec écriture BIN dans config.env: %w", err)
	}
	fmt.Printf("%s BIN mis à jour dans %s\n", green("✓"), confPath())
	fmt.Printf("\nProchaines étapes :\n  1. renseigne MODEL : %s\n  2. démarre        : %s\n",
		bold("jean edit"), bold("jean restart"))
	return nil
}

// ---------------------------------------------------------------------------
// update
// ---------------------------------------------------------------------------

func llamacppUpdate(args []string) error {
	ref := ""
	clean := false
	noRestart := false
	force := false
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--ref="):
			ref = strings.TrimPrefix(a, "--ref=")
		case a == "--clean":
			clean = true
		case a == "--no-restart":
			noRestart = true
		case a == "--force":
			force = true
		default:
			return fmt.Errorf("option inconnue: %s", a)
		}
	}

	if err := requireTools("git", "cmake"); err != nil {
		return err
	}
	if err := ensureCompiler(); err != nil {
		return err
	}
	ensureAccelerator() // best-effort : installe le toolkit GPU si une carte est détectée

	repo := llamacppRepoDir()
	if !isDir(filepath.Join(repo, ".git")) {
		return fmt.Errorf("aucun dépôt llama.cpp trouvé (%s).\n       → lance d'abord %s", repo, bold("jean llamacpp install"))
	}
	fmt.Printf("%s dépôt : %s\n", cyan("▶"), repo)

	oldCommit := gitOutput(repo, "rev-parse", "--short", "HEAD")

	// Détermine la branche à suivre (master par défaut si HEAD détaché).
	branch := ref
	if branch == "" {
		branch = gitOutput(repo, "rev-parse", "--abbrev-ref", "HEAD")
		if branch == "" || branch == "HEAD" {
			branch = "master"
		}
	}

	if err := runStep("git fetch", repo, "git", "fetch", "origin", "--quiet"); err != nil {
		return err
	}

	// Déjà à jour ? On s'arrête (sauf --clean / --force qui forcent un rebuild).
	localRev := gitOutput(repo, "rev-parse", "HEAD")
	remoteRev := gitOutput(repo, "rev-parse", "origin/"+branch)
	if localRev != "" && localRev == remoteRev && !clean && !force && llamaServerBin(repo) != "" {
		fmt.Printf("%s déjà à jour (%s) — rien à faire\n", green("[ok]"), oldCommit)
		fmt.Printf("       (utilise %s pour forcer une recompilation)\n", dim("--force"))
		return nil
	}

	// Met à jour la source.
	if ref != "" {
		if err := runStep("git checkout", repo, "git", "checkout", ref); err != nil {
			return err
		}
	} else {
		if err := runStep("git pull --ff-only", repo, "git", "pull", "--ff-only", "origin", branch); err != nil {
			return fmt.Errorf("git pull a échoué (modifs locales ? essaie de résoudre à la main): %w", err)
		}
	}
	newCommit := gitOutput(repo, "rev-parse", "--short", "HEAD")

	// On stoppe le service : le binaire en cours d'exécution ne peut pas être
	// réécrit par l'étape de link (« Text file busy »).
	svcWasUp := serviceIsActive()
	if svcWasUp {
		fmt.Printf("%s arrêt du service %s le temps du build…\n", yellow("[info]"), serviceName())
		if err := serviceAction("stop"); err != nil {
			fmt.Printf("%s impossible d'arrêter le service (%v) — le build peut échouer si le binaire est verrouillé\n", yellow("[warn]"), err)
		}
	}

	plan := detectBuildPlan()
	printPlan(plan, repo)

	if err := buildLlamacpp(repo, plan, clean); err != nil {
		// On tente de remettre le service debout même en cas d'échec.
		if svcWasUp && !noRestart {
			_ = serviceAction("start")
		}
		return err
	}
	bin := llamaServerBin(repo)
	if bin == "" {
		return fmt.Errorf("build terminé mais binaire introuvable sous %s", filepath.Join(repo, "build"))
	}
	if err := SetConfigKey("BIN", bin); err != nil {
		return fmt.Errorf("build ok mais échec écriture BIN dans config.env: %w", err)
	}

	fmt.Printf("\n%s mis à jour : %s → %s\n", green("✓"), oldCommit, newCommit)

	if noRestart {
		fmt.Printf("%s --no-restart : pense à lancer %s\n", dim("[info]"), bold("jean restart"))
		return nil
	}
	if svcWasUp {
		fmt.Printf("%s redémarrage du service…\n", cyan("▶"))
		return serviceAction("start")
	}
	fmt.Printf("%s service non démarré auparavant — lance %s quand tu veux\n", dim("[info]"), bold("jean start"))
	return nil
}

// ---------------------------------------------------------------------------
// status
// ---------------------------------------------------------------------------

func llamacppStatus(args []string) error {
	repo := llamacppRepoDir()
	fmt.Printf("%s\n", bold("llama.cpp"))
	fmt.Printf("  dépôt    : %s\n", repo)
	if !isDir(filepath.Join(repo, ".git")) {
		fmt.Printf("  %s pas encore installé — %s\n", yellow("état"), bold("jean llamacpp install"))
		return nil
	}
	commit := gitOutput(repo, "log", "-1", "--format=%h %ci %s")
	branch := gitOutput(repo, "rev-parse", "--abbrev-ref", "HEAD")
	fmt.Printf("  branche  : %s\n", branch)
	fmt.Printf("  commit   : %s\n", commit)

	if bin := llamaServerBin(repo); bin != "" {
		fmt.Printf("  binaire  : %s\n", green(bin))
	} else {
		fmt.Printf("  binaire  : %s (pas encore compilé)\n", yellow("absent"))
	}

	// Retard sur origin (best-effort, sans fetch réseau).
	if branch != "" && branch != "HEAD" {
		if behind := gitOutput(repo, "rev-list", "--count", "HEAD..origin/"+branch); behind != "" && behind != "0" {
			fmt.Printf("  maj      : %s commit(s) de retard sur origin/%s — %s\n", yellow(behind), branch, bold("jean llamacpp update"))
		}
	}

	plan := detectBuildPlan()
	fmt.Printf("  backend  : %s\n", planLabel(plan))
	return nil
}

// ---------------------------------------------------------------------------
// Détection matérielle & build
// ---------------------------------------------------------------------------

// detectBuildPlan probes the machine and returns the CMake flags for the best
// available accelerator. Order of preference: CUDA → ROCm/HIP → Metal (macOS)
// → Vulkan → CPU.
func detectBuildPlan() buildPlan {
	p := buildPlan{backend: "cpu", jobs: numJobs()}
	// Flags communs : Release + tuning natif pour la machine de build.
	// (libcurl est activé d'office par llama.cpp ; LLAMA_CURL est déprécié.)
	p.flags = []string{
		"-DCMAKE_BUILD_TYPE=Release",
		"-DGGML_NATIVE=ON",
		// L'UI web embarquée de llama-server exige npm (ou un téléchargement
		// d'assets pré-compilés depuis HuggingFace) pour générer un service-worker
		// PWA — une dépendance lourde qui casse le build sur une machine sans node.
		// jean fournit sa propre UI, donc on la désactive : build plus rapide et
		// sans dépendance réseau/npm. BUILD_UI=OFF coupe npm ; USE_PREBUILT_UI=OFF
		// coupe le téléchargement d'assets pré-compilés depuis HuggingFace (qui
		// échoue sur un réseau restreint et fait planter l'embed). Sur un checkout
		// neuf le dist est vide → llama-server embarque une UI vide sans erreur.
		// Voir scripts/ui-assets.cmake côté llama.cpp.
		"-DLLAMA_BUILD_UI=OFF",
		"-DLLAMA_USE_PREBUILT_UI=OFF",
	}

	// Sur Windows, le générateur CMake par défaut est « NMake Makefiles », qui
	// suppose un Developer Command Prompt MSVC. On force le générateur Visual
	// Studio : il localise le toolchain MSVC tout seul via le registre, sans
	// vcvars, depuis un shell ordinaire.
	if runtime.GOOS == "windows" {
		p.gen = msvcGenerator()
		p.genArch = "x64"
		if runtime.GOARCH == "arm64" {
			p.genArch = "ARM64"
		}
	}

	if runtime.GOOS == "darwin" {
		// Metal est activé par défaut sur Apple Silicon ; on l'explicite.
		p.backend = "metal"
		p.flags = append(p.flags, "-DGGML_METAL=ON")
		return p
	}

	// CUDA : nvcc présent ET un GPU NVIDIA visible.
	if nvcc := findNvcc(); nvcc != "" && hasNvidiaGPU() {
		p.backend = "cuda"
		p.cudaCXX = nvcc
		// NB : on n'active PAS GGML_CUDA_FA_ALL_QUANTS — il compile les kernels
		// Flash-Attention pour toutes les combinaisons de quant (des centaines de
		// .cu), ce qui explose le temps de build pour un gain d'inférence marginal.
		p.flags = append(p.flags, "-DGGML_CUDA=ON", "-DGGML_CUDA_F16=ON")
		if arch := detectCudaArch(); arch != "" {
			p.cudaArch = arch
			p.flags = append(p.flags, "-DCMAKE_CUDA_ARCHITECTURES="+arch)
		}
		return p
	}

	// AMD ROCm / HIP.
	if hasTool("hipcc") || isDir("/opt/rocm") {
		p.backend = "hip"
		p.flags = append(p.flags, "-DGGML_HIP=ON")
		return p
	}

	// Vulkan (GPU générique) — utile sur Intel/AMD sans ROCm.
	if hasTool("glslc") && (isFile("/usr/lib/x86_64-linux-gnu/libvulkan.so.1") || hasTool("vulkaninfo")) {
		p.backend = "vulkan"
		p.flags = append(p.flags, "-DGGML_VULKAN=ON")
		return p
	}

	return p // CPU
}

// buildLlamacpp configures and builds the llama-server target. It handles the
// "relocated checkout" gotcha: a build/ whose CMake cache was generated under a
// different source path can't reconfigure in place, so we wipe it. `clean`
// forces a from-scratch build regardless.
func buildLlamacpp(repo string, p buildPlan, clean bool) error {
	build := filepath.Join(repo, "build")

	if clean || cacheStale(build, repo) {
		if isDir(build) {
			fmt.Printf("%s reconfiguration propre (suppression de build/)\n", dim("[info]"))
			old := build + ".old"
			_ = os.RemoveAll(old)
			if err := os.Rename(build, old); err != nil {
				_ = os.RemoveAll(build) // dernier recours
			}
		}
	}

	// nvcc doit être dans le PATH et exposé via CUDACXX pour la config CMake.
	env := ""
	if p.backend == "cuda" && p.cudaCXX != "" {
		cudaBin := filepath.Dir(p.cudaCXX)
		parts := []string{
			"CUDACXX=" + p.cudaCXX,
			"PATH=" + cudaBin + string(os.PathListSeparator) + os.Getenv("PATH"),
		}
		// L'intégration MSBuild CUDA (générateur Visual Studio) résout
		// CudaToolkitDir depuis CUDA_PATH / CUDA_PATH_Vx_y. L'installeur les pose
		// dans l'environnement persistant, mais pas dans ce process déjà lancé —
		// on les réinjecte sinon le configure échoue sur « CUDA Toolkit directory '' ».
		parts = append(parts, cudaPathEnv(filepath.Dir(cudaBin))...)
		env = strings.Join(parts, "\x00")
	}

	cfgArgs := []string{"-B", "build", "-S", "."}
	if p.gen != "" {
		cfgArgs = append(cfgArgs, "-G", p.gen)
		if p.genArch != "" {
			cfgArgs = append(cfgArgs, "-A", p.genArch)
		}
	}
	cfgArgs = append(cfgArgs, p.flags...)
	cfgLog := filepath.Join(repo, "configure.log")
	if err := runBuildStep("cmake configure", repo, env, "cmake", cfgLog, cfgArgs...); err != nil {
		hintMissingBuildDep(p, cfgLog)
		return fmt.Errorf("configuration CMake échouée: %w", err)
	}

	buildArgs := []string{"--build", "build", "--config", "Release",
		"-j", fmt.Sprintf("%d", p.jobs), "--target", "llama-server"}
	// Générateur Visual Studio : MSBuild réaffiche par défaut la ligne de commande
	// nvcc complète de chaque kernel (des pavés illisibles). On le passe en
	// verbosité minimale via les args natifs après « -- ».
	if strings.HasPrefix(p.gen, "Visual Studio") {
		buildArgs = append(buildArgs, "--", "/nologo", "/verbosity:minimal")
	}
	if err := runBuildStep("cmake build", repo, env, "cmake", filepath.Join(repo, "build.log"), buildArgs...); err != nil {
		return fmt.Errorf("compilation échouée: %w", err)
	}
	return nil
}

// hintMissingBuildDep scanne le log de configuration CMake à la recherche de
// dépendances manquantes CONNUES et affiche un indice d'installation adapté à la
// distribution, plutôt que de laisser l'utilisateur face à l'erreur CMake brute.
// Best-effort : silencieux si rien de reconnu. (Issue #6 : backend Vulkan qui
// échoue sur « Could not find ... SPIRV-Headers ».)
func hintMissingBuildDep(p buildPlan, cfgLog string) {
	data, err := os.ReadFile(cfgLog)
	if err != nil {
		return
	}
	log := string(data)
	// Backend Vulkan : les en-têtes SPIR-V (paquet SPIRV-Headers) sont requis par
	// la config CMake de ggml-vulkan, mais absents par défaut sur beaucoup de
	// distros même quand glslc/libvulkan sont là.
	if p.backend == "vulkan" && strings.Contains(log, "SPIRV-Headers") {
		fmt.Printf("\n%s dépendance manquante pour le backend %s : les en-têtes SPIR-V (paquet « SPIRV-Headers ») sont introuvables.\n",
			yellow("[dépendance]"), green("Vulkan"))
		if cmd := pkgInstallHint("spirv-headers"); cmd != "" {
			fmt.Printf("            installe-les puis relance %s : %s\n", bold("jean llamacpp install"), bold(cmd))
		} else {
			fmt.Printf("            installe le paquet de développement « SPIRV-Headers » de ta distribution, puis relance %s.\n", bold("jean llamacpp install"))
		}
	}
}

// pkgInstallHint renvoie la commande d'installation d'un paquet adaptée au
// gestionnaire de paquets présent sur la machine (best-effort ; "" si aucun
// gestionnaire connu n'est trouvé). Sert uniquement à afficher un indice — on
// n'exécute rien automatiquement.
func pkgInstallHint(pkg string) string {
	for _, m := range []struct{ bin, cmd string }{
		{"pacman", "sudo pacman -S " + pkg},
		{"apt-get", "sudo apt-get install -y " + pkg},
		{"dnf", "sudo dnf install -y " + pkg},
		{"zypper", "sudo zypper install -y " + pkg},
		{"brew", "brew install " + pkg},
	} {
		if _, err := exec.LookPath(m.bin); err == nil {
			return m.cmd
		}
	}
	return ""
}

// cacheStale reports whether build/CMakeCache.txt was generated for a different
// source directory than `repo` (the relocated-checkout case).
func cacheStale(build, repo string) bool {
	cache := filepath.Join(build, "CMakeCache.txt")
	b, err := os.ReadFile(cache)
	if err != nil {
		return false // pas de cache => configure neuf, rien à nettoyer
	}
	absRepo, _ := filepath.Abs(repo)
	for _, line := range strings.Split(string(b), "\n") {
		// CMAKE_HOME_DIRECTORY pointe vers le source dir d'origine.
		if strings.HasPrefix(line, "CMAKE_HOME_DIRECTORY:") {
			if i := strings.IndexByte(line, '='); i >= 0 {
				home := strings.TrimSpace(line[i+1:])
				return home != "" && home != absRepo
			}
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// Sondes matérielles
// ---------------------------------------------------------------------------

// findNvcc returns the path to nvcc from PATH or a /usr/local/cuda* install,
// preferring the highest version.
func findNvcc() string {
	if p, err := exec.LookPath("nvcc"); err == nil {
		return p
	}
	if runtime.GOOS == "windows" {
		// CUDA_PATH est posé par l'installeur officiel.
		if cp := os.Getenv("CUDA_PATH"); cp != "" {
			if p := filepath.Join(cp, "bin", "nvcc.exe"); isFile(p) {
				return p
			}
		}
		// Layout standard : …\NVIDIA GPU Computing Toolkit\CUDA\v12.x\bin\nvcc.exe
		for _, base := range []string{os.Getenv("ProgramFiles"), `C:\Program Files`} {
			if base == "" {
				continue
			}
			matches, _ := filepath.Glob(filepath.Join(base, "NVIDIA GPU Computing Toolkit", "CUDA", "v*", "bin", "nvcc.exe"))
			if len(matches) > 0 {
				sort.Strings(matches) // v12.2 < v12.8 → on prend le plus récent
				return matches[len(matches)-1]
			}
		}
		return ""
	}
	if p := "/usr/local/cuda/bin/nvcc"; isFile(p) {
		return p
	}
	matches, _ := filepath.Glob("/usr/local/cuda-*/bin/nvcc")
	if len(matches) > 0 {
		sort.Strings(matches) // cuda-12.2 < cuda-12.8 lexicographiquement → on prend le dernier
		return matches[len(matches)-1]
	}
	return ""
}

func hasNvidiaGPU() bool {
	if !hasTool("nvidia-smi") {
		return false
	}
	out, err := hideCmd(exec.Command("nvidia-smi", "-L")).Output()
	return err == nil && strings.Contains(string(out), "GPU")
}

// detectCudaArch queries every GPU's compute capability via nvidia-smi and
// returns them as CMake-style arch codes (e.g. "8.6" → "86"), deduped and
// joined with ';'. Empty when the driver is too old to report it (CMake then
// falls back to native detection).
func detectCudaArch() string {
	out, err := hideCmd(exec.Command("nvidia-smi", "--query-gpu=compute_cap", "--format=csv,noheader")).Output()
	if err != nil {
		return ""
	}
	seen := map[string]bool{}
	var archs []string
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		cap := strings.TrimSpace(line)
		if cap == "" || strings.Contains(strings.ToLower(cap), "not supported") {
			continue
		}
		code := strings.ReplaceAll(cap, ".", "") // "12.0" → "120"
		if code != "" && !seen[code] {
			seen[code] = true
			archs = append(archs, code)
		}
	}
	return strings.Join(archs, ";")
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func numJobs() int {
	n := runtime.NumCPU()
	if n < 1 {
		return 1
	}
	return n
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// llamaServerBin returns the path to the built llama-server binary under repo,
// probing the layouts the different CMake generators emit: the Visual Studio
// multi-config generator nests it under build/bin/Release/ and Windows adds a
// .exe suffix, whereas the Unix Makefiles generator drops it in build/bin/.
// Returns "" when no binary is found.
func llamaServerBin(repo string) string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	for _, rel := range []string{
		filepath.Join("build", "bin", "Release", "llama-server"+ext),
		filepath.Join("build", "bin", "llama-server"+ext),
		filepath.Join("build", "Release", "llama-server"+ext),
		filepath.Join("build", "llama-server"+ext),
	} {
		if p := filepath.Join(repo, rel); isFile(p) {
			return p
		}
	}
	return ""
}

func hasTool(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

func requireTools(tools ...string) error {
	missing := missingTools(tools)
	if len(missing) == 0 {
		return nil
	}

	// Tentative d'installation automatique (winget sur Windows, apt/brew/dnf sur
	// Unix). On rafraîchit ensuite le PATH du process car un installeur système
	// écrit le PATH machine sans toucher l'environnement déjà chargé.
	fmt.Printf("%s outils manquants: %s — installation automatique…\n", yellow("[info]"), strings.Join(missing, ", "))
	for _, t := range missing {
		if err := autoInstallTool(t); err != nil {
			fmt.Printf("  %s %s: %v\n", dim("•"), t, err)
		}
	}
	refreshToolPath()

	if still := missingTools(tools); len(still) > 0 {
		return fmt.Errorf("outils toujours manquants après tentative d'install: %s — installe-les à la main puis réessaie", strings.Join(still, ", "))
	}
	fmt.Printf("%s outils installés.\n", green("✓"))
	return nil
}

func missingTools(tools []string) []string {
	var missing []string
	for _, t := range tools {
		if !hasTool(t) {
			missing = append(missing, t)
		}
	}
	return missing
}

// gitOutput runs a git command in `dir` and returns trimmed stdout (or "").
func gitOutput(dir string, args ...string) string {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// runStep runs a command in `dir` streaming output live to the terminal.
func runStep(name, dir, bin string, args ...string) error {
	return runStepEnv(name, dir, "", bin, args...)
}

// runStepEnv is runStep with optional extra env vars (NUL-separated KEY=VAL
// pairs in `extraEnv`, which override existing ones).
func runStepEnv(name, dir, extraEnv, bin string, args ...string) error {
	fmt.Printf("\n%s %s %s\n", cyan("▶"), name, dim(strings.Join(args, " ")))
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
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
	return cmd.Run()
}

// runBuildStep runs a compile step while keeping the terminal clean: the full
// output goes to logPath, and the screen shows only a single self-rewriting
// progress line (spinner + compiled-file count) plus any real compiler
// diagnostics. The hundreds of per-file nvcc/cl command echoes are hidden. On
// failure the tail of the log is printed so the actual error is never lost.
func runBuildStep(name, dir, extraEnv, bin, logPath string, args ...string) error {
	fmt.Printf("\n%s %s\n", cyan("▶"), name)
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	if extraEnv != "" {
		env := os.Environ()
		for _, kv := range strings.Split(extraEnv, "\x00") {
			if kv != "" {
				env = upsertEnv(env, kv)
			}
		}
		cmd.Env = env
	}

	var logf *os.File
	if logPath != "" {
		if f, err := os.Create(logPath); err == nil {
			logf = f
			defer logf.Close()
		}
	}

	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		return err
	}

	frames := []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}
	var (
		mu    sync.Mutex
		count int
		label = "préparation…"
		fi    int
	)
	clearLine := func() {
		if colorOn {
			fmt.Print("\r\033[K")
		}
	}
	// draw redessine la ligne d'état ; appelé par une horloge pour rester animé
	// même quand un seul gros fichier compile pendant plusieurs minutes.
	draw := func() {
		if !colorOn {
			return
		}
		mu.Lock()
		fi = (fi + 1) % len(frames)
		fmt.Printf("\r\033[K  %c %s", frames[fi], label)
		mu.Unlock()
	}

	done := make(chan struct{})
	go func() {
		sc := bufio.NewScanner(pr)
		sc.Buffer(make([]byte, 1<<20), 1<<20) // les échos de commande sont énormes
		for sc.Scan() {
			line := sc.Text()
			if logf != nil {
				fmt.Fprintln(logf, line)
			}
			mu.Lock()
			if f := compiledFile(line); f != "" {
				count++
				label = fmt.Sprintf("compilation… %d fichiers  %s", count, dim("("+f+")"))
				mu.Unlock()
				continue
			}
			if p := phaseLabel(line); p != "" {
				label = p
			}
			mu.Unlock()
			// On ne fait remonter que les vraies ERREURS (les warnings MSVC/linker
			// d'un projet tiers sont du bruit ; ils restent dans le log). Les CMake
			// Error de la phase configure sont aussi affichés.
			if reBuildError.MatchString(line) || strings.HasPrefix(strings.TrimSpace(line), "CMake Error") {
				mu.Lock()
				clearLine()
				fmt.Println("  " + strings.TrimSpace(line))
				mu.Unlock()
			}
		}
		close(done)
	}()

	// Horloge d'animation, indépendante du flux de sortie.
	stop := make(chan struct{})
	tickerDone := make(chan struct{})
	go func() {
		defer close(tickerDone)
		t := time.NewTicker(120 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				draw()
			}
		}
	}()

	err := cmd.Wait()
	_ = pw.Close()
	<-done
	close(stop)
	<-tickerDone
	clearLine()
	if err == nil && count > 0 {
		fmt.Printf("  %s %d fichiers compilés\n", green("✓"), count)
	}
	if err != nil && logPath != "" {
		fmt.Printf("%s étape échouée — log complet : %s\n", yellow("[err]"), logPath)
		printLogTail(logPath, 30)
	}
	return err
}

// phaseLabel maps a non-compile output line to a short status label, or "" to
// leave the current label unchanged. Keeps the spinner informative during the
// CMake configure phase and the final link.
func phaseLabel(line string) string {
	t := strings.TrimSpace(line)
	switch {
	case strings.HasPrefix(t, "-- "):
		return "configuration… " + truncLabel(strings.TrimPrefix(t, "-- "), 50)
	case strings.Contains(t, "Linking") || strings.Contains(t, "Build files have been written"):
		return "édition de liens…"
	}
	return ""
}

func truncLabel(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n-1] + "…"
	}
	return s
}

var (
	// MSBuild (Windows) : « Compiling CUDA source file …\foo.cu… » ou nom de
	// source seul « foo.cpp » imprimé par cl.
	reCompilingCUDA = regexp.MustCompile(`Compiling .*?([\w.\-]+\.cu)\b`)
	reBareSource    = regexp.MustCompile(`^[\w.\-]+\.(c|cc|cpp|cxx|cu|cuh)$`)
	// Make / Ninja (Linux, macOS) : « [ 45%] Building CXX object …/foo.cpp.o » ou
	// « [12/345] Building CUDA object …/foo.cu.o ».
	reBuildingObj = regexp.MustCompile(`Building (?:C|CXX|CUDA|ASM)\w* object .*?/([^/]+?)\.o(?:bj)?\b`)
	// Vraies erreurs : « foo.cpp(12): error C2065 » (MSVC), « foo.cpp:12:5: error: »
	// (gcc/clang), « LINK : fatal error LNK1104 ». On exige le « : » devant le
	// mot-clé pour ne PAS matcher les flags type -D_CRT_SECURE_NO_WARNINGS dans les
	// lignes de commande. Les warnings (bruit d'un projet tiers) sont exclus.
	reBuildError = regexp.MustCompile(`(?i):\s*(fatal error|error)\b`)
)

// compiledFile returns the source filename a build line announces compiling, or
// "" if the line isn't a compile-progress marker. Handles both the MSBuild
// (Windows) and Make/Ninja (Unix) output formats.
func compiledFile(line string) string {
	t := strings.TrimSpace(line)
	if m := reCompilingCUDA.FindStringSubmatch(t); m != nil {
		return m[1]
	}
	if m := reBuildingObj.FindStringSubmatch(t); m != nil {
		return m[1]
	}
	if reBareSource.MatchString(t) {
		return t
	}
	return ""
}

// printLogTail prints the last n lines of the log file (best-effort).
func printLogTail(path string, n int) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	for _, l := range lines {
		fmt.Println("  " + dim(l))
	}
}

// upsertEnv replaces KEY=… in env if present, else appends kv (kv is "KEY=VAL").
func upsertEnv(env []string, kv string) []string {
	key := kv
	if i := strings.IndexByte(kv, '='); i >= 0 {
		key = kv[:i]
	}
	for i, e := range env {
		if strings.HasPrefix(e, key+"=") {
			env[i] = kv
			return env
		}
	}
	return append(env, kv)
}

func planLabel(p buildPlan) string {
	switch p.backend {
	case "cuda":
		arch := p.cudaArch
		if arch == "" {
			arch = "native"
		}
		return green("CUDA") + dim(" (arch="+arch+", nvcc="+p.cudaCXX+")")
	case "hip":
		return green("ROCm/HIP")
	case "metal":
		return green("Metal")
	case "vulkan":
		return green("Vulkan")
	default:
		return yellow("CPU") + dim(" (aucun accélérateur détecté)")
	}
}

func printPlan(p buildPlan, repo string) {
	fmt.Printf("\n%s configuration du build\n", bold("•"))
	fmt.Printf("  backend  : %s\n", planLabel(p))
	fmt.Printf("  jobs     : %d\n", p.jobs)
	fmt.Printf("  flags    : %s\n", dim(strings.Join(p.flags, " ")))
}
