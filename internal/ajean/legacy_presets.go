package ajean

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// legacy_presets.go : de quoi convertir les presets d'AJEAN 0.18 en services (services_convert.go).
// Ces fonctions construisaient la commande des moteurs llama.cpp et ExLlama ; elles
// ne servent plus qu'à la conversion et partiront dans une version ultérieure.

// launchSpec : commande d'un moteur, prête à lancer (cmdServe) ou à écrire
// dans un service (conversion des presets, services_convert.go).
type launchSpec struct {
	Bin   string
	Args  []string // sans argv[0]
	Dir   string
	Env   map[string]string
	Host  string
	Port  string
	Label string
}

// llamaLaunch construit la commande llama-server d'une config. Les variables
// d'environnement nécessaires sont posées dans le process (os.Setenv), puis
// relevées par captureLaunch quand on convertit un preset.
func llamaLaunch(cfg map[string]string) (launchSpec, error) {
	bin := cfg["BIN"]
	if bin == "" {
		return launchSpec{}, fmt.Errorf("BIN non défini — lance « ajean edit »")
	}
	model := cfg["MODEL"]
	if model == "" {
		return launchSpec{}, fmt.Errorf("MODEL non défini — lance « ajean edit »")
	}
	// MODEL vaut soit un simple nom de fichier (le .gguf vit dans AJEAN_HOME ou
	// dans un dossier déclaré — disque externe…), soit un chemin absolu. Sous
	// systemd/launchd le WorkingDirectory vaut AJEAN_HOME, donc le relatif tombait
	// juste ; lancé depuis une app de bureau, le répertoire courant est « / » et
	// llama-server ne trouvait rien. On résout donc explicitement, quel que soit
	// le contexte de lancement.
	resolved, err := resolveServeModelPath(model)
	if err != nil {
		return launchSpec{}, err
	}
	model = resolved
	if _, err := os.Stat(model); err != nil {
		return launchSpec{}, fmt.Errorf("modèle introuvable : %s", model)
	}
	if !filepath.IsAbs(bin) {
		bin = filepath.Join(AjeanHome(), bin)
	}

	// Make sure llama-server can find its bundled shared libraries (the .so/.dll
	// neighbours of the binary). This is platform-specific: LD_LIBRARY_PATH on
	// Linux, PATH on Windows — handled inside execServer.
	setLibraryPath(filepath.Dir(bin))

	// Sélection GPU (ajean gpu) : on filtre les devices visibles par llama-server.
	// CUDA_DEVICE_ORDER=PCI_BUS_ID garantit que les index correspondent à ceux
	// affichés par nvidia-smi (sinon CUDA réordonne par "device le plus rapide").
	if v := cfg["CUDA_VISIBLE_DEVICES"]; v != "" {
		_ = os.Setenv("CUDA_VISIBLE_DEVICES", v)
		_ = os.Setenv("CUDA_DEVICE_ORDER", "PCI_BUS_ID")
	}

	get := func(key, fallback string) string {
		if v, ok := cfg[key]; ok && v != "" {
			return v
		}
		return fallback
	}
	kv := get("KV_TYPE", "")
	ktv := get("KV_TYPE_K", kv)
	vtv := get("KV_TYPE_V", kv)

	llmArgs := []string{bin,
		"-m", model,
		"-ngl", get("NGL", "999"),
		"-c", get("CTX", "32768"),
		"-t", get("THREADS", "0"),
		"-tb", get("THREADS_BATCH", "0"),
		"-b", get("BATCH", "2048"),
		"-ub", get("UBATCH", "512"),
		// Une seule séquence par défaut : au-delà, llama-server DIVISE le contexte
		// entre les slots (n_ctx/n_parallel) ET réserve le cache KV pour chacune,
		// ce qui plafonnait le contexte utile et faisait OOM en spéculatif MTP. Un
		// preset peut remonter ça via NP= ou un -np dans EXTRA_ARGS (ajouté après,
		// donc prioritaire pour llama-server).
		"-np", get("NP", "1"),
		"--host", get("HOST", "0.0.0.0"),
		"--port", get("PORT", "8080"),
	}
	if ktv != "" {
		llmArgs = append(llmArgs, "-ctk", ktv)
	}
	if vtv != "" {
		llmArgs = append(llmArgs, "-ctv", vtv)
	}
	// Vision : le projecteur multimodal (mmproj-*.gguf) donne des yeux au modèle.
	// C'est un fichier .gguf À PART du modèle, chargé via --mmproj. On le résout
	// comme le modèle (nom simple cherché dans les dossiers déclarés, ou chemin
	// absolu), pour qu'un preset écrit sous Windows reste lançable ailleurs et que
	// le champ « Vision » de l'interface n'ait qu'à écrire le nom du fichier.
	// Introuvable = on préfère le dire clairement plutôt que laisser llama-server
	// mourir en boucle sur un « failed to load mmproj » cryptique.
	if mm := strings.TrimSpace(cfg["MMPROJ"]); mm != "" {
		mmPath, err := resolveServeModelPath(mm)
		if err != nil {
			return launchSpec{}, fmt.Errorf("projecteur vision introuvable : %s (%v)", mm, err)
		}
		if _, err := os.Stat(mmPath); err != nil {
			return launchSpec{}, fmt.Errorf("projecteur vision introuvable : %s", mmPath)
		}
		llmArgs = append(llmArgs, "--mmproj", mmPath)
	}
	// Décodage spéculatif à modèle de draft (EAGLE-3, dFlash, dSpark, modèle
	// brouillon séparé, et parfois MTP quand la tête est fournie à part) : le .gguf
	// qui anticipe les jetons est choisi dans l'éditeur de preset et rangé dans la
	// clé MODEL_DRAFT, traduite ici en --model-draft et résolue comme le modèle
	// principal (nom simple cherché dans les dossiers déclarés, ou chemin absolu).
	// MTP est le plus souvent intégré au modèle → clé vide, pas de --model-draft.
	if md := strings.TrimSpace(cfg["MODEL_DRAFT"]); md != "" {
		mdPath, err := resolveServeModelPath(md)
		if err != nil {
			return launchSpec{}, fmt.Errorf("modèle de draft introuvable : %s (%v)", md, err)
		}
		if _, err := os.Stat(mdPath); err != nil {
			return launchSpec{}, fmt.Errorf("modèle de draft introuvable : %s", mdPath)
		}
		llmArgs = append(llmArgs, "--model-draft", mdPath)
	}
	// Raisonnement. Trois cas, et la nuance compte :
	//
	//   REASONING=on|auto|deepseek → --reasoning <valeur>
	//   REASONING=off              → --reasoning off  (interdiction EXPLICITE)
	//   clé absente                → aucun drapeau, le moteur fait son défaut
	//
	// L'interface écrivait « off » en EFFAÇANT la ligne, ce qui n'est pas du tout
	// la même chose : sans drapeau, llama-server suit le gabarit du modèle, et un
	// modèle à raisonnement raisonne. L'interrupteur affichait donc « désactivé »
	// pendant que le modèle réfléchissait quand même. Il faut le dire au moteur.
	if r := strings.TrimSpace(cfg["REASONING"]); r != "" {
		if reasoningActive(r) {
			// budget illimité par défaut (-1) : on laisse le modèle réfléchir jusqu'au
			// bout au lieu de le couper à 2048, ce qui tronquait la vraie réponse (la
			// réflexion atteignait le plafond et il ne restait plus de marge pour le
			// contenu). L'anti-boucle côté llm_client.go reste le garde-fou. NE PAS forcer 0 :
			// sur llama.cpp vanilla, 0 = "immediate end" → coupe tout le raisonnement
			// (le fork ik_llama.cpp l'ignore). Configurable via REASONING_BUDGET.
			llmArgs = append(llmArgs, "--reasoning", r, "--reasoning-budget", get("REASONING_BUDGET", "-1"))
		} else if binSupportsReasoningFlag(bin) {
			// Pas de budget ici : « off » suffit, et un budget sur un moteur qui
			// n'attend rien d'autre ne ferait qu'ajouter une occasion d'échouer.
			llmArgs = append(llmArgs, "--reasoning", "off")
		} else {
			// Vieux moteur (ou fork) qui ne connaît pas le drapeau : le lui passer
			// le ferait sortir en erreur au démarrage, donc boucler. On le dit et on
			// continue sans — mieux vaut un modèle qui réfléchit qu'un moteur mort.
			fmt.Fprintf(os.Stderr, "[ajean serve] ce moteur ne connaît pas --reasoning : impossible de désactiver le raisonnement\n")
		}
	}
	// API_KEY protège le serveur quand il est exposé sur internet : llama-server
	// exige alors l'en-tête "Authorization: Bearer <clé>". La clé est lue depuis
	// $AJEAN_HOME/.api_key en priorité (elle survit ainsi aux changements de preset
	// qui réécrivent config.env), avec config.env comme repli rétro-compatible.
	if k := readAPIKey(); k != "" {
		llmArgs = append(llmArgs, "--api-key", k)
	} else if k := cfg["API_KEY"]; k != "" {
		llmArgs = append(llmArgs, "--api-key", k)
	}
	// EXTRA_ARGS is appended verbatim, split like the shell would — quotes kept
	// together so a path with spaces stays one argument.
	llmArgs = append(llmArgs, splitArgs(cfg["EXTRA_ARGS"])...)

	// Compat llama.cpp récent : --mlock / --no-mmap ont été REMPLACÉS par
	// --load-mode. Sur un tel moteur, les anciens drapeaux le font mourir au
	// démarrage (boucle systemd). On traduit ici, une fois tous les arguments
	// réunis, sans que les presets aient à connaître la version du backend.
	llmArgs = reconcileLoadMode(llmArgs, bin)
	return launchSpec{
		Bin:   bin,
		Args:  llmArgs[1:],
		Dir:   AjeanHome(), // chemins relatifs d'EXTRA_ARGS (ex. --mmproj mmproj-F16.gguf)
		Env:   map[string]string{},
		Host:  argValue(llmArgs, "--host"),
		Port:  argValue(llmArgs, "--port"),
		Label: "llama.cpp model=" + filepath.Base(model),
	}, nil
}

