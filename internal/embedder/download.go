package embedder

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	fetchAttempts = 4
	stallMax      = time.Minute // sans un octet reçu pendant ce temps, la connexion est tenue pour morte
	partExt       = ".part"
)

// retryPause : l'attente avant de reprendre un téléchargement coupé.
var retryPause = 3 * time.Second

// statusError : le serveur a refusé la requête. Insister ne changerait rien.
type statusError int

func (e statusError) Error() string { return fmt.Sprintf("le serveur a répondu %d", int(e)) }

// fetch télécharge url dans dest. Le fichier est écrit sous un nom provisoire
// et ne prend son nom qu'une fois complet et vérifié : dest n'est jamais un
// fichier à moitié reçu. Un téléchargement coupé reprend où il s'était arrêté,
// tout de suite si la coupure vient du réseau, au prochain appel sinon. size
// (0 : inconnue) et sum (SHA-256 en hexadécimal, "" : inconnue) sont vérifiées ;
// un fichier dont l'empreinte diffère est supprimé.
func fetch(ctx context.Context, url, dest string, size int64, sum string, progress func(done, total int64)) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	part := dest + partExt
	for attempt := 1; ; attempt++ {
		err := download(ctx, url, part, size, progress)
		if err == nil {
			break
		}
		var refused statusError
		if ctx.Err() != nil || errors.As(err, &refused) || attempt == fetchAttempts {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryPause):
		}
	}
	if sum != "" {
		got, err := fileSHA256(part)
		if err != nil {
			return err
		}
		if !strings.EqualFold(got, sum) {
			_ = os.Remove(part)
			return fmt.Errorf("%s refusé : son empreinte SHA-256 (%s) n'est pas celle attendue (%s)", filepath.Base(dest), got, sum)
		}
	}
	return os.Rename(part, dest)
}

// download reçoit dans part ce qui lui manque encore.
func download(ctx context.Context, url, part string, size int64, progress func(done, total int64)) error {
	var have int64
	if fi, err := os.Stat(part); err == nil {
		have = fi.Size()
	}
	if size > 0 && have == size {
		progress(have, size)
		return nil
	}
	if size > 0 && have > size {
		have = 0 // plus gros que le fichier attendu : ce n'est pas lui
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	stall := time.AfterFunc(stallMax, cancel)
	defer stall.Stop()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "ajean")
	if have > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", have))
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	flags := os.O_CREATE | os.O_WRONLY
	switch resp.StatusCode {
	case http.StatusPartialContent:
		if !strings.HasPrefix(resp.Header.Get("Content-Range"), fmt.Sprintf("bytes %d-", have)) {
			return errors.New("le serveur a repris le téléchargement au mauvais endroit")
		}
		flags |= os.O_APPEND
	case http.StatusOK: // le serveur ignore la reprise : on repart du début
		flags |= os.O_TRUNC
		have = 0
	case http.StatusRequestedRangeNotSatisfiable:
		// Ce qu'on a déjà ne correspond plus au fichier servi.
		_ = os.Remove(part)
		return errors.New("reprise du téléchargement impossible")
	default:
		return statusError(resp.StatusCode)
	}
	total := size
	if total == 0 && resp.ContentLength > 0 {
		total = have + resp.ContentLength
	}
	f, err := os.OpenFile(part, flags, 0o644)
	if err != nil {
		return err
	}
	done, err := receive(f, resp.Body, have, total, stall, progress)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && total > 0 && done != total {
		err = fmt.Errorf("téléchargement incomplet (%d octets sur %d)", done, total)
	}
	return err
}

// receive copie le corps de la réponse dans f, en repoussant à chaque morceau
// l'échéance qui coupe une connexion muette.
func receive(f *os.File, body io.Reader, done, total int64, stall *time.Timer, progress func(done, total int64)) (int64, error) {
	buf := make([]byte, 256<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return done, werr
			}
			done += int64(n)
			stall.Reset(stallMax)
			progress(done, total)
		}
		if err == io.EOF {
			return done, nil
		}
		if err != nil {
			return done, err
		}
	}
}

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
