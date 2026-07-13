package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/caddyserver/certmagic"
	"github.com/libdns/cloudflare"
)

// oai.go — front TLS de l'accès OpenAI public "VPS aveugle" (SNI passthrough).
//
// Principe : le SaaS parle HTTPS OpenAI standard vers <machine>.oai.ajean.link.
// Le VPS relais ne fait que recopier les octets TLS bruts (routage par SNI, sans
// déchiffrer). C'est ICI, sur l'agent, que le TLS est terminé — avec un cert dont
// la clé privée ne quitte JAMAIS cette machine — puis proxifié vers llama-server
// local (/v1). Un attaquant qui possède le VPS ne voit donc que du chiffré.
//
// Le certificat est obtenu par Let's Encrypt via challenge DNS-01 Cloudflare
// (l'agent est derrière CGNAT → pas de HTTP-01). En test local, on retombe sur
// un certificat auto-signé.

// oaiHandler construit le reverse-proxy vers llama-server, restreint à la surface
// compatible OpenAI. On NE touche PAS à l'en-tête Authorization : le SaaS envoie
// la vraie clé (.api_key), que llama-server valide lui-même (--api-key).
func oaiHandler() http.Handler {
	llama := &url.URL{Scheme: "http", Host: fmt.Sprintf("127.0.0.1:%d", LLMPort())}
	lp := httputil.NewSingleHostReverseProxy(llama)
	lp.FlushInterval = -1 // streaming SSE des complétions
	lp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, e error) {
		http.Error(w, "llama-server injoignable: "+e.Error(), http.StatusBadGateway)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		if strings.HasPrefix(p, "/v1") || p == "/health" || p == "/props" || p == "/metrics" || strings.HasPrefix(p, "/slots") {
			lp.ServeHTTP(w, r)
			return
		}
		http.Error(w, "not found (endpoint OpenAI: /v1/*)", http.StatusNotFound)
	})
}

// runOAIFront termine le TLS sur rawLn (avec tlsCfg) puis sert oaiHandler dessus.
// rawLn peut être un vrai listener TCP (test local) ou un listener alimenté par
// les streams "raw" du tunnel (prod). Bloquant.
func runOAIFront(rawLn net.Listener, tlsCfg *tls.Config) error {
	srv := &http.Server{
		Handler:     oaiHandler(),
		IdleTimeout: 120 * time.Second,
		// completions longues : pas de Read/Write timeout.
	}
	return srv.Serve(tls.NewListener(rawLn, tlsCfg))
}

// cfToken lit le token API Cloudflare (permission DNS edit sur la zone ajean.link)
// depuis $JEAN_CF_TOKEN ou $JEAN_HOME/.cf_token. Sert au challenge DNS-01.
func cfToken() string {
	if t := strings.TrimSpace(os.Getenv("JEAN_CF_TOKEN")); t != "" {
		return t
	}
	b, _ := os.ReadFile(filepath.Join(JeanHome(), ".cf_token"))
	return strings.TrimSpace(string(b))
}

// newCertmagic configure certmagic pour émettre/renouveler via Cloudflare DNS-01,
// avec stockage persistant dans $JEAN_HOME/certs (la clé privée reste locale).
func newCertmagic(token string) (*certmagic.Config, error) {
	if token == "" {
		return nil, fmt.Errorf("token Cloudflare absent (JEAN_CF_TOKEN ou %s)", filepath.Join(JeanHome(), ".cf_token"))
	}
	certmagic.Default.Storage = &certmagic.FileStorage{Path: filepath.Join(JeanHome(), "certs")}
	certmagic.DefaultACME.Agreed = true
	certmagic.DefaultACME.Email = strings.TrimSpace(os.Getenv("JEAN_ACME_EMAIL"))
	certmagic.DefaultACME.DNS01Solver = &certmagic.DNS01Solver{
		DNSManager: certmagic.DNSManager{
			DNSProvider: &cloudflare.Provider{APIToken: token},
		},
	}
	return certmagic.NewDefault(), nil
}

// acmeTLSConfig émet (ou charge le cache) le cert LE pour domain via DNS-01
// Cloudflare et renvoie un *tls.Config qui le sert.
func acmeTLSConfig(domain string) (*tls.Config, error) {
	cfg, err := newCertmagic(cfToken())
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if err := cfg.ManageSync(ctx, []string{domain}); err != nil {
		return nil, err
	}
	return &tls.Config{GetCertificate: cfg.GetCertificate, MinVersion: tls.VersionTLS12}, nil
}

// selfSignedTLSConfig fabrique un *tls.Config auto-signé pour host. Tests locaux
// uniquement (curl -k) avant de brancher Let's Encrypt.
func selfSignedTLSConfig(host string) (*tls.Config, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return nil, err
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}, nil
}

// cmdOAI pilote l'accès OpenAI public côté agent.
//
//	jean oai cert <domaine>         émet/charge le cert Let's Encrypt (DNS-01 CF)
//	                                et s'arrête — pour tester l'émission.
//	jean oai serve [port] [domaine] termine le TLS sur :port et proxifie vers
//	                                llama /v1. Avec un domaine réel + token CF →
//	                                cert Let's Encrypt ; sinon cert auto-signé.
func cmdOAI(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "cert":
		if len(args) == 0 || args[0] == "" {
			return fmt.Errorf("usage: jean oai cert <domaine>")
		}
		domain := args[0]
		fmt.Printf("[jean oai] émission du certificat pour %s (Let's Encrypt, DNS-01 Cloudflare)…\n", bold(domain))
		if _, err := acmeTLSConfig(domain); err != nil {
			return err
		}
		fmt.Printf("%s certificat prêt (stocké dans %s)\n", green("[ok]"), filepath.Join(JeanHome(), "certs"))
		return nil
	case "serve":
		port := 8443
		if len(args) > 0 && args[0] != "" {
			n, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("port invalide: %s", args[0])
			}
			port = n
		}
		domain := ""
		if len(args) > 1 {
			domain = args[1]
		}
		var tlsCfg *tls.Config
		var err error
		mode := "auto-signé (test)"
		if domain != "" && net.ParseIP(domain) == nil && cfToken() != "" {
			tlsCfg, err = acmeTLSConfig(domain)
			mode = "Let's Encrypt " + domain
		} else {
			host := domain
			if host == "" {
				host = "localhost"
			}
			tlsCfg, err = selfSignedTLSConfig(host)
		}
		if err != nil {
			return err
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
		if err != nil {
			return err
		}
		fmt.Printf("[jean oai] front TLS :%d [%s] → llama /v1 :%d\n", port, mode, LLMPort())
		return runOAIFront(ln, tlsCfg)
	default:
		fmt.Println("usage:")
		fmt.Println("  jean oai cert <domaine>          émet le cert Let's Encrypt (DNS-01 Cloudflare) et s'arrête")
		fmt.Println("  jean oai serve [port] [domaine]  front TLS → llama /v1 (domaine réel = Let's Encrypt, sinon auto-signé)")
		return nil
	}
}
