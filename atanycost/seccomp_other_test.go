//go:build !linux

package atanycost_test

// seccompFiltered: no seccomp here.
func seccompFiltered() bool { return false }
