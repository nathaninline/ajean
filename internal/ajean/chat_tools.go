package ajean

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	toolDefaultTimeout = 30
	toolMaxTimeout     = 300
	toolMaxOutput      = 8000 // characters of stdout/stderr returned to the model
)

// baseSystemPrompt is the always-on system preamble. Structured like pi's
// proven prompt (identity → method → guidelines → concision → date): a concrete,
// procedural prompt gives the model rails so it stops deliberating forever
// ("Wait, let me check… Wait, I'll just run it…") and commits to an action.
// The per-tool "Outil disponible" sections live in machine/skills prompts so
// they only appear when the matching feature is on.
func baseSystemPrompt(caps Caps) string {
	hasMem := caps.Mem != MemOff
	// No tool access at all → no agentic preamble. A plain chat model told to
	// "call tools immediately" hallucinates textual tool calls (e.g.
	// default_api:bash) that leak into the answer. Let the user's own system
	// prompt stand alone. (Internet requiert l'agent, donc pas testé ici.)
	if !caps.Agent && !hasMem {
		return ""
	}
	var b strings.Builder
	// Prompt VOLONTAIREMENT court. Un préambule verbeux (longue liste de
	// « guidelines », surtout des méta-instructions sur la réflexion) fait
	// sur-raisonner les modèles à reasoning (Qwen3) : ils émettent leur <think>
	// puis le token de fin SANS appeler d'outil (~25-45 % de tours « morts »
	// mesurés). Une version courte et directe ramène ça à 0 %. NE PAS regonfler.
	// L'assistant s'appelle « Jean » ; « AJEAN » est l'APP dans laquelle il tourne
	// (l'IA locale de Alice). Le modèle recopie la casse d'ici quand il se
	// présente, d'où « Jean » et « AJEAN » écrits tels quels. Éviter « real tools »,
	// qui sonnait bizarre à l'oral (« je fonctionne avec de vrais outils »).
	b.WriteString("You are Jean, the assistant inside AJEAN, an AI app that runs on this machine. You can act on it directly through your tools.")
	if caps.Mem == MemAlways {
		b.WriteString(" You evolve with every conversation: you actively maintain a persistent memory so nothing useful is lost between sessions.")
	}
	// PAS de catalogue d'outils ici : leurs schémas, envoyés dans la même requête,
	// les décrivent déjà un par un. Les réénumérer coûtait ~120 tokens à chaque
	// tour pour répéter ce que le modèle a juste à côté. Ne restent que les
	// consignes que les schémas ne portent pas — le shell utilisé, et les
	// politiques d'usage.
	if caps.Agent {
		// Le shell de la machine CIBLE (cmd.exe pour un poste Windows), pas celui du
		// serveur : sinon le modèle écrit du bash là où tourne cmd.exe (et inversement).
		b.WriteString("\n\nThe shell is " + agentTargetShellName() + ": use its syntax.\n")
	} else {
		b.WriteString("\n\n")
	}
	// Politique d'usage de la mémoire selon le mode.
	switch caps.Mem {
	case MemAlways:
		b.WriteString("\nManaging your memory is part of the job, not optional:\n")
		b.WriteString("- Save anything worth keeping (a preference, fact, decision, how-to) with mem_add, or mem_edit to update a page — on your own, without being asked.\n")
		b.WriteString("- Before any task or answer, call mem_search first, then mem_read the best page — even for trivial-seeming questions or ones with new specifics (a name, a value): your saved method still applies, only the parameter changes. A tool check never replaces this; memory may hold context the tools won't reveal.\n")
		b.WriteString("- Keep memory tidy: many small focused pages (one topic each, kept short and logical) rather than a few giant ones you'll struggle to read. mem_edit the right page instead of duplicating; split a page that grew too long; mem_delete what's wrong, obsolete, or merged elsewhere.\n")
	case MemOnDemand:
		b.WriteString("\nMemory is ON-DEMAND: you have the mem_* tools but do NOT read or write memory on your own. Call mem_search/mem_read only when the user explicitly asks you to recall or look something up, and mem_add/mem_edit only when the user explicitly asks you to remember something. Otherwise leave memory untouched and answer directly.\n")
	}
	if caps.Agent {
		// La règle « write, jamais echo/cat » est INDISPENSABLE (cmd.exe massacre
		// les guillemets imbriqués) mais elle vit maintenant dans les schémas de
		// write et bash, là où elle s'applique. Elle était écrite trois fois.
		b.WriteString("For anything about the system or files, use bash instead of guessing. Act immediately — call the right tool, then answer. Never end your turn after only thinking. Be concise.\n")
		// Un lien Markdown ordinaire, comme dans n'importe quel chat : l'UI en fait
		// un téléchargement (voir /api/chat/file). Aucun outil ni syntaxe spéciale
		// à connaître pour le modèle — juste [texte](chemin).
		b.WriteString("To give the user a file, link it in Markdown with its path relative to your working directory — [the report](report.pdf) — which downloads it. A raw server path is useless: they read you in a browser.\n")
		// Auto-planification : le modèle a les outils task_* (schémas joints). On ne
		// répète pas leur usage, juste QUE la capacité existe et QUAND s'en servir —
		// sinon il ne penserait jamais à se planifier quoi que ce soit.
		b.WriteString("You can also schedule work for yourself: task_create sets a future/recurring job (reminders, watches, cleanups), task_list/task_update/task_delete manage them. Only for something that must recur or happen later, not a one-off you can do now.\n")
		// La consigne « garder les scripts dans le dossier scripts, pas le workspace »
		// vit dans le briefing machine ; ici on n'ajoute que ce qu'elle ne dit pas —
		// qu'un tel script peut tourner en tâche SANS modèle.
		b.WriteString("A script kept in your scripts folder can be scheduled to run on its own with no model via task_create's 'script'.\n")
	}
	if caps.Internet {
		// Le catalogue des outils web est parti dans leurs schémas ; ne reste ici
		// que l'ordre d'appel, que les schémas pris isolément ne disent pas.
		year := time.Now().Format("2006")
		b.WriteString("\nWeb: web_open first, then web_read/web_grep on it.\n")
		b.WriteString("Your training data is stale. For ANY question about recent/latest/current things (releases, versions, news, prices, scores, 'since when') call web_search BEFORE writing any date or version, and match what you actually read.\n")
		b.WriteString("Today is in " + year + ". If a query needs a year use ONLY " + year + ", never a remembered past year like " + prevYear(year) + " — it biases results toward stale pages; better still, omit the year. Don't hedge ('probably') about a fact a tool can verify — search instead.\n")
	}
	if caps.Agent {
		if line := mcpPromptLine(); line != "" {
			b.WriteString(line)
		}
	}
	b.WriteString("\nDate: " + time.Now().Format("2006-01-02"))
	return b.String()
}

