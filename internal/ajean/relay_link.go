package ajean

// relay_link.go — `ajean link <token>` : connecte ce serveur AJEAN au relais public
// (ajean.link) par une connexion SORTANTE persistante, pour qu'un utilisateur
// y accède depuis n'importe où sans ouvrir de port (CGNAT, box, etc.).
//
// Principe : l'agent ouvre un WebSocket vers le relais, l'authentifie avec le
// token d'abonnement, puis multiplexe (yamux) ce lien unique en un stream par
// requête navigateur. Chaque stream est reverse-proxyfié vers le `ajean web`
// local. Keepalive + reconnexion automatique avec backoff.
//
// Le token est fourni par la boutique à l'achat. Il est mémorisé dans
// $AJEAN_HOME/.link_token pour que `ajean link` (sans argument) reprenne la
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
// $AJEAN_LINK_URL (utile pour tester contre un relais local).
const defaultRelayURL = "wss://ajean.link/agent"

// machineID renvoie un identifiant stable de la machine, créé au premier appel.
// Permet au relais de regrouper les connexions d'une même machine sous le compte.
func machineID() string {
	if id := getStr(bkState, "link_machine"); id != "" {
		return id
	}
	buf := make([]byte, 8)
	_, _ = rand.Read(buf)
	id := hex.EncodeToString(buf)
	_ = putStr(bkState, "link_machine", id)
	return id
}

// readLinkToken renvoie la clé d'abonnement enregistrée, ou "".
func readLinkToken() string { return getStr(bkState, "link_token") }

func saveLinkToken(tok string) error { return putStr(bkState, "link_token", tok) }

// removeLinkToken oublie la clé de liaison enregistrée (idempotent).
func removeLinkToken() error { return putStr(bkState, "link_token", "") }

// relayURL resolves the relay WebSocket endpoint (env override → default).
func relayURL() string {
	if u := os.Getenv("AJEAN_LINK_URL"); u != "" {
		return u
	}
	return defaultRelayURL
}

// uiUnitName est l'unité qui exécute « ajean web » : l'UI locale, le tunnel du
// relais et l'endpoint OpenAI, dans un seul et même process.
const uiUnitName = "ajean-ui"

func uiServiceName() string { return uiUnitName }

// cmdLink ne gère QUE le compte d'accès distant : le jeton, l'appairage, l'état.
// Le tunnel n'est plus un service à part — il est ouvert par « ajean web » dès
// qu'un jeton est enregistré. Piloter le tunnel, c'est donc piloter le service
// d'interface (voir cmdUI).
func cmdLink(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "":
		// `ajean link` seul : afficher l'aide (NE démarre rien — éviter de prendre
		// un mot pour un jeton et d'écraser le vrai).
		printLinkHelp()
		return nil
	case "status":
		tok := readLinkToken()
		if tok == "" {
			fmt.Println(yellow("[info]") + " aucun jeton enregistré — lance: ajean link <token>")
			return nil
		}
		fmt.Printf("%s jeton enregistré (%s…), relais: %s\n", green("[ok]"), tok[:min(8, len(tok))], relayURL())
		if uiServiceActive() {
			fmt.Printf("%s service %s actif — le tunnel est ouvert\n", green("[ok]"), uiServiceName())
		} else {
			fmt.Printf("%s service %s arrêté — pas de tunnel (ajean ui start)\n", yellow("[info]"), uiServiceName())
		}
		return nil
	case "logout":
		if err := removeLinkToken(); err != nil {
			return err
		}
		fmt.Println(green("[ok]") + " jeton supprimé — « ajean ui restart » pour fermer le tunnel")
		return nil
	case "code":
		code, err := newPairCode()
		if err != nil {
			return fmt.Errorf("génération du code (droits sur %s ?): %w", AjeanHome(), err)
		}
		fmt.Printf("%s code d'appairage (valable 10 min, à usage unique) :\n       %s\n", green("[link]"), bold(code))
		return nil
	}

	// Un argument restant n'est traité comme JETON que s'il en a la forme (`jl_…`).
	// Sinon c'est une faute de frappe / sous-commande inconnue : on REFUSE, sans
	// jamais écraser le jeton enregistré (le bug qui rendait le serveur injoignable).
	if !strings.HasPrefix(sub, "jl_") {
		fmt.Fprintf(os.Stderr, "%s sous-commande inconnue : %q\n\n", yellow("[link]"), sub)
		printLinkHelp()
		return fmt.Errorf("sous-commande link inconnue: %s", sub)
	}
	if err := saveLinkToken(strings.TrimSpace(sub)); err != nil {
		return err
	}
	// Nouveau jeton : le service d'interface doit le relire pour ouvrir le tunnel.
	if err := uiServiceCtl("restart"); err != nil {
		return err
	}
	return linkPrintIdentity()
}

