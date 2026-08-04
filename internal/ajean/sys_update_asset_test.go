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

// Une release de transition publie les deux noms : on doit préférer ajean-*.
func TestPickAssetPrefersAjean(t *testing.T) {
	names := updateAssetNames()
	got, url, _ := pickAsset(relWith(names[1], names[0]))
	if got != names[0] {
		t.Fatalf("got %q, attendu la préférence %q", got, names[0])
	}
	if url == "" {
		t.Fatal("URL vide")
	}
}

// Release future, purgée des anciens noms : ajean-* seul suffit.
func TestPickAssetAjeanOnly(t *testing.T) {
	names := updateAssetNames()
	if got, _, _ := pickAsset(relWith(names[0])); got != names[0] {
		t.Fatalf("got %q, attendu %q", got, names[0])
	}
}

// Ancienne release, antérieure au renommage : le repli sur jean-* doit marcher,
// sinon un client neuf ne pourrait pas s'installer depuis une release existante.
func TestPickAssetFallsBackToLegacy(t *testing.T) {
	names := updateAssetNames()
	got, _, _ := pickAsset(relWith(names[1]))
	if got != names[1] {
		t.Fatalf("got %q, attendu le repli %q", got, names[1])
	}
}

// Le nom renvoyé sert à vérifier le SHA-256 : il doit être celui réellement
// choisi, pas le nom préféré, sinon la vérification échoue sur un repli.
func TestPickAssetReturnsCheckedName(t *testing.T) {
	names := updateAssetNames()
	got, url, size := pickAsset(relWith(names[1]))
	if got != names[1] || url != "https://example.invalid/"+names[1] || size != 42 {
		t.Fatalf("incohérence nom/URL/taille: %q %q %d", got, url, size)
	}
}

// Aucun asset pour cette plateforme → pas d'URL, l'appelant doit pouvoir le voir.
func TestPickAssetNoMatch(t *testing.T) {
	if _, url, _ := pickAsset(relWith("ajean-plan9-386", "SHA256SUMS")); url != "" {
		t.Fatalf("URL inattendue: %q", url)
	}
}
