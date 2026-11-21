//go:build windows

package ajean

// sys_taskdialog_windows.go — la boîte de dialogue moderne de Windows.
//
// Les questions posées au lancement (installer, mettre à jour, version plus
// ancienne…) passaient par MessageBoxW. AJEAN n'étant pas déclaré « compatible
// haute résolution », Windows agrandissait cette boîte comme une image : floue
// et pixelisée sur un écran à 150 ou 200 %, avec l'allure de Windows 95.
//
// TaskDialog est la boîte actuelle (titre en grand, texte, boutons). Elle vit
// dans la version 6 de comctl32, que Windows ne charge que pour un programme
// qui la réclame par un manifeste. Plutôt que d'en embarquer un (il changerait
// le comportement de toutes les fenêtres d'AJEAN), on active ce contexte le
// temps de l'appel, à partir de celui que shell32.dll fournit, et on demande le
// rendu net pour ce seul fil d'exécution. Au moindre échec, l'appelant retombe
// sur MessageBoxW.

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"unsafe"
)

var (
	k32td              = syscall.NewLazyDLL("kernel32.dll")
	pCreateActCtxW     = k32td.NewProc("CreateActCtxW")
	pActivateActCtx    = k32td.NewProc("ActivateActCtx")
	pDeactivateActCtx  = k32td.NewProc("DeactivateActCtx")
	pReleaseActCtx     = k32td.NewProc("ReleaseActCtx")
	pSetThreadDpiAwCtx = u32s.NewProc("SetThreadDpiAwarenessContext")
	invalidHandleValue = ^uintptr(0)
	dpiPerMonitorV2    = ^uintptr(3)     // DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 (-4)
	tdIconInformation  = uintptr(0xFFFD) // TD_INFORMATION_ICON
	appIconResource    = uintptr(1)      // icône du .exe (posée par goversioninfo)
)

const (
	actCtxResourceNameValid = 0x008
	actCtxAssemblyDirValid  = 0x004
	tdcbfOK                 = 0x1
	tdcbfYes                = 0x2
	tdcbfNo                 = 0x4
)

// actCtxW : ACTCTXW (même disposition que la structure C sur amd64 et arm64).
type actCtxW struct {
	cbSize              uint32
	dwFlags             uint32
	lpSource            *uint16
	wProcessorArch      uint16
	wLangID             uint16
	lpAssemblyDirectory *uint16
	lpResourceName      uintptr
	lpApplicationName   *uint16
	hModule             uintptr
}

// taskDialog affiche la boîte moderne. flags : ceux de MessageBox (mbYesNo pour
// une question, sinon un simple OK). Renvoie (bouton, true), ou (0, false) si la
// boîte n'a pas pu être affichée.
func taskDialog(text, title string, flags uintptr) (int, bool) {
	// Contexte d'activation et rendu haute résolution sont propres au fil d'exécution.
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	sysDir := filepath.Join(os.Getenv("SystemRoot"), "System32")
	src, err1 := syscall.UTF16PtrFromString(filepath.Join(sysDir, "shell32.dll"))
	dir, err2 := syscall.UTF16PtrFromString(sysDir)
	if err1 != nil || err2 != nil {
		return 0, false
	}
	ctx := actCtxW{dwFlags: actCtxResourceNameValid | actCtxAssemblyDirValid, lpSource: src, lpAssemblyDirectory: dir, lpResourceName: 124}
	ctx.cbSize = uint32(unsafe.Sizeof(ctx))
	h, _, _ := pCreateActCtxW.Call(uintptr(unsafe.Pointer(&ctx)))
	if h == 0 || h == invalidHandleValue {
		return 0, false
	}
	defer pReleaseActCtx.Call(h)
	var cookie uintptr
	if ok, _, _ := pActivateActCtx.Call(h, uintptr(unsafe.Pointer(&cookie))); ok == 0 {
		return 0, false
	}
	defer pDeactivateActCtx.Call(0, cookie)

	comctl, err := syscall.LoadLibrary("comctl32.dll") // version 6, grâce au contexte actif
	if err != nil {
		return 0, false
	}
	proc, err := syscall.GetProcAddress(comctl, "TaskDialog")
	if err != nil {
		return 0, false
	}

	// Rendu net : sans ça la boîte est dessinée à 100 % puis agrandie par Windows.
	if pSetThreadDpiAwCtx.Find() == nil {
		if old, _, _ := pSetThreadDpiAwCtx.Call(dpiPerMonitorV2); old != 0 {
			defer pSetThreadDpiAwCtx.Call(old)
		}
	}

	// Premier paragraphe = la question, en grand ; le reste = les précisions.
	main, content, _ := strings.Cut(strings.TrimSpace(text), "\n\n")
	pTitle, _ := syscall.UTF16PtrFromString(title)
	pMain, _ := syscall.UTF16PtrFromString(strings.TrimSpace(main))
	pContent, _ := syscall.UTF16PtrFromString(strings.TrimSpace(content))
	buttons := uintptr(tdcbfOK)
	if flags&mbYesNo != 0 {
		buttons = tdcbfYes | tdcbfNo
	}
	// L'icône d'AJEAN si le .exe en porte une, sinon l'icône « information ».
	hInst, _, _ := pGetModuleHandleW.Call(0)
	icon := appIconResource
	if hInst == 0 {
		icon = tdIconInformation
	}
	var pressed int32
	hr, _, _ := syscall.SyscallN(proc, 0, hInst, uintptr(unsafe.Pointer(pTitle)), uintptr(unsafe.Pointer(pMain)),
		uintptr(unsafe.Pointer(pContent)), buttons, icon, uintptr(unsafe.Pointer(&pressed)))
	if int32(hr) < 0 || pressed == 0 {
		return 0, false
	}
	return int(pressed), true
}

// cmdDialogTest (« ajean dialog-test », absent de l'aide) : affiche la question
// de mise à jour telle que la pose l'installeur, pour vérifier son rendu.
func cmdDialogTest(args []string) error {
	r := messageBox("Mettre à jour AJEAN en version "+Version+" ?\n\n"+
		"AJEAN est en cours d'exécution (version 0.18.17). Il sera fermé, mis à jour, puis relancé.\n\n"+
		"Non garde la version actuelle.", "Mise à jour d'AJEAN", mbYesNo|mbIconQuestion)
	fmt.Println("bouton :", r)
	return nil
}
