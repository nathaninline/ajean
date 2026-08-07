package ajean

import "testing"

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