// binHelp lit une fois l'aide du binaire, avec le même chemin de bibliothèques
// que le vrai lancement (setLibraryPath a déjà été appelé) : sans ça un moteur
// parfaitement valide échoue à s'exécuter (« libllama-common.so introuvable »)
// et on conclurait à tort qu'il ne gère pas tel drapeau. Renvoie le texte et un
// booléen « lisible » (false = aide illisible, ne rien en déduire).
func binHelp(bin string) (string, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// hideCmd : sans lui, ce « llama-server --help » ouvre une fenêtre de console
	// noire sous Windows (llama-server est une app console), qui clignote à chaque
	// (re)démarrage du moteur et à chaque changement de preset. No-op sous Unix.
	cmd := hideCmd(exec.CommandContext(ctx, bin, "--help"))
	// Env complet + chemin des bibliothèques voisines du binaire : utilisable
	// depuis le process web (qui n'a pas appelé setLibraryPath) sans polluer son
	// environnement. Sans ça, un moteur valide échoue sur « libllama introuvable »
	// et on conclurait à tort qu'il ne gère pas tel drapeau.
	cmd.Env = libraryPathEnv(filepath.Dir(bin))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil && out.Len() == 0 {
		return "", false
	}
	return out.String(), true
}

// binSupportsReasoningFlag dit si ce llama-server accepte « --reasoning ».
//
// Le drapeau est récent : les moteurs plus anciens, et certains forks, ne le
// connaissent pas et REFUSENT de démarrer sur un argument inconnu. Comme on ne
// l'ajoute que pour interdire le raisonnement, mieux vaut demander au binaire
// que parier : on lit son aide, une fois, au lancement du moteur.
//
// L'aide se lit avec le même chemin de bibliothèques que le vrai lancement
// (setLibraryPath a déjà été appelé) : sans ça un moteur parfaitement valide
// échoue à s'exécuter (« libllama-common.so introuvable ») et on conclurait à
// tort qu'il ne gère pas le drapeau.
func binSupportsReasoningFlag(bin string) bool {
	h, ok := binHelp(bin)
	if !ok {
		return false // aide illisible : on ne prend pas le risque
	}
	return strings.Contains(h, "--reasoning ")
}

