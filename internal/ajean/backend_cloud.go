package ajean

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// backend_cloud.go : presets « GPU cloud ». Un tel preset est un preset NORMAL
// (contexte, cache KV, spéculatif, EXTRA_ARGS… réglés dans l'éditeur habituel),
// mais au lieu de lancer llama-server sur cette machine, AJEAN le déploie sur un
// GPU loué chez Modal (modal.com) et route le chat vers l'endpoint obtenu.
//
//   CLOUD=modal        marqueur
//   CLOUD_GPU=A10G     type de GPU Modal (T4, L4, A10G, L40S, A100-80GB, H100…)
//   CLOUD_MODEL=<url>  lien Hugging Face direct vers le .gguf
//
// Le déploiement passe par la CLI `modal` (pip install modal + modal setup sur
// la machine qui fait tourner AJEAN). Le modèle est téléchargé une fois dans un
// volume Modal partagé, au premier démarrage du conteneur. Le GPU s'éteint seul
// après CLOUD_IDLE secondes sans requête : on ne paie que l'usage.

const (
	cloudKeyFlag  = "CLOUD"
	cloudKeyGPU   = "CLOUD_GPU"
	cloudKeyModel = "CLOUD_MODEL"
	cloudKeyIdle  = "CLOUD_IDLE"
	cloudKeyProf  = "CLOUD_PROFILE" // profil Modal (compte) ; vide = profil actif de la CLI
	cloudVolume   = "ajean-models"
)

func isCloudConfig(cfg map[string]string) bool {
	return strings.EqualFold(strings.TrimSpace(cfg[cloudKeyFlag]), "modal")
}

// usesRemoteEndpoint : pas de llama-server local pour cette config (API externe
// ou GPU cloud). C'est ce que les chemins « moteur local » doivent tester.
func usesRemoteEndpoint(cfg map[string]string) bool {
	return isExternalConfig(cfg) || isCloudConfig(cfg)
}

// État du déploiement, partagé avec /api/status.
var cloudState struct {
	sync.Mutex
	deploying bool
	err       string
	fp        string // empreinte de la config du dernier déploiement lancé
	gen       int    // incrémenté à chaque déploiement : un ancien n'écrase pas le suivant
}

// cloudStatus : état du déploiement de la config cloud ACTIVE. Une erreur
// laissée par une autre version de la config (preset modifié depuis) ne compte pas.
func cloudStatus() (deploying bool, errMsg string) {
	fp := cloudFingerprint(ReadConfig())
	cloudState.Lock()
	defer cloudState.Unlock()
	if cloudState.fp != fp {
		return cloudState.deploying, ""
	}
	return cloudState.deploying, cloudState.err
}

// cloudReady : le preset cloud actif a un endpoint déployé et à jour.
func cloudReady(cfg map[string]string) bool {
	dep, e := cloudStatus()
	return !dep && e == "" && getStr(bkState, "cloud_url") != "" &&
		getStr(bkState, "cloud_fp") == cloudFingerprint(cfg)
}

// cloudFingerprint : ce qui, s'il change, exige un redéploiement.
func cloudFingerprint(cfg map[string]string) string {
	args, _ := cloudServerArgs(cfg, "")
	return configFingerprint(map[string]string{
		"a": strings.Join(args, "\x00"),
		"g": cloudGPU(cfg),
		"m": cfg[cloudKeyModel],
		"i": cloudIdle(cfg),
		"p": cloudProfile(cfg),
	})
}

func cloudGPU(cfg map[string]string) string {
	if g := strings.TrimSpace(cfg[cloudKeyGPU]); g != "" {
		return g
	}
	return "A10G"
}

func cloudProfile(cfg map[string]string) string { return strings.TrimSpace(cfg[cloudKeyProf]) }

// modalCmdFor : CLI Modal sur un profil donné (vide = profil actif), sortie UTF-8.
func modalCmdFor(profile string, args ...string) (*exec.Cmd, error) {
	cmd, err := modalCommand(args...)
	if err != nil {
		return nil, err
	}
	cmd.Env = append(os.Environ(), "PYTHONIOENCODING=utf-8", "PYTHONUTF8=1")
	if profile != "" {
		cmd.Env = append(cmd.Env, "MODAL_PROFILE="+profile)
	}
	return cmd, nil
}

func cloudIdle(cfg map[string]string) string {
	if v := strings.TrimSpace(cfg[cloudKeyIdle]); v != "" {
		return v
	}
	return "300"
}

// cloudModelFile : nom du .gguf dans le volume, tiré du lien.
func cloudModelFile(link string) string {
	link = strings.TrimSpace(link)
	if i := strings.IndexAny(link, "?#"); i >= 0 {
		link = link[:i]
	}
	return path.Base(link)
}

