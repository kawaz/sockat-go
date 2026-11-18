//go:build !(darwin || ios || linux || android || freebsd)

package sockat

import "os"

// threadCwdSupported: no per-thread working directories here.
const threadCwdSupported = false

// threadCwdRoute cannot be used here.
func threadCwdRoute(op, *os.File, string) (endpoint, error) { return nil, errUnusable }
