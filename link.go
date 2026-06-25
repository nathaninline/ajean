package main

// link.go — `jean link <token>` : connecte ce serveur Jean au relais public
// (ajean.link) par une connexion SORTANTE persistante, pour qu'un utilisateur
// y accède depuis n'importe où sans ouvrir de port (CGNAT, box, etc.).
//
// Principe : l'agent ouvre un WebSocket vers le relais, l'authentifie avec le
// token d'abonnement, puis multiplexe (yamux) ce lien unique en un stream par
// requête navigateur. Chaque stream est reverse-proxyfié vers le `jean web`
// local. Keepalive + reconnexion automatique avec backoff.
//
// Le token est fourni par la boutique à l'achat. Il est mémorisé dans
// $JEAN_HOME/.link_token pour que `jean link` (sans argument) reprenne la
// connexion.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// defaultRelayURL est l'endpoint WebSocket du relais. Surchageable via
// $JEAN_LINK_URL (utile pour tester contre un relais local).
const defaultRelayURL = "wss://ajean.link/agent"

func linkTokenPath() string   { return filepath.Join(JeanHome(), ".link_token") }
func linkMachinePath() string { return filepath.Join(JeanHome(), ".link_machine") }

// machineID returns a stable per-machine identifier, creating one on first use.
// Permet au relais de regrouper les connexions d'une même machine sous le compte.
func machineID() string {
	if b, err := os.ReadFile(linkMachinePath()); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id
		}
	}
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	id := hex.EncodeToString(buf)
	_ = os.MkdirAll(JeanHome(), 0o755)
	_ = os.WriteFile(linkMachinePath(), []byte(id+"\n"), 0o600)
	return id
}

