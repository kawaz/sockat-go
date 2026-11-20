package sockat

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"syscall"
)

// ForBind is a spelling of a path to bind by yourself, made by
// ResolveForBind: hand Path to the net package (net.ListenUnix,
// net.ListenUnixgram, ...) in place of the path.
type ForBind struct {
	// Path is the spelling to bind: the path itself where the net package
	// takes it, otherwise a shorter spelling through B, D and E naming the
	// same place for as long as Spelling says.
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
	// Path is the spelling to connect to: the path itself where the net
	// package takes it, otherwise a shorter spelling through B, D and E
	// naming the same socket file until Cleanup.
	Path string
	// Route and Skipped are as Report's: for diagnostics, not a promise.
	Route   string
	Skipped []Skip

	clean *cleaner
}

// ResolveForBind spells path so that the net package can bind it: path
// itself where the net package takes it (Spelling Original, or CwdRelative
// for a relative path), otherwise a spelling through a directory of this
// user's processes (B, see Listen) that names the same place for as long as
// its Spelling says (Linked). Nothing is bound.
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
// EINVAL for a path that contains a NUL byte or for a long path that does
// not end with a file name.
func ResolveForBind(path string, opts Options) (*ForBind, error) {
	return defaultWorld.resolveForBind(path, opts)
}

// ResolveForConnect spells path so that the net package can connect to it:
// path itself where the net package takes it, otherwise a spelling through a
// directory of this user's processes (B, see Listen) and a symlink to the
// socket file, which names it until Cleanup. Nothing is connected, and
// nothing about the socket file is looked at: the spelling names whatever is
// at path when it is used.
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
	if f.Spelling.Kind == CwdRelative {
		if now, ok := statID("."); !ok || now != f.cwd {
			return nil, pathErr(errCwdChangedSinceResolve)
		}
	}
	// The spelling's directory is resolved to a real path and its last
	// component followed while it is a symlink: the E of link-file, and a
	// symlink of the caller's at the path, which bind(2) follows on some
	// systems (SPEC "ForBind の SocketFile"), so Remove leaves the caller's
	// symlink and removes the socket.
	file, err := realFile(f.Path)
	if err != nil {
		return nil, pathErr(errnoOf(err))
	}
	file, id, err := socketAt(file)
	if err != nil {
		return nil, pathErr(err)
	}
	return &SocketFile{file: file, id: id, clean: f.clean}, nil
}

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
// abstract name's leading one, are EINVAL whatever the length; a path the
// standard library accepts, and an abstract name, are spelt as they are; a
// longer one takes the spellings of the bind order (link-dir, then link-file
// where bind(2) is measured to follow a symlink: whether it follows cannot be
// told before the bind).
func (w *world) resolveBind(path string, opts Options, tr *trace) (*ForBind, error) {
	f := &ForBind{path: path, clean: &cleaner{}}
	s, short, err := shortSpelling(path, opts)
	if err != nil {
		return nil, err
	}
	if short {
		f.Path, f.Route, f.Spelling = s, string(routeStandard), Spelling{Kind: Original}
		if !abstractName(path) && !isAbs(path) {
			// A cwd that cannot be looked up now leaves the zero fileID,
			// which no cwd SocketFile looks up later has.
			f.Spelling.Kind = CwdRelative
			f.cwd, _ = statID(".")
		}
		return f, nil
	}
	c, err := w.newBindCall(path, opts)
	if err != nil {
		return nil, err
	}
	tr.add(routeStandard, "path too long")
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
		return nil, syscall.ENAMETOOLONG
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

// resolveConnect spells path for a connect (resolveForConnect), checked as
// resolveBind checks it: a longer path takes link-file only, through the
// process's D, B spelt by procB (where /proc can be used) or its real path.
func (w *world) resolveConnect(path string, opts Options, tr *trace) (*ForConnect, error) {
	f := &ForConnect{clean: &cleaner{}}
	s, short, err := shortSpelling(path, opts)
	if err != nil {
		return nil, err
	}
	if short {
		f.Path, f.Route = s, string(routeStandard)
		return f, nil
	}
	c, err := w.newBindCall(path, opts)
	if err != nil {
		return nil, err
	}
	tr.add(routeStandard, "path too long")
	target, ok := absTarget(path)
	if !ok {
		tr.add(routeLinkFile, "cwd unreadable")
		return nil, syscall.ENAMETOOLONG
	}
	ln, err := c.linkFileSpelling(target, hasProc)
	if errors.Is(err, errUnusable) {
		tr.add(routeLinkFile, reasonOf(err))
		return nil, syscall.ENAMETOOLONG
	}
	if err != nil {
		return nil, err
	}
	f.Path, f.Route, f.clean.e = ln.spelling, string(routeLinkFile), ln.e
	return f, nil
}

// shortSpelling is what a resolve does before the routes, as listen and dial
// do: EINVAL for an Options.Base of the wrong form or a NUL (an abstract
// name's leading one aside), whatever the length; the spelling, with short
// true, of an abstract name or a path the standard library accepts
// (stdSpelling).
func shortSpelling(path string, opts Options) (string, bool, error) {
	if opts.Base != "" {
		if err := givenShape(opts.Base); err != nil {
			return "", false, err
		}
	}
	if abstractName(path) {
		return path, true, nil
	}
	if strings.IndexByte(path, 0) >= 0 {
		return "", false, fmt.Errorf("socket path must not contain a NUL byte: %w", syscall.EINVAL)
	}
	if s := stdSpelling(path); len(s) <= directMax {
		return s, true, nil
	}
	return "", false, nil
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
