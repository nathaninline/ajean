package ajean

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Migration complète de l'agencement système jean → ajean (Unix).
//
// Renommer le dossier de données ne suffit pas sur une machine installée : le
// chemin est aussi inscrit dans /etc/default/jean et dans l'unité systemd. Les
// trois doivent bouger ensemble, sinon on obtient le pire des cas — des données
// déplacées et un service qui ne redémarre plus.
//
// QUAND. Jamais au démarrage ordinaire : un service qui réécrit des unités
// systemd à chaque boot finirait par en casser une, et sur un parc entier ça
// veut dire des machines qui ne redémarrent plus. Uniquement à deux moments
// délibérés et déjà privilégiés : `ajean install`, et juste après une mise à
// jour réussie — le service est de toute façon sur le point d'être relancé.
//
// CE QU'ON S'AUTORISE À DÉPLACER. Un chemin inscrit à la main est un ordre. On
// ne migre donc QUE si le chemin actuel est exactement l'ancien emplacement par
// défaut, celui que notre propre installeur a écrit. Un utilisateur qui a pointé
// JEAN_HOME vers son gros disque garde son choix, intact et silencieux.

// layoutPlan décrit une migration d'agencement. Les chemins sont des champs
// plutôt que des constantes pour que la logique soit testable sur un faux
// système de fichiers, sans toucher au vrai /etc.
type layoutPlan struct {
	Old        string   // /etc/jean
	New        string   // /etc/ajean
	DefaultDir string   // /etc/default
	UnitDir    string   // /etc/systemd/system
	Units      []string // unités à arrêter puis relancer
	BackupDir  string   // où déposer l'archive de sécurité

	// stopUnit / startUnit / reloadUnits sont injectables pour les tests.
	stopUnit    func(string) error
	startUnit   func(string) error
	unitActive  func(string) bool
	reloadUnits func() error
	isRoot      func() bool
	backup      func(layoutPlan) error
}

func defaultLayoutPlan() layoutPlan {
	return layoutPlan{
		Old:        legacyDefaultHome(),
		New:        defaultAjeanHome(),
		DefaultDir: "/etc/default",
		UnitDir:    "/etc/systemd/system",
		Units:      []string{serviceName(), linkServiceName()},
		BackupDir:  "/root",
		stopUnit:   func(u string) error { return exec.Command("systemctl", "stop", u).Run() },
		startUnit:  func(u string) error { return exec.Command("systemctl", "start", u).Run() },
		unitActive: func(u string) bool {
			out, _ := exec.Command("systemctl", "is-active", u).Output()
			return strings.TrimSpace(string(out)) == "active"
		},
		reloadUnits: func() error { return exec.Command("systemctl", "daemon-reload").Run() },
		isRoot:      func() bool { return os.Geteuid() == 0 },
		backup:      backupLayout,
	}
}

// updateLayoutPlan est la variante utilisée juste après une mise à jour, quand
// on s'exécute DANS le service de lien. Elle n'arrête et ne relance rien.
//
// Deux raisons, et la première est fatale :
//
//  1. Arrêter jean-link depuis jean-link, c'est se tuer soi-même au milieu de la
//     migration — dossier déplacé, chemins pas encore réécrits, machine cassée.
//  2. Arrêter le service d'inférence rechargerait un modèle de plusieurs
//     dizaines de Go, ce qui n'a rien à faire derrière un bouton « mettre à jour ».
//
// C'est sans danger : sous Linux un rename de dossier fonctionne même avec des
// fichiers ouverts, et un process en cours garde son répertoire de travail par
// son inode. L'unité est réécrite dans la foulée, donc le prochain redémarrage —
// celui de jean-link, immédiat, puis celui du service d'inférence, plus tard —
// repart sur les bons chemins.
func updateLayoutPlan() layoutPlan {
	p := defaultLayoutPlan()
	p.unitActive = func(string) bool { return false }
	return p
}

