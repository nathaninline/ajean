package ajean

// relay_link.go — `ajean link <token>` : connecte ce serveur Jean au relais public
// (ajean.link) par une connexion SORTANTE persistante, pour qu'un utilisateur
// y accède depuis n'importe où sans ouvrir de port (CGNAT, box, etc.).
//
// Principe : l'agent ouvre un WebSocket vers le relais, l'authentifie avec le
// token d'abonnement, puis multiplexe (yamux) ce lien unique en un stream par
// requête navigateur. Chaque stream est reverse-proxyfié vers le `ajean web`
// local. Keepalive + reconnexion automatique avec backoff.
//
// Le token est fourni par la boutique à l'achat. Il est mémorisé dans
// $JEAN_HOME/.link_token pour que `ajean link` (sans argument) reprenne la
// connexion.

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"
)

// defaultRelayURL est l'endpoint WebSocket du relais. Surchageable via
// $JEAN_LINK_URL (utile pour tester contre un relais local).
const defaultRelayURL = "wss://ajean.link/agent"

func linkTokenPath() string   { return filepath.Join(AjeanHome(), ".link_token") }
func linkMachinePath() string { return filepath.Join(AjeanHome(), ".link_machine") }

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
	_ = os.MkdirAll(AjeanHome(), 0o755)
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
	if err := os.MkdirAll(AjeanHome(), 0o755); err != nil {
		return err
	}
	return os.WriteFile(linkTokenPath(), []byte(tok+"\n"), 0o600)
}

