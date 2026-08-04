// gen-icon écrit cmd/jean/icon.ico à partir de l'icône de marque dessinée dans
// internal/jean (sys_brand_icon.go), pour que l'icône du .exe soit toujours la
// même que le favicon de l'UI et que celle de la zone de notification.
//
// Lancé par `go generate ./cmd/jean`, AVANT goversioninfo qui embarque le .ico
// dans les ressources Windows.
package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/nathaninline/ajean/internal/ajean"
)

func main() {
	out := "icon.ico"
	if len(os.Args) > 1 {
		out = os.Args[1]
	}
	// Les tailles attendues par l'explorateur Windows, de la barre des tâches à
	// l'aperçu en grandes icônes.
	write(out, ajean.BrandICO(16, 32, 48, 64, 128, 256))

	// PNG 1024 pour le bundle macOS : la CI en tire le .icns via sips/iconutil.
	// Elle partait auparavant du .ico, ce qui suppose que sips sache lire un ICO —
	// et l'échec y est masqué par « || true », donc une app Mac sans icône passait
	// inaperçue. Un PNG ne laisse aucune place au doute.
	write(strings.TrimSuffix(out, ".ico")+".png", ajean.BrandIconPNG(1024))
}

func write(path string, data []byte) {
	if err := os.WriteFile(path, data, 0o644); err != nil {
		fmt.Fprintln(os.Stderr, "gen-icon:", err)
		os.Exit(1)
	}
	fmt.Println("OK ->", path)
}
