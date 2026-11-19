//go:build !linux

package sockat_test

// seccompFiltered: no seccomp here.
func seccompFiltered() bool { return false }
