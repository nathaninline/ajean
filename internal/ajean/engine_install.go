// engine_install.go — installation du moteur épinglé : téléchargement vérifié,
// extraction dans un dossier provisoire, sonde, puis mise en place d'un seul
// geste. Voir engine.go pour le principe.
package ajean

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Un seul installateur à la fois
// ---------------------------------------------------------------------------

// Le service du moteur (ajean serve) et l'interface (ajean web) sont deux
// processus : au premier démarrage après une mise à jour, les deux constatent
// que le moteur manque. engineMu sérialise dans un processus, le fichier verrou
// entre processus. Le détenteur rafraîchit sa date toutes les 5 s ; un verrou
// muet depuis 45 s est celui d'un processus mort, on le reprend.
var engineMu sync.Mutex

const engineLockStale = 45 * time.Second

func engineLock(ctx context.Context, logf func(string)) (func(), error) {
	if err := os.MkdirAll(engineRoot(), 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(engineRoot(), ".lock")
	warned := false
	for {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
		if err == nil {
			fmt.Fprintf(f, "%d\n", os.Getpid())
			f.Close()
			stop := make(chan struct{})
			go func() {
				t := time.NewTicker(5 * time.Second)
				defer t.Stop()
				for {
					select {
					case <-stop:
						return
					case <-t.C:
						now := time.Now()
						_ = os.Chtimes(path, now, now)
					}
				}
			}()
			return func() { close(stop); _ = os.Remove(path) }, nil
		}
		if !os.IsExist(err) {
			return nil, fmt.Errorf("dossier du moteur inaccessible en écriture (%s) : %w", engineRoot(), err)
		}
		fi, serr := os.Stat(path)
		if serr != nil {
			if os.IsNotExist(serr) {
				continue // libéré entre-temps
			}
			return nil, serr
		}
		if time.Since(fi.ModTime()) > engineLockStale {
			_ = os.Remove(path)
			continue
		}
		if !warned {
			logf("une installation du moteur est déjà en cours dans un autre processus : attente…")
			warned = true
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Second):
		}
	}
}

// ---------------------------------------------------------------------------
// Annulation (bouton « arrêter » de l'interface)
// ---------------------------------------------------------------------------

var (
	engineCancelMu sync.Mutex
	engineCancelFn context.CancelFunc
)

// engineCancelInstall interrompt l'installation en cours dans CE processus.
func engineCancelInstall() bool {
	engineCancelMu.Lock()
	defer engineCancelMu.Unlock()
	if engineCancelFn == nil {
		return false
	}
	engineCancelFn()
	return true
}

// ---------------------------------------------------------------------------
// Sonde
// ---------------------------------------------------------------------------

// engineProbeTimeout : comme Ollama (30 s, 90 s sous Windows où le premier
// chargement des DLL CUDA passe par l'antivirus).
func engineProbeTimeout() time.Duration {
	if runtime.GOOS == "windows" {
		return 90 * time.Second
	}
	return 30 * time.Second
}

// engineProbe lance le moteur fraîchement extrait dans un processus jetable et
// dit s'il est utilisable. Variable : remplacée dans les tests.
//
// Une variante GPU doit ÉNUMÉRER au moins une carte (--list-devices) : un build
// CUDA qui démarre sans voir de carte (pilote trop ancien, carte non gérée)
// tournerait en CPU sans le dire, alors que Vulkan ferait mieux. Metal et CPU
// n'ont qu'à s'exécuter (--version).
var engineProbe = func(bin string, libDirs []string, variant string) error {
	arg := "--list-devices"
	needDevice := variant == "cuda" || variant == "rocm" || variant == "vulkan"
	if !needDevice {
		arg = "--version"
	}
	ctx, cancel := context.WithTimeout(context.Background(), engineProbeTimeout())
	defer cancel()
	cmd := hideCmd(exec.CommandContext(ctx, bin, arg))
	cmd.Env = libraryPathEnvWith(filepath.Dir(bin), libDirs)
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return fmt.Errorf("le moteur n'a pas répondu en %s", engineProbeTimeout())
	}
	if err != nil {
		return fmt.Errorf("le moteur ne démarre pas (%v)%s", err, probeTail(string(out)))
	}
	if needDevice && len(parseListDevices(string(out))) == 0 {
		return fmt.Errorf("le moteur ne voit aucune carte graphique%s", probeTail(string(out)))
	}
	return nil
}

