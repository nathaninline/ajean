//go:build linux

package ajean

// backend_moe_linux.go : préparation du moteur MoE sous Linux — le binaire de
// la release (moteur compilé par nous, patch remote-drop inclus).

import (
	"archive/zip"
	"io"
	"os"
	"path/filepath"
	"strconv"
)

// moeEngineDir : le dossier où le moteur attend — engine-cuda12/ sous Linux
// (release CUDA 12).
func moeEngineDir() string { return filepath.Join(moeSrcDir(), "engine-cuda12") }

// moePrepareEngine : pose le moteur de la release dans engine-cuda12/.
func moePrepareEngine() error {
	if moeEngineInstalled() == moeEngineAsset {
		return nil
	}
	dir := moeEngineDir()
	if _, err := os.Stat(dir); err != nil {
		return err
	}
	zp, err := moeFetch(moeEngineAsset)
	if err != nil {
		return err
	}
	z, err := zip.OpenReader(zp)
	if err != nil {
		return err
	}
	defer z.Close()
	for _, f := range z.File {
		name := filepath.Base(f.Name)
		if name != "strata" && name != "strata-vision" && name != "BUILD.json" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		tmp := filepath.Join(dir, name+".new")
		w, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err == nil {
			_, err = io.Copy(w, rc)
			if cerr := w.Close(); err == nil {
				err = cerr
			}
		}
		rc.Close()
		if err != nil {
			_ = os.Remove(tmp)
			return err
		}
		// rename : remplace le binaire même s'il tourne encore
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return os.WriteFile(moeEngineMarker(), []byte(moeEngineAsset+"\n"), 0o644)
}

// moeEnsureEngine : la porte d'entrée commune (appelée par serveMoe).
func moeEnsureEngine() error {
	if moeEngineInstalled() == moeEngineAsset {
		return nil
	}
	dir := moeEngineDir()
	if _, err := os.Stat(dir); err != nil {
		return err
	}
	return moePrepareEngine()
}

// moeRunInstaller : l'installeur du moteur sous Linux — setup.sh crée le venv
// puis y relance setup.py (Python 3.10+ et venv vérifiés par moeDetect).
func moeRunInstaller(family, quant string, env moeEnv, pre string) error {
	args := []string{"setup.sh", "--setup", "--yes", "--no-start", "--no-browser",
		"--family", family, "--model", quant,
		"--context", "131072", "--vision", "gpu",
		"--cuda", "12", "--prebuilt", pre + string(os.PathSeparator),
		"--data-dir", moeDataDir(),
		"--gpu", strconv.Itoa(env.Main),
	}
	// Le mode « peu de RAM » est décidé et appliqué par ajean (MOE_MMAP, voir
	// moeBuildConfig) : l'installeur tourne en mode normal, sans réserver sa
	// propre variante (qui ne voit pas un experts.bin déjà présent).
	args = append(args, "--low-ram", "off")
	return runStepEnv("installation du moteur", moeSrcDir(),
		"CUDA_DEVICE_ORDER=PCI_BUS_ID\x00PYTHONUNBUFFERED=1", "bash", args...)
}

// moeVenvPython : le Python de l'environnement du moteur, dans ses sources.
func moeVenvPython(srcDir string) string { return filepath.Join(srcDir, ".venv", "bin", "python") }

// moeWriteLauncher : le lanceur moeHome()/ajean-moe (sh) que ajean serve exécute.
// Renvoie (programme, args) — le sh passe ses args à server.py via "$@", et args
// est SANS le programme (c'est serveMoe qui le pose en tête, comme pour
// llama-server).
func moeWriteLauncher(srcDir, py string) (string, []string, error) {
	p := filepath.Join(moeHome(), "ajean-moe")
	body := "#!/bin/sh\n# AJEAN MoE : serveur du moteur MoE d'AJEAN (lancé par ajean serve)\n" +
		"cd '" + srcDir + "' && exec '" + py + "' '" + filepath.Join(srcDir, "serve", "server.py") + "' --engine strata \"$@\"\n"
	if err := os.WriteFile(p+".new", []byte(body), 0o755); err != nil {
		return "", nil, err
	}
	if err := os.Rename(p+".new", p); err != nil {
		return "", nil, err
	}
	return p, nil, nil
}

// moeAlias : un lien moeHome()/bin/<name> vers un binaire du moteur, recréé à
// chaque lancement ; le process apparaît sous ce nom. En cas d'échec, le chemin
// d'origine.
func moeAlias(target, name string) string {
	dir := filepath.Join(moeHome(), "bin")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return target
	}
	link := filepath.Join(dir, name)
	tmp := link + ".new"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return target
	}
	if err := os.Rename(tmp, link); err != nil {
		_ = os.Remove(tmp)
		return target
	}
	return link
}
