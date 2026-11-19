package sockat

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// LastResort selects whether ListenEx and DialEx may take the last resort: a
// route tried only where every default route is unusable, that reaches the
// path at the cost of something (see [Loss]). The default routes, and the
// order they are tried in, are the same either way.
//
// Pass the constants rather than a bare true or false, so the call says what
// it chooses:
//
//	l, discard, res, err := sockat.ListenEx(path, sockat.UseLastResort)
type LastResort bool

const (
	// NoLastResort tries the default routes only, as Listen and Dial do.
	NoLastResort LastResort = false
	// UseLastResort tries the last resort after the default routes: for
	// ListenEx, a bind through a symlink to the socket's directory in a
	// temporary place (symlink-dir); for DialEx, a fork, which Go cannot make
	// (so DialEx reaches what Dial reaches).
	UseLastResort LastResort = true
)

// Loss names something a route gives up compared with the net package
// (SPEC "失うもの"). Its string is SPEC's name. The names are not ordered and
// none includes another. More names may be added later: an unknown one means
// only that something was lost, not what.
type Loss string

const (
	// Respelled: the kernel keeps a spelling of the path, not the path
	// (Addr() and the like report it), and the socket file is not removed
	// when the listener is closed: remove it by the path you passed, or with
	// ListenEx's discard.
	Respelled Loss = "respelled"
	// CwdRelative: the path was relative and the net package took it as it
	// is, so the kernel keeps a path relative to the cwd. Removing or looking
	// up the socket file by that spelling after the cwd has changed reaches
	// somewhere else. The net package's own risk rather than one a route
	// brings; reported all the same. Removing it by the path made absolute,
	// or with discard, is safe.
	CwdRelative Loss = "cwd-relative"
	// Forked: a fork's side effects. sockat-go has no fork route, so this
	// does not occur; it is here so the names match the other
	// implementations.
	Forked Loss = "forked"
	// RecreatableSpelling: the spelling the kernel keeps goes through a place
	// others can write to (a sticky /tmp). Once it is visible to them (Linux's
	// /proc/net/unix), another user can make it name a directory of their
	// choosing; removing the socket file by that spelling may then remove a
	// file there. Removing it by the path you passed, or with discard, is
	// safe. Only ListenEx's last resort gives this up.
	RecreatableSpelling Loss = "recreatable-spelling"
)

