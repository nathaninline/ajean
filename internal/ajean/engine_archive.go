// engine_archive.go — téléchargement vérifié (SHA-256), avec reprise, des
// archives du moteur épinglé. L'extraction (.zip sous Windows, .tar.gz ailleurs)
// est celle du précompilé : extractArchive, backend_prebuilt.go.
package ajean

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// fileSHA256 renvoie l'empreinte hexadécimale d'un fichier.
func fileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// engineRetryPause : attente avant un nouvel essai de téléchargement (multipliée
// par le numéro de l'essai). Variable pour les tests.
var engineRetryPause = 2 * time.Second

// engineFetch télécharge url vers dest et vérifie taille et empreinte. Une
// archive déjà présente et intacte n'est pas retéléchargée ; un téléchargement
// coupé reprend où il s'était arrêté (dest + « .part », requête Range). Trois
// tentatives : les archives CUDA pèsent plusieurs centaines de Mo et une
// coupure réseau ne doit pas faire repartir de zéro.
func engineFetch(ctx context.Context, url, dest string, size int64, sha string, logf func(string)) error {
	if got, err := fileSHA256(dest); err == nil && strings.EqualFold(got, sha) {
		logf("déjà téléchargé : " + filepath.Base(dest))
		return nil
	}
	part := dest + ".part"
	var lastErr error
	for attempt := 1; attempt <= 3; attempt++ {
		if attempt > 1 {
			logf(fmt.Sprintf("nouvel essai (%d/3) : %v", attempt, lastErr))
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt) * engineRetryPause):
			}
		}
		if lastErr = engineFetchOnce(ctx, url, part, size, logf); lastErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			continue
		}
		got, err := fileSHA256(part)
		if err != nil {
			return err
		}
		if !strings.EqualFold(got, sha) {
			// Fichier complet mais différent de celui qui a été validé : on ne
			// l'installe jamais, et on ne le garde pas pour une reprise.
			_ = os.Remove(part)
			lastErr = fmt.Errorf("empreinte SHA-256 inattendue pour %s (reçu %s, attendu %s)", filepath.Base(dest), got, sha)
			continue
		}
		_ = os.Remove(dest)
		return os.Rename(part, dest)
	}
	return lastErr
}

func engineFetchOnce(ctx context.Context, url, part string, size int64, logf func(string)) error {
	var have int64
	if fi, err := os.Stat(part); err == nil {
		have = fi.Size()
	}
	if size > 0 && have >= size {
		if have == size {
			return nil // complet : l'empreinte tranchera
		}
		_ = os.Remove(part)
		have = 0
	}
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := (&http.Client{Timeout: 0}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		flags |= os.O_APPEND
	case http.StatusOK:
		flags |= os.O_TRUNC // le serveur ignore Range : on repart du début
		have = 0
	default:
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	buf := make([]byte, 1<<20)
	done, lastLog := have, have
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return werr
			}
			done += int64(n)
			if done-lastLog >= 25<<20 {
				lastLog = done
				if size > 0 {
					logf(fmt.Sprintf("⬇ %d / %d Mo (%d%%)", done/1_000_000, size/1_000_000, done*100/size))
				} else {
					logf(fmt.Sprintf("⬇ %d Mo", done/1_000_000))
				}
			}
		}
		if rerr == io.EOF {
			if size > 0 && done != size {
				return fmt.Errorf("téléchargement incomplet (%d / %d octets)", done, size)
			}
			return nil
		}
		if rerr != nil {
			return rerr
		}
	}
}

// copyTree copie récursivement src vers dst (fichiers et liens symboliques).
func copyTree(src, dst string) error {
	return filepath.Walk(src, func(p string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		out := filepath.Join(dst, rel)
		switch {
		case fi.IsDir():
			return os.MkdirAll(out, 0o755)
		case fi.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(p)
			if err != nil {
				return err
			}
			_ = os.Remove(out)
			return os.Symlink(target, out)
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		w, err := os.OpenFile(out, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fi.Mode()&0o777)
		if err != nil {
			return err
		}
		if _, err := io.Copy(w, in); err != nil {
			w.Close()
			return err
		}
		return w.Close()
	})
}
