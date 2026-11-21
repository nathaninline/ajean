package ajean

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testAPIKey = "sk-essai-0123456789abcdef"

func secretsTestHome(t *testing.T) string {
	t.Helper()
	home := testHome(t)
	secretCacheBust()
	t.Cleanup(secretCacheBust)
	old := secretsPruneGrace
	secretsPruneGrace = 0
	t.Cleanup(func() { secretsPruneGrace = old })
	if err := os.MkdirAll(presetsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	return home
}

func writePresetFile(t *testing.T, id, content string) string {
	t.Helper()
	p := filepath.Join(presetsDir(), id+".env")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func readFileT(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Aller-retour : la valeur rangée se relit, n'apparaît nulle part en clair, et la
// clé du coffre existe en deux copies.
func TestSecretRoundTrip(t *testing.T) {
	home := secretsTestHome(t)
	ref, err := secretPut("preset essai", testAPIKey)
	if err != nil {
		t.Fatal(err)
	}
	if !isSecretRef(ref) || strings.Contains(ref, testAPIKey) {
		t.Fatalf("référence inattendue : %q", ref)
	}
	if got, err := secretResolve(ref); err != nil || got != testAPIKey {
		t.Fatalf("relecture : %q %v", got, err)
	}
	db, err := os.ReadFile(filepath.Join(home, "ajean.db"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(db, []byte(testAPIKey)) {
		t.Fatal("la clé d'API figure en clair dans la base")
	}
	for _, p := range []string{secretsKeyPath(), secretsKeyBakPath()} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("copie de la clé du coffre absente : %s", p)
		}
	}
	// Une valeur ordinaire passe telle quelle ; ce qui ressemble à une référence
	// sans en être une aussi.
	for _, v := range []string{"", "sk-en-clair", "vault:pas-un-id", "vault:"} {
		if got, err := secretResolve(v); err != nil || got != v {
			t.Fatalf("%q devait passer tel quel : %q %v", v, got, err)
		}
	}
	// Référence bien formée mais inconnue : erreur, pas une clé vide.
	if got, err := secretResolve("vault:0123456789abcdef"); err == nil || got != "" {
		t.Fatalf("référence inconnue : %q %v", got, err)
	}
	if err := secretPutID("0123456789abcdef", "x", ""); err == nil {
		t.Fatal("un secret vide devait être refusé")
	}
}

// La clé du coffre : une copie perdue se répare, deux copies perdues ne se
// remplacent JAMAIS par une clé neuve.
func TestSecretsKeyLossAndRepair(t *testing.T) {
	secretsTestHome(t)
	ref, err := secretPut("x", testAPIKey)
	if err != nil {
		t.Fatal(err)
	}
	// Copie principale perdue : la copie de secours suffit et la répare.
	if err := os.Remove(secretsKeyPath()); err != nil {
		t.Fatal(err)
	}
	secretCacheBust()
	if got, err := secretResolve(ref); err != nil || got != testAPIKey {
		t.Fatalf("avec la seule copie de secours : %q %v", got, err)
	}
	if _, err := os.Stat(secretsKeyPath()); err != nil {
		t.Fatal("la copie principale devait être réparée")
	}
	// Copie principale abîmée : idem.
	if err := os.WriteFile(secretsKeyPath(), []byte("n'importe quoi"), 0o600); err != nil {
		t.Fatal(err)
	}
	secretCacheBust()
	if got, err := secretResolve(ref); err != nil || got != testAPIKey {
		t.Fatalf("copie principale abîmée : %q %v", got, err)
	}
	// Les deux perdues : erreur à la lecture, refus d'en ranger d'autres, et
	// surtout aucune clé neuve fabriquée.
	os.Remove(secretsKeyPath())
	os.Remove(secretsKeyBakPath())
	secretCacheBust()
	if got, err := secretResolve(ref); err == nil || got != "" {
		t.Fatalf("sans clé du coffre : %q %v", got, err)
	}
	if _, err := secretPut("y", "autre-valeur"); err == nil {
		t.Fatal("ranger un secret sans la clé d'un coffre non vide devait être refusé")
	}
	if _, err := os.Stat(secretsKeyPath()); err == nil {
		t.Fatal("une clé neuve a été fabriquée à la place de la clé perdue")
	}
	if len(secretList()) != 1 {
		t.Fatal("le secret d'origine ne doit pas avoir été effacé")
	}
	// Mauvaise clé (fichier remplacé) : indéchiffrable, pas un contenu faux.
	other, _ := randBytes(memDEKLen)
	if err := writeSecretsKeyFile(secretsKeyPath(), other); err != nil {
		t.Fatal(err)
	}
	secretCacheBust()
	if got, err := secretResolve(ref); err == nil || got != "" {
		t.Fatalf("avec une autre clé : %q %v", got, err)
	}
}

const presetExt = "# NAME=Mon fournisseur\n# commentaire à garder\nEXTERNAL=1\nEXTERNAL_URL=https://api.exemple.test/v1\nEXTERNAL_MODEL=modele-x\nEXTERNAL_KEY=" + testAPIKey + "\nCTX=8192\n"

// Activation puis désactivation : les fichiers reviennent octet pour octet, et
// entre les deux la clé n'est plus dans les presets ni la configuration.
func TestSecretsEnableDisableReversible(t *testing.T) {
	secretsTestHome(t)
	p1 := writePresetFile(t, "fournisseur", presetExt)
	p2 := writePresetFile(t, "meme-cle", strings.Replace(presetExt, "Mon fournisseur", "Même clé", 1))
	local := "# NAME=Local\nMODEL=m.gguf\nCTX=4096\n"
	p3 := writePresetFile(t, "local", local)
	if err := applyPresetFile(p1); err != nil {
		t.Fatal(err)
	}
	if err := SetMCPServer("outil", MCPServerConfig{URL: "https://mcp.exemple.test", Headers: map[string]string{"Authorization": "Bearer jeton-mcp-123", "X-Version": "2"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if err := SetMCPServer("local", MCPServerConfig{Command: "serveur", Env: map[string]string{"GITHUB_TOKEN": "ghp_essai", "HOME_DIR": "/tmp"}, Enabled: true}); err != nil {
		t.Fatal(err)
	}

	rep, err := secretsEnable()
	if err != nil || len(rep.Skipped) != 0 {
		t.Fatalf("activation : %v %v", err, rep)
	}
	if !secretsOn() {
		t.Fatal("le coffre devait être activé")
	}
	c1 := readFileT(t, p1)
	if strings.Contains(c1, testAPIKey) || strings.Contains(readFileT(t, p2), testAPIKey) {
		t.Fatal("la clé est restée en clair dans un preset")
	}
	if !strings.Contains(c1, "# commentaire à garder") || !strings.HasPrefix(c1, "# NAME=Mon fournisseur\n") {
		t.Fatalf("le reste du preset a été modifié :\n%s", c1)
	}
	if readFileT(t, p3) != local {
		t.Fatal("un preset sans clé ne devait pas être touché")
	}
	ref := parseEnv(c1)[extKeyToken]
	if !isSecretRef(ref) || parseEnv(readFileT(t, p2))[extKeyToken] != ref {
		t.Fatalf("la même clé devait partager une seule entrée : %q", ref)
	}
	if got := ReadConfig()[extKeyToken]; got != ref {
		t.Fatalf("la configuration active devait reprendre la référence du preset actif : %q", got)
	}
	// Le preset actif reste reconnu comme actif.
	list, _ := ListPresets()
	for _, p := range list {
		if p.ID == "fournisseur" && !p.Active {
			t.Fatal("le preset actif n'est plus reconnu après l'activation du coffre")
		}
	}
	// L'endroit par où la clé part vers l'API la retrouve.
	if ep := resolveChatEndpoint(); ep.Key != testAPIKey || ep.KeyErr != nil || !ep.External {
		t.Fatalf("clé non résolue pour l'appel : %q %v", ep.Key, ep.KeyErr)
	}
	servers, _ := LoadMCPConfig()
	if h := servers["outil"].Headers; !isSecretRef(h["Authorization"]) || h["X-Version"] != "2" {
		t.Fatalf("en-têtes MCP : %v", h)
	}
	if e := servers["local"].Env; !isSecretRef(e["GITHUB_TOKEN"]) || e["HOME_DIR"] != "/tmp" {
		t.Fatalf("variables MCP : %v", e)
	}
	open, err := resolveMCPSecrets(servers["outil"])
	if err != nil || open.Headers["Authorization"] != "Bearer jeton-mcp-123" {
		t.Fatalf("jeton MCP non résolu à la connexion : %v %v", open.Headers, err)
	}
	if n := len(secretList()); n != 3 {
		t.Fatalf("3 secrets attendus (clé partagée + 2 jetons MCP), obtenu %d", n)
	}
	st := secretsStatus()
	if st["on"] != true || st["count"] != 3 || st["plain"] != 0 || st["key_ok"] != true {
		t.Fatalf("état : %v", st)
	}

	rep, err = secretsDisable()
	if err != nil || len(rep.Skipped) != 0 {
		t.Fatalf("désactivation : %v %v", err, rep)
	}
	if secretsOn() {
		t.Fatal("le coffre devait être désactivé")
	}
	if readFileT(t, p1) != presetExt {
		t.Fatalf("le preset n'est pas revenu à l'identique :\n%s", readFileT(t, p1))
	}
	if ReadConfig()[extKeyToken] != testAPIKey {
		t.Fatal("la configuration active n'a pas retrouvé sa clé en clair")
	}
	servers, _ = LoadMCPConfig()
	if servers["outil"].Headers["Authorization"] != "Bearer jeton-mcp-123" || servers["local"].Env["GITHUB_TOKEN"] != "ghp_essai" {
		t.Fatalf("jetons MCP non remis en clair : %v %v", servers["outil"].Headers, servers["local"].Env)
	}
	if n := len(secretList()); n != 0 {
		t.Fatalf("le coffre devait être vide après la remise en clair : %d", n)
	}
	if ep := resolveChatEndpoint(); ep.Key != testAPIKey {
		t.Fatal("après désactivation, la clé en clair doit marcher comme avant")
	}
}

// Désactivation avec une clé de coffre perdue : rien n'est touché, rien n'est effacé.
func TestSecretsDisableRefusesWhenUnreadable(t *testing.T) {
	secretsTestHome(t)
	p := writePresetFile(t, "fournisseur", presetExt)
	if _, err := secretsEnable(); err != nil {
		t.Fatal(err)
	}
	sealed := readFileT(t, p)
	os.Remove(secretsKeyPath())
	os.Remove(secretsKeyBakPath())
	secretCacheBust()
	if _, err := secretsDisable(); err == nil {
		t.Fatal("la désactivation devait refuser : une référence est illisible")
	}
	if readFileT(t, p) != sealed || len(secretList()) != 1 || !secretsOn() {
		t.Fatal("un refus ne doit rien changer")
	}
	// L'appel dit l'erreur au lieu d'envoyer une clé vide ou la référence.
	if err := applyPresetFile(p); err != nil {
		t.Fatal(err)
	}
	if ep := resolveChatEndpoint(); ep.KeyErr == nil || ep.Key != "" {
		t.Fatalf("clé illisible : attendu une erreur et aucune clé, obtenu %q %v", ep.Key, ep.KeyErr)
	}
}

// Clé du preset illisible : le tour s'arrête sur une erreur visible dans le fil,
// sans appeler l'API distante.
func TestRunChatReportsUnreadableKey(t *testing.T) {
	secretsTestHome(t)
	p := writePresetFile(t, "fournisseur", presetExt)
	if _, err := secretsEnable(); err != nil {
		t.Fatal(err)
	}
	if err := applyPresetFile(p); err != nil {
		t.Fatal(err)
	}
	os.Remove(secretsKeyPath())
	os.Remove(secretsKeyBakPath())
	secretCacheBust()
	var shown error
	_, err := runChat(context.Background(), []Message{{Role: "user", Content: "bonjour"}}, 0.7, Caps{}, func(ev StreamEvent) bool {
		if ev.Err != nil {
			shown = ev.Err
		}
		return true
	}, nil)
	if err == nil || shown == nil || !strings.Contains(shown.Error(), "coffre") {
		t.Fatalf("erreur attendue dans le fil : %v / %v", err, shown)
	}
	if strings.Contains(shown.Error(), testAPIKey) {
		t.Fatal("le message d'erreur ne doit pas contenir la clé")
	}
}

// Coffre éteint : SavePreset écrit comme en 0.18. Allumé : la clé part au coffre,
// et celle qu'on remplace en sort.
func TestSavePresetSealsOnlyWhenOn(t *testing.T) {
	secretsTestHome(t)
	body := "EXTERNAL=1\nEXTERNAL_URL=https://api.exemple.test/v1\nEXTERNAL_MODEL=m\nEXTERNAL_KEY=" + testAPIKey + "\n"
	id, err := SavePreset("", "Fournisseur", body)
	if err != nil {
		t.Fatal(err)
	}
	if c, _ := ReadPreset(id); !strings.Contains(c, "EXTERNAL_KEY="+testAPIKey) || len(secretList()) != 0 {
		t.Fatalf("coffre éteint : le preset doit garder sa clé en clair\n%s", c)
	}
	if _, err := secretsEnable(); err != nil {
		t.Fatal(err)
	}
	id2, err := SavePreset("", "Autre", strings.Replace(body, testAPIKey, "sk-autre-cle-999", 1))
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := ReadPreset(id2)
	ref2 := parseEnv(c2)[extKeyToken]
	if strings.Contains(c2, "sk-autre-cle-999") || !isSecretRef(ref2) {
		t.Fatalf("coffre allumé : la clé devait partir au coffre\n%s", c2)
	}
	// Réenregistrer sans toucher à la clé garde la même référence.
	if _, err := SavePreset(id2, "Autre", c2); err != nil {
		t.Fatal(err)
	}
	if c, _ := ReadPreset(id2); parseEnv(c)[extKeyToken] != ref2 {
		t.Fatal("la référence devait être conservée")
	}
	// Remplacer la clé : l'ancienne quitte le coffre, la nouvelle y entre.
	if _, err := SavePreset(id2, "Autre", strings.Replace(c2, ref2, "sk-nouvelle-cle-000", 1)); err != nil {
		t.Fatal(err)
	}
	c3, _ := ReadPreset(id2)
	ref3 := parseEnv(c3)[extKeyToken]
	if ref3 == ref2 || !isSecretRef(ref3) {
		t.Fatalf("nouvelle clé non rangée : %q", ref3)
	}
	if v, err := secretResolve(ref3); err != nil || v != "sk-nouvelle-cle-000" {
		t.Fatalf("nouvelle clé : %q %v", v, err)
	}
	if _, err := secretResolve(ref2); err == nil {
		t.Fatal("l'ancienne clé, plus référencée, devait quitter le coffre")
	}
	// Supprimer le preset retire sa clé ; celle de l'autre preset reste.
	if err := DeletePreset(id2); err != nil {
		t.Fatal(err)
	}
	if n := len(secretList()); n != 1 {
		t.Fatalf("après suppression : 1 secret attendu, obtenu %d", n)
	}
	c1, _ := ReadPreset(id)
	if v, err := secretResolve(parseEnv(c1)[extKeyToken]); err != nil || v != testAPIKey {
		t.Fatalf("la clé du preset restant a disparu : %q %v", v, err)
	}
}

// Le ménage ne retire rien quand une source est illisible.
func TestSecretsPruneKeepsWhenInDoubt(t *testing.T) {
	secretsTestHome(t)
	writePresetFile(t, "fournisseur", presetExt)
	if _, err := secretsEnable(); err != nil {
		t.Fatal(err)
	}
	// Liste des serveurs MCP illisible : impossible de savoir ce qui est référencé.
	if err := putStr(bkState, "mcp", "{pas du json"); err != nil {
		t.Fatal(err)
	}
	if _, err := secretPut("orphelin", "valeur-orpheline"); err != nil {
		t.Fatal(err)
	}
	if n := secretsPrune(); n != 0 || len(secretList()) != 2 {
		t.Fatalf("dans le doute, rien ne doit être retiré : %d retiré(s)", n)
	}
	_ = putStr(bkState, "mcp", "")
	if n := secretsPrune(); n != 1 || len(secretList()) != 1 {
		t.Fatalf("source redevenue lisible : l'orphelin seul devait partir (%d)", n)
	}
}

// Une ligne EXTERNAL_KEY inhabituelle : laissée en clair plutôt que mal réécrite.
func TestPresetSetExtKey(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"EXTERNAL_KEY=abc\n", "EXTERNAL_KEY=vault:0123456789abcdef\n"},
		{"  export EXTERNAL_KEY = \"abc\"\r\nCTX=1\r\n", "  export EXTERNAL_KEY = vault:0123456789abcdef\r\nCTX=1\r\n"},
		{"A=1\nEXTERNAL_KEY=abc", "A=1\nEXTERNAL_KEY=vault:0123456789abcdef"},
	} {
		got, ok := presetSetExtKey(tc.in, "vault:0123456789abcdef")
		if !ok || got != tc.want {
			t.Errorf("%q → %q (ok=%v), attendu %q", tc.in, got, ok, tc.want)
		}
	}
	if _, ok := presetSetExtKey("CTX=1\n", "vault:0123456789abcdef"); ok {
		t.Error("sans ligne EXTERNAL_KEY, rien à remplacer")
	}
	// Valeur avec espaces : relue à l'identique.
	if got, ok := presetSetExtKey("EXTERNAL_KEY=vault:0123456789abcdef\n", "clé avec espaces"); !ok || parseEnv(got)[extKeyToken] != "clé avec espaces" {
		t.Errorf("valeur avec espaces : %q", got)
	}
}

// La sauvegarde emporte les clés du coffre ; une machine vierge les retrouve.
func TestBundleCarriesSecrets(t *testing.T) {
	secretsTestHome(t)
	writePresetFile(t, "fournisseur", presetExt)
	if _, err := secretsEnable(); err != nil {
		t.Fatal(err)
	}
	// Une entrée abîmée ne doit pas faire échouer toute la sauvegarde.
	if err := putBytes(bkSecrets, "abime", []byte(`{"label":"x","box":"pas-du-base64"}`)); err != nil {
		t.Fatal(err)
	}
	secretCacheBust()
	tarData, err := buildBundleTar()
	if err != nil {
		t.Fatal(err)
	}
	// Machine vierge : autre dossier, donc autre clé de coffre.
	secretsTestHome(t)
	ensureDefaultProject()
	if err := restoreBundleTar(tarData); err != nil {
		t.Fatal(err)
	}
	c, err := ReadPreset("fournisseur")
	if err != nil {
		t.Fatal(err)
	}
	ref := parseEnv(c)[extKeyToken]
	if !isSecretRef(ref) {
		t.Fatalf("preset restauré : %q", ref)
	}
	if v, err := secretResolve(ref); err != nil || v != testAPIKey {
		t.Fatalf("la clé d'API n'a pas suivi la sauvegarde : %q %v", v, err)
	}
	// Sans coffre, la sauvegarde reste celle de la 0.18 (pas de secrets.json).
	secretsTestHome(t)
	writePresetFile(t, "fournisseur", presetExt)
	if b, err := secretsExport(); err != nil || b != nil {
		t.Fatalf("coffre vide : rien à exporter (%v)", err)
	}
	if err := secretsImport([]byte("{pas du json")); err == nil {
		t.Fatal("des secrets illisibles devaient être refusés")
	}
}
