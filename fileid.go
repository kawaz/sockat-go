package sockat

import (
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

// fileID is (st_dev, st_ino).
type fileID struct{ dev, ino uint64 }

func statID(path string) (fileID, bool) {
	var st unix.Stat_t
	if err := unix.Stat(path, &st); err != nil {
		return fileID{}, false
	}
	return fileID{uint64(st.Dev), uint64(st.Ino)}, true
}

// realDir is dir as a real path (symlinks resolved, absolute), for an E to
// point to: the kernel then reaches the socket's directory as dir names it
// now.
//
// dir is resolved as the kernel resolves it (SPEC "長いパス": the path is
// never folded lexically). A relative dir is put after the cwd as
// getcwd(3) reports it, with no cleaning, and the whole is resolved
// component by component, each ".." taken from the directory reached so far
// (so "a/sym/.." is the parent of where sym leads, not "a"). Every component
// is looked up: on macOS getcwd(3) still reports the path of a cwd that was
// removed, which resolving "." alone would take as real.
//
// Design rationale: not filepath.Abs, which cleans the path (folding
// "sym/.." to nothing) and starts from $PWD where $PWD names the cwd, so
// that "../x" from a cwd entered through a symlink is taken from the
// symlink's parent rather than the cwd's. filepath.EvalSymlinks itself does
// not clean its input and takes each ".." from the real directory reached.
func realDir(dir string) (string, error) {
	if !isAbs(dir) {
		cwd, err := syscall.Getwd()
		if err != nil {
			return "", err
		}
		dir = cwd + "/" + dir
	}
	return filepath.EvalSymlinks(dir)
}
