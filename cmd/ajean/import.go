package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"

	"ajean/internal/config"
	"ajean/internal/home"
	"ajean/internal/memory"
)

// importCommand : « ajean import <dossier> » reprend ce que l'ancien AJEAN
// (jusqu'à la 0.20, mode « Jean ») savait de l'utilisateur.
//
//	mémoire   profil, leçons, règles d'écriture, fiches, journal
//	fichiers  jean-workspace → WORKSPACE ; jean-scripts → WORKSPACE/anciens-scripts
//
// Rien de ce qui existe déjà ici n'est écrasé, et rien n'est modifié dans le
// dossier de l'ancien AJEAN : la reprise peut être relancée. À faire AJEAN
// arrêté : en marche, il pourrait écrire dans le journal au même moment.
//
// Ce qui ne se reprend pas : les scripts ne tournent plus (aucune commande
// système ici ; ils sont copiés pour que l'assistant puisse les refaire en
// outils), ni les tâches planifiées, ni les applications, ni le fil de la
// conversation (le journal en garde la trace).
func importCommand(args []string) error {
	files := true
	var dir string
	for _, a := range args {
		switch {
		case a == "--no-files":
			files = false
		case dir == "" && a != "" && a[0] != '-':
			dir = a
		default:
			return errors.New("usage : ajean import <dossier de l'ancien AJEAN> [--no-files]")
		}
	}
	if dir == "" {
		return errors.New("usage : ajean import <dossier de l'ancien AJEAN> [--no-files]")
	}
	mem := memory.LegacyDir(dir)
	if mem == "" {
		return fmt.Errorf("aucune mémoire de l'ancien AJEAN dans %s (attendu : son dossier de données, celui qui contient memory/_jean)", dir)
	}
	if err := config.Load(); err != nil {
		return err
	}

	rep, err := memory.ImportLegacy(mem)
	if err != nil {
		return err
	}
	fmt.Println("Mémoire reprise depuis", mem)
	fmt.Printf("  profil    %d ligne(s)\n", rep.Facts)
	fmt.Printf("  leçons    %d\n", rep.Lessons)
	fmt.Printf("  règles    %d\n", rep.Rules)
	fmt.Printf("  fiches    %d (%d déjà là)\n", rep.Fiches, rep.FichesSkipped)
	fmt.Printf("  journal   %d entrée(s)\n", rep.Entries)

	if files && mem != dir {
		for _, c := range []struct{ from, to, label string }{
			{filepath.Join(dir, "jean-workspace"), home.Workspace(), "fichiers  "},
			{filepath.Join(dir, "jean-scripts"), filepath.Join(home.Workspace(), "anciens-scripts"), "scripts   "},
		} {
			n, skipped, err := copyTree(c.from, c.to)
			if err != nil {
				return err
			}
			fmt.Printf("  %s%d copié(s), %d déjà là\n", c.label, n, skipped)
		}
	}
	for _, n := range rep.Notes {
		fmt.Println("  !", n)
	}
	fmt.Println("Non repris : tâches planifiées, applications, fil de la conversation (le journal en garde la trace).")
	fmt.Println("Les anciens scripts sont dans WORKSPACE/anciens-scripts : ils ne tournent plus tels quels, l'assistant peut les refaire en outils.")
	return nil
}

// copyTree copie un dossier dans un autre sans rien écraser : un fichier déjà
// présent est laissé tel quel. Les liens symboliques ne sont pas suivis (ils
// pourraient mener hors du dossier). Un dossier source absent ne copie rien.
func copyTree(from, to string) (copied, skipped int, err error) {
	if _, err := os.Stat(from); errors.Is(err, os.ErrNotExist) {
		return 0, 0, nil
	}
	err = filepath.WalkDir(from, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		dst := filepath.Join(to, rel)
		switch {
		case d.IsDir():
			return os.MkdirAll(dst, 0o755)
		case !d.Type().IsRegular():
			return nil
		}
		if _, err := os.Lstat(dst); err == nil {
			skipped++
			return nil
		}
		if err := copyFile(path, dst); err != nil {
			return err
		}
		copied++
		return nil
	})
	return copied, skipped, err
}

func copyFile(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(to)
		return err
	}
	if info, err := in.Stat(); err == nil {
		_ = os.Chtimes(to, info.ModTime(), info.ModTime())
	}
	return nil
}