// binSupportsLoadMode dit si ce llama-server attend la nouvelle syntaxe
// « --load-mode MODE » (refactor amont qui a REMPLACÉ --mlock / --no-mmap).
// Sur un tel moteur, passer les anciens drapeaux le fait sortir en erreur au
// démarrage → boucle systemd. Voir reconcileLoadMode.
func binSupportsLoadMode(bin string) bool {
	h, ok := binHelp(bin)
	return ok && strings.Contains(h, "--load-mode")
}

// reconcileLoadMode traduit les anciens drapeaux --mlock / --no-mmap vers le
// nouveau --load-mode QUAND le moteur ne connaît plus les anciens (llama.cpp
// récent). Les presets continuent de stocker --mlock / --no-mmap (portables
// d'un backend à l'autre, y compris les forks qui gardent l'ancienne syntaxe) ;
// on choisit ici la bonne forme pour CE binaire, au lancement.
//
// Correspondance (deux interrupteurs indépendants → une seule valeur) :
//
//	--mlock seul          → mmap+mlock  (mmap + résident : l'ancien sens de --mlock)
//	--no-mmap seul        → none        (pas de mmap)
//	--mlock + --no-mmap   → mlock       (pas de mmap + résident)
//	aucun des deux        → inchangé
func reconcileLoadMode(args []string, bin string) []string {
	// Court-circuit : sans ancien drapeau, rien à faire — et surtout on évite de
	// lancer « --help » pour rien à chaque démarrage du moteur.
	if !containsAny(args, "--mlock", "--no-mmap", "--load-mode", "-lm") {
		return args
	}
	return translateLoadMode(args, binSupportsLoadMode(bin))
}

