//go:build linux

package ajean

// Assistant root des services (Linux). AJEAN tourne sous le compte de
// l'utilisateur, et l'IA y lance des commandes : elle ne doit jamais devenir
// root. Les seules actions privilégiées passent par `ajean-svc svc-root …`, une
// copie du binaire appartenant à root, autorisée par sudoers sans mot de passe,
// et qui n'agit que sur les unités écrites par AJEAN : leur nom est toujours
// ajean-svc-<id>.service et elles sont inscrites dans sa liste
// (/etc/ajean-svc/managed.json, modifiable seulement par root) :
//   write <unité>       (texte sur l'entrée standard) écrire ou modifier une unité
//   start|stop|restart|reset-failed|enable|disable|remove <unité>
//
// `sudo ajean install-helper` installe l'assistant, la règle sudoers et le
// gardien RAM (ajean-guard.service, root).

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	rootDir        = "/etc/ajean-svc"
	rootHelperPath = "/usr/local/sbin/ajean-svc"
	sudoersPath    = "/etc/sudoers.d/ajean-svc"
	guardUnit      = "ajean-guard.service"
	maxUnitBytes   = 64 * 1024
)

// rootUnitRe : seules les unités écrites par AJEAN sont acceptées.
var rootUnitRe = regexp.MustCompile(`^ajean-svc-[a-z0-9][a-z0-9-]{0,40}\.service$`)

func rootManagedPath() string { return filepath.Join(rootDir, "managed.json") }

