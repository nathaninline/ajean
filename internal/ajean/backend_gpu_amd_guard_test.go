package ajean

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeAmdSmi installe un faux amd-smi dans un PATH isolé qui note chaque appel
// dans calls.log et renvoie un JSON minimal (ou échoue si fail).
func fakeAmdSmi(t *testing.T, fail bool) (logPath string) {
	dir := t.TempDir()
	logPath = filepath.Join(dir, "calls.log")
	static := `[{"gpu":0,"asic":{"market_name":"Radeon Test"}}]`
	metric := `[{"gpu":0,"mem_usage":{"total_vram":{"value":16384,"unit":"MB"},"used_vram":{"value":2048,"unit":"MB"}},"usage":{"gfx_activity":{"value":42,"unit":"%"}},"temperature":{"edge":{"value":55,"unit":"C"}}}]`
	if runtime.GOOS == "windows" {
		body := "@echo off\r\necho %1>>\"" + logPath + "\"\r\n"
		if fail {
			body += "exit /b 1\r\n"
		} else {
			body += "if \"%1\"==\"static\" echo " + static + "\r\nif \"%1\"==\"metric\" echo " + metric + "\r\n"
		}
		os.WriteFile(filepath.Join(dir, "amd-smi.bat"), []byte(body), 0o755)
	} else {
		body := "#!/bin/sh\necho \"$1\" >> '" + logPath + "'\n"
		if fail {
			body += "exit 1\n"
		} else {
			body += "[ \"$1\" = static ] && echo '" + static + "'\n[ \"$1\" = metric ] && echo '" + metric + "'\nexit 0\n"
		}
		os.WriteFile(filepath.Join(dir, "amd-smi"), []byte(body), 0o755)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	amdMu.Lock()
	amdNames, amdRetryAt = nil, time.Time{}
	amdMu.Unlock()
	return logPath
}

func calls(p string) []string {
	b, _ := os.ReadFile(p)
	return strings.Fields(string(b))
}

// Issue #259 : sous Windows, amd-smi ne doit JAMAIS être lancé.
func TestAmdSmiNeverOnWindows(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows uniquement")
	}
	log := fakeAmdSmi(t, false)
	for i := 0; i < 3; i++ {
		amdVramGPUs()
		rocmVramGPUs()
		sampleVramGPUs()
	}
	if c := calls(log); len(c) != 0 {
		t.Fatalf("amd-smi lancé sous Windows : %v", c)
	}
}

// Linux : mêmes données qu'avant, noms lus une seule fois.
func TestAmdSmiLinuxDataAndCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hors Windows")
	}
	log := fakeAmdSmi(t, false)
	var g []map[string]any
	for i := 0; i < 3; i++ {
		g = amdVramGPUs()
	}
	if len(g) != 1 || g[0]["name"] != "Radeon Test" || g[0]["total"] != 16384 ||
		g[0]["used"] != 2048 || g[0]["util"] != 42 || g[0]["temp"] != 55 {
		t.Fatalf("données inattendues : %v", g)
	}
	if got := strings.Join(calls(log), ","); got != "static,metric,metric,metric" {
		t.Fatalf("appels : %s", got)
	}
}

// Linux : un échec ne relance pas amd-smi à chaque tick, mais on réessaie après la pause.
func TestAmdSmiLinuxBackoff(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("hors Windows")
	}
	log := fakeAmdSmi(t, true)
	amdVramGPUs()
	n := len(calls(log))
	amdVramGPUs()
	amdVramGPUs()
	if len(calls(log)) != n {
		t.Fatalf("relancé pendant la pause : %v", calls(log))
	}
	amdMu.Lock()
	amdRetryAt = time.Now().Add(-time.Second)
	amdMu.Unlock()
	amdVramGPUs()
	if len(calls(log)) == n {
		t.Fatal("pas de nouvel essai après la pause")
	}
}

func TestGpuTelemetryOff(t *testing.T) {
	log := fakeAmdSmi(t, false)
	t.Setenv("AJEAN_GPU_TELEMETRY", "off")
	if g := sampleVramGPUs(); len(g) != 0 {
		t.Fatalf("télémétrie non coupée : %v", g)
	}
	if c := calls(log); len(c) != 0 {
		t.Fatalf("amd-smi lancé malgré off : %v", c)
	}
}