// readLinkToken returns the saved subscription token, or "".
func readLinkToken() string {
	b, err := os.ReadFile(linkTokenPath())
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

func saveLinkToken(tok string) error {
	if err := os.MkdirAll(JeanHome(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(linkTokenPath(), []byte(tok+"\n"), 0o600)
}

// relayURL resolves the relay WebSocket endpoint (env override → default).
func relayURL() string {
	if u := strings.TrimSpace(os.Getenv("JEAN_LINK_URL")); u != "" {
		return u
	}
	return defaultRelayURL
}

// linkServiceName est l'unité systemd qui exécute le worker « jean link --foreground ».
const linkServiceName = "jean-link"

func cmdLink(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "serve", "--foreground", "fg":
		// Le worker réel (boucle de connexion au relais), pendant de `jean serve`.
		// C'est ce que lance l'unité systemd ; il NE doit PAS rendre la main.
		return runLinkForeground()
	case "status":
		tok := readLinkToken()
		if tok == "" {
			fmt.Println(yellow("[info]") + " aucun token enregistré — lance: jean link <token>")
			return nil
		}
		fmt.Printf("%s token enregistré (%s…), relais: %s\n", green("[ok]"), tok[:min(8, len(tok))], relayURL())
		if linkServiceActive() {
			fmt.Printf("%s service %s: actif\n", green("[ok]"), linkServiceName)
		} else {
			fmt.Printf("%s service %s: arrêté (jean link pour démarrer)\n", yellow("[info]"), linkServiceName)
		}
		return nil
	case "logout":
		_ = os.Remove(linkTokenPath())
		fmt.Println(green("[ok]") + " token supprimé")
		return nil
	case "stop":
		return linkServiceCtl("stop")
	case "restart":
		if err := linkServiceCtl("restart"); err != nil {
			return err
		}
		return linkPrintIdentity()
	case "code":
		code, err := newPairCode()
		if err != nil {
			return fmt.Errorf("génération du code (droits sur %s ?): %w", JeanHome(), err)
		}
		fmt.Printf("%s code d'appairage (valable 10 min, à usage unique) :\n       %s\n", green("[link]"), bold(code))
		return nil
	}

	// Sans sous-commande : (optionnellement) enregistrer le token, puis DÉMARRER le
	// service en arrière-plan (ne bloque pas le terminal) et afficher empreinte + code.
	gotToken := sub != ""
	if gotToken {
		if err := saveLinkToken(strings.TrimSpace(sub)); err != nil {
			return err
		}
	}
	if readLinkToken() == "" {
		return fmt.Errorf("aucun token. Usage: jean link <token>  (token fourni à l'achat sur la boutique)")
	}
	// Un nouveau token n'est pris en compte qu'au redémarrage du worker.
	action := "start"
	if gotToken {
		action = "restart"
	}
	if err := linkServiceCtl(action); err != nil {
		return err
	}
	return linkPrintIdentity()
}

// linkPrintIdentity affiche l'empreinte E2E (à confirmer une fois) et un code
// d'appairage frais (à saisir une fois) pour le portail.
func linkPrintIdentity() error {
	if fp := e2eFingerprint(); fp != "" {
		fmt.Printf("\n%s empreinte E2E de cette machine :\n       %s\n", green("[e2e]"), bold(fp))
		fmt.Printf("       Confirme-la dans le portail (Mon compte → serveur) pour activer la boîte noire.\n")
	}
	code, err := newPairCode()
	if err != nil {
		fmt.Printf("%s code d'appairage indisponible (droits sur %s ?): %v\n", yellow("[link]"), JeanHome(), err)
		fmt.Printf("       Réessaie : sudo jean link code\n")
		return nil
	}
	fmt.Printf("       Code d'appairage (valable 10 min, usage unique) : %s\n\n", bold(code))
	return nil
}

// runLinkForeground exécute la boucle de connexion au relais (le worker supervisé
// par systemd). Reconnexion automatique avec backoff. Ne rend jamais la main.
func runLinkForeground() error {
	token := readLinkToken()
	if token == "" {
		return fmt.Errorf("aucun token. Usage: jean link <token>")
	}
	fmt.Printf("%s connexion au relais %s …\n", cyan("[link]"), relayURL())
	fmt.Printf("       (UI Jean + endpoint OpenAI servis dans le tunnel — pas besoin de 'jean web')\n")
	if fp := e2eFingerprint(); fp != "" {
		fmt.Printf("%s empreinte E2E : %s   (code d'appairage : jean link code)\n", green("[e2e]"), bold(fp))
	}

	// On construit le handler une seule fois ; il est servi à travers chaque tunnel.
	handler := newLinkHandler()

	backoff := time.Second
	for {
		err := runLinkSession(token, handler)
		if err != nil {
			fmt.Printf("%s lien perdu: %v — reconnexion dans %s\n", yellow("[link]"), err, backoff)
		}
		time.Sleep(backoff)
		if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
	}
}

// linkServiceCtl pilote l'unité systemd jean-link (start/stop/restart), avec sudo
// non interactif si on n'est pas root. Linux uniquement (le relais cible est Linux).
func linkServiceCtl(action string) error {
	if runtime.GOOS != "linux" {
		return fmt.Errorf("gestion du service %s : Linux/systemd uniquement (lance « jean link --foreground » directement)", linkServiceName)
	}
	bin, pre := "systemctl", []string{}
	if os.Geteuid() != 0 {
		bin, pre = "sudo", []string{"-n", "systemctl"}
	}
	cmd := exec.Command(bin, append(pre, action, linkServiceName)...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl %s %s: %w", action, linkServiceName, err)
	}
	switch action {
	case "start", "restart":
		fmt.Printf("%s service %s %s\n", green("[ok]"), linkServiceName, action+"é")
	case "stop":
		fmt.Printf("%s service %s arrêté\n", green("[ok]"), linkServiceName)
	}
	return nil
}

// linkServiceActive indique si l'unité systemd jean-link tourne.
func linkServiceActive() bool {
	if runtime.GOOS != "linux" {
		return false
	}
	out, _ := exec.Command("systemctl", "is-active", linkServiceName).Output()
	return strings.TrimSpace(string(out)) == "active"
}

// newLinkHandler construit le handler servi à travers le tunnel :
//   - /v1/*, /health, /props, /metrics, /slots → llama-server local (endpoint
//     compatible OpenAI, avec injection de la clé API locale) → permet de
//     brancher OpenCode, Hermes, etc. sur ajean.link/oai/<machine>/v1
//   - tout le reste → l'UI web de Jean (avec injection de la clé de pilotage)
func newLinkHandler() http.Handler {
	web := withLocalAuth(newWebMux())
	llama := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", LLMPort())}
	lp := httputil.NewSingleHostReverseProxy(llama)
	lp.FlushInterval = -1 // streaming SSE des complétions
	apiKey := readAPIKey()
	base := lp.Director
	lp.Director = func(req *http.Request) {
		base(req)
		// Le client distant n'a pas la clé API de llama-server ; on l'injecte ici
		// (l'auth réelle est faite par le relais via la clé de liaison du compte).
		if apiKey != "" {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		}
	}
	lp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		http.Error(w, "llama-server injoignable: "+e.Error(), http.StatusBadGateway)
	}
	// Boîte noire « zéro exception » : TOUTE l'API (chat ET contrôle) ne transite
	// que chiffrée de bout en bout. Le relais ne voit jamais de clair.
	//   - /api/e2e/chat : chat chiffré (streaming SSE chiffré).
	//   - /api/e2e/req  : proxy de contrôle chiffré (presets, VRAM, skills, service…).
	// Tout autre /api/* en clair est REFUSÉ via le tunnel.
	oaiAllowed := os.Getenv("JEAN_LINK_ALLOW_OAI") == "1"
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/api/e2e/") {
			if p == "/api/e2e/req" {
				handleE2EReq(w, r, web) // dispatche dans le handler local authentifié
				return
			}
			if p == "/api/e2e/pair" {
				handleE2EPair(w, r) // appairage d'une identité utilisateur (code hors-bande)
				return
			}
			web.ServeHTTP(w, r) // /api/e2e/chat est routé par le mux
			return
		}
		if strings.HasPrefix(p, "/api/") {
			http.Error(w, "via le relais : seuls les endpoints chiffrés /api/e2e/* sont autorisés (boîte noire)", http.StatusForbidden)
			return
		}
		if strings.HasPrefix(p, "/v1") || p == "/health" || p == "/props" || p == "/metrics" || strings.HasPrefix(p, "/slots") {
			// L'endpoint OpenAI (OpenCode/Hermes) ne peut pas être chiffré navigateur :
			// il transiterait en clair par le relais. Désactivé par défaut.
			if !oaiAllowed {
				http.Error(w, "endpoint OpenAI désactivé via le relais (transiterait en clair) ; JEAN_LINK_ALLOW_OAI=1 pour l'autoriser", http.StatusForbidden)
				return
			}
			lp.ServeHTTP(w, r)
			return
		}
		web.ServeHTTP(w, r)
	})
}

