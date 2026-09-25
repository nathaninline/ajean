package ajean

// cli_chat.go : « ajean chat », le chat en terminal. Volontairement simple et
// rapide, dans l'esprit de pi : une conversation locale (rien n'est partagé avec
// l'interface web), trois outils (bash, write, edit) qui agissent dans le dossier
// courant, pas de mémoire ni de projets. L'interface reste « en ligne » : le
// texte défile dans le terminal, seules la saisie et la ligne d'état sont
// redessinées.
//
//	ajean chat                    session interactive
//	ajean chat "question"         session interactive, premier message envoyé
//	ajean chat -p "question"      une réponse puis sortie (scripts)
//	echo texte | ajean chat "…"   idem, l'entrée standard est ajoutée au message

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// terminalSystemPrompt : prompt système du chat terminal, court à dessein (voir
// baseSystemPrompt : un préambule verbeux fait sur-raisonner les modèles).
func terminalSystemPrompt(caps Caps) string {
	var b strings.Builder
	b.WriteString("You are Jean, the assistant of AJEAN, running in the user's terminal on this machine.")
	if caps.Agent {
		cwd := agentWorkspace()
		b.WriteString(fmt.Sprintf("\n\nWorking directory: %s (%s/%s). The shell is %s: use its syntax. Relative paths in bash, write and edit resolve in the working directory.\n",
			cwd, runtime.GOOS, runtime.GOARCH, shellName()))
		b.WriteString("Use bash to look around and run things, write to create a file, edit for a precise change (read the file first). Act instead of guessing: call the tool, then answer. Never end your turn after only thinking.\n")
	} else {
		b.WriteString("\n\n")
	}
	b.WriteString("Your answer is displayed in a terminal: be concise, use plain Markdown (short paragraphs, lists, fenced code blocks with a language).\n")
	b.WriteString("Date: " + time.Now().Format("2006-01-02"))
	return b.String()
}

// chatSession : état d'une conversation terminal.
type chatSession struct {
	ui        *chatUI
	msgs      []Message
	system    string
	tools     bool
	lastUser  string
	lastReply string
	ctxMax    int
	ctxUsed   int
}

func (s *chatSession) caps() Caps {
	return Caps{Agent: s.tools, Terminal: true, Mem: MemOff}
}

func (s *chatSession) history() []Message {
	var out []Message
	if s.system != "" {
		out = append(out, Message{Role: "system", Content: s.system})
	}
	return append(out, s.msgs...)
}

// cmdChat : point d'entrée de « ajean chat ».
func cmdChat(args []string) error {
	var system, first string
	printMode, tools := false, true
	var rest []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "-p" || a == "--print":
			printMode = true
		case a == "--no-tools":
			tools = false
		case (a == "-s" || a == "--system") && i+1 < len(args):
			system = args[i+1]
			i++
		case strings.HasPrefix(a, "--system="):
			system = strings.TrimPrefix(a, "--system=")
		case a == "-h" || a == "--help":
			fmt.Print(chatUsage)
			return nil
		default:
			rest = append(rest, a)
		}
	}
	first = strings.TrimSpace(strings.Join(rest, " "))
	// Entrée redirigée (echo … | ajean chat) : son contenu rejoint le message et
	// on passe en réponse unique, faute de clavier pour la suite.
	// Sortie redirigée (ajean chat "…" > fichier) : pas d'interface, juste la réponse.
	if !stdoutIsTerminal() && first != "" {
		printMode = true
	}
	if !stdinIsTerminal() {
		b, _ := io.ReadAll(os.Stdin)
		if in := strings.TrimSpace(string(b)); in != "" {
			if first != "" {
				first += "\n\n" + in
			} else {
				first = in
			}
		}
		printMode = true
	}
	// Les outils agissent dans le dossier d'où « ajean chat » est lancé, comme
	// un outil de ligne de commande (et non dans le workspace de l'app web).
	if cwd, err := os.Getwd(); err == nil {
		_ = os.Setenv(workspaceEnv, cwd)
	}
	s := &chatSession{system: system, tools: tools}
	// CTX pilote aussi la jauge d'un preset externe (voir externalPresetContent).
	if v, err := strconv.Atoi(ReadConfig()["CTX"]); err == nil && v > 0 {
		s.ctxMax = v
	} else if !externalActive() {
		s.ctxMax = 32768
	}
	if printMode {
		if first == "" {
			return fmt.Errorf("rien à envoyer : ajean chat -p \"ta question\"")
		}
		return s.printOnce(first)
	}
	return s.interactive(first)
}

const chatUsage = `ajean chat : discuter avec le modèle depuis le terminal

  ajean chat                     session interactive
  ajean chat "question"          session interactive, premier message envoyé
  ajean chat -p "question"       une seule réponse puis sortie (scripts)
  echo texte | ajean chat "…"    l'entrée standard est ajoutée au message

Options :
  -s, --system "texte"   prompt système personnalisé
  --no-tools             pas d'outils (réponse texte uniquement)

Les outils (bash, write, edit) agissent dans le dossier courant.
Dans la session, /help liste les commandes.
`

