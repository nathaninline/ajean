package ajean

import (
	"runtime"
	"testing"
)

// relWith fabrique une release ne contenant que les assets nommés.
func relWith(names ...string) *ghRelease {
	rel := &ghRelease{TagName: "v9.9.9"}
	for _, n := range names {
		rel.Assets = append(rel.Assets, struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		}{Name: n, BrowserDownloadURL: "https://example.invalid/" + n, Size: 42})
	}
	return rel
}

// L'asset de la plateforme est trouvé au milieu des autres, et le nom renvoyé
// est bien celui qui servira à vérifier le SHA-256.
func TestPickAssetTrouveSaPlateforme(t *testing.T) {
	want := updateAssetName()
	got, url, size := pickAsset(relWith("SHA256SUMS", "ajean-plan9-386", want))
	if got != want {
		t.Fatalf("got %q, attendu %q", got, want)
	}
	if url != "https://example.invalid/"+want || size != 42 {
		t.Fatalf("incohérence URL/taille : %q %d", url, size)
	}
}

// Aucun asset pour cette plateforme → pas d'URL, l'appelant doit pouvoir le voir.
func TestPickAssetSansCorrespondance(t *testing.T) {
	if _, url, _ := pickAsset(relWith("ajean-plan9-386", "SHA256SUMS")); url != "" {
		t.Fatalf("URL inattendue: %q", url)
	}
}

// Le schéma de nommage ne doit PAS coïncider avec ajean-<GOOS>-<GOARCH> : c'est
// ce qui empêche la mise à jour automatique d'une 0.7 de s'installer sur une
// machine encore agencée en 0.7.
func TestNomDAssetDistinctDuSchema07(t *testing.T) {
	legacy := "ajean-" + runtime.GOOS + "-" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		legacy += ".exe"
	}
	if got := updateAssetName(); got == legacy {
		t.Fatalf("le nom %q est celui que cherche la 0.7 — sa mise à jour casserait l'installation", got)
	}
}

// Les six noms publiés par la release, vérifiés un par un.
func TestNomsDAssetParPlateforme(t *testing.T) {
	for _, c := range []struct{ goos, goarch, want string }{
		{"linux", "amd64", "ajean-linux"},
		{"linux", "arm64", "ajean-linux-arm"},
		{"darwin", "amd64", "ajean-macos"},
		{"darwin", "arm64", "ajean-macos-arm"},
		{"windows", "amd64", "ajean-windows.exe"},
		{"windows", "arm64", "ajean-windows-arm.exe"},
	} {
		if got := assetNameFor(c.goos, c.goarch); got != c.want {
			t.Errorf("%s/%s → %q, attendu %q", c.goos, c.goarch, got, c.want)
		}
	}
}
