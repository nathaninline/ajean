package ajean

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// backend_llamacpp.go — gestion du backend llama.cpp (clone, build, mise à jour).
//
// `ajean llamacpp install`  installe un build neuf, détecte automatiquement
//                          l'accélérateur (CUDA / ROCm / Metal / CPU) et la
//                          compute capability du GPU, puis pointe BIN dessus.
// `ajean llamacpp update`   met à jour le dépôt existant (git pull) et recompile
//                          avec la bonne config, sans intervention.
// `ajean llamacpp status`   montre le commit courant, le backend détecté et le
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
	case "update":
		return llamacppUpdate(args)
	case "status", "":
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
			return fmt.Errorf("binaires installés mais échec d'écriture de BIN : %w", err)
		}
		fmt.Printf("%s BIN mis à jour — %s pour appliquer\n", green("✓"), bold("ajean restart"))
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
// otherwise the default under $AJEAN_HOME/backends/llama.cpp.
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
	return filepath.Join(backendsDir(), "llama.cpp")
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
	customURL := ""
	customName := ""
	backend := ""
	for _, a := range args {
		switch {
		case strings.HasPrefix(a, "--dir="):
			repo = strings.TrimPrefix(a, "--dir=")
		case strings.HasPrefix(a, "--ref="):
			ref = strings.TrimPrefix(a, "--ref=")
		case strings.HasPrefix(a, "--repo="):
			customURL = strings.TrimPrefix(a, "--repo=")
		case strings.HasPrefix(a, "--name="):
			customName = strings.TrimPrefix(a, "--name=")
		case strings.HasPrefix(a, "--backend="):
			backend = strings.ToLower(strings.TrimSpace(strings.TrimPrefix(a, "--backend=")))
			if !isKnownBuildBackend(backend) {
				return fmt.Errorf("backend inconnu: %s (attendu cuda | hip | vulkan | cpu)", backend)
			}
		case a == "--force":
			force = true
		case a == "--no-switch":
			noSwitch = true
		default:
			return fmt.Errorf("option inconnue: %s", a)
		}
	}

	// Backend CUSTOM : fork llama.cpp installé depuis une URL Git dans
	// backends/<nom>. On NE touche PAS au BIN global — un backend custom se
	// choisit par modèle (preset → section Moteur → « backend détecté »). C'est
	// exactement le cas d'un moteur ternaire type PrismML qui ne sert qu'à un
	// seul modèle : le rattacher globalement casserait les autres presets.
	if customURL != "" {
		bin, err := installCustomBackend(customURL, customName, ref, func(s string) {
			fmt.Printf("%s %s\n", cyan("▶"), s)
		})
		if err != nil {
			return err
		}
		fmt.Printf("\n%s backend custom compilé : %s\n", green("✓"), bin)
		fmt.Printf("Pour l'utiliser : édite un modèle → section %s → « backend détecté » et choisis-le.\n", bold("Moteur"))
		return nil
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
			fmt.Printf("       → %s pour le mettre à jour, ou --force pour repartir de zéro\n", bold("ajean llamacpp update"))
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

	plan := buildPlanFor(backend)
	if backend != "" {
		fmt.Printf("%s backend forcé : %s\n", yellow("[info]"), bold(backend))
	}
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
		fmt.Printf("%s --no-switch : configuration inchangée (BIN à régler manuellement)\n", dim("[info]"))
		return nil
	}
	if err := SetConfigKey("BIN", bin); err != nil {
		return fmt.Errorf("build ok mais échec d'écriture de BIN : %w", err)
	}
	fmt.Printf("%s BIN mis à jour\n", green("✓"))
	fmt.Printf("\nProchaines étapes :\n  1. renseigne MODEL : %s\n  2. démarre        : %s\n",
		bold("ajean edit"), bold("ajean restart"))
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
		return fmt.Errorf("aucun dépôt llama.cpp trouvé (%s).\n       → lance d'abord %s", repo, bold("ajean llamacpp install"))
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
		fmt.Printf("%s --no-restart : pense à lancer %s\n", dim("[info]"), bold("ajean restart"))
		return nil
	}
	if svcWasUp {
		fmt.Printf("%s redémarrage du service…\n", cyan("▶"))
		return serviceAction("start")
	}
	fmt.Printf("%s service non démarré auparavant — lance %s quand tu veux\n", dim("[info]"), bold("ajean start"))
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
		fmt.Printf("  %s pas encore installé — %s\n", yellow("état"), bold("ajean llamacpp install"))
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
			fmt.Printf("  maj      : %s commit(s) de retard sur origin/%s — %s\n", yellow(behind), branch, bold("ajean llamacpp update"))
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
// (implémentation dans backend_build.go)

// ---------------------------------------------------------------------------
// Backends custom (fork llama.cpp installé depuis une URL Git)
// ---------------------------------------------------------------------------

