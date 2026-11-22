package sockat

import (
	"errors"
	"os"
	"strings"
	"syscall"
)

// ForBind is a spelling of a path to bind by yourself, made by
// ResolveForBind: hand Path to the net package (net.ListenUnix,
// net.ListenUnixgram, ...) in place of the path.
type ForBind struct {
	// Path is the spelling to bind: an absolute path naming the same place
	// where the net package takes it (the path itself, when absolute),
	// otherwise a shorter spelling through B, D and E naming the same place
	// for as long as Spelling says.
	Path string
	// Spelling is what Path is good for afterwards (it is the spelling the
	// kernel keeps for the socket bound by it). Branch on it.
	Spelling Spelling
	// Route, Base and Skipped are as Bound's: for diagnostics, not a
	// promise.
	Route   string
	Base    string
	Skipped []Skip

	path  string // the caller's path
	cwd   fileID // the cwd at the resolve, for a CwdRelative spelling
	clean *cleaner
}

// ForConnect is a spelling of a path to connect to by yourself, made by
// ResolveForConnect: hand Path to the net package (net.DialUnix,
// (*net.UnixConn).WriteToUnix, ...) in place of the path, as many times as
// you like, and call Cleanup once done.
type ForConnect struct {
	// Path is the spelling to connect to: an absolute path naming the same
	// socket file where the net package takes it (the path itself, when
	// absolute), otherwise a shorter spelling through B, D and E naming the
	// same socket file for as long as Spelling says.
	Path string
	// Spelling is what Path is good for afterwards, for as long as you keep
	// it: Original (an absolute path), CwdRelative (the path itself,
	// relative: no absolute spelling could be used, and it names another
	// socket once the cwd changes) or Linked (through B, D and E: Removable
	// is false, as Path names E, a symlink to the socket file; Until says how
	// long it can be followed).
	Spelling Spelling
	// Route and Skipped are as Report's: for diagnostics, not a promise.
	Route   string
	Skipped []Skip

	clean *cleaner
}

// ResolveForBind spells path so that the net package can bind it: path
// itself where the net package takes it, and for a relative path, or a
// longer one, an absolute path naming the same place that the net package
// takes (its directory's real path, or $PWD's spelling of it, with the file
// name; Spelling Original); otherwise a spelling through a directory of this
// user's processes (B, see Listen) that names the same place for as long as
// its Spelling says (Linked). Nothing is bound. The spelling of a relative
// path does not depend on the cwd, as Listen binds it, except where nothing
// else can be used: then it is the relative path itself (CwdRelative).
//
// Bind Path yourself, then call SocketFile for a SocketFile that removes the
// socket file; call Cleanup (or the SocketFile's Remove or Cleanup) once the
// spelling is no longer needed. A path that only a spelling relative to a
// directory held open could reach (Listen's Dangling routes) is
// ENAMETOOLONG: use Listen or ListenEx for it.
//
// Options are as ListenEx takes them. The error is a *RouteError whose Err
// is an *os.PathError (Op "resolve", Path path): for example ENOENT when the
// directory of path does not exist, ENAMETOOLONG when no spelling fits,
// EINVAL for an empty path or a path that contains a NUL byte or for a long path that does
// not end with a file name.
func ResolveForBind(path string, opts Options) (*ForBind, error) {
	return defaultWorld.resolveForBind(path, opts)
}

// ResolveForConnect spells path so that the net package can connect to it,
// as ResolveForBind does (Spelling tells what the spelling is good for): path
// itself where the net package takes it, and for a relative path, or a
// longer one, an absolute path naming the same socket file that the net
// package takes (Original); otherwise a spelling through a directory of this
// user's processes (B, see Listen) and a symlink to the socket file, which
// names it until Cleanup (Linked), or, where nothing else can be used, the
// relative path itself (CwdRelative). Nothing is connected, and nothing
// about the socket file is looked at: the spelling names whatever is at path
// when it is used. Unlike Dial, which connects through a relative path as it
// is, it spells a relative path absolute: the spelling may be used after the
// cwd changed.
//
// Options.Base is as DialEx takes it; UsableCrossUID is not looked at (the
// spelling of a connect stays with no peer). The error is a *RouteError
// whose Err is an *os.PathError (Op "resolve", Path path): ENAMETOOLONG
// when no spelling fits (or no B can be made), and otherwise as
// ResolveForBind's.
func ResolveForConnect(path string, opts Options) (*ForConnect, error) {
	return defaultWorld.resolveForConnect(path, opts)
}

