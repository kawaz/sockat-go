//go:build !(linux || android || darwin || ios || freebsd)

package sockat

import "syscall"

// symlinkat is not needed here: the central route has no means on these
// systems, so D and R are never made.
func symlinkat(string, int, string) error { return syscall.ENOSYS }