// probeTail : la fin de la sortie du moteur, pour dire POURQUOI il a été écarté.
func probeTail(out string) string {
	if msg := engineErrorFromLog(out); msg != "" {
		return " : " + msg
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(out, "\r", "")), "\n")
	if len(lines) == 0 || lines[len(lines)-1] == "" {
		return ""
	}
	last := lines[len(lines)-1]
	if len(last) > 200 {
		last = last[:200] + "…"
	}
	return " : " + last
}

// ---------------------------------------------------------------------------
// Installation
// ---------------------------------------------------------------------------

func engineExeName() string {
	if runtime.GOOS == "windows" {
		return "llama-server.exe"
	}
	return "llama-server"
}

// findEngineExe localise llama-server sous dir (l'arborescence interne des
// archives officielles varie : racine, llama-bNNNN/, build/bin…).
func findEngineExe(dir string) string {
	var found []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == engineExeName() {
			found = append(found, p)
		}
		return nil
	})
	if len(found) == 0 {
		return ""
	}
	sort.Strings(found)
	return found[0]
}

// engineRuntimeLibDirs : sous-dossiers de dir (relatifs) qui contiennent des
// bibliothèques partagées. L'archive cudart est plate sous Windows et rangée
// dans un sous-dossier sous Linux.
func engineRuntimeLibDirs(root, dir string) []string {
	seen := map[string]bool{}
	var out []string
	_ = filepath.WalkDir(dir, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		n := strings.ToLower(d.Name())
		if !strings.HasSuffix(n, ".dll") && !strings.Contains(n, ".so") && !strings.HasSuffix(n, ".dylib") {
			return nil
		}
		rel, rerr := filepath.Rel(root, filepath.Dir(p))
		if rerr != nil || seen[rel] {
			return nil
		}
		seen[rel] = true
		out = append(out, filepath.ToSlash(rel))
		return nil
	})
	sort.Strings(out)
	return out
}

// engineReusableRuntime cherche, parmi les moteurs déjà installés (version
// précédente), des bibliothèques GPU IDENTIQUES à celles demandées. Les
// bibliothèques CUDA changent rarement d'une version de llama.cpp à l'autre :
// les recopier évite de retélécharger ~400 Mo à chaque mise à jour d'AJEAN.
func engineReusableRuntime(sha string) string {
	entries, err := os.ReadDir(engineRoot())
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		d := filepath.Join(engineRoot(), e.Name())
		if info := readEngineInfo(d); info != nil && info.ExtraSHA256 != "" &&
			strings.EqualFold(info.ExtraSHA256, sha) && isDir(filepath.Join(d, "runtime")) {
			return filepath.Join(d, "runtime")
		}
	}
	return ""
}

// engineInstall installe le moteur épinglé, variante par variante, jusqu'à en
// trouver une qui passe la sonde. force=true réinstalle même si le moteur est
// déjà en place (réparation). Renvoie ce qui a été installé.
func engineInstall(force bool, logf, phasef func(string)) (*engineInfo, error) {
	if logf == nil {
		logf = func(string) {}
	}
	if phasef == nil {
		phasef = func(string) {}
	}
	engineMu.Lock()
	defer engineMu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	engineCancelMu.Lock()
	engineCancelFn = cancel
	engineCancelMu.Unlock()
	defer func() {
		engineCancelMu.Lock()
		engineCancelFn = nil
		engineCancelMu.Unlock()
		cancel()
	}()

	unlock, err := engineLock(ctx, logf)
	if err != nil {
		return nil, err
	}
	defer unlock()

	// Un autre processus l'a peut-être installé pendant qu'on attendait le verrou.
	if !force {
		if e := engineInstalled(); e != nil {
			return e, nil
		}
	}
	m, err := engineAssets()
	if err != nil {
		return nil, err
	}
	forced := engineForcedVariant()
	cands, err := engineCandidates(m.Assets, engineMachine(), forced)
	if err != nil {
		return nil, err
	}
	if forced != "" {
		logf("variante imposée sur cette machine : " + forced)
	}
	dl := filepath.Join(engineRoot(), "dl")
	if err := os.MkdirAll(dl, 0o755); err != nil {
		return nil, err
	}
	var reasons []string
	for i, v := range cands {
		logf(fmt.Sprintf("llama.cpp %s, variante %s", m.Tag, v.Label))
		// Réparation : on ne recopie pas des bibliothèques peut-être abîmées.
		info, err := engineInstallVariant(ctx, m.Tag, v, dl, !force, logf, phasef)
		if err == nil {
			enginePrune(info.dir, logf)
			logf("moteur prêt : llama.cpp " + info.Tag + ", " + info.Label)
			return info, nil
		}
		if ctx.Err() != nil {
			return nil, errors.New("installation du moteur annulée")
		}
		reasons = append(reasons, v.Label+" : "+err.Error())
		if i < len(cands)-1 {
			logf("✗ " + v.Label + " écartée (" + err.Error() + ") : essai de " + cands[i+1].Label)
		}
	}
	return nil, fmt.Errorf("impossible d'installer le moteur llama.cpp %s — %s", m.Tag, strings.Join(reasons, " ; "))
}