// translateLoadMode contient la logique pure (testable sans binaire) : réécrit
// --mlock / --no-mmap en --load-mode quand supported vaut vrai ; sinon rend les
// arguments inchangés (moteur ancien ou fork qui garde l'ancienne syntaxe).
func translateLoadMode(args []string, supported bool) []string {
	if !supported {
		return downgradeLoadMode(args)
	}
	hasMlock, hasNoMmap, hasLoadMode := false, false, false
	kept := make([]string, 0, len(args))
	for _, a := range args {
		switch a {
		case "--mlock":
			hasMlock = true
		case "--no-mmap":
			hasNoMmap = true
		default:
			if a == "--load-mode" || a == "-lm" {
				hasLoadMode = true
			}
			kept = append(kept, a)
		}
	}
	if !hasMlock && !hasNoMmap {
		return args
	}
	// L'utilisateur a déjà posé un --load-mode explicite (EXTRA_ARGS) : il gagne ;
	// on s'est contenté de retirer les anciens drapeaux pour éviter le conflit
	// (« only the last one takes effect »).
	if hasLoadMode {
		return kept
	}
	mode := "mmap+mlock"
	switch {
	case hasMlock && hasNoMmap:
		mode = "mlock"
	case hasNoMmap:
		mode = "none"
	}
	return append(kept, "--load-mode", mode)
}

// downgradeLoadMode fait le chemin inverse pour un moteur ANCIEN (ou un fork)
// qui ne connaît pas --load-mode : l'éditeur écrit désormais --load-mode, et le
// passer tel quel à un tel moteur le ferait sortir en erreur au démarrage.
//
//	none        → --no-mmap
//	mlock       → --mlock --no-mmap
//	mmap+mlock  → --mlock
//	auto, mmap  → rien (comportement par défaut de l'ancien moteur)
//	dio         → rien (DirectIO inconnu de l'ancien moteur)
func downgradeLoadMode(args []string) []string {
	mode, found := "", false
	kept := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		if args[i] == "--load-mode" || args[i] == "-lm" {
			found = true
			if i+1 < len(args) {
				mode = strings.ToLower(args[i+1])
				i++
			}
			continue
		}
		kept = append(kept, args[i])
	}
	if !found {
		return args
	}
	add := func(flag string) {
		for _, a := range kept {
			if a == flag {
				return
			}
		}
		kept = append(kept, flag)
	}
	switch mode {
	case "none":
		add("--no-mmap")
	case "mlock":
		add("--mlock")
		add("--no-mmap")
	case "mmap+mlock":
		add("--mlock")
	case "dio":
		fmt.Fprintf(os.Stderr, "[ajean serve] ce moteur ne connaît pas --load-mode dio (DirectIO) : chargement par défaut\n")
	}
	return kept
}

func containsAny(args []string, needles ...string) bool {
	for _, a := range args {
		for _, n := range needles {
			if a == n {
				return true
			}
		}
	}
	return false
}

// argValue renvoie la valeur de la DERNIÈRE occurrence de flag (llama-server
// retient la dernière : EXTRA_ARGS peut surcharger --port).
func argValue(args []string, flag string) string {
	v := ""
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			v = args[i+1]
		}
	}
	return v
}

// resolveServeModelPath résout MODEL= pour LANCER le moteur. Différence avec
// resolveModelPath : un chemin absolu qui pointe sur un .gguf existant est
// accepté même hors des dossiers déclarés. L'allowlist protège des opérations
// destructrices pilotées depuis l'UI (suppression d'un fichier) ; l'appliquer à
// « ouvrir ce modèle en lecture » rendait la config éditée à la main
// inutilisable : MODEL=/home/moi/models/x.gguf renvoyait « dossier non
// autorisé », le moteur mourait en boucle, et le seul remède connu était
// d'aller déclarer le dossier dans l'interface web.
func resolveServeModelPath(name string) (string, error) {
	p, err := resolveModelPath(name)
	if err == nil {
		return p, nil
	}
	s := strings.TrimSpace(strings.Trim(strings.TrimSpace(name), `"`))
	if filepath.IsAbs(s) && strings.HasSuffix(strings.ToLower(s), ".gguf") {
		abs := filepath.Clean(s)
		if st, e := os.Stat(abs); e == nil && !st.IsDir() {
			return abs, nil
		}
	}
	return "", err
}

// isExl3Config : la configuration fait tourner le moteur ExLlama.
func isExl3Config(cfg map[string]string) bool { return cfg["ENGINE"] == "exl3" }

