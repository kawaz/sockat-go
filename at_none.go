//go:build !freebsd

package sockat

// atSupported: no bindat / connectat here.
const atSupported = false

// atOp cannot be used here.
func atOp(op, int, string) (endpoint, error) { return nil, errUnusable }
