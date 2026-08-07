package ajean

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// Test de la reprise 0.7 → 0.8. À supprimer avec migrate_07.go.
//
// Ce code déplace les presets, la mémoire et des modèles de plusieurs dizaines
// de gigaoctets sur la machine de quelqu'un : il ne peut pas ne pas être testé.

// fake07Home fabrique un dossier de données dans l'ancienne disposition.
func fake07Home(t *testing.T) string {
	t.Helper()
	home := testHome(t)
	stopLegacyUnits = func() {} // pas de systemctl pendant les tests
	t.Cleanup(func() { stopLegacyUnits = stopLegacyUnitsReal })

	write := func(rel, body string) {
		p := filepath.Join(home, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("config.env", "MODEL=\"m.gguf\"\nCTX=4096\nBIN=/usr/bin/llama-server\n")
	write("configs/MON PRESET.env", "# NAME=MON PRESET\nMODEL=\"m.gguf\"\nCTX=8192\n")
	write("MEMORY/user-preferences.md", "# Préférences\nfuseau: Europe/Paris\n")
	write("m.gguf", "gguf")
	write(".api_key", "sk-jean-secret\n")
	write(".link_token", "jl_abcdef123456\n")
	write(".agent_enabled", "")
	write("webprefs.json", `{"theme":"dark"}`)
	write("mcp.json", `{"mcpServers":{"fs":{"command":"npx","enabled":true}}}`)
	write("sysprompt.txt", "Tu es AJEAN.\n")
	write("conversation.json", `{"seq":42,"messages":[{"role":"user","content":"salut"}]}`)
	write("SKILLS/vieux-skill/SKILL.md", "# un skill d'avant\n")
	write(".authorized_users", "AABBCCDDEEFF00112233445566778899AABBCCDDEEFF001122334455667788AA\n")
	return home
}

func TestMigration07(t *testing.T) {
	home := fake07Home(t)
	if !needsMigration07(home) {
		t.Fatal("ancienne disposition non détectée")
	}
	if err := migrateFrom07(home); err != nil {
		t.Fatal(err)
	}

	// Les dossiers ont bougé, pas disparu.
	for _, c := range []struct{ path, want string }{
		{filepath.Join(home, "presets", "MON PRESET.env"), "CTX=8192"},
		{filepath.Join(home, "memory", "user-preferences.md"), "Europe/Paris"},
		{filepath.Join(home, "models", "m.gguf"), "gguf"},
	} {
		b, err := os.ReadFile(c.path)
		if err != nil {
			t.Fatalf("%s absent : %v", c.path, err)
		}
		if !strings.Contains(string(b), c.want) {
			t.Errorf("%s : contenu inattendu %q", c.path, string(b))
		}
	}
	// hasEntry et non os.Stat : sur un système de fichiers insensible à la casse,
	// Stat("MEMORY") réussit sur le dossier « memory » fraîchement créé.
	for _, gone := range []string{"configs", "MEMORY", "m.gguf", "config.env"} {
		if hasEntry(home, gone) {
			t.Errorf("%s traîne encore à la racine", gone)
		}
	}

	// Les réglages sont en base.
	cfg := ReadConfig()
	if cfg["MODEL"] != "m.gguf" || cfg["CTX"] != "4096" {
		t.Errorf("configuration non reprise : %v", cfg)
	}
	if readAPIKey() != "sk-jean-secret" {
		t.Errorf("clé API = %q", readAPIKey())
	}
	if readLinkToken() != "jl_abcdef123456" {
		t.Errorf("jeton de liaison = %q", readLinkToken())
	}
	if !agentEnabled() {
		t.Error("mode agent perdu")
	}
	if readSysPrompt() != "Tu es AJEAN." {
		t.Errorf("prompt système = %q", readSysPrompt())
	}
	if loadWebPrefs()["theme"] != "dark" {
		t.Error("préférences perdues")
	}
	if servers, _ := LoadMCPConfig(); servers["fs"].Command != "npx" {
		t.Errorf("serveurs MCP perdus : %v", servers)
	}
	LoadConversation()
	if conv.Seq != 42 {
		t.Errorf("conversation perdue (seq %d)", conv.Seq)
	}
	// Les identités appairées : les perdre casse le chat chiffré du portail
	// sans que rien ne le dise (le tunnel, lui, s'établit normalement).
	// La liste est mise en cache une fois par process (authOnce) : en production
	// c'est un process neuf qui sert après l'install, ici il faut le dire.
	authOnce, authSet = sync.Once{}, map[string]bool{}
	if !isAuthorizedUser("aabbccddeeff00112233445566778899aabbccddeeff001122334455667788aa") {
		t.Error("identité appairée perdue : le chat E2E du portail serait refusé")
	}

	// Les fichiers consommés sont rangés, pas détruits.
	for _, archive := range []string{"config.env", "SKILLS"} {
		if _, err := os.Stat(filepath.Join(home, "avant-0.8", archive)); err != nil {
			t.Errorf("%s n'a pas été conservé dans avant-0.8/", archive)
		}
	}
	if hasEntry(home, "SKILLS") {
		t.Error("SKILLS/ traîne encore à la racine")
	}

	// Idempotence : relancer ne doit plus rien voir à migrer.
	if needsMigration07(home) {
		t.Error("la migration se redéclencherait au prochain install")
	}
	if err := migrateFrom07(home); err != nil {
		t.Fatalf("second appel : %v", err)
	}
}

// Une installation 0.8 neuve ne doit rien déclencher.
func TestMigration07IgnoreUneInstallationNeuve(t *testing.T) {
	home := testHome(t)
	if err := provisionDataDir(); err != nil {
		t.Fatal(err)
	}
	if needsMigration07(home) {
		t.Fatal("migration déclenchée sur une installation neuve")
	}
}

// Machine migrée à la main : les nouveaux dossiers sont remplis ET les anciens
// traînent encore. L'install ne doit rien écraser, ni échouer.
func TestMigration07NEcrasePasUnDossierDejaRempli(t *testing.T) {
	home := fake07Home(t)
	precieux := filepath.Join(home, "presets", "DEJA LA.env")
	if err := os.MkdirAll(filepath.Dir(precieux), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(precieux, []byte("CTX=1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := migrateFrom07(home); err != nil {
		t.Fatalf("la migration doit se poursuivre, pas échouer : %v", err)
	}
	b, err := os.ReadFile(precieux)
	if err != nil || !strings.Contains(string(b), "CTX=1") {
		t.Error("le preset déjà migré a été écrasé")
	}
	if !hasEntry(home, "configs") {
		t.Error("l'ancien dossier a été supprimé alors qu'il fallait le laisser")
	}
}
