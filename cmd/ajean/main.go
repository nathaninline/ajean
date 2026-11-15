// AJEAN — un assistant personnel qui se souvient de vous. Un seul binaire : on
// le branche sur une API compatible OpenAI et c'est parti.
//
// Variables d'environnement (toutes facultatives) :
//
//	AJEAN_HOME                dossier des données (défaut : celui de l'exécutable)
//	AJEAN_HOST, AJEAN_PORT    adresse d'écoute (défaut : 127.0.0.1:8090)
//	AJEAN_WEB_KEY             clé exigée par l'interface (indispensable hors de 127.0.0.1)
//	AJEAN_API_URL, AJEAN_API_KEY, AJEAN_MODEL   l'API, à la place des réglages
//	AJEAN_NO_BROWSER          ne pas ouvrir l'interface au démarrage
//	AJEAN_BROWSER             navigateur que l'assistant pilote (chemin complet)
//	AJEAN_BROWSER_HEADLESS    le piloter sans fenêtre
//	AJEAN_TOOLS_LAN           autoriser ses outils et son navigateur à joindre le réseau local
package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"time"

	"ajean/internal/agent"
	"ajean/internal/apps"
	"ajean/internal/browser"
	"ajean/internal/config"
	"ajean/internal/embedder"
	"ajean/internal/home"
	"ajean/internal/memory"
	"ajean/internal/server"
	"ajean/internal/tasks"
	"ajean/internal/workspace"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "AJEAN :", err)
		os.Exit(1)
	}
}

func run() error {
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
	if host != "127.0.0.1" && host != "localhost" && os.Getenv("AJEAN_WEB_KEY") == "" {
		return errors.New("AJEAN_HOST ouvre l'interface au réseau : définis aussi AJEAN_WEB_KEY (clé d'accès)")
	}
	ln, err := net.Listen("tcp", net.JoinHostPort(host, port))
	if err != nil {
		return err
	}
	url := "http://" + net.JoinHostPort(host, port)
	if host == "0.0.0.0" || host == "::" {
		url = "http://" + net.JoinHostPort("localhost", port)
	}
	fmt.Println("AJEAN est prêt :", url)
	if !config.Get().Chat().Ready() {
		fmt.Println("Aucune API configurée : ouvre l'interface, les réglages s'affichent.")
	}
	if os.Getenv("AJEAN_NO_BROWSER") == "" {
		openInBrowser(url)
	}

	srv := &http.Server{Handler: server.New(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
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
