//go:build windows

package nodeclient

// runas_windows.go — « agir comme l'utilisateur connecté ».
//
// Le poste tourne en service LocalSystem (pour survivre aux redémarrages, même
// sans session ouverte). Mais LocalSystem ne voit ni le Bureau, ni les fichiers,
// ni le profil de l'utilisateur : l'IA était donc coincée sur le profil SYSTEM.
//
// Quand quelqu'un est connecté à la session console, on récupère SON jeton et on
// exécute en son nom : le shell est lancé avec ce jeton (SysProcAttr.Token) et
// son environnement (%USERPROFILE%, %APPDATA%…), et les accès fichiers sont
// faits sous impersonation. Personne de connecté (ou pas le privilège, hors
// service) → on retombe SILENCIEUSEMENT sur le comportement d'avant (SYSTEM).
// Rien ne casse jamais : tout échec de bascule = repli SYSTEM.

import (
	"os/exec"
	"runtime"
	"syscall"
	"unicode/utf16"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	modkernel32 = windows.NewLazySystemDLL("kernel32.dll")
	modwtsapi32 = windows.NewLazySystemDLL("wtsapi32.dll")
	moduserenv  = windows.NewLazySystemDLL("userenv.dll")
	modadvapi32 = windows.NewLazySystemDLL("advapi32.dll")

	procWTSGetActiveConsoleSessionId = modkernel32.NewProc("WTSGetActiveConsoleSessionId")
	procWTSQueryUserToken            = modwtsapi32.NewProc("WTSQueryUserToken")
	procCreateEnvironmentBlock       = moduserenv.NewProc("CreateEnvironmentBlock")
	procDestroyEnvironmentBlock      = moduserenv.NewProc("DestroyEnvironmentBlock")
	procImpersonateLoggedOnUser      = modadvapi32.NewProc("ImpersonateLoggedOnUser")
	procRevertToSelf                 = modadvapi32.NewProc("RevertToSelf")
)

// activeUserToken renvoie le jeton PRIMAIRE de l'utilisateur de la session
// console active. ok=false s'il n'y a personne de connecté, ou si on n'a pas le
// privilège SeTcbPrivilege (cas d'un poste lancé hors service : on agit alors
// déjà comme l'utilisateur courant, rien à faire). L'appelant DOIT Close le
// jeton quand ok.
func activeUserToken() (windows.Token, bool) {
	r, _, _ := procWTSGetActiveConsoleSessionId.Call()
	sid := uint32(r)
	if sid == 0xFFFFFFFF { // aucune session rattachée à la console physique
		return 0, false
	}
	var tok windows.Token
	ret, _, _ := procWTSQueryUserToken.Call(uintptr(sid), uintptr(unsafe.Pointer(&tok)))
	if ret == 0 || tok == 0 {
		return 0, false
	}
	return tok, true
}

// userEnvBlock construit le bloc d'environnement de l'utilisateur (pour que
// %USERPROFILE%, %APPDATA%, PATH… pointent vers SON profil, pas celui de
// SYSTEM), sous forme de []string "K=V" prête pour cmd.Env. nil si indisponible.
func userEnvBlock(tok windows.Token) []string {
	var blk *uint16
	r, _, _ := procCreateEnvironmentBlock.Call(uintptr(unsafe.Pointer(&blk)), uintptr(tok), 0)
	if r == 0 || blk == nil {
		return nil
	}
	defer procDestroyEnvironmentBlock.Call(uintptr(unsafe.Pointer(blk)))
	// Bloc = suite de chaînes UTF-16 terminées par \0, close par un \0 final. On
	// avance avec unsafe.Add sur un pointeur typé (mémoire native, hors GC).
	var env []string
	p := unsafe.Pointer(blk)
	for {
		s := windows.UTF16PtrToString((*uint16)(p))
		if s == "" {
			break
		}
		env = append(env, s)
		// (nombre d'unités UTF-16 + le \0) × 2 octets.
		p = unsafe.Add(p, (len(utf16.Encode([]rune(s)))+1)*2)
	}
	return env
}

// applyRunAsUser prépare `cmd` pour tourner sous le compte de l'utilisateur
// connecté (jeton + environnement). Renvoie une fonction de nettoyage à appeler
// APRÈS l'exécution (ferme le jeton). Personne de connecté → ne touche à rien et
// renvoie un nettoyage vide (le processus tourne alors tel quel, SYSTEM).
func applyRunAsUser(cmd *exec.Cmd) func() {
	tok, ok := activeUserToken()
	if !ok {
		return func() {}
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Token = syscall.Token(tok)
	if env := userEnvBlock(tok); env != nil {
		cmd.Env = env
	}
	return func() { tok.Close() }
}

// withActiveUser exécute fn sous l'identité de l'utilisateur connecté
// (impersonation), pour que les accès fichiers voient SES fichiers et respectent
// SES droits. Personne de connecté / échec d'impersonation → fn s'exécute tel
// quel (SYSTEM). Le thread est verrouillé le temps de l'impersonation ; s'il ne
// peut pas être proprement rétabli, on ne le rend pas au pool (il meurt avec la
// goroutine) plutôt que de laisser une identité usurpée fuiter.
func withActiveUser(fn func() string) string {
	tok, ok := activeUserToken()
	if !ok {
		return fn()
	}
	defer tok.Close()
	runtime.LockOSThread()
	if r, _, _ := procImpersonateLoggedOnUser.Call(uintptr(tok)); r == 0 {
		runtime.UnlockOSThread()
		return fn()
	}
	out := fn()
	if r, _, _ := procRevertToSelf.Call(); r == 0 {
		return out // thread compromis : on ne l'UnlockOSThread pas → il ne resert pas
	}
	runtime.UnlockOSThread()
	return out
}