// protectedUnit : ce qu'il ne faut jamais toucher, sous peine de perdre l'accès
// à la machine (ou AJEAN lui-même).
func protectedUnit(name string) bool {
	for _, p := range []string{"ajean-ui", "ajean-guard", "ssh", "dbus", "systemd-", "user@", "getty", "init.scope", "cloudflared", "polkit", "networkd", "NetworkManager", "sudo"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

// --- Côté utilisateur ---------------------------------------------------------

func helperCmd(args ...string) *exec.Cmd {
	if os.Geteuid() == 0 {
		exe, _ := os.Executable()
		return exec.Command(exe, append([]string{"svc-root"}, args...)...)
	}
	helper := rootHelperPath
	if h := os.Getenv("AJEAN_ROOT_HELPER"); h != "" {
		helper = h
	}
	return exec.Command("sudo", append([]string{"-n", helper, "svc-root"}, args...)...)
}

func helperErr(out []byte, err error) error {
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if strings.Contains(msg, "a password is required") || strings.Contains(msg, "a terminal is required") || strings.Contains(msg, "command not found") || errors.Is(err, exec.ErrNotFound) {
		return errors.New("l'assistant root d'AJEAN n'est pas installé : lancer « sudo ajean install-helper »")
	}
	if msg == "" {
		msg = err.Error()
	}
	return errors.New(msg)
}

func rootRun(op, unit string) error {
	out, err := helperCmd(op, unit).CombinedOutput()
	return helperErr(out, err)
}

func rootWriteUnit(unit, text string) error {
	cmd := helperCmd("write", unit)
	cmd.Stdin = strings.NewReader(text)
	out, err := cmd.CombinedOutput()
	return helperErr(out, err)
}

// rootIsManaged : lecture de la liste root (lisible par tous).
func rootIsManaged(unit string) bool {
	for _, u := range rootLoadManaged() {
		if u == unit {
			return true
		}
	}
	return false
}

// rootLoadManaged : la liste, réduite aux unités écrites par AJEAN.
func rootLoadManaged() []string {
	var list []string
	if b, err := os.ReadFile(rootManagedPath()); err == nil {
		_ = json.Unmarshal(b, &list)
	}
	out := []string{}
	for _, u := range list {
		if rootUnitRe.MatchString(u) {
			out = append(out, u)
		}
	}
	return out
}

// --- Côté root ---------------------------------------------------------------

var rootMu sync.Mutex

func rootSaveManaged(list []string) error {
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(list, "", "  ")
	tmp := rootManagedPath() + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, rootManagedPath())
}

func rootSetManaged(unit string, on bool) error {
	rootMu.Lock()
	defer rootMu.Unlock()
	list := rootLoadManaged()
	out := list[:0]
	for _, u := range list {
		if u != unit {
			out = append(out, u)
		}
	}
	if on {
		out = append(out, unit)
	}
	return rootSaveManaged(out)
}

func rootSystemctl(args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemctl", args...).CombinedOutput()
	if err != nil {
		if m := strings.TrimSpace(string(out)); m != "" {
			return errors.New(m)
		}
	}
	return err
}

// checkNewUnit : une unité créée par AJEAN ne peut pas donner root.
// callerIsRoot : l'assistant a été lancé par un process déjà root (AJEAN
// installé et lancé en root), pas par un compte normal via sudo. La règle
// « jamais root » n'empêche alors aucune élévation (l'IA tourne déjà en root) et
// bloquerait tous les modèles : comme l'ancien moteur, ils tournent en root.
func callerIsRoot() bool {
	su := os.Getenv("SUDO_USER")
	return su == "" || su == "root"
}

func checkNewUnit(text string) error {
	l := unitLines(text)
	u, _ := unitGetKey(l, "[Service]", "User")
	if (u == "" || u == "root" || u == "0") && !callerIsRoot() {
		return errors.New("une unité créée par AJEAN doit avoir un utilisateur (pas root)")
	}
	for _, line := range l {
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if strings.HasPrefix(k, "Exec") && (strings.HasPrefix(v, "+") || strings.HasPrefix(v, "!")) {
			return errors.New("commande privilégiée refusée dans une unité créée par AJEAN")
		}
		switch k {
		case "PermissionsStartOnly", "AmbientCapabilities", "CapabilityBoundingSet", "Group", "SupplementaryGroups", "DynamicUser":
			return fmt.Errorf("option %s refusée dans une unité créée par AJEAN", k)
		}
	}
	return nil
}

// unitLines découpe le fichier en lignes logiques (les suites « \ » sont recollées).
func unitLines(text string) []string {
	var out []string
	cur := ""
	for _, l := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		if strings.HasSuffix(l, "\\") {
			cur += strings.TrimSuffix(l, "\\") + " "
			continue
		}
		out = append(out, cur+l)
		cur = ""
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}

func unitSectionOf(lines []string, i int) string {
	for ; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			return t
		}
	}
	return ""
}

func unitGetKey(lines []string, section, key string) (string, int) {
	for i, l := range lines {
		k, v, ok := strings.Cut(l, "=")
		if ok && strings.TrimSpace(k) == key && unitSectionOf(lines, i) == section {
			return strings.TrimSpace(v), i
		}
	}
	return "", -1
}

