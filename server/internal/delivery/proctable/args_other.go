//go:build !linux && !darwin

package proctable

// Args is unavailable on this platform.
func Args(int) ([]string, bool) { return nil, false }
