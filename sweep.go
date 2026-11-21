package sockat

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// Sweep removes the directories (D) that processes which have ended left in
// this user's directories of /tmp, $XDG_RUNTIME_DIR and $TMPDIR (sockat-<euid>,
// sockat under $XDG_RUNTIME_DIR), with the symlinks in them. Those
// directories are for this library alone: only the entries in them named as
// a D is (10 letters and digits) are looked at, and anything else put there
// stays. A D whose process is alive (it holds a lock on it) is left alone,
// and so is one that holds anything but symlinks, and the symlinks right in
// those directories. Nothing is made: a place with no such directory, or one
// that fails its checks (another owner, writable by others), is passed over.
// Directories of other names (those in a socket's directory) are not looked
// at.
//
// It is never called for you. The error is an *os.PathError (several joined
// with errors.Join) for what could not be read or removed.
func Sweep() error {
	return defaultWorld.sweep()
}

// SweepBase is Sweep for the directory base, as given to Options.Base, which
// the application that manages it sweeps (the directory is for this library
// alone, as Sweep's are). Nothing happens when base, or the directory it is
// in, does not exist. The error is an *os.PathError (several
// joined with errors.Join): EINVAL for a base that is not absolute, does
// not end with a name or holds a NUL, ENOTDIR for one that is not a directory, EPERM for a
// symlink, for one not of this user or writable by others, or for a
// directory on the way others can write to; and otherwise as Sweep's.
func SweepBase(base string) error {
	pathErr := func(err error) error { return &os.PathError{Op: "sweep", Path: base, Err: err} }
	if givenShape(base) != nil {
		return pathErr(syscall.EINVAL)
	}
	parent, name, _ := splitPath(base)
	real, err := realDir(parent)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return pathErr(errnoOf(err))
	}
	if checkAncestors(real, false) != nil {
		return pathErr(syscall.EPERM)
	}
	return sweepB(joinUnder(real, name), base, true)
}

// sweep is Sweep on w: the B of a default name in each of w's places 1 to 3
// whose ancestors pass their check.
func (w *world) sweep() error {
	var errs []error
	for _, p := range w.autoPlaces() {
		real, err := realDir(p.path)
		if err != nil || checkAncestors(real, false) != nil {
			continue
		}
		b := joinUnder(real, bName(p.kind))
		errs = append(errs, sweepB(b, b, false))
	}
	return errors.Join(errs...)
}

// sweepB removes the dead D in the B at real (named path for the errors): a
// directory of ours others cannot write to, opened without following a
// symlink. Nothing when it is not there. given makes a B that fails the
// check an error (EPERM, ENOTDIR); otherwise it is passed over.
func sweepB(real, path string, given bool) error {
	pathErr := func(err error) error { return &os.PathError{Op: "sweep", Path: path, Err: err} }
	failed := func(err error) error {
		if given {
			return pathErr(err)
		}
		return nil
	}
	var st unix.Stat_t
	switch err := unix.Lstat(real, &st); {
	case errors.Is(err, unix.ENOENT):
		return nil
	case err != nil:
		return failed(err)
	case uint32(st.Mode)&unix.S_IFMT == unix.S_IFLNK:
		return failed(syscall.EPERM)
	case uint32(st.Mode)&unix.S_IFMT != unix.S_IFDIR:
		return failed(syscall.ENOTDIR)
	}
	fd, err := unix.Open(real, dirOpenFlags, 0)
	if err != nil {
		return failed(err)
	}
	f := os.NewFile(uintptr(fd), real)
	defer f.Close()
	if err := unix.Fstat(fd, &st); err != nil {
		return pathErr(err)
	}
	if !isOurDir(&st) || uint32(st.Mode)&0o022 != 0 {
		return failed(syscall.EPERM)
	}
	names, err := f.Readdirnames(-1)
	if err != nil {
		return pathErr(err)
	}
	var errs []error
	for _, n := range names {
		if err := sweepD(fd, n); err != nil {
			errs = append(errs, &os.PathError{Op: "sweep", Path: joinUnder(real, n), Err: err})
		}
	}
	return errors.Join(errs...)
}

// sweepD removes the entry name of B (bfd) when it is a D whose process has
// ended (SPEC-routes "D と E"): named as a D is (isRandomName), a directory
// of ours, opened without following a symlink, on which an exclusive lock can be taken at once (a live
// process holds a shared one), still named name once locked, and holding
// nothing but symlinks. The symlinks go, then D. Anything else is left
// alone: a symlink right in B (an E of a connect), a D that is locked, or
// that holds anything else. The error is the system call's, for a D found
// dead that could not be removed.
func sweepD(bfd int, name string) error {
	// Only a name of D's form (SPEC "sweep"): a directory of another name in
	// B is not one this library made, whatever it holds.
	if !isRandomName(name) {
		return nil
	}
	var st unix.Stat_t
	if unix.Fstatat(bfd, name, &st, unix.AT_SYMLINK_NOFOLLOW) != nil || !isOurDir(&st) {
		return nil
	}
	dfd, err := unix.Openat(bfd, name, dirOpenFlags, 0)
	if err != nil {
		return nil
	}
	defer unix.Close(dfd)
	if unix.Flock(dfd, unix.LOCK_EX|unix.LOCK_NB) != nil {
		return nil
	}
	var held, now unix.Stat_t
	if unix.Fstat(dfd, &held) != nil || !isOurDir(&held) ||
		unix.Fstatat(bfd, name, &now, unix.AT_SYMLINK_NOFOLLOW) != nil || now.Dev != held.Dev || now.Ino != held.Ino {
		return nil
	}
	lfd, err := unix.Dup(dfd)
	if err != nil {
		return err
	}
	lf := os.NewFile(uintptr(lfd), name)
	entries, err := lf.Readdirnames(-1)
	lf.Close()
	if err != nil {
		return err
	}
	for _, e := range entries {
		if unix.Fstatat(dfd, e, &st, unix.AT_SYMLINK_NOFOLLOW) != nil || uint32(st.Mode)&unix.S_IFMT != unix.S_IFLNK {
			return nil
		}
	}
	for _, e := range entries {
		if err := unix.Unlinkat(dfd, e, 0); err != nil && !errors.Is(err, unix.ENOENT) {
			return err
		}
	}
	if err := unix.Unlinkat(bfd, name, unix.AT_REMOVEDIR); err != nil && !errors.Is(err, unix.ENOENT) &&
		!errors.Is(err, unix.ENOTEMPTY) && !errors.Is(err, unix.EEXIST) {
		return err
	}
	return nil
}