// printOnce : réponse unique, texte brut sur stdout (le reste sur stderr).
func (s *chatSession) printOnce(msg string) error {
	if err := waitEngineQuiet(); err != nil {
		return err
	}
	s.msgs = append(s.msgs, Message{Role: "user", Content: msg})
	var failed error
	_, err := runChat(context.Background(), InjectSkills(s.history(), s.caps()), 0.7, s.caps(), func(ev StreamEvent) bool {
		switch {
		case ev.Err != nil:
			failed = ev.Err
		case ev.Content != "":
			fmt.Print(ev.Content)
		case ev.ToolUsed != nil && !ev.ToolUsed.Done && !ev.ToolUsed.Typing:
			fmt.Fprintf(os.Stderr, "%s %s %s\n", dim("●"), toolTitle(ev.ToolUsed.Name), dim(oneLine(ev.ToolUsed.Label)))
		}
		return true
	}, nil)
	fmt.Println()
	if err == nil {
		err = failed
	}
	return err
}

// waitEngineQuiet : mode non interactif, on n'attend pas un chargement.
func waitEngineQuiet() error {
	if healthCheck() {
		return nil
	}
	if msg := modelLoadError(); msg != "" {
		return errors.New(msg)
	}
	return fmt.Errorf("moteur injoignable sur :%d (ajean start pour le lancer)", LLMPort())
}

func (s *chatSession) interactive(first string) error {
	raw, err := enterRaw()
	if err != nil {
		return fmt.Errorf("terminal non interactif : %w", err)
	}
	defer raw.restore()
	compactLogOut = io.Discard
	out := crlfWriter{os.Stdout}
	ui := &chatUI{out: out, raw: raw}
	s.ui = ui
	fmt.Fprint(out, "\x1b[?2004h") // collage encadré (bracketed paste)
	defer fmt.Fprint(out, "\x1b[?2004l")
	keys := newKeyReader(terminalInput())
	ui.keys = keys.ch

	s.banner()
	if !s.ensureEngine() {
		return nil
	}
	ed := newLineEditor(out, keys.ch)
	ed.completions = chatCommandNames()
	ed.hint = func(buf string) string {
		if strings.HasPrefix(buf, "/") {
			return chatCommandHint(buf)
		}
		if strings.Contains(buf, "\n") {
			return "Entrée envoie · Alt+Entrée ou Ctrl+J : nouvelle ligne"
		}
		return ""
	}
	pendingQuit := false
	typeahead := ""
	if first != "" {
		fmt.Fprint(out, accent("› ")+first+"\n")
		ed.addHistory(first)
		typeahead = s.turn(first)
	}
	for {
		fmt.Fprint(out, "\n")
		line, res := ed.readLine(typeahead)
		typeahead = ""
		switch res {
		case editEOF:
			fmt.Fprint(out, dim("À bientôt.")+"\n")
			return nil
		case editInterrupt:
			if pendingQuit {
				return nil
			}
			pendingQuit = true
			fmt.Fprint(out, dim("Ctrl-C encore une fois (ou Ctrl-D) pour quitter.")+"\n")
			continue
		}
		pendingQuit = false
		text := strings.TrimSpace(line)
		if text == "" {
			continue
		}
		if strings.HasPrefix(text, "/") && !strings.HasPrefix(text, "//") {
			quit, retry := s.command(text)
			if quit {
				return nil
			}
			if retry != "" {
				typeahead = s.turn(retry)
			}
			continue
		}
		typeahead = s.turn(line)
	}
}

// banner : en-tête de session, une seule fois.
func (s *chatSession) banner() {
	out := s.ui.out
	model := chatModelName()
	fmt.Fprintf(out, "\n %s %s\n", col("1;36", "ajean"), dim("v"+Version))
	info := model
	if s.ctxMax > 0 {
		info += dim(" · contexte " + fmtCtxWindow(s.ctxMax))
	}
	fmt.Fprintf(out, " %s %s\n", dim("modèle "), info)
	cwd := agentWorkspace()
	if home, err := os.UserHomeDir(); err == nil && home != "" && strings.HasPrefix(cwd, home) {
		cwd = "~" + cwd[len(home):]
	}
	tools := "désactivés"
	if s.tools {
		tools = "bash, write, edit"
	}
	// Chemin trop long : on garde la fin, la partie parlante.
	if w := termWidth() - 10; visibleWidth(cwd) > w && w > 10 {
		r := []rune(cwd)
		for visibleWidth(string(r)) > w-1 {
			r = r[1:]
		}
		cwd = "…" + string(r)
	}
	fmt.Fprintf(out, " %s %s\n", dim("dossier"), cwd)
	fmt.Fprintf(out, " %s %s\n", dim("outils "), tools)
	fmt.Fprintf(out, " %s\n", dim("/help pour l'aide · Échap interrompt une réponse · Ctrl-D quitte"))
}

func chatModelName() string {
	cfg := ReadConfig()
	if isExternalConfig(cfg) {
		if m := cfg["EXTERNAL_MODEL"]; m != "" {
			return m
		}
	}
	if id := activePresetID(); id != "" {
		if list, err := ListPresets(); err == nil {
			for _, p := range list {
				if p.ID == id {
					return p.Name
				}
			}
		}
	}
	return strings.TrimSuffix(filepath.Base(cfg["MODEL"]), ".gguf")
}

