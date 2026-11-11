package ajean

// Garde-fou des services : vérifications avant démarrage (programme, port,
// RAM, VRAM), surveillance pendant le chargement, chien de garde RAM permanent
// dans le serveur web, mémorisation des pics réels.

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// resources : relevé instantané. VRAM en Mo par index de carte.
type resources struct {
	AvailRAMMB int
	TotalRAMMB int
	VRAMUsed   map[string]int
	VRAMTotal  map[string]int
}

func sampleResources() resources {
	used, total := ramUsageMB()
	r := resources{TotalRAMMB: total, AvailRAMMB: -1, VRAMUsed: map[string]int{}, VRAMTotal: map[string]int{}}
	if total > 0 {
		r.AvailRAMMB = total - used
	}
	for i, g := range sampleVramGPUs() {
		k := strconv.Itoa(i)
		r.VRAMUsed[k] = anyInt(g["used"])
		r.VRAMTotal[k] = anyInt(g["total"])
	}
	return r
}

func anyInt(v any) int {
	switch x := v.(type) {
	case int:
		return x
	case int64:
		return int(x)
	case float64:
		return int(x)
	}
	return 0
}

// ramFloorMB : sous ce seuil de RAM libre, le chien de garde arrête un service.
// AJEAN_RAM_FLOOR_MB le remplace.
func ramFloorMB(totalMB int) int {
	if v, err := strconv.Atoi(os.Getenv("AJEAN_RAM_FLOOR_MB")); err == nil && v > 0 {
		return v
	}
	return max(1024, totalMB*3/100)
}

// effectiveNeed : le plus grand du besoin déclaré et du pic mesuré.
func effectiveNeed(s *Service) ServiceNeed {
	peak := loadServiceState()[s.ID].Peak
	n := ServiceNeed{RAMGB: max(s.Need.RAMGB, peak.RAMGB), VRAMGB: map[string]float64{}}
	for k, v := range s.Need.VRAMGB {
		n.VRAMGB[k] = v
	}
	for k, v := range peak.VRAMGB {
		n.VRAMGB[k] = max(n.VRAMGB[k], v)
	}
	return n
}

// preflightService : refuse un démarrage voué à l'échec ou dangereux.
func preflightService(s *Service, force bool) error {
	if s.Managed() && s.Command == "" && s.Exec != "" {
		if !svcIsFile(s.Exec) {
			if _, err := exec.LookPath(s.Exec); err != nil {
				return fmt.Errorf("programme introuvable : %s", s.Exec)
			}
		}
	}
	if s.Managed() {
		if s.Workdir != "" {
			if fi, err := os.Stat(s.Workdir); err != nil || !fi.IsDir() {
				return fmt.Errorf("dossier de travail introuvable : %s", s.Workdir)
			}
		}
	}
	if p := localPort(s); p != "" && svcPortBusy(p) {
		return fmt.Errorf("le port %s est déjà utilisé par un autre programme", p)
	}
	if force {
		return nil
	}
	need := effectiveNeed(s)
	res := sampleResources()
	var short []string
	if need.RAMGB > 0 && res.AvailRAMMB >= 0 {
		margin := ramFloorMB(res.TotalRAMMB)
		if int(need.RAMGB*1024) > res.AvailRAMMB-margin {
			short = append(short, fmt.Sprintf("RAM : %.1f Go nécessaires, %.1f Go libres (marge %.1f Go)",
				need.RAMGB, mbToGB(res.AvailRAMMB), mbToGB(margin)))
		}
	}
	for k, v := range need.VRAMGB {
		total, ok := res.VRAMTotal[k]
		if !ok {
			short = append(short, fmt.Sprintf("carte %s absente", k))
			continue
		}
		free := total - res.VRAMUsed[k]
		if int(v*1024) > free-256 {
			short = append(short, fmt.Sprintf("VRAM carte %s : %.1f Go nécessaires, %.1f Go libres", k, v, mbToGB(free)))
		}
	}
	if len(short) == 0 {
		return nil
	}
	msg := "mémoire insuffisante :\n  " + strings.Join(short, "\n  ")
	if act := activeServiceNames(s.ID); len(act) > 0 {
		msg += "\nservices en marche à arrêter pour faire de la place : " + strings.Join(act, ", ")
	}
	return fmt.Errorf("%s", msg)
}

