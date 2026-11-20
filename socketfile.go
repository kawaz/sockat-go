package sockat

import (
	"errors"
	"os"
	"path/filepath"
	"sync"

	"golang.org/x/sys/unix"
)

// SocketFile is a bound socket file, recorded right after the bind (its real
// path and (st_dev, st_ino)), and the E its spelling goes through, if any
// (SPEC "remove と cleanup").
type SocketFile struct {
	file     string // the socket file's real path; "" for none (an abstract name)
	id       fileID
	e        *link // the spelling's E (link-dir, link-file); nil for none
	removeMu sync.Once
	removed  error
	cleanMu  sync.Once
	cleaned  error
}

// Remove removes the socket file, once, and then does what Cleanup does.
//
// The file is removed only while its path still names the file bound (the
// same (st_dev, st_ino)), so a socket that someone else has bound at the path
// since, while the listener is open, is left alone. Call it before closing the
// listener: the kernel holds the socket file only while it is open, and a
// file removed and made again after the close gets the same (st_dev, st_ino)
// on file systems that reuse them at once (ext4, UFS, FFS). Between the check
// and the removal, a replacement cannot be told apart (POSIX has no removal
// by inode).
//
// It does not close the listener: close it yourself, after Remove. It
// returns nil when the file is removed, is gone, or is no longer the one
// bound; an error (an *os.PathError) when the file cannot be checked (lstat
// fails otherwise than for a missing file, EACCES for example) or the removal
// fails, joined with Cleanup's error if any. The cleanup is done either way.
// Calling it again does nothing.
func (f *SocketFile) Remove() error {
	f.removeMu.Do(func() {
		f.removed = errors.Join(f.removeFile(), f.Cleanup())
	})
	return f.removed
}

// Cleanup removes the E the spelling goes through, once: the spelling then
// names nothing (do not use it afterwards). B and D stay. It does nothing for
// a spelling that goes through no E. The error is an *os.PathError. Calling it
// again does nothing.
func (f *SocketFile) Cleanup() error {
	f.cleanMu.Do(func() {
		if f.e != nil {
			if err := f.e.remove(); err != nil {
				f.cleaned = &os.PathError{Op: "unlink", Path: f.e.path(), Err: err}
			}
		}
	})
	return f.cleaned
}

// removeFile removes the recorded file while its path names it.
func (f *SocketFile) removeFile() error {
	if f.file == "" {
		return nil
	}
	var st unix.Stat_t
	if e := unix.Lstat(f.file, &st); e != nil {
		// Gone (or a component no longer a directory): nothing to remove.
		// Any other failure (EACCES, ...) leaves it unknown whether the
		// file is the one bound, and is returned.
		if !errors.Is(e, unix.ENOENT) && !errors.Is(e, unix.ENOTDIR) {
			return &os.PathError{Op: "lstat", Path: f.file, Err: e}
		}
		return nil
	}
	if (fileID{uint64(st.Dev), uint64(st.Ino)}) != f.id {
		return nil
	}
	// Gone in the meantime is as good as removed.
	if err := os.Remove(f.file); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// errNotRecorded is ListenEx's error when the socket file it bound cannot be
// looked up to be recorded, or, for a relative path, may not be the one at the
// path recorded (the cwd changed before the bind).
var errNotRecorded = errors.New("the socket file bound could not be recorded for its removal; the listener was closed and the file left")

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