// removeLinkToken oublie la clé de liaison enregistrée (idempotent).
func removeLinkToken() error {
	if err := os.Remove(linkTokenPath()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// relayURL resolves the relay WebSocket endpoint (env override → default).
func relayURL() string {
	if u := envAny("AJEAN_LINK_URL", "JEAN_LINK_URL"); u != "" {
		return u
	}
	return defaultRelayURL
}

// linkUnitName / legacyLinkUnitName : l'unité systemd qui exécute le worker
// « ajean link --foreground », et son nom d'avant le renommage.
const (
	linkUnitName       = "ajean-link"
	legacyLinkUnitName = "jean-link"
)

// linkServiceName renvoie l'unité de lien réellement installée sur la machine
// (voir sys_unitname.go : un serveur mis à jour tourne encore sous jean-link).
func linkServiceName() string { return resolveUnitName(linkUnitName, legacyLinkUnitName) }

func cmdLink(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "":
		// `ajean link` seul : afficher l'aide des sous-commandes (NE démarre pas —
		// éviter de prendre un mot pour un token et d'écraser le vrai).
		printLinkHelp()
		return nil
	case "serve", "--foreground", "fg":
		// Le worker réel (boucle de connexion au relais), pendant de `ajean serve`.
		// C'est ce que lance l'unité systemd ; il NE doit PAS rendre la main.
		return runLinkForeground()
	case "start":
		return startLink(false)
	case "status":
		tok := readLinkToken()
		if tok == "" {
			fmt.Println(yellow("[info]") + " aucun token enregistré — lance: ajean link <token>")
			return nil
		}
		fmt.Printf("%s token enregistré (%s…), relais: %s\n", green("[ok]"), tok[:min(8, len(tok))], relayURL())
		if linkServiceActive() {
			fmt.Printf("%s service %s: actif\n", green("[ok]"), linkServiceName())
		} else {
			fmt.Printf("%s service %s: arrêté (ajean link pour démarrer)\n", yellow("[info]"), linkServiceName())
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
			return fmt.Errorf("génération du code (droits sur %s ?): %w", AjeanHome(), err)
		}
		fmt.Printf("%s code d'appairage (valable 10 min, à usage unique) :\n       %s\n", green("[link]"), bold(code))
		return nil
	}

	// Un argument restant n'est traité comme TOKEN que s'il en a la forme (`jl_…`).
	// Sinon c'est une faute de frappe / sous-commande inconnue : on REFUSE, sans
	// jamais écraser le token enregistré (le bug qui rendait le serveur injoignable).
	if !strings.HasPrefix(sub, "jl_") {
		fmt.Fprintf(os.Stderr, "%s sous-commande inconnue : %q\n\n", yellow("[link]"), sub)
		printLinkHelp()
		return fmt.Errorf("sous-commande link inconnue: %s", sub)
	}
	if err := saveLinkToken(strings.TrimSpace(sub)); err != nil {
		return err
	}
	// Nouveau token → on (re)démarre le worker pour qu'il le prenne en compte.
	return startLink(true)
}

// startLink démarre (ou redémarre si force) le service de lien puis affiche
// l'identité (empreinte + code). Si le service tourne déjà et qu'on ne force pas,
// on le signale au lieu d'un faux « démarré ».
func startLink(force bool) error {
	if readLinkToken() == "" {
		return fmt.Errorf("aucun token. Usage: ajean link <token>  (token fourni à l'achat sur la boutique)")
	}
	switch {
	case force:
		if err := linkServiceCtl("restart"); err != nil {
			return err
		}
	case linkServiceActive():
		fmt.Printf("%s service %s déjà en cours — « ajean link restart » pour le relancer\n", yellow("[info]"), linkServiceName())
	default:
		if err := linkServiceCtl("start"); err != nil {
			return err
		}
	}
	return linkPrintIdentity()
}

// printLinkHelp liste les sous-commandes de `ajean link`.
func printLinkHelp() {
	fmt.Print(`ajean link — accès distant via le relais ajean.link

Usage :
  ajean link <token>     enregistre le token (1re fois / pour le changer) et démarre le lien
  ajean link start       démarre le lien en arrière-plan (service)
  ajean link restart     redémarre le service de lien
  ajean link stop        arrête le service de lien
  ajean link status      état du token et du service
  ajean link code        génère un code d'appairage (valable 10 min, à usage unique)
  ajean link logout      oublie le token enregistré

Le token est fourni sur ajean.link. « ajean link » seul affiche cette aide.
`)
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
		fmt.Printf("%s code d'appairage indisponible (droits sur %s ?): %v\n", yellow("[link]"), AjeanHome(), err)
		fmt.Printf("       Réessaie : sudo ajean link code\n")
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
		return fmt.Errorf("aucun token. Usage: ajean link <token>")
	}
	fmt.Printf("%s connexion au relais %s …\n", cyan("[link]"), relayURL())
	fmt.Printf("       (UI AJEAN + endpoint OpenAI servis dans le tunnel — pas besoin de 'ajean web')\n")
	if fp := e2eFingerprint(); fp != "" {
		fmt.Printf("%s empreinte E2E : %s   (code d'appairage : ajean link code)\n", green("[e2e]"), bold(fp))
	}

	// UNE seule instance du mux web → UNE seule conversation en mémoire, PARTAGÉE
	// entre l'UI locale (:8090, jean.example.org) et le tunnel (app.ajean.link). Avant,
	// `ajean web` et `ajean link` étaient deux process distincts avec chacun leur
	// historique → confusion. Désormais le process jean-link est l'unique
	// propriétaire de la conversation et sert les DEUX surfaces.
	webMux := newWebMux()
	go serveLocalWebMux(webMux)
	// On construit le handler une seule fois ; il est servi à travers chaque tunnel.
	handler := newLinkHandler(webMux)
	// Front TLS de l'accès OpenAI public. Toujours prêt ; c'est le drapeau
	// oaiPublicEnabled (piloté par l'UI, lu en direct au démux) qui autorise ou non
	// le trafic. Cert obtenu à la demande via TLS-ALPN-01 à travers le tunnel.
	oaiTLS := oaiTLSConfig()
	fmt.Printf("%s front OpenAI public prêt (activation en direct via l'UI ; état: %v)\n", green("[oai]"), oaiPublicEnabled())

	backoff := time.Second
	for {
		err := runLinkSession(token, handler, oaiTLS)
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

// ---------------------------------------------------------------------------
// Tunnel DANS le process de l'app (macOS/Windows).
//
// Le modèle Linux est : UN SEUL process possède la conversation et sert les deux
// surfaces (UI locale :8090 + tunnel). C'est ce qui rend le fil identique en
// local et sur app.ajean.link — la conversation est un objet EN MÉMOIRE (voir
// chat_conversation.go), simplement persisté sur disque ; deux process qui la
// servent = deux fils divergents qui s'écrasent dans conversation.json.
//
// Sur un poste de bureau, l'app EST ce process propriétaire : elle sert déjà
// :8090, donc elle fait aussi tourner le tunnel elle-même au lieu de déléguer à
// un worker détaché. L'accès distant vit donc aussi longtemps que Jean.app est
// ouverte — sur un portable qui s'endort, c'est de toute façon la réalité.

var appLink struct {
	mu      sync.Mutex
	running bool
	stop    chan struct{}
}

// appOwnsLink : vrai dans le process de l'app de bureau, qui pilote le tunnel
// en interne. linkServiceCtl s'y adapte pour ne PAS lancer de worker concurrent.
// appWebMux est le mux servi par l'app — réutilisé pour le tunnel afin que les
// deux surfaces partagent la même conversation.
var (
	appOwnsLink bool
	appWebMux   *http.ServeMux
)

// startAppLink démarre la boucle de lien dans ce process, en servant le mux de
// l'app (donc la MÊME conversation que l'UI locale). Idempotent.
func startAppLink(mux *http.ServeMux) {
	appLink.mu.Lock()
	defer appLink.mu.Unlock()
	if appLink.running {
		return
	}
	token := readLinkToken()
	if token == "" {
		return
	}
	stop := make(chan struct{})
	appLink.running, appLink.stop = true, stop
	handler := newLinkHandler(mux)
	oaiTLS := oaiTLSConfig()
	go func() {
		backoff := time.Second
		for {
			select {
			case <-stop:
				return
			default:
			}
			_ = runLinkSession(token, handler, oaiTLS)
			select {
			case <-stop:
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff *= 2
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
			}
		}
	}()
}

// stopAppLink arrête la boucle interne (la session en cours se termine d'elle-même).
func stopAppLink() {
	appLink.mu.Lock()
	defer appLink.mu.Unlock()
	if !appLink.running {
		return
	}
	close(appLink.stop)
	appLink.running = false
}

func appLinkRunning() bool {
	appLink.mu.Lock()
	defer appLink.mu.Unlock()
	return appLink.running
}

// killForeignLinkWorker tue un worker de lien détaché encore en vie (laissé par
// une version antérieure ou une autre copie de l'app). Indispensable AVANT que
// l'app ne prenne la main : sinon deux process servent la même conversation et
// les fils divergent entre l'UI locale et app.ajean.link.
func killForeignLinkWorker() {
	if pid := linkUserPID(); pid > 0 {
		killTree(pid)
		_ = os.Remove(linkPIDPath())
	}
}

// serveLocalWebMux sert l'UI web locale sur :8090 avec le mux fourni — le MÊME
// que celui exposé dans le tunnel. Le process jean-link devient ainsi l'unique
// propriétaire de la conversation, partagée entre l'accès local et l'accès
// distant. Remplace le `ajean web` autonome (à ne plus lancer séparément).
func serveLocalWebMux(mux *http.ServeMux) {
	addr := "0.0.0.0:8090"
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		fmt.Printf("%s UI locale non démarrée (%s occupé ? tue le vieux « ajean web ») : %v\n", yellow("[web]"), addr, err)
		return
	}
	fmt.Printf("%s UI locale sur http://%s (même conversation que le tunnel)\n", green("[web]"), addr)
	_ = http.Serve(ln, mux)
}

// linkPIDPath / linkLogPath : suivi du worker de lien hors systemd (macOS,
// Windows), où Jean est une app de bureau lancée sans droits root.
func linkPIDPath() string { return adoptLegacyLinkFile(".pid") }
func linkLogPath() string { return adoptLegacyLinkFile(".log") }

// adoptLegacyLinkFile renvoie le chemin « ajean-link<ext> » en reprenant au
// passage le « jean-link<ext> » laissé par une version antérieure.
//
// Reprendre le fichier PID n'est pas cosmétique : c'est LUI qui dit si un worker
// tourne déjà. Sans cette reprise, la première version renommée ne verrait plus
// le worker en cours, en démarrerait un second, et la machine se retrouverait
// avec deux tunnels concurrents vers le relais. Idempotent, silencieux en cas
// d'échec (on retombe simplement sur le nouveau nom).
func adoptLegacyLinkFile(ext string) string {
	newPath := filepath.Join(AjeanHome(), linkUnitName+ext)
	if _, err := os.Stat(newPath); err == nil {
		return newPath
	}
	legacy := filepath.Join(AjeanHome(), legacyLinkUnitName+ext)
	if _, err := os.Stat(legacy); err != nil {
		return newPath
	}
	_ = os.Rename(legacy, newPath)
	return newPath
}

// linkServiceCtl pilote le worker de lien (start/stop/restart). Sous Linux c'est
// l'unité systemd jean-link (avec sudo non interactif si on n'est pas root) ;
// ailleurs — macOS et Windows, où il n'y a ni systemd ni droits root — on lance
// « ajean link serve » en processus détaché suivi par un fichier PID, comme le
// fait déjà le service principal. Sans ça, l'accès distant restait définitivement
// « arrêté » sur un Mac ou un PC : le token était enregistré mais rien ne
// composait jamais le tunnel.
func linkServiceCtl(action string) error {
	if runtime.GOOS != "linux" {
		// Dans l'app de bureau, le tunnel tourne DANS ce process (conversation
		// unique) : on ne lance surtout pas un worker séparé.
		if appOwnsLink {
			switch action {
			case "stop":
				stopAppLink()
			case "start", "restart":
				stopAppLink()
				killForeignLinkWorker()
				startAppLink(appWebMux)
				if !appLinkRunning() {
					return fmt.Errorf("tunnel non démarré (clé de liaison absente ?)")
				}
			default:
				return fmt.Errorf("action inconnue: %s", action)
			}
			return nil
		}
		return linkUserSvcCtl(action)
	}
	bin, pre := "systemctl", []string{}
	if os.Geteuid() != 0 {
		bin, pre = "sudo", []string{"-n", "systemctl"}
	}
	cmd := exec.Command(bin, append(pre, action, linkServiceName())...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl %s %s: %w", action, linkServiceName(), err)
	}
	switch action {
	case "start", "restart":
		fmt.Printf("%s service %s %s\n", green("[ok]"), linkServiceName(), action+"é")
	case "stop":
		fmt.Printf("%s service %s arrêté\n", green("[ok]"), linkServiceName())
	}
	return nil
}

// linkServiceActive indique si le worker de lien tourne (unité systemd sous
// Linux, processus suivi par fichier PID ailleurs).
func linkServiceActive() bool {
	if runtime.GOOS != "linux" {
		if appOwnsLink {
			return appLinkRunning()
		}
		return linkUserPID() > 0
	}
	out, _ := exec.Command("systemctl", "is-active", linkServiceName()).Output()
	return strings.TrimSpace(string(out)) == "active"
}

// linkUserPID renvoie le PID du worker de lien s'il tourne vraiment, 0 sinon
// (fichier absent, illisible, ou process mort → on nettoie le fichier obsolète).
func linkUserPID() int {
	b, err := os.ReadFile(linkPIDPath())
	if err != nil {
		return 0
	}
	if i := strings.IndexByte(string(b), '\n'); i >= 0 {
		b = b[:i]
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(b)))
	if pid <= 0 || !pidAlive(pid) {
		return 0
	}
	return pid
}