// cloudModelURL : accepte un lien /blob/ (page HF) et le convertit en /resolve/.
func cloudModelURL(link string) string {
	return strings.Replace(strings.TrimSpace(link), "/blob/", "/resolve/", 1)
}

// cloudAPIKey : clé qui protège l'endpoint public. Générée une fois par machine.
func cloudAPIKey() string {
	if k := getStr(bkState, "cloud_key"); k != "" {
		return k
	}
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	k := hex.EncodeToString(b)
	_ = putStr(bkState, "cloud_key", k)
	return k
}

// cloudServerArgs traduit le preset en arguments llama-server pour le conteneur
// (image officielle ggml-org, donc llama.cpp récent). Même logique que
// cmdServe, moins ce qui n'a de sens que sur la machine locale.
func cloudServerArgs(cfg map[string]string, key string) ([]string, error) {
	file := cloudModelFile(cfg[cloudKeyModel])
	if !strings.HasSuffix(strings.ToLower(file), ".gguf") {
		return nil, fmt.Errorf("lien du modèle invalide : il faut un lien direct vers un .gguf")
	}
	get := func(k, def string) string {
		if v := strings.TrimSpace(cfg[k]); v != "" {
			return v
		}
		return def
	}
	args := []string{
		"-m", "/models/" + file,
		"-ngl", get("NGL", "999"),
		"-c", get("CTX", "32768"),
		"-b", get("BATCH", "2048"),
		"-ub", get("UBATCH", "512"),
		"-np", get("NP", "1"),
		"--host", "0.0.0.0", "--port", "8080",
	}
	kv := get("KV_TYPE", "")
	if k := get("KV_TYPE_K", kv); k != "" {
		args = append(args, "-ctk", k)
	}
	if v := get("KV_TYPE_V", kv); v != "" {
		args = append(args, "-ctv", v)
	}
	if r := strings.TrimSpace(cfg["REASONING"]); r != "" {
		if reasoningActive(r) {
			args = append(args, "--reasoning", r, "--reasoning-budget", get("REASONING_BUDGET", "-1"))
		} else {
			args = append(args, "--reasoning", "off")
		}
	}
	if key != "" {
		args = append(args, "--api-key", key)
	}
	// EXTRA_ARGS, moins le choix de cartes locales (un seul GPU là-bas) et les
	// chemins de fichiers locaux, qui n'existent pas dans le conteneur.
	extra := splitArgs(cfg["EXTRA_ARGS"])
	drop := map[string]bool{"--device": true, "-dev": true, "--tensor-split": true, "-ts": true,
		"--main-gpu": true, "-mg": true, "--mmproj": true, "--model-draft": true, "-md": true,
		"--chat-template-file": true, "--threads": true, "-t": true, "-tb": true, "--threads-batch": true}
	for i := 0; i < len(extra); i++ {
		if drop[extra[i]] {
			i++ // saute aussi la valeur
			continue
		}
		args = append(args, extra[i])
	}
	return translateLoadMode(args, true), nil
}

const cloudScript = `# Généré par AJEAN : ne pas éditer, réécrit à chaque déploiement.
import json, os, subprocess, urllib.request
import modal

CFG = json.loads(%q)

image = modal.Image.from_registry("ghcr.io/ggml-org/llama.cpp:server-cuda", add_python="3.11").entrypoint([])
models = modal.Volume.from_name(%q, create_if_missing=True)
app = modal.App(CFG["app"], image=image)


def ensure_model():
    dst = "/models/" + CFG["file"]
    if os.path.exists(dst):
        return
    tmp = dst + ".part"
    print("téléchargement du modèle", CFG["url"], flush=True)
    with urllib.request.urlopen(CFG["url"]) as r, open(tmp, "wb") as f:
        while True:
            b = r.read(16 << 20)
            if not b:
                break
            f.write(b)
    os.replace(tmp, dst)
    models.commit()


@app.function(gpu=CFG["gpu"], memory=CFG["memory"], volumes={"/models": models},
              scaledown_window=CFG["idle"], timeout=24 * 3600)
@modal.concurrent(max_inputs=8)
@modal.web_server(port=8080, startup_timeout=1800)
def serve():
    ensure_model()
    subprocess.Popen(["/app/llama-server"] + CFG["args"])
`

var cloudURLRe = regexp.MustCompile(`https://[A-Za-z0-9.-]+\.modal\.run`)

// modalCommand trouve le client Modal : le runtime privé d'AJEAN s'il est
// installé, sinon une CLI déjà présente sur la machine. Sans l'un ni l'autre,
// l'installation du runtime privé démarre et l'appelant reçoit errCloudRuntime.
func modalCommand(args ...string) (*exec.Cmd, error) {
	if cloudRuntimeReady() {
		return hideCmd(exec.Command(cloudRuntimePython(), append([]string{"-m", "modal"}, args...)...)), nil
	}
	if p, err := exec.LookPath("modal"); err == nil {
		return hideCmd(exec.Command(p, args...)), nil
	}
	startCloudRuntimeInstall()
	return nil, errCloudRuntime
}