// prevYear returns the year before the given "2006"-formatted year string, used
// to name explicitly the stale year the model must NOT put in search queries.
func prevYear(year string) string {
	n, err := strconv.Atoi(year)
	if err != nil {
		return year
	}
	return strconv.Itoa(n - 1)
}

// machineSystemPrompt returns a short briefing about the host the model is
// running on, so that when machine access is enabled it knows *which* machine
// run_shell acts upon (and doesn't claim it has no access to "your PC").
// Returns "" when machine access is off.
func machineSystemPrompt(caps Caps) string {
	if !caps.Agent {
		return ""
	}
	// Cible = un poste distant : bash/write/edit s'exécutent LÀ-BAS, pas sur ce
	// serveur. Le modèle doit le savoir explicitement, sinon il croit agir sur
	// l'hôte du serveur et se trompe de machine.
	if tgt, ok := nodeTargetMetaGet(); ok {
		var b strings.Builder
		b.WriteString("Machine: you are operating on a REMOTE node named " + tgt.name)
		if tgt.os != "" {
			b.WriteString(" (" + tgt.os + ")")
		}
		b.WriteString(". Your bash, write and edit tools run on THAT machine — a different computer than this server.")
		if strings.HasPrefix(strings.ToLower(tgt.os), "windows") {
			b.WriteString(" Its shell is cmd.exe: use cmd syntax (never bash idioms like ls, 2>nul, single quotes, or 'cmd //c'). To create a file, use the write tool, never echo/type into it.")
		}
		if tgt.root != "" {
			b.WriteString(" File paths in write/edit resolve inside its working folder " + tgt.root + "; read/write are confined there.")
		}
		// Le poste peut tourner en compte de service (Windows: LocalSystem) : les
		// variables d'environnement personnelles (%USERPROFILE%, $HOME) ne désignent
		// PAS forcément l'utilisateur interactif. Utiliser des chemins absolus.
		b.WriteString(" It may run as a background service account, so %USERPROFILE%/$HOME may not point to the interactive user — prefer absolute paths (e.g. C:\\Users\\<name>\\...).")
		if !tgt.connected {
			b.WriteString(" ⚠ It is currently OFFLINE: those tools will fail until it reconnects. Tell the user instead of trying repeatedly.")
		}
		return b.String()
	}
	host, _ := os.Hostname()
	if host == "" {
		host = "unknown"
	}
	who := ""
	if u, err := user.Current(); err == nil {
		who = u.Username
	}
	cwd := agentWorkspace()

	var b strings.Builder
	b.WriteString(fmt.Sprintf("Machine: host=%s, %s/%s", host, runtime.GOOS, runtime.GOARCH))
	if who != "" {
		b.WriteString(", user=" + who)
	}
	if cwd != "" {
		b.WriteString(", cwd=" + cwd)
	}
	b.WriteString(".")
	if cwd != "" {
		b.WriteString(" This is your working folder: relative paths in write/edit/bash resolve here, and it is the default place for scratch work — notes, outputs, downloads, a clone or a test. But it is DISPOSABLE: a cleanup or a test can wipe it, so never keep anything important here. Any script you want to KEEP (or schedule), write it into your scripts folder " + scriptsDir() + " instead — a separate folder a workspace wipe won't touch; you write and run scripts there normally. Do NOT install or write files into system directories such as /usr/local/bin, /usr, /bin or /etc: those need root and are not yours. Only use an absolute path outside this folder (except your scripts folder) when the user explicitly named that location.")
		// memory reste réservé à ses outils : le modèle essaie parfois de `cat` le
		// dossier memory, on lui dit explicitement de ne pas le faire (bash/write/edit
		// le refuseront de toute façon).
		b.WriteString(" The memory folder is OFF-LIMITS to bash, write and edit (those tools will refuse) — always use the mem_* tools for it.")
	}
	return b.String()
}