// withLocalAuth injecte la clé de pilotage locale dans chaque requête arrivant
// par le tunnel. Le navigateur distant ne connaît que le token (vérifié par le
// relais) ; c'est ici, en local, qu'on satisfait l'auth de l'API web sans
// exposer la clé au client.
func withLocalAuth(next http.Handler) http.Handler {
	webKey := readWebKey()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if webKey != "" && r.Header.Get("Authorization") == "" {
			r.Header.Set("Authorization", "Bearer "+webKey)
		}
		next.ServeHTTP(w, r)
	})
}

// runLinkSession opens one WebSocket→yamux session and serves it until it dies.
// It blocks for the lifetime of the connection and returns the error that ended
// it (so the caller can reconnect).
func runLinkSession(token string, handler http.Handler) error {
	ctx := context.Background()
	dialCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	// Identifie la machine auprès du relais (id stable + hostname).
	host, _ := os.Hostname()
	dialURL := relayURL()
	q := url.Values{"m": {machineID()}, "h": {host}}
	if pk := e2ePubHex(); pk != "" {
		q.Set("pk", pk) // clé publique E2E → publiée au relais pour le scellement navigateur
	}
	if strings.Contains(dialURL, "?") {
		dialURL += "&" + q.Encode()
	} else {
		dialURL += "?" + q.Encode()
	}

	c, _, err := websocket.Dial(dialCtx, dialURL, &websocket.DialOptions{
		HTTPHeader: http.Header{"Authorization": {"Bearer " + token}},
	})
	if err != nil {
		return fmt.Errorf("dial: %w", err)
	}
	// Pas de limite de taille : on fait passer des streams arbitraires (SSE).
	c.SetReadLimit(-1)
	conn := websocket.NetConn(ctx, c, websocket.MessageBinary)

	// L'agent est le côté "serveur" yamux : c'est le relais qui ouvre un stream
	// par requête navigateur, et nous on les accepte.
	ycfg := yamux.DefaultConfig()
	ycfg.EnableKeepAlive = true
	ycfg.KeepAliveInterval = 25 * time.Second
	ycfg.ConnectionWriteTimeout = 30 * time.Second
	ycfg.LogOutput = io.Discard
	sess, err := yamux.Server(conn, ycfg)
	if err != nil {
		return fmt.Errorf("yamux: %w", err)
	}
	defer sess.Close()

	fmt.Printf("%s lien établi ✓\n", green("[link]"))

	// sess implémente net.Listener : chaque Accept() = un stream = une requête.
	// On sert directement l'UI Jean (le handler gère lui-même le flush SSE).
	srv := &http.Server{Handler: handler}
	return srv.Serve(sess)
}