var errCloudRuntime = fmt.Errorf("installation du composant GPU cloud en cours")

// waitCloudRuntime attend la fin de l'installation du runtime privé.
func waitCloudRuntime() error {
	for i := 0; i < 900; i++ {
		st, _, e := cloudRuntimeStatus()
		switch st {
		case "ready":
			return nil
		case "error":
			return fmt.Errorf("%s", e)
		}
		time.Sleep(time.Second)
	}
	return fmt.Errorf("installation du composant GPU cloud trop longue")
}

// cloudDeploy (re)déploie le preset cloud actif si nécessaire. Asynchrone :
// l'UI suit l'avancement via /api/status (health=false + load_error).
//
// Idempotent : appelé à chaque sondage de /api/status, il ne relance rien tant
// qu'un déploiement de la MÊME config tourne, ni après un échec de cette même
// config (sinon on bouclerait sur l'erreur). force=true (bascule explicite sur
// le preset) retente malgré un échec.
func cloudDeploy() { cloudDeployOpt(false) }

func cloudDeployOpt(force bool) {
	cfg := ReadConfig()
	if !isCloudConfig(cfg) || cloudReady(cfg) {
		return
	}
	fp := cloudFingerprint(cfg)
	cloudState.Lock()
	if cloudState.fp == fp && (cloudState.deploying || (cloudState.err != "" && !force)) {
		cloudState.Unlock()
		return
	}
	cloudState.gen++
	gen := cloudState.gen
	cloudState.deploying, cloudState.err, cloudState.fp = true, "", fp
	cloudState.Unlock()
	go func() {
		url, err := runCloudDeploy(cfg)
		cloudState.Lock()
		defer cloudState.Unlock()
		if gen != cloudState.gen {
			return
		}
		cloudState.deploying = false
		if err != nil {
			cloudState.err = err.Error()
			fmt.Printf("%s déploiement Modal : %v\n", red("[ERREUR]"), err)
			return
		}
		_ = putStr(bkState, "cloud_url", url)
		_ = putStr(bkState, "cloud_fp", cloudFingerprint(cfg))
		fmt.Printf("%s GPU cloud prêt : %s\n", green("[ok]"), url)
	}()
}

func runCloudDeploy(cfg map[string]string) (string, error) {
	args, err := cloudServerArgs(cfg, cloudAPIKey())
	if err != nil {
		return "", err
	}
	app := cloudAppName()
	idle := 300
	fmt.Sscan(cloudIdle(cfg), &idle)
	spec, _ := json.Marshal(map[string]any{
		"app": app, "gpu": cloudGPU(cfg), "idle": idle, "memory": 32768,
		"url": cloudModelURL(cfg[cloudKeyModel]), "file": cloudModelFile(cfg[cloudKeyModel]),
		"args": args,
	})
	dir := filepath.Join(AjeanHome(), "cloud")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	script := filepath.Join(dir, app+".py")
	// %q de Go produit un littéral valide aussi en Python pour ce contenu (JSON ASCII).
	if err := os.WriteFile(script, []byte(fmt.Sprintf(cloudScript, string(spec), cloudVolume)), 0o600); err != nil {
		return "", err
	}
	cmd, err := modalCmdFor(cloudProfile(cfg), "deploy", script)
	if err == errCloudRuntime {
		if err = waitCloudRuntime(); err == nil {
			cmd, err = modalCmdFor(cloudProfile(cfg), "deploy", script)
		}
	}
	if err != nil {
		return "", err
	}
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("déploiement Modal impossible : %s", modalErrorText(string(out)))
	}
	url := cloudURLRe.FindString(string(out))
	if url == "" {
		return "", fmt.Errorf("modal deploy n'a renvoyé aucune URL")
	}
	return url, nil
}

// cloudAppName : nom de l'app Modal du preset actif.
func cloudAppName() string {
	app := "ajean-" + strings.ToLower(regexp.MustCompile(`[^A-Za-z0-9]+`).ReplaceAllString(activePresetID(), "-"))
	app = strings.Trim(app, "-")
	if len(app) > 50 {
		app = app[:50]
	}
	return app
}

// cloudEndpoint : endpoint de complétions du GPU cloud déployé.
func cloudEndpoint(cfg map[string]string) chatEndpoint {
	return chatEndpoint{
		URL:      completionsURL(getStr(bkState, "cloud_url")),
		Model:    "ajean",
		Key:      cloudAPIKey(),
		External: true,
		Cloud:    true,
	}
}

