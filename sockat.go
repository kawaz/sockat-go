package sockat

import (
	"errors"
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
// Right after the bind it records the socket file (by its real path and
// (st_dev, st_ino)); discard removes the file only while that path still
// names that same file, so a socket that someone else has bound at the path
// since is left alone. Between discard's check and the removal, a replacement
// cannot be told apart (POSIX has no removal by inode). When the file cannot
// be recorded, the listener is closed, the file is left, and an error is
// returned.
//
// discard does not close the listener: close it yourself (before or after).
// It returns nil when the file is removed, is gone, or is no longer the one
// bound; an error when the file cannot be checked (lstat fails otherwise than
// for a missing file, for example EACCES) or the removal fails. Calling it again does nothing. The listener does not remove
// the socket file on Close (short paths included), so the removal is
// discard's alone.
func ListenEx(path string) (l *net.UnixListener, discard func() error, err error) {
	if abstractName(path) {
		// No file: nothing to record or remove.
		l, err = Listen(path)
		if err != nil {
			return nil, nil, err
		}
		return l, func() error { return nil }, nil
	}
	// The real path is resolved before the bind, as the bind resolves it;
	// the file's identity is read the moment the bind returns.
	file, ferr := realFile(path)
	ep, _, err := run(opListen, path)
	if err != nil {
		return nil, nil, err
	}
	l = ep.(*net.UnixListener)
	l.SetUnlinkOnClose(false)
	var id fileID
	ok := ferr == nil
	if ok {
		id, ok = lstatID(file)
	}
	if !ok {
		l.Close()
		return nil, nil, opError(opListen, path, errNotRecorded)
	}
	return l, discarder(file, id), nil
}

// errNotRecorded is ListenEx's error when the socket file it bound cannot be
// looked up to be recorded.
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
