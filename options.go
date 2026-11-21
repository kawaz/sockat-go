package sockat

// Options is what ListenEx and DialEx take besides the path. The zero value
// is the default: ListenEx and DialEx then try the routes Listen and Dial do.
type Options struct {
	// UsableCrossUID asks for a spelling a peer of another uid can follow:
	// a B where B and every directory on the way to it have search permission
	// for others, and no /proc spelling. Where there is none, a bind does not
	// fall back to a spelling others cannot follow but goes on to the routes
	// whose spelling is Dangling. Whether it was met shows in Bound.Spelling.
	// Only the bind looks at it: the spelling of a connect stays with no
	// peer.
	UsableCrossUID bool
	// Base is the path of B itself (for example /tmp/app-1000/sockat, a
	// place the application manages), "" for none: B is made there instead of
	// in /tmp, $XDG_RUNTIME_DIR or $TMPDIR, and a check that fails is an
	// error rather than a fall back to those places. It must be absolute,
	// end with a name and hold no NUL (EINVAL otherwise, whatever the length of the path);
	// for a long path, a missing parent is ENOENT, a parent or B that is not
	// a directory ENOTDIR, and a B that is a symlink or that others could
	// replace, or a directory on the way that others could replace, EPERM
	// (with UsableCrossUID of a bind, also one without search permission for
	// others). A short path does not look further at it.
	Base string
}

// Bound is what ListenEx reports besides the listener.
type Bound struct {
	// File removes the socket file and cleans up the spelling's E.
	File *SocketFile
	// Spelling is what the spelling the kernel keeps (the listener's Addr())
	// is good for afterwards. Branch on it.
	Spelling Spelling
	// Route is the name of the route taken (such as "standard", "link-dir",
	// "thread-cwd"). For diagnostics: the names are not a promise.
	Route string
	// Base is which B the spelling goes through ("proc", "tmp", "runtime",
	// "tmpdir", "sockdir", "given"), "" when it goes through none. For
	// diagnostics, as Route.
	Base string
	// Skipped is the routes tried before Route and found unusable, in the
	// order tried, each with why: never nil. For diagnostics, as Route.
	Skipped []Skip
}

// Report is what DialEx reports besides the connection: the route taken and
// the routes found unusable before it (never nil). For diagnostics: the names
// are not a promise.
type Report struct {
	Route   string
	Skipped []Skip
}

// Skip is a route found unusable, and why (a short text, for diagnostics).
type Skip struct {
	Name   string
	Reason string
}

// RouteError is the error of ListenEx and DialEx: the route that bound /
// connected and failed (empty when none did: every route was unusable, which
// is ENAMETOOLONG, or the call failed before any route, such as ENOENT for a
// missing directory or an Options.Base that fails its check) and the routes
// found unusable before it, for diagnostics. It wraps the *net.OpError Listen
// / Dial would return, so errors.Is(err, syscall.ENOENT) and errors.As to
// *net.OpError work through it.
type RouteError struct {
	Route   string
	Skipped []Skip
	Err     error
}

func (e *RouteError) Error() string { return e.Err.Error() }

// Unwrap returns the underlying *net.OpError.
func (e *RouteError) Unwrap() error { return e.Err }

// skips is the trace's routes as Skipped reports them: never nil.
func (t *trace) skips() []Skip {
	out := make([]Skip, 0, len(t.skipped))
	for _, s := range t.skipped {
		out = append(out, Skip{Name: string(s.name), Reason: s.reason})
	}
	return out
}
