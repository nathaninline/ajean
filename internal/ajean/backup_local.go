package ajean

// backup_local.go — la sauvegarde LOCALE : une archive chiffrée de tout ce
// qu'AJEAN sait, écrite dans un dossier choisi (disque externe, dossier
// synchronisé).
//
// Elle complète ce qui existait sans le remplacer :
//   - la sauvegarde ajean.link (backup_bundle.go, relay_backup.go) est réservée
//     aux abonnés, exige la mémoire chiffrée ET déverrouillée, et n'emporte que
//     la mémoire, les presets et la configuration ;
//   - les snapshots locaux (mem_snapshots.go) ne couvrent que la mémoire et
//     restent sur le même disque.
//
// Celle-ci marche pour tout le monde, sans relais ni chiffrement de la mémoire,
// et emporte AUSSI la base (conversations, tâches, réglages, serveurs MCP), les
// scripts, les apps, les voix, le coffre de la mémoire et la clé du coffre des
// clés d'API. N'y sont pas : les modèles, les moteurs, le dossier de travail
// jetable et les snapshots — gros, et qui se retrouvent autrement.
//
//	ajean backup <dossier>             écrit ajean-AAAAMMJJ-HHMMSS.ajbk
//	ajean restore <fichier> <dossier>  la déchiffre dans un dossier VIDE
//
// La restauration n'écrit jamais par-dessus une installation : on restaure à
// côté, on regarde, puis on pointe AJEAN_HOME dessus (ou on recopie).
//
// AJEAN_BACKUP_DIR + AJEAN_BACKUP_KEY : une sauvegarde par jour depuis le
// service d'interface, les 7 dernières gardées.
//
// Format : "AJLB1" | sel (16) | t, m (uint32) | p (uint8) | blocs. Chaque bloc :
// drapeau (1 = dernier) | longueur (uint32) | AES-256-GCM(tar.gz, par tranches
// de 1 Mio). La clé vient de la phrase par Argon2id (mem_crypto.go). Le drapeau
// est authentifié et le compteur de bloc est dans le nonce : une archive
// tronquée, réordonnée ou altérée est refusée, jamais restaurée à moitié.

import (
	"archive/tar"
	"compress/gzip"
	"crypto/cipher"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	bolt "go.etcd.io/bbolt"
)

const (
	localBackupMagic  = "AJLB1"
	localBackupChunk  = 1 << 20
	localBackupPrefix = "ajean-"
	localBackupSuffix = ".ajbk"
	localBackupKept   = 7
	localBackupMinPw  = 12
	stLocalBackupLast = "backup_local_last"
)

// localBackupKDF : coût de la dérivation de la phrase (les tests l'abaissent).
var localBackupKDF = kekParams{T: argonTime, M: argonMemory, P: argonThreads}

// localBackupDirs / localBackupFiles : ce qui part dans l'archive, relatif à
// AJEAN_HOME. La base est traitée à part (copie cohérente).
var (
	localBackupDirs  = []string{"memory", "presets", "scripts", "jean-scripts", "apps", "voices", "actions", "approved"}
	localBackupFiles = []string{".keyvault", ".keyvault.bak", ".secrets_key", ".secrets_key.bak"}
)

// ---- chiffrement par blocs -----------------------------------------------------

type sealWriter struct {
	w   io.Writer
	gcm cipher.AEAD
	buf []byte
	n   uint64
}

func chunkNonce(n uint64) []byte {
	nonce := make([]byte, memNonceLen)
	binary.BigEndian.PutUint64(nonce[memNonceLen-8:], n)
	return nonce
}

func chunkAAD(last bool) []byte {
	if last {
		return []byte(localBackupMagic + "/1")
	}
	return []byte(localBackupMagic + "/0")
}

func (s *sealWriter) flush(last bool) error {
	ct := s.gcm.Seal(nil, chunkNonce(s.n), s.buf, chunkAAD(last))
	s.n++
	s.buf = s.buf[:0]
	head := make([]byte, 5)
	if last {
		head[0] = 1
	}
	binary.BigEndian.PutUint32(head[1:], uint32(len(ct)))
	if _, err := s.w.Write(head); err != nil {
		return err
	}
	_, err := s.w.Write(ct)
	return err
}

func (s *sealWriter) Write(p []byte) (int, error) {
	total := len(p)
	for len(p) > 0 {
		room := localBackupChunk - len(s.buf)
		if room > len(p) {
			room = len(p)
		}
		s.buf = append(s.buf, p[:room]...)
		p = p[room:]
		if len(s.buf) == localBackupChunk {
			if err := s.flush(false); err != nil {
				return 0, err
			}
		}
	}
	return total, nil
}

