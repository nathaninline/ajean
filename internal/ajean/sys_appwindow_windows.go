//go:build windows

package ajean

// sys_appwindow_windows.go — la fenêtre AJEAN vue de Windows.
//
// La fenêtre de l'application est un Edge/Chrome lancé en mode « --app ». Deux
// conséquences qu'on corrige ici, en parlant directement à la fenêtre :
//
//  1. Relancer AJEAN alors qu'il tourne déjà (raccourci, fichier téléchargé)
//     ouvrait une SECONDE fenêtre. On retrouve la fenêtre existante et on la
//     ramène au premier plan (focusAppWindow).
//
//  2. Le navigateur donne à sa fenêtre l'icône du favicon en 16 px, agrandie par
//     la barre des tâches : floue à côté des autres applications. On pose la
//     nôtre, dessinée exactement à la taille que la barre des tâches affiche
//     (keepAppWindowIcon). Le navigateur remet la sienne à chaque rechargement
//     de page : on revérifie donc toutes les deux secondes.

import (
	"fmt"
	"strconv"
	"sync"
	"syscall"
	"time"
	"unsafe"
)

var (
	pEnumWindows         = u32s.NewProc("EnumWindows")
	pGetClassNameW       = u32s.NewProc("GetClassNameW")
	pGetWindowTextW      = u32s.NewProc("GetWindowTextW")
	pIsWindowVisible     = u32s.NewProc("IsWindowVisible")
	pIsIconic            = u32s.NewProc("IsIconic")
	pShowWindowAW        = u32s.NewProc("ShowWindow")
	pSetForegroundWindow = u32s.NewProc("SetForegroundWindow")
	pGetForegroundWindow = u32s.NewProc("GetForegroundWindow")
	pSwitchToThisWindow  = u32s.NewProc("SwitchToThisWindow")
	pKeybdEvent          = u32s.NewProc("keybd_event")
	pSendMessageTimeoutW = u32s.NewProc("SendMessageTimeoutW")
	pGetDpiForWindow     = u32s.NewProc("GetDpiForWindow")
	pCreateIconFromResEx = u32s.NewProc("CreateIconFromResourceEx")
)

const (
	appWindowClass = "Chrome_WidgetWin_1" // fenêtres de Chromium (Edge, Chrome, Brave)
	appWindowTitle = "AJEAN"              // <title> de l'interface : une fenêtre --app n'affiche que lui

	wmGetIcon       = 0x007F
	wmSetIcon       = 0x0080
	iconSmall       = 0
	iconBig         = 1
	swRestore       = 9
	smtoAbortIfHung = 0x0002
)

// Un seul rappel EnumWindows pour toute la vie du process : syscall.NewCallback
// ne libère jamais ses entrées, en créer un par appel épuise la table.
var (
	enumMu    sync.Mutex
	enumFound []uintptr
	enumCB    uintptr
	enumOnce  sync.Once
)

func windowString(proc *syscall.LazyProc, hwnd uintptr) string {
	buf := make([]uint16, 256)
	n, _, _ := proc.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), uintptr(len(buf)))
	return syscall.UTF16ToString(buf[:n])
}

// appWindows : les fenêtres AJEAN ouvertes (visibles, de classe Chromium, dont le
// titre est exactement celui de l'interface). Un onglet AJEAN dans un navigateur
// ordinaire n'en fait pas partie : son titre porte le nom du navigateur.
func appWindows() []uintptr {
	enumOnce.Do(func() {
		enumCB = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
			if v, _, _ := pIsWindowVisible.Call(hwnd); v == 0 {
				return 1
			}
			if windowString(pGetClassNameW, hwnd) == appWindowClass && windowString(pGetWindowTextW, hwnd) == appWindowTitle {
				enumFound = append(enumFound, hwnd)
			}
			return 1 // continuer l'énumération
		})
	})
	enumMu.Lock()
	defer enumMu.Unlock()
	enumFound = nil
	pEnumWindows.Call(enumCB, 0)
	return append([]uintptr(nil), enumFound...)
}

// focusAppWindow ramène la fenêtre AJEAN existante au premier plan. Renvoie
// false s'il n'y en a pas : l'appelant en ouvre alors une.
func focusAppWindow() bool {
	wins := appWindows()
	if len(wins) == 0 {
		return false
	}
	hwnd := wins[0]
	if min, _, _ := pIsIconic.Call(hwnd); min != 0 {
		pShowWindowAW.Call(hwnd, swRestore)
	}
	pSetForegroundWindow.Call(hwnd)
	// Windows refuse le premier plan à un processus qui n'a pas reçu la dernière
	// action de l'utilisateur (il fait seulement clignoter la barre des tâches).
	// Une frappe ALT simulée lève ce verrou ; à défaut, SwitchToThisWindow.
	if fg, _, _ := pGetForegroundWindow.Call(); fg != hwnd {
		const vkMenu, keyUp = 0x12, 0x0002
		pKeybdEvent.Call(vkMenu, 0, 0, 0)
		pKeybdEvent.Call(vkMenu, 0, keyUp, 0)
		pSetForegroundWindow.Call(hwnd)
		if fg, _, _ := pGetForegroundWindow.Call(); fg != hwnd {
			pSwitchToThisWindow.Call(hwnd, 1)
		}
	}
	return true
}