// cmdUI pilote le service d'interface — « ajean web » en arrière-plan : l'UI
// locale, le tunnel du relais et l'endpoint OpenAI, servis par le MÊME process.
// C'est ce qui garantit une conversation unique, identique en local et à distance.
func cmdUI(args []string) error {
	action := "status"
	if len(args) > 0 && args[0] != "" {
		action = args[0]
	}
	switch action {
	case "start", "stop", "restart":
		return uiServiceCtl(action)
	case "status":
		if uiServiceActive() {
			fmt.Printf("%s service %s : actif\n", green("[ok]"), uiServiceName())
		} else {
			fmt.Printf("%s service %s : arrêté\n", yellow("[info]"), uiServiceName())
		}
		if readLinkToken() != "" {
			fmt.Printf("  accès distant : jeton enregistré — le tunnel s'ouvre avec le service\n")
		}
		return nil
	default:
		return fmt.Errorf("usage: ajean ui [start|stop|restart|status]")
	}
}

// printLinkHelp liste les sous-commandes de `ajean link`.
func printLinkHelp() {
	fmt.Print(`ajean link — accès distant via le relais ajean.link

Usage :
  ajean link <token>     enregistre le jeton (1re fois / pour le changer) et ouvre le tunnel
  ajean link status      état du jeton et du tunnel
  ajean link code        génère un code d'appairage (valable 10 min, à usage unique)
  ajean link logout      oublie le jeton enregistré

Le jeton est fourni sur ajean.link. Le tunnel est ouvert par le service
d'interface (« ajean ui ») dès qu'un jeton est enregistré : il n'y a pas de
service séparé pour l'accès distant.
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
// un worker détaché. L'accès distant vit donc aussi longtemps que AJEAN.app est
// ouverte — sur un portable qui s'endort, c'est de toute façon la réalité.

var appLink struct {
	mu      sync.Mutex
	running bool
	stop    context.CancelFunc
	done    chan struct{} // fermé quand la boucle a vraiment rendu la main
}

// appOwnsLink : vrai dans le process de l'app de bureau, qui pilote le tunnel
// en interne. uiServiceCtl s'y adapte pour ne PAS lancer de worker concurrent.
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
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	appLink.running, appLink.stop, appLink.done = true, cancel, done
	handler := newLinkHandler(mux)
	oaiTLS := oaiTLSConfig()
	go func() {
		defer close(done)
		backoff := time.Second
		for ctx.Err() == nil {
			started := time.Now()
			_ = runLinkSession(ctx, token, handler, oaiTLS)
			// Une session qui a TENU repart de zéro. Sans cette remise, le délai
			// grimpait de coupure en coupure et restait collé à 30 s pour toujours,
			// y compris pour reconnecter un lien qui venait de vivre des heures.
			if time.Since(started) > time.Minute {
				backoff = time.Second
			}
			select {
			case <-ctx.Done():
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

// stopAppLink arrête la boucle interne ET attend qu'elle ait rendu la main.
// L'attente est ce qui garantit qu'un redémarrage ne fait pas cohabiter deux
// sessions : sans elle, le nouveau tunnel s'ouvrait pendant que l'ancien vivait
// encore, et le relais voyait deux agents pour la même machine.
func stopAppLink() {
	appLink.mu.Lock()
	if !appLink.running {
		appLink.mu.Unlock()
		return
	}
	cancel, done := appLink.stop, appLink.done
	appLink.running, appLink.stop, appLink.done = false, nil, nil
	appLink.mu.Unlock()

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second): // filet : on ne bloque pas l'UI indéfiniment
	}
}

func appLinkRunning() bool {
	appLink.mu.Lock()
	defer appLink.mu.Unlock()
	return appLink.running
}

// killForeignUIWorker tue un worker de lien détaché encore en vie (laissé par
// une version antérieure ou une autre copie de l'app). Indispensable AVANT que
// l'app ne prenne la main : sinon deux process servent la même conversation et
// les fils divergent entre l'UI locale et app.ajean.link.
func killForeignUIWorker() {
	if pid := uiUserPID(); pid > 0 {
		killTree(pid)
		_ = os.Remove(uiPIDPath())
	}
}

// uiPIDPath / uiLogPath : suivi du worker de lien hors systemd (macOS,
// Windows), où AJEAN est une app de bureau lancée sans droits root.
func uiPIDPath() string { return filepath.Join(AjeanHome(), uiUnitName+".pid") }
func uiLogPath() string { return filepath.Join(AjeanHome(), uiUnitName+".log") }

// uiServiceCtl pilote le worker de lien (start/stop/restart). Sous Linux c'est
// l'unité systemd ajean-ui (avec sudo non interactif si on n'est pas root) ;
// ailleurs — macOS et Windows, où il n'y a ni systemd ni droits root — on lance
// « ajean link serve » en processus détaché suivi par un fichier PID, comme le
// fait déjà le service principal. Sans ça, l'accès distant restait définitivement
// « arrêté » sur un Mac ou un PC : le token était enregistré mais rien ne
// composait jamais le tunnel.
func uiServiceCtl(action string) error {
	if runtime.GOOS != "linux" {
		// Dans l'app de bureau, le tunnel tourne DANS ce process (conversation
		// unique) : on ne lance surtout pas un worker séparé.
		if appOwnsLink {
			switch action {
			case "stop":
				stopAppLink()
			case "start", "restart":
				stopAppLink()
				killForeignUIWorker()
				startAppLink(appWebMux)
				if !appLinkRunning() {
					return fmt.Errorf("tunnel non démarré (clé de liaison absente ?)")
				}
			default:
				return fmt.Errorf("action inconnue: %s", action)
			}
			return nil
		}
		return uiUserSvcCtl(action)
	}
	bin, pre := "systemctl", []string{}
	if os.Geteuid() != 0 {
		bin, pre = "sudo", []string{"-n", "systemctl"}
	}
	cmd := exec.Command(bin, append(pre, action, uiServiceName())...)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("systemctl %s %s: %w", action, uiServiceName(), err)
	}
	switch action {
	case "start", "restart":
		fmt.Printf("%s service %s %s\n", green("[ok]"), uiServiceName(), action+"é")
	case "stop":
		fmt.Printf("%s service %s arrêté\n", green("[ok]"), uiServiceName())
	}
	return nil
}

// uiServiceActive indique si le worker de lien tourne (unité systemd sous
// Linux, processus suivi par fichier PID ailleurs).
func uiServiceActive() bool {
	if runtime.GOOS != "linux" {
		if appOwnsLink {
			return appLinkRunning()
		}
		return uiUserPID() > 0
	}
	out, _ := exec.Command("systemctl", "is-active", uiServiceName()).Output()
	return strings.TrimSpace(string(out)) == "active"
}

// uiUserPID renvoie le PID du worker de lien s'il tourne vraiment, 0 sinon
// (fichier absent, illisible, ou process mort → on nettoie le fichier obsolète).
func uiUserPID() int {
	b, err := os.ReadFile(uiPIDPath())
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

// uiUserSvcCtl : équivalent de systemctl pour les OS sans systemd.
func uiUserSvcCtl(action string) error {
	switch action {
	case "stop", "restart":
		if pid := uiUserPID(); pid > 0 {
			killTree(pid)
			for i := 0; i < 30 && pidAlive(pid); i++ {
				time.Sleep(100 * time.Millisecond)
			}
		}
		_ = os.Remove(uiPIDPath())
		if action == "stop" {
			fmt.Printf("%s service %s arrêté\n", green("[ok]"), uiServiceName())
			return nil
		}
	case "start":
		if uiUserPID() > 0 {
			fmt.Printf("%s service %s déjà en cours\n", yellow("[info]"), uiServiceName())
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
	logf, err := os.OpenFile(uiLogPath(), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return fmt.Errorf("ouverture du log %s: %w", uiLogPath(), err)
	}
	defer logf.Close()

	cmd := spawnDetached(self, "web")
	cmd.Stdout, cmd.Stderr = logf, logf
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("démarrage de « ajean web »: %w", err)
	}
	pid := cmd.Process.Pid
	// PID + binaire d'origine : la 2e ligne dit QUELLE copie de l'app a lancé ce
	// worker — précieux pour diagnostiquer un process rescapé d'une autre version.
	if err := os.WriteFile(uiPIDPath(), []byte(strconv.Itoa(pid)+"\n"+self+"\n"), 0o644); err != nil {
		return fmt.Errorf("écriture du PID: %w", err)
	}
	_ = cmd.Process.Release()

	// Le worker peut mourir aussitôt (token refusé, relais injoignable) : on laisse
	// passer un instant avant de déclarer la victoire, sinon l'UI afficherait
	// « en ligne » sur un process déjà mort.
	time.Sleep(1500 * time.Millisecond)
	if !pidAlive(pid) {
		_ = os.Remove(uiPIDPath())
		return fmt.Errorf("le lien s'est arrêté aussitôt — voir %s", uiLogPath())
	}
	fmt.Printf("%s service %s démarré (PID %d)\n", green("[ok]"), uiServiceName(), pid)
	return nil
}

// newLinkHandler construit le handler servi à travers le tunnel :
//   - /v1/*, /health, /props, /metrics, /slots → llama-server local (endpoint
//     compatible OpenAI, avec injection de la clé API locale) → permet de
//     brancher OpenCode, Hermes, etc. sur ajean.link/oai/<machine>/v1
//   - tout le reste → l'UI web de AJEAN (avec injection de la clé de pilotage)
func newLinkHandler(mux *http.ServeMux) http.Handler {
	// Plus d'injection de clé (withLocalAuth) : les requêtes du tunnel sont
	// authentifiées par l'IDENTITÉ E2E de l'utilisateur (handleE2EReq marque la
	// requête, requireWebAuth l'accepte). Le serveur n'a donc plus besoin de
	// détenir la clé de pilotage en clair — ce qui permet à cette clé de servir de
	// clé de chiffrement sans que le serveur puisse ouvrir le coffre.
	web := http.Handler(mux)
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
	oaiAllowed := os.Getenv("AJEAN_LINK_ALLOW_OAI") == "1"
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
		// Poste distant : l'enrôlement (sceau anonyme) et le WebSocket (canal
		// chiffré poste↔agent) sont E2E — le relais ne voit que de l'opaque. On les
		// laisse donc passer, comme /api/e2e/*, sans casser la boîte noire.
		if p == "/api/node/enroll" || p == "/api/node/ws" {
			web.ServeHTTP(w, r)
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
				http.Error(w, "endpoint OpenAI désactivé via le relais (transiterait en clair) ; AJEAN_LINK_ALLOW_OAI=1 pour l'autoriser", http.StatusForbidden)
				return
			}
			lp.ServeHTTP(w, r)
			return
		}
		web.ServeHTTP(w, r)
	})
}

// runLinkSession opens one WebSocket→yamux session and serves it until it dies.
// It blocks for the lifetime of the connection and returns the error that ended
// it (so the caller can reconnect).
// runLinkSession tient UNE session de tunnel, jusqu'à ce qu'elle meure ou que
// ctx soit annulé. Le contexte est indispensable : sans lui, un « arrêter le
// lien » ne prenait effet qu'à la mort naturelle du WebSocket, qui n'arrive
// jamais tant que le relais répond. Un redémarrage (stop puis start enchaînés)
// lançait donc une seconde session pendant que la première vivait encore : deux
// agents connectés au relais pour la même machine, une seule conversation
// derrière.
func runLinkSession(ctx context.Context, token string, handler http.Handler, oaiTLS *tls.Config) error {
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
	//   - requête HTTP normale (UI AJEAN / E2E) → servie par `handler` ;
	//   - session TLS brute (accès OpenAI public) → terminée par le front TLS.
	// On les distingue au 1er octet (0x16 = handshake TLS). Sans OAI activé, tout
	// va au HTTP (comportement historique inchangé).
	httpLn := newChanListener(sess.Addr())
	defer httpLn.Close()
	// Même garde-fou que le serveur local : un stream du tunnel qui n'envoie
	// jamais d'en-têtes ne doit pas retenir une goroutine indéfiniment. Pas de
	// WriteTimeout : le chat E2E est un flux SSE de longue durée.
	srv := &http.Server{Handler: handler, ReadHeaderTimeout: 30 * time.Second}
	go srv.Serve(httpLn)

	var oaiLn *chanListener
	if oaiTLS != nil {
		oaiLn = newChanListener(sess.Addr())
		defer oaiLn.Close()
		go runOAIFront(oaiLn, oaiTLS)
	}

	// Annulation : fermer la session yamux débloque l'Accept ci-dessous, qui est
	// le seul point où cette fonction attend. La goroutine meurt avec la session.
	go func() {
		<-ctx.Done()
		_ = sess.Close()
	}()

	for {
		stream, err := sess.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return ctx.Err() // arrêt demandé, pas une panne
			}
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
