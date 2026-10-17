package ajean

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func moeArgs(t *testing.T, cfg map[string]any) []string {
	t.Helper()
	var out []string
	for _, a := range cfg["args"].([]any) {
		out = append(out, a.(string))
	}
	return out
}

func argAfter(args []string, flag string) string {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag {
			return args[i+1]
		}
	}
	return ""
}

func TestMoeBuildConfigApplyTuning(t *testing.T) {
	base := map[string]any{
		"exe":  "/x/engine-cuda12/strata",
		"args": []any{"--pack", "/p", "--prefill", "8192", "--max-context", "131072"},
		"port": 8080.0,
	}
	out, err := moeBuildConfig(base, map[string]string{"PORT": "8081", "CTX": "65536"}, "k")
	if err != nil {
		t.Fatal(err)
	}
	a := moeArgs(t, out)
	for flag, want := range map[string]string{"--prefill": "16384", "--short-read": "150", "--prompt-cache-root": "256", "--max-context": "65536", "--pack": "/p"} {
		if got := argAfter(a, flag); got != want {
			t.Errorf("%s = %q, attendu %q (args %v)", flag, got, want, a)
		}
	}
	if strings.Count(strings.Join(a, " "), "--prefill") != 1 {
		t.Errorf("--prefill dupliqué : %v", a)
	}
	if hasArg(a, "--remote-expert-opt") || hasArg(a, "--expert-cache-device1") {
		t.Errorf("aide 2e carte sans 2e carte : %v", a)
	}
	if out["port"] != 8081 || out["api_key"] != "k" || out["host"] != "0.0.0.0" {
		t.Errorf("port/clé/hôte : %v %v %v", out["port"], out["api_key"], out["host"])
	}
	// la config de l'installeur n'est pas modifiée
	if base["args"].([]any)[3] != "8192" {
		t.Error("la config de base a été modifiée")
	}
}

func TestMoeBuildConfigHelperGPU(t *testing.T) {
	base := map[string]any{"args": []any{"--pack", "/p", "--mmap-experts", "--vram-reserve-mib", "700"}, "gpu": 1.0,
		"vision": map[string]any{"exe": "v", "gpu": true}}
	out, err := moeBuildConfig(base, map[string]string{"MOE_HELPER_GPU": "0"}, "")
	if err != nil {
		t.Fatal(err)
	}
	a := moeArgs(t, out)
	if argAfter(a, "--expert-cache-device1") != "auto" || !hasArg(a, "--remote-expert-opt") {
		t.Errorf("aide 2e carte absente : %v", a)
	}
	if hasArg(a, "--vram-reserve-mib") || hasArg(a, "700") {
		t.Errorf("réserve de VRAM gardée : %v", a)
	}
	if _, has := out["api_key"]; has {
		t.Error("api_key posée sans clé")
	}
	if _, has := out["gpu"]; has {
		t.Error("clé gpu gardée avec une carte d'aide (le serveur en ferait un --layer-split)")
	}
	if out["vision"].(map[string]any)["cuda_device"] != 0 {
		t.Errorf("encodeur pas sur la carte d'aide : %v", out["vision"])
	}
	if out["env"].(map[string]any)["STRATA_PLE_BATCH"] != "0" {
		t.Errorf("STRATA_PLE_BATCH absent : %v", out["env"])
	}
}

func TestMoeDropFits(t *testing.T) {
	q := moeQuants["IQ3_XXS"]
	env := moeEnv{Main: 1, Helper: 0, RAMGB: 46.9, GPUs: []moeGPU{{Index: 0, VRAMGB: 7.8}, {Index: 1, VRAMGB: 15.9}}}
	if !moeDropFits(q, env) {
		t.Error("serveur de test (46 Go + 3070) : le mode hors RAM devrait tenir")
	}
	f := moeFitFor(q, env, 0)
	if f.Mmap || !f.Drop {
		t.Errorf("fit = mmap %v drop %v, attendu drop", f.Mmap, f.Drop)
	}
	env.Helper = -1
	if moeDropFits(q, env) {
		t.Error("sans carte d'aide, pas de mode hors RAM")
	}
	env.Helper, env.RAMGB = 0, 32
	if moeDropFits(q, env) {
		t.Error("32 Go : ne tient pas, doit rester en mmap")
	}
}

