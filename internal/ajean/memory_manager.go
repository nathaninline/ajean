package ajean

// memory_manager.go — Gestionnaire de mémoire centralisé pour AJEAN.
//
// Surveille la RAM système et la VRAM GPU en temps réel, applique des limites
// configurables, nettoie automatiquement les ressources inutilisées et évite
// les saturations qui provoquent des plantages (issue #133 : plantage après
// 1h30 avec multi-GPU).
//
// Le gestionnaire fonctionne en arrière-plan (goroutine) et expose des métriques
// via l'API web (/api/memory/status). Il est conçu pour être non-intrusif :
// il ne bloque jamais les générations, il nettoie et avertit.

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ---------------------------------------------------------------------------
// Logs persistants des événements mémoire (issue #133 : debug après crash)
// ---------------------------------------------------------------------------

// memoryLogPath retourne le chemin du fichier de log mémoire.
// Le log est persistant : il survit aux redémarrages et permet de comprendre
// ce qui s'est passé avant un plantage (OOM, fuite mémoire, etc.).
func memoryLogPath() string {
	return filepath.Join(AjeanHome(), "logs", "memory.log")
}

// logMemoryEvent écrit un événement dans le log mémoire avec timestamp.
// Format: [2026-10-11 19:30:45] LEVEL message
// Le fichier est créé si inexistant, tronqué si > 1 MiB (rotation simple).
func logMemoryEvent(level, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	timestamp := time.Now().Format("2006-01-02 15:04:05")
	line := fmt.Sprintf("[%s] %s %s\n", timestamp, level, msg)

	// Créer le répertoire logs si nécessaire
	logPath := memoryLogPath()
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "[memory] impossible de créer le répertoire logs: %v\n", err)
		return
	}

	// Rotation simple : si le fichier dépasse 1 MiB, on garde les 500 dernières lignes
	if fi, err := os.Stat(logPath); err == nil && fi.Size() > 1024*1024 {
		rotateMemoryLog(logPath)
	}

	// Ajouter la ligne
	f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		fmt.Fprintf(os.Stderr, "[memory] impossible d'écrire le log: %v\n", err)
		return
	}
	defer f.Close()
	f.WriteString(line)

	// Également sur stderr pour les logs systemd
	fmt.Fprint(os.Stderr, line)
}

// rotateMemoryLog tronque le log en gardant les 500 dernières lignes.
func rotateMemoryLog(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	// Garder les 500 dernières lignes
	if len(lines) > 500 {
		lines = lines[len(lines)-500:]
	}

	// Réécrire le fichier
	content := strings.Join(lines, "\n") + "\n"
	os.WriteFile(path, []byte(content), 0o644)
}

// ReadMemoryLog retourne les N dernières lignes du log mémoire.
// Si n <= 0, retourne tout le fichier (limité à 1000 lignes max).
func ReadMemoryLog(n int) ([]string, error) {
	logPath := memoryLogPath()
	f, err := os.Open(logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return []string{}, nil
		}
		return nil, err
	}
	defer f.Close()

	var lines []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}

	// Limiter à 1000 lignes max
	if len(lines) > 1000 {
		lines = lines[len(lines)-1000:]
	}

	// Retourner les N dernières lignes
	if n > 0 && len(lines) > n {
		lines = lines[len(lines)-n:]
	}

	return lines, nil
}

// MemoryLimits définit les seuils de mémoire au-delà desquels le gestionnaire
// agit (avertissement, nettoyage, éviction). Toutes les valeurs sont en MiB.
type MemoryLimits struct {
	// RAM système : seuil d'avertissement (défaut 80% de la RAM totale)
	RAMWarnPercent float64
	// RAM système : seuil critique → nettoyage agressif (défaut 90%)
	RAMCriticalPercent float64
	// VRAM GPU : seuil d'avertissement (défaut 85% de la VRAM totale)
	VRAMWarnPercent float64
	// VRAM GPU : seuil critique → nettoyage agressif (défaut 95%)
	VRAMCriticalPercent float64
	// Nombre maximum de conversations en mémoire RAM (éviction LRU au-delà)
	MaxConversationsInRAM int
	// Taille maximale d'une conversation en mémoire (messages tronqués au-delà)
	MaxConversationMessages int
	// Intervalle de surveillance en secondes
	MonitorIntervalSeconds int
}

