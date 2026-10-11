package ajean

import (
	"testing"
	"time"
)

func TestMemoryManagerSnapshot(t *testing.T) {
	mm := NewMemoryManager(DefaultMemoryLimits())

	snap := mm.takeSnapshot()

	// Vérifier que le timestamp est récent
	if time.Since(snap.Timestamp) > 5*time.Second {
		t.Errorf("timestamp trop ancien: %v", snap.Timestamp)
	}

	// Vérifier que la RAM totale est détectée (sur une machine avec RAM)
	if snap.RAMTotalMiB <= 0 {
		t.Logf("RAM totale non détectée (peut être normal en CI): %.0f MiB", snap.RAMTotalMiB)
	} else {
		t.Logf("RAM totale: %.0f MiB, utilisée: %.0f MiB (%.1f%%)",
			snap.RAMTotalMiB, snap.RAMUsedMiB, snap.RAMUsedPercent)
	}

	// Vérifier que le pourcentage est cohérent
	if snap.RAMTotalMiB > 0 {
		expectedPct := snap.RAMUsedMiB / snap.RAMTotalMiB * 100
		if snap.RAMUsedPercent < expectedPct-0.1 || snap.RAMUsedPercent > expectedPct+0.1 {
			t.Errorf("pourcentage RAM incohérent: got %.2f, expected %.2f", snap.RAMUsedPercent, expectedPct)
		}
	}

	// VRAM peut être 0 si pas de GPU NVIDIA
	t.Logf("VRAM totale: %.0f MiB, utilisée: %.0f MiB (%.1f%%)",
		snap.VRAMTotalMiB, snap.VRAMUsedMiB, snap.VRAMUsedPercent)
}

func TestMemoryManagerThresholds(t *testing.T) {
	limits := DefaultMemoryLimits()
	limits.RAMWarnPercent = 50
	limits.RAMCriticalPercent = 80
	limits.VRAMWarnPercent = 50
	limits.VRAMCriticalPercent = 80

	mm := NewMemoryManager(limits)

	// Test avec RAM à 60% → warning
	snap := MemorySnapshot{
		RAMTotalMiB:     16000,
		RAMUsedMiB:      9600, // 60%
		RAMUsedPercent:  60,
		VRAMTotalMiB:    16000,
		VRAMUsedMiB:     8000, // 50%
		VRAMUsedPercent: 50,
	}
	level := mm.evaluateThresholds(snap)
	if level != "warning" {
		t.Errorf("expected warning at 60%% RAM, got %s", level)
	}

	// Test avec RAM à 85% → critical
	snap.RAMUsedMiB = 13600 // 85%
	snap.RAMUsedPercent = 85
	level = mm.evaluateThresholds(snap)
	if level != "critical" {
		t.Errorf("expected critical at 85%% RAM, got %s", level)
	}

	// Test avec VRAM à 90% → critical
	snap.RAMUsedMiB = 8000 // 50%
	snap.RAMUsedPercent = 50
	snap.VRAMUsedMiB = 14400 // 90%
	snap.VRAMUsedPercent = 90
	level = mm.evaluateThresholds(snap)
	if level != "critical" {
		t.Errorf("expected critical at 90%% VRAM, got %s", level)
	}

	// Test OK (sous les seuils)
	snap.RAMUsedMiB = 7840 // 49%
	snap.RAMUsedPercent = 49
	snap.VRAMUsedMiB = 7840 // 49%
	snap.VRAMUsedPercent = 49
	level = mm.evaluateThresholds(snap)
	if level != "ok" {
		t.Errorf("expected ok at 49%%/49%%, got %s", level)
	}
}

func TestMemoryManagerCleanup(t *testing.T) {
	mm := NewMemoryManager(DefaultMemoryLimits())

	// Le nettoyage ne doit pas paniquer même sans conversations
	stats := mm.Cleanup("test")

	if stats.Trigger != "test" {
		t.Errorf("expected trigger 'test', got '%s'", stats.Trigger)
	}

	// Vérifier que le compteur de nettoyage a été incrémenté
	snap := mm.GetSnapshot()
	if snap.CleanupCount != 1 {
		t.Errorf("expected cleanup count 1, got %d", snap.CleanupCount)
	}
}

func TestMemoryManagerLimits(t *testing.T) {
	mm := NewMemoryManager(DefaultMemoryLimits())

	// Lire les limites par défaut
	limits := mm.GetLimits()
	if limits.RAMWarnPercent != 80 {
		t.Errorf("expected RAM warn 80, got %.0f", limits.RAMWarnPercent)
	}

	// Modifier les limites
	limits.RAMWarnPercent = 70
	limits.VRAMCriticalPercent = 90
	mm.SetLimits(limits)

	// Vérifier la mise à jour
	updated := mm.GetLimits()
	if updated.RAMWarnPercent != 70 {
		t.Errorf("expected RAM warn 70, got %.0f", updated.RAMWarnPercent)
	}
	if updated.VRAMCriticalPercent != 90 {
		t.Errorf("expected VRAM critical 90, got %.0f", updated.VRAMCriticalPercent)
	}
}

func TestParseMeminfoValue(t *testing.T) {
	tests := []struct {
		line     string
		expected float64
	}{
		{"MemTotal:       16384000 kB", 16000}, // 16384000 kB = 16000 MiB
		{"MemAvailable:    8192000 kB", 8000},  // 8192000 kB = 8000 MiB
		{"Invalid line", 0},
		{"", 0},
	}

	for _, tt := range tests {
		got := parseMeminfoValue(tt.line)
		if got != tt.expected {
			t.Errorf("parseMeminfoValue(%q) = %.0f, expected %.0f", tt.line, got, tt.expected)
		}
	}
}
