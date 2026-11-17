// AJEAN — un assistant personnel qui se souvient de vous. Un seul binaire : on
// le branche sur une API compatible OpenAI et c'est parti.
//
// Variables d'environnement (toutes facultatives) :
//
//	AJEAN_HOME                dossier des données (défaut : celui de l'exécutable)
//	AJEAN_HOST, AJEAN_PORT    adresse d'écoute (défaut : 127.0.0.1:8090)
//	AJEAN_WEB_KEY             mot de passe de l'interface, à défaut d'un mot de passe enregistré
//	AJEAN_TLS_CERT, AJEAN_TLS_KEY   certificat et clé (fichiers PEM) : l'interface passe en HTTPS
//	AJEAN_API_URL, AJEAN_API_KEY, AJEAN_MODEL   l'API, à la place des réglages
//	AJEAN_NO_BROWSER          ne pas ouvrir l'interface au démarrage
//	AJEAN_BROWSER             navigateur que l'assistant pilote (chemin complet)
//	AJEAN_BROWSER_HEADLESS    le piloter sans fenêtre
//	AJEAN_TOOLS_LAN           autoriser ses outils et son navigateur à joindre le réseau local
//
// Commandes :
//
//	ajean                     lance l'assistant
//	ajean password            définit ou remplace le mot de passe de l'interface
//	ajean password --remove   le retire (l'interface redevient réservée à la machine)
//	ajean import <dossier>    reprend la mémoire et les fichiers de l'ancien AJEAN (0.x)
//
// L'accès à l'interface : sans mot de passe, seule la machine elle-même l'ouvre.
// Avec un mot de passe, tout le monde se connecte. Pour l'ouvrir au réseau
// (AJEAN_HOST), un mot de passe est exigé au démarrage.
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"time"

	"golang.org/x/term"

	"ajean/internal/agent"
	"ajean/internal/apps"
	"ajean/internal/auth"
	"ajean/internal/browser"
	"ajean/internal/config"
	"ajean/internal/embedder"
	"ajean/internal/home"
	"ajean/internal/memory"
	"ajean/internal/server"
	"ajean/internal/tasks"
	"ajean/internal/workspace"
)

// version : posée à la publication (-ldflags "-X main.version=1.2.3"), « dev » sinon.
var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "AJEAN :", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "password":
			return passwordCommand(os.Args[2:])
		case "import":
			return importCommand(os.Args[2:])
		case "version", "--version", "-v":
			fmt.Println("AJEAN", version)
			return nil
		default:
			return fmt.Errorf("commande inconnue : %s (commandes : password, import, version)", os.Args[1])
		}
	}
	for _, dir := range []string{home.Memory(), home.Tools(), home.Apps()} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := workspace.Init(); err != nil {
		return fmt.Errorf("dossier WORKSPACE inutilisable : %w", err)
	}
	if err := config.Load(); err != nil {
		return err
	}
	agent.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	defer browser.Shutdown()
	defer embedder.Shutdown()
	defer workspace.Close()
	apps.Changed = agent.AppsChanged
	// Un moteur qui vient d'être installé a tout le journal à indexer : on n'attend
	// pas la prochaine entrée pour s'y mettre.
	embedder.Changed = func() { agent.EmbedChanged(); memory.WakeIndexer() }
	go tasks.Start(ctx, tasks.Hooks{Run: agent.RunTask, Deliver: agent.DeliverTask, Changed: agent.TasksChanged})
	go agent.RunSleepSchedule(ctx)
	memory.StartIndexer(ctx)

	host, port := envOr("AJEAN_HOST", "127.0.0.1"), envOr("AJEAN_PORT", "8090")
	local := host == "127.0.0.1" || host == "localhost" || host == "::1"
	if !local && !auth.Configured() {
		return errors.New("AJEAN_HOST ouvre l'interface au réseau : un mot de passe est requis d'abord (commande « ajean password »)")
	}
	cert, key := os.Getenv("AJEAN_TLS_CERT"), os.Getenv("AJEAN_TLS_KEY")
	if (cert == "") != (key == "") {
		return errors.New("AJEAN_TLS_CERT et AJEAN_TLS_KEY vont ensemble : le certificat et sa clé")
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	scheme := "http://"
	if cert != "" {
		scheme = "https://"
	}
	url := scheme + net.JoinHostPort(host, port)
	if host == "0.0.0.0" || host == "::" {
		url = scheme + net.JoinHostPort("localhost", port)
	}
	fmt.Println("AJEAN", version, "est prêt :", url)
	switch {
	case !auth.Configured():
		fmt.Println("Accès : cette machine seulement (aucun mot de passe). Pour s'y connecter d'ailleurs : « ajean password ».")
	case cert == "" && !local:
		fmt.Println("Accès : protégé par mot de passe, mais en clair (HTTP). Par Internet : un proxy HTTPS devant, ou AJEAN_TLS_CERT et AJEAN_TLS_KEY.")
	default:
		fmt.Println("Accès : protégé par mot de passe.")
	}
	if !config.Get().Chat().Ready() {
		fmt.Println("Aucune API configurée : ouvre l'interface, les réglages s'affichent.")
	}
	if os.Getenv("AJEAN_NO_BROWSER") == "" {
		openInBrowser(url)
	}

	srv := &http.Server{Handler: server.New(), ReadHeaderTimeout: 10 * time.Second, MaxHeaderBytes: 64 << 10}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if cert != "" {
		err = srv.ServeTLS(ln, cert, key)
	} else {
		err = srv.Serve(ln)
	}
	if !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// passwordCommand : « ajean password » définit ou remplace le mot de passe de
// l'interface, « ajean password --remove » le retire. C'est aussi la sortie de
// secours quand il est oublié : qui a la main sur la machine peut le refaire.
// Un serveur en marche le prend en compte tout de suite ; toutes les sessions
// ouvertes sont fermées.
func passwordCommand(args []string) error {
	if len(args) > 0 {
		if args[0] != "--remove" {
			return errors.New("usage : ajean password [--remove]")
		}
		if err := auth.RemovePassword(); err != nil {
			return err
		}
		fmt.Println("Mot de passe retiré : l'interface n'est plus ouverte qu'à cette machine.")
		return nil
	}
	pw, err := readPassword(fmt.Sprintf("Nouveau mot de passe (%d caractères au moins) : ", auth.MinPassword))
	if err != nil {
		return err
	}
	if term.IsTerminal(int(os.Stdin.Fd())) {
		again, err := readPassword("Encore une fois : ")
		if err != nil {
			return err
		}
		if again != pw {
			return errors.New("les deux saisies diffèrent")
		}
	}
	if err := auth.SetPassword(pw); err != nil {
		return err
	}
	fmt.Println("Mot de passe enregistré. Les sessions ouvertes sont fermées.")
	return nil
}

// readPassword lit un mot de passe sans l'afficher. Sans terminal (entrée
// redirigée), il est lu sur une ligne de l'entrée standard.
func readPassword(prompt string) (string, error) {
	fd := int(os.Stdin.Fd())
	if !term.IsTerminal(fd) {
		line, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err != nil && line == "" {
			return "", errors.New("aucun mot de passe sur l'entrée standard")
		}
		return strings.TrimRight(line, "\r\n"), nil
	}
	fmt.Print(prompt)
	raw, err := term.ReadPassword(fd)
	fmt.Println()
	return string(raw), err
}

func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// openInBrowser ouvre l'interface dans le navigateur par défaut de l'utilisateur.
func openInBrowser(url string) {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	_ = cmd.Start()
}
