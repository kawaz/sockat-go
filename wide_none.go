//go:build !(darwin || ios || freebsd)

package sockat

// wideUsable: no widened sockaddr here.
func wideUsable(op) bool { return false }

// wideMax: no widened sockaddr here, so the plain limit.
const wideMax = directMax

// wideOp cannot be used here.
func wideOp(op, string) (endpoint, error) { return nil, errUnusable }

// refusesWide: there is no widened form to refuse.
func refusesWide(error) bool { return false }