func mbToGB(mb int) float64 { return float64(mb) / 1024 }

func activeServiceNames(except string) []string {
	list, _ := LoadServices()
	state := loadServiceState()
	var out []string
	for i := range list {
		s := &list[i]
		if s.ID == except || !s.Local() {
			continue
		}
		if st := osSvcState(s); st == "active" || st == "activating" {
			label := s.ID
			if p := state[s.ID].Peak.RAMGB; p > 0 {
				label += fmt.Sprintf(" (%.1f Go RAM)", p)
			}
			out = append(out, label)
		}
	}
	return out
}

// watchStartup suit le chargement jusqu'à ce que l'API réponde.
func watchStartup(s *Service, base resources, say func(string)) error {
	if strings.TrimSpace(s.URL) == "" {
		return nil // pas d'API à attendre : démarré suffit
	}
	deadline := time.Now().Add(s.readyTimeout())
	minAvail := base.AvailRAMMB
	maxVRAM := map[string]int{}
	begin := time.Now()
	started := loadServiceState()[s.ID].StartedAt
	if started.IsZero() {
		started = begin
	}
	lastSay := time.Time{}
	for time.Now().Before(deadline) {
		time.Sleep(time.Second)
		r := sampleResources()
		if r.AvailRAMMB >= 0 {
			if minAvail < 0 || r.AvailRAMMB < minAvail {
				minAvail = r.AvailRAMMB
			}
			if floor := ramFloorMB(r.TotalRAMMB); r.AvailRAMMB < floor {
				_ = osSvcStop(s)
				return fmt.Errorf("arrêté par le garde-fou : RAM libre %.1f Go sous le seuil de %.1f Go",
					mbToGB(r.AvailRAMMB), mbToGB(floor))
			}
		}
		for k, v := range r.VRAMUsed {
			maxVRAM[k] = max(maxVRAM[k], v)
		}
		// Le gardien root a tué le modèle qui charge : sans arrêt, le système le
		// relancerait pour retomber sur la même saturation.
		if k, hit := strikeFor(GuardKills(), ServiceUnitName(s), started, time.Now().Add(time.Minute)); hit {
			_ = osSvcStop(s)
			return errors.New("arrêté " + guardStrikeText(k))
		}
		st := osSvcState(s)
		if (st == "failed" || st == "inactive") && time.Since(begin) > 3*time.Second {
			tail := osSvcLogTail(s, 20)
			// Sans cela, il se relancerait en boucle et reprendrait le port.
			_ = osSvcStop(s)
			return fmt.Errorf("le service s'est arrêté pendant le chargement (%s)\n%s", st, tail)
		}
		if probeReady(s, 2*time.Second) == nil {
			peak := ServiceNeed{VRAMGB: map[string]float64{}}
			if base.AvailRAMMB >= 0 && minAvail >= 0 {
				peak.RAMGB = roundGB(base.AvailRAMMB - minAvail)
			}
			for k, v := range maxVRAM {
				if d := v - base.VRAMUsed[k]; d > 256 {
					peak.VRAMGB[k] = roundGB(d)
				}
			}
			updateServiceState(s.ID, func(rt *serviceRuntime) { rt.Peak = peak })
			say(fmt.Sprintf("prêt en %s (pic RAM %.1f Go)", time.Since(begin).Round(time.Second), peak.RAMGB))
			return nil
		}
		if time.Since(lastSay) > 10*time.Second {
			lastSay = time.Now()
			say(fmt.Sprintf("chargement… %s, RAM libre %.1f Go", time.Since(begin).Round(time.Second), mbToGB(r.AvailRAMMB)))
		}
	}
	_ = osSvcStop(s)
	return fmt.Errorf("pas de réponse de %s/models après %s : service arrêté\n%s", s.URL, s.readyTimeout(), osSvcLogTail(s, 20))
}

func roundGB(mb int) float64 { return float64(int(float64(mb)/1024*10+0.5)) / 10 }

// --- Gardien RAM root : ce qu'il a fait à un service -----------------------
//
// Le gardien (ajean-guard, Linux) tourne hors d'AJEAN et n'écrit pas dans son
// état : son journal (GuardKills) fait foi.