// ensureEngine : attend que le modèle réponde, en proposant de démarrer le
// moteur s'il est arrêté. false = abandon.
func (s *chatSession) ensureEngine() bool {
	if healthCheck() {
		return true
	}
	ui := s.ui
	if !serviceIsActive() {
		fmt.Fprint(ui.out, "\n"+yellow("●")+" Le moteur est arrêté. Le démarrer ? "+dim("[O/n] "))
		k, ok := <-ui.keys
		if !ok {
			return false
		}
		yes := k.kind == keyEnter || (k.kind == keyRune && strings.ContainsRune("oOyY", k.r))
		fmt.Fprint(ui.out, "\n")
		if !yes {
			fmt.Fprint(ui.out, dim("Lance-le avec « ajean start », puis relance « ajean chat ».")+"\n")
			return false
		}
		var err error
		ui.cooked(func() { err = serviceAction("start") })
		if err != nil {
			fmt.Fprint(ui.out, red("✗ "+err.Error())+"\n")
			return false
		}
	}
	return s.waitReady("Chargement du modèle")
}

// waitReady : spinner jusqu'à ce que le moteur réponde (Échap/Ctrl-C abandonne).
func (s *chatSession) waitReady(label string) bool {
	ui := s.ui
	ui.spinStart(label)
	defer ui.spinStop()
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case k, ok := <-ui.keys:
			if !ok || k.kind == keyEsc || k.kind == keyCtrlC {
				ui.spinStop()
				fmt.Fprint(ui.out, dim("Attente abandonnée.")+"\n")
				return false
			}
		case <-tick.C:
			if healthCheck() {
				return true
			}
			if msg := modelLoadError(); msg != "" {
				ui.spinStop()
				fmt.Fprint(ui.out, red("✗ "+msg)+"\n")
				return false
			}
		}
	}
}

// turn envoie un message et affiche la réponse. Rend ce qui a été tapé pendant
// la génération (pré-rempli dans la saisie suivante).
func (s *chatSession) turn(text string) string {
	ui := s.ui
	s.lastUser = text
	s.msgs = append(s.msgs, Message{Role: "user", Content: text})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Pendant la réponse : Échap / Ctrl-C interrompent, le reste de la frappe
	// est mis de côté pour la saisie suivante.
	var typed []rune
	stop := make(chan struct{})
	watcherDone := make(chan struct{})
	interrupted := false
	go func() {
		defer close(watcherDone)
		for {
			select {
			case <-stop:
				return
			case k, ok := <-ui.keys:
				if !ok {
					cancel()
					return
				}
				switch k.kind {
				case keyEsc, keyCtrlC:
					interrupted = true
					cancel()
				case keyRune:
					typed = append(typed, k.r)
				case keyPaste:
					typed = append(typed, []rune(k.text)...)
				case keyBackspace:
					if len(typed) > 0 {
						typed = typed[:len(typed)-1]
					}
				}
			}
		}
	}()

	ui.beginTurn()
	caps := s.caps()
	if compacted, changed := MaybeCompact(ctx, s.msgs, caps, 0); changed {
		s.msgs = compacted
		ui.note("contexte résumé pour tenir dans la fenêtre")
	}
	extra, err := runChat(ctx, InjectSkills(s.history(), caps), 0.7, caps, func(ev StreamEvent) bool {
		switch {
		case ev.Err != nil:
			if ctx.Err() == nil {
				ui.errorLine(ev.Err.Error())
			}
		case ev.NewHistory != nil:
			base := ev.NewHistory
			for len(base) > 0 && base[0].Role == "system" {
				base = base[1:]
			}
			s.msgs = append([]Message(nil), base...)
			ui.note("contexte résumé pour tenir dans la fenêtre")
		case ev.Compacting != nil:
			if *ev.Compacting {
				ui.spinStart("Résumé du contexte")
			}
		case ev.Stats != nil:
			ui.stats = ev.Stats
		case ev.DropReasoning:
			ui.dropReasoning()
		case ev.ToolUsed != nil:
			ui.tool(ev.ToolUsed)
		case ev.Reasoning != "":
			ui.reasoning(ev.Reasoning)
		case ev.Content != "":
			ui.content(ev.Content)
		}
		return ctx.Err() == nil
	}, nil)
	close(stop)
	<-watcherDone
	reply := ui.endTurn(interrupted || errors.Is(ctx.Err(), context.Canceled))

	if st := ui.stats; st != nil && st.PromptTokensTotal > 0 {
		s.ctxUsed = st.PromptTokensTotal + st.GenTokens
	}
	ui.footer(s.ctxUsed, s.ctxMax)
	stopped := interrupted || errors.Is(ctx.Err(), context.Canceled)
	switch {
	case err == nil && !stopped:
		s.msgs = append(s.msgs, extra...)
		s.msgs = append(s.msgs, Message{Role: "assistant", Content: reply})
		s.lastReply = reply
	case reply != "":
		// Interrompu : on garde ce qui a été écrit, pour que la suite ait le fil.
		s.msgs = append(s.msgs, Message{Role: "assistant", Content: reply + "\n\n[réponse interrompue]"})
		s.lastReply = reply
	default:
		// Rien de produit : on retire le message pour pouvoir le renvoyer tel quel
		// (seulement s'il est bien le dernier : une compaction a pu tout remplacer).
		if n := len(s.msgs); n > 0 && s.msgs[n-1].Role == "user" && msgText(s.msgs[n-1]) == text {
			s.msgs = s.msgs[:n-1]
		}
	}
	return string(typed)
}