func sendMessage(hwnd, msg, wparam, lparam uintptr) uintptr {
	var res uintptr
	pSendMessageTimeoutW.Call(hwnd, msg, wparam, lparam, smtoAbortIfHung, 500, uintptr(unsafe.Pointer(&res)))
	return res
}

// Icônes déjà créées, par taille en pixels. Elles vivent autant que le process :
// Windows les détruit avec lui.
var (
	appIconMu sync.Mutex
	winIcons  = map[int]uintptr{}
)

func windowIconHandle(px int) uintptr {
	appIconMu.Lock()
	defer appIconMu.Unlock()
	if h, ok := winIcons[px]; ok {
		return h
	}
	png := BrandIconPNG(px) // Windows accepte un PNG comme image d'icône
	h, _, _ := pCreateIconFromResEx.Call(uintptr(unsafe.Pointer(&png[0])), uintptr(len(png)), 1, 0x00030000, uintptr(px), uintptr(px), 0)
	winIcons[px] = h
	return h
}

func windowDPI(hwnd uintptr) int {
	if pGetDpiForWindow.Find() != nil {
		return 96 // avant Windows 10 1607
	}
	if d, _, _ := pGetDpiForWindow.Call(hwnd); d >= 96 {
		return int(d)
	}
	return 96
}

// appWindowIconSizes : tailles (px) de la grande et de la petite icône pour une
// fenêtre à cette densité. La barre des tâches dessine la grande icône en 24
// points : on la fournit exactement à cette taille (24, 36, 48 px à 100, 150,
// 200 %), sans mise à l'échelle, donc nette. La petite est celle de la barre de
// titre (16 points).
func appWindowIconSizes(dpi int) (big, small int) {
	return (24*dpi + 48) / 96, (16*dpi + 48) / 96
}

// applyAppWindowIcon pose notre icône sur la fenêtre si ce n'est pas déjà elle.
// Renvoie les icônes précédentes (pour les remettre, voir cmdWindowIcon).
func applyAppWindowIcon(hwnd uintptr) (prevBig, prevSmall uintptr) {
	big, small := appWindowIconSizes(windowDPI(hwnd))
	hb, hs := windowIconHandle(big), windowIconHandle(small)
	if hb == 0 || hs == 0 {
		return 0, 0
	}
	prevBig, prevSmall = sendMessage(hwnd, wmGetIcon, iconBig, 0), sendMessage(hwnd, wmGetIcon, iconSmall, 0)
	if prevBig != hb {
		sendMessage(hwnd, wmSetIcon, iconBig, hb)
	}
	if prevSmall != hs {
		sendMessage(hwnd, wmSetIcon, iconSmall, hs)
	}
	return prevBig, prevSmall
}

// keepAppWindowIcon garde l'icône d'AJEAN sur sa fenêtre tant que l'application
// tourne. Lancé une fois par cmdApp.
func keepAppWindowIcon() {
	for {
		for _, hwnd := range appWindows() {
			applyAppWindowIcon(hwnd)
		}
		time.Sleep(2 * time.Second)
	}
}

// cmdWindowIcon (« ajean window-icon [secondes] », absent de l'aide) : banc
// d'essai de l'icône de fenêtre. Pose l'icône sur la ou les fenêtres AJEAN
// ouvertes, attend, puis remet les icônes d'origine : celles qu'on a créées
// disparaissent avec ce process, on ne laisse donc pas la fenêtre sans icône.
func cmdWindowIcon(args []string) error {
	wait := 8
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 {
			wait = n
		}
	}
	wins := appWindows()
	if len(wins) == 0 {
		return fmt.Errorf("aucune fenêtre AJEAN ouverte")
	}
	type prev struct{ hwnd, big, small uintptr }
	var saved []prev
	for _, hwnd := range wins {
		dpi := windowDPI(hwnd)
		big, small := appWindowIconSizes(dpi)
		pb, ps := applyAppWindowIcon(hwnd)
		saved = append(saved, prev{hwnd, pb, ps})
		fmt.Printf("fenêtre %#x : %d ppp, icônes %d px et %d px posées\n", hwnd, dpi, big, small)
	}
	time.Sleep(time.Duration(wait) * time.Second)
	for _, p := range saved {
		sendMessage(p.hwnd, wmSetIcon, iconBig, p.big)
		sendMessage(p.hwnd, wmSetIcon, iconSmall, p.small)
	}
	fmt.Println("icônes d'origine remises")
	return nil
}
