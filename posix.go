//go:build !freebsd

package sockat

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// The fallback routes of a dial (SPEC "予備の経路"), for when the central
// route cannot be used: a connect leaves no spelling behind with the peer (the
// connecting socket is not bound), so these spell the path without a random
// part. A bind never takes them.

// posixFallbackDial tries, in order, for a path longer than directMax whose
// directory part is dir and file name name:
//
//  1. (withRelative) the path spelled relative to the cwd, when that fits;
//  2. for each of cfg.tmpBases: a symlink to the socket file in it when it is
//     a safe place (symlinkRoute), or, when it is not, a directory of our own
//     in it held by its fd (heldLinkRoute: Linux with /proc only).
//
// A failure of a route ends the search with that error; errUnusable when no
// route can be used.
func posixFallbackDial(path, dir string, withRelative bool, cfg *config) (endpoint, route, error) {
	if withRelative {
		if rel := newRelative(path, dir); rel != nil {
			ep, err := rel.run(func(s string) (endpoint, error) { return viaStd(opDial, s, false) })
			return ep, routeRelative, err
		}
	}
	for _, base := range cfg.tmpBases {
		var ep endpoint
		var r route
		var err error
		if !safeBase(base) {
			ep, err = heldLinkRoute(base, path, cfg.procSelfFD)
			r = routeHeldLink
		} else {
			ep, r, err = symlinkRoute(base, path)
		}
		switch {
		case err == nil:
			return ep, r, nil
		case errors.Is(err, errUnusable):
		default:
			return nil, "", err
		}
	}
	return nil, "", errUnusable
}

// relative is a relative spelling of an absolute socket path, checked to
// name the same directory as the absolute one.
type relative struct {
	spelling string
	cwd      fileID // the cwd when the spelling was made
}

// cwdSpellings returns the spellings of the cwd that the path may start with:
// what getcwd(3) reports (a real path), and $PWD (the path the shell used to
// get there, possibly through symlinks) but only when it names the cwd.
//
// $PWD is just an environment variable: it is stale after a chdir(2) that did
// not update it, and anyone can set it. It is used only when stat($PWD) and
// stat(".") agree on (st_dev, st_ino); otherwise cutting it off the path would
// spell a directory relative to the wrong place.
func cwdSpellings(cwd string, cwdID fileID) []string {
	spellings := []string{cwd}
	if pwd := os.Getenv("PWD"); isAbs(pwd) && pwd != cwd {
		if id, ok := statID(pwd); ok && id == cwdID {
			spellings = append(spellings, pwd)
		}
	}
	return spellings
}

// newRelative spells path (whose directory part is dir) relative to the cwd.
//
// Two ways, the first one first:
//
//  1. Below the cwd. When path starts, component by component, with a
//     spelling of the cwd (getcwd(3), or a $PWD that names the cwd), cut that
//     prefix off. The rest only descends from the cwd along path itself, so it
//     resolves the same way path does (through the same symlinks).
//  2. Through real paths. Only when 1 gives nothing of at most directMax
//     bytes: resolve dir to a real path and climb out of the cwd as getcwd(3)
//     reports it (both real, so every ".." climbs out of a real directory, as
//     the kernel does). Comparing the path as given instead would miss the
//     common part when one side goes through a symlink (/var against
//     /private/var on macOS), and a ".." would climb out of a symlink's target
//     rather than the directory the path names.
//
// The shorter result is kept. nil when path is not absolute, the cwd cannot be
// read, neither way gives a spelling the sockaddr carries (directMax bytes),
// or the relative directory turns out not to be the absolute
// one (a cwd that was removed, or one changed meanwhile by another thread).
func newRelative(path, dir string) *relative {
	if !isAbs(path) {
		return nil
	}
	_, name, err := splitPath(path)
	if err != nil {
		return nil
	}
	cwdBefore, ok := statID(".")
	if !ok {
		return nil
	}
	cwd, err := syscall.Getwd()
	if err != nil {
		return nil
	}

	best := ""
	keep := func(rel string) {
		rel = asSpelling(rel)
		if best == "" || len(rel) < len(best) {
			best = rel
		}
	}
	for _, prefix := range cwdSpellings(cwd, cwdBefore) {
		if rel, ok := below(path, prefix); ok {
			keep(rel)
		}
	}
	if best == "" || len(best) > directMax {
		if realDir, err := filepath.EvalSymlinks(dir); err == nil {
			if realDir, err = filepath.Abs(realDir); err == nil {
				if rel, ok := relativeTo(filepath.Join(realDir, name), cwd); ok {
					keep(rel)
				}
			}
		}
	}
	if best == "" || len(best) > directMax {
		return nil
	}
	relDir, _, err := splitPath(best)
	if err != nil {
		return nil
	}
	relID, ok1 := statID(relDir)
	dirID, ok2 := statID(dir)
	if !ok1 || !ok2 || relID != dirID {
		return nil
	}
	if now, ok := statID("."); !ok || now != cwdBefore {
		return nil
	}
	return &relative{spelling: best, cwd: cwdBefore}
}