// ─── commandes « / » ───────────────────────────────────────────────────────

type chatCommand struct {
	name, args, help string
}

var chatCommands = []chatCommand{
	{"/help", "", "cette aide"},
	{"/new", "", "nouvelle conversation (efface le contexte)"},
	{"/retry", "", "régénère la dernière réponse"},
	{"/undo", "", "retire le dernier échange"},
	{"/copy", "", "copie la dernière réponse dans le presse-papiers"},
	{"/save", "[fichier]", "enregistre la conversation en Markdown"},
	{"/system", "[texte]", "affiche ou change le prompt système"},
	{"/tools", "[on|off]", "active ou coupe les outils (bash, write, edit)"},
	{"/think", "", "affiche ou masque le raisonnement du modèle"},
	{"/model", "[n°]", "liste les presets ou bascule sur l'un d'eux"},
	{"/quit", "", "quitte (ou Ctrl-D)"},
}

func chatCommandNames() []string {
	var n []string
	for _, c := range chatCommands {
		n = append(n, c.name)
	}
	return append(n, "/clear", "/exit")
}

func chatCommandHint(buf string) string {
	word := strings.Fields(buf + " ")[0]
	var m []chatCommand
	for _, c := range chatCommands {
		if c.name == word {
			m = []chatCommand{c}
			break
		}
		if strings.HasPrefix(c.name, word) {
			m = append(m, c)
		}
	}
	switch len(m) {
	case 0:
		return "commande inconnue · /help"
	case 1:
		return strings.TrimSpace(m[0].name+" "+m[0].args) + " : " + m[0].help
	}
	names := make([]string, len(m))
	for i, c := range m {
		names[i] = c.name
	}
	return strings.Join(names, "  ")
}

// copyToClipboard : en plus de la séquence OSC 52 (terminaux récents, SSH
// compris), passe par l'outil du système quand il existe.
func copyToClipboard(s string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		// clip.exe lit l'UTF-16 avec BOM : sans ça les accents sont abîmés.
		cmd = exec.Command("clip")
		u := []byte{0xff, 0xfe}
		for _, r := range utf16Encode(s) {
			u = append(u, byte(r), byte(r>>8))
		}
		cmd.Stdin = strings.NewReader(string(u))
	case "darwin":
		cmd = exec.Command("pbcopy")
		cmd.Stdin = strings.NewReader(s)
	default:
		for _, c := range [][]string{{"wl-copy"}, {"xclip", "-selection", "clipboard"}, {"xsel", "-ib"}} {
			if hasTool(c[0]) {
				cmd = exec.Command(c[0], c[1:]...)
				cmd.Stdin = strings.NewReader(s)
				break
			}
		}
	}
	if cmd != nil {
		_ = hideCmd(cmd).Run()
	}
}

// command exécute une commande « / ». Rend quit, ou un message à renvoyer.
func (s *chatSession) command(text string) (quit bool, resend string) {
	ui := s.ui
	f := strings.Fields(text)
	name, arg := f[0], strings.TrimSpace(strings.TrimPrefix(text, f[0]))
	switch name {
	case "/quit", "/exit", "/q":
		return true, ""
	case "/help", "/?":
		var b strings.Builder
		for _, c := range chatCommands {
			left := strings.TrimSpace(c.name + " " + c.args)
			b.WriteString("  " + accent(fmt.Sprintf("%-18s", left)) + " " + c.help + "\n")
		}
		b.WriteString("\n" + dim("  Entrée envoie · Alt+Entrée, Ctrl+J ou « \\ » en fin de ligne : nouvelle ligne\n"))
		b.WriteString(dim("  ↑ ↓ historique · Tab complète une commande · Échap ou Ctrl-C interrompt une réponse\n"))
		fmt.Fprint(ui.out, b.String())
	case "/new", "/clear", "/reset":
		s.msgs, s.lastReply, s.ctxUsed = nil, "", 0
		fmt.Fprint(ui.out, "\x1b[H\x1b[2J")
		s.banner()
		ui.note("nouvelle conversation")
	case "/retry":
		if s.lastUser == "" {
			ui.note("rien à régénérer")
			break
		}
		s.dropLastExchange()
		return false, s.lastUser
	case "/undo":
		if !s.dropLastExchange() {
			ui.note("rien à retirer")
			break
		}
		ui.note("dernier échange retiré")
	case "/copy":
		if s.lastReply == "" {
			ui.note("aucune réponse à copier")
			break
		}
		fmt.Fprint(ui.out, "\x1b]52;c;"+base64.StdEncoding.EncodeToString([]byte(s.lastReply))+"\a")
		copyToClipboard(s.lastReply)
		ui.note("dernière réponse copiée")
	case "/save":
		path := arg
		if path == "" {
			path = "ajean-chat-" + time.Now().Format("20060102-150405") + ".md"
		}
		if err := os.WriteFile(path, []byte(s.transcript()), 0o644); err != nil {
			ui.errorLine(err.Error())
			break
		}
		abs, _ := filepath.Abs(path)
		ui.note("conversation enregistrée : " + abs)
	case "/system", "/sys":
		if arg == "" {
			if s.system == "" {
				ui.note("aucun prompt système personnalisé (/system <texte> pour en définir un)")
			} else {
				ui.note("prompt système : " + s.system)
			}
			break
		}
		if arg == "-" || arg == "off" {
			s.system = ""
			ui.note("prompt système personnalisé retiré")
			break
		}
		s.system = arg
		ui.note("prompt système mis à jour")
	case "/tools":
		switch strings.ToLower(arg) {
		case "on":
			s.tools = true
		case "off":
			s.tools = false
		default:
			s.tools = !s.tools
		}
		if s.tools {
			ui.note("outils activés : bash, write, edit")
		} else {
			ui.note("outils coupés : réponses texte uniquement")
		}
	case "/think":
		ui.showThink = !ui.showThink
		if ui.showThink {
			ui.note("raisonnement affiché")
		} else {
			ui.note("raisonnement masqué (résumé en une ligne)")
		}
	case "/model", "/models":
		s.modelCommand(arg)
	default:
		ui.note("commande inconnue : " + name + " (/help)")
	}
	return false, ""
}