// serveExl3 : la branche ExLlama de `ajean serve`. Écrit la config et la clé de
// TabbyAPI puis lui passe la main (systemd supervise directement le moteur).
// exl3Launch écrit la config TabbyAPI et rend la commande (voir launchSpec).
func exl3Launch(cfg map[string]string) (launchSpec, error) {
	st, modelDir, err := exl3Preflight(cfg)
	if err != nil {
		return launchSpec{}, err
	}
	key := readAPIKey()
	if key == "" {
		key = strings.TrimSpace(cfg["API_KEY"])
	}
	if err := os.WriteFile(exl3ConfigFile(), []byte(exl3BuildConfig(cfg, modelDir, key)), 0o600); err != nil {
		return launchSpec{}, err
	}
	// TabbyAPI lit ses clés dans api_tokens.yml, dans son dossier
	tokens := filepath.Join(st.Dir, "api_tokens.yml")
	if key != "" {
		body := "api_key: " + yq(key) + "\nadmin_key: " + yq(key) + "\n"
		if err := os.WriteFile(tokens, []byte(body), 0o600); err != nil {
			return launchSpec{}, err
		}
	} else {
		_ = os.Remove(tokens)
	}
	_ = os.Setenv("CUDA_DEVICE_ORDER", "PCI_BUS_ID")
	// Les cartes du preset (EXL3_GPUS, vide = toutes) font foi. Ni la clé machine
	// CUDA_VISIBLE_DEVICES (réglée pour llama.cpp, souvent « 1,0 ») ni la valeur de
	// l'unité systemd ne s'appliquent : elles inversaient ou masquaient les cartes.
	// Une clé à part, car CUDA_VISIBLE_DEVICES est reportée d'un preset à l'autre
	// (softPreservedKeys) : l'écrire ici cassait ensuite les presets llama.cpp.
	if v := strings.TrimSpace(cfg["EXL3_GPUS"]); v != "" {
		_ = os.Setenv("CUDA_VISIBLE_DEVICES", v)
	} else {
		_ = os.Unsetenv("CUDA_VISIBLE_DEVICES")
	}
	if v := strings.TrimSpace(cfg["EXL3_VISION_DEVICE"]); strings.HasPrefix(v, "cuda:") && cfg["EXL3_VISION"] == "1" {
		_ = os.Setenv("TABBY_VISION_DEVICE", v)
	} else {
		_ = os.Unsetenv("TABBY_VISION_DEVICE")
	}
	_ = os.Setenv("PYTHONUNBUFFERED", "1")
	port := strconv.Itoa(exl3Num(cfg, "PORT", 8080))
	py := exl3Python(st.Dir)
	return launchSpec{
		Bin:  py,
		Args: []string{"main.py", "--config", exl3ConfigFile()},
		Dir:  st.Dir,
		Env:  map[string]string{},
		Host: firstNonEmpty(cfg["HOST"], "0.0.0.0"),
		Port: port,
		Label: fmt.Sprintf("ExLlama (exllamav3 %s, TabbyAPI %s) model=%s",
			firstNonEmpty(st.Exllama, "?"), shortSHA(st.Commit), filepath.Base(modelDir)),
	}, nil
}

// exl3Preflight : ce sans quoi le moteur ne peut pas démarrer.
func exl3Preflight(cfg map[string]string) (*exl3Installed, string, error) {
	st := exl3ReadInstalled()
	if st == nil {
		return nil, "", fmt.Errorf("moteur ExLlama non installé : installez-le depuis la section Moteur")
	}
	dir, err := exl3ResolveModel(cfg["EXL3_MODEL"])
	if err != nil {
		return nil, "", err
	}
	if !isFile(filepath.Join(dir, "config.json")) {
		return nil, "", fmt.Errorf("%s n'est pas un modèle (config.json absent)", dir)
	}
	return st, dir, nil
}