// DefaultMemoryLimits retourne les limites par défaut, ajustées à la machine.
func DefaultMemoryLimits() MemoryLimits {
	return MemoryLimits{
		RAMWarnPercent:          80,
		RAMCriticalPercent:      90,
		VRAMWarnPercent:         85,
		VRAMCriticalPercent:     95,
		MaxConversationsInRAM:   10,
		MaxConversationMessages: 500,
		MonitorIntervalSeconds:  30,
	}
}

// MemorySnapshot est un instantané des métriques mémoire à un moment T.
type MemorySnapshot struct {
	Timestamp time.Time `json:"timestamp"`

	// RAM système (MiB)
	RAMTotalMiB    float64 `json:"ram_total_mib"`
	RAMUsedMiB     float64 `json:"ram_used_mib"`
	RAMFreeMiB     float64 `json:"ram_free_mib"`
	RAMUsedPercent float64 `json:"ram_used_percent"`
	RAMProcessMiB  float64 `json:"ram_process_mib"` // mémoire utilisée par Ajean

	// VRAM GPU (MiB) — 0 si pas de GPU NVIDIA
	VRAMTotalMiB    float64 `json:"vram_total_mib"`
	VRAMUsedMiB     float64 `json:"vram_used_mib"`
	VRAMFreeMiB     float64 `json:"vram_free_mib"`
	VRAMUsedPercent float64 `json:"vram_used_percent"`

	// État du gestionnaire
	ConversationsInRAM int       `json:"conversations_in_ram"`
	LastCleanup        time.Time `json:"last_cleanup,omitempty"`
	CleanupCount       int       `json:"cleanup_count"`
	WarningsEmitted    int       `json:"warnings_emitted"`
}

// MemoryManager gère la surveillance et le nettoyage mémoire.
type MemoryManager struct {
	mu       sync.RWMutex
	limits   MemoryLimits
	latest   MemorySnapshot
	stopCh   chan struct{}
	stopOnce sync.Once

	// Callbacks appelés lors des événements (pour logs, notifications, etc.)
	onWarning  func(snapshot MemorySnapshot, level string)
	onCritical func(snapshot MemorySnapshot)
	onCleanup  func(stats CleanupStats)
}

// CleanupStats statistiques d'un nettoyage.
type CleanupStats struct {
	Timestamp            time.Time `json:"timestamp"`
	ConversationsEvicted int       `json:"conversations_evicted"`
	MessagesTruncated    int       `json:"messages_truncated"`
	RAMFreedMiB          float64   `json:"ram_freed_mib"`
	Trigger              string    `json:"trigger"` // "scheduled", "ram_critical", "vram_critical", "manual"
}

// Global memory manager instance
var memManager *MemoryManager
var memManagerOnce sync.Once

// GetMemoryManager retourne l'instance globale du gestionnaire mémoire.
func GetMemoryManager() *MemoryManager {
	memManagerOnce.Do(func() {
		memManager = NewMemoryManager(DefaultMemoryLimits())
	})
	return memManager
}

// NewMemoryManager crée un nouveau gestionnaire avec les limites spécifiées.
func NewMemoryManager(limits MemoryLimits) *MemoryManager {
	return &MemoryManager{
		limits: limits,
		stopCh: make(chan struct{}),
	}
}

// Start lance la surveillance en arrière-plan.
func (m *MemoryManager) Start() {
	go m.monitorLoop()
	msg := fmt.Sprintf("surveillance démarrée (intervalle: %ds, limites: RAM %d%%/%d%%, VRAM %d%%/%d%%)",
		m.limits.MonitorIntervalSeconds,
		int(m.limits.RAMWarnPercent), int(m.limits.RAMCriticalPercent),
		int(m.limits.VRAMWarnPercent), int(m.limits.VRAMCriticalPercent))
	fmt.Fprintf(os.Stderr, "[memory] %s\n", msg)
	logMemoryEvent("INFO", "%s", msg)
}

// Stop arrête la surveillance.
func (m *MemoryManager) Stop() {
	m.stopOnce.Do(func() {
		close(m.stopCh)
	})
}

