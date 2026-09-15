// backend_build.go — machinerie de compilation de llama.cpp : détection du
// plan de build (CUDA/ROCm/Metal/Vulkan/CPU), cmake, suivi de progression, logs.
package ajean

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
	"strconv"
	"strings"
	"sync"
	"time"
)

// buildSink est un collecteur de lignes optionnel : quand il est posé (jobs
// web, voir web_llamacpp.go), runStep/runBuildStep y dupliquent leur sortie en
// plus du terminal / des fichiers de log. nil en usage CLI normal.
// buildPhase, lui, pose la PHASE de haut niveau affichée dans l'UI (le grand
// texte à côté du spinner), pour les étapes hors flux de log (ex. installation
// d'un outil via winget) qui, sinon, laissaient l'UI figée sur la phase
// précédente pendant de longues secondes sans rien indiquer.
var (
	buildSinkMu sync.Mutex
	buildSink   func(string)
	buildPhase  func(string)
)

func setBuildSink(f func(string)) {
	buildSinkMu.Lock()
	buildSink = f
	buildSinkMu.Unlock()
}

func setBuildPhase(f func(string)) {
	buildSinkMu.Lock()
	buildPhase = f
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

// Annulation d'un build en cours (bouton « arrêter » de l'UI). Un seul job tourne
// à la fois (garde lcCur), donc un unique pointeur de commande courante suffit :
// runStepEnv/runBuildStep l'enregistrent le temps de leur exécution, et
// cancelCurrentBuild tue l'arbre de process (cmake → ninja → cl/nvcc).
var (
	curCmdMu      sync.Mutex
	curCmd        *exec.Cmd
	buildCanceled bool
)

func setCurCmd(c *exec.Cmd) {
	curCmdMu.Lock()
	curCmd = c
	curCmdMu.Unlock()
}

// resetBuildCancel remet le drapeau à zéro au démarrage d'un nouveau job.
func resetBuildCancel() {
	curCmdMu.Lock()
	buildCanceled = false
	curCmdMu.Unlock()
}

// buildWasCanceled indique si l'échec de l'étape courante vient d'une annulation
// (pour afficher « compilation annulée » plutôt qu'une erreur brute).
func buildWasCanceled() bool {
	curCmdMu.Lock()
	defer curCmdMu.Unlock()
	return buildCanceled
}

// cancelCurrentBuild tue l'arbre de process de l'étape en cours. Renvoie true si
// une étape tournait réellement.
func cancelCurrentBuild() bool {
	curCmdMu.Lock()
	c := curCmd
	if c != nil && c.Process != nil {
		buildCanceled = true
	}
	curCmdMu.Unlock()
	if c == nil || c.Process == nil {
		return false
	}
	killTree(c.Process.Pid)
	return true
}

// emitBuildPhase met à jour la phase de haut niveau de l'UI (no-op en CLI, où
// buildPhase est nil et où fmt.Printf sert déjà d'indicateur).
func emitBuildPhase(phase string) {
	buildSinkMu.Lock()
	f := buildPhase
	buildSinkMu.Unlock()
	if f != nil {
		f(phase)
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

// buildBackends liste les backends d'accélération qu'on sait forcer via
// `--backend` (issue #28 : sur une machine où HIP est détecté mais cassé,
// pouvoir imposer Vulkan ou CPU sans bricoler le PATH).
var buildBackends = []string{"cuda", "hip", "vulkan", "cpu"}

func isKnownBuildBackend(b string) bool {
	for _, k := range buildBackends {
		if k == b {
			return true
		}
	}
	return b == "metal"
}

func detectBuildPlan() buildPlan { return buildPlanFor("") }

// buildTools liste les outils requis pour compiler llama.cpp sur la plateforme
// courante. Windows compile avec le générateur Ninja (voir buildPlanFor) → ninja
// est indispensable (auto-installé via winget par requireTools). Unix utilise le
// générateur Makefiles → make, fourni par ensureCompiler.
func buildTools() []string {
	if runtime.GOOS == "windows" {
		return []string{"git", "cmake", "ninja"}
	}
	return []string{"git", "cmake"}
}

// buildEnv accumule des variables d'environnement de build en préservant l'ordre
// d'insertion, avec une gestion de PATH insensible à la casse (« Path » sous
// Windows, « PATH » ailleurs). Sans ça, mélanger l'environnement MSVC de vcvars
// (« Path=… ») et un ajout « PATH=… » produirait DEUX entrées de chemin que le
// process enfant départagerait de façon indéfinie — cl.exe/nvcc parfois
// introuvables. La clé canonique fusionne les deux ; le nom d'affichage conservé
// est celui vu en premier (donc « Path » de vcvars sous Windows).
type buildEnv struct {
	order []string          // noms d'affichage, dans l'ordre d'insertion
	val   map[string]string // clé canonique -> valeur
	seen  map[string]bool   // clé canonique déjà vue
}

func newBuildEnv() *buildEnv {
	return &buildEnv{val: map[string]string{}, seen: map[string]bool{}}
}

func envKeyCanon(k string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(k)
	}
	return k
}

func (b *buildEnv) set(name, v string) {
	c := envKeyCanon(name)
	if !b.seen[c] {
		b.seen[c] = true
		b.order = append(b.order, name)
	}
	b.val[c] = v
}

// prependPath met dir en tête de PATH, en repartant du PATH courant du process
// s'il n'a pas encore été posé.
func (b *buildEnv) prependPath(dir string) {
	name := "PATH"
	if runtime.GOOS == "windows" {
		name = "Path"
	}
	cur, ok := b.val[envKeyCanon(name)]
	if !ok {
		cur = os.Getenv("PATH")
	}
	if cur != "" {
		b.set(name, dir+string(os.PathListSeparator)+cur)
	} else {
		b.set(name, dir)
	}
}

// serialize rend l'environnement au format attendu par runBuildStep : des
// paires « NOM=VALEUR » séparées par des NUL. "" si rien n'a été posé.
func (b *buildEnv) serialize() string {
	if len(b.order) == 0 {
		return ""
	}
	parts := make([]string, 0, len(b.order))
	for _, name := range b.order {
		parts = append(parts, name+"="+b.val[envKeyCanon(name)])
	}
	return strings.Join(parts, "\x00")
}

// reVerNum capture le premier numéro de version pointé d'un chemin
// (« …/v13.4/bin » → « 13.4 », « /usr/local/cuda-12.8/lib64 » → « 12.8 »).
var reVerNum = regexp.MustCompile(`\d+(?:\.\d+)+`)

func pathVersion(s string) string { return reVerNum.FindString(filepath.ToSlash(s)) }

// cmpVersion compare deux versions pointées NUMÉRIQUEMENT (« 12.10 » > « 12.4 »,
// « 13.4 » > « 9.0 ») là où un tri de chaînes se tromperait. Renvoie -1, 0 ou 1.
// Une chaîne vide est considérée plus petite que toute version.
func cmpVersion(a, b string) int {
	if a == b {
		return 0
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// highestVersionedPath renvoie, parmi des chemins porteurs d'un numéro de version,
// celui dont la version est la plus élevée (tri sémantique). Les chemins sans
// version détectable ne l'emportent jamais sur un chemin versionné. "" si la
// liste est vide.
func highestVersionedPath(paths []string) string {
	best, bestVer := "", ""
	for _, p := range paths {
		v := pathVersion(p)
		if best == "" || (v != "" && cmpVersion(v, bestVer) > 0) {
			best, bestVer = p, v
		}
	}
	return best
}

// sortByVersionDesc trie des chemins par version décroissante (sémantique).
func sortByVersionDesc(paths []string) {
	sort.SliceStable(paths, func(i, j int) bool {
		return cmpVersion(pathVersion(paths[i]), pathVersion(paths[j])) > 0
	})
}

// cudaJobsCap plafonne le parallélisme d'un build CUDA selon la RAM disponible.
// nvcc est très gourmand : chaque unité de compilation lourde (surtout avec
// GGML_CUDA_FA_ALL_QUANTS, qui génère beaucoup de gros .cu) peut culminer autour
// de 2 Go. Sur une machine à beaucoup de cœurs mais peu de RAM, « -j = tous les
// cœurs » fait tomber le build en OOM avec une erreur illisible (le genre
// d'échec qui fait abandonner l'utilisateur). On vise ~2 Go par job, borné par
// le nombre de cœurs, minimum 1. RAM inconnue (0) => aucune restriction.
func cudaJobsCap(cpuJobs int) int {
	ram := totalRAMGB()
	if ram <= 0 {
		return cpuJobs
	}
	byRAM := int(ram / 2.0)
	if byRAM < 1 {
		byRAM = 1
	}
	if byRAM < cpuJobs {
		return byRAM
	}
	return cpuJobs
}

// buildPlanFor construit le plan CMake. `force` vide = détection automatique de
// l'accélérateur ; sinon on impose ce backend ("cuda"|"hip"|"vulkan"|"cpu"|
// "metal") sans passer par la détection — utile quand la détection choisit un
// backend cassé sur la machine (HIP à demi installé, etc.).
func buildPlanFor(force string) buildPlan {
	force = strings.ToLower(strings.TrimSpace(force))
	p := buildPlan{backend: "cpu", jobs: numJobs()}
	// Flags communs : Release + tuning natif pour la machine de build.
	// (libcurl est activé d'office par llama.cpp ; LLAMA_CURL est déprécié.)
	p.flags = []string{
		"-DCMAKE_BUILD_TYPE=Release",
		"-DGGML_NATIVE=ON",
		// L'UI web embarquée de llama-server exige npm (ou un téléchargement
		// d'assets pré-compilés depuis HuggingFace) pour générer un service-worker
		// PWA — une dépendance lourde qui casse le build sur une machine sans node.
		// ajean fournit sa propre UI, donc on la désactive : build plus rapide et
		// sans dépendance réseau/npm. BUILD_UI=OFF coupe npm ; USE_PREBUILT_UI=OFF
		// coupe le téléchargement d'assets pré-compilés depuis HuggingFace (qui
		// échoue sur un réseau restreint et fait planter l'embed). Sur un checkout
		// neuf le dist est vide → llama-server embarque une UI vide sans erreur.
		// Voir scripts/ui-assets.cmake côté llama.cpp.
		"-DLLAMA_BUILD_UI=OFF",
		"-DLLAMA_USE_PREBUILT_UI=OFF",
	}

	// Sur Windows, on compile avec le générateur NINJA (et non Visual Studio).
	//
	// Le générateur Visual Studio passe par MSBuild, qui exige : (1) que CMake
	// trouve « une instance de Visual Studio » correspondant EXACTEMENT au nom du
	// générateur (« Visual Studio 17 2022 »…) — cassé dès qu'un utilisateur a une
	// autre édition, ex. VS 2026 (issue #73) ; (2) pour CUDA, l'intégration MSBuild
	// « CUDA x.y.props » installée au bon endroit — absente/mauvaise version →
	// « No CUDA toolset found » (issues #10, #75). Ces deux erreurs cryptiques
	// (« exit status 1 ») sont STRUCTURELLES au générateur VS.
	//
	// Ninja invoque cl.exe et nvcc directement : aucune de ces deux dépendances.
	// Il lui faut juste l'environnement MSVC (INCLUDE/LIB/PATH), que le générateur
	// VS mettait en place tout seul — on le fournit via vcvars au moment du build
	// (voir buildLlamacpp → msvcDevEnv). Ninja est mono-configuration : pas de « -A »,
	// et CMAKE_BUILD_TYPE=Release (déjà posé) suffit.
	if runtime.GOOS == "windows" {
		p.gen = "Ninja"
		// Désactive les en-têtes précompilés (PCH) du build. En amont, llama.cpp a
		// activé le PCH sur tools/server (commit 3bcfeb70, 2026-09-11) : sous MSVC,
		// le symbole de comptabilité du PCH est ré-exporté par WINDOWS_EXPORT_ALL_SYMBOLS
		// de la lib partagée llama-server-impl sous la forme d'un « __ » ambigu, ce qui
		// casse l'édition de liens (LNK2001 « symbole externe non résolu __ » → LNK1120).
		// Le lien casse llama-server.exe alors que tout le reste compile (issue #72 :
		// build KO sur RTX 5060 Ti/A5000 et RTX 5090 le jour où master était cassé,
		// corrigé 8 h plus tard en amont par la PR #28763). Comme ajean suit master en
		// continu, on se protège de cette classe de casse transitoire : le PCH n'est
		// qu'une optimisation de temps de compilation, le couper ne change ni les
		// artefacts ni le lien, mais supprime le symbole que MSVC mal-exporte.
		p.flags = append(p.flags, "-DCMAKE_DISABLE_PRECOMPILE_HEADERS=ON")
	}

	if runtime.GOOS == "darwin" && (force == "" || force == "metal") {
		// Metal est activé par défaut sur Apple Silicon ; on l'explicite.
		p.backend = "metal"
		p.flags = append(p.flags, "-DGGML_METAL=ON")
		return p
	}

	// Forçage explicite : on saute la détection et on impose le backend demandé.
	// CPU ne demande aucun flag (déjà le défaut du plan).
	if force == "cpu" {
		return p
	}
	if force == "vulkan" {
		p.backend = "vulkan"
		p.flags = append(p.flags, "-DGGML_VULKAN=ON")
		return p
	}
	if force == "hip" {
		p.backend = "hip"
		p.flags = append(p.flags, "-DGGML_HIP=ON")
		return p
	}
	if force == "cuda" {
		p.backend = "cuda"
		applyCudaPlan(&p)
		return p
	}

	// CUDA : nvcc présent ET un GPU NVIDIA visible.
	if nvcc := findNvcc(); nvcc != "" && hasNvidiaGPU() {
		p.backend = "cuda"
		applyCudaPlan(&p)
		return p
	}

	// AMD ROCm / HIP.
	if hasTool("hipcc") || isDir("/opt/rocm") {
		p.backend = "hip"
		p.flags = append(p.flags, "-DGGML_HIP=ON")
		return p
	}

	// Vulkan (GPU générique) — utile sur Intel/AMD sans ROCm.
	if hasTool("glslc") && (hasVulkanLib() || hasTool("vulkaninfo")) {
		p.backend = "vulkan"
		p.flags = append(p.flags, "-DGGML_VULKAN=ON")
		return p
	}

	return p // CPU
}

// applyCudaPlan renseigne un plan déjà marqué backend=cuda : choix d'un toolkit
// COMPATIBLE avec le GPU (et non le plus récent aveuglément), flags GGML, arch.
// Pose cudaArchUnsupported quand aucun toolkit installé ne sait compiler pour le
// GPU détecté (ex. GPU Pascal sm_61 alors que seul CUDA 13 — qui a supprimé
// Pascal — est présent), afin que buildLlamacpp avertisse au lieu de lancer un
// build qui casse sur le test de compilation de CMake.
func applyCudaPlan(p *buildPlan) {
	p.jobs = cudaJobsCap(p.jobs)
	arch := detectCudaArch()
	nvcc, ver, ok := selectNvcc(arch)
	if nvcc == "" { // repli défensif : garder le comportement historique
		nvcc = findNvcc()
		ver = cudaVersionOf(nvcc)
		ok = true
	}
	p.cudaCXX = nvcc
	p.cudaVer = ver
	if arch != "" && !ok {
		p.cudaArchUnsupported = true
	}
	// GGML_CUDA_FA_ALL_QUANTS=ON : compile un noyau Flash-Attention pour CHAQUE
	// combinaison de type de cache KV (K et V). Sans lui, llama.cpp ne compile
	// qu'un sous-ensemble (f16, q8_0, q4_0) ; tout autre type — q5_0, q5_1, q4_1,
	// et donc les combos asymétriques K/V recommandés (q5_0/q4_1…) — n'a pas de
	// noyau et retombe sur l'attention générique avec déquantification par
	// position : le PREFILL s'effondre (~10× plus lent), pas un gain marginal.
	p.flags = append(p.flags, "-DGGML_CUDA=ON", "-DGGML_CUDA_F16=ON", "-DGGML_CUDA_FA_ALL_QUANTS=ON")
	// Racine du toolkit explicite : sans elle, CMake la déduit du chemin de nvcc.
	// Avec un nvcc hors toolkit (/usr/bin/nvcc, paquet Ubuntu) il cherche
	// cuda_runtime.h et cudart dans /usr, ne les trouve pas, et sort « CUDA Toolkit
	// not found » APRÈS avoir pourtant affiché la version de nvcc.
	if root := cudaToolkitRoot(nvcc); root != "" && runtime.GOOS != "windows" {
		p.flags = append(p.flags, "-DCUDAToolkit_ROOT="+root, "-DCMAKE_CUDA_COMPILER="+nvcc)
	}
	if arch != "" {
		p.cudaArch = arch
		p.flags = append(p.flags, "-DCMAKE_CUDA_ARCHITECTURES="+arch)
	}
}

// buildLlamacpp configures and builds the llama-server target. It handles the
// "relocated checkout" gotcha: a build/ whose CMake cache was generated under a
// different source path can't reconfigure in place, so we wipe it. `clean`
// forces a from-scratch build regardless.
func buildLlamacpp(repo string, p buildPlan, clean bool) error {
	// Garde-fou avant de lancer un build CUDA voué à l'échec : le GPU de la machine
	// est plus ancien que ce que le seul toolkit CUDA installé sait compiler (ex.
	// GPU Pascal + CUDA 13, qui a supprimé Pascal). Sans ce filet, l'utilisateur
	// attendait la config CMake pour ne récolter qu'un « nvcc is not able to
	// compile a simple test program » incompréhensible. On explique et on renvoie
	// une erreur actionnable au lieu de brûler du temps sur un build impossible.
	if p.backend == "cuda" && p.cudaArchUnsupported {
		return cudaArchUnsupportedError(p)
	}

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

	// Environnement de build. On accumule les variables dans une petite map
	// ordonnée (clé PATH insensible à la casse sous Windows) puis on la sérialise
	// en KEY\x00VAL pour runBuildStep.
	be := newBuildEnv()

	// Windows + Ninja : fournir l'environnement MSVC (cl.exe, INCLUDE, LIB,
	// LIBPATH, PATH…) que le générateur Visual Studio mettait en place seul. Sans
	// lui, Ninja ne trouve pas le compilateur (« cl : not found » / configure KO).
	if runtime.GOOS == "windows" && p.gen == "Ninja" {
		devEnv, err := msvcDevEnv()
		if err != nil {
			return err
		}
		for _, kv := range devEnv {
			if i := strings.IndexByte(kv, '='); i > 0 {
				be.set(kv[:i], kv[i+1:])
			}
		}
	}

	// CUDA : nvcc exposé via CUDACXX et son dossier bin en tête de PATH.
	if p.backend == "cuda" && p.cudaCXX != "" {
		cudaBin := filepath.Dir(p.cudaCXX)
		be.set("CUDACXX", p.cudaCXX)
		be.prependPath(cudaBin)
		// CUDA_PATH / CUDA_PATH_Vx_y : sans effet pour Ninja, mais inoffensif et
		// utile si un jour on repasse par MSBuild (no-op sur Unix).
		for _, kv := range cudaPathEnv(filepath.Dir(cudaBin)) {
			if i := strings.IndexByte(kv, '='); i > 0 {
				be.set(kv[:i], kv[i+1:])
			}
		}
		// Générateur Visual Studio (repli si jamais Ninja n'est pas choisi) :
		// vérifier/réparer l'intégration MSBuild de CUDA avant de configurer.
		if strings.HasPrefix(p.gen, "Visual Studio") {
			if err := ensureCudaVSIntegration(filepath.Dir(cudaBin)); err != nil {
				return err
			}
		}
	}
	env := be.serialize()

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

// cudaArchFamily nomme la génération d'un code d'arch CMake (61 → Pascal), pour
// un message lisible. "" si non reconnu.
func cudaArchFamily(code int) string {
	switch {
	case code >= 50 && code < 60:
		return "Maxwell (GTX 900)"
	case code >= 60 && code < 70:
		return "Pascal (GTX 10xx)"
	case code >= 70 && code < 75:
		return "Volta"
	}
	return ""
}

// archDisplay met un code CMake au format compute capability lisible
// (61 → « 6.1 », 120 → « 12.0 »).
func archDisplay(code int) string {
	s := strconv.Itoa(code)
	if len(s) < 2 {
		return s
	}
	return s[:len(s)-1] + "." + s[len(s)-1:]
}

// cudaArchUnsupportedError explique, quand le GPU est trop ancien pour le seul
// toolkit CUDA installé, pourquoi le build est impossible et comment le régler
// (installer un CUDA 12.x, qui supporte encore Maxwell/Pascal/Volta). Le message
// part aussi vers l'UI web (emitBuildLine).
func cudaArchUnsupportedError(p buildPlan) error {
	minArch := minArchCode(p.cudaArch)
	fam := cudaArchFamily(minArch)
	if fam != "" {
		fam = " — famille " + fam
	}
	ver := p.cudaVer
	if ver == "" {
		ver = "installé"
	} else {
		ver = "CUDA " + ver
	}
	lines := []string{
		"Le moteur ne peut pas être compilé pour ce GPU avec le CUDA présent sur la machine.",
		fmt.Sprintf("GPU détecté : compute capability %s%s.", archDisplay(minArch), fam),
		fmt.Sprintf("Toolkit trouvé : %s, qui ne compile plus que pour Turing (sm_75) et au-delà.", ver),
		"CUDA 13 a supprimé le support de Maxwell, Pascal et Volta.",
		"",
		"Solution : installe le CUDA Toolkit 12.x (par ex. 12.8), qui supporte encore ce GPU,",
		"puis relance l'installation du moteur. Les deux versions de CUDA peuvent cohabiter.",
	}
	for _, l := range lines {
		fmt.Println("  " + l)
		emitBuildLine(l)
	}
	return fmt.Errorf("GPU %s incompatible avec %s (CUDA 13 ne supporte plus cette génération) : installe un CUDA Toolkit 12.x", archDisplay(minArch), ver)
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
		fmt.Printf("            <VS>\\MSBuild\\Microsoft\\VC\\<version>\\BuildCustomizations, puis relance %s.\n", bold("ajean llamacpp install"))
	}
	// Linux/CUDA : nvcc trouvé mais en-têtes/cudart introuvables = le toolkit
	// complet n'est pas installé (seul le paquet nvcc l'est). On passe déjà
	// CUDAToolkit_ROOT quand un vrai toolkit existe ; si ça échoue quand même,
	// c'est qu'il manque pour de bon.
	if p.backend == "cuda" && strings.Contains(log, "CUDA Toolkit not found") {
		fmt.Printf("\n%s nvcc est présent mais le CUDA Toolkit complet (en-têtes + cudart) est introuvable.\n", yellow("[dépendance]"))
		fmt.Printf("            Installe le toolkit NVIDIA officiel (il se pose dans /usr/local/cuda), puis relance %s.\n", bold("ajean llamacpp install"))
	}
	// nvcc casse sur le programme de test de CMake : cause fréquente = GPU trop
	// ancien pour le toolkit (CUDA 13 a supprimé Maxwell/Pascal/Volta). Normalement
	// intercepté AVANT le build par cudaArchUnsupportedError ; ce filet couvre les
	// cas où l'arch n'a pas pu être détectée (driver muet) et où CMake a tenté sa
	// détection native.
	if p.backend == "cuda" && (strings.Contains(log, "is not able to compile a simple test program") ||
		strings.Contains(log, "CMakeTestCUDACompiler")) {
		ver := p.cudaVer
		if ver != "" {
			ver = "CUDA " + ver + " "
		}
		fmt.Printf("\n%s nvcc %séchoue à compiler le programme de test de CMake.\n", yellow("[CUDA]"), ver)
		fmt.Printf("            Cause la plus fréquente : ton GPU est plus ancien que ce que ce CUDA supporte.\n")
		fmt.Printf("            CUDA 13 a supprimé Maxwell (GTX 900), Pascal (GTX 10xx) et Volta ; ils exigent un CUDA 12.x.\n")
		fmt.Printf("            Installe le CUDA Toolkit 12.x (par ex. 12.8) puis relance %s.\n", bold("ajean llamacpp install"))
	}
	if p.backend == "vulkan" && strings.Contains(log, "SPIRV-Headers") {
		fmt.Printf("\n%s dépendance manquante pour le backend %s : les en-têtes SPIR-V (paquet « SPIRV-Headers ») sont introuvables.\n",
			yellow("[dépendance]"), green("Vulkan"))
		if cmd := pkgInstallHint("spirv-headers"); cmd != "" {
			fmt.Printf("            installe-les puis relance %s : %s\n", bold("ajean llamacpp install"), bold(cmd))
		} else {
			fmt.Printf("            installe le paquet de développement « SPIRV-Headers » de ta distribution, puis relance %s.\n", bold("ajean llamacpp install"))
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
	if runtime.GOOS != "windows" {
		return findNvccUnix(exec.LookPath)
	}
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
				return highestVersionedPath(matches) // tri sémantique : v12.10 > v12.4, v13.4 > v9.0
			}
		}
		return ""
	}
	return ""
}

// findNvccUnix choisit nvcc sous Linux/macOS. L'ORDRE compte : un nvcc rangé
// dans un vrai toolkit (/usr/local/cuda/bin/nvcc) passe AVANT celui que le PATH
// expose. Sur Ubuntu, le paquet nvidia-cuda-toolkit pose un shim /usr/bin/nvcc
// alors que les en-têtes et cudart vivent dans /usr/local/cuda : CMake déduit
// alors la racine du toolkit depuis le chemin de nvcc (donc /usr), n'y trouve ni
// cuda_runtime.h ni cudart, et échoue sur « CUDA Toolkit not found » alors que
// nvcc a bien été détecté. lookPath est injecté pour les tests.
func findNvccUnix(lookPath func(string) (string, error)) string {
	if p := "/usr/local/cuda/bin/nvcc"; isFile(p) {
		return p
	}
	matches, _ := filepath.Glob("/usr/local/cuda-*/bin/nvcc")
	if len(matches) > 0 {
		return highestVersionedPath(matches) // tri sémantique : cuda-12.10 > cuda-12.4
	}
	if p, err := lookPath("nvcc"); err == nil {
		return p
	}
	return ""
}

// nvccCandidates liste TOUS les nvcc installés (pas seulement le plus récent),
// pour pouvoir choisir un toolkit compatible avec le GPU. Windows : PATH +
// CUDA_PATH + tous les toolkits …\CUDA\v*. Unix : /usr/local/cuda*, puis PATH.
func nvccCandidates() []string {
	var out []string
	seen := map[string]bool{}
	add := func(p string) {
		if p == "" || !isFile(p) {
			return
		}
		ap, _ := filepath.Abs(p)
		key := strings.ToLower(ap)
		if !seen[key] {
			seen[key] = true
			out = append(out, p)
		}
	}
	if runtime.GOOS == "windows" {
		if p, err := exec.LookPath("nvcc"); err == nil {
			add(p)
		}
		if cp := os.Getenv("CUDA_PATH"); cp != "" {
			add(filepath.Join(cp, "bin", "nvcc.exe"))
		}
		for _, base := range []string{os.Getenv("ProgramFiles"), `C:\Program Files`} {
			if base == "" {
				continue
			}
			m, _ := filepath.Glob(filepath.Join(base, "NVIDIA GPU Computing Toolkit", "CUDA", "v*", "bin", "nvcc.exe"))
			for _, p := range m {
				add(p)
			}
		}
		return out
	}
	add("/usr/local/cuda/bin/nvcc")
	m, _ := filepath.Glob("/usr/local/cuda-*/bin/nvcc")
	for _, p := range m {
		add(p)
	}
	if p, err := exec.LookPath("nvcc"); err == nil {
		add(p)
	}
	return out
}

// reNvccRelease capture la version dans la sortie de `nvcc --version`
// (« Cuda compilation tools, release 12.8, V12.8.61 »).
var reNvccRelease = regexp.MustCompile(`release (\d+\.\d+)`)

// cudaVersionOf déduit la version d'un toolkit à partir du chemin de son nvcc
// (« …/v13.4/bin/nvcc.exe » → « 13.4 », « /usr/local/cuda-12.8/… » → « 12.8 »).
// Le layout /usr/local/cuda (lien symbolique sans version) n'a pas de numéro
// dans son chemin : on retombe alors sur `nvcc --version`. "" si indéterminable.
func cudaVersionOf(nvcc string) string {
	if nvcc == "" {
		return ""
	}
	if v := pathVersion(nvcc); v != "" {
		return v
	}
	out, err := hideCmd(exec.Command(nvcc, "--version")).Output()
	if err != nil {
		return ""
	}
	if m := reNvccRelease.FindStringSubmatch(string(out)); m != nil {
		return m[1]
	}
	return ""
}

// cudaMinArchForVersion renvoie la compute capability MINIMALE (code CMake, ex.
// 75) qu'un toolkit CUDA de version `ver` sait encore compiler. CUDA 13 a
// supprimé Maxwell/Pascal/Volta → minimum Turing (sm_75). CUDA 12 descend
// jusqu'à Maxwell (sm_50), CUDA 11 jusqu'à Kepler (sm_35). 0 = version inconnue
// (on ne bloque alors pas).
func cudaMinArchForVersion(ver string) int {
	maj := 0
	if i := strings.IndexByte(ver, '.'); i > 0 {
		maj, _ = strconv.Atoi(ver[:i])
	} else {
		maj, _ = strconv.Atoi(ver)
	}
	switch {
	case maj >= 13:
		return 75
	case maj == 12:
		return 50
	case maj == 11:
		return 35
	}
	return 0
}

// minArchCode renvoie le plus PETIT code d'arch d'une liste « 61;86 » (le GPU le
// plus ancien de la machine, celui qui contraint le choix du toolkit). 0 si vide.
func minArchCode(archs string) int {
	min := 0
	for _, a := range strings.Split(archs, ";") {
		n, err := strconv.Atoi(strings.TrimSpace(a))
		if err != nil {
			continue
		}
		if min == 0 || n < min {
			min = n
		}
	}
	return min
}

// selectNvcc choisit le toolkit pour compiler pour `archs` (codes détectés par
// nvidia-smi). Il retient le nvcc de version la plus ÉLEVÉE qui sait ENCORE
// compiler pour le GPU le plus ancien de la machine — au lieu du plus récent
// aveuglément. Ça évite qu'un CUDA 13 tout juste installé casse le build d'un GPU
// Pascal (sm_61) que CUDA 13 ne supporte plus. Renvoie le nvcc retenu, sa
// version, et compatible=false si AUCUN toolkit installé ne supporte le GPU (on
// renvoie alors le plus récent, à l'appelant d'avertir).
func selectNvcc(archs string) (nvcc, ver string, compatible bool) {
	cands := nvccCandidates()
	if len(cands) == 0 {
		return "", "", false
	}
	sortByVersionDesc(cands) // plus récent d'abord
	minGPU := minArchCode(archs)
	for _, c := range cands {
		v := cudaVersionOf(c)
		mn := cudaMinArchForVersion(v)
		if minGPU == 0 || mn == 0 || minGPU >= mn {
			return c, v, true
		}
	}
	best := cands[0]
	return best, cudaVersionOf(best), false
}

// cudaToolkitRoot remonte de <root>/bin/nvcc à <root>. Renvoie "" quand le
// chemin ne suit pas ce layout, ou quand la racine déduite est /usr : dans ce
// cas la racine n'apprend rien à CMake (c'est justement le cas qui échoue).
func cudaToolkitRoot(nvcc string) string {
	if nvcc == "" {
		return ""
	}
	if filepath.Base(filepath.Dir(nvcc)) != "bin" {
		return ""
	}
	root := filepath.Dir(filepath.Dir(nvcc))
	switch filepath.ToSlash(root) {
	case "", ".", "/", "/usr":
		return ""
	}
	return root
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

// hasVulkanLib détecte la présence du loader Vulkan (libvulkan) sans supposer un
// unique chemin de distribution. L'ancienne détection testait en dur le chemin
// Debian multiarch (/usr/lib/x86_64-linux-gnu/libvulkan.so.1), invisible sur
// Fedora/RHEL/Atomic où la lib vit dans /usr/lib64/ (issues #28, #29). On
// interroge d'abord ldconfig (la source de vérité du linker dynamique), puis on
// retombe sur une liste de chemins connus.
func hasVulkanLib() bool {
	// ldconfig -p liste les bibliothèques connues du cache, tous chemins confondus.
	if out, err := exec.Command("ldconfig", "-p").Output(); err == nil {
		if strings.Contains(string(out), "libvulkan.so") {
			return true
		}
	}
	for _, p := range []string{
		"/usr/lib/x86_64-linux-gnu/libvulkan.so.1", // Debian/Ubuntu multiarch
		"/usr/lib64/libvulkan.so.1",                // Fedora/RHEL/Bazzite
		"/usr/lib/libvulkan.so.1",                  // Arch et divers
		"/lib64/libvulkan.so.1",
	} {
		if isFile(p) {
			return true
		}
	}
	return false
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
	emitBuildLine("outils manquants : " + strings.Join(missing, ", ") + " — installation automatique…")
	for _, t := range missing {
		// Phase de haut niveau : sans ça, l'UI web restait figée sur la phase
		// précédente pendant toute l'installation winget (plusieurs dizaines de
		// secondes), donnant l'impression d'un blocage.
		emitBuildPhase("installation de " + t + "…")
		if err := autoInstallTool(t); err != nil {
			fmt.Printf("  %s %s: %v\n", dim("•"), t, err)
			emitBuildLine("échec de l'installation de " + t + " : " + err.Error())
		} else {
			emitBuildLine(t + " installé")
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
	cmd := hideCmd(exec.Command("git", args...)) // pas de flash console (appelé à chaque refresh de l'UI)
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
	cmd := hideCmd(exec.Command(bin, args...))
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
	// Start + register + Wait (au lieu de Run) pour que le bouton « arrêter » puisse
	// tuer l'arbre de process de cette étape (git clone, fetch…).
	if err := cmd.Start(); err != nil {
		return err
	}
	setCurCmd(cmd)
	err := cmd.Wait()
	setCurCmd(nil)
	return err
}

// runBuildStep runs a compile step while keeping the terminal clean: the full
// output goes to logPath, and the screen shows only a single self-rewriting
// progress line (spinner + compiled-file count) plus any real compiler
// diagnostics. The hundreds of per-file nvcc/cl command echoes are hidden. On
// failure the tail of the log is printed so the actual error is never lost.
func runBuildStep(name, dir, extraEnv, bin, logPath string, args ...string) error {
	fmt.Printf("\n%s %s\n", cyan("▶"), name)
	cmd := hideCmd(exec.Command(bin, args...))
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
	setCurCmd(cmd) // permet au bouton « arrêter » de tuer l'arbre de process
	defer setCurCmd(nil)

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
				fmt.Fprintln(logf, line) // fichier de log : TOUT (debug complet)
			}
			cf := compiledFile(line)
			pl := phaseLabel(line)
			isErr := reBuildError.MatchString(line) || strings.HasPrefix(strings.TrimSpace(line), "CMake Error")
			// Sink (log affiché dans l'UI web) : uniquement la progression, les
			// phases et les VRAIES erreurs. Les milliers de warnings/notes du
			// compilateur (et leurs accents mal encodés depuis la sortie ANSI de
			// cl.exe) restent dans le fichier de log, pas sous les yeux de
			// l'utilisateur — ça donnait un journal illisible et « bas de gamme ».
			if cf != "" || pl != "" || isErr {
				emitBuildLine(line)
			}
			mu.Lock()
			if cf != "" {
				count++
				label = fmt.Sprintf("compilation… %d fichiers  %s", count, dim("("+cf+")"))
				mu.Unlock()
				continue
			}
			if pl != "" {
				label = pl
			}
			mu.Unlock()
			// On ne fait remonter au TERMINAL que les vraies ERREURS (les warnings
			// MSVC/linker d'un projet tiers sont du bruit ; ils restent dans le log).
			// Les CMake Error de la phase configure sont aussi affichés.
			if isErr {
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