// machineMgmtSystemPrompt returns a short briefing that makes the model aware it
// can manage the postes (other machines) itself. Returns "" unless machine
// management is enabled AND the agent has its tools — so it adds NOTHING to the
// current prompt when the feature is off. Kept short on purpose (see the
// anti-verbose note in baseSystemPrompt).
func machineMgmtSystemPrompt(caps Caps) string {
	if !caps.Agent || !machinesEnabled() {
		return ""
	}
	var b strings.Builder
	b.WriteString("You manage the postes (other machines) yourself. machines_list shows the paired postes and your current target; machines_use switches which machine your bash/write/edit run on (a poste slug, or \"local\" for this server).")
	b.WriteString(" To add a new machine: run `ajean postes pair` with bash (on this local server) to get a pairing code and a ready-to-run `ajean remote install ...` line, then run that line on the target machine over ssh (you have ssh through bash — you can also scan the LAN to find machines). Once it connects it shows up in machines_list; switch to it with machines_use.")
	b.WriteString(" Do this on your own when it helps, without asking first. `ajean postes ...` commands only work while your target is local.")
	return b.String()
}

// runShell executes a command via the platform shell (bash -c on Unix, cmd /C
// on Windows — see newShellCmd in sys_platform_*.go) with a clamped timeout,
// returning a single string formatted "exit: N\n\nstdout:\n...\n\nstderr:\n..."
// truncated to keep tool output bounded.
//
// ⚠️ parent est le contexte DU TOUR : c'est lui qui rend le bouton stop utile.
// La commande naissait auparavant d'un context.Background(), donc arrêter la
// génération n'arrêtait rien du tout — le tour restait bloqué jusqu'au bout du
// délai (5 minutes au maximum), bouton stop sans effet.
func runShell(parent context.Context, command string, timeoutSec int) string {
	// Accès réservé aux outils : memory et scripts ne sont JAMAIS touchés au shell
	// (ni lus, ni écrits, ni listés) — uniquement via mem_* et script_*.
	if msg := guardToolOnlyCommand(command); msg != "" {
		return msg
	}
	// Garde-fou : refuse une commande qui viserait à supprimer un dossier protégé
	// (scripts, mémoire, presets, base, racine des données). Voir chat_scripts.go.
	if msg := guardDestructive(command); msg != "" {
		return msg
	}
	if timeoutSec <= 0 {
		timeoutSec = toolDefaultTimeout
	}
	if timeoutSec > toolMaxTimeout {
		timeoutSec = toolMaxTimeout
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeoutSec)*time.Second)
	defer cancel()
	cmd, cleanup := newShellCmd(ctx, command)
	defer cleanup()
	// Le shell démarre dans le workspace, pas dans le dossier d'où ajean a été
	// lancé : un `> notes.txt` du modèle ne doit pas atterrir sur le Bureau.
	//
	// Le dossier est résolu UNE fois par process (agentWorkspace), donc s'il
	// disparaît ensuite — l'utilisateur fait le ménage, ou le modèle lui-même le
	// supprime — toutes les commandes suivantes échouaient sur un « chdir : no
	// such file or directory » incompréhensible, et ce jusqu'au redémarrage. On
	// le recrée au besoin, et à défaut on démarre là où on peut plutôt que de
	// tout refuser.
	if ws := agentWorkspace(); ws != "" {
		if err := os.MkdirAll(ws, 0o755); err == nil {
			cmd.Dir = ws
		}
	}
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	// ⚠️ WaitDelay borne l'attente APRÈS la fin (ou la mise à mort) du process.
	// Sans elle, Wait attend que les tubes de sortie soient fermés — donc que
	// TOUS ceux qui les tiennent aient disparu, petits-enfants compris. Une
	// commande du genre « ./serveur & » rend la main tout de suite mais laisse
	// un process en arrière-plan accroché aux tubes : runShell ne revenait alors
	// JAMAIS, ni au délai, ni au stop. Le tour restait bloqué à vie, et la seule
	// issue connue était de redémarrer ajean-ui.
	cmd.WaitDelay = 2 * time.Second
	err := cmd.Run()
	switch {
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return fmt.Sprintf("[timeout après %ds]", timeoutSec)
	case errors.Is(parent.Err(), context.Canceled):
		return "[commande interrompue]"
	}
	exit := 0
	if err != nil {
		if ee, ok := err.(*exec.ExitError); ok {
			exit = ee.ExitCode()
		} else {
			return fmt.Sprintf("[erreur: %v]", err)
		}
	}
	out := tailRunes(stdout.String(), toolMaxOutput)
	errOut := tailRunes(stderr.String(), toolMaxOutput)
	parts := []string{fmt.Sprintf("exit: %d", exit)}
	if out != "" {
		parts = append(parts, "stdout:\n"+out)
	}
	if errOut != "" {
		parts = append(parts, "stderr:\n"+errOut)
	}
	return strings.Join(parts, "\n\n")
}