func TestMoeBuildConfigDrop(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AJEAN_HOME", home)
	pack := filepath.Join(home, "pack")
	_ = os.MkdirAll(pack, 0o755)
	base := map[string]any{"args": []any{"--pack", pack, "--pcie-frac", "0.3"}, "env": map[string]any{}}
	cfg := map[string]string{"MOE_HELPER_GPU": "0", "MOE_DROP": "1", "MOE_MMAP": "0"}
	// sans experts.bin ni moteur à jour : repli mmap (ce lancement écrit experts.bin)
	out, _ := moeBuildConfig(base, cfg, "")
	if out["env"].(map[string]any)["STRATA_ARENA_MMAP"] != "1" || out["env"].(map[string]any)["STRATA_REMOTE_DROP"] != nil {
		t.Fatalf("repli mmap attendu : %v", out["env"])
	}
	_ = os.WriteFile(filepath.Join(pack, "experts.bin"), []byte("x"), 0o644)
	_ = os.MkdirAll(moeEngineDir(), 0o755)
	_ = os.WriteFile(moeEngineMarker(), []byte(moeEngineAssetName()+"\n"), 0o644)
	out, _ = moeBuildConfig(base, cfg, "")
	e := out["env"].(map[string]any)
	if e["STRATA_REMOTE_DROP"] != "1" || e["STRATA_ARENA_MMAP"] != nil {
		t.Errorf("mode hors RAM attendu : %v", e)
	}
	if a := moeArgs(t, out); hasArg(a, "--pcie-frac") {
		t.Errorf("--pcie-frac gardé (la part PCIe doit être automatique) : %v", a)
	}
	// carte d'aide coupée : pas de mode hors RAM
	cfg["MOE_HELPER"] = "0"
	out, _ = moeBuildConfig(base, cfg, "")
	if out["env"].(map[string]any)["STRATA_REMOTE_DROP"] != nil {
		t.Error("mode hors RAM sans carte d'aide")
	}
}

func TestMoeBuildConfigMmap(t *testing.T) {
	base := map[string]any{"args": []any{"--pack", "/p", "--mmap-experts"}, "env": map[string]any{"X": "1"}}
	out, err := moeBuildConfig(base, map[string]string{"MOE_HELPER_GPU": "0"}, "")
	if err != nil {
		t.Fatal(err)
	}
	a := moeArgs(t, out)
	env := out["env"].(map[string]any)
	if hasArg(a, "--mmap-experts") || argAfter(a, "--pcie-frac") != "0" || env["STRATA_ARENA_MMAP"] != "1" || env["X"] != "1" {
		t.Errorf("mmap validé non appliqué : %v %v", a, env)
	}
}

// TestMoeLauncherArgs : les args du lanceur ne doivent pas contenir le
// programme (serveMoe le pose en tête, comme pour llama-server — sinon il est
// deux fois sur la ligne de commande et server.py reçoit un flag inattendu).
// Sous Linux le lanceur est écrit dans moeHome/ : le dossier doit exister
// (t.Setenv ne le crée pas). Sous Windows il n'y a rien à écrire, le python
// doit seulement exister. Ailleurs il n'y a pas de moteur : pas de test.
func TestMoeLauncherArgs(t *testing.T) {
	switch runtime.GOOS {
	case "linux", "windows":
	default:
		t.Skipf("pas de moteur MoE sous %s : rien à lancer", runtime.GOOS)
	}
	t.Setenv("AJEAN_HOME", t.TempDir())
	if err := os.MkdirAll(moeHome(), 0o755); err != nil {
		t.Fatal(err)
	}
	srcDir := t.TempDir()
	py := moeVenvPython(srcDir)
	_ = os.MkdirAll(filepath.Dir(py), 0o755)
	_ = os.WriteFile(py, []byte("x"), 0o755)
	prog, largs, err := moeWriteLauncher(srcDir, py)
	if err != nil {
		t.Fatal(err)
	}
	if prog == "" {
		t.Fatal("pas de programme")
	}
	for _, a := range largs {
		if a == prog {
			t.Errorf("le programme est injecté dans les args (doublé dans la commande) : %v", largs)
		}
	}
}