// Cleanup removes the E the spelling goes through: Path then names nothing
// (do not use it afterwards). B and D stay. It does nothing for a spelling
// that goes through no E. It is the same cleanup as that of every SocketFile
// f.SocketFile gave: whichever succeeds first removes the E, and the others
// then do nothing. The error is an *os.PathError. After a success, calling
// it again does nothing and returns nil; after a failure, the next call
// tries again. Concurrent calls, with those of the SocketFiles' Remove and
// Cleanup, remove one at a time.
func (f *ForBind) Cleanup() error { return f.clean.cleanup() }

// Cleanup removes the E the spelling goes through: Path then names nothing
// (do not use it afterwards). B and D stay. It does nothing for a spelling
// that goes through no E. The error is an *os.PathError. After a success,
// calling it again does nothing and returns nil; after a failure, the next
// call tries again. Concurrent calls unlink one at a time.
func (f *ForConnect) Cleanup() error { return f.clean.cleanup() }

// SocketFile records the socket file bound by f.Path (its real path and
// (st_dev, st_ino), found by following f.Path now) and returns a SocketFile
// whose Remove removes it while the path still names it, as ListenEx's does,
// and whose Cleanup is f's. A symlink at the last component of the path
// (which bind(2) follows on macOS and NetBSD) is followed to the socket it
// leads to: that is what Remove removes, and the symlink stays.
//
// Call it after the bind and before closing the socket: a replacement
// between the bind and this call cannot be told apart. The error is an
// *os.PathError (Op "socketfile", Path the caller's path): the spelling does
// not lead to a file (ENOENT before the bind), what it leads to is not a
// socket (ENOTSOCK: a file that was at the path before the bind, for
// example), or, for a CwdRelative spelling, the cwd is no longer the one of
// the resolve. A spelling of an abstract socket name has no file: its
// SocketFile's Remove does nothing.
func (f *ForBind) SocketFile() (*SocketFile, error) {
	if abstractName(f.Path) {
		return &SocketFile{clean: f.clean}, nil
	}
	pathErr := func(err error) error { return &os.PathError{Op: "socketfile", Path: f.path, Err: err} }
	// The spelling's directory is resolved to a real path and its last
	// component followed while it is a symlink: the E of link-file, and a
	// symlink of the caller's at the path, which bind(2) follows on some
	// systems (SPEC "ForBind の SocketFile"), so Remove leaves the caller's
	// symlink and removes the socket.
	file, err := socketFileReal(f.Path)
	// A relative spelling is resolved against the process's cwd, which
	// another goroutine may change at any time: the cwd is compared with the
	// resolve's after the real path is found, so a change up to then is seen
	// (a cwd changed and changed back is not). The real path is absolute, and
	// nothing after this goes through the cwd.
	if f.Spelling.Kind == CwdRelative {
		if now, ok := statID("."); !ok || now != f.cwd {
			return nil, pathErr(errCwdChangedSinceResolve)
		}
	}
	if err != nil {
		return nil, pathErr(errnoOf(err))
	}
	file, id, err := socketAt(file)
	if err != nil {
		return nil, pathErr(err)
	}
	return &SocketFile{file: file, id: id, clean: f.clean}, nil
}

// socketFileReal is the realFile of ForBind.SocketFile.
//
// Design rationale: a variable rather than a direct call only so that a test
// (TestSocketFileOfACwdRelativeResolveChecksTheCwdAfterTheRealPath, in a
// child process) can change the cwd between SocketFile's start and the real
// path, which no input of SocketFile can bring about. Nothing else assigns
// it.
var socketFileReal = realFile