// pinnedHome lit le chemin impose par /etc/default/ajean ou /etc/default/jean.
// Renvoie ("", "") si aucun des deux ne fixe de valeur.
func (p layoutPlan) pinnedHome() (file, value string) {
	for _, name := range []string{"ajean", "jean"} {
		f := filepath.Join(p.DefaultDir, name)
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		for _, line := range strings.Split(string(b), "\n") {
			s := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
			if s == "" || strings.HasPrefix(s, "#") {
				continue
			}
			k, v, ok := strings.Cut(s, "=")
			if !ok {
				continue
			}
			if k = strings.TrimSpace(k); k == "AJEAN_HOME" || k == "JEAN_HOME" {
				return f, strings.Trim(strings.TrimSpace(v), "\"'")
			}
		}
	}
	return "", ""
}

// layoutNeedsMigration dit si une migration est possible ET legitime, avec la
// raison quand elle ne l'est pas (affichée telle quelle par `ajean install`).
func (p layoutPlan) layoutNeedsMigration() (bool, string) {
	if !isDir(p.Old) {
		return false, "" // rien à migrer : machine neuve ou déjà migrée
	}
	if isDir(p.New) {
		return false, fmt.Sprintf("%s et %s existent tous les deux — migration à faire à la main", p.Old, p.New)
	}
	// Le point délicat : distinguer notre artefact d'un choix de l'utilisateur.
	if file, value := p.pinnedHome(); value != "" && value != p.Old {
		return false, fmt.Sprintf("%s fixe le dossier à %s — chemin choisi explicitement, laissé intact", file, value)
	}
	return true, ""
}

// migrateLayout exécute la migration complète. Renvoie nil sans rien faire
// quand il n'y a rien à migrer. En cas d'échec après le déplacement, remet la
// machine dans son état initial et relance ce qui tournait.
func migrateLayout(p layoutPlan) error {
	ok, why := p.layoutNeedsMigration()
	if !ok {
		if why != "" {
			fmt.Printf("%s %s\n", dim("[info]"), why)
		}
		return nil
	}
	if !p.isRoot() {
		fmt.Printf("%s dossier de données encore en %s — relance en root pour le migrer\n", yellow("[info]"), p.Old)
		return nil
	}

	fmt.Printf("%s migration de l'agencement %s → %s\n", cyan("[migration]"), p.Old, p.New)

	if err := p.backup(p); err != nil {
		return fmt.Errorf("sauvegarde impossible, migration annulée : %w", err)
	}

	var stopped []string
	for _, u := range p.Units {
		if p.unitActive(u) {
			_ = p.stopUnit(u)
			stopped = append(stopped, u)
			fmt.Printf("  %s arrêté %s\n", green("✓"), u)
		}
	}
	restart := func() {
		for _, u := range stopped {
			_ = p.startUnit(u)
		}
	}

	// Déplacement atomique : même volume, donc instantané même avec 200 Go de
	// modèles, et jamais un état où les données sont à moitié quelque part.
	if err := os.Rename(p.Old, p.New); err != nil {
		restart()
		return fmt.Errorf("déplacement impossible (%w) — rien n'a été modifié", err)
	}

	// À partir d'ici, toute erreur doit rendre la machine à son état d'origine.
	rollback := func(cause error) error {
		_ = os.Rename(p.New, p.Old)
		restart()
		return fmt.Errorf("%w — état initial restauré", cause)
	}

	if err := rewriteTree(p.New, p.Old, p.New); err != nil {
		return rollback(err)
	}
	if err := writeDefaults(p); err != nil {
		return rollback(err)
	}
	if err := rewriteUnits(p); err != nil {
		return rollback(err)
	}
	_ = p.reloadUnits()
	restart()
	fmt.Printf("  %s dossier de données : %s\n", green("✓"), p.New)
	return nil
}