// doLLM envoie la requête ; pour un GPU cloud, attend le réveil du conteneur
// (llama-server répond 503 « Loading model » tant que le modèle charge).
func doLLM(ctx context.Context, req *http.Request, body []byte, ep chatEndpoint) (*http.Response, error) {
	if !ep.Cloud {
		return http.DefaultClient.Do(req)
	}
	deadline := time.Now().Add(30 * time.Minute) // 1er démarrage = téléchargement du modèle
	for {
		resp, err := http.DefaultClient.Do(req)
		if err != nil || resp.StatusCode != http.StatusServiceUnavailable || time.Now().After(deadline) {
			return resp, err
		}
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(5 * time.Second):
		}
		next := req.Clone(ctx)
		next.Body = io.NopCloser(bytes.NewReader(body))
		req = next
	}
}

// cloudVRAMMB : mémoire des GPU Modal, pour l'affichage « Appareil ».
var cloudVRAMMB = map[string]int{"T4": 16384, "L4": 24576, "A10G": 24576, "A10": 24576, "L40S": 49152,
	"A100": 40960, "A100-40GB": 40960, "A100-80GB": 81920, "H100": 81920, "H200": 144384, "B200": 196608}

// cloudGPUInfo : carte du GPU cloud actif pour /api/telemetry (nil sinon).
// La conso réelle n'est pas mesurée (le conteneur peut dormir) : used = 0 et
// l'état du déploiement dans `status`.
func cloudGPUInfo() map[string]any {
	cfg := ReadConfig()
	if !isCloudConfig(cfg) {
		return nil
	}
	g := cloudGPU(cfg)
	st := "prêt · s'éteint après " + cloudIdle(cfg) + " s sans requête"
	if dep, e := cloudStatus(); dep {
		st = "déploiement en cours…"
	} else if e != "" {
		st = "erreur de déploiement"
	} else if !cloudReady(cfg) {
		st = "pas encore déployé"
	}
	out := map[string]any{"name": "Modal " + g, "total": cloudVRAMMB[strings.ToUpper(g)], "used": 0, "cloud": true, "status": st}
	if m := cloudMetrics(); m != nil {
		out["used"], out["total"], out["util"], out["temp"] = m[0], m[1], m[2], m[3]
		out["awake"] = true
	} else if st == "prêt · s'éteint après "+cloudIdle(cfg)+" s sans requête" {
		out["status"] = "en veille · se réveille au prochain message"
	}
	if b := cloudBillingCached(cloudProfile(cfg)); b != nil {
		out["billing"] = b
	}
	return out
}

// Mesure du GPU cloud : `modal container list` dit si un conteneur tourne (sans
// le réveiller), puis `modal container exec … nvidia-smi` lit sa VRAM. ~3 s par
// mesure, donc en tâche de fond, au plus toutes les 10 s, et seulement tant que
// l'UI la demande.
var cloudMet struct {
	sync.Mutex
	at      time.Time
	running bool
	vals    []int // used, total (Mo), util (%), temp (°C) ; nil = conteneur éteint
}

func cloudMetrics() []int {
	cloudMet.Lock()
	defer cloudMet.Unlock()
	if !cloudMet.running && time.Since(cloudMet.at) > 10*time.Second {
		cloudMet.running = true
		go refreshCloudMetrics(cloudAppName(), cloudProfile(ReadConfig()))
	}
	if time.Since(cloudMet.at) > 60*time.Second {
		return nil // mesure trop vieille : on ne l'affiche plus
	}
	return cloudMet.vals
}

func refreshCloudMetrics(app, profile string) {
	var vals []int
	defer func() {
		cloudMet.Lock()
		cloudMet.vals, cloudMet.at, cloudMet.running = vals, time.Now(), false
		cloudMet.Unlock()
	}()
	cmd, err := modalCmdFor(profile, "container", "list", "--json")
	if err != nil {
		return
	}
	out, err := cmd.Output()
	if err != nil {
		return
	}
	var list []struct {
		ID  string `json:"container_id"`
		App string `json:"app_name"`
	}
	if json.Unmarshal(out, &list) != nil {
		return
	}
	id := ""
	for _, c := range list {
		if c.App == app {
			id = c.ID
		}
	}
	if id == "" {
		return
	}
	cmd, _ = modalCmdFor(profile, "container", "exec", id, "--", "nvidia-smi",
		"--query-gpu=memory.used,memory.total,utilization.gpu,temperature.gpu", "--format=csv,noheader,nounits")
	out, err = cmd.Output()
	if err != nil {
		return
	}
	f := strings.Split(strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]), ",")
	if len(f) != 4 {
		return
	}
	v := make([]int, 4)
	for i := range f {
		fmt.Sscan(strings.TrimSpace(f[i]), &v[i])
	}
	vals = v
}
