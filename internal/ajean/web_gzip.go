package ajean

import (
	"compress/gzip"
	"net/http"
	"strings"
	"sync"
)

// web_gzip.go — compression gzip des réponses du serveur web LOCAL.
//
// L'interface (≈ 850 Ko de HTML/JS/CSS) et les réponses JSON volumineuses (liste
// des sessions, résultats d'outils, journal d'une conversation relue) partaient en
// clair. gzip les divise typiquement par 4 à 8, ce qui se sent sur un téléphone en
// Wi-Fi. La décision se prend au PREMIER octet, d'après le Content-Type choisi par
// le handler : les flux (text/event-stream), les binaires et ce qui est déjà
// compressé passent tels quels — un flux SSE compressé retiendrait ses événements
// dans le tampon de gzip au lieu de les livrer en direct.

var gzPool = sync.Pool{New: func() any { w, _ := gzip.NewWriterLevel(nil, gzip.BestSpeed); return w }}

func gzipHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") || r.Method == http.MethodHead {
			next.ServeHTTP(w, r)
			return
		}
		gw := &gzipWriter{ResponseWriter: w}
		defer gw.finish()
		next.ServeHTTP(gw, r)
	})
}

type gzipWriter struct {
	http.ResponseWriter
	decided bool
	gz      *gzip.Writer
}

// compressible : types textuels uniquement, jamais un flux ni un contenu déjà encodé.
func compressible(h http.Header) bool {
	if h.Get("Content-Encoding") != "" {
		return false
	}
	ct := strings.ToLower(h.Get("Content-Type"))
	if strings.HasPrefix(ct, "text/event-stream") {
		return false
	}
	return strings.HasPrefix(ct, "text/") || strings.Contains(ct, "json") ||
		strings.Contains(ct, "javascript") || strings.Contains(ct, "xml") || strings.Contains(ct, "manifest")
}

func (g *gzipWriter) decide() {
	if g.decided {
		return
	}
	g.decided = true
	h := g.Header()
	h.Add("Vary", "Accept-Encoding")
	if !compressible(h) {
		return
	}
	h.Set("Content-Encoding", "gzip")
	h.Del("Content-Length")
	gz := gzPool.Get().(*gzip.Writer)
	gz.Reset(g.ResponseWriter)
	g.gz = gz
}

func (g *gzipWriter) WriteHeader(code int) {
	// Pas de corps pour ces statuts : ne jamais annoncer un encodage.
	if code == http.StatusNoContent || code == http.StatusNotModified || code < 200 {
		g.decided = true
	}
	g.decide()
	g.ResponseWriter.WriteHeader(code)
}

func (g *gzipWriter) Write(b []byte) (int, error) {
	g.decide()
	if g.gz != nil {
		return g.gz.Write(b)
	}
	return g.ResponseWriter.Write(b)
}

// Flush : les handlers qui diffusent (SSE) doivent continuer à pouvoir vider leur
// tampon ; dans ce cas ils ne sont de toute façon pas compressés (voir compressible).
func (g *gzipWriter) Flush() {
	if g.gz != nil {
		_ = g.gz.Flush()
	}
	if f, ok := g.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// Unwrap laisse http.ResponseController atteindre le writer d'origine.
func (g *gzipWriter) Unwrap() http.ResponseWriter { return g.ResponseWriter }

func (g *gzipWriter) finish() {
	if g.gz != nil {
		_ = g.gz.Close()
		g.gz.Reset(nil)
		gzPool.Put(g.gz)
		g.gz = nil
	}
}