// Result is what ListenEx and DialEx report beside the listener or the
// connection.
type Result struct {
	// Losses is what the route taken gave up, in no particular order: never
	// nil, empty when nothing was. Branch on these.
	Losses []Loss
	// Route is the name of the route taken (SPEC's route names, such as
	// "standard", "thread-cwd" or "symlink-file"). For diagnostics: the names
	// are not a promise and may change.
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

// RouteError is the error of ListenEx and DialEx: the route that bound /
// connected and failed (empty when none did: every route was unusable, which
// is ENAMETOOLONG, or the call failed before any route, such as ENOENT for a
// missing directory) and the routes found unusable before it, for diagnostics
// as in Result. It wraps the *net.OpError Listen / Dial would return, so
// errors.Is(err, syscall.ENOENT) and errors.As to *net.OpError work through
// it.
type RouteError struct {
	Route   string
	Skipped []Skip
	Err     error
}

func (e *RouteError) Error() string { return e.Err.Error() }

// Unwrap returns the underlying *net.OpError.
func (e *RouteError) Unwrap() error { return e.Err }

// ListenEx is Listen, with a function that removes the socket file later and
// what the route taken gave up. With UseLastResort, where every route of
// Listen is unusable (ENAMETOOLONG), it binds through a symlink to the
// socket's directory, <place>/sockat.XXXXXXXXXX/<name>, made in
// $XDG_RUNTIME_DIR, $TMPDIR or /tmp and removed right after: places only the
// calling user can write to first (Losses: Respelled), then shared ones,
// sticky and owned by root or the user (Losses: Respelled,
// RecreatableSpelling).
//
// Right after the bind it records the socket file (by its real path and
// (st_dev, st_ino)); discard removes the file only while that path still
// names that same file, so a socket that someone else has bound at the path
// since is left alone. Between discard's check and the removal, a replacement
// cannot be told apart (POSIX has no removal by inode). When the file cannot
// be recorded, the listener is closed, the file is left, and an error is
// returned; so too for a relative path when the cwd changes (another
// goroutine's os.Chdir) between the resolving of its real path and the bind.
//
// discard does not close the listener: close it yourself (before or after).
// It returns nil when the file is removed, is gone, or is no longer the one
// bound; an error when the file cannot be checked (lstat fails otherwise than
// for a missing file, for example EACCES) or the removal fails. Calling it
// again does nothing. The listener does not remove the socket file on Close
// (short paths included), so the removal is discard's alone.
//
// The error is a *RouteError.
func ListenEx(path string, lr LastResort) (l *net.UnixListener, discard func() error, res Result, err error) {
	return listenEx(path, lr, nil)
}

// DialEx is Dial, with what the route taken gave up (nothing: a connect
// leaves no spelling behind) and the routes tried. Its last resort is a fork,
// which Go cannot make: with UseLastResort, where every route of Dial is
// unusable, the fork route is listed in Skipped and the error is still
// ENAMETOOLONG, so DialEx reaches what Dial reaches.
//
// The error is a *RouteError.
func DialEx(path string, lr LastResort) (*net.UnixConn, Result, error) {
	ep, res, err := dialReported(path, lr, nil)
	if err != nil {
		return nil, Result{}, err
	}
	return ep.(*net.UnixConn), res, nil
}

// listenEx is ListenEx with the environment's inputs given as cfg (nil reads
// them from the environment, for a long path only).
func listenEx(path string, lr LastResort, cfg *config) (*net.UnixListener, func() error, Result, error) {
	// An abstract name has no file to record or remove.
	file, ferr := "", error(nil)
	// A relative path is resolved against the cwd twice, by realFile and by
	// the bind, and the cwd is the process's: another goroutine may change it
	// in between. It is read before the first and compared after the bind.
	rel := !abstractName(path) && !isAbs(path)
	var cwd fileID
	cwdOK := true
	if !abstractName(path) {
		if rel {
			cwd, cwdOK = statID(".")
		}
		// The real path is resolved before the bind, as the bind resolves
		// it; the file's identity is read the moment the bind returns.
		file, ferr = realFile(path)
	}
	ep, res, err := bindEx(path, lr, cfg)
	if err != nil {
		return nil, nil, Result{}, err
	}
	l := ep.(*net.UnixListener)
	l.SetUnlinkOnClose(false)
	if abstractName(path) {
		return l, func() error { return nil }, res, nil
	}
	var id fileID
	ok := ferr == nil && cwdOK
	if ok {
		id, ok = lstatID(file)
	}
	if ok && rel {
		// Another cwd means the bind may have resolved path elsewhere than
		// file, which would then name an unrelated file of the same name.
		// (A cwd changed and changed back is not seen.)
		now, same := statID(".")
		ok = same && now == cwd
	}
	if !ok {
		l.Close()
		return nil, nil, Result{}, &RouteError{Route: res.Route, Skipped: res.Skipped, Err: opError(opListen, path, errNotRecorded)}
	}
	return l, discarder(file, id), res, nil
}

// bindEx is the bind of listenEx.
//
// Design rationale: a variable rather than a direct call to listenReported
// only so that a test (TestListenExWhoseCwdChangesBeforeTheBindRecordsNothing,
// in a child process) can change the cwd between realFile and the bind, which
// no input of listenEx can bring about. Nothing else assigns it.
var bindEx = listenReported

// listenReported binds as Listen does, recording the routes tried and, with
// lr, taking the last resort (symlink-dir) where every route is unusable.
func listenReported(path string, lr LastResort, cfg *config) (endpoint, Result, error) {
	tr := &trace{}
	ep, r, err := runTraced(opListen, path, cfg, tr)
	if err == nil {
		return ep, Result{Losses: bindLosses(r, path), Route: string(r), Skipped: tr.skips()}, nil
	}
	if lr != UseLastResort || !noRoute(r, tr, err) {
		return nil, Result{}, &RouteError{Route: string(r), Skipped: tr.skips(), Err: err}
	}
	// The routes were tried (so path is long and has a directory that was
	// looked up): the environment's inputs are read now if not given.
	if cfg == nil {
		cfg = configFromEnv()
	}
	ep, shared, err2 := lastResortBind(path, cfg.dirBases)
	switch {
	case errors.Is(err2, errUnusable):
		tr.add(routeSymlinkDir, reasonOf(err2))
		return nil, Result{}, &RouteError{Skipped: tr.skips(), Err: err}
	case err2 != nil:
		return nil, Result{}, &RouteError{Route: string(routeSymlinkDir), Skipped: tr.skips(), Err: opError(opListen, path, err2)}
	}
	losses := []Loss{Respelled}
	if shared {
		losses = append(losses, RecreatableSpelling)
	}
	return ep, Result{Losses: losses, Route: string(routeSymlinkDir), Skipped: tr.skips()}, nil
}

// lastResortBind is symlink-dir for path: the bind by
// <place>/sockat.XXXXXXXXXX/<name>, the symlink pointing to the real path of
// the socket's directory.
func lastResortBind(path string, bases []string) (endpoint, bool, error) {
	dir, name, err := splitPath(path)
	if err != nil {
		return nil, false, unusable("path not split")
	}
	real, err := realDir(dir)
	if err != nil {
		return nil, false, unusable("socket directory not resolved")
	}
	return symlinkDir(real, name, bases)
}

// dialReported connects as Dial does, recording the routes tried and, with
// lr, naming the last resort (fork-cwd-direct, which Go cannot make) where
// every route is unusable.
func dialReported(path string, lr LastResort, cfg *config) (endpoint, Result, error) {
	tr := &trace{}
	ep, r, err := runTraced(opDial, path, cfg, tr)
	if err == nil {
		return ep, Result{Losses: []Loss{}, Route: string(r), Skipped: tr.skips()}, nil
	}
	if lr == UseLastResort && noRoute(r, tr, err) {
		tr.add(routeForkCwdDirect, noForkRoute)
	}
	return nil, Result{}, &RouteError{Route: string(r), Skipped: tr.skips(), Err: err}
}

// noRoute reports whether a bind / connect failed because every route was
// unusable: ENAMETOOLONG with no route that bound / connected, after the
// routes were tried (standard, the first, is in the trace; a failure before
// the routes, such as a directory past PATH_MAX, leaves the trace empty).
func noRoute(r route, tr *trace, err error) bool {
	return r == "" && len(tr.skipped) > 0 && errors.Is(err, syscall.ENAMETOOLONG)
}

// bindLosses is what a bind of path through r gave up: for standard,
// nothing, or cwd-relative when path is relative (the kernel keeps it as
// given, to be resolved against whatever the cwd is later; an abstract name
// is no path); respelled for the others. symlink-dir in a shared place adds
// recreatable-spelling (listenReported).
func bindLosses(r route, path string) []Loss {
	if r == routeStandard {
		if !isAbs(path) && !abstractName(path) {
			return []Loss{CwdRelative}
		}
		return []Loss{}
	}
	return []Loss{Respelled}
}

// skips is the trace's routes as Skipped reports them: never nil.
func (t *trace) skips() []Skip {
	out := make([]Skip, 0, len(t.skipped))
	for _, s := range t.skipped {
		out = append(out, Skip{Name: string(s.name), Reason: s.reason})
	}
	return out
}

// errNotRecorded is ListenEx's error when the socket file it bound cannot be
// looked up to be recorded, or, for a relative path, may not be the one at the
// path recorded (the cwd changed before the bind).
var errNotRecorded = errors.New("the socket file bound could not be recorded for its removal; the listener was closed and the file left")

// discarder returns ListenEx's discard for the socket file recorded as file
// and id.
func discarder(file string, id fileID) func() error {
	var once sync.Once
	return func() error {
		var err error
		once.Do(func() {
			var st unix.Stat_t
			if e := unix.Lstat(file, &st); e != nil {
				// Gone (or a component no longer a directory): nothing to
				// remove. Any other failure (EACCES, ...) leaves it unknown
				// whether the file is the one bound, and is returned.
				if !errors.Is(e, unix.ENOENT) && !errors.Is(e, unix.ENOTDIR) {
					err = &os.PathError{Op: "lstat", Path: file, Err: e}
				}
				return
			}
			if (fileID{uint64(st.Dev), uint64(st.Ino)}) != id {
				return
			}
			// Gone in the meantime is as good as removed.
			if err = os.Remove(file); errors.Is(err, os.ErrNotExist) {
				err = nil
			}
		})
		return err
	}
}

// realFile is the socket file at path with its directory resolved to a real
// path, so a later change of the cwd or of a symlink on the way does not
// retarget it.
func realFile(path string) (string, error) {
	dir, name, err := splitPath(path)
	if err != nil {
		return "", err
	}
	real, err := realDir(dir)
	if err != nil {
		return "", err
	}
	return filepath.Join(real, name), nil
}

// lstatID is (st_dev, st_ino) of path itself (a symlink is not followed).
func lstatID(path string) (fileID, bool) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return fileID{}, false
	}
	return fileID{uint64(st.Dev), uint64(st.Ino)}, true
}
