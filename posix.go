//go:build !freebsd

package sockat

import (
	"os"
	"path/filepath"
	"syscall"
)

// The routes of a dial that spell the path in the plain sockaddr without D
// (every system but FreeBSD): relative, and symlink-file. A connect leaves no
// spelling behind with the peer (the connecting socket is not bound), so these
// spellings need no random part; a listen never takes relative.

// relative connects through the path spelled relative to the cwd
// (newRelative), then confirms the cwd did not change meanwhile (relative.run).
// Unusable when the path is not absolute or no relative spelling fits.
func (d *dialer) relative() (endpoint, error) {
	if !isAbs(d.path) {
		return nil, unusable("path not absolute")
	}
	rel := newRelative(d.path, d.dir)
	if rel == nil {
		return nil, unusable("no relative spelling")
	}
	return rel.run(func(s string) (endpoint, error) { return viaStd(opDial, s, false) })
}

// symlinkFile connects through a symlink to the socket file in the first of
// cfg.tmpBases where it can be used (symlinkFile).
func (d *dialer) symlinkFile() (endpoint, error) {
	return symlinkFile(d.path, d.cfg.tmpBases)
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