func rootWriteLike(path string, content []byte, mode os.FileMode) error {
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".ajean-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err = f.Write(content); err == nil {
		err = f.Chmod(mode)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func rootBackup(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	dir := filepath.Join(rootDir, "sauvegardes")
	_ = os.MkdirAll(dir, 0o700)
	_ = os.WriteFile(filepath.Join(dir, filepath.Base(path)+"."+time.Now().Format("20060102-150405")), b, 0o600)
}

func rootWrite(unit string, text []byte) error {
	path := filepath.Join(systemUnitDir, unit)
	old, oldErr := os.ReadFile(path)
	isNew := oldErr != nil
	if err := checkNewUnit(string(text)); err != nil {
		return err
	}
	if !isNew {
		rootBackup(path)
	}
	if err := rootWriteLike(path, text, 0o644); err != nil {
		return err
	}
	// Un fichier que systemd refuse est annulé tout de suite.
	err := rootSystemctl("daemon-reload")
	state := ""
	if err == nil {
		out, _ := exec.Command("systemctl", "show", "-p", "LoadState", "--value", "--", unit).Output()
		state = strings.TrimSpace(string(out))
	}
	if err != nil || state != "loaded" {
		if isNew {
			os.Remove(path)
		} else {
			_ = rootWriteLike(path, old, 0o644)
		}
		_ = rootSystemctl("daemon-reload")
		return errors.New("modification refusée par systemd, l'ancienne version est conservée")
	}
	return rootSetManaged(unit, true)
}

func rootRemove(unit string) error {
	path := filepath.Join(systemUnitDir, unit)
	_ = rootSystemctl("stop", "--", unit)
	_ = rootSystemctl("disable", "--", unit)
	if _, err := os.Stat(path); err == nil {
		dir := filepath.Join(rootDir, "supprimes")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		if err := os.Rename(path, filepath.Join(dir, unit+"."+time.Now().Format("20060102-150405"))); err != nil {
			return err
		}
	}
	_ = rootSystemctl("daemon-reload")
	_ = rootSystemctl("reset-failed", "--", unit)
	return rootSetManaged(unit, false)
}

// cmdSvcRoot : point d'entrée de l'assistant (exige root).
func cmdSvcRoot(args []string) error {
	if os.Geteuid() != 0 {
		return errors.New("svc-root : réservé à root (via sudo)")
	}
	if len(args) == 2 && args[0] == "clean" {
		return rootCleanOp(args[1])
	}
	if len(args) == 1 && args[0] == "version" {
		fmt.Println(helperProto)
		return nil
	}
	if len(args) != 2 || !rootUnitRe.MatchString(args[1]) {
		return errors.New("usage : svc-root <action> ajean-svc-<id>.service")
	}
	op, unit := args[0], args[1]
	switch op {
	case "write":
		text, err := io.ReadAll(io.LimitReader(os.Stdin, maxUnitBytes+1))
		if err != nil {
			return err
		}
		if len(text) > maxUnitBytes || !bytes.Contains(text, []byte("[Service]")) {
			return errors.New("unité invalide")
		}
		return rootWrite(unit, text)
	case "remove":
		return rootRemove(unit)
	}
	if !rootIsManaged(unit) {
		return errors.New("service non géré par AJEAN")
	}
	switch op {
	case "start", "stop", "restart", "reset-failed", "enable", "disable":
		return rootSystemctl(op, "--", unit)
	}
	return fmt.Errorf("action inconnue : %s", op)
}

// helperProto : version du dialogue AJEAN ↔ assistant root. À monter quand
// l'assistant gagne une action dont AJEAN a besoin : un assistant plus ancien
// (copie figée le jour de son installation) est alors signalé, avec la commande
// pour le réinstaller. Jamais de mise à jour automatique : l'assistant devrait
// recopier en root un binaire que le compte de l'utilisateur (et donc l'IA)
// peut modifier.
const helperProto = 1

// helperProtoInstalled : version annoncée par l'assistant installé (0 = trop
// ancien pour le dire, -1 = absent ou injoignable).
func helperProtoInstalled() int {
	if os.Geteuid() == 0 {
		return helperProto
	}
	if !svcIsFile(rootHelperPath) {
		return -1
	}
	out, err := helperCmd("version").CombinedOutput()
	if err != nil {
		if strings.Contains(string(out), "usage") {
			return 0 // assistant d'avant l'action version
		}
		return -1
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0
	}
	return n
}

var helperUserRe = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// cmdInstallHelper : `sudo ajean install-helper [utilisateur]`.
func cmdInstallHelper(args []string) error {
	if os.Geteuid() != 0 {
		return errors.New("à lancer avec sudo : sudo ajean install-helper")
	}
	user := os.Getenv("SUDO_USER")
	if len(args) > 0 {
		user = args[0]
	}
	if user == "" || user == "root" || !helperUserRe.MatchString(user) {
		return errors.New("utilisateur introuvable : sudo ajean install-helper <utilisateur>")
	}
	return installRootHelper(user)
}

// installRootHelper pose l'assistant root, la règle sudoers de `user` et le
// gardien RAM. Appelé en root par `ajean install` et `ajean install-helper`.
// `user` = root : AJEAN tourne déjà en root et appelle l'assistant sans sudo,
// aucune règle sudoers n'est écrite.
func installRootHelper(user string) error {
	if user != "root" && !helperUserRe.MatchString(user) {
		return fmt.Errorf("nom d'utilisateur refusé pour la règle sudoers : %q", user)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	b, err := os.ReadFile(exe)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(rootHelperPath), 0o755); err != nil {
		return err
	}
	if err := rootWriteLike(rootHelperPath, b, 0o755); err != nil {
		return err
	}
	_ = os.Chown(rootHelperPath, 0, 0)
	_ = os.Chmod(rootHelperPath, 0o755)
	fmt.Printf("  %s assistant root : %s\n", green("✓"), rootHelperPath)
	if user != "root" {
		rule := fmt.Sprintf("# AJEAN : actions sur les services gérés, sans mot de passe.\n%s ALL=(root) NOPASSWD: %s svc-root *\n", user, rootHelperPath)
		tmp := sudoersPath + ".tmp"
		if err := os.WriteFile(tmp, []byte(rule), 0o440); err != nil {
			return err
		}
		if out, err := exec.Command("visudo", "-cf", tmp).CombinedOutput(); err != nil {
			os.Remove(tmp)
			return fmt.Errorf("règle sudoers refusée : %s", strings.TrimSpace(string(out)))
		}
		if err := os.Rename(tmp, sudoersPath); err != nil {
			return err
		}
		fmt.Printf("  %s règle sudoers : %s (%s)\n", green("✓"), sudoersPath, user)
	}
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return err
	}
	if _, err := os.Stat(rootManagedPath()); err != nil {
		_ = rootSaveManaged([]string{})
	}
	guard := fmt.Sprintf("[Unit]\nDescription=AJEAN, gardien RAM des services\nAfter=network.target\n\n[Service]\nExecStart=%s guard\nOOMScoreAdjust=-1000\nRestart=always\nRestartSec=2\n\n[Install]\nWantedBy=multi-user.target\n", rootHelperPath)
	if err := os.WriteFile(filepath.Join(systemUnitDir, guardUnit), []byte(guard), 0o644); err != nil {
		return err
	}
	_ = rootSystemctl("daemon-reload")
	removeLegacyEngineRoot()
	_ = rootSystemctl("enable", "--", guardUnit)
	_ = rootSystemctl("restart", "--", guardUnit)
	fmt.Printf("  %s gardien RAM : %s\n", green("✓"), guardUnit)
	return nil
}