// run calls f with the relative spelling, then confirms the cwd is still the
// one the spelling was made against.
//
// A relative path is resolved against the cwd at the moment of the call, and
// the cwd is shared by every thread of the process. If another thread changes
// it in between, the call went to some other directory: the result is
// discarded (a listener or connection is closed) and an error is returned. A
// socket file that such a bind created is left where it is, because the
// directory it went to is unknown. The check cannot see a change that is
// undone before it runs; the guarantee is for processes that do not change the
// cwd while binding / connecting.
func (r *relative) run(f func(spelling string) (endpoint, error)) (endpoint, error) {
	ep, err := f(r.spelling)
	if now, ok := statID("."); !ok || now != r.cwd {
		if ep != nil {
			ep.Close()
		}
		return nil, errCwdChanged
	}
	return ep, err
}

// symlinkRoute connects to path through a symlink <base>/sockat.XXXXXXXXXX
// to the socket file. A connect follows a symlink in the last component, so
// the spelling's length depends on neither the directory nor the file name.
// The link holds the caller's own spelling (made absolute against the cwd), so
// the kernel resolves it as it resolves path.
//
// Used when the spelling fits and base is a place where nobody else can
// replace the symlink (safeBase); both are checked before anything is
// created. The symlink is removed before this returns, whatever the outcome.
// errUnusable when the route cannot be used here.
func symlinkRoute(base, path string) (endpoint, route, error) {
	link := underBase(base, linkPrefix+string(make([]byte, linkRandomLen)))
	if len(link) > directMax || !safeBase(base) {
		return nil, "", errUnusable
	}
	target, ok := absTarget(path)
	if !ok {
		return nil, "", errUnusable
	}
	link, err := createLink(base, target, randomName)
	if err != nil {
		return nil, "", errUnusable
	}
	defer os.Remove(link)
	ep, err := viaStd(opDial, link, false)
	return ep, routeSymlinkFile, err
}

// absTarget is the caller's spelling made absolute against the cwd, for a
// symlink to hold: the kernel then resolves the link as it resolves the
// caller's path. false when the cwd cannot be read.
func absTarget(target string) (string, bool) {
	if isAbs(target) {
		return target, true
	}
	cwd, err := syscall.Getwd()
	if err != nil {
		return "", false
	}
	return cwd + "/" + target, true
}

// safeBase reports whether base, followed through symlinks (macOS's /tmp is
// one to /private/tmp), is a directory where no other user (root aside) can
// remove or rename an entry, so the symlink cannot be replaced between its
// creation and the bind / connect through it. false when it cannot be looked
// up.
func safeBase(base string) bool {
	var st unix.Stat_t
	if err := unix.Stat(base, &st); err != nil {
		return false
	}
	return st.Mode&unix.S_IFMT == unix.S_IFDIR && onlyWeCanReplace(uint32(st.Mode), st.Uid, uint32(os.Geteuid()))
}

// onlyWeCanReplace reports whether, in a directory of mode owned by owner,
// only the user us and root can remove or rename the entries us creates:
//
//   - sticky (as /tmp is) and owned by root or us: an entry can then be removed
//     or renamed only by its owner, the directory's owner and root. The owner
//     matters: in a sticky directory of another user, that user can replace
//     anything;
//   - owned by us with no write permission for group and others
//     (mode&0o022 == 0, as a private TMPDIR is): nobody else can create,
//     remove or rename entries there at all.
//
// What remains is a replacement by us itself or by root, which is outside what
// sockat guards against.
func onlyWeCanReplace(mode, owner, us uint32) bool {
	const sticky = 0o1000
	const groupOrOtherWrite = 0o022
	ours := owner == us || owner == 0
	return (mode&sticky != 0 && ours) || (owner == us && mode&groupOrOtherWrite == 0)
}

// createLink creates a symlink under base pointing to target, named by
// nextName (called once per try; its error ends the attempt), and returns its
// path.
//
// symlink(2) fails with EEXIST instead of replacing an existing entry, which
// is the O_EXCL of symlinks: the link that is created is always one nobody
// else had, and a name already taken (by anyone) is skipped for the next one.
// Any other error ends the attempt.
func createLink(base, target string, nextName func() (string, error)) (string, error) {
	for range linkAttempts {
		name, err := nextName()
		if err != nil {
			return "", err
		}
		link := underBase(base, name)
		err = os.Symlink(target, link)
		if err == nil {
			return link, nil
		}
		if !errors.Is(err, syscall.EEXIST) {
			return "", err
		}
	}
	return "", errNoFreeName
}

// underBase spells the entry name of the directory base as base followed by
// it, without cleaning base (a lexical clean would resolve ".." differently
// from the kernel where base goes through a symlink).
func underBase(base, name string) string {
	for len(base) > 1 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	if base == "/" {
		return "/" + name
	}
	return base + "/" + name
}
