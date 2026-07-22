// backend_build.go — machinerie de compilation de llama.cpp : détection du
// plan de build (CUDA/ROCm/Metal/Vulkan/CPU), cmake, suivi de progression, logs.
package jean

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

// buildSink est un collecteur de lignes optionnel : quand il est posé (jobs
// web, voir web_llamacpp.go), runStep/runBuildStep y dupliquent leur sortie en
// plus du terminal / des fichiers de log. nil en usage CLI normal.
var (
	buildSinkMu sync.Mutex
	buildSink   func(string)
)

func setBuildSink(f func(string)) {
	buildSinkMu.Lock()
	buildSink = f
	buildSinkMu.Unlock()
}

func emitBuildLine(line string) {
	buildSinkMu.Lock()
	f := buildSink
	buildSinkMu.Unlock()
	if f != nil {
		f(line)
	}
}

// sinkWriter découpe un flux en lignes et les pousse vers emitBuildLine.
// Sert à téer la sortie des commandes de runStep quand un sink est actif.
// (mutex : stdout et stderr d'une même commande peuvent écrire en parallèle)
type sinkWriter struct {
	mu  sync.Mutex
	buf []byte
}

func (s *sinkWriter) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buf = append(s.buf, p...)
	for {
		i := strings.IndexByte(string(s.buf), '\n')
		if i < 0 {
			break
		}
		emitBuildLine(strings.TrimRight(string(s.buf[:i]), "\r"))
		s.buf = s.buf[i+1:]
	}
	return len(p), nil
}

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
		// Générateur Visual Studio : vérifie (et répare si possible) l'intégration
		// MSBuild de CUDA AVANT de configurer — sinon CMake échoue sur le cryptique
		// « No CUDA toolset found » (cf. issue #10 : CUDA dans un chemin custom sans
		// l'option « Visual Studio Integration », ou VS installé après CUDA).
		if strings.HasPrefix(p.gen, "Visual Studio") {
			if err := ensureCudaVSIntegration(filepath.Dir(cudaBin)); err != nil {
				return err
			}
		}
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
	// Windows/CUDA : « No CUDA toolset found » = intégration MSBuild de CUDA
	// absente de Visual Studio. Normalement intercepté AVANT le configure par
	// ensureCudaVSIntegration ; ce filet sert aux cas où la détection n'a pas pu
	// conclure (vswhere absent, install VS non standard).
	if p.backend == "cuda" && strings.Contains(log, "No CUDA toolset found") {
		fmt.Printf("\n%s l'intégration Visual Studio de CUDA est absente (« No CUDA toolset found »).\n", yellow("[dépendance]"))
		fmt.Printf("            Relance l'installeur du CUDA Toolkit (installation personnalisée) en cochant « CUDA → Visual Studio Integration »\n")
		fmt.Printf("            (Visual Studio avec le workload C++ doit déjà être installé), ou copie les fichiers de\n")
		fmt.Printf("            <toolkit>\\extras\\visual_studio_integration\\MSBuildExtensions vers\n")
		fmt.Printf("            <VS>\\MSBuild\\Microsoft\\VC\\<version>\\BuildCustomizations, puis relance %s.\n", bold("jean llamacpp install"))
	}
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
	// Tee vers le sink de build (jobs web) en plus du terminal.
	var out io.Writer = os.Stdout
	var errw io.Writer = os.Stderr
	buildSinkMu.Lock()
	sinkOn := buildSink != nil
	buildSinkMu.Unlock()
	if sinkOn {
		sw := &sinkWriter{}
		out = io.MultiWriter(os.Stdout, sw)
		errw = io.MultiWriter(os.Stderr, sw)
	}
	cmd.Stdout = out
	cmd.Stderr = errw
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
			emitBuildLine(line)
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
