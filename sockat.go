package sockat

import (
	"net"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// Listen creates a socket bound to path and listening on it, like
// net.ListenUnix("unix", ...) but for a path of any length.
//
// A path that the net package accepts goes to net.ListenUnix unchanged, so it
// behaves exactly as with the net package (including the removal of the
// socket file when the listener is closed). A longer path is reached by the
// routes the system has (see the package documentation); the socket file is
// created at path, and is not removed when the listener is closed.
//
// The error is a *net.OpError, as from the net package: for example
// EADDRINUSE when path exists, ENOENT when its directory does not,
// ENAMETOOLONG when no route reaches path, EINVAL for a path that contains a
// NUL byte or for a long path that does not end with a file name.
func Listen(path string) (*net.UnixListener, error) {
	ep, _, err := run(opListen, path)
	if err != nil {
		return nil, err
	}
	return ep.(*net.UnixListener), nil
}

// Dial connects to the socket at path, like net.DialUnix("unix", nil, ...)
// but for a path of any length.
//
// A path that the net package accepts goes to net.DialUnix unchanged. A
// longer path is reached by the routes the system has (see the package
// documentation). Like the net package's, the connect does not wait for room
// in a full backlog: Linux answers EAGAIN, macOS ECONNREFUSED.
//
// The error is a *net.OpError, as from the net package: for example ENOENT
// when nothing is at path, ECONNREFUSED when nobody listens on it,
// ENAMETOOLONG when no route reaches path, EINVAL for a path that contains a
// NUL byte or for a long path that does not end with a file name.
func Dial(path string) (*net.UnixConn, error) {
	ep, _, err := run(opDial, path)
	if err != nil {
		return nil, err
	}
	return ep.(*net.UnixConn), nil
}

// ListenEx is Listen, with a function that removes the socket file later.
//
// It remembers the socket file it bound (by its real path and (st_dev,
// st_ino)); discard removes the file only while that path still names that
// same file, so a socket that someone else has bound at the path since is left
// alone. discard does not close the listener: close it yourself (before or
// after). Its error is that of the removal; nil when the file is gone or is no
// longer the one bound. Calling it again does nothing.
//
// The listener does not remove the socket file on Close (short paths
// included), so the removal is discard's alone.
func ListenEx(path string) (l *net.UnixListener, discard func() error, err error) {
	l, err = Listen(path)
	if err != nil {
		return nil, nil, err
	}
	l.SetUnlinkOnClose(false)
	return l, discarder(path), nil
}

// discarder returns ListenEx's discard for the socket file just bound at path.
func discarder(path string) func() error {
	var once sync.Once
	if abstractName(path) {
		return func() error { return nil }
	}
	file, id, ok := boundFile(path)
	if !ok {
		return func() error { return nil }
	}
	return func() error {
		var err error
		once.Do(func() {
			if now, ok := lstatID(file); ok && now == id {
				err = os.Remove(file)
			}
		})
		return err
	}
}

// boundFile is the real path of the socket file at path (its directory
// resolved, so a later change of the cwd or of a symlink on the way does not
// retarget it) and its (st_dev, st_ino).
func boundFile(path string) (string, fileID, bool) {
	dir, name, err := splitPath(path)
	if err != nil {
		return "", fileID{}, false
	}
	real, err := realDir(dir)
	if err != nil {
		return "", fileID{}, false
	}
	file := filepath.Join(real, name)
	id, ok := lstatID(file)
	return file, id, ok
}

// lstatID is (st_dev, st_ino) of path itself (a symlink is not followed).
func lstatID(path string) (fileID, bool) {
	var st unix.Stat_t
	if err := unix.Lstat(path, &st); err != nil {
		return fileID{}, false
	}
	return fileID{uint64(st.Dev), uint64(st.Ino)}, true
}
