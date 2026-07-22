// assemble-ui reconstruit internal/jean/ui/index.html à partir des sources
// internal/jean/ui/src/ (remplace l'ancien ui/assemble.ps1 — multiplateforme).
//
// index.html est un fichier GÉNÉRÉ mais committé : go:embed (web_server.go) et
// ajean-app/build-server-ui.ps1 le lisent tel quel. Pour modifier l'UI : éditer
// ui/src/ (index.tmpl.html, styles.css, js/NN-*.js concaténés dans l'ordre
// alphabétique, tout en scope global) puis relancer :
//
//	go generate ./internal/jean        (ou : go run ./tools/assemble-ui)
//
// Les marqueurs @@CSS@@ / @@JS@@ du template sont remplacés avec LEUR fin de
// ligne (LF ou CRLF) ; chaque source apporte ses propres fins de ligne.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

func main() {
	uiDir := "internal/jean/ui"
	if len(os.Args) > 1 {
		uiDir = os.Args[1]
	}
	if err := run(uiDir); err != nil {
		fmt.Fprintln(os.Stderr, "assemble-ui:", err)
		os.Exit(1)
	}
}

func run(uiDir string) error {
	srcDir := filepath.Join(uiDir, "src")
	tmpl, err := os.ReadFile(filepath.Join(srcDir, "index.tmpl.html"))
	if err != nil {
		return err
	}
	css, err := os.ReadFile(filepath.Join(srcDir, "styles.css"))
	if err != nil {
		return err
	}
	entries, err := os.ReadDir(filepath.Join(srcDir, "js"))
	if err != nil {
		return err
	}
	var names []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".js") {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	var js []byte
	for _, n := range names {
		b, err := os.ReadFile(filepath.Join(srcDir, "js", n))
		if err != nil {
			return err
		}
		js = append(js, b...)
	}

	html := string(tmpl)
	for marker, content := range map[string]string{"@@CSS@@": string(css), "@@JS@@": string(js)} {
		re := regexp.MustCompile(regexp.QuoteMeta(marker) + "\r?\n")
		if !re.MatchString(html) {
			return fmt.Errorf("marqueur %s introuvable dans index.tmpl.html", marker)
		}
		html = re.ReplaceAllStringFunc(html, func(string) string { return content })
	}

	dst := filepath.Join(uiDir, "index.html")
	if err := os.WriteFile(dst, []byte(html), 0o644); err != nil {
		return err
	}
	fmt.Printf("OK -> %s (%d octets)\n", dst, len(html))
	return nil
}