// Close écrit le dernier bloc (éventuellement vide) : c'est lui qui prouve que
// l'archive est entière.
func (s *sealWriter) Close() error { return s.flush(true) }

type openReader struct {
	r    io.Reader
	gcm  cipher.AEAD
	buf  []byte
	n    uint64
	done bool
}

var errBackupDamaged = errors.New("phrase incorrecte ou archive abîmée")

func (o *openReader) Read(p []byte) (int, error) {
	for len(o.buf) == 0 {
		if o.done {
			return 0, io.EOF
		}
		head := make([]byte, 5)
		if _, err := io.ReadFull(o.r, head); err != nil {
			return 0, errors.New("archive tronquée")
		}
		size := binary.BigEndian.Uint32(head[1:])
		if head[0] > 1 || size > localBackupChunk+64 {
			return 0, errBackupDamaged
		}
		ct := make([]byte, size)
		if _, err := io.ReadFull(o.r, ct); err != nil {
			return 0, errors.New("archive tronquée")
		}
		last := head[0] == 1
		pt, err := o.gcm.Open(nil, chunkNonce(o.n), ct, chunkAAD(last))
		if err != nil {
			return 0, errBackupDamaged
		}
		o.n++
		o.buf, o.done = pt, last
		if last {
			// Rien ne doit suivre le dernier bloc.
			if n, _ := o.r.Read(make([]byte, 1)); n != 0 {
				return 0, errBackupDamaged
			}
		}
	}
	n := copy(p, o.buf)
	o.buf = o.buf[n:]
	return n, nil
}

// ---- contenu ---------------------------------------------------------------------

func localBackupSkip(rel string) bool {
	base := filepath.Base(rel)
	return strings.HasPrefix(base, ".tmp-") || strings.HasSuffix(base, ".tmp") || strings.HasSuffix(base, ".part")
}

// writeLocalBackupTar écrit le contenu d'AJEAN_HOME dans tw.
func writeLocalBackupTar(tw *tar.Writer) error {
	root := AjeanHome()
	// La base : une copie COHÉRENTE, prise dans une transaction de lecture (un
	// simple recopiage du fichier pourrait tomber en pleine écriture).
	err := withDB(func(d *bolt.DB) error {
		return d.View(func(tx *bolt.Tx) error {
			if err := tw.WriteHeader(&tar.Header{Name: "ajean.db", Mode: 0o600, Size: tx.Size(), ModTime: time.Now()}); err != nil {
				return err
			}
			_, err := tx.WriteTo(tw)
			return err
		})
	})
	if err != nil {
		return fmt.Errorf("copie de la base : %w", err)
	}
	addFile := func(path, rel string, info fs.FileInfo) error {
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()
		hdr := &tar.Header{Name: filepath.ToSlash(rel), Mode: int64(info.Mode().Perm()), Size: info.Size(), ModTime: info.ModTime()}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		// CopyN : un fichier qui grossit pendant la copie ne doit pas casser le tar.
		if _, err := io.CopyN(tw, f, hdr.Size); err != nil {
			return fmt.Errorf("%s : %w", rel, err)
		}
		return nil
	}
	for _, name := range localBackupFiles {
		p := filepath.Join(root, name)
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		if err := addFile(p, name, info); err != nil {
			return err
		}
	}
	for _, dir := range localBackupDirs {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil
				}
				return err
			}
			rel, err := filepath.Rel(root, path)
			if err != nil {
				return err
			}
			if d.IsDir() || !d.Type().IsRegular() || localBackupSkip(rel) {
				return nil
			}
			info, err := d.Info()
			if err != nil {
				if errors.Is(err, fs.ErrNotExist) {
					return nil // retiré entre le listing et la lecture
				}
				return err
			}
			return addFile(path, rel, info)
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func localBackupCipher(pass string, salt []byte, kp kekParams) (cipher.AEAD, error) {
	return memNewGCM(deriveKEK(pass, salt, kp.T, kp.M, kp.P))
}