func (s *chatSession) dropLastExchange() bool {
	for i := len(s.msgs) - 1; i >= 0; i-- {
		if s.msgs[i].Role == "user" {
			s.msgs = s.msgs[:i]
			return true
		}
	}
	return false
}

func (s *chatSession) transcript() string {
	var b strings.Builder
	b.WriteString("# Conversation AJEAN (" + time.Now().Format("2006-01-02 15:04") + ")\n\n")
	for _, m := range s.msgs {
		switch m.Role {
		case "user":
			b.WriteString("## Vous\n\n" + msgText(m) + "\n\n")
		case "assistant":
			if strings.TrimSpace(msgText(m)) != "" {
				b.WriteString("## Jean\n\n" + msgText(m) + "\n\n")
			}
		}
	}
	return b.String()
}

func (s *chatSession) modelCommand(arg string) {
	ui := s.ui
	list, err := ListPresets()
	if err != nil || len(list) == 0 {
		ui.note("aucun preset trouvé")
		return
	}
	if arg == "" {
		var b strings.Builder
		for i, p := range list {
			mark := "  "
			name := p.Name
			if p.Active {
				mark, name = accent("● "), bold(p.Name)
			}
			b.WriteString(fmt.Sprintf("  %s%s %s\n", mark, dim(fmt.Sprintf("%2d", i+1)), name))
		}
		b.WriteString(dim("  /model <n°> pour basculer (le modèle est rechargé)") + "\n")
		fmt.Fprint(ui.out, b.String())
		return
	}
	n, err := strconv.Atoi(arg)
	if err != nil || n < 1 || n > len(list) {
		ui.note("numéro invalide (/model pour la liste)")
		return
	}
	if list[n-1].Active {
		ui.note(list[n-1].Name + " est déjà actif")
		return
	}
	ui.spinStart("Bascule sur " + list[n-1].Name)
	// Même logique que l'UI web (handleSwitch) : preset externe = on arrête le
	// moteur local au lieu de le redémarrer (il n'a pas de modèle à charger).
	ui.cooked(func() {
		devnull, _ := os.Open(os.DevNull)
		old := os.Stdout
		if devnull != nil {
			os.Stdout = devnull
			defer devnull.Close()
		}
		defer func() { os.Stdout = old }()
		if err = applyPresetFile(list[n-1].Path); err != nil {
			return
		}
		if usesRemoteEndpoint(ReadConfig()) {
			cloudDeploy()
			if serviceIsActive() {
				err = serviceAction("stop")
			}
			return
		}
		err = serviceAction("restart")
	})
	ui.spinStop()
	if err != nil {
		ui.errorLine(err.Error())
		return
	}
	if v, e := strconv.Atoi(ReadConfig()["CTX"]); e == nil && v > 0 {
		s.ctxMax = v
	}
	s.ctxUsed = 0
	// API externe : rien à attendre. GPU cloud : on attend le déploiement
	// (healthCheck suit cloudReady), comme un modèle local qui charge.
	if isExternalConfig(ReadConfig()) || s.waitReady("Chargement de "+list[n-1].Name) {
		ui.note("modèle actif : " + chatModelName())
	}
}

// ─── rendu ─────────────────────────────────────────────────────────────────