// monitorLoop boucle de surveillance principale.
func (m *MemoryManager) monitorLoop() {
	interval := time.Duration(m.limits.MonitorIntervalSeconds) * time.Second
	if interval <= 0 {
		interval = 30 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Première mesure immédiate
	m.check()

	for {
		select {
		case <-ticker.C:
			m.check()
		case <-m.stopCh:
			return
		}
	}
}

// check effectue une mesure et applique les actions nécessaires.
func (m *MemoryManager) check() {
	snap := m.takeSnapshot()

	m.mu.Lock()
	m.latest = snap
	m.mu.Unlock()

	// Vérifier les seuils et agir
	level := m.evaluateThresholds(snap)
	switch level {
	case "critical":
		m.handleCritical(snap)
	case "warning":
		m.handleWarning(snap)
	}
}

// evaluateThresholds évalue les seuils et retourne le niveau d'alerte.
func (m *MemoryManager) evaluateThresholds(snap MemorySnapshot) string {
	// VRAM critique
	if snap.VRAMTotalMiB > 0 && snap.VRAMUsedPercent >= m.limits.VRAMCriticalPercent {
		return "critical"
	}
	// RAM critique
	if snap.RAMUsedPercent >= m.limits.RAMCriticalPercent {
		return "critical"
	}
	// VRAM warning
	if snap.VRAMTotalMiB > 0 && snap.VRAMUsedPercent >= m.limits.VRAMWarnPercent {
		return "warning"
	}
	// RAM warning
	if snap.RAMUsedPercent >= m.limits.RAMWarnPercent {
		return "warning"
	}
	return "ok"
}

// handleWarning émet un avertissement.
func (m *MemoryManager) handleWarning(snap MemorySnapshot) {
	m.mu.Lock()
	m.latest.WarningsEmitted++
	m.mu.Unlock()

	msg := fmt.Sprintf("⚠ AVERTISSEMENT: RAM %.0f%% (%.0f/%.0f MiB), VRAM %.0f%% (%.0f/%.0f MiB)",
		snap.RAMUsedPercent, snap.RAMUsedMiB, snap.RAMTotalMiB,
		snap.VRAMUsedPercent, snap.VRAMUsedMiB, snap.VRAMTotalMiB)
	fmt.Fprintf(os.Stderr, "[memory] %s\n", msg)
	logMemoryEvent("WARN", "%s", msg)

	if m.onWarning != nil {
		m.onWarning(snap, "warning")
	}
}

// handleCritical effectue un nettoyage agressif.
func (m *MemoryManager) handleCritical(snap MemorySnapshot) {
	msg := fmt.Sprintf("🚨 CRITIQUE: RAM %.0f%% (%.0f/%.0f MiB), VRAM %.0f%% (%.0f/%.0f MiB) — nettoyage agressif déclenché",
		snap.RAMUsedPercent, snap.RAMUsedMiB, snap.RAMTotalMiB,
		snap.VRAMUsedPercent, snap.VRAMUsedMiB, snap.VRAMTotalMiB)
	fmt.Fprintf(os.Stderr, "[memory] %s\n", msg)
	logMemoryEvent("CRIT", "%s", msg)

	if m.onCritical != nil {
		m.onCritical(snap)
	}

	// Nettoyage agressif
	stats := m.Cleanup("ram_critical")
	logMsg := fmt.Sprintf("nettoyage effectué: %d conversations évincées, %d messages tronqués, %.0f MiB libérés",
		stats.ConversationsEvicted, stats.MessagesTruncated, stats.RAMFreedMiB)
	fmt.Fprintf(os.Stderr, "[memory] %s\n", logMsg)
	logMemoryEvent("INFO", "%s", logMsg)
}

// takeSnapshot collecte les métriques mémoire actuelles.
func (m *MemoryManager) takeSnapshot() MemorySnapshot {
	snap := MemorySnapshot{
		Timestamp: time.Now(),
	}

	// RAM système
	snap.RAMTotalMiB = float64(totalRAMGB()) * 1024
	snap.RAMUsedMiB, snap.RAMFreeMiB = readSystemRAM()
	if snap.RAMTotalMiB > 0 {
		snap.RAMUsedPercent = snap.RAMUsedMiB / snap.RAMTotalMiB * 100
	}

	// RAM du processus Ajean
	snap.RAMProcessMiB = readProcessRAMMiB()

	// VRAM GPU (NVIDIA)
	snap.VRAMTotalMiB, snap.VRAMUsedMiB, snap.VRAMFreeMiB = readNvidiaVRAM()
	if snap.VRAMTotalMiB > 0 {
		snap.VRAMUsedPercent = snap.VRAMUsedMiB / snap.VRAMTotalMiB * 100
	}

	// Conversations en RAM
	snap.ConversationsInRAM = countConversationsInRAM()

	m.mu.RLock()
	snap.LastCleanup = m.latest.LastCleanup
	snap.CleanupCount = m.latest.CleanupCount
	snap.WarningsEmitted = m.latest.WarningsEmitted
	m.mu.RUnlock()

	return snap
}

// Cleanup effectue un nettoyage des ressources mémoire.
// trigger indique la raison du nettoyage.
func (m *MemoryManager) Cleanup(trigger string) CleanupStats {
	logMemoryEvent("INFO", "nettoyage démarré (trigger: %s)", trigger)

	stats := CleanupStats{
		Timestamp: time.Now(),
		Trigger:   trigger,
	}

	// 1. Éviction LRU des conversations (garder les MaxConversationsInRAM plus récentes)
	evicted := evictOldConversations(m.limits.MaxConversationsInRAM)
	stats.ConversationsEvicted = evicted

	// 2. Tronquer les longues conversations
	truncated := truncateLongConversations(m.limits.MaxConversationMessages)
	stats.MessagesTruncated = truncated

	// 3. Forcer le garbage collector Go
	before := readProcessRAMMiB()
	runtime.GC()
	runtime.GC() // double GC pour libérer plus
	after := readProcessRAMMiB()
	stats.RAMFreedMiB = before - after
	if stats.RAMFreedMiB < 0 {
		stats.RAMFreedMiB = 0
	}

	m.mu.Lock()
	m.latest.LastCleanup = time.Now()
	m.latest.CleanupCount++
	m.mu.Unlock()

	logMemoryEvent("INFO", "nettoyage terminé: %d conv. évincées, %d msg tronqués, %.0f MiB libérés (avant: %.0f MiB, après: %.0f MiB)",
		stats.ConversationsEvicted, stats.MessagesTruncated, stats.RAMFreedMiB, before, after)

	if m.onCleanup != nil {
		m.onCleanup(stats)
	}

	return stats
}

// GetSnapshot retourne le dernier snapshot connu.
func (m *MemoryManager) GetSnapshot() MemorySnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.latest
}

