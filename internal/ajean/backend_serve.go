package ajean

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

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
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// hideCmd : sans lui, ce « llama-server --help » ouvre une fenêtre de console
	// noire sous Windows (llama-server est une app console), qui clignote à chaque
	// (re)démarrage du moteur et à chaque changement de preset. No-op sous Unix.
	cmd := hideCmd(exec.CommandContext(ctx, bin, "--help"))
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	if err := cmd.Run(); err != nil && out.Len() == 0 {
		return false // aide illisible : on ne prend pas le risque
	}
	return strings.Contains(out.String(), "--reasoning ")
}

// cmdServe replaces the historic start.sh: read config.env, build the
// llama-server invocation, and exec it (replacing this process so systemd
// supervises llama-server directly).
func cmdServe(args []string) error {
	cfg := ReadConfig()
	bin := cfg["BIN"]
	if bin == "" {
		return fmt.Errorf("BIN non défini — lance « ajean edit »")
	}
	model := cfg["MODEL"]
	if model == "" {
		return fmt.Errorf("MODEL non défini — lance « ajean edit »")
	}
	// MODEL vaut soit un simple nom de fichier (le .gguf vit dans AJEAN_HOME ou
	// dans un dossier déclaré — disque externe…), soit un chemin absolu. Sous
	// systemd/launchd le WorkingDirectory vaut AJEAN_HOME, donc le relatif tombait
	// juste ; lancé depuis une app de bureau, le répertoire courant est « / » et
	// llama-server ne trouvait rien. On résout donc explicitement, quel que soit
	// le contexte de lancement.
	resolved, err := resolveServeModelPath(model)
	if err != nil {
		return err
	}
	model = resolved
	if _, err := os.Stat(model); err != nil {
		return fmt.Errorf("modèle introuvable : %s", model)
	}
	// Modèle découpé en tranches : llama-server ouvre les suivantes tout seul, mais
	// s'il en manque une il démarre puis meurt sur un tenseur introuvable — message
	// incompréhensible, et systemd relance en boucle. On le dit ici, en clair.
	if missing := shardFamilyMissing(filepath.Dir(model), filepath.Base(model)); len(missing) > 0 {
		return fmt.Errorf("modèle incomplet : il manque %s dans %s — ce modèle tient en %d fichiers, télécharge-les tous",
			strings.Join(missing, ", "), filepath.Dir(model), len(shardFamily(filepath.Base(model))))
	}
	if !filepath.IsAbs(bin) {
		bin = filepath.Join(AjeanHome(), bin)
	}
	// Le moteur précompilé s'installe dans un dossier versionné : un preset écrit
	// avant une mise à jour pointe sur une release qui n'existe plus. On le fait
	// suivre au moteur courant plutôt que d'échouer en 127.
	bin = prebuiltResolveBin(bin)

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
			return fmt.Errorf("projecteur vision introuvable : %s (%v)", mm, err)
		}
		if _, err := os.Stat(mmPath); err != nil {
			return fmt.Errorf("projecteur vision introuvable : %s", mmPath)
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
			return fmt.Errorf("modèle de draft introuvable : %s (%v)", md, err)
		}
		if _, err := os.Stat(mdPath); err != nil {
			return fmt.Errorf("modèle de draft introuvable : %s", mdPath)
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

	// Working dir = AJEAN_HOME so relative paths in EXTRA_ARGS (e.g. --mmproj
	// mmproj-F16.gguf) still resolve.
	_ = os.Chdir(AjeanHome())

	fmt.Fprintf(os.Stderr, "[ajean serve] %s  model=%s  port=%s\n",
		bin, filepath.Base(model), get("PORT", "8080"))

	// Hand off to the llama-server process. On Unix this replaces the current
	// process (exec); on Windows it runs as a child and waits. See sys_platform_*.go.
	return execServer(bin, llmArgs)
}