type chatUI struct {
	mu        sync.Mutex
	out       io.Writer
	raw       *rawTerm
	keys      <-chan key
	showThink bool

	md          mdRenderer
	partial     string // ligne de réponse en cours d'écriture (brute)
	reply       strings.Builder
	stats       *StatsEvent
	turnStart   time.Time
	thinkStart  time.Time
	thinkChars  int
	thinking    bool
	thinkLineOn bool // raisonnement affiché : une ligne est en cours
	needGap     bool // un bloc (outil, résumé de réflexion) précède : ligne vide avant le texte

	spinOn    bool
	spinDrawn bool
	spinLabel string
	spinSince time.Time
	spinStopC chan struct{}
	toolStart time.Time
}

var spinFrames = func() []string {
	// La console Windows historique (conhost, hors Windows Terminal) n'a pas les
	// glyphes braille dans ses polices par défaut.
	if runtime.GOOS == "windows" && os.Getenv("WT_SESSION") == "" && os.Getenv("TERM_PROGRAM") == "" {
		return []string{"|", "/", "-", "\\"}
	}
	return []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
}()

func (u *chatUI) cooked(f func()) {
	u.raw.restore()
	defer func() {
		if r, err := enterRaw(); err == nil {
			u.raw.state = r.state
		}
	}()
	f()
}

func (u *chatUI) spinStart(label string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.spinLabel = label
	u.spinSince = time.Now()
	if u.spinOn {
		return
	}
	u.spinOn = true
	stop := make(chan struct{})
	u.spinStopC = stop
	go func() {
		t := time.NewTicker(90 * time.Millisecond)
		defer t.Stop()
		i := 0
		for {
			select {
			case <-stop:
				return
			case <-t.C:
				u.mu.Lock()
				if u.spinOn && u.partial == "" && !u.thinkLineOn {
					u.drawSpinner(i)
				}
				u.mu.Unlock()
				i++
			}
		}
	}()
}

func (u *chatUI) drawSpinner(i int) {
	el := time.Since(u.spinSince)
	extra := fmtDuration(el)
	if u.thinking && u.thinkChars > 0 {
		extra += " · ~" + fmtTokens(u.thinkChars/4) + " tok"
	}
	line := accent(spinFrames[i%len(spinFrames)]) + " " + u.spinLabel + "… " + dim(extra+" · Échap pour interrompre")
	fmt.Fprint(u.out, "\r\x1b[2K"+truncateANSI(line, termWidth()-1))
	u.spinDrawn = true
}

// clearSpinLocked efface la ligne du spinner (verrou tenu).
func (u *chatUI) clearSpinLocked() {
	if u.spinDrawn {
		fmt.Fprint(u.out, "\r\x1b[2K")
		u.spinDrawn = false
	}
}

func (u *chatUI) spinStop() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.stopSpinLocked()
}

func (u *chatUI) stopSpinLocked() {
	if u.spinOn {
		close(u.spinStopC)
		u.spinOn = false
	}
	u.clearSpinLocked()
}

func (u *chatUI) beginTurn() {
	u.mu.Lock()
	u.md = mdRenderer{}
	u.partial = ""
	u.reply.Reset()
	u.stats = nil
	u.turnStart = time.Now()
	u.thinking, u.thinkChars, u.thinkLineOn, u.needGap = false, 0, false, false
	u.mu.Unlock()
	u.spinStart("Réflexion")
}

// reasoning : raisonnement du modèle. Masqué par défaut (le spinner compte),
// résumé en une ligne quand la réponse commence.
func (u *chatUI) reasoning(s string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if !u.thinking {
		u.thinking = true
		u.thinkStart = time.Now()
		u.spinSince = u.thinkStart
		u.spinLabel = "Réflexion"
	}
	u.thinkChars += len([]rune(s))
	if !u.showThink {
		return
	}
	u.stopSpinLocked()
	for _, r := range s {
		if !u.thinkLineOn {
			fmt.Fprint(u.out, dim("┊ "))
			u.thinkLineOn = true
		}
		if r == '\n' {
			fmt.Fprint(u.out, "\n")
			u.thinkLineOn = false
			continue
		}
		fmt.Fprint(u.out, col("2;3", string(r)))
	}
}

// endThinkingLocked referme le raisonnement (résumé si masqué).
func (u *chatUI) endThinkingLocked() {
	if !u.thinking {
		return
	}
	u.thinking = false
	u.clearSpinLocked()
	if u.thinkLineOn {
		fmt.Fprint(u.out, "\n")
		u.thinkLineOn = false
	}
	d := time.Since(u.thinkStart)
	if !u.showThink && d >= 500*time.Millisecond {
		fmt.Fprint(u.out, dim("✻ Réfléchi pendant "+fmtDuration(d)+" · /think pour afficher")+"\n")
		u.needGap = true
	}
	if u.showThink {
		fmt.Fprint(u.out, "\n")
	}
}

func (u *chatUI) dropReasoning() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.thinkChars = 0
}

