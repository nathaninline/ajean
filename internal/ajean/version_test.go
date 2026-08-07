package ajean

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
)

// La ressource de version Windows (cmd/ajean/versioninfo.json) doit suivre la
// constante Version. Elles ont divergé en 0.8.0 : la constante était à jour, la
// ressource restait à 0.7.13. Conséquence peu lisible — le binaire installé se
// DÉCLARAIT 0.7.13 auprès de Windows, si bien que relancer le fichier téléchargé
// proposait indéfiniment « appliquer la mise à jour ? » sur une version déjà
// installée. Ce test rend l'oubli impossible.
func TestVersionInfoWindowsSuitLaVersion(t *testing.T) {
	b, err := os.ReadFile("../../cmd/ajean/versioninfo.json")
	if err != nil {
		t.Fatal(err)
	}
	var vi struct {
		FixedFileInfo struct {
			FileVersion    struct{ Major, Minor, Patch int }
			ProductVersion struct{ Major, Minor, Patch int }
		}
		StringFileInfo struct{ ProductVersion string }
	}
	if err := json.Unmarshal(b, &vi); err != nil {
		t.Fatal(err)
	}
	f := vi.FixedFileInfo.FileVersion
	for name, got := range map[string]string{
		"FixedFileInfo.FileVersion":     fmt.Sprintf("%d.%d.%d", f.Major, f.Minor, f.Patch),
		"FixedFileInfo.ProductVersion":  fmt.Sprintf("%d.%d.%d", vi.FixedFileInfo.ProductVersion.Major, vi.FixedFileInfo.ProductVersion.Minor, vi.FixedFileInfo.ProductVersion.Patch),
		"StringFileInfo.ProductVersion": vi.StringFileInfo.ProductVersion,
	} {
		if got != Version {
			t.Errorf("%s = %s, attendu %s — pense à `go generate ./cmd/ajean` après un bump", name, got, Version)
		}
	}
}
