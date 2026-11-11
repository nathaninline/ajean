//go:build linux

package ajean

import (
	"strings"
	"testing"
)

func TestSystemdUnitText(t *testing.T) {
	s := Service{ID: "b", Name: "B", Exec: "/opt/moteur", Args: []string{"--port", "8777", "--alias", "mon modèle", "50%"},
		Workdir: "/opt", Env: map[string]string{"CUDA_VISIBLE_DEVICES": "0"}}
	u := systemdUnitText(&s)
	for _, want := range []string{
		`ExecStart=/bin/bash -lc '/opt/moteur --port 8777 --alias \'mon modèle\' 50%%'`,
		"WorkingDirectory=/opt",
		"Environment=CUDA_VISIBLE_DEVICES=0",
		"Restart=on-failure",
		"StartLimitBurst=3",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("manque %q dans\n%s", want, u)
		}
	}
	// Une ligne de commande, un utilisateur, un plafond mémoire, pas de relance.
	c := Service{ID: "c", Name: "C", Command: "llama-server -m /m/x.gguf --port 8081", User: "alice", MemoryMaxGB: 2, NoRestart: true}
	u = systemdUnitText(&c)
	for _, want := range []string{
		"ExecStart=/bin/bash -lc 'llama-server -m /m/x.gguf --port 8081'",
		"User=alice",
		"MemoryMax=2048M",
		"MemorySwapMax=0",
	} {
		if !strings.Contains(u, want) {
			t.Errorf("manque %q dans\n%s", want, u)
		}
	}
	if strings.Contains(u, "Restart=") {
		t.Errorf("relance non voulue dans\n%s", u)
	}
}

// L'assistant root accepte les unités qu'AJEAN écrit, et rien qui donne root.
func TestCheckNewUnit(t *testing.T) {
	// Appel par un compte normal via sudo : jamais root.
	t.Setenv("SUDO_USER", "alice")
	s := Service{ID: "c", Name: "C", Command: "llama-server -m /m/x.gguf --port 8081", User: "alice"}
	ok := systemdUnitText(&s)
	if err := checkNewUnit(ok); err != nil {
		t.Fatalf("unité écrite par AJEAN refusée : %v", err)
	}
	for name, bad := range map[string]string{
		"sans utilisateur": strings.Replace(ok, "User=alice\n", "", 1),
		"root":             strings.Replace(ok, "User=alice", "User=root", 1),
		"commande +":       strings.Replace(ok, "ExecStart=", "ExecStartPre=+/bin/true\nExecStart=", 1),
		"groupe":           strings.Replace(ok, "User=alice", "User=alice\nGroup=root", 1),
	} {
		if bad == ok {
			t.Fatalf("%s : cas de test sans effet", name)
		}
		if checkNewUnit(bad) == nil {
			t.Errorf("%s : aurait dû être refusé", name)
		}
	}
}

// L'assistant root ne reçoit que des unités au nom d'AJEAN.
func TestRootUnitName(t *testing.T) {
	for name, want := range map[string]bool{
		"ajean-svc-basalt.service": true,
		"ajean-svc-a.service":      true,
		"ajean-svc-.service":       false,
		"ajean-svc-A.service":      false,
		"ajean-svc-x.timer":        false,
		"../ajean-svc-x.service":   false,
		"ajean-svc-x.service/..":   false,
		"ssh.service":              false,
		"llama.service":            false,
		"xajean-svc-x.service":     false,
		"ajean-svc-x.service.d":    false,
		"ajean-svc-x y.service":    false,
		"ajean-svc-x\n.service":    false,
		"ajean-guard.service":      false,
		"ajean-ui.service":         false,
	} {
		if got := rootUnitRe.MatchString(name); got != want {
			t.Errorf("%q : %v, attendu %v", name, got, want)
		}
	}
	s := Service{ID: "flash-next"}
	if !rootUnitRe.MatchString(unitFile(&s)) {
		t.Errorf("nom d'unité refusé : %s", unitFile(&s))
	}
}

// AJEAN lancé en root (pas de sudo) : ses modèles peuvent tourner en root,
// comme l'ancien moteur ; les options qui donnent des droits restent refusées.
func TestCheckNewUnitCallerRoot(t *testing.T) {
	t.Setenv("SUDO_USER", "")
	s := Service{ID: "c", Name: "C", Command: "llama-server -m /m/x.gguf --port 8081", User: "root"}
	u := systemdUnitText(&s)
	if err := checkNewUnit(u); err != nil {
		t.Fatalf("AJEAN en root : unité root refusée : %v", err)
	}
	if checkNewUnit(strings.Replace(u, "ExecStart=", "ExecStartPre=+/bin/true\nExecStart=", 1)) == nil {
		t.Fatal("commande + acceptée")
	}
}