func (u *chatUI) content(s string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.endThinkingLocked()
	u.stopSpinLocked()
	u.reply.WriteString(s)
	// Pas de ligne vide en tête de réponse ; une seule après un bloc d'outils.
	if u.partial == "" && (u.reply.Len() == len(s) || u.needGap) {
		s = strings.TrimLeft(s, "\n")
		if s == "" {
			return
		}
		if u.needGap {
			fmt.Fprint(u.out, "\n")
			u.needGap = false
		}
	}
	s = strings.ReplaceAll(s, "\t", "    ")
	for {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			u.partial += s
			fmt.Fprint(u.out, s)
			return
		}
		fmt.Fprint(u.out, s[:i])
		u.partial += s[:i]
		u.finishLineLocked()
		s = s[i+1:]
	}
}

// finishLineLocked remplace la ligne brute en cours par sa version mise en forme.
func (u *chatUI) finishLineLocked() {
	width := termWidth()
	rows := rowsFor(visibleWidth(u.partial), width)
	if rows > 1 {
		fmt.Fprintf(u.out, "\x1b[%dA", rows-1)
	}
	fmt.Fprint(u.out, "\r\x1b[J"+u.md.line(u.partial, width)+"\n")
	u.partial = ""
}

func (u *chatUI) flushPartialLocked() {
	if u.partial != "" {
		u.finishLineLocked()
	}
}

func (u *chatUI) tool(t *ToolUsedEvent) {
	u.mu.Lock()
	defer u.mu.Unlock()
	switch {
	case t.Typing:
		u.endThinkingLocked()
		u.flushPartialLocked()
		u.spinLabel = toolTitle(t.Name) + " : préparation"
		if !u.spinOn {
			u.mu.Unlock()
			u.spinStart(u.spinLabel)
			u.mu.Lock()
		}
	case !t.Done:
		u.endThinkingLocked()
		u.flushPartialLocked()
		u.stopSpinLocked()
		// Du texte précède l'outil : une ligne vide pour détacher le bloc.
		if u.reply.Len() > 0 && !u.needGap {
			fmt.Fprint(u.out, "\n")
		}
		width := termWidth()
		head := accent("●") + " " + bold(toolTitle(t.Name))
		label := oneLine(t.Label)
		if label != "" {
			head += " " + dim(truncateWidth(label, width-visibleWidth(head)-2))
		}
		fmt.Fprint(u.out, head+"\n")
		u.toolStart = time.Now()
		u.mu.Unlock()
		u.spinStart("Exécution")
		u.mu.Lock()
	default:
		u.stopSpinLocked()
		fmt.Fprint(u.out, toolResultLines(t, termWidth(), time.Since(u.toolStart)))
		u.needGap = true
		u.mu.Unlock()
		u.spinStart("Réflexion")
		u.mu.Lock()
		u.thinking = false
	}
}

func (u *chatUI) note(s string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.clearSpinLocked()
	fmt.Fprint(u.out, dim("  "+s)+"\n")
}

func (u *chatUI) errorLine(s string) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.flushPartialLocked()
	u.clearSpinLocked()
	fmt.Fprint(u.out, red("✗ "+s)+"\n")
}

// endTurn clôt l'affichage de la réponse et rend son texte.
func (u *chatUI) endTurn(interrupted bool) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.stopSpinLocked()
	u.endThinkingLocked()
	u.flushPartialLocked()
	if u.md.inCode {
		fmt.Fprint(u.out, dim("╰─")+"\n")
	}
	if interrupted {
		fmt.Fprint(u.out, yellow("■ Interrompu")+"\n")
	}
	return strings.TrimSpace(u.reply.String())
}

func (u *chatUI) footer(used, max int) {
	u.mu.Lock()
	defer u.mu.Unlock()
	parts := []string{fmtDuration(time.Since(u.turnStart))}
	if st := u.stats; st != nil {
		if st.GenTokens > 0 {
			parts = append(parts, fmtTokens(st.GenTokens)+" tok")
		}
		if st.GenPerSecond > 0 {
			parts = append(parts, strings.Replace(strconv.FormatFloat(st.GenPerSecond, 'f', 1, 64), ".", ",", 1)+" tok/s")
		}
	}
	if used > 0 && max > 0 {
		pct := used * 100 / max
		c := fmt.Sprintf("contexte %s/%s (%d%%)", fmtTokens(used), fmtCtxWindow(max), pct)
		if pct >= 80 {
			c = yellow(c)
		}
		parts = append(parts, c)
	}
	fmt.Fprint(u.out, dim("  "+strings.Join(parts, " · "))+"\n")
}

// ─── utilitaires d'affichage ───────────────────────────────────────────────

func toolTitle(name string) string {
	switch name {
	case "bash":
		return "Bash"
	case "write":
		return "Écriture"
	case "edit":
		return "Édition"
	case "see_image":
		return "Image"
	}
	return name
}

