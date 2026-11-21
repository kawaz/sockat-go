//go:build illumos || solaris

package sockat

import "golang.org/x/sys/unix"

// symlinkat makes a symlink name in the directory dirfd, whose real path is
// dirPath, pointing to target.
//
// Design rationale: golang.org/x/sys/unix has no Symlinkat here, so the
// symlink is made by the directory's real path (dirPath/name). D, whose path
// that is, is this process's and locked, and B and D are 0711 directories of
// ours under ancestors others cannot replace, so the path names the
// directory dirfd holds.
func symlinkat(target string, _ int, dirPath, name string) error {
	return unix.Symlink(target, dirPath+"/"+name)
}

// readlinkat is what the symlink name in the directory dirfd, whose real
// path is dirPath, points to.
//
// Design rationale: golang.org/x/sys/unix has no Readlinkat here either, so
// the symlink is read by the directory's real path, as symlinkat makes it.
func readlinkat(_ int, dirPath, name string) (string, error) {
	return readlinkWith(func(buf []byte) (int, error) { return unix.Readlink(dirPath+"/"+name, buf) })
}