// errCwdChangedSinceResolve is ForBind.SocketFile's error for a relative
// spelling when the cwd changed since the resolve: the path may name an
// unrelated file of the same name.
var errCwdChangedSinceResolve = errors.New("the current directory changed since the path was resolved; the socket file it names may be another")

// resolveForBind is ResolveForBind on w.
func (w *world) resolveForBind(path string, opts Options) (*ForBind, error) {
	tr := &trace{}
	f, err := w.resolveBind(path, opts, tr)
	if err != nil {
		return nil, &RouteError{Skipped: tr.skips(), Err: resolveError(path, err)}
	}
	f.Skipped = tr.skips()
	return f, nil
}

// resolveBind spells path for a bind (resolveForBind), checking it as
// listen does: an Options.Base of the wrong form, a NUL other than an
// abstract name's leading one, are EINVAL whatever the length; an absolute
// path the standard library accepts, and an abstract name, are spelt as they
// are, a relative path by its first absolute candidate that fits (resolveStd);
// otherwise, after a long absolute path's real-path candidate, the spellings
// of the bind order (link-dir, then link-file where bind(2) is measured to
// follow a symlink: whether it follows cannot be told before the bind).
// A relative path for which none can be used is spelt as it is when it fits
// (CwdRelative), as bind binds it.
func (w *world) resolveBind(path string, opts Options, tr *trace) (*ForBind, error) {
	f := &ForBind{path: path, clean: &cleaner{}}
	if !abstractName(path) && !isAbs(path) {
		// The cwd for a CwdRelative spelling's SocketFile to compare with,
		// read before anything looks the path up. A cwd that cannot be
		// looked up leaves the zero fileID, which no cwd SocketFile looks up
		// later has.
		f.cwd, _ = statID(".")
	}
	c, s, kind, err := w.resolveCall(path, opts, tr)
	if err != nil {
		return nil, err
	}
	if c == nil {
		f.Path, f.Route, f.Spelling = s, string(routeStandard), Spelling{Kind: kind}
		return f, nil
	}
	ln, err := c.linkDirSpelling()
	r := routeLinkDir
	if errors.Is(err, errUnusable) && w.cfg.bindFollows == followsYes {
		tr.add(routeLinkDir, reasonOf(err))
		r = routeLinkFile
		if target, ok := absTarget(path); ok {
			ln, err = c.linkFileSpelling(target, false)
		} else {
			err = unusable("cwd unreadable")
		}
	}
	if errors.Is(err, errUnusable) {
		tr.add(r, reasonOf(err))
		s, err := asIsOrTooLong(path, tr)
		if err != nil {
			return nil, err
		}
		f.Path, f.Route, f.Spelling = s, string(routeStandard), Spelling{Kind: CwdRelative}
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	f.Path, f.Spelling, f.Route, f.Base, f.clean.e = ln.spelling, ln.sp, string(r), ln.base, ln.e
	return f, nil
}

// resolveForConnect is ResolveForConnect on w.
func (w *world) resolveForConnect(path string, opts Options) (*ForConnect, error) {
	tr := &trace{}
	f, err := w.resolveConnect(path, Options{Base: opts.Base}, tr)
	if err != nil {
		return nil, &RouteError{Skipped: tr.skips(), Err: resolveError(path, err)}
	}
	f.Skipped = tr.skips()
	return f, nil
}

// resolveConnect spells path for a connect (resolveForConnect), checked and
// made absolute as resolveBind does it: a path that takes the routes takes
// link-file only, through the process's D, B spelt by procB (where /proc can
// be used) or its real path.
func (w *world) resolveConnect(path string, opts Options, tr *trace) (*ForConnect, error) {
	f := &ForConnect{clean: &cleaner{}}
	c, s, kind, err := w.resolveCall(path, opts, tr)
	if err != nil {
		return nil, err
	}
	if c == nil {
		f.Path, f.Route, f.Spelling = s, string(routeStandard), Spelling{Kind: kind}
		return f, nil
	}
	var ln linked
	if target, ok := absTarget(path); ok {
		ln, err = c.linkFileSpelling(target, hasProc)
	} else {
		err = unusable("cwd unreadable")
	}
	if errors.Is(err, errUnusable) {
		tr.add(routeLinkFile, reasonOf(err))
		s, err := asIsOrTooLong(path, tr)
		if err != nil {
			return nil, err
		}
		f.Path, f.Route, f.Spelling = s, string(routeStandard), Spelling{Kind: CwdRelative}
		return f, nil
	}
	if err != nil {
		return nil, err
	}
	f.Path, f.Route, f.Spelling, f.clean.e = ln.spelling, string(routeLinkFile), ln.sp, ln.e
	return f, nil
}

// resolveStd is what a resolve does before the routes, as bind does: EINVAL
// for an Options.Base of the wrong form, an empty path or a NUL (an abstract
// name's leading one aside), whatever the length; the spelling and its kind
// (Original) of an abstract name, an absolute path the standard library
// accepts, and the first absolute candidate of a relative path that fits
// (relCandidate); a short relative path not ending with a file name as it is
// (stdSpelling, CwdRelative), having no candidate. Kind 0 for a path that
// takes the routes.
func resolveStd(path string, opts Options) (string, SpellingKind, error) {
	if opts.Base != "" {
		if err := givenShape(opts.Base); err != nil {
			return "", 0, err
		}
	}
	if path == "" {
		return "", 0, errEmptyPath
	}
	if abstractName(path) {
		return path, Original, nil
	}
	if strings.IndexByte(path, 0) >= 0 {
		return "", 0, errNULInPath
	}
	if isAbs(path) {
		if len(path) <= directMax {
			return path, Original, nil
		}
		return "", 0, nil
	}
	if dir, name, err := splitPath(path); err != nil {
		if fitsAsIs(path) {
			return stdSpelling(path), CwdRelative, nil
		}
	} else if s := relCandidate(dir, name); s != "" {
		return s, Original, nil
	}
	return "", 0, nil
}

// resolveCall is a resolve of path up to its routes: what resolveStd spells,
// or else the call of the routes (newBindCall), with a long absolute path's
// real-path candidate spelt when it fits, and Options.Base checked
// otherwise. With no call, the spelling and its kind are returned: a relative
// path for which no route can be used (its directory has no real path within
// PATH_MAX) is spelt as it is (asIsOrTooLong).
func (w *world) resolveCall(path string, opts Options, tr *trace) (*bindCall, string, SpellingKind, error) {
	if s, kind, err := resolveStd(path, opts); err != nil || kind != 0 {
		return nil, s, kind, err
	}
	c, err := w.newBindCall(path, opts)
	if errors.Is(err, errUnusable) {
		s, err := asIsOrTooLong(path, tr)
		return nil, s, CwdRelative, err
	}
	if err != nil {
		return nil, "", 0, err
	}
	if s := c.realCandidate(); s != "" {
		return nil, s, Original, nil
	}
	if err := c.checkBase(); err != nil {
		return nil, "", 0, err
	}
	if isAbs(path) {
		tr.add(routeStandard, "path too long")
	} else {
		tr.add(routeStandard, "no absolute path fits")
	}
	return c, "", 0, nil
}

// asIsOrTooLong is the spelling of a resolve of path whose routes are all
// unusable: a relative path as it is (stdSpelling, with standard, the route
// taken, out of tr) when it fits (SPEC "相対パスを絶対パスにする" 5),
// otherwise ENAMETOOLONG.
func asIsOrTooLong(path string, tr *trace) (string, error) {
	if !isAbs(path) && fitsAsIs(path) {
		tr.dropStandard()
		return stdSpelling(path), nil
	}
	return "", syscall.ENAMETOOLONG
}

// resolveError is the error of a resolve of path: an *os.PathError of Op
// "resolve" around err (the errno itself for a system call's error).
func resolveError(path string, err error) error {
	var se *os.SyscallError
	if errors.As(err, &se) {
		err = se.Err
	}
	return &os.PathError{Op: "resolve", Path: path, Err: err}
}
