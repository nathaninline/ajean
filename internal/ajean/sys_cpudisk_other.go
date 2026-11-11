//go:build !linux

package ajean

// Processeur et stockage : relevés sous Linux seulement pour l'instant.
func cpuTelemetry() map[string]any  { return nil }
func diskTelemetry() map[string]any { return nil }
