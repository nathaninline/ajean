package browser

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/netip"
	"syscall"
	"time"

	"ajean/internal/netguard"
)

const proxyTimeout = 10 * time.Second // ouverture d'une connexion sortante, lecture d'un en-tête de requête

// proxy : le seul chemin du navigateur vers le réseau. Chaque connexion
// sortante passe par netguard, qui juge l'adresse réellement jointe, après
// résolution du nom : ce que la page charge (cadre, worker, WebSocket) et la
// façon dont un nom se résout n'y changent rien.
type proxy struct {
	addr    string // où le proxy écoute, « 127.0.0.1:port »
	server  *http.Server
	forward http.Handler                                                         // requêtes http en clair
	dial    func(ctx context.Context, network, address string) (net.Conn, error) // toute connexion sortante
	ctx     context.Context                                                      // annulé à l'arrêt : les tunnels ouverts se ferment
	cancel  context.CancelFunc
}

// refusal distingue un refus de netguard d'une panne de réseau.
type refusal struct{ error }

func guard(network, address string, c syscall.RawConn) error {
	if err := netguard.Control(network, address, c); err != nil {
		return refusal{err}
	}
	return nil
}

// startProxy ouvre le proxy sur un port libre de la boucle locale.
func startProxy() (*proxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	silent := log.New(io.Discard, "", 0) // les adresses visitées ne s'écrivent nulle part
	p := &proxy{
		addr: ln.Addr().String(),
		dial: (&net.Dialer{Timeout: proxyTimeout, Control: guard}).DialContext,
	}
	p.ctx, p.cancel = context.WithCancel(context.Background())
	p.forward = &httputil.ReverseProxy{
		Rewrite: func(*httputil.ProxyRequest) {}, // la requête part telle quelle, à l'adresse qu'elle nomme
		Transport: &http.Transport{
			// Pas de proxy du système : c'est lui qui ouvrirait la connexion, et
			// netguard ne verrait que son adresse.
			Proxy: nil,
			DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
				return p.dial(ctx, network, address)
			},
			DisableCompression: true,
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) { fail(w, err) },
		ErrorLog:     silent,
	}
	p.server = &http.Server{Handler: p, ReadHeaderTimeout: proxyTimeout, ErrorLog: silent}
	go func() { _ = p.server.Serve(ln) }()
	return p, nil
}

func (p *proxy) close() {
	p.cancel()
	_ = p.server.Close()
}

func (p *proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	client, err := netip.ParseAddrPort(r.RemoteAddr)
	switch {
	case err != nil || !client.Addr().Unmap().IsLoopback():
		// Le proxy sort sur Internet : il ne sert que le navigateur de cette machine.
		deny(w, "client refusé")
	case r.Method == http.MethodConnect:
		p.tunnel(w, r)
	case r.URL.Scheme == "http" && r.URL.Host != "":
		p.forward.ServeHTTP(w, r)
	default:
		deny(w, "requête refusée")
	}
}

// tunnel relie le navigateur à l'hôte demandé par CONNECT (https, WebSocket)
// et recopie les octets dans les deux sens jusqu'à ce qu'un côté ferme.
func (p *proxy) tunnel(w http.ResponseWriter, r *http.Request) {
	upstream, err := p.dial(r.Context(), "tcp", r.Host)
	if err != nil {
		fail(w, err)
		return
	}
	defer upstream.Close()
	client, buffered, err := http.NewResponseController(w).Hijack()
	if err != nil {
		http.Error(w, "AJEAN : tunnel impossible", http.StatusInternalServerError)
		return
	}
	defer client.Close()
	// Fermer les deux bouts débloque les deux copies : à l'arrêt du proxy, ou dès
	// qu'un sens est terminé.
	closeBoth := func() { _ = client.Close(); _ = upstream.Close() }
	defer context.AfterFunc(p.ctx, closeBoth)()
	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}
	go func() {
		_, _ = io.Copy(upstream, buffered) // le lecteur peut déjà tenir les premiers octets du navigateur
		closeBoth()
	}()
	_, _ = io.Copy(client, upstream)
}

// fail répond à une connexion sortante qui n'a pas abouti : 403 si netguard l'a
// refusée, 502 si l'hôte est simplement injoignable.
func fail(w http.ResponseWriter, err error) {
	if r := (refusal{}); errors.As(err, &r) {
		deny(w, r.Error())
		return
	}
	http.Error(w, "AJEAN : hôte injoignable", http.StatusBadGateway)
}

func deny(w http.ResponseWriter, reason string) {
	http.Error(w, "AJEAN : "+reason, http.StatusForbidden)
}