// toolResultLines : résumé du résultat sous l'appel (« ⎿ … »).
func toolResultLines(t *ToolUsedEvent, width int, took time.Duration) string {
	pre := dim("  ⎿ ")
	ind := "    "
	var b strings.Builder
	res := strings.TrimSpace(t.Result)
	switch t.Name {
	case "bash":
		exit, body := parseShellResult(res)
		lines := nonEmptyLines(body)
		status := ""
		if exit != 0 {
			status = red(fmt.Sprintf("code de sortie %d", exit))
		} else if exit == 0 && strings.HasPrefix(res, "exit:") {
			status = "ok"
		} else {
			status = yellow(oneLine(res))
		}
		sum := status
		if len(lines) > 0 {
			sum += dim(fmt.Sprintf(" · %d ligne%s", len(lines), plural(len(lines))))
		}
		b.WriteString(pre + sum + dim(" · "+fmtDuration(took)) + "\n")
		show := lines
		if len(show) > 4 {
			show = show[:4]
		}
		for _, l := range show {
			b.WriteString(dim(ind+truncateWidth(l, width-len(ind)-1)) + "\n")
		}
		if len(lines) > len(show) {
			b.WriteString(dim(fmt.Sprintf("%s… %d ligne%s de plus", ind, len(lines)-len(show), plural(len(lines)-len(show)))) + "\n")
		}
	default:
		if len(t.Diff) > 0 {
			// Vrais totaux (le diff est tronqué) ; à défaut, décompte du diff.
			add, del := t.Added, t.Removed
			if add == 0 && del == 0 {
				for _, d := range t.Diff {
					switch d.Op {
					case "+":
						add++
					case "-":
						del++
					}
				}
			}
			b.WriteString(pre + dim(fmt.Sprintf("+%d −%d", add, del)) + "\n")
			shown := 0
			for _, d := range t.Diff {
				if (d.Op != "+" && d.Op != "-") || strings.TrimSpace(d.Text) == "" {
					continue
				}
				if shown == 8 {
					b.WriteString(dim(ind+"…") + "\n")
					break
				}
				l := truncateWidth(d.Text, width-len(ind)-3)
				if d.Op == "+" {
					b.WriteString(ind + accent("+ "+l) + "\n")
				} else {
					b.WriteString(ind + red("- "+l) + "\n")
				}
				shown++
			}
			break
		}
		first := oneLine(res)
		if isToolError(res) {
			b.WriteString(pre + red(truncateWidth(first, width-6)) + "\n")
		} else {
			b.WriteString(pre + dim(truncateWidth(first, width-6)) + "\n")
		}
	}
	return b.String()
}

func parseShellResult(res string) (int, string) {
	exit := -1
	if strings.HasPrefix(res, "exit:") {
		line := res
		if i := strings.IndexByte(res, '\n'); i >= 0 {
			line = res[:i]
		}
		exit, _ = strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(line, "exit:")))
	}
	var body []string
	for _, sec := range strings.Split(res, "\n\n") {
		switch {
		case strings.HasPrefix(sec, "stdout:\n"):
			body = append(body, strings.TrimPrefix(sec, "stdout:\n"))
		case strings.HasPrefix(sec, "stderr:\n"):
			body = append(body, strings.TrimPrefix(sec, "stderr:\n"))
		case strings.HasPrefix(sec, "exit:"):
		default:
			if len(body) > 0 {
				body[len(body)-1] += "\n\n" + sec
			}
		}
	}
	return exit, strings.Join(body, "\n")
}

func isToolError(s string) bool {
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "erreur") || strings.HasPrefix(l, "error") || strings.HasPrefix(l, "[erreur") || strings.Contains(l, "introuvable")
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(s, "\r", ""), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, strings.ReplaceAll(l, "\t", "    "))
		}
	}
	return out
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i] + " …"
	}
	return s
}

func plural(n int) string {
	if n > 1 {
		return "s"
	}
	return ""
}

func fmtDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%d ms", d.Milliseconds())
	case d < time.Minute:
		return strings.Replace(strconv.FormatFloat(d.Seconds(), 'f', 1, 64), ".", ",", 1) + " s"
	default:
		return fmt.Sprintf("%d min %02d s", int(d.Minutes()), int(d.Seconds())%60)
	}
}

func fmtTokens(n int) string {
	if n < 1000 {
		return strconv.Itoa(n)
	}
	v := float64(n) / 1000
	if v >= 10 || v == float64(int(v)) {
		return fmt.Sprintf("%dk", int(v+0.5))
	}
	return strings.Replace(strconv.FormatFloat(v, 'f', 1, 64), ".", ",", 1) + "k"
}

// truncateANSI coupe une chaîne colorée à w colonnes visibles.
func truncateANSI(s string, w int) string {
	if visibleWidth(s) <= w {
		return s
	}
	var b strings.Builder
	cw := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			j := skipANSI(s, i)
			b.WriteString(s[i:j])
			i = j
			continue
		}
		r, n := utf8.DecodeRuneInString(s[i:])
		rw := runeWidth(r)
		if cw+rw > w {
			break
		}
		b.WriteString(s[i : i+n])
		cw += rw
		i += n
	}
	return b.String() + "\x1b[0m"
}

func utf16Encode(s string) []uint16 { return utf16.Encode([]rune(s)) }

// fmtCtxWindow : taille de fenêtre de contexte, en « k » binaires quand elle tombe
// juste (32768 → 32k, comme l'écrivent les presets et llama.cpp).
func fmtCtxWindow(n int) string {
	if n >= 1024 && n%1024 == 0 {
		return strconv.Itoa(n/1024) + "k"
	}
	return fmtTokens(n)
}