func TestMoeCudaDevices(t *testing.T) {
	cases := []struct {
		main, helper, want string
	}{
		{"1", "0", "1,0"},
		{"0", "-1", "0"},
		{"0", "", "0"},
		{"", "", ""},
	}
	for _, c := range cases {
		got := moeCudaDevices(map[string]string{"MOE_MAIN_GPU": c.main, "MOE_HELPER_GPU": c.helper})
		if got != c.want {
			t.Errorf("main=%q helper=%q : %q, attendu %q", c.main, c.helper, got, c.want)
		}
	}
}

func TestMoeRecommend(t *testing.T) {
	// la machine de test : 5060 Ti 16 Go + 3070 8 Go, 47 Go de RAM, disque large
	env := moeEnv{Supported: true, Main: 1, Helper: 0, RAMGB: 47, DiskFreeGB: 400,
		GPUs: []moeGPU{{Index: 0, VRAMGB: 8, Arch: 86}, {Index: 1, VRAMGB: 16, Arch: 120}}}
	if got := moeRecommend("swift", env); got != "IQ3_XXS" {
		t.Errorf("swift : %q, attendu IQ3_XXS", got)
	}
	// IQ3_S demande de garder 50 Go d'experts en RAM : pas sur 47 Go
	if got := moeRecommend("qwen", env); got != "IQ3_XXS" {
		t.Errorf("qwen 47 Go : %q, attendu IQ3_XXS", got)
	}
	f := moeFitFor(moeQuants["IQ3_XXS"], env, 0)
	// trop juste pour tout garder en RAM, mais la 3070 garde les siens : mode hors RAM
	if !f.OK || f.Mmap || !f.Drop {
		t.Errorf("IQ3_XXS sur 47 Go : ok=%v mmap=%v drop=%v (attendu ok, hors RAM)", f.OK, f.Mmap, f.Drop)
	}
	// 64 Go : IQ3_S tient en RAM
	env.RAMGB = 64
	if got := moeRecommend("qwen", env); got != "IQ3_S" {
		t.Errorf("qwen 64 Go : %q, attendu IQ3_S", got)
	}
	// 16 Go de RAM et une seule carte de 8 Go : rien ne tient
	small := moeEnv{Supported: true, Main: 0, Helper: -1, RAMGB: 16, DiskFreeGB: 400,
		GPUs: []moeGPU{{Index: 0, VRAMGB: 8, Arch: 86}}}
	if got := moeRecommend("swift", small); got != "" {
		t.Errorf("petite machine : %q, attendu aucune recommandation", got)
	}
	// disque trop petit
	env.DiskFreeGB = 50
	if f := moeFitFor(moeQuants["Q2_0"], env, 0); f.OK {
		t.Error("Q2_0 accepté avec 50 Go de disque")
	}
}

func TestMoePresetName(t *testing.T) {
	if got := moePresetName("swift", "IQ3_XXS"); got != "FLASH NEXT SWIFT 1.5 IQ3_XXS" {
		t.Errorf("%q", got)
	}
	if got := moePresetName("qwen", "Q2_0"); got != "FLASH NEXT CLASSIQUE Q2_0" {
		t.Errorf("%q", got)
	}
}

func TestMoeNeutral(t *testing.T) {
	upstream := "Str" + "ata"
	if got := moeNeutral("  [ok] " + upstream + " engine copied to /x/" + strings.ToLower(upstream) + "-0.1.39"); strings.Contains(strings.ToLower(got), strings.ToLower(upstream)) {
		t.Errorf("%q", got)
	}
}

