//go:build !(darwin || ios || linux || android)

package sockat

// threadCwdPossible: no per-thread working directories here.
func threadCwdPossible() bool { return false }

// inThreadCwd cannot be used here.
func inThreadCwd(op, int, string) (endpoint, error) { return nil, errUnusable }
