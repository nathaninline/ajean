package ajean

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeEngine020 pose un moteur épinglé tel que la 0.20 l'a installé : marqueur
// engine.json sans le champ « ajean » (la 0.21 l'écrit, la 0.20 non).
func fakeEngine020(t *testing.T, tag string) string {
	t.Helper()
	bin := fakeEngineDir(t, tag)
	marker := `{"tag":"` + tag + `","variant":"cpu","label":"CPU (0.20)","asset":"x.zip","bin":"` + engineExeName() + `","installed_at":"2026-01-01T00:00:00Z"}`
	if err := os.WriteFile(filepath.Join(filepath.Dir(bin), engineMarker), []byte(marker), 0o644); err != nil {
		t.Fatal(err)
	}
	return bin
}

// fakeModel pose un .gguf dans le dossier des modèles et renvoie son chemin.
func fakeModel(t *testing.T, name string) string {
	t.Helper()
	if err := os.MkdirAll(modelsDir(), 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(modelsDir(), name)
	if err := os.WriteFile(p, []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// savePresetT enregistre un preset et renvoie son id.
func savePresetT(t *testing.T, name, content string) string {
	t.Helper()
	id, err := SavePreset("", name, content)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// snapshotInstall : tout ce qu'une passe de reprise pourrait toucher (fichiers
// de presets, configuration active, réglages en base), pour comparer avant/après.
func snapshotInstall(t *testing.T) map[string]string {
	t.Helper()
	snap := map[string]string{}
	ents, _ := os.ReadDir(presetsDir())
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(presetsDir(), e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		snap["preset:"+e.Name()] = string(b)
	}
	snap["config"] = formatEnv(ReadConfig())
	for _, k := range []string{"agent", "internet", "active_preset", "sysprompt", migrated019Key, engine020JobKey, stateExtService, "engine_variant"} {
		snap["state:"+k] = getStr(bkState, k)
	}
	return snap
}

func diffSnapshots(t *testing.T, what string, before, after map[string]string) {
	t.Helper()
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s : %s a changé\n  avant : %q\n  après : %q", what, k, v, after[k])
		}
	}
	for k := range after {
		if _, ok := before[k]; !ok {
			t.Errorf("%s : %s est apparu (%q)", what, k, after[k])
		}
	}
}

// 0.18.17 → 0.21 : RIEN à faire. Une installation 0.18 dont le preset actif
// désigne un moteur compilé, avec des presets ExLlama, MoE, GPU cloud, API et à
// unité système, traverse les passes de reprise sans qu'un octet ne bouge, et
// sans que le moteur épinglé soit installé.
func TestMigration018RienNeChange(t *testing.T) {
	testHome(t)
	failIfEngineDownloads(t)
	compiled := fakeCompiledEngine(t)
	model := fakeModel(t, "qwen.gguf")

	local := savePresetT(t, "Qwen local", "BIN=\""+compiled+"\"\nMODEL="+model+"\nCTX=8192\nEXTRA_ARGS=\"--mlock --flash-attn on\"\n")
	exl3 := savePresetT(t, "ExLlama", "ENGINE=exl3\nEXL3_MODEL=mon-modele\nCTX=16384\n")
	savePresetT(t, "MoE", "ENGINE=moe\nMOE_CONFIG=/srv/moe/config.json\n")
	savePresetT(t, "Cloud", "CLOUD=modal\nCLOUD_MODEL=https://huggingface.co/a/b/resolve/main/m.gguf\n")
	savePresetT(t, "API", "EXTERNAL=1\nEXTERNAL_URL=https://api.exemple.com/v1\nEXTERNAL_MODEL=m\nBIN=\""+compiled+"\"\nPORT=8080\n")
	savePresetT(t, "Unité", "EXTERNAL=1\nEXTERNAL_URL=http://127.0.0.1:9002/v1\nEXTERNAL_MODEL=m\nEXTERNAL_SERVICE=ajean-moe\n")
	if err := applyPresetFile(filepath.Join(presetsDir(), local+".env")); err != nil {
		t.Fatal(err)
	}
	// Réglages de la 0.18 : agent et internet coupés (clés absentes), un prompt
	// système propre au preset.
	if err := setPresetSysPrompt(local, "Réponds en alexandrins."); err != nil {
		t.Fatal(err)
	}

	before := snapshotInstall(t)
	migrateOlderVersions()
	migrateOlderVersions() // au démarrage du service ET de l'interface
	diffSnapshots(t, "0.18 → 0.21", before, snapshotInstall(t))

	if cameFrom020() {
		t.Error("installation 0.18 prise pour une installation 0.20")
	}
	cfg := ReadConfig()
	if bin, pinned := resolveBin(cfg); pinned || bin != compiled {
		t.Errorf("moteur du preset actif : %s (épinglé=%v), attendu le moteur compilé", bin, pinned)
	}
	if engineWanted() {
		t.Error("le moteur épinglé serait téléchargé chez quelqu'un qui a déjà son moteur")
	}
	if err := preflightEngine(); err != nil {
		t.Errorf("pré-vol du preset 0.18 : %v", err)
	}
	if bin, err := serveBin(cfg); err != nil || bin != compiled {
		t.Errorf("le service lancerait %s (%v), attendu le moteur compilé", bin, err)
	}
	if agentEnabled() || internetEnabled() {
		t.Error("le mode agent ou l'accès internet ont été activés sur une installation 0.18")
	}
	if readSysPrompt() != "Réponds en alexandrins." {
		t.Errorf("prompt système du preset : %q", readSysPrompt())
	}

	// Même chose avec le preset ExLlama actif : il garde son moteur.
	if err := applyPresetFile(filepath.Join(presetsDir(), exl3+".env")); err != nil {
		t.Fatal(err)
	}
	before = snapshotInstall(t)
	migrateOlderVersions()
	diffSnapshots(t, "0.18 → 0.21, preset ExLlama actif", before, snapshotInstall(t))
	if c := ReadConfig(); !isExl3Config(c) || usesLlamaServer(c) || usesPinnedEngine(c) || engineWanted() {
		t.Errorf("preset ExLlama actif : config=%v, moteur épinglé voulu=%v", c, engineWanted())
	}
	if err := preflightEngine(); err == nil || !strings.Contains(err.Error(), "ExLlama") || strings.Contains(err.Error(), "retiré") {
		t.Errorf("le preset ExLlama doit être confié à ExLlama (ici non installé), obtenu : %v", err)
	}
	if engineInstalled() != nil || isDir(engineRoot()) {
		t.Error("le moteur épinglé a été installé pendant la reprise d'une installation 0.18")
	}
}

// 0.20.x → 0.21 : les presets Local restent sur le moteur épinglé (déjà installé,
// pas retéléchargé), les presets Service restent valides, les presets ExLlama,
// MoE et GPU cloud redémarrent, le mode agent et l'accès internet restent
// actifs, rien n'est supprimé du disque.
func TestMigrateFrom020(t *testing.T) {
	home := testHome(t)
	failIfEngineDownloads(t)
	old := engineManifestTest
	engineManifestTest = &engineManifest{Tag: "b100"} // la 0.21 épingle le même tag que la 0.20
	t.Cleanup(func() { engineManifestTest = old })
	pinned := fakeEngine020(t, "b100")
	compiled := fakeCompiledEngine(t) // moteur de la 0.18 resté sur le disque
	// Le précompilé de la 0.18, lui, a été effacé par la 0.20.
	gone := filepath.Join(prebuiltDir(), "llama-b9000", engineExeName())
	model := fakeModel(t, "qwen.gguf")
	engineLog := filepath.Join(home, "ajean-engine.log")
	_ = os.WriteFile(engineLog, []byte("▶ téléchargement…\n"), 0o644)
	_ = putBytes(bkState, engine020JobKey, []byte(`{"action":"engine","phase":"llama.cpp b100 prêt","running":false}`))

	created020 := "MODEL=" + model + "\nCTX=4096\nHOST=0.0.0.0\nPORT=8080\n"
	service := "EXTERNAL=1\nEXTERNAL_URL=http://127.0.0.1:9001/v1\nSERVICE_CMD=vllm serve mon/modele --port 9001\nSERVICE_ENV=HF_HOME=/data\nCTX=65536\n"
	exl3 := "ENGINE=exl3\nEXL3_MODEL=mon-modele\n"
	moe := "ENGINE=moe\nMOE_CONFIG=/srv/moe/config.json\n"
	cloud := "CLOUD=modal\nCLOUD_MODEL=https://huggingface.co/a/b/resolve/main/m.gguf\n"
	ids := map[string]string{
		"créé en 0.20":     savePresetT(t, "Qwen 0.20", created020),
		"hérité précompil": savePresetT(t, "Qwen hérité", "BIN=\""+gone+"\"\nMODEL="+model+"\nCTX=8192\nEXTRA_ARGS=\"--flash-attn auto\"\n"),
		"hérité compilé":   savePresetT(t, "Qwen compilé", "export BIN="+compiled+"\nMODEL="+model+"\n"),
		"service":          savePresetT(t, "vLLM", service),
		"exl3":             savePresetT(t, "ExLlama", exl3),
		"moe":              savePresetT(t, "MoE", moe),
		"cloud":            savePresetT(t, "Cloud", cloud),
		"api":              savePresetT(t, "API", "EXTERNAL=1\nEXTERNAL_URL=https://api.exemple.com/v1\nEXTERNAL_MODEL=m\nBIN=\""+gone+"\"\n"),
	}
	path := func(k string) string { return filepath.Join(presetsDir(), ids[k]+".env") }
	read := func(k string) string {
		b, err := os.ReadFile(path(k))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	// Preset actif : celui hérité de la 0.18, dont le BIN désigne un moteur disparu.
	if err := applyPresetFile(path("hérité précompil")); err != nil {
		t.Fatal(err)
	}
	_ = setPresetSysPrompt(ids["hérité précompil"], "Prompt de la 0.18.")
	untouched := map[string]string{}
	for _, k := range []string{"créé en 0.20", "service", "exl3", "moe", "cloud"} {
		untouched[k] = read(k)
	}
	if !cameFrom020() {
		t.Fatal("installation 0.20 non reconnue")
	}

	migrateOlderVersions()

	// Presets Local : tous sur le moteur épinglé ; le chemin de l'ancien moteur
	// reste lisible dans le preset.
	for k, bin := range map[string]string{"hérité précompil": gone, "hérité compilé": compiled, "api": gone} {
		c := read(k)
		cfg := parseEnv(c)
		if cfg["BIN"] != "" {
			t.Errorf("%s : BIN de la 0.18 encore actif (%q)", k, cfg["BIN"])
		}
		if !strings.Contains(c, bin020Note+bin) {
			t.Errorf("%s : l'ancien chemin n'a pas été gardé en commentaire :\n%s", k, c)
		}
		if k != "api" && (!usesPinnedEngine(cfg) || cfg["MODEL"] != model) {
			t.Errorf("%s : pas sur le moteur épinglé, ou réglages perdus : %v", k, cfg)
		}
	}
	if c := parseEnv(read("hérité précompil")); c["CTX"] != "8192" || c["EXTRA_ARGS"] != "--flash-attn auto" {
		t.Errorf("réglages du preset hérité perdus : %v", c)
	}
	// Les autres presets ne bougent pas d'un octet.
	for k, want := range untouched {
		if got := read(k); got != want {
			t.Errorf("%s modifié :\n%s", k, got)
		}
	}
	if !usesPinnedEngine(parseEnv(read("créé en 0.20"))) {
		t.Error("un preset Local créé en 0.20 (sans BIN) doit tourner sur le moteur épinglé")
	}
	// Service : toujours un preset Service valide.
	if svc := parseEnv(read("service")); serviceCmdOf(svc) == "" || usesRemoteEndpoint(svc) || extBaseURL(svc) != "http://127.0.0.1:9001/v1" {
		t.Errorf("preset Service : %v", svc)
	}
	// ExLlama, MoE, GPU cloud : confiés à leur moteur, plus de « moteur retiré ».
	for _, k := range []string{"exl3", "moe", "cloud"} {
		if c := parseEnv(read(k)); usesLlamaServer(c) || usesPinnedEngine(c) {
			t.Errorf("%s pris pour un preset llama.cpp", k)
		}
	}

	// Configuration active : le moteur épinglé déjà installé, sans téléchargement.
	cfg := ReadConfig()
	if cfg["BIN"] != "" || cfg["MODEL"] != model {
		t.Errorf("configuration active : %v", cfg)
	}
	if bin, isPinned := resolveBin(cfg); !isPinned || !samePath(bin, pinned) {
		t.Errorf("moteur de la configuration active : %s (épinglé=%v), attendu %s", bin, isPinned, pinned)
	}
	if bin, err := serveBin(cfg); err != nil || !samePath(bin, pinned) {
		t.Errorf("le service lancerait %s (%v), attendu le moteur de la 0.20 réutilisé", bin, err)
	}
	if engineWanted() {
		t.Error("le moteur épinglé déjà installé serait retéléchargé")
	}
	if err := preflightEngine(); err != nil {
		t.Errorf("pré-vol après reprise : %v", err)
	}
	if getStr(bkState, "active_preset") != ids["hérité précompil"] {
		t.Errorf("preset actif perdu : %q", getStr(bkState, "active_preset"))
	}

	// Mode agent et accès internet : actifs d'office en 0.20, ils le restent.
	if !agentEnabled() || !internetEnabled() {
		t.Errorf("agent=%v internet=%v après reprise, attendu actifs", agentEnabled(), internetEnabled())
	}
	// Le prompt système du preset, ignoré par la 0.20, est resté en base.
	if readSysPrompt() != "Prompt de la 0.18." {
		t.Errorf("prompt système du preset : %q", readSysPrompt())
	}
	// Rien n'est supprimé du disque.
	for _, p := range []string{pinned, compiled, engineLog, model, filepath.Join(home, "backends", "llama.cpp")} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("supprimé pendant la reprise : %s", p)
		}
	}

	// La passe ne se rejoue pas.
	if cameFrom020() {
		t.Error("installation encore prise pour une 0.20 après la reprise")
	}
	if e := engineInstalled(); e == nil || e.By != "0.20" || e.Label != "CPU (0.20)" {
		t.Errorf("marqueur du moteur après reprise : %+v", e)
	}
	_ = setAgentEnabled(false) // l'utilisateur coupe l'agent en 0.21
	before := snapshotInstall(t)
	migrateOlderVersions()
	diffSnapshots(t, "seconde reprise", before, snapshotInstall(t))
}

// Un agent coupé explicitement en 0.20 (« ajean agent off », noté "0") reste
// coupé ; la 0.21 relit les deux formes de la clé.
func TestMigrateFrom020AgentCoupe(t *testing.T) {
	testHome(t)
	_ = putBytes(bkState, engine020JobKey, []byte(`{"action":"engine"}`))
	_ = putStr(bkState, "agent", "0")
	if agentEnabled() {
		t.Fatal(`"0" doit se lire « coupé »`)
	}
	migrateOlderVersions()
	if agentEnabled() {
		t.Error("un agent coupé en 0.20 a été réactivé")
	}
	if !internetEnabled() {
		t.Error("l'accès internet, actif d'office en 0.20, a été coupé")
	}
	_ = putStr(bkState, "agent", "1") // forme écrite par « ajean agent on » en 0.20
	if !agentEnabled() {
		t.Error(`"1" doit se lire « actif »`)
	}
}

// Le tag épinglé a changé entre la 0.20 installée et cette version : le moteur
// de l'ancien tag n'est pas pris pour le bon, le nouveau est installé au
// démarrage du service, et la configuration reste sur le moteur épinglé.
func TestMigrateFrom020AutreTag(t *testing.T) {
	testHome(t)
	f := newFakeRelease(t)
	engineTestSetup(t, "b200", f, func(string) error { return nil })
	fakeEngine020(t, "b100")
	model := fakeModel(t, "qwen.gguf")
	setConfig(t, "MODEL="+model+"\n")

	migrateOlderVersions()
	if engineInstalled() != nil {
		t.Fatal("le moteur de l'ancien tag passe pour celui de cette version")
	}
	if !engineWanted() {
		t.Error("la configuration active tourne sur le moteur épinglé : il doit être installé")
	}
	bin, err := serveBin(ReadConfig())
	if err != nil {
		t.Fatal(err)
	}
	if e := engineInstalled(); e == nil || e.Tag != "b200" || e.By != Version || !samePath(bin, e.binPath()) {
		t.Errorf("moteur après mise à jour : %+v", e)
	}
}

// dropStaleBin ne touche qu'aux lignes BIN qui désignent un moteur de la 0.18.
func TestDropStaleBin(t *testing.T) {
	testHome(t)
	pinned := filepath.Join(engineDir("b90"), engineExeName())
	in := "# NAME=Test\nMODEL=m.gguf\nBIN=\"/opt/llama/llama-server\"\n# BIN=/ancien/commentaire\nEXTRA_ARGS=\"--device CUDA0\"\n"
	out, changed := dropStaleBin(in)
	if !changed || strings.Contains(out, "\nBIN=") || !strings.Contains(out, bin020Note+"/opt/llama/llama-server") ||
		!strings.Contains(out, "# BIN=/ancien/commentaire") || !strings.Contains(out, "MODEL=m.gguf") || !strings.Contains(out, `EXTRA_ARGS="--device CUDA0"`) {
		t.Errorf("réécriture inattendue :\n%s", out)
	}
	if strings.Count(out, "\n") != strings.Count(in, "\n") {
		t.Errorf("le nombre de lignes a changé :\n%s", out)
	}
	for _, keep := range []string{"MODEL=m.gguf\n", "BIN=\nMODEL=m.gguf\n", "BIN=\"" + pinned + "\"\nMODEL=m.gguf\n"} {
		if got, changed := dropStaleBin(keep); changed || got != keep {
			t.Errorf("preset sur le moteur épinglé réécrit : %q → %q", keep, got)
		}
	}
}
