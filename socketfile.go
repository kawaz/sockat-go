package sockat

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
	"syscall"

	"golang.org/x/sys/unix"
)

// SocketFile is a bound socket file, recorded right after the bind (its real
// path and (st_dev, st_ino)), and the E its spelling goes through, if any
// (SPEC "remove と cleanup").
type SocketFile struct {
	file     string // the socket file's real path; "" for none (an abstract name)
	id       fileID
	clean    *cleaner // the spelling's E (link-dir, link-file)
	removeMu sync.Once
	removed  error
}

// cleaner cleans up the E of a spelling (nil for none) once, for every
// holder of it: the SocketFile of ListenEx, and the ForBind (and the
// SocketFiles it gives) or ForConnect of a resolve.
type cleaner struct {
	e    *link
	once sync.Once
	err  error
}

// cleanup removes E the first time it is called and returns that outcome
// every time: an *os.PathError, or nil (no E, or one already gone).
func (c *cleaner) cleanup() error {
	c.once.Do(func() {
		if c.e != nil {
			if err := c.e.remove(); err != nil {
				c.err = &os.PathError{Op: "unlink", Path: c.e.path(), Err: err}
			}
		}
	})
	return c.err
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
// again does nothing. For a SocketFile from ForBind.SocketFile it is the
// same cleanup as the ForBind's: the E is removed once by whichever comes
// first.
func (f *SocketFile) Cleanup() error {
	return f.clean.cleanup()
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

// maxFollow is how many symlinks socketAt follows before it gives up with
// ELOOP: at least what the kernels follow in a path (40 on Linux, 32 on
// macOS), so that a bind that went through them is followed to its end.
const maxFollow = 40

// socketAt is the socket file at file (whose directory is a real path): when
// its last component is a symlink, followed as bind(2) follows it on the
// systems where it does (SPEC "remove", "ForBind の SocketFile"), each target
// taken relative to the directory of its symlink and its directory resolved
// to a real path. It returns the real path of the socket reached and its
// (st_dev, st_ino), or the error: the lstat's (ENOENT, ...), ENOTSOCK when
// what is reached is not a socket, ELOOP after maxFollow symlinks.
//
// Only the last component is followed: the directories on the way are
// already real paths, and each target's are resolved by realFile.
func socketAt(file string) (string, fileID, error) {
	for range maxFollow + 1 {
		var st unix.Stat_t
		if err := unix.Lstat(file, &st); err != nil {
			return "", fileID{}, err
		}
		switch uint32(st.Mode) & unix.S_IFMT {
		case unix.S_IFSOCK:
			return file, fileID{uint64(st.Dev), uint64(st.Ino)}, nil
		case unix.S_IFLNK:
		default:
			return "", fileID{}, syscall.ENOTSOCK
		}
		target, err := os.Readlink(file)
		if err != nil {
			return "", fileID{}, errnoOf(err)
		}
		if !isAbs(target) {
			target = filepath.Dir(file) + "/" + target
		}
		if file, err = realFile(target); err != nil {
			return "", fileID{}, errnoOf(err)
		}
	}
	return "", fileID{}, syscall.ELOOP
}
