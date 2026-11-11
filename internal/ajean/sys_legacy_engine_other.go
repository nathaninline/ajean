//go:build !linux && !windows && !darwin

package ajean

func retireLegacyEngine() {}

func legacyWarning() string { return "" }