func TestMoeBuildConfigMmapFromPreset(t *testing.T) {
	base := map[string]any{"args": []any{"--pack", "/p"}}
	out, err := moeBuildConfig(base, map[string]string{"MOE_MMAP": "1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	a := moeArgs(t, out)
	if argAfter(a, "--pcie-frac") != "0" || out["env"].(map[string]any)["STRATA_ARENA_MMAP"] != "1" {
		t.Errorf("MOE_MMAP=1 non appliqué : %v %v", a, out["env"])
	}
	out, _ = moeBuildConfig(base, map[string]string{"MOE_MMAP": "0"}, "")
	if out["env"].(map[string]any)["STRATA_ARENA_MMAP"] != nil {
		t.Errorf("mmap appliqué sans MOE_MMAP : %v", out["env"])
	}
}

func TestMoeFitCountsExistingData(t *testing.T) {
	env := moeEnv{Supported: true, Main: 1, Helper: 0, RAMGB: 47, DiskFreeGB: 32,
		GPUs: []moeGPU{{Index: 0, VRAMGB: 8, Arch: 86}, {Index: 1, VRAMGB: 16, Arch: 120}}}
	if f := moeFitFor(moeQuants["IQ3_XXS"], env, 120); !f.OK {
		t.Errorf("données déjà présentes, refusé quand même : %s", f.Why)
	}
	if f := moeFitFor(moeQuants["IQ3_XXS"], env, 0); f.OK {
		t.Error("32 Go libres sans données : accepté")
	}
}

func TestMoeBuildConfigPresetOverrides(t *testing.T) {
	base := map[string]any{"args": []any{"--kv", "int8", "--spec", "4"}}
	out, _ := moeBuildConfig(base, map[string]string{"MOE_KV": "fp16", "MOE_SPEC": "3"}, "")
	a := moeArgs(t, out)
	if argAfter(a, "--kv") != "fp16" || argAfter(a, "--spec") != "3" {
		t.Errorf("%v", a)
	}
}

func TestMoeDetailsFor(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AJEAN_HOME", home)
	base := filepath.Join(home, "base.json")
	if err := os.WriteFile(base, []byte(`{"args":["--pack","/p","--kv","int8","--spec","4","--max-context","131072"],"vision":{"exe":"v"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	_ = os.MkdirAll(presetsDir(), 0o755)
	preset := "ENGINE=moe\nMOE_CONFIG=" + base + "\nMOE_MAIN_GPU=1\nMOE_HELPER_GPU=0\nMOE_MMAP=1\nMOE_KV=fp16\nMOE_KV_RESIDENT=32768\nMOE_SPEC=3\n"
	if err := os.WriteFile(filepath.Join(presetsDir(), "X.env"), []byte(preset), 0o644); err != nil {
		t.Fatal(err)
	}
	env := moeEnv{GPUs: []moeGPU{{Index: 0, Name: "NVIDIA GeForce RTX 3070", VRAMGB: 8}, {Index: 1, Name: "NVIDIA GeForce RTX 5060 Ti", VRAMGB: 16}}}
	d := moeDetailsFor("X", env)
	if d == nil {
		t.Fatal("pas de détails")
	}
	if d.KV != "fp16" || d.KVResident != "32768" || d.Spec != "3" || d.Ctx != "131072" || !d.Mmap ||
		d.MainGPU != "RTX 5060 Ti (16 Go)" || d.HelperGPU != "RTX 3070 (8 Go)" || d.VisionGPU != "RTX 3070 (8 Go)" ||
		d.ShortRead != "48" || d.CacheRoot != "256" || d.Prefill != "16384" {
		t.Errorf("%+v", *d)
	}
}

func TestMoeHaveGBPerChoice(t *testing.T) {
	t.Setenv("AJEAN_HOME", t.TempDir())
	write := func(rel string, n int) {
		p := filepath.Join(moeDataDir(), rel)
		_ = os.MkdirAll(filepath.Dir(p), 0o755)
		if err := os.WriteFile(p, make([]byte, n), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("models/swift-IQ3_XXS/a.gguf", 3000)
	write("packs/swift-iq3_xxs/experts.bin", 2000)
	write("mtp/rt/x", 500)
	write("models/IQ2_XS/b.gguf", 7000) // un autre choix : ne compte pas pour swift IQ3_XXS
	if got := moeHaveGB("swift", "IQ3_XXS") * 1e9; got != 5500 {
		t.Errorf("swift IQ3_XXS : %v octets, attendu 5500", got)
	}
	if got := moeHaveGB("swift", "IQ2_XS") * 1e9; got != 500 {
		t.Errorf("swift IQ2_XS : %v octets, attendu 500 (seulement la tête MTP commune)", got)
	}
}

func TestMoeApplySettings(t *testing.T) {
	in := "# NAME=x\nENGINE=moe\nCTX=131072\nMOE_KV=fp16\nMOE_SPEC=3\nMOE_HELPER_GPU=0\n"
	out, err := moeApplySettings(in, moeSettingsReq{Ctx: 65536, KV: "int8", Spec: 4, Vision: false, Helper: false})
	if err != nil {
		t.Fatal(err)
	}
	cfg := parseEnv(out)
	if cfg["CTX"] != "65536" || cfg["MOE_KV"] != "int8" || cfg["MOE_SPEC"] != "4" || cfg["MOE_VISION"] != "0" ||
		cfg["MOE_HELPER"] != "0" || cfg["MOE_HELPER_GPU"] != "0" || cfg["ENGINE"] != "moe" {
		t.Errorf("%q", out)
	}
	if strings.Count(out, "CTX=") != 1 {
		t.Errorf("CTX dupliqué : %q", out)
	}
	if moeHelperOn(cfg) || moeCudaDevices(map[string]string{"MOE_MAIN_GPU": "1", "MOE_HELPER_GPU": "0", "MOE_HELPER": "0"}) != "1" {
		t.Error("carte d'aide coupée mais encore utilisée")
	}
	base := map[string]any{"args": []any{"--vision", "--pack", "/p"}, "vision": map[string]any{"exe": "v"}}
	b, _ := moeBuildConfig(base, cfg, "")
	if hasArg(moeArgs(t, b), "--vision") || b["vision"] != nil || hasArg(moeArgs(t, b), "--expert-cache-device1") {
		t.Errorf("vision / aide coupées mais présentes : %v", b)
	}
	for _, bad := range []moeSettingsReq{{Ctx: 1000, KV: "fp16", Spec: 3}, {Ctx: 32768, KV: "q2", Spec: 3}, {Ctx: 32768, KV: "fp16", Spec: 9}} {
		if _, err := moeApplySettings(in, bad); err == nil {
			t.Errorf("accepté : %+v", bad)
		}
	}
}

func TestMoeSetEffort(t *testing.T) {
	base := "# NAME=x\nENGINE=moe\nCTX=131072\n"
	got := moeSetEffort(base, "low")
	if parseEnv(got)["REASONING_EFFORT"] != "low" || parseEnv(got)["CTX"] != "131072" {
		t.Fatalf("effort non inscrit : %q", got)
	}
	got = moeSetEffort(got, "medium")
	if strings.Count(got, "REASONING_EFFORT=") != 1 || parseEnv(got)["REASONING_EFFORT"] != "medium" {
		t.Fatalf("effort en double ou non remplacé : %q", got)
	}
	if got = moeSetEffort(got, ""); got != base {
		t.Fatalf("défaut : la clé doit disparaître, reçu %q", got)
	}
}

func TestMoeNeutralVersion(t *testing.T) {
	got := moeNeutral("serve: engine 0.1.44.1 ready")
	if !strings.Contains(got, "AJEAN MoE "+moeVersion) || strings.Contains(got, "0.1.44") {
		t.Errorf("%q", got)
	}
}

func TestMoeBuildConfigHelperPrompt(t *testing.T) {
	base := map[string]any{"args": []any{"--pack", "/p"}}
	out, _ := moeBuildConfig(base, map[string]string{"MOE_HELPER_GPU": "0"}, "")
	if argAfter(moeArgs(t, out), "--short-read") != "48" || out["env"].(map[string]any)["STRATA_PREFILL_HELPER_SOURCE"] != "1" {
		t.Errorf("carte d'aide : lecture courte et source d'aide attendues : %v %v", moeArgs(t, out), out["env"])
	}
	// sans carte d'aide : réglages d'avant
	out, _ = moeBuildConfig(base, map[string]string{}, "")
	if argAfter(moeArgs(t, out), "--short-read") != "150" || out["env"].(map[string]any)["STRATA_PREFILL_HELPER_SOURCE"] != nil {
		t.Errorf("une carte : %v %v", moeArgs(t, out), out["env"])
	}
}

func TestMoeBuildConfigDropReserveAndCtx(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AJEAN_HOME", home)
	pack := filepath.Join(home, "pack")
	_ = os.MkdirAll(pack, 0o755)
	_ = os.WriteFile(filepath.Join(pack, "experts.bin"), []byte("x"), 0o644)
	_ = os.MkdirAll(filepath.Join(moeSrcDir(), "engine-cuda12"), 0o755)
	_ = os.WriteFile(moeEngineMarker(), []byte(moeEngineAsset+"\n"), 0o644)
	base := map[string]any{"args": []any{"--pack", pack}, "env": map[string]any{}}
	cfg := map[string]string{"MOE_HELPER_GPU": "0", "MOE_DROP": "1", "CTX": "167936"}
	out, _ := moeBuildConfig(base, cfg, "")
	if out["env"].(map[string]any)["STRATA_REMOTE_DROP_RESERVE_MIB"] != "640" {
		t.Errorf("réserve de la carte d'aide : %v", out["env"])
	}
	if got := argAfter(moeArgs(t, out), "--max-context"); got != "131072" {
		t.Errorf("contexte hors RAM non plafonné : %s", got)
	}
	// hors du mode hors RAM : le contexte demandé
	cfg["MOE_DROP"], cfg["MOE_MMAP"] = "0", "1"
	out, _ = moeBuildConfig(base, cfg, "")
	if got := argAfter(moeArgs(t, out), "--max-context"); got != "167936" {
		t.Errorf("contexte plafonné sans mode hors RAM : %s", got)
	}
}

func TestMoeMigrate(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AJEAN_HOME", home)
	oldDir := filepath.Join(moeHome(), "ajean-moe-1.0")
	newDir := moeSrcDir()
	for _, d := range []string{filepath.Join(oldDir, ".venv", "bin"), filepath.Join(newDir, "engine-cuda12"), presetsDir()} {
		_ = os.MkdirAll(d, 0o755)
	}
	_ = os.WriteFile(moeVenvPython(oldDir), []byte("#!/bin/sh\n"), 0o755)
	for _, d := range []string{oldDir, newDir} {
		_ = os.WriteFile(filepath.Join(d, "requirements.txt"), []byte("numpy\n"), 0o644)
	}
	_ = os.WriteFile(filepath.Join(newDir, "setup.py"), []byte(""), 0o644)
	_ = os.WriteFile(moeEngineMarker(), []byte(moeEngineAsset+"\n"), 0o644)
	old := filepath.Join(oldDir, "eng-swift-iq3_xxs.json")
	_ = os.WriteFile(old, []byte(`{"exe": "`+filepath.Join(oldDir, "engine-cuda12", "e")+`", "args": []}`), 0o644)
	preset := filepath.Join(presetsDir(), "P.env")
	_ = os.WriteFile(preset, []byte("ENGINE=moe\nMOE_CONFIG="+old+"\nCTX=1\n"), 0o644)
	cfg := map[string]string{"ENGINE": "moe", "MOE_CONFIG": old}
	if err := moeMigrate(cfg); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(newDir, "eng-swift-iq3_xxs.json")
	if cfg["MOE_CONFIG"] != want || ReadConfig()["MOE_CONFIG"] != want {
		t.Fatalf("config vivante : %q", cfg["MOE_CONFIG"])
	}
	b, _ := os.ReadFile(want)
	if strings.Contains(string(b), oldDir) || !strings.Contains(string(b), filepath.Join(newDir, "engine-cuda12")) {
		t.Errorf("chemins non réécrits : %s", b)
	}
	if p, _ := os.ReadFile(preset); !strings.Contains(string(p), "MOE_CONFIG="+want+"\n") {
		t.Errorf("preset non réécrit : %s", p)
	}
	if !isFile(moeVenvPython(newDir)) {
		t.Error("environnement Python non repris")
	}
	// déjà à jour : rien à faire
	if err := moeMigrate(cfg); err != nil || cfg["MOE_CONFIG"] != want {
		t.Errorf("seconde passe : %v %q", err, cfg["MOE_CONFIG"])
	}
}
