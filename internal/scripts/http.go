package scripts

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"ajean/internal/netguard"
)

const maxResponse = 2 << 20 // octets lus d'une réponse web

// client : le seul accès au réseau des scripts. netguard contrôle chaque
// connexion (donc aussi les redirections) ; pas de proxy, sinon c'est lui qui
// ouvrirait la connexion et le contrôle ne verrait que son adresse.
var client = &http.Client{
	Timeout: 20 * time.Second,
	Transport: &http.Transport{
		Proxy:       nil,
		DialContext: (&net.Dialer{Timeout: 10 * time.Second, Control: netguard.Control}).DialContext,
	},
}

// fetch fait une requête pour un script et rend {status, body, type}.
func fetch(ctx context.Context, method, url, body string, headers map[string]any) (map[string]any, error) {
	if !strings.HasPrefix(url, "http://") && !strings.HasPrefix(url, "https://") {
		return nil, errors.New("URL refusée : http:// ou https:// uniquement")
	}
	var payload io.Reader
	if body != "" {
		payload = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, strings.ToUpper(method), url, payload)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "ajean-tool/1.0")
	for k, v := range headers {
		req.Header.Set(k, fmt.Sprint(v))
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": resp.StatusCode, "body": string(b), "type": resp.Header.Get("Content-Type")}, nil
}