// GetLimits retourne les limites actuelles.
func (m *MemoryManager) GetLimits() MemoryLimits {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.limits
}

// SetLimits met à jour les limites (pour configuration dynamique).
func (m *MemoryManager) SetLimits(limits MemoryLimits) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.limits = limits
}

// OnWarning enregistre un callback appelé lors d'un avertissement.
func (m *MemoryManager) OnWarning(fn func(MemorySnapshot, string)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onWarning = fn
}

// OnCritical enregistre un callback appelé lors d'un état critique.
func (m *MemoryManager) OnCritical(fn func(MemorySnapshot)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onCritical = fn
}

// OnCleanup enregistre un callback appelé après un nettoyage.
func (m *MemoryManager) OnCleanup(fn func(CleanupStats)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onCleanup = fn
}

// ---------------------------------------------------------------------------
// Fonctions de lecture système
// ---------------------------------------------------------------------------

// readSystemRAM lit la RAM utilisée/libre du système (Linux/macOS/Windows).
func readSystemRAM() (usedMiB, freeMiB float64) {
	// Méthode 1: /proc/meminfo (Linux)
	if data, err := os.ReadFile("/proc/meminfo"); err == nil {
		var total, available float64
		for _, line := range strings.Split(string(data), "\n") {
			if strings.HasPrefix(line, "MemTotal:") {
				total = parseMeminfoValue(line)
			} else if strings.HasPrefix(line, "MemAvailable:") {
				available = parseMeminfoValue(line)
			}
		}
		if total > 0 {
			freeMiB = available
			usedMiB = total - available
			return
		}
	}

	// Méthode 2: sysinfo via commande système
	switch runtime.GOOS {
	case "darwin":
		// macOS: vm_stat
		out, err := exec.Command("vm_stat").Output()
		if err == nil {
			freeMiB = parseMacOSFreeRAM(string(out))
		}
	case "windows":
		// Windows: wmic
		out, err := exec.Command("wmic", "OS", "get", "FreePhysicalMemory,TotalVisibleMemorySize", "/Value").Output()
		if err == nil {
			usedMiB, freeMiB = parseWindowsRAM(string(out))
			return
		}
	}

	// Fallback: estimation via runtime
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	usedMiB = float64(ms.Alloc) / 1024 / 1024
	return
}

