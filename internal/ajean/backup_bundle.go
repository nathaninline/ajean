package ajean

// backup_bundle.go — construit et restaure un paquet de sauvegarde
// {mémoire, presets, réglages}, CHIFFRÉ côté agent avant de quitter la machine.
//
// Le relais ne voit qu'un blob opaque (voir jean-relay/backup.go). Comme le tar
// entier est chiffré, même les noms de fichiers et de presets sont cachés.
//
// Format du blob :
//   magic "AJBK1" || uint32(len entête) || entête JSON || corps
//   entête  : { v, salt, kdf, box_pw, box_rec } — la DEK de sauvegarde wrappée
//             par la KEK(mot de passe) ET par la KEK(clé de récupération).
//   corps   : gcmSeal(backupDEK, tar.gz du bundle).
//
// Restauration : mot de passe → KEK → déballe la DEK → déchiffre le corps. Ne
// dépend d'AUCUN état de l'ancienne machine : un serveur vierge suffit.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

var backupMagic = []byte("AJBK2") // v2 : entête = keyvault, corps chiffré par la DEK

const backupBodyAAD = "ajean-backup-body"

// buildBundleTar empaquète mémoire + presets + réglages en un tar.gz.
// Les fichiers mémoire sont pris TELS QUELS (chiffrés si le chiffrement est
// actif) : le bundle reste donc chiffré même avant l'enveloppe de sauvegarde.
func buildBundleTar() ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)

	addDir := func(prefix, dir string) error {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil // dossier absent = rien à ajouter
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				return err
			}
			hdr := &tar.Header{Name: prefix + "/" + e.Name(), Mode: 0o600, Size: int64(len(b))}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if _, err := tw.Write(b); err != nil {
				return err
			}
		}
		return nil
	}

	// Mémoire de TOUS les projets : un sous-dossier par projet (memory/<slug>/...).
	if roots, err := os.ReadDir(projectsRoot()); err == nil {
		for _, d := range roots {
			if !d.IsDir() {
				continue
			}
			if err := addDir("memory/"+d.Name(), filepath.Join(projectsRoot(), d.Name())); err != nil {
				return nil, err
			}
		}
	}
	if err := addDir("presets", presetsDir()); err != nil {
		return nil, err
	}
	// Réglages (bucket config) sérialisés en JSON.
	cfg, _ := json.Marshal(ReadConfig())
	if err := tw.WriteHeader(&tar.Header{Name: "config.json", Mode: 0o600, Size: int64(len(cfg))}); err != nil {
		return nil, err
	}
	if _, err := tw.Write(cfg); err != nil {
		return nil, err
	}

	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// buildBackupBlob assemble le paquet chiffré : magic || uint32(len keyvault) ||
// keyvault JSON || gcmSeal(DEK, tar). AUCUN mot de passe de sauvegarde : le corps
// est chiffré avec la DEK de la mémoire (déjà en RAM), et le keyvault (la DEK
// wrappée par la clé d'API + la clé de récupération) voyage en entête. La
// restauration n'a donc besoin que de la clé d'API. Le relais ne voit qu'un blob
// opaque : le keyvault en entête ne contient que la DEK CHIFFRÉE.
func buildBackupBlob(v *keyVault, dek, tarData []byte) ([]byte, error) {
	kv, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	body, err := gcmSeal(dek, tarData, []byte(backupBodyAAD))
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	out.Write(backupMagic)
	var lenbuf [4]byte
	binary.BigEndian.PutUint32(lenbuf[:], uint32(len(kv)))
	out.Write(lenbuf[:])
	out.Write(kv)
	out.Write(body)
	return out.Bytes(), nil
}

// openBackupBlob lit l'entête keyvault, l'ouvre avec secret (clé d'API OU clé de
// récupération) pour retrouver la DEK, puis déchiffre le corps.
func openBackupBlob(blob []byte, secret string) ([]byte, error) {
	if !bytes.HasPrefix(blob, backupMagic) {
		return nil, fmt.Errorf("format de sauvegarde non reconnu")
	}
	rest := blob[len(backupMagic):]
	if len(rest) < 4 {
		return nil, fmt.Errorf("blob tronqué")
	}
	n := binary.BigEndian.Uint32(rest[:4])
	rest = rest[4:]
	if int(n) > len(rest) {
		return nil, fmt.Errorf("entête tronqué")
	}
	var v keyVault
	if err := json.Unmarshal(rest[:n], &v); err != nil {
		return nil, fmt.Errorf("entête illisible : %w", err)
	}
	body := rest[n:]
	dek, _, err := v.unlockWith(secret)
	if err != nil {
		dek, _, err = v.unlockWith(normalizeRecovery(secret))
	}
	if err != nil {
		return nil, fmt.Errorf("clé incorrecte")
	}
	return gcmOpen(dek, body, []byte(backupBodyAAD))
}

// restoreBundleTar réécrit mémoire + presets + réglages depuis un tar.gz. Prend
// D'ABORD un snapshot de sécurité de la mémoire, puis écrit de façon vérifiée.
func restoreBundleTar(tarData []byte) error {
	if _, err := snapshotMemory("avant-restauration-relais"); err != nil {
		return fmt.Errorf("snapshot de sécurité impossible, restauration annulée : %w", err)
	}
	gz, err := gzip.NewReader(bytes.NewReader(tarData))
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	var cfgJSON []byte
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			return err
		}
		// Les chemins tar sont TOUJOURS à séparateur « / » (jamais l'OS) : on
		// raisonne en slash, sinon le préfixe "memory/" casse sous Windows.
		name := path.Clean("/" + strings.ReplaceAll(hdr.Name, "\\", "/"))[1:]
		if strings.Contains(name, "..") {
			continue // sécurité : jamais de traversée
		}
		base := path.Base(name)
		switch {
		case name == "config.json":
			cfgJSON = b
		case strings.HasPrefix(name, "memory/"):
			// Chemin relatif SOUS memory/ (memory/<slug>/page.md). Les sauvegardes
			// d'avant les projets stockaient les pages à plat (memory/page.md) : on
			// les rattache au projet par défaut.
			rel := strings.TrimPrefix(name, "memory/")
			if !strings.Contains(rel, "/") {
				rel = defaultProjectSlug + "/" + rel
			}
			dst := filepath.Join(projectsRoot(), filepath.FromSlash(rel))
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				return err
			}
			if err := memWriteFileVerified(dst, b, 0o600); err != nil {
				return err
			}
		case strings.HasPrefix(name, "presets/"):
			if err := os.MkdirAll(presetsDir(), 0o755); err != nil {
				return err
			}
			if err := memWriteFileVerified(filepath.Join(presetsDir(), base), b, 0o600); err != nil {
				return err
			}
		}
	}
	if len(cfgJSON) > 0 {
		var cfg map[string]string
		if json.Unmarshal(cfgJSON, &cfg) == nil {
			_ = WriteConfig(cfg)
		}
	}
	return nil
}
