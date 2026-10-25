//go:build windows

package ajean

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// Tests Windows des services externes (EXTERNAL_SERVICE) : la dÃ©finition
// (services/ajean-<nom>.json), le suivi PID, et l'exclusivitÃ© GPU obtenue par
// l'ordre des appels â€” l'Ã©quivalent des garanties systemd sous Linux.

func TestExtServiceDefParsing(t *testing.T) {
	home := testHome(t)
	defsDir := filepath.Join(home, "services")
	if err := os.MkdirAll(defsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(defsDir, "ajean-strata.json")

	// pas de dÃ©finition : erreur lisible avec le chemin Ã  crÃ©er
	if _, err := loadExtServiceDef("ajean-strata"); err == nil {
		t.Fatal("dÃ©finition absente acceptÃ©e")
	}

	// dÃ©finition complÃ¨te, chemins relatifs rÃ©solus depuis le dossier services/
	good := `{"command":"bin\\serve.exe","args":["--port","8080"],"cwd":"strata"}`
	if err := os.WriteFile(p, []byte(good), 0o644); err != nil {
		t.Fatal(err)
	}
	def, err := loadExtServiceDef("ajean-strata")
	if err != nil {
		t.Fatalf("parsing : %v", err)
	}
	if def.Command != filepath.Join(defsDir, "bin", "serve.exe") {
		t.Fatalf("commande relative : %q", def.Command)
	}
	if def.Cwd != filepath.Join(defsDir, "strata") {
		t.Fatalf("cwd relatif : %q", def.Cwd)
	}
	if len(def.Args) != 2 || def.Args[1] != "8080" {
		t.Fatalf("args : %v", def.Args)
	}

	// command manquant
	bad := filepath.Join(defsDir, "ajean-bad.json")
	if err := os.WriteFile(bad, []byte(`{"args":["x"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadExtServiceDef("ajean-bad"); err == nil {
		t.Fatal("dÃ©finition sans command acceptÃ©e")
	}

	// JSON cassÃ©
	if err := os.WriteFile(bad, []byte(`{`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loadExtServiceDef("ajean-bad"); err == nil {
		t.Fatal("JSON cassÃ© acceptÃ©")
	}
}

func TestExtUnitStateTracking(t *testing.T) {
	testHome(t)
	// jamais dÃ©marrÃ© â†’ inactive, pas de PIDs
	if s := unitActiveState("ajean-strata"); s != "inactive" {
		t.Fatalf("Ã©tat initial : %q", s)
	}
	if pids := unitPIDs("ajean-strata"); pids != nil {
		t.Fatalf("pids initial : %v", pids)
	}
	if pid := extUnitPID("ajean-strata"); pid != 0 {
		t.Fatalf("pid initial : %d", pid)
	}

	// PID vivant (ce processus mÃªme) â†’ active, pids connus
	if s := unitActiveStateWithPID("ajean-strata", os.Getpid()); s != "active" {
		t.Fatalf("Ã©tat vivant : %q", s)
	}
	if pids := unitPIDs("ajean-strata"); len(pids) != 1 || pids[0] != os.Getpid() {
		t.Fatalf("pids vivants : %v", pids)
	}

	// PID mort â†’ failed (le service s'est arrÃªtÃ©)
	if s := unitActiveStateWithPID("ajean-strata", 99999999); s != "failed" {
		t.Fatalf("Ã©tat mort : %q", s)
	}
}

// unitActiveStateWithPID pose un fichier PID puis lit l'Ã©tat : le vrai
// unitActiveState dÃ©pend du PID rÃ©el, qu'on ne peut pas contrÃ´ler dans un test.
func unitActiveStateWithPID(unit string, pid int) string {
	if pid > 0 {
		if err := os.WriteFile(extPidPath(unit), []byte(strconv.Itoa(pid)), 0o644); err != nil {
			panic(err)
		}
	} else {
		_ = os.Remove(extPidPath(unit))
	}
	return unitActiveState(unit)
}

// L'exclusivitÃ© GPU est la garantie de fond : le flux de bascule (
// SwitchToPreset) arrÃªte TOUJOURS un moteur avant d'en dÃ©marrer un autre.
// Sous Linux c'est en plus Contraint par Conflicts=ajean-engine ; ici c'est
// l'ordre des appels, testÃ© Ã  travers syncExternalService comme sous Linux.
func TestWindowsExternalServiceMutex(t *testing.T) {
	testHome(t)
	home := AjeanHome()
	defsDir := filepath.Join(home, "services")
	if err := os.MkdirAll(defsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(defsDir, "ajean-strata.json")
	if err := os.WriteFile(p, []byte(`{"command":"bin\\strata.exe","args":["--port","8080"]}`), 0o644); err != nil {
		t.Fatal(err)
	}

	var calls []string
	fakeStart := func(unit string) error {
		calls = append(calls, "start "+unit)
		// le Â« process Â» est ce test mÃªme : vivant, stoppable sans douleur
		return os.WriteFile(extPidPath(unit), []byte(strconv.Itoa(os.Getpid())), 0o644)
	}
	fakeStop := func(unit string) error {
		calls = append(calls, "stop "+unit)
		return os.Remove(extPidPath(unit))
	}
	realAction := unitAction
	extUnitAction = func(unit, action string) error {
		if action == "start" {
			return fakeStart(unit)
		}
		return fakeStop(unit)
	}
	defer func() { extUnitAction = realAction }()

	steps := []struct {
		cfg  map[string]string
		want []string
	}{
		// bascule vers le preset Strata : le moteur tiers dÃ©marre
		{map[string]string{"EXTERNAL": "1", "EXTERNAL_URL": "http://127.0.0.1:8080/v1", extKeyService: "ajean-strata"},
			[]string{"start ajean-strata"}},
		// re-bascule : idempotent
		{map[string]string{"EXTERNAL": "1", "EXTERNAL_URL": "http://127.0.0.1:8080/v1", extKeyService: "ajean-strata"},
			[]string{"start ajean-strata"}},
		// bascule vers un preset local : le moteur tiers s'arrÃªte AVANT
		// que llama.cpp reparte (cette partie est testÃ©e par
		// TestSyncExternalService ; ici on vÃ©rifie juste l'arrÃªt)
		{map[string]string{"MODEL": "x.gguf"}, []string{"stop ajean-strata"}},
		{map[string]string{"MODEL": "x.gguf"}, nil},
	}
	for i, s := range steps {
		calls = nil
		if err := syncExternalService(s.cfg); err != nil {
			t.Fatalf("Ã©tape %d: %v", i, err)
		}
		if len(calls) != len(s.want) {
			t.Fatalf("Ã©tape %d: appels %v, attendu %v", i, calls, s.want)
		}
		for j := range calls {
			if calls[j] != s.want[j] {
				t.Fatalf("Ã©tape %d: appels %v, attendu %v", i, calls, s.want)
			}
		}
	}

	// aprÃ¨s arrÃªt : plus rien de suivi, Ã©tat propre
	if s := unitActiveState("ajean-strata"); s != "inactive" {
		t.Fatalf("Ã©tat aprÃ¨s arrÃªt : %q", s)
	}
}