// exl3BuildConfig écrit la config de TabbyAPI pour ce preset (YAML, valeurs
// citées en JSON, qui est du YAML valide).
func exl3BuildConfig(cfg map[string]string, modelDir, apiKey string) string {
	get := func(k, def string) string {
		if v := strings.TrimSpace(cfg[k]); v != "" {
			return v
		}
		return def
	}
	ctx := exl3Num(cfg, "CTX", 32768)
	cache := max(exl3Num(cfg, "EXL3_CACHE", ctx), ctx)
	// exllamav3 alloue le cache par pages de 256 jetons
	cache = (cache + 255) / 256 * 256
	mode := get("EXL3_CACHE_MODE", "8,8")
	if !exl3CacheModes[mode] {
		mode = "8,8"
	}
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	w("# Écrit par ajean à chaque lancement (preset ENGINE=exl3) : ne pas modifier.")
	w("network:")
	w("  host: %s", yq(get("HOST", "0.0.0.0")))
	w("  port: %d", exl3Num(cfg, "PORT", 8080))
	w("  disable_auth: %t", apiKey == "")
	w("  send_tracebacks: false")
	w("  web_ui: false")
	w("logging:")
	w("  log_prompt: false")
	w("  log_generation_params: false")
	w("  log_requests: false")
	w("model:")
	w("  model_dir: %s", yq(filepath.Dir(modelDir)))
	w("  model_name: %s", yq(filepath.Base(modelDir)))
	w("  max_seq_len: %d", ctx)
	w("  cache_size: %d", cache)
	w("  cache_mode: %s", yq(mode))
	w("  chunk_size: %d", exl3Num(cfg, "EXL3_CHUNK", 2048))
	w("  max_batch_size: %d", exl3Num(cfg, "EXL3_BATCH", 1))
	tp := cfg["EXL3_TP"] == "1"
	w("  tensor_parallel: %t", tp)
	if tp && cfg["EXL3_TP_BACKEND"] == "nccl" {
		w("  tensor_parallel_backend: nccl")
	}
	if split := exl3FloatList(cfg["EXL3_GPU_SPLIT"]); len(split) > 0 {
		w("  gpu_split_auto: false")
		w("  gpu_split: %s", yamlFloats(split))
	} else {
		w("  gpu_split_auto: true")
	}
	if r := exl3FloatList(cfg["EXL3_RESERVE"]); len(r) > 0 {
		w("  autosplit_reserve: %s", yamlFloats(r))
	}
	vision := cfg["EXL3_VISION"] == "1"
	w("  vision: %t", vision)
	vdev := strings.TrimSpace(cfg["EXL3_VISION_DEVICE"])
	w("  vision_offload: %t", vision && vdev == "cpu")
	w("  reasoning: true")
	if r := strings.TrimSpace(cfg["REASONING"]); r != "" && !reasoningActive(r) {
		// raisonnement coupé : imposé au gabarit, quel que soit le client
		w("  template_vars_force:")
		w("    enable_thinking: false")
	}
	if n := exl3Num(cfg, "REASONING_BUDGET", 0); n > 0 {
		w("  reasoning_budget_tokens: %d", n)
	}
	w("draft_model:")
	switch d := get("EXL3_DRAFT", "disabled"); d {
	case "mtp", "ngram":
		w("  draft_mode: %s", d)
		w("  draft_num_tokens: %d", exl3Num(cfg, "EXL3_DRAFT_TOKENS", 3))
	case "model":
		if dd, err := exl3ResolveModel(cfg["EXL3_DRAFT_MODEL"]); err == nil {
			w("  draft_mode: model")
			w("  draft_model_dir: %s", yq(filepath.Dir(dd)))
			w("  draft_model_name: %s", yq(filepath.Base(dd)))
			w("  draft_cache_mode: %s", yq(mode))
			w("  draft_num_tokens: %d", exl3Num(cfg, "EXL3_DRAFT_TOKENS", 4))
		} else {
			w("  draft_mode: disabled")
		}
	default:
		w("  draft_mode: disabled")
	}
	// MTP adaptatif : la longueur des brouillons suit le taux d'acceptation
	// (plafond = jetons anticipés). Mesuré moins bon à 4 utilisateurs : en option.
	if d := get("EXL3_DRAFT", "disabled"); cfg["EXL3_DRAFT_DYNAMIC"] == "1" && (d == "mtp" || d == "model") {
		w("  dynamic_draft: true")
		if c, err := strconv.ParseFloat(strings.TrimSpace(cfg["EXL3_DRAFT_CONFIDENCE"]), 64); err == nil && c > 0 && c < 1 {
			w("  draft_confidence: %s", strconv.FormatFloat(c, 'f', -1, 64))
		}
	}
	// Recopie : un passage du contexte répété mot pour mot avance de N jetons d'un coup.
	if n := exl3Num(cfg, "EXL3_HYBRID", 0); n > 0 {
		w("  hybrid_draft_tokens: %d", n)
	}
	return b.String()
}

// exl3Python : le Python de l'environnement de TabbyAPI.
func exl3Python(dir string) string {
	if runtime.GOOS == "windows" {
		return filepath.Join(dir, ".venv", "Scripts", "python.exe")
	}
	return filepath.Join(dir, ".venv", "bin", "python")
}

func exl3Home() string { return filepath.Join(AjeanHome(), "exl3") }

func exl3ConfigFile() string { return filepath.Join(exl3Home(), "config-ajean.yml") }

// exl3QuantLabel : « EXL3 4.0 » pour les étiquettes de preset.
func exl3QuantLabel(cfg map[string]string) string {
	dir, err := exl3ResolveModel(cfg["EXL3_MODEL"])
	if err == nil {
		if m, ok := exl3ReadModel(dir); ok && m.BPW > 0 {
			return "EXL3 " + strconv.FormatFloat(m.BPW, 'f', -1, 64)
		}
	}
	return "EXL3"
}

func yq(s string) string { return strconv.Quote(s) }

// exl3Num : un entier positif du preset, ou def.
func exl3Num(cfg map[string]string, key string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(cfg[key])); err == nil && n > 0 {
		return n
	}
	return def
}