// strikeFor : dernière intervention du gardien sur cette unité entre deux instants.
func strikeFor(kills []GuardKill, unit string, from, to time.Time) (GuardKill, bool) {
	unit = strings.TrimSuffix(unit, ".service")
	for i := len(kills) - 1; i >= 0; i-- {
		if at := time.UnixMilli(kills[i].At); kills[i].Name == unit && at.After(from) && at.Before(to) {
			return kills[i], true
		}
	}
	return GuardKill{}, false
}

// guardKillOf : l'intervention du gardien qui explique le dernier arrêt du
// service. Elle doit coïncider avec la fin de son dernier processus : un modèle
// relancé depuis, ou arrêté plus tard pour une autre raison, n'est pas concerné.
func guardKillOf(s *Service) (GuardKill, bool) {
	kills := GuardKills()
	if !s.Local() || len(kills) == 0 {
		return GuardKill{}, false
	}
	exit := osSvcLastExit(s)
	if exit.IsZero() {
		return GuardKill{}, false
	}
	// La fin est notée à la seconde, juste après la frappe.
	return strikeFor(kills, ServiceUnitName(s), exit.Add(-15*time.Second), exit.Add(2*time.Second))
}

func guardStrikeText(k GuardKill) string {
	at := time.UnixMilli(k.At)
	when := "à " + at.Format("15:04")
	if y, m, d := at.Date(); time.Now().YearDay() != at.YearDay() || time.Now().Year() != y {
		when = fmt.Sprintf("le %02d/%02d à %s", d, int(m), at.Format("15:04"))
	}
	return fmt.Sprintf("par le gardien RAM %s : mémoire disponible sous le seuil, le modèle occupait %.1f Go",
		when, float64(k.Bytes)/(1<<30))
}

// guardNote : la phrase à afficher quand le gardien a arrêté ce service ("" sinon).
func guardNote(s *Service) string {
	if k, ok := guardKillOf(s); ok {
		return "arrêté " + guardStrikeText(k)
	}
	return ""
}

// parseUnitTime lit un horodatage de systemd (« Mon 2026-11-09 14:32:10 CET »).
// Le jour et le fuseau dépendent de la langue du système : seules la date et
// l'heure sont lues, en heure locale. Zéro si absent (« n/a », vide).
func parseUnitTime(v string) time.Time {
	f := strings.Fields(v)
	for i := 0; i+1 < len(f); i++ {
		if t, err := time.ParseInLocation("2006-01-02 15:04:05", f[i]+" "+f[i+1], time.Local); err == nil {
			return t
		}
	}
	return time.Time{}
}

// --- Chien de garde permanent (serveur web) --------------------------------

var watchdogOnce sync.Once

// StartServiceWatchdog : toutes les 2 s, si la RAM libre passe sous le seuil,
// arrête le service local démarré le plus récemment (souvent celui qui charge).
func StartServiceWatchdog() {
	watchdogOnce.Do(func() { go serviceWatchdogLoop() })
}

func serviceWatchdogLoop() {
	var cooldown time.Time
	for {
		time.Sleep(2 * time.Second)
		if time.Now().Before(cooldown) || osHasRootGuard() {
			continue // sous Linux, le gardien root (ajean-guard) s'en charge
		}
		used, total := ramUsageMB()
		if total == 0 || total-used >= ramFloorMB(total) {
			continue
		}
		victim := newestActiveService()
		if victim == nil {
			continue
		}
		msg := fmt.Sprintf("arrêté par le garde-fou : RAM libre %.1f Go sous le seuil", mbToGB(total-used))
		log.Printf("services: %s %s", victim.ID, msg)
		_ = osSvcStop(victim)
		updateServiceState(victim.ID, func(r *serviceRuntime) { r.LastError = msg })
		cooldown = time.Now().Add(15 * time.Second)
	}
}

func newestActiveService() *Service {
	list, _ := LoadServices()
	state := loadServiceState()
	var cands []Service
	for _, s := range list {
		if s.Local() {
			if st := osSvcState(&s); st == "active" || st == "activating" {
				cands = append(cands, s)
			}
		}
	}
	if len(cands) == 0 {
		return nil
	}
	sort.Slice(cands, func(i, j int) bool { return state[cands[i].ID].StartedAt.After(state[cands[j].ID].StartedAt) })
	return &cands[0]
}