// shellName is the shell runShell actually spawns on this platform. The model is
// told this explicitly: advertising the tool as "bash" on Windows made it emit
// bash quoting into cmd.exe, which mangles it (unterminated string literals, and
// stray "Commande ECHO activée." landing inside generated files).
func shellName() string {
	if runtime.GOOS == "windows" {
		return "cmd.exe"
	}
	return "bash"
}

// fileWrite writes content to path verbatim, creating parent directories and
// replacing any existing file. This is the escape hatch from shell quoting: a
// model with only a shell has to build files with echo/python -c, which is
// unreliable everywhere and outright broken on cmd.exe.
func fileWrite(path, content string) string {
	if strings.TrimSpace(path) == "" {
		return "[erreur] chemin vide"
	}
	path = resolveAgentPath(path)
	// memory et scripts sont réservés à leurs outils dédiés : pas d'écriture directe.
	if msg := guardToolOnlyPath(path); msg != "" {
		return msg
	}
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return "[erreur] " + err.Error()
		}
	}
	// Préserve les permissions d'origine quand le fichier existe déjà (un script
	// 0755 réécrit doit rester exécutable).
	mode := os.FileMode(0o644)
	existed := false
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode()
		existed = true
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		return "[erreur] " + err.Error()
	}
	verb := "créé"
	if existed {
		verb = "réécrit"
	}
	return fmt.Sprintf("[ok] %s %s (%d octets)", path, verb, len(content))
}

// fileEdit applies a single exact-text replacement to a file on disk: oldText
// must appear EXACTLY once (otherwise it errors), so the model can patch a file
// without rewriting it whole. Returns a short status string for the tool result.
func fileEdit(path, oldText, newText string) string {
	if strings.TrimSpace(path) == "" {
		return "[erreur] chemin vide"
	}
	if oldText == "" {
		return "[erreur] old vide"
	}
	path = resolveAgentPath(path)
	// memory et scripts sont réservés à leurs outils dédiés : pas d'édition directe.
	if msg := guardToolOnlyPath(path); msg != "" {
		return msg
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return "[erreur] " + err.Error()
	}
	content := string(b)
	n := strings.Count(content, oldText)
	if n == 0 {
		// Modification déjà en place : on le dit clairement plutôt que de renvoyer
		// une erreur, sinon le modèle croit avoir échoué et recommence.
		if newText != "" && strings.Contains(content, newText) {
			return "[ok] déjà à jour — le fichier contient déjà cette modification"
		}
		return "[erreur] old introuvable dans le fichier"
	}
	if n > 1 {
		return fmt.Sprintf("[erreur] old apparaît %d fois — ajoute du contexte pour le rendre unique", n)
	}
	updated := strings.Replace(content, oldText, newText, 1)
	// Préserve les permissions d'origine (un script 0755 doit rester exécutable).
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode()
	}
	if err := os.WriteFile(path, []byte(updated), mode); err != nil {
		return "[erreur] " + err.Error()
	}
	return fmt.Sprintf("[ok] %s modifié (1 remplacement)", path)
}

// tailRunes returns the last n runes of s (used to cap tool output).
func tailRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[len(r)-n:])
}
