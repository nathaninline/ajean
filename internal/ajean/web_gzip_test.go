package ajean

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// JSON compressé, flux SSE et binaires laissés tels quels.
func TestGzipHandler(t *testing.T) {
	h := gzipHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			sendJSON(w, 200, map[string]any{"x": strings.Repeat("a", 5000)})
		case "/sse":
			w.Header().Set("Content-Type", "text/event-stream")
			w.Write([]byte("data: 1\n\n"))
			w.(http.Flusher).Flush()
		case "/bin":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte{1, 2, 3})
		}
	}))
	get := func(p string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", p, nil)
		r.Header.Set("Accept-Encoding", "gzip, deflate")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	j := get("/json")
	if j.Header().Get("Content-Encoding") != "gzip" {
		t.Fatal("JSON non compressé")
	}
	zr, err := gzip.NewReader(j.Body)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(zr)
	if !strings.Contains(string(b), strings.Repeat("a", 5000)) || j.Body.Len() > 500 {
		t.Fatalf("décompression/ratio inattendus (%d o compressés)", j.Body.Len())
	}
	if s := get("/sse"); s.Header().Get("Content-Encoding") != "" || s.Body.String() != "data: 1\n\n" || !s.Flushed {
		t.Fatal("le flux SSE ne doit pas être compressé et doit rester vidable")
	}
	if b := get("/bin"); b.Header().Get("Content-Encoding") != "" {
		t.Fatal("binaire compressé à tort")
	}
}