func engineInstallVariant(ctx context.Context, tag string, v engineVariant, dl string, reuse bool, logf, phasef func(string)) (*engineInfo, error) {
	stage := filepath.Join(engineRoot(), tag+".tmp")
	if err := os.RemoveAll(stage); err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage) // sans effet une fois le dossier renommé
	if err := os.MkdirAll(stage, 0o755); err != nil {
		return nil, err
	}
	fetch := func(a engineAsset) (string, error) {
		phasef(fmt.Sprintf("téléchargement de %s (%d Mo)…", a.Name, a.Size/1_000_000))
		dest := filepath.Join(dl, a.Name)
		url := engineDownloadBase + "/" + tag + "/" + a.Name
		if err := engineFetch(ctx, url, dest, a.Size, a.SHA256, logf); err != nil {
			return "", fmt.Errorf("téléchargement de %s : %w", a.Name, err)
		}
		return dest, nil
	}
	arc, err := fetch(v.Main)
	if err != nil {
		return nil, err
	}
	phasef("extraction de " + v.Main.Name + "…")
	if err := extractArchive(arc, stage); err != nil {
		return nil, fmt.Errorf("extraction de %s : %w", v.Main.Name, err)
	}
	info := &engineInfo{Tag: tag, Variant: v.ID, Label: v.Label, Asset: v.Main.Name, InstalledAt: nowStamp(), dir: stage}
	if v.Extra != nil {
		rt := filepath.Join(stage, "runtime")
		if old := engineReusableRuntime(v.Extra.SHA256); reuse && old != "" {
			phasef("bibliothèques GPU déjà présentes : copie…")
			if err := copyTree(old, rt); err != nil {
				return nil, fmt.Errorf("copie des bibliothèques GPU : %w", err)
			}
		} else {
			xarc, err := fetch(*v.Extra)
			if err != nil {
				return nil, err
			}
			phasef("extraction de " + v.Extra.Name + "…")
			if err := extractArchive(xarc, rt); err != nil {
				return nil, fmt.Errorf("extraction de %s : %w", v.Extra.Name, err)
			}
		}
		info.ExtraSHA256 = v.Extra.SHA256
		info.LibDirs = engineRuntimeLibDirs(stage, rt)
	}
	bin := findEngineExe(stage)
	if bin == "" {
		return nil, fmt.Errorf("llama-server introuvable dans %s", v.Main.Name)
	}
	if runtime.GOOS != "windows" {
		_ = os.Chmod(bin, 0o755)
	}
	rel, err := filepath.Rel(stage, bin)
	if err != nil {
		return nil, err
	}
	info.Bin = filepath.ToSlash(rel)

	phasef("vérification du moteur (" + v.Label + ")…")
	if err := engineProbe(bin, info.libPaths(), v.ID); err != nil {
		return nil, err
	}

	b, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(stage, engineMarker), append(b, '\n'), 0o644); err != nil {
		return nil, err
	}
	final := engineDir(tag)
	if err := os.RemoveAll(final); err != nil {
		return nil, fmt.Errorf("le moteur en place ne peut pas être remplacé (encore en cours d'exécution ?) : %w", err)
	}
	if err := os.Rename(stage, final); err != nil {
		return nil, fmt.Errorf("mise en place du moteur : %w", err)
	}
	info.dir = final
	return info, nil
}

// enginePrune supprime tout ce qui n'est pas le moteur courant : versions
// précédentes, archives téléchargées, et l'ancien dossier des précompilés
// (géré par AJEAN avant la 0.20, plusieurs centaines de Mo devenus inutiles).
func enginePrune(keep string, logf func(string)) {
	entries, _ := os.ReadDir(engineRoot())
	for _, e := range entries {
		p := filepath.Join(engineRoot(), e.Name())
		if samePath(p, keep) || e.Name() == ".lock" {
			continue
		}
		if err := os.RemoveAll(p); err == nil && e.IsDir() && e.Name() != "dl" && !strings.HasSuffix(e.Name(), ".tmp") {
			logf("ancienne version du moteur supprimée : " + e.Name())
		}
	}
	if old := filepath.Join(backendsDir(), "llama.cpp-prebuilt"); isDir(old) {
		if err := os.RemoveAll(old); err == nil {
			logf("ancien moteur précompilé supprimé : " + old)
		}
	}
}

