// Package atanycost binds a Unix domain socket at a long path at any cost:
// where sockat.Listen gives up with ENAMETOOLONG, it also goes through a
// symlink in a temporary place, and reports what that gave up.
//
// Importing this package is the opt-in: the default API (package sockat)
// never gives anything up beyond what its documentation describes. Use this
// only where reaching the path matters more than what is lost, and look at
// the Level it reports.
package atanycost

import (
	"net"

	_ "github.com/kawaz/sockat-go" // sets anyhook.Listen
	"github.com/kawaz/sockat-go/internal/anyhook"
)

// Level is what the route taken gave up compared with net.ListenUnix. The
// values are not ordered: Forked and SharedSymlink each include Respelled but
// not each other. More values may be added later: an unknown value means only
// that something was lost (not the standard library), not what.
type Level int

const (
	// None: the standard library took the path itself.
	None Level = iota
	// Respelled: the kernel keeps a spelling of the path, not the path
	// (Addr() and the like report it), and the socket file is not removed
	// when the listener is closed.
	Respelled
	// Forked: Respelled, and a fork's side effects. sockat-go has no fork
	// route, so this does not occur; it is here so the values match the
	// other implementations.
	Forked
	// SharedSymlink: Respelled, and the spelling the kernel keeps goes
	// through a place others can write to (a sticky /tmp), where another user
	// can make it name a directory of their choosing; something that removes
	// the socket file by that spelling may then remove a file there.
	SharedSymlink
)

func (l Level) String() string {
	switch l {
	case None:
		return "none"
	case Respelled:
		return "respelled"
	case Forked:
		return "forked"
	case SharedSymlink:
		return "shared-symlink"
	}
	return "unknown"
}

// Result is what Listen reports beside the listener.
type Result struct {
	// Level is what the route taken gave up.
	Level Level
	// Route is the name of the route taken (SPEC's route names, such as
	// "thread-cwd" or "symlink"). For diagnostics: the names are not a
	// promise and may change.
	Route string
	// Skipped is the routes tried before Route and found unusable, in the
	// order tried, each with why. For diagnostics, as Route.
	Skipped []Skip
}

// Skip is a route found unusable, and why (a short text, for diagnostics).
type Skip struct {
	Name   string
	Reason string
}

// Listen is sockat.Listen, and where that ends in ENAMETOOLONG, a bind
// through a symlink to the socket's directory, <place>/sockat.XXXXXXXXXX/<name>,
// made in $XDG_RUNTIME_DIR, $TMPDIR or /tmp and removed right after. Places
// only the calling user can write to are tried first (Level Respelled); then
// shared ones, sticky and owned by root or the user (Level SharedSymlink).
//
// The listener is the net package's own, as from sockat.Listen.
func Listen(path string) (*net.UnixListener, Result, error) {
	l, out, err := anyhook.Listen(path)
	if err != nil {
		return nil, Result{}, err
	}
	r := Result{Level: Level(out.Level), Route: out.Route}
	for _, s := range out.Skipped {
		r.Skipped = append(r.Skipped, Skip{Name: s.Name, Reason: s.Reason})
	}
	return l, r, nil
}