// installCustomBackend clone (ou met à jour) un fork de llama.cpp depuis `url`
// dans backends/<name> et compile llama-server avec le plan détecté pour la
// machine. Il NE touche PAS à BIN : un backend custom se choisit par modèle
// (éditeur de preset → section Moteur → « backend détecté »). Renvoie le chemin
// du binaire compilé. `phase` reçoit les étapes de haut niveau (clone, build…) ;
// la sortie détaillée du build passe par le sink habituel (terminal en CLI,
// job web sinon).
func installCustomBackend(url, name, ref string, phase func(string)) (string, error) {
	if phase == nil {
		phase = func(string) {}
	}
	url = strings.TrimSpace(url)
	if url == "" {
		return "", fmt.Errorf("URL du dépôt vide")
	}
	if !looksLikeGitURL(url) {
		return "", fmt.Errorf("URL de dépôt invalide (attendu https://…, git@… ou ssh://…) : %s", url)
	}
	if strings.TrimSpace(name) == "" {
		name = deriveBackendName(url)
	}
	dir, err := backendDir(name)
	if err != nil {
		return "", err
	}

	phase("vérification des outils (git, cmake, compilateur)…")
	if err := requireTools("git", "cmake"); err != nil {
		return "", err
	}
	if err := ensureCompiler(); err != nil {
		return "", err
	}
	ensureAccelerator() // best-effort : installe le toolkit GPU si une carte est détectée

	if isDir(filepath.Join(dir, ".git")) {
		// Backend déjà cloné : on le met à jour plutôt que de re-cloner.
		phase("dépôt déjà présent — mise à jour…")
		_ = runStep("git fetch", dir, "git", "fetch", "origin", "--quiet")
		if ref != "" {
			if err := runStep("git checkout", dir, "git", "checkout", ref); err != nil {
				return "", err
			}
		} else if branch := gitOutput(dir, "rev-parse", "--abbrev-ref", "HEAD"); branch != "" && branch != "HEAD" {
			_ = runStep("git pull --ff-only", dir, "git", "pull", "--ff-only", "origin", branch)
		}
	} else {
		if err := os.MkdirAll(filepath.Dir(dir), 0o755); err != nil {
			return "", err
		}
		phase("clone de " + url + "…")
		if err := runStep("git clone", "", "git", "clone", "--depth=1", url, dir); err != nil {
			return "", fmt.Errorf("git clone a échoué : %w", err)
		}
		if ref != "" {
			// --depth=1 ne récupère que HEAD ; on approfondit pour atteindre le ref.
			_ = runStep("git fetch", dir, "git", "fetch", "--unshallow", "origin")
			if err := runStep("git checkout", dir, "git", "checkout", ref); err != nil {
				return "", err
			}
		}
	}

	plan := detectBuildPlan()
	phase(fmt.Sprintf("compilation (backend=%s)…", plan.backend))
	if err := buildLlamacpp(dir, plan, false); err != nil {
		return "", err
	}
	bin := llamaServerBin(dir)
	if bin == "" {
		return "", fmt.Errorf("build terminé mais binaire introuvable sous %s", filepath.Join(dir, "build"))
	}
	return bin, nil
}

func looksLikeGitURL(u string) bool {
	for _, p := range []string{"https://", "http://", "git@", "ssh://", "git://"} {
		if strings.HasPrefix(u, p) {
			return true
		}
	}
	return false
}

// deriveBackendName construit un nom de dossier de backend à partir d'une URL de
// dépôt. On garde le nom du dépôt, préfixé du propriétaire quand ça éviterait
// une collision avec le backend canonique (backends/llama.cpp).
func deriveBackendName(url string) string {
	s := strings.TrimSuffix(strings.TrimSpace(url), ".git")
	s = strings.TrimRight(s, "/")
	parts := strings.FieldsFunc(s, func(r rune) bool { return r == '/' || r == ':' })
	repo, owner := "", ""
	if n := len(parts); n > 0 {
		repo = parts[n-1]
		if n > 1 {
			owner = parts[n-2]
		}
	}
	name := repo
	if name == "" {
		name = "backend"
	}
	// Un fork nommé « llama.cpp » écraserait le backend optimisé canonique ; on
	// le distingue par le propriétaire (ex. llama.cpp-prismml-eng).
	if strings.EqualFold(name, "llama.cpp") && owner != "" {
		name = name + "-" + owner
	}
	return sanitizeBackendName(name)
}

// sanitizeBackendName réduit un nom à un identifiant de dossier sûr
// ([a-z0-9._-]), pour qu'il ne puisse jamais s'échapper de backends/.
func sanitizeBackendName(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		case r == ' ' || r == '/' || r == '\\':
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), ".-")
	if out == "" {
		return "backend"
	}
	return out
}

// backendDir renvoie backends/<name>, en refusant tout nom qui s'échapperait du
// dossier backends (../, chemin absolu…).
func backendDir(name string) (string, error) {
	name = sanitizeBackendName(name)
	root, err := filepath.Abs(backendsDir())
	if err != nil {
		return "", err
	}
	p := filepath.Join(root, name)
	if !strings.HasPrefix(p, root+string(filepath.Separator)) {
		return "", fmt.Errorf("nom de backend invalide")
	}
	return p, nil
}