// linkUserSvcCtl : équivalent de systemctl pour les OS sans systemd.
func linkUserSvcCtl(action string) error {
	switch action {
	case "stop", "restart":
		if pid := linkUserPID(); pid > 0 {
			killTree(pid)
			for i := 0; i < 30 && pidAlive(pid); i++ {
				time.Sleep(100 * time.Millisecond)
			}
		}
		_ = os.Remove(linkPIDPath())
		if action == "stop" {
			fmt.Printf("%s service %s arrêté\n", green("[ok]"), linkServiceName())
			return nil
		}
	case "start":
		if linkUserPID() > 0 {
			fmt.Printf("%s service %s déjà en cours\n", yellow("[info]"), linkServiceName())
			return nil
		}
	default:
		return fmt.Errorf("action inconnue: %s", action)
	}

	self, err := os.Executable()
	if err != nil {
		return err
	}
	if p, err := filepath.EvalSymlinks(self); err == nil {
		self = p
	}
	if err := os.MkdirAll(AjeanHome(), 0o755); err != nil {
		return err
	}
	logf, err := os.OpenFile(linkLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("ouverture du log %s: %w", linkLogPath(), err)
	}
	defer logf.Close()

	cmd := spawnDetached(self, "link", "serve")
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("démarrage de « ajean link serve »: %w", err)
	}
	pid := cmd.Process.Pid
	// PID + binaire d'origine : la 2e ligne dit QUELLE copie de l'app a lancé ce
	// worker — précieux pour diagnostiquer un process rescapé d'une autre version.
	if err := os.WriteFile(linkPIDPath(), []byte(strconv.Itoa(pid)+"\n"+self+"\n"), 0o644); err != nil {
		return fmt.Errorf("écriture du PID: %w", err)
	}
	_ = cmd.Process.Release()

	// Le worker peut mourir aussitôt (token refusé, relais injoignable) : on laisse
	// passer un instant avant de déclarer la victoire, sinon l'UI afficherait
	// « en ligne » sur un process déjà mort.
	time.Sleep(1500 * time.Millisecond)
	if !pidAlive(pid) {
		_ = os.Remove(linkPIDPath())
		return fmt.Errorf("le lien s'est arrêté aussitôt — voir %s", linkLogPath())
	}
	fmt.Printf("%s service %s démarré (PID %d)\n", green("[ok]"), linkServiceName(), pid)
	return nil
}