// uninstallRootHelper retire le gardien RAM, l'assistant et sa règle sudoers.
// Les unités des modèles ne sont pas touchées : ce sont des services de la
// machine, et la liste gérée reste en place pour une réinstallation.
func uninstallRootHelper() {
	_ = rootSystemctl("stop", "--", guardUnit)
	_ = rootSystemctl("disable", "--", guardUnit)
	for _, p := range []string{filepath.Join(systemUnitDir, guardUnit), sudoersPath, rootHelperPath} {
		if err := os.Remove(p); err == nil {
			fmt.Printf("  %s %s\n", green("✓"), p)
		}
	}
}

// rootCleanOp : nettoyages du disque qui demandent root.
func rootCleanOp(what string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch what {
	case "apt":
		return exec.CommandContext(ctx, "apt-get", "clean").Run()
	case "journal":
		return exec.CommandContext(ctx, "journalctl", "--vacuum-size=200M").Run()
	case "backups":
		for _, d := range []string{"sauvegardes", "supprimes"} {
			p := filepath.Join(rootDir, d)
			ents, _ := os.ReadDir(p)
			for _, e := range ents {
				_ = os.RemoveAll(filepath.Join(p, e.Name()))
			}
		}
		return nil
	}
	return fmt.Errorf("nettoyage inconnu : %s", what)
}
