package jean

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	case "prebuilt":
		// Binaires officiels précompilés (aucune compilation) — voir backend_prebuilt.go.
		bin, err := prebuiltInstall(
			func(s string) { fmt.Println("  " + s) },
			func(s string) { fmt.Printf("%s %s\n", cyan("▶"), s) },
		)
		if err != nil {
			return err
		}
		if err := SetConfigKey("BIN", bin); err != nil {
			return fmt.Errorf("binaires installés mais échec écriture BIN dans config.env: %w", err)
		}
		fmt.Printf("%s BIN mis à jour dans %s — %s pour appliquer\n", green("✓"), confPath(), bold("jean restart"))
		return nil
	default:
		return fmt.Errorf("sous-commande inconnue: %s (install | update | prebuilt | status)", sub)
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