// CreateLocalBackup écrit une archive chiffrée dans dir et renvoie son chemin.
// L'archive est écrite sous un nom provisoire puis renommée : un fichier .ajbk
// est toujours une archive complète.
func CreateLocalBackup(dir, pass string) (string, error) {
	if len([]rune(pass)) < localBackupMinPw {
		return "", fmt.Errorf("phrase de sauvegarde trop courte (%d caractères au moins)", localBackupMinPw)
	}
	if strings.TrimSpace(dir) == "" {
		return "", errors.New("dossier de sauvegarde manquant")
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if underDir(abs, AjeanHome()) {
		return "", fmt.Errorf("le dossier de sauvegarde ne peut pas être dans %s : choisis un autre disque ou un autre dossier", AjeanHome())
	}
	if err := os.MkdirAll(abs, 0o700); err != nil {
		return "", err
	}
	salt, err := randBytes(memSaltLen)
	if err != nil {
		return "", err
	}
	gcm, err := localBackupCipher(pass, salt, localBackupKDF)
	if err != nil {
		return "", err
	}
	final := filepath.Join(abs, localBackupPrefix+time.Now().Format("20060102-150405")+localBackupSuffix)
	tmp := final + ".partial"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	fail := func(err error) (string, error) {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	head := append([]byte(localBackupMagic), salt...)
	head = binary.BigEndian.AppendUint32(head, localBackupKDF.T)
	head = binary.BigEndian.AppendUint32(head, localBackupKDF.M)
	head = append(head, localBackupKDF.P)
	if _, err := f.Write(head); err != nil {
		return fail(err)
	}
	sw := &sealWriter{w: f, gcm: gcm, buf: make([]byte, 0, localBackupChunk)}
	gz := gzip.NewWriter(sw)
	tw := tar.NewWriter(gz)
	if err := writeLocalBackupTar(tw); err != nil {
		return fail(err)
	}
	if err := tw.Close(); err != nil {
		return fail(err)
	}
	if err := gz.Close(); err != nil {
		return fail(err)
	}
	if err := sw.Close(); err != nil {
		return fail(err)
	}
	if err := f.Sync(); err != nil {
		return fail(err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return "", err
	}
	if err := os.Rename(tmp, final); err != nil {
		os.Remove(tmp)
		return "", err
	}
	return final, nil
}

// RestoreLocalBackup déchiffre une archive dans dest, un dossier vide ou absent.
// L'archive est d'abord lue EN ENTIER à blanc : une phrase fausse ou un fichier
// abîmé ne laissent rien derrière eux.
func RestoreLocalBackup(path, dest, pass string) (int, error) {
	if ents, err := os.ReadDir(dest); err == nil && len(ents) > 0 {
		return 0, fmt.Errorf("le dossier %s doit être vide : une restauration n'écrit jamais par-dessus des données", dest)
	}
	root, err := filepath.Abs(dest)
	if err != nil {
		return 0, err
	}
	if _, err := walkLocalBackup(path, pass, nil); err != nil {
		return 0, err
	}
	return walkLocalBackup(path, pass, func(name string, mode os.FileMode, r io.Reader) error {
		clean := filepath.Clean(filepath.FromSlash(name))
		target := filepath.Join(root, clean)
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) || !underDir(target, root) || target == root {
			return fmt.Errorf("chemin refusé dans l'archive : %s", name)
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		if mode == 0 {
			mode = 0o600
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_EXCL, mode.Perm())
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, r); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

// walkLocalBackup déchiffre l'archive et appelle fn pour chaque fichier (fn nil :
// simple vérification). Renvoie le nombre de fichiers.
func walkLocalBackup(path, pass string, fn func(name string, mode os.FileMode, r io.Reader) error) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	head := make([]byte, len(localBackupMagic)+memSaltLen+9)
	if _, err := io.ReadFull(f, head); err != nil || string(head[:len(localBackupMagic)]) != localBackupMagic {
		return 0, errors.New("ce fichier n'est pas une sauvegarde locale d'AJEAN")
	}
	rest := head[len(localBackupMagic):]
	salt := rest[:memSaltLen]
	kp := kekParams{T: binary.BigEndian.Uint32(rest[memSaltLen:]), M: binary.BigEndian.Uint32(rest[memSaltLen+4:]), P: rest[memSaltLen+8]}
	// Paramètres lus dans un fichier non authentifié à ce stade : bornés, pour
	// qu'une archive forgée ne réclame pas des gigaoctets à la dérivation.
	if kp.T == 0 || kp.T > 16 || kp.M == 0 || kp.M > 1<<20 || kp.P == 0 || kp.P > 16 {
		return 0, errBackupDamaged
	}
	gcm, err := localBackupCipher(pass, salt, kp)
	if err != nil {
		return 0, err
	}
	gz, err := gzip.NewReader(&openReader{r: f, gcm: gcm})
	if err != nil {
		if errors.Is(err, errBackupDamaged) || strings.Contains(err.Error(), "tronquée") {
			return 0, err
		}
		return 0, errBackupDamaged
	}
	tr := tar.NewReader(gz)
	n := 0
	for {
		h, err := tr.Next()
		if errors.Is(err, io.EOF) {
			// Vide le flux jusqu'au dernier bloc : c'est lui qui atteste l'intégrité.
			if _, err := io.Copy(io.Discard, gz); err != nil {
				return n, err
			}
			return n, nil
		}
		if err != nil {
			return n, err
		}
		if h.Typeflag != tar.TypeReg {
			continue
		}
		if fn == nil {
			if _, err := io.Copy(io.Discard, tr); err != nil {
				return n, err
			}
		} else if err := fn(h.Name, os.FileMode(h.Mode), tr); err != nil {
			return n, err
		}
		n++
	}
}

// pruneLocalBackups ne garde que les `keep` archives les plus récentes de dir
// (le nom porte la date). Ne touche à aucun autre fichier.
func pruneLocalBackups(dir string, keep int) {
	ents, _ := os.ReadDir(dir)
	var names []string
	for _, e := range ents {
		if !e.IsDir() && strings.HasPrefix(e.Name(), localBackupPrefix) && strings.HasSuffix(e.Name(), localBackupSuffix) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for i := 0; i < len(names)-keep; i++ {
		_ = os.Remove(filepath.Join(dir, names[i]))
	}
}

// ---- sauvegarde quotidienne ------------------------------------------------------

var localBackupSchedOnce sync.Once

// StartLocalBackupScheduler : une sauvegarde par jour si AJEAN_BACKUP_DIR et
// AJEAN_BACKUP_KEY sont définis. Sans eux (le défaut), ne fait rien.
func StartLocalBackupScheduler() {
	dir, pass := strings.TrimSpace(os.Getenv("AJEAN_BACKUP_DIR")), os.Getenv("AJEAN_BACKUP_KEY")
	if dir == "" || pass == "" {
		return
	}
	localBackupSchedOnce.Do(func() {
		go func() {
			for {
				localBackupTick(dir, pass, time.Now())
				time.Sleep(time.Hour)
			}
		}()
	})
}

// localBackupTick fait la sauvegarde du jour si la dernière a plus de 24 h.
func localBackupTick(dir, pass string, now time.Time) {
	if last := getStr(bkState, stLocalBackupLast); last != "" {
		if ts, err := time.Parse(time.RFC3339, last); err == nil && now.Sub(ts) < 24*time.Hour {
			return
		}
	}
	path, err := CreateLocalBackup(dir, pass)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[sauvegarde] sauvegarde locale impossible : %v\n", err)
		return
	}
	_ = putStr(bkState, stLocalBackupLast, now.UTC().Format(time.RFC3339))
	pruneLocalBackups(dir, localBackupKept)
	fmt.Printf("[sauvegarde] %s\n", path)
}

// ---- commandes -------------------------------------------------------------------

// localBackupPass : la phrase, depuis AJEAN_BACKUP_KEY ou saisie au clavier.
func localBackupPass(confirm bool) (string, error) {
	if p := os.Getenv("AJEAN_BACKUP_KEY"); p != "" {
		return p, nil
	}
	pass, tty, err := readSecretLine("Phrase de sauvegarde : ")
	if err != nil {
		return "", err
	}
	if tty && confirm {
		again, _, err := readSecretLine("Encore une fois : ")
		if err != nil {
			return "", err
		}
		if again != pass {
			return "", errors.New("les deux saisies diffèrent")
		}
	}
	return pass, nil
}

// cmdBackup : « ajean backup <dossier> ».
func cmdBackup(args []string) error {
	dir := strings.TrimSpace(os.Getenv("AJEAN_BACKUP_DIR"))
	if len(args) > 0 && args[0] != "" {
		dir = args[0]
	}
	if dir == "" {
		return errors.New("usage: ajean backup <dossier>   (ou AJEAN_BACKUP_DIR)")
	}
	pass, err := localBackupPass(true)
	if err != nil {
		return err
	}
	path, err := CreateLocalBackup(dir, pass)
	if err != nil {
		return err
	}
	size := int64(0)
	if fi, err := os.Stat(path); err == nil {
		size = fi.Size()
	}
	fmt.Printf("%s sauvegarde écrite : %s (%.1f Mo)\n", green("[ok]"), path, float64(size)/(1<<20))
	fmt.Println(dim("       sans la phrase, elle est illisible : gardez-la ailleurs que sur cette machine"))
	fmt.Println(dim("       n'y sont pas : modèles, moteurs, dossier de travail"))
	return nil
}

// cmdRestore : « ajean restore <fichier> <dossier-vide> ».
func cmdRestore(args []string) error {
	if len(args) != 2 {
		return errors.New("usage: ajean restore <fichier.ajbk> <dossier-vide>")
	}
	pass, err := localBackupPass(false)
	if err != nil {
		return err
	}
	n, err := RestoreLocalBackup(args[0], args[1], pass)
	if err != nil {
		return err
	}
	fmt.Printf("%s %d fichier(s) restauré(s) dans %s\n", green("[ok]"), n, args[1])
	fmt.Println(dim("       pour s'en servir : AJEAN_HOME=" + args[1] + " (ou recopier son contenu dans le dossier de données, service arrêté)"))
	return nil
}
