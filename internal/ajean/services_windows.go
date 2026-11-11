//go:build windows

package ajean

// Services sous Windows : vrais services SCM. Un programme quelconque ne peut
// pas être un service Windows, donc le binaire AJEAN sert d'enveloppe
// (`ajean service host <id>`, à la nssm) : il lance la commande, la relance si
// elle plante (3 fois en 5 min au plus), écrit le journal dans un fichier et
// tue toute l'arborescence à l'arrêt. La création demande les droits admin une
// fois (UAC) ; la sécurité du service autorise ensuite les utilisateurs
// connectés à le démarrer et l'arrêter sans élévation.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// Démarrer / arrêter / interroger : accordé aux utilisateurs authentifiés (AU).
const svcSDDL = "D:(A;;CCLCSWRPWPDTLOCRRC;;;SY)(A;;CCDCLCSWRPWPDTLOCRSDRCWDWO;;;BA)(A;;CCLCSWRPWPLOCRRC;;;AU)"

func svcIsElevated() bool { return windows.GetCurrentProcessToken().IsElevated() }

// svcInstall crée ou met à jour le service SCM (élévation si nécessaire).
func osSvcInstall(s *Service) error {
	if !svcIsElevated() {
		return runElevated("service", "install", s.ID, "--home", AjeanHome())
	}
	return osSvcInstallElevated(s)
}