// newLinkHandler construit le handler servi à travers le tunnel :
//   - /v1/*, /health, /props, /metrics, /slots → llama-server local (endpoint
//     compatible OpenAI, avec injection de la clé API locale) → permet de
//     brancher OpenCode, Hermes, etc. sur ajean.link/oai/<machine>/v1
//   - tout le reste → l'UI web de Jean (avec injection de la clé de pilotage)
func newLinkHandler(mux *http.ServeMux) http.Handler {
	web := withLocalAuth(mux)
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
func runLinkSession(token string, handler http.Handler, oaiTLS *tls.Config) error {
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

	// Chaque Accept() = un stream. Deux natures possibles :
	//   - requête HTTP normale (UI Jean / E2E) → servie par `handler` ;
	//   - session TLS brute (accès OpenAI public) → terminée par le front TLS.
	// On les distingue au 1er octet (0x16 = handshake TLS). Sans OAI activé, tout
	// va au HTTP (comportement historique inchangé).
	httpLn := newChanListener(sess.Addr())
	defer httpLn.Close()
	srv := &http.Server{Handler: handler}
	go srv.Serve(httpLn)

	var oaiLn *chanListener
	if oaiTLS != nil {
		oaiLn = newChanListener(sess.Addr())
		defer oaiLn.Close()
		go runOAIFront(oaiLn, oaiTLS)
	}

	for {
		stream, err := sess.Accept()
		if err != nil {
			return fmt.Errorf("accept: %w", err)
		}
		go demuxTunnelStream(stream, httpLn, oaiLn)
	}
}

// demuxTunnelStream aiguille un stream du tunnel selon son 1er octet : 0x16 (TLS)
// → front OAI ; sinon → HTTP. On consulte l'octet sans le consommer (peekedConn).
func demuxTunnelStream(stream net.Conn, httpLn, oaiLn *chanListener) {
	br := bufio.NewReader(stream)
	b, err := br.Peek(1)
	if err != nil {
		stream.Close()
		return
	}
	pc := &peekedConn{Conn: stream, r: br}
	if b[0] == 0x16 { // handshake TLS = accès OpenAI public
		if oaiLn != nil && oaiPublicEnabled() {
			oaiLn.push(pc)
		} else {
			stream.Close() // public désactivé → on refuse (fail-closed)
		}
		return
	}
	httpLn.push(pc)
}
