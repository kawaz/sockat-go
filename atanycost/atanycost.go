// Package atanycost binds a Unix domain socket at a long path at any cost:
// where sockat.Listen gives up with ENAMETOOLONG, it also goes through a
// symlink in a temporary place, and reports what that gave up.
//
// Importing this package is the opt-in: the default API (package sockat)
// never gives anything up beyond what its documentation describes. Use this
// only where reaching the path matters more than what is lost, and look at
// the Losses it reports.
package atanycost

import (
	"net"

	_ "github.com/kawaz/sockat-go" // sets anyhook.Listen
	"github.com/kawaz/sockat-go/internal/anyhook"
)

// Loss names something a route gives up compared with net.ListenUnix (SPEC
// "失うもの"). Its string is SPEC's name. The names are not ordered and none
// includes another. More names may be added later: an unknown one means only
// that something was lost, not what.
type Loss string

const (
	// Respelled: the kernel keeps a spelling of the path, not the path
	// (Addr() and the like report it), and the socket file is not removed
	// when the listener is closed: remove it by the path you passed.
	Respelled Loss = "respelled"
	// Forked: a fork's side effects. sockat-go has no fork route, so this
	// does not occur; it is here so the names match the other
	// implementations.
	Forked Loss = "forked"
	// RecreatableSpelling: the spelling the kernel keeps goes through a place
	// others can write to (a sticky /tmp). Once it is visible to them (Linux's
	// /proc/net/unix), another user can make it name a directory of their
	// choosing; removing the socket file by that spelling may then remove a
	// file there. Removing it by the path you passed is safe.
	RecreatableSpelling Loss = "recreatable-spelling"
	// CwdRelative: the path was relative and the standard library took it as
	// it is, so the kernel keeps a path relative to the cwd. Removing or
	// looking up the socket file by that spelling after the cwd has changed
	// reaches somewhere else. The standard library's own risk rather than
	// one a route brings; reported all the same. Removing it by the path made
	// absolute is safe.
	CwdRelative Loss = "cwd-relative"
)

// Result is what Listen reports beside the listener.
type Result struct {
	// Losses is what the route taken gave up: never nil on success, empty
	// when nothing was (the standard library took an absolute path). Branch
	// on these.
	Losses []Loss
	// Route is the name of the route taken (SPEC's route names, such as
	// "thread-cwd" or "symlink"). For diagnostics: the names are not a
	// promise and may change.
	Route string
	// Skipped is the routes tried before Route and found unusable, in the
	// order tried, each with why. For diagnostics, as Route.
	Skipped []Skip
}

// Has reports whether l is among the losses.
func (r Result) Has(l Loss) bool {
	for _, x := range r.Losses {
		if x == l {
			return true
		}
	}
	return false
}

// Skip is a route found unusable, and why (a short text, for diagnostics).
type Skip struct {
	Name   string
	Reason string
}

// Error is Listen's error: the route that bound and failed (empty when none
// did, as for ENAMETOOLONG when every route was unusable) and the routes
// found unusable before, for diagnostics as in Result. It wraps the
// *net.OpError the net package would return, so errors.Is(err,
// syscall.ENOENT) and errors.As to *net.OpError work through it.
type Error struct {
	Route   string
	Skipped []Skip
	Err     error
}

func (e *Error) Error() string { return e.Err.Error() }

// Unwrap returns the underlying *net.OpError.
func (e *Error) Unwrap() error { return e.Err }

// Listen is sockat.Listen, and where that ends in ENAMETOOLONG, a bind
// through a symlink to the socket's directory, <place>/sockat.XXXXXXXXXX/<name>,
// made in $XDG_RUNTIME_DIR, $TMPDIR or /tmp and removed right after. Places
// only the calling user can write to are tried first (Losses: Respelled);
// then shared ones, sticky and owned by root or the user (Losses: Respelled,
// RecreatableSpelling).
//
// The listener is the net package's own, as from sockat.Listen. The error is
// an *Error.
func Listen(path string) (*net.UnixListener, Result, error) {
	l, out, err := anyhook.Listen(path)
	skipped := make([]Skip, 0, len(out.Skipped))
	for _, s := range out.Skipped {
		skipped = append(skipped, Skip{Name: s.Name, Reason: s.Reason})
	}
	if err != nil {
		return nil, Result{}, &Error{Route: out.Route, Skipped: skipped, Err: err}
	}
	r := Result{Losses: []Loss{}, Route: out.Route, Skipped: skipped}
	for _, x := range out.Losses {
		r.Losses = append(r.Losses, Loss(x))
	}
	return l, r, nil
}