// parseMeminfoValue extrait la valeur en kB d'une ligne /proc/meminfo.
func parseMeminfoValue(line string) float64 {
	fields := strings.Fields(line)
	if len(fields) >= 2 {
		if v, err := strconv.ParseFloat(fields[1], 64); err == nil {
			return v / 1024 // kB → MiB
		}
	}
	return 0
}

// parseMacOSFreeRAM parse la sortie de vm_stat (macOS).
func parseMacOSFreeRAM(output string) float64 {
	// "Pages free: 12345." → pages de 4096 bytes
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "Pages free:") {
			fields := strings.Fields(line)
			if len(fields) >= 3 {
				if pages, err := strconv.ParseFloat(strings.TrimSuffix(fields[2], "."), 64); err == nil {
					return pages * 4096 / 1024 / 1024 // → MiB
				}
			}
		}
	}
	return 0
}

// parseWindowsRAM parse la sortie de wmic (Windows).
func parseWindowsRAM(output string) (usedMiB, freeMiB float64) {
	for _, line := range strings.Split(output, "\n") {
		if strings.HasPrefix(line, "FreePhysicalMemory=") {
			v := strings.TrimPrefix(line, "FreePhysicalMemory=")
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				freeMiB = f / 1024 // kB → MiB
			}
		} else if strings.HasPrefix(line, "TotalVisibleMemorySize=") {
			v := strings.TrimPrefix(line, "TotalVisibleMemorySize=")
			if f, err := strconv.ParseFloat(v, 64); err == nil {
				usedMiB = f/1024 - freeMiB
			}
		}
	}
	return
}

// readProcessRAMMiB retourne la RAM utilisée par le processus courant (MiB).
func readProcessRAMMiB() float64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	// HeapAlloc + StackInuse + MSpanInuse + MCacheInuse ≈ mémoire utilisée
	return float64(ms.HeapAlloc+ms.StackInuse+ms.MSpanInuse+ms.MCacheInuse) / 1024 / 1024
}

// readNvidiaVRAM lit la VRAM NVIDIA via nvidia-smi.
func readNvidiaVRAM() (totalMiB, usedMiB, freeMiB float64) {
	cmd := exec.Command("nvidia-smi",
		"--query-gpu=memory.total,memory.used,memory.free",
		"--format=csv,noheader,nounits")
	out, err := cmd.Output()
	if err != nil {
		return 0, 0, 0 // pas de GPU NVIDIA ou nvidia-smi absent
	}
	fields := strings.Split(strings.TrimSpace(string(out)), ",")
	if len(fields) < 3 {
		return 0, 0, 0
	}
	totalMiB, _ = strconv.ParseFloat(strings.TrimSpace(fields[0]), 64)
	usedMiB, _ = strconv.ParseFloat(strings.TrimSpace(fields[1]), 64)
	freeMiB, _ = strconv.ParseFloat(strings.TrimSpace(fields[2]), 64)
	return
}

// ---------------------------------------------------------------------------
// Fonctions d'éviction (stubs à connecter au système de conversations)
// ---------------------------------------------------------------------------

// countConversationsInRAM retourne le nombre de conversations actuellement en RAM.
// TODO: connecter au vrai système de conversations quand l'API sera disponible.
func countConversationsInRAM() int {
	// Pour l'instant, retourne 1 (la conversation active)
	// Sera remplacé par l'appel au vrai registre de conversations
	return 1
}

// evictOldConversations évince les plus anciennes conversations au-delà de la limite.
// Retourne le nombre de conversations évincées.
// TODO: connecter au vrai système d'archivage des conversations.
func evictOldConversations(maxKeep int) int {
	// Pour l'instant, retourne 0 (pas d'éviction)
	// Sera remplacé par l'appel au vrai système d'archivage
	return 0
}

// truncateLongConversations tronque les conversations dépassant la limite de messages.
// Retourne le nombre de messages tronqués.
// TODO: connecter au vrai système de conversations.
func truncateLongConversations(maxMessages int) int {
	// Pour l'instant, retourne 0 (pas de troncature)
	// Sera remplacé par l'appel au vrai système de conversations
	return 0
}
