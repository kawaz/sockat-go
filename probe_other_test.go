//go:build !(linux || darwin)

package sockat

// threadCwdAvailable: no per-thread working directories here (or, on
// FreeBSD, no route that would use them).
func threadCwdAvailable() bool { return false }
