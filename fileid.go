package sockat

import (
	"path/filepath"

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
// dir is made absolute first and then resolved component by component, so
// every component is looked up: on macOS getcwd(3) still reports the path of
// a cwd that was removed, which resolving "." alone would take as real.
func realDir(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(abs)
}