// exl3FloatList : « 5.5, 11 » → [5.5 11] (vide ou illisible → nil).
func exl3FloatList(s string) []float64 {
	var out []float64
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' || r == ' ' }) {
		f, err := strconv.ParseFloat(strings.TrimSpace(p), 64)
		if err != nil || f < 0 {
			return nil
		}
		out = append(out, f)
	}
	return out
}

func yamlFloats(v []float64) string {
	s := make([]string, len(v))
	for i, f := range v {
		s[i] = strconv.FormatFloat(f, 'f', -1, 64)
	}
	return "[" + strings.Join(s, ", ") + "]"
}

func shortSHA(s string) string {
	if len(s) > 7 {
		return s[:7]
	}
	return s
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return s
		}
	}
	return ""
}

// isMoeConfig : la configuration active fait tourner le moteur MoE.
func isMoeConfig(cfg map[string]string) bool { return cfg["ENGINE"] == "moe" }

func isCloudConfig(cfg map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(cfg["CLOUD"]), "modal")
}

// externalServiceOf renvoie le service externe que la config demande ("" = aucun).
func externalServiceOf(cfg map[string]string) string {
	if !isExternalConfig(cfg) {
		return ""
	}
	return strings.TrimSpace(cfg[extKeyService])
}

const extKeyService = "EXTERNAL_SERVICE"

// resolveModelPath transforme une valeur MODEL= (nom de fichier OU chemin
// absolu) en chemin absolu vers un .gguf. Un chemin absolu n'est accepté que
// s'il est dans un des dossiers déclarés — sinon l'UI web deviendrait un moyen
// de lire/supprimer n'importe quel fichier de la machine. Un simple nom de
// fichier est cherché dans chaque dossier, AJEAN_HOME en premier.
func resolveModelPath(name string) (string, error) {
	s := strings.TrimSpace(strings.Trim(strings.TrimSpace(name), `"`))
	if s == "" {
		return "", fmt.Errorf("nom de modèle invalide")
	}
	if !strings.HasSuffix(strings.ToLower(s), ".gguf") {
		return "", fmt.Errorf("le modèle doit être un fichier .gguf")
	}
	if filepath.IsAbs(s) {
		abs := filepath.Clean(s)
		for _, d := range modelDirs() {
			if pathWithin(d, abs) {
				return abs, nil
			}
		}
		return "", fmt.Errorf("dossier non autorisé : %s — ajoute-le dans « Dossiers de modèles »", filepath.Dir(abs))
	}
	base := baseName(s)
	if base == "" || base == "." {
		return "", fmt.Errorf("nom de modèle invalide")
	}
	for _, d := range modelDirs() {
		p := filepath.Join(d, base)
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return filepath.Join(modelsDir(), base), nil // introuvable : l'appelant décidera
}

// exl3Installed : ce qui est en place (engine.json).
type exl3Installed struct {
	Dir         string `json:"dir"`
	Commit      string `json:"commit"`
	Date        string `json:"date,omitempty"` // date du commit TabbyAPI
	CUDA        string `json:"cuda"`
	Exllama     string `json:"exllamav3,omitempty"`
	Torch       string `json:"torch,omitempty"`
	InstalledAt int64  `json:"installed_at"`
}

func exl3ReadInstalled() *exl3Installed {
	b, err := os.ReadFile(exl3StateFile())
	if err != nil {
		return nil
	}
	var st exl3Installed
	if json.Unmarshal(b, &st) != nil || st.Dir == "" {
		return nil
	}
	if !isFile(exl3Python(st.Dir)) || !isFile(filepath.Join(st.Dir, "main.py")) {
		return nil
	}
	return &st
}

// exl3ResolveModel : EXL3_MODEL est un chemin absolu ou un nom de dossier cherché
// dans les dossiers de modèles (et leur sous-dossier exl3).
func exl3ResolveModel(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("aucun modèle choisi (EXL3_MODEL)")
	}
	if filepath.IsAbs(name) {
		if isDir(name) {
			return name, nil
		}
		return "", fmt.Errorf("dossier du modèle introuvable : %s", name)
	}
	for _, d := range modelDirs() {
		for _, p := range []string{filepath.Join(d, "exl3", name), filepath.Join(d, name)} {
			if isDir(p) {
				return p, nil
			}
		}
	}
	return "", fmt.Errorf("modèle %s introuvable dans les dossiers de modèles", name)
}

// exl3CacheModes : les modes de cache acceptés (clé EXL3_CACHE_MODE).
var exl3CacheModes = map[string]bool{"FP16": true, "8,8": true, "6,6": true, "4,4": true, "8,6": true, "6,4": true, "Q8": true, "Q6": true, "Q4": true}