func osSvcInstallElevated(s *Service) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	name := ServiceUnitName(s)
	cfg := mgr.Config{
		DisplayName: "AJEAN " + s.Name,
		Description: "Moteur d'IA lancé par AJEAN (services.json)",
		StartType:   map[bool]uint32{true: mgr.StartAutomatic, false: mgr.StartManual}[s.AutostartWanted],
	}
	args := []string{"service", "host", s.ID, "--home", AjeanHome()}
	if ex, err := m.OpenService(name); err == nil {
		defer ex.Close()
		cfg.BinaryPathName = windows.EscapeArg(exe) + " " + joinWinArgs(args)
		cfg.ServiceType = windows.SERVICE_WIN32_OWN_PROCESS
		cfg.ErrorControl = mgr.ErrorNormal
		if err := ex.UpdateConfig(cfg); err != nil {
			return err
		}
	} else {
		sv, err := m.CreateService(name, exe, cfg, args...)
		if err != nil {
			return err
		}
		sv.Close()
	}
	if out, err := hideCmd(exec.Command("sc.exe", "sdset", name, svcSDDL)).CombinedOutput(); err != nil {
		return fmt.Errorf("droits du service : %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func joinWinArgs(a []string) string {
	q := make([]string, len(a))
	for i, x := range a {
		q[i] = windows.EscapeArg(x)
	}
	return strings.Join(q, " ")
}

func osSvcUninstall(s *Service) error {
	if !svcIsElevated() {
		return runElevated("service", "uninstall-unit", s.ID, "--home", AjeanHome())
	}
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	sv, err := m.OpenService(ServiceUnitName(s))
	if err != nil {
		return nil
	}
	defer sv.Close()
	return sv.Delete()
}

// runElevated relance AJEAN en administrateur (invite UAC) et attend la fin.
func runElevated(args ...string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	q := make([]string, len(args))
	for i, a := range args {
		q[i] = "'" + strings.ReplaceAll(windows.EscapeArg(a), "'", "''") + "'"
	}
	ps := fmt.Sprintf("$p = Start-Process -FilePath '%s' -ArgumentList %s -Verb RunAs -Wait -PassThru -WindowStyle Hidden; exit $p.ExitCode",
		strings.ReplaceAll(exe, "'", "''"), strings.Join(q, ","))
	out, err := hideCmd(exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", ps)).CombinedOutput()
	if err != nil {
		return fmt.Errorf("élévation refusée ou échouée : %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// openSvc ouvre le service avec les seuls droits accordés aux utilisateurs.
func openSvc(name string) (*mgr.Service, func(), error) {
	h, err := windows.OpenSCManager(nil, nil, windows.SC_MANAGER_CONNECT)
	if err != nil {
		return nil, nil, err
	}
	n, _ := windows.UTF16PtrFromString(name)
	sh, err := windows.OpenService(h, n, windows.SERVICE_START|windows.SERVICE_STOP|windows.SERVICE_QUERY_STATUS)
	if err != nil {
		windows.CloseServiceHandle(h)
		return nil, nil, err
	}
	sv := &mgr.Service{Name: name, Handle: sh}
	return sv, func() { windows.CloseServiceHandle(sh); windows.CloseServiceHandle(h) }, nil
}

func osSvcStart(s *Service) error {
	sv, done, err := openSvc(ServiceUnitName(s))
	if err != nil {
		return err
	}
	defer done()
	return sv.Start()
}

func osSvcStop(s *Service) error {
	sv, done, err := openSvc(ServiceUnitName(s))
	if err != nil {
		return nil // pas installé : déjà arrêté
	}
	defer done()
	st, err := sv.Query()
	if err != nil || st.State == svc.Stopped {
		return err
	}
	if _, err := sv.Control(svc.Stop); err != nil {
		return err
	}
	for i := 0; i < 60; i++ {
		if st, err := sv.Query(); err != nil || st.State == svc.Stopped {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("le service ne s'arrête pas")
}

func osSvcState(s *Service) string {
	sv, done, err := openSvc(ServiceUnitName(s))
	if err != nil {
		return "inactive"
	}
	defer done()
	st, err := sv.Query()
	if err != nil {
		return "unknown"
	}
	switch st.State {
	case svc.Running:
		return "active"
	case svc.StartPending, svc.ContinuePending:
		return "activating"
	case svc.Stopped:
		if svcIsFile(svcFailMarker(s)) {
			return "failed"
		}
	}
	return "inactive"
}

func svcFailMarker(s *Service) string { return svcLogPath(s) + ".failed" }

func osSvcLogTail(s *Service, n int) string {
	b, err := os.ReadFile(svcLogPath(s))
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.TrimRight(string(b), "\r\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// --- Enveloppe : `ajean service host <id>` ----------------------------------

type svcHost struct{ s Service }

func cmdServiceHost(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage : ajean service host <id>")
	}
	s, err := GetService(args[0])
	if err != nil {
		return err
	}
	return svc.Run(ServiceUnitName(&s), &svcHost{s: s})
}

func (h *svcHost) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	s := &h.s
	_ = os.MkdirAll(filepath.Dir(svcLogPath(s)), 0o755)
	_ = os.Remove(svcFailMarker(s))
	job, _ := windows.CreateJobObject(nil, nil)
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	_, _ = windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info)))

	var (
		mu       sync.Mutex
		cur      *exec.Cmd
		stopping bool
		exited   = make(chan error, 1)
		crashes  []time.Time
	)
	launch := func() error {
		logf := openRotatedLog(svcLogPath(s))
		av := s.argv()
		cmd := exec.Command(av[0], av[1:]...)
		cmd.Dir = s.Workdir
		cmd.Env = os.Environ()
		keys := make([]string, 0, len(s.Env))
		for k := range s.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			cmd.Env = append(cmd.Env, k+"="+s.Env[k])
		}
		cmd.Stdout, cmd.Stderr = logf, logf
		hideCmd(cmd)
		if err := cmd.Start(); err != nil {
			fmt.Fprintf(logf, "[ajean] lancement impossible : %v\n", err)
			logf.Close()
			return err
		}
		if ph, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid)); err == nil {
			_ = windows.AssignProcessToJobObject(job, ph)
			windows.CloseHandle(ph)
		}
		mu.Lock()
		cur = cmd
		mu.Unlock()
		go func() { err := cmd.Wait(); logf.Close(); exited <- err }()
		return nil
	}
	if err := launch(); err != nil {
		_ = os.WriteFile(svcFailMarker(s), []byte(err.Error()), 0o644)
		return true, 1
	}
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				mu.Lock()
				stopping = true
				if cur != nil && cur.Process != nil {
					_ = cur.Process.Kill()
				}
				mu.Unlock()
				windows.CloseHandle(job) // tue toute l'arborescence
				return false, 0
			}
		case err := <-exited:
			mu.Lock()
			st := stopping
			mu.Unlock()
			if st {
				return false, 0
			}
			now := time.Now()
			recent := crashes[:0]
			for _, t := range crashes {
				if now.Sub(t) < 5*time.Minute {
					recent = append(recent, t)
				}
			}
			crashes = append(recent, now)
			if len(crashes) > 3 {
				msg := fmt.Sprintf("plantages répétés, service arrêté (dernier : %v)", err)
				appendLog(svcLogPath(s), "[ajean] "+msg)
				_ = os.WriteFile(svcFailMarker(s), []byte(msg), 0o644)
				windows.CloseHandle(job)
				return true, 1
			}
			appendLog(svcLogPath(s), fmt.Sprintf("[ajean] le programme s'est arrêté (%v), relance dans 5 s", err))
			time.Sleep(5 * time.Second)
			if err := launch(); err != nil {
				_ = os.WriteFile(svcFailMarker(s), []byte(err.Error()), 0o644)
				return true, 1
			}
		}
	}
}

// openRotatedLog : journal en ajout, renommé en .1 au-delà de 20 Mo.
func openRotatedLog(path string) *os.File {
	if fi, err := os.Stat(path); err == nil && fi.Size() > 20<<20 {
		_ = os.Rename(path, path+".1")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		f, _ = os.Open(os.DevNull)
	}
	return f
}

func appendLog(path, line string) {
	f := openRotatedLog(path)
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), line)
}

func osSvcInstallElevatedOrDirect(s *Service) error { return osSvcInstallElevated(s) }
