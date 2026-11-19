package sockat_test

import "golang.org/x/sys/unix"

// seccompFiltered reports whether the process runs under a seccomp filter
// (prctl(PR_GET_SECCOMP) other than 0), which rules thread-cwd out.
func seccompFiltered() bool {
	mode, err := unix.PrctlRetInt(unix.PR_GET_SECCOMP, 0, 0, 0, 0)
	return err == nil && mode != 0
}