// engineEnsure renvoie le chemin du llama-server à lancer, en installant le
// moteur épinglé s'il manque. C'est ce qu'appelle « ajean serve » : après une
// mise à jour d'AJEAN qui change de version de llama.cpp, le nouveau moteur est
// en place avant le premier chargement de modèle, sans geste de l'utilisateur.
func engineEnsure(logf, phasef func(string)) (string, error) {
	if p := engineDevBin(); p != "" {
		return p, nil
	}
	if e := engineInstalled(); e != nil {
		return e.binPath(), nil
	}
	e, err := engineInstall(false, logf, phasef)
	if err != nil {
		return "", err
	}
	return e.binPath(), nil
}

// ---------------------------------------------------------------------------
// Ligne de commande : ajean engine [status | install | repair]
// ---------------------------------------------------------------------------

func cmdEngine(args []string) error {
	sub := "status"
	if len(args) > 0 {
		sub, args = args[0], args[1:]
	}
	switch sub {
	case "status":
		return engineStatusCLI()
	case "install", "repair":
		variant, setVariant := "", false
		for i := 0; i < len(args); i++ {
			a := args[i]
			if a == "--variant" && i+1 < len(args) {
				i++
				a = "--variant=" + args[i]
			}
			v, ok := strings.CutPrefix(a, "--variant=")
			if !ok {
				return fmt.Errorf("option inconnue : %s (attendu --variant=auto|cuda|rocm|vulkan|cpu)", a)
			}
			variant, setVariant = v, true
		}
		if setVariant {
			if err := setEngineForcedVariant(variant); err != nil {
				return err
			}
		}
		force := sub == "repair" || setVariant
		if !force {
			if e := engineInstalled(); e != nil {
				fmt.Printf("%s moteur déjà installé : llama.cpp %s, %s\n", green("[ok]"), e.Tag, e.Label)
				return nil
			}
		}
		// Le binaire en cours d'exécution ne peut pas être remplacé (Windows) :
		// on arrête le service le temps de la réparation.
		wasUp := force && engineInstalled() != nil && serviceIsActive()
		if wasUp {
			fmt.Printf("%s arrêt du service %s le temps de la réinstallation…\n", yellow("[info]"), serviceName())
			_ = serviceAction("stop")
		}
		_, err := engineInstall(force,
			func(s string) { fmt.Println("  " + s) },
			func(s string) { fmt.Printf("%s %s\n", cyan("▶"), s) })
		if wasUp {
			_ = serviceAction("start")
		}
		return err
	default:
		return fmt.Errorf("sous-commande inconnue : %s (status | install | repair [--variant=auto|cuda|rocm|vulkan|cpu])", sub)
	}
}

func engineStatusCLI() error {
	fmt.Printf("%s\n", bold("Moteur llama.cpp"))
	fmt.Printf("  version  : %s (livrée avec AJEAN %s)\n", enginePin(), Version)
	if p := engineDevBin(); p != "" {
		fmt.Printf("  %s AJEAN_ENGINE_BIN remplace le moteur livré : %s\n", yellow("dev"), p)
	}
	if e := engineInstalled(); e != nil {
		fmt.Printf("  variante : %s\n", e.Label)
		fmt.Printf("  binaire  : %s\n", green(e.binPath()))
	} else {
		fmt.Printf("  état     : %s — il s'installe seul au démarrage, ou tout de suite avec %s\n",
			yellow("pas encore installé"), bold("ajean engine install"))
	}
	if v := engineForcedVariant(); v != "" {
		fmt.Printf("  imposée  : %s (retour à l'automatique : %s)\n", v, bold("ajean engine repair --variant=auto"))
	}
	if legacy := engineLegacyDirs(); len(legacy) > 0 {
		fmt.Printf("\n  Dossiers d'anciennes versions, qu'AJEAN n'utilise plus (à supprimer si tu n'en as plus besoin) :\n")
		for _, l := range legacy {
			fmt.Printf("    %s  %s\n", l.Path, dim("— "+l.What))
		}
	}
	return nil
}