// backupLayout archive les petits fichiers (config, presets, mémoire, clés).
// Les .gguf et backends/ sont exclus : des dizaines de Go qui se retéléchargent,
// et qui ne sont de toute façon pas menacés — ils ne font que changer de nom de
// dossier parent.
func backupLayout(p layoutPlan) error {
	if _, err := exec.LookPath("tar"); err != nil {
		return err
	}
	if err := os.MkdirAll(p.BackupDir, 0o755); err != nil {
		return err
	}
	dst := filepath.Join(p.BackupDir, "ajean-sauvegarde-"+time.Now().Format("20060102-150405")+".tar.gz")
	cmd := exec.Command("tar", "czf", dst, "-C", p.Old,
		"--exclude=./backends", "--exclude=*.gguf", "--exclude=*.gguf.part", "--exclude=*.log", ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v: %s", err, strings.TrimSpace(string(out)))
	}
	fmt.Printf("  %s sauvegarde %s\n", green("✓"), dst)
	return nil
}

// rewriteTree remplace toute mention de oldPath par newPath dans les fichiers
// texte de root. On PARCOURT au lieu de deviner une liste : l'expérience montre
// que les chemins absolus se cachent partout — EXTRA_ARGS d'un preset, copies
// .bak, notes de MEMORY. Les .gguf et backends/ sont sautés (aucun chemin à y
// trouver, et les lire prendrait des heures).
func rewriteTree(root, oldPath, newPath string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // un fichier illisible ne doit pas faire échouer la migration
		}
		if info.IsDir() {
			if info.Name() == "backends" {
				return filepath.SkipDir
			}
			return nil
		}
		switch {
		case strings.HasSuffix(path, ".gguf"), strings.HasSuffix(path, ".gguf.part"):
			return nil
		case info.Size() > 4<<20: // au-delà, ce n'est plus de la configuration
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil || !strings.Contains(string(b), oldPath) {
			return nil
		}
		out := strings.ReplaceAll(string(b), oldPath, newPath)
		mode := info.Mode().Perm()
		if err := os.WriteFile(path, []byte(out), mode); err != nil {
			return fmt.Errorf("réécriture de %s : %w", path, err)
		}
		fmt.Printf("  %s chemins mis à jour : %s\n", green("✓"), strings.TrimPrefix(path, root+string(os.PathSeparator)))
		return nil
	})
}

// writeDefaults pose /etc/default/ajean et repointe l'ancien fichier au lieu de
// le supprimer : des scripts d'utilisateurs le sourcent pour lire $JEAN_HOME.
func writeDefaults(p layoutPlan) error {
	if err := os.MkdirAll(p.DefaultDir, 0o755); err != nil {
		return err
	}
	body := fmt.Sprintf("# Généré par ajean — racine des données\nAJEAN_HOME=%s\nJEAN_HOME=%s\n", p.New, p.New)
	if err := os.WriteFile(filepath.Join(p.DefaultDir, "ajean"), []byte(body), 0o644); err != nil {
		return err
	}
	legacy := filepath.Join(p.DefaultDir, "jean")
	if b, err := os.ReadFile(legacy); err == nil {
		if out := strings.ReplaceAll(string(b), p.Old, p.New); out != string(b) {
			if err := os.WriteFile(legacy, []byte(out), 0o644); err != nil {
				return err
			}
		}
	}
	return nil
}

// rewriteUnits corrige les chemins DANS les unités, sans les renommer. Renommer
// imposerait un disable/enable et une fenêtre où plus rien n'est actif, pour un
// gain invisible : le code sait déjà utiliser l'unité réellement installée.
func rewriteUnits(p layoutPlan) error {
	entries, err := filepath.Glob(filepath.Join(p.UnitDir, "*.service"))
	if err != nil {
		return err
	}
	for _, unit := range entries {
		b, err := os.ReadFile(unit)
		if err != nil || !strings.Contains(string(b), p.Old) {
			continue
		}
		if err := os.WriteFile(unit+".avant-ajean", b, 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(unit, []byte(strings.ReplaceAll(string(b), p.Old, p.New)), 0o644); err != nil {
			return err
		}
		fmt.Printf("  %s unité corrigée : %s\n", green("✓"), filepath.Base(unit))
	}
	return nil
}
