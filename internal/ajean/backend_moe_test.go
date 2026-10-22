package ajean

import (
	"os"
	"path/filepath"
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
	_ = os.MkdirAll(filepath.Join(moeSrcDir(), "engine-cuda12"), 0o755)
	_ = os.WriteFile(moeEngineMarker(), []byte(moeEngineAsset+"\n"), 0o644)
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

func TestMoeAMDCards(t *testing.T) {
	// une RX 9070 XT (16 Go) et un iGPU 780M (mémoire partagée, « total » = part de RAM)
	list := []map[string]any{
		{"name": "AMD Radeon RX 9070 XT", "total": 16384, "used": 0},
		{"name": "AMD Radeon 780M", "total": 4096, "used": 0},
	}
	archs := map[int]string{0: "gfx1201", 1: "gfx1103"}
	cards := moeAMDCards(list, archs)
	if len(cards) != 1 {
		t.Fatalf("%d carte(s), attendu 1 (l'iGPU doit être exclu) : %+v", len(cards), cards)
	}
	if cards[0].Name != "AMD Radeon RX 9070 XT" || cards[0].VRAMGB != 16 || cards[0].Arch != 0 {
		t.Errorf("%+v", cards[0])
	}
	if cards[0].AMDArch != "gfx1201" {
		t.Errorf("arch : %q", cards[0].AMDArch)
	}
	// rien de lisible : aucune carte
	if got := moeAMDCards(nil, archs); len(got) != 0 {
		t.Errorf("nil : %+v", got)
	}
	// nom manquant : un nom générique, sans perdre la carte
	if got := moeAMDCards([]map[string]any{{"total": 12288}}, nil); len(got) != 1 || got[0].Name != "AMD GPU 0" {
		t.Errorf("%+v", got)
	}
}

func TestMoeSmiArchs(t *testing.T) {
	// la clé réelle de `amd-smi static --json` est asic.target_graphics_version,
	// une chaîne déjà formatée (« gfx1201 »), pas un triple de nombres.
	static := []map[string]any{
		{"gpu": 0, "asic": map[string]any{"target_graphics_version": "gfx1201", "market_name": "AMD Radeon RX 9070 XT"}},
		{"gpu": 1, "asic": map[string]any{"target_graphics_version": "N/A"}},
		{"gpu": 2},
	}
	archs := amdSmiArchsOf(static)
	if archs[0] != "gfx1201" {
		t.Errorf("carte 0 : %q", archs[0])
	}
	// « N/A » et un bloc asic absent : pas d'arch, et la carte n'est pas
	// inventée à partir de zéros (gfx000 ferait passer une carte pour gfx1100)
	if _, ok := archs[1]; ok {
		t.Errorf("N/A doit donner une arch vide : %q", archs[1])
	}
	if _, ok := archs[2]; ok {
		t.Errorf("bloc asic absent doit donner une arch vide : %q", archs[2])
	}
	if len(archs) != 1 {
		t.Errorf("%d archs, attendu 1 : %+v", len(archs), archs)
	}
}

func TestMoeAMDSupported(t *testing.T) {
	// une MI300X (gfx942, 192 Go de HBM) passe tous les seuils de VRAM et ne
	// se compile pas ; un iGPU 890M (gfx1150) non plus
	list := []moeGPU{
		{Index: 0, Name: "AMD Radeon RX 9070 XT", VRAMGB: 16, AMDArch: "gfx1201"},
		{Index: 1, Name: "AMD Instinct MI300X", VRAMGB: 192, AMDArch: "gfx942"},
		{Index: 2, Name: "AMD Radeon 890M", VRAMGB: 22, AMDArch: "gfx1150"},
	}
	ok := moeAMDSupported(list)
	if len(ok) != 1 || ok[0].AMDArch != "gfx1201" {
		t.Errorf("%+v", ok)
	}
	// arch absente (repli muet) : on ne propose pas ce qu'on ne peut pas nommer
	if got := moeAMDSupported([]moeGPU{{Index: 0, VRAMGB: 16}}); len(got) != 0 {
		t.Errorf("%+v", got)
	}
	// Strix Halo (gfx1151, APU) est supporté, et la carte dédiée est choisie
	// AVANT lui (sa GDDR est plus rapide que la LPDDR5X partagée)
	cards := []moeGPU{
		{Index: 0, Name: "Strix Halo", VRAMGB: 58, AMDArch: "gfx1151", UMA: true, DedicatedGB: 2},
		{Index: 1, Name: "RX 7900 XTX", VRAMGB: 24, AMDArch: "gfx1100"},
	}
	ok = moeAMDSupported(cards)
	if len(ok) != 2 || ok[0].AMDArch != "gfx1100" || ok[1].AMDArch != "gfx1151" {
		t.Errorf("ordre : %+v", ok)
	}
	// à égalité de type : la plus grosse mémoire d'abord
	cards = []moeGPU{
		{Index: 0, VRAMGB: 16, AMDArch: "gfx1201"},
		{Index: 1, VRAMGB: 24, AMDArch: "gfx1100"},
	}
	ok = moeAMDSupported(cards)
	if ok[0].VRAMGB != 24 {
		t.Errorf("ordre par mémoire : %+v", ok)
	}
}

func TestMoeCardExtraGB(t *testing.T) {
	// une carte dédiée : toute sa VRAM s'ajoute à la RAM
	if got := moeCardExtraGB(moeGPU{VRAMGB: 16}); got != 16 {
		t.Errorf("%v", got)
	}
	// un APU : seul son carve-out s'ajoute (la mémoire partagée EST la RAM)
	apu := moeGPU{VRAMGB: 58, UMA: true, DedicatedGB: 2}
	if got := moeCardExtraGB(apu); got != 2 {
		t.Errorf("APU : %v (la mémoire partagée est comptée deux fois)", got)
	}
}

func TestMoeApplyUMA(t *testing.T) {
	// Strix Halo : carve-out de 2 Go, pool GTT de 30 Go, 64 Go de RAM
	vram, dedicated, shared := moeApplyUMA(2, 30, 64)
	if dedicated != 2 {
		t.Errorf("carve-out : %v", dedicated)
	}
	if shared != 30 {
		t.Errorf("partagée : %v", shared)
	}
	if vram != 32 {
		t.Errorf("GPU : %v", vram)
	}
	// pool GTT absent : la moitié de la RAM (32 Go), qui ne dépasse pas ce qui
	// reste après les 6 Go laissés à l'OS (58 Go) : elle est gardée telle quelle
	_, _, shared = moeApplyUMA(2, 0, 64)
	if shared != 32 {
		t.Errorf("pool absent : %v (la moitié de 64 Go)", shared)
	}
	// le pool annoncé dépasse ce qui reste à l'OS : on s'arrête à la RAM - 6 Go
	_, _, shared = moeApplyUMA(2, 100, 32)
	if shared != 26 {
		t.Errorf("pool trop gros : %v", shared)
	}
	// RAM trop petite pour laisser 6 Go à l'OS : rien de partagé
	_, _, shared = moeApplyUMA(2, 100, 5)
	if shared != 0 {
		t.Errorf("RAM 5 Go : %v", shared)
	}
}

func TestMoeAMDArchs(t *testing.T) {
	// la liste amont (AMD_ARCHS de setup.py)
	for _, a := range []string{"gfx1100", "gfx1101", "gfx1102", "gfx1200", "gfx1201", "gfx1030", "gfx1031", "gfx1151"} {
		if !moeAMDArchs[a] {
			t.Errorf("%s doit être supportée", a)
		}
	}
	// les archs hors liste : un gate de VRAM seul les laisse passer
	for _, a := range []string{"gfx942", "gfx908", "gfx1012", "gfx1150", "gfx1152", "gfx1103", ""} {
		if moeAMDArchs[a] {
			t.Errorf("%s ne doit pas être proposée", a)
		}
	}
	if !moeStrixHalo("gfx1151") {
		t.Error("gfx1151 est Strix Halo")
	}
	// gfx_arch_is de setup.py : jamais un préfixe
	for _, a := range []string{"gfx1150", "gfx1152", "gfx115"} {
		if moeStrixHalo(a) {
			t.Errorf("%q n'est pas gfx1151", a)
		}
	}
}

func TestMoeKFDStrixHalo(t *testing.T) {
	// un Strix Halo (gfx1151, 8060S = 80 SIMD) avec un carve-out BIOS de 2 Go
	// et un pool GTT de 30 Go : le nom vient du nombre de CU, la mémoire du GPU
	// est la somme carve-out + partagée, et la part ajoutée à la RAM est le
	// carve-out seul. gfx_target_version 110501 = major 11, minor 5, step 1.
	root := t.TempDir()
	kfd := filepath.Join(root, "class/kfd/kfd/topology/nodes")
	mustWrite(t, filepath.Join(kfd, "1", "properties"), "simd_count 80\ngfx_target_version 110501\ndrm_render_minor 129\n")
	dev := filepath.Join(root, "class/drm/renderD129/device")
	mustWrite(t, filepath.Join(dev, "mem_info_vram_total"), "2147483648\n")
	mustWrite(t, filepath.Join(dev, "mem_info_gtt_total"), "32212254720\n")
	cards := moeKFDGPUs(root)
	if len(cards) != 1 {
		t.Fatalf("%d carte(s) : %+v", len(cards), cards)
	}
	c := cards[0]
	if c.AMDArch != "gfx1151" {
		t.Errorf("arch : %q", c.AMDArch)
	}
	if !c.UMA {
		t.Error("l'APU doit être marqué UMA")
	}
	if c.DedicatedGB < 1.9 || c.DedicatedGB > 2.1 {
		t.Errorf("carve-out : %v", c.DedicatedGB)
	}
	// la mémoire du GPU est le carve-out plus la part partagée (le pool GTT de
	// 30 Go, moins ce qui reste à l'OS sur la machine qui fait le test)
	wantShared := min(30.0, totalRAMGB()-moeUMAOSLeftGB)
	if wantShared > 0 && c.VRAMGB < c.DedicatedGB+wantShared-0.2 {
		t.Errorf("mémoire GPU : %v (le pool partagé n'est pas ajouté), attendu ~%v", c.VRAMGB, c.DedicatedGB+wantShared)
	}
	if got := moeCardExtraGB(c); got < 1.9 || got > 2.1 {
		t.Errorf("ajout à la RAM : %v (la mémoire partagée est comptée deux fois)", got)
	}
	if !moeAMDArchs[c.AMDArch] {
		t.Errorf("gfx1151 doit être proposée : %+v", c)
	}
	// sans product_name : le nom vient du nombre de CU
	if c.Name != "AMD Radeon 8060S (Ryzen AI Max, Strix Halo, gfx1151)" {
		t.Errorf("nom : %q", c.Name)
	}
}

func TestMoeEngineFor(t *testing.T) {
	if got := moeEngineFor(moeVendorNVIDIA); got != moeEngineAsset {
		t.Errorf("nvidia : %q", got)
	}
	if got := moeEngineFor(moeVendorAMD); got != "compiled" {
		t.Errorf("amd : %q", got)
	}
}

func TestMoeEngineVendor(t *testing.T) {
	if got := moeEngineVendor(map[string]any{"backend": "hip"}); got != moeVendorAMD {
		t.Errorf("hip : %q", got)
	}
	if got := moeEngineVendor(map[string]any{"backend": "cuda"}); got != moeVendorNVIDIA {
		t.Errorf("cuda : %q", got)
	}
	// clé absente (configs d'origine) : NVIDIA
	if got := moeEngineVendor(map[string]any{}); got != moeVendorNVIDIA {
		t.Errorf("absente : %q", got)
	}
}

func TestMoeKFDGPUs(t *testing.T) {
	// arbre factice : un nœud CPU (0) puis un nœud GPU (1) — 9070 XT, 16 Go,
	// comme la topologie KFD d'une machine amdgpu sans amd-smi ni rocm-smi
	root := t.TempDir()
	kfd := filepath.Join(root, "class/kfd/kfd/topology/nodes")
	mustWrite(t, filepath.Join(kfd, "0", "properties"), "simd_count 0\ngfx_target_version 0\ndrm_render_minor 128\n")
	mustWrite(t, filepath.Join(kfd, "1", "properties"), "simd_count 128\ngfx_target_version 120001\ndrm_render_minor 129\n")
	dev := filepath.Join(root, "class/drm/renderD129/device")
	mustWrite(t, filepath.Join(dev, "mem_info_vram_total"), "17095983104\n")
	mustWrite(t, filepath.Join(dev, "product_name"), "Radeon Graphics\n")

	cards := moeKFDGPUs(root)
	if len(cards) != 1 {
		t.Fatalf("%d carte(s), attendu 1 (le nœud CPU doit être sauté) : %+v", len(cards), cards)
	}
	c := cards[0]
	if c.Index != 0 || c.Name != "Radeon Graphics" || c.VRAMGB < 15.9 || c.VRAMGB > 16.1 {
		t.Errorf("%+v", c)
	}

	// sans product_name : le nom vient de la table par arch (gfx1201)
	root2 := t.TempDir()
	kfd2 := filepath.Join(root2, "class/kfd/kfd/topology/nodes")
	mustWrite(t, filepath.Join(kfd2, "1", "properties"), "simd_count 64\ngfx_target_version 120001\ndrm_render_minor 129\n")
	dev2 := filepath.Join(root2, "class/drm/renderD129/device")
	mustWrite(t, filepath.Join(dev2, "mem_info_vram_total"), "17095983104\n")
	cards = moeKFDGPUs(root2)
	if len(cards) != 1 || cards[0].Name != "AMD Radeon RX 9070 series / AI PRO R9700 (gfx1201)" {
		t.Errorf("%+v", cards)
	}

	// topologie absente : rien
	if got := moeKFDGPUs(t.TempDir()); got != nil {
		t.Errorf("%+v", got)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// Le moteur AMD meurt à la charge des bibliothèques quand la config porte les
// chemins ROCm de l'installation d'origine (l'environnement Python a été déplacé) :
// le serveur du moteur écarte les dossiers morts et lance sans LD_LIBRARY_PATH.
func TestMoeRepairLibDirs(t *testing.T) {
	// le home doit être isolé : moeHome() tombe sinon sur le home par défaut de
	// la machine, /etc/ajean sur Linux (AJEAN_HOME est lu par AjeanHome(), et
	// /etc/default/ajean le fixe). Un test qui écrit sous moeHome() sans isoler
	// échoue sur le runner, non-root : « mkdir /etc/ajean: permission denied ».
	home := t.TempDir()
	t.Setenv("AJEAN_HOME", home)
	srcDir := filepath.Join(moeHome(), "ajean-moe-1.1")
	sp := filepath.Join(srcDir, ".venv", "lib", "python3.14", "site-packages")
	for _, d := range []string{"_rocm_sdk_devel/lib", "_rocm_sdk_libraries_gfx120X_all/lib"} {
		if err := os.MkdirAll(filepath.Join(sp, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	dead := filepath.Join(home, "moe-test-home", "moe", "ajean-moe-1.1",
		".venv", "lib", "python3.14", "site-packages", "_rocm_sdk_devel", "lib")
	final := map[string]any{"lib_dirs": []any{dead}}
	if !moeRepairLibDirs(final, srcDir) {
		t.Fatal("lib_dirs non redressée")
	}
	got := final["lib_dirs"].([]any)
	want := []string{filepath.Join(sp, "_rocm_sdk_devel", "lib"),
		filepath.Join(sp, "_rocm_sdk_libraries_gfx120X_all", "lib")}
	if len(got) != len(want) {
		t.Fatalf("lib_dirs : %v", got)
	}
	for i, d := range want {
		if got[i] != d {
			t.Errorf("lib_dirs %d : %v (attendu %s)", i, got[i], d)
		}
	}
	// déjà bonne : seconde passe ne change rien
	if moeRepairLibDirs(final, srcDir) {
		t.Errorf("seconde passe modifie la config : %v", final["lib_dirs"])
	}
	// aucun environnement Python : on garde la liste telle quelle
	if moeRepairLibDirs(final, filepath.Join(home, "moe", "ajean-moe-9.9")) {
		t.Error("sans venv, la config est modifiée")
	}
}
