package main

import (
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
	"strconv"
	"strings"
	"time"
)

// oai.go — front TLS de l'accès OpenAI public "VPS aveugle" (SNI passthrough).
//
// Principe : le SaaS parle HTTPS OpenAI standard vers <machine>.oai.ajean.link.
// Le VPS relais ne fait que recopier les octets TLS bruts (routage par SNI, sans
// déchiffrer). C'est ICI, sur l'agent, que le TLS est terminé — avec un cert dont
// la clé privée ne quitte JAMAIS cette machine — puis proxifié vers llama-server
// local (/v1). Un attaquant qui possède le VPS ne voit donc que du chiffré.
//
// Ce fichier fournit la terminaison TLS + le proxy. La source du certificat
// (auto-signé pour les tests locaux ; Let's Encrypt via DNS-01 en prod) et
// l'alimentation par le tunnel (streams "raw") sont branchées séparément.

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

// runOAIFront termine le TLS sur rawLn (avec cert) puis sert oaiHandler dessus.
// rawLn peut être un vrai listener TCP (test local) ou un listener alimenté par
// les streams "raw" du tunnel (prod). Bloquant.
func runOAIFront(rawLn net.Listener, cert tls.Certificate) error {
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		MinVersion:   tls.VersionTLS12,
	}
	srv := &http.Server{
		Handler:      oaiHandler(),
		ReadTimeout:  0, // completions longues : pas de timeout de lecture
		WriteTimeout: 0,
		IdleTimeout:  120 * time.Second,
	}
	return srv.Serve(tls.NewListener(rawLn, tlsCfg))
}

// selfSignedCert fabrique en mémoire un certificat auto-signé pour host, valable
// 1 an. Sert UNIQUEMENT aux tests locaux (curl -k) avant de brancher Let's
// Encrypt. En prod le cert vient de certmagic/ACME (phase 2).
func selfSignedCert(host string) (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
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
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, nil
}

// cmdOAI pilote l'accès OpenAI public côté agent.
//
//	jean oai serve [port] [host]   (test local) termine le TLS sur :port avec un
//	                               cert auto-signé et proxifie vers llama /v1.
//	                               défaut port=8443, host=localhost.
func cmdOAI(args []string) error {
	sub := ""
	if len(args) > 0 {
		sub = args[0]
		args = args[1:]
	}
	switch sub {
	case "serve":
		port := 8443
		if len(args) > 0 && args[0] != "" {
			n, err := strconv.Atoi(args[0])
			if err != nil {
				return fmt.Errorf("port invalide: %s", args[0])
			}
			port = n
		}
		host := "localhost"
		if len(args) > 1 && args[1] != "" {
			host = args[1]
		}
		cert, err := selfSignedCert(host)
		if err != nil {
			return err
		}
		ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", port))
		if err != nil {
			return err
		}
		fmt.Printf("[jean oai] front TLS (test) https://%s:%d/v1  → llama :%d\n", host, port, LLMPort())
		fmt.Printf("%s cert auto-signé — teste avec: %s\n", yellow("[info]"),
			bold(fmt.Sprintf("curl -k https://%s:%d/v1/models -H 'Authorization: Bearer <clé>'", host, port)))
		return runOAIFront(ln, cert)
	default:
		fmt.Println("usage: jean oai serve [port] [host]   (front TLS de test → llama /v1)")
		return nil
	}
}

var _ = os.Getenv // réservé : flags d'activation en phase suivante