// exl3ReadModel : dir est-il un modèle EXL3 (config.json avec une
// quantization_config exl3) ? Lit aussi le débit et la présence de la vision.
func exl3ReadModel(dir string) (exl3Model, bool) {
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		return exl3Model{}, false
	}
	var c struct {
		Quant struct {
			Method string  `json:"quant_method"`
			BPW    float64 `json:"bits"`
		} `json:"quantization_config"`
		Vision json.RawMessage `json:"vision_config"`
	}
	if json.Unmarshal(b, &c) != nil || !strings.EqualFold(c.Quant.Method, "exl3") {
		return exl3Model{}, false
	}
	m := exl3Model{Name: filepath.Base(dir), Path: dir, BPW: c.Quant.BPW}
	// la vision n'est utilisable que si ses poids ont été gardés dans le quant
	// (config.json la décrit même quand ils ont été retirés)
	if len(c.Vision) > 0 && string(c.Vision) != "null" {
		m.Vision = exl3HasVisionWeights(dir)
	}
	entries, _ := os.ReadDir(dir)
	var size int64
	for _, e := range entries {
		if fi, err := e.Info(); err == nil && !e.IsDir() {
			size += fi.Size()
		}
	}
	m.SizeGB = float64(size) / 1e9
	return m, true
}

type exl3Model struct {
	Name   string  `json:"name"`
	Path   string  `json:"path"`
	SizeGB float64 `json:"size_gb"`
	BPW    float64 `json:"bpw,omitempty"`
	Vision bool    `json:"vision,omitempty"`
}

func exl3StateFile() string { return filepath.Join(exl3Home(), "engine.json") }

// modelDirs renvoie tous les dossiers où chercher un .gguf : models/ d'abord
// (il reste la destination des téléchargements), puis les dossiers ajoutés.
func modelDirs() []string {
	return append([]string{modelsDir()}, extraModelDirs()...)
}

// pathWithin dit si le fichier p est dans le dossier dir (ou un sous-dossier).
func pathWithin(dir, p string) bool {
	d, f := normDir(dir), normDir(p)
	return f == d || strings.HasPrefix(f, d+"/")
}

// exl3HasVisionWeights : des tenseurs de l'encodeur d'images figurent dans les
// poids (index des tranches, ou en-tête d'un fichier unique).
func exl3HasVisionWeights(dir string) bool {
	isVis := func(k string) bool { return strings.Contains(k, "visual") || strings.Contains(k, "vision_tower") }
	if b, err := os.ReadFile(filepath.Join(dir, "model.safetensors.index.json")); err == nil {
		var idx struct {
			WeightMap map[string]string `json:"weight_map"`
		}
		if json.Unmarshal(b, &idx) == nil {
			for k := range idx.WeightMap {
				if isVis(k) {
					return true
				}
			}
		}
		return false
	}
	f, err := os.Open(filepath.Join(dir, "model.safetensors"))
	if err != nil {
		return false
	}
	defer f.Close()
	var n [8]byte
	if _, err := io.ReadFull(f, n[:]); err != nil {
		return false
	}
	size := int64(n[0]) | int64(n[1])<<8 | int64(n[2])<<16 | int64(n[3])<<24 | int64(n[4])<<32 | int64(n[5])<<40
	if size <= 0 || size > 64<<20 {
		return false
	}
	hdr := make([]byte, size)
	if _, err := io.ReadFull(f, hdr); err != nil {
		return false
	}
	var m map[string]json.RawMessage
	if json.Unmarshal(hdr, &m) != nil {
		return false
	}
	for k := range m {
		if isVis(k) {
			return true
		}
	}
	return false
}

// extraModelDirs renvoie les dossiers déclarés par l'utilisateur (base puis
// variable d'environnement), nettoyés et dédoublonnés, models/ exclu.
func extraModelDirs() []string {
	var raw []string
	getJSON(bkState, "model_dirs", &raw)
	if v := os.Getenv("AJEAN_MODEL_DIRS"); v != "" {
		raw = append(raw, strings.Split(v, modelDirsListSep())...)
	}
	return dedupDirs(raw, modelsDir())
}

// modelDirsListSep sépare les dossiers dans $AJEAN_MODEL_DIRS (':' sous Unix,
// ';' sous Windows, comme le PATH).
func modelDirsListSep() string {
	if runtime.GOOS == "windows" {
		return ";"
	}
	return ":"
}

// dedupDirs nettoie une liste de chemins : vides retirés, chemins absolus
// normalisés, doublons et `skip` écartés (comparaison insensible à la casse
// sous Windows, comme le système de fichiers).
func dedupDirs(list []string, skip string) []string {
	seen := map[string]bool{normDir(skip): true}
	out := []string{}
	for _, d := range list {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		abs, err := filepath.Abs(d)
		if err != nil {
			continue
		}
		abs = filepath.Clean(abs)
		k := normDir(abs)
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, abs)
	}
	return out
}
