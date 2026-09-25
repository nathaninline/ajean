package ajean

import (
	"bytes"
	"crypto/subtle"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

// cloud_proxy.go : avec un preset GPU Cloud, le service moteur (ajean-engine)
// ne lance pas llama-server mais un RELAIS sur le même hôte et le même port
// (HOST:PORT, 8080 par défaut) vers le GPU Modal. Tout ce qui parlait au moteur
// local continue donc de marcher tel quel : l'API OpenAI sur le réseau local,
// l'endpoint public <machine>.oai.ajean.link, le tunnel, `ajean bench`…
//
// Le relais applique la même règle d'accès que llama-server (clé API de la
// machine en Bearer), puis remplace l'en-tête par la clé du GPU Cloud, que les
// clients ne connaissent jamais. Il n'interroge pas le GPU pour /health : un
// simple sondage de santé ne doit pas le réveiller (ni le facturer).

// cloudProxyTarget : URL du GPU Cloud déployé, relue au plus toutes les 5 s
// (l'interface la met à jour dans l'autre process après un déploiement).
var cloudTarget struct {
	sync.Mutex
	at  time.Time
	url string
}

func cloudProxyTarget() string {
	cloudTarget.Lock()
	defer cloudTarget.Unlock()
	if time.Since(cloudTarget.at) > 5*time.Second {
		cloudTarget.url, cloudTarget.at = getStr(bkState, "cloud_url"), time.Now()
	}
	return cloudTarget.url
}

// serveCloudProxy remplace cmdServe pour un preset GPU Cloud. Bloquant.
func serveCloudProxy(cfg map[string]string) error {
	host := strings.TrimSpace(cfg["HOST"])
	if host == "" {
		host = "0.0.0.0"
	}
	port := strings.TrimSpace(cfg["PORT"])
	if port == "" {
		port = "8080"
	}
	if err := waitPortFree(host, port, 5*time.Second); err != nil {
		return err
	}
	localKey := readAPIKey()
	if localKey == "" {
		localKey = strings.TrimSpace(cfg["API_KEY"])
	}
	fmt.Fprintf(os.Stderr, "[ajean serve] GPU Cloud : relais %s:%s → Modal\n", host, port)
	srv := &http.Server{
		Addr:              net.JoinHostPort(host, port),
		Handler:           cloudProxyHandler(localKey),
		ReadHeaderTimeout: 30 * time.Second,
	}
	return srv.ListenAndServe()
}

func cloudProxyHandler(localKey string) http.Handler {
	proxy := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			u, _ := url.Parse(cloudProxyTarget())
			pr.SetURL(u)
			pr.Out.Host = u.Host
			pr.Out.Header.Set("Authorization", "Bearer "+cloudAPIKey())
		},
		Transport:     cloudTransport{},
		FlushInterval: -1, // flux SSE relayé au fil de l'eau
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			http.Error(w, `{"error":{"message":"GPU Cloud injoignable : `+strings.ReplaceAll(err.Error(), `"`, `'`)+`"}}`, http.StatusBadGateway)
		},
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			if cloudProxyTarget() == "" {
				http.Error(w, `{"status":"deploying"}`, http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		if localKey != "" {
			got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
			if subtle.ConstantTimeCompare([]byte(got), []byte(localKey)) != 1 {
				w.Header().Set("Content-Type", "application/json")
				http.Error(w, `{"error":{"message":"Invalid API Key","type":"authentication_error"}}`, http.StatusUnauthorized)
				return
			}
		}
		if cloudProxyTarget() == "" {
			http.Error(w, `{"error":{"message":"GPU Cloud en cours de déploiement"}}`, http.StatusServiceUnavailable)
			return
		}
		proxy.ServeHTTP(w, r)
	})
}

// cloudTransport : attend le réveil du GPU (llama-server répond 503 tant que le
// modèle charge) en rejouant la requête, et suit l'activité pour le compte à
// rebours de mise en veille. Le corps est gardé en mémoire (32 Mo max) pour
// pouvoir être rejoué.
type cloudTransport struct{}

func (cloudTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	var body []byte
	if req.Body != nil {
		b, err := io.ReadAll(io.LimitReader(req.Body, 32<<20))
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		body = b
	}
	cloudActivityBegin()
	deadline := time.Now().Add(30 * time.Minute)
	for {
		r := req.Clone(req.Context())
		r.Body = io.NopCloser(bytes.NewReader(body))
		r.ContentLength = int64(len(body))
		resp, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			cloudActivityEnd()
			return nil, err
		}
		if resp.StatusCode != http.StatusServiceUnavailable || time.Now().After(deadline) {
			resp.Body = &cloudBody{ReadCloser: resp.Body}
			return resp, nil
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		select {
		case <-req.Context().Done():
			cloudActivityEnd()
			return nil, req.Context().Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// cloudServiceAction adapte une action du service moteur au preset actif :
// API Externe → rien à lancer, on arrête le moteur local ; GPU Cloud → on
// (re)déploie si besoin, et le service lance le relais (voir serveCloudProxy).
func cloudServiceAction(action string) string {
	if action != "start" && action != "restart" {
		return action
	}
	cfg := ReadConfig()
	if isCloudConfig(cfg) {
		cloudDeploy()
	} else if isExternalConfig(cfg) {
		return "stop"
	}
	return action
}
