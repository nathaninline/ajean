package ajean

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// util_files.go : petits utilitaires (fichiers, tailles, outils) gardés de l'ancienne gestion des moteurs.

// humanBytes formate une taille en Go/Mo pour les messages d'erreur.
func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f Go", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.0f Mo", float64(n)/float64(1<<20))
	default:
		return fmt.Sprintf("%d o", n)
	}
}

func isFile(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir()
}

func isDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// normDir met un chemin sous forme comparable (séparateurs unifiés, casse
// neutralisée sous Windows, slash final retiré).
func normDir(p string) string {
	// Clean d'abord (il réintroduit le séparateur natif), puis on unifie en '/'.
	p = strings.ReplaceAll(filepath.Clean(strings.TrimSpace(p)), "\\", "/")
	if runtime.GOOS == "windows" {
		p = strings.ToLower(p)
	}
	return p
}

func hasTool(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// diskFree renvoie l'espace libre du volume qui porte dir, ou -1 s'il est
// inconnu. Le dossier n'existe pas forcément encore (models/ au premier
// lancement) : on remonte les parents jusqu'à en trouver un qui existe.
func diskFree(dir string) int64 {
	d, err := filepath.Abs(strings.TrimSpace(dir))
	if err != nil {
		return -1
	}
	for {
		if st, err := os.Stat(d); err == nil && st.IsDir() {
			return diskFreeAt(d)
		}
		parent := filepath.Dir(d)
		if parent == d {
			return -1
		}
		d = parent
	}
}

// baseName renvoie le nom de fichier d'un chemin en acceptant les DEUX
// séparateurs : un config.env écrit sous Windows (C:\models\x.gguf) peut être
// relu par un ajean Linux, où filepath.Base ne coupe pas sur '\'.
func baseName(p string) string {
	p = strings.TrimRight(strings.TrimSpace(p), `/\`)
	if i := strings.LastIndexAny(p, `/\`); i >= 0 {
		return p[i+1:]
	}
	return p
}

// quantFromName extracts a quantization tag from a model filename by splitting
// on '-' and '.' and keeping the longest segment that looks like a quant token.
// Returns "" when nothing matches.
func quantFromName(name string) string {
	base := name
	if dot := strings.LastIndexByte(base, '.'); dot >= 0 && strings.EqualFold(base[dot:], ".gguf") {
		base = base[:dot]
	}
	segs := strings.FieldsFunc(base, func(r rune) bool { return r == '-' || r == '.' })
	best := ""
	for _, seg := range segs {
		if quantSegRe.MatchString(seg) && len(seg) > len(best) {
			best = seg
		}
	}
	return strings.ToUpper(best)
}

// detectQuant returns the quantization tag for a preset: an explicit QUANT= line
// (manual override, with or without a leading '#') wins; otherwise it is
// auto-detected from the MODEL= filename. Returns "" when unknown.
func detectQuant(content string) string {
	for _, line := range strings.Split(content, "\n") {
		s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "#"))
		i := strings.IndexByte(s, '=')
		if i >= 0 && strings.EqualFold(strings.TrimSpace(s[:i]), "QUANT") {
			if v := unquoteValue(strings.TrimSpace(s[i+1:])); v != "" {
				return strings.ToUpper(v)
			}
		}
	}
	return quantFromName(baseName(modelFromPresetContent(content)))
}

// modelFromPresetContent extracts the MODEL= value from a preset's config.env
// body (nom de fichier ou chemin absolu, tel quel), or "" if absent.
func modelFromPresetContent(content string) string {
	for _, line := range strings.Split(content, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		i := strings.IndexByte(s, '=')
		if i < 0 {
			continue
		}
		if strings.TrimSpace(s[:i]) == "MODEL" {
			return unquoteValue(strings.TrimSpace(s[i+1:]))
		}
	}
	return ""
}

// presetReasoning returns the raw REASONING= value from a preset's config.env
// body, or "" if absent.
func presetReasoning(content string) string {
	for _, line := range strings.Split(content, "\n") {
		s := strings.TrimSpace(line)
		if s == "" || strings.HasPrefix(s, "#") {
			continue
		}
		i := strings.IndexByte(s, '=')
		if i < 0 {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(s[:i]), "REASONING") {
			return unquoteValue(strings.TrimSpace(s[i+1:]))
		}
	}
	return ""
}

// reasoningActive reports whether a REASONING= value enables reasoning. backend_serve.go
// passes the flag whenever the value is non-empty, but an explicit off/none is
// treated here as disabled so the UI badge isn't misleading.
func reasoningActive(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "off", "none", "false", "0", "no", "disable", "disabled":
		return false
	}
	return true
}

// doLLM envoie une requête de complétion. Vu par le pré-chauffage de prompt
// (llm_prewarm.go) : une génération occupe le moteur, il ne faut pas s'insérer
// devant elle.
func doLLM(ctx context.Context, req *http.Request, body []byte, ep chatEndpoint) (*http.Response, error) {
	llmInFlight.Add(1)
	defer llmInFlight.Add(-1)
	return http.DefaultClient.Do(req)
}

// downloadWithProgress télécharge url vers dest en journalisant la progression
// par tranches de ~25 Mo.
func downloadWithProgress(url, dest string, total int64, logf func(string)) error {
	client := &http.Client{Timeout: 0}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if total <= 0 {
		total = resp.ContentLength
	}
	f, err := os.Create(dest)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 1<<20)
	var done, lastLog int64
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			if done-lastLog >= 25<<20 {
				lastLog = done
				if total > 0 {
					logf(fmt.Sprintf("⬇ %d / %d Mo (%d%%)", done/1_000_000, total/1_000_000, done*100/total))
				} else {
					logf(fmt.Sprintf("⬇ %d Mo", done/1_000_000))
				}
			}
		}
		if rerr == io.EOF {
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// quantSegRe matches a single name segment that looks like a GGUF quantization
// token: Q8_0, Q6_K, Q5_K_M, Q4_K_XL, IQ4_XS, IQ3_XXS, Q4, 4bpw, BF16, F16…
var quantSegRe = regexp.MustCompile(`(?i)^(I?Q\d+(_[A-Za-z0-9]+)*|\d+BPW|BF16|FP16|F16|FP32|F32)$`)

func pathVersion(s string) string { return reVerNum.FindString(filepath.ToSlash(s)) }

// cmpVersion compare deux versions pointées NUMÉRIQUEMENT (« 12.10 » > « 12.4 »,
// « 13.4 » > « 9.0 ») là où un tri de chaînes se tromperait. Renvoie -1, 0 ou 1.
// Une chaîne vide est considérée plus petite que toute version.
func cmpVersion(a, b string) int {
	if a == b {
		return 0
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			if x < y {
				return -1
			}
			return 1
		}
	}
	return 0
}

// reVerNum capture le premier numéro de version pointé d'un chemin
// (« …/v13.4/bin » → « 13.4 », « /usr/local/cuda-12.8/lib64 » → « 12.8 »).
var reVerNum = regexp.MustCompile(`\d+(?:\.\d+)+`)
