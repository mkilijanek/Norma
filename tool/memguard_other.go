//go:build !linux

package tool

// No /proc here: the guard is disabled (a nil source means newMemGuard returns nil).
func newProcSource() procSource { return nil }
