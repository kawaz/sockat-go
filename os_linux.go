//go:build linux || android

package sockat

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// runLong is the routes for a path longer than directMax on Linux:
// /proc/self/fd/<dirfd>/<name> with the directory opened O_PATH; when that
// cannot be used, the POSIX order (posixRun) with that directory fd for the
// thread-cwd route.
//
// The kernel resolves the /proc/self/fd/<n> magic link to the directory the
// fd refers to, so the socket is created in (or looked up in) that directory
// without ever changing the cwd. That route has no length limit on the
// directory, needs no temporary file, so whenever it can be used nothing else
// is tried. It cannot be used when <procSelfFD>/<dirfd> is not there (no /proc
// mounted, as in some chroots and minimal containers) or when the spelling
// does not fit sun_path (a file name of about 90 bytes or more). What a bind /
// connect through /proc returns is the result, never retried on another route.
func runLong(o op, path string, cfg *config) (endpoint, route, error) {
	dir, name, err := splitPath(path)
	if err != nil {
		return nil, "", err
	}
	opened, err := openDir(dir)
	if err != nil {
		return nil, "", err
	}
	// opened stays open until this returns, so the /proc spelling keeps
	// naming it.
	defer opened.Close()
	if via, ok := procSpelling(opened, name, cfg.procSelfFD); ok {
		ep, err := viaStd(o, via, false)
		return ep, routeProc, err
	}
	return posixRun(o, path, dir, name, opened, cfg)
}

// procSpelling returns <procSelfFD>/<dir>/<name>, or false when the /proc
// route cannot be used:
//
//   - <procSelfFD>/<dir> cannot be looked up (no /proc). Without this check a
//     bind / connect through the spelling would fail with ENOENT, which reads
//     as "the directory does not exist" although it does;
//   - the spelling does not fit sun_path (a file name of about 90 bytes or
//     more).
func procSpelling(dir *os.File, name, procSelfFD string) (string, bool) {
	fdDir := procSelfFD + "/" + strconv.Itoa(int(dir.Fd()))
	if _, err := os.Stat(fdDir); err != nil {
		return "", false
	}
	via := fdDir + "/" + name
	return via, len(via) <= directMax
}

// openDir opens dir O_PATH | O_DIRECTORY, which needs only search permission.
// The fd serves both the /proc route (/proc/self/fd/<fd> names the directory)
// and the thread-cwd route (fchdir(2) takes an O_PATH directory fd since
// Linux 3.5).
func openDir(dir string) (*os.File, error) {
	fd, err := unix.Open(dir, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, syscallError("open", err)
	}
	return os.NewFile(uintptr(fd), dir), nil
}

// threadChdir makes the cwd of the calling thread, and of no other, the
// directory dirfd: unshare(2) with CLONE_FS gives the thread a working
// directory of its own, then fchdir(2) moves it.
//
// errUnusable when the thread cannot have a cwd of its own. unshare(2) is not
// even called under a seccomp filter (prctl(PR_GET_SECCOMP) other than 0, or
// prctl failing): a filter may answer it by killing the process with SIGSYS
// (systemd's SystemCallFilter= does) rather than with an error, and Docker's
// default profile refuses it anyway (EPERM). A failure of fchdir(2) after
// unshare(2) is a real failure and is returned.
func threadChdir(dirfd int) error {
	if mode, err := unix.PrctlRetInt(unix.PR_GET_SECCOMP, 0, 0, 0, 0); err != nil || mode != 0 {
		return errUnusable
	}
	if err := unix.Unshare(unix.CLONE_FS); err != nil {
		return errUnusable
	}
	return syscallError("fchdir", unix.Fchdir(dirfd))
}

// heldLinkName is the name of the symlink inside the held directory.
const heldLinkName = "s"

// heldLinkRoute connects to path through a place base that others can write
// to (not safeBase), where a symlink reached by its name would not be safe:
// someone could rename it away and put their own in its place.
//
//  1. Create a directory sockat.XXXXXXXXXX of mode 0700 under base (names as
//     for the symlink: a fresh one on EEXIST, linkAttempts at most).
//  2. Open it O_PATH | O_DIRECTORY | O_NOFOLLOW and check with fstat(2) that
//     what was opened is a directory, owned by the effective uid, with no
//     permission for group and others (isOurPrivateDir). A directory swapped
//     in between the mkdir and the open fails the check. Then chmod it to 0700
//     through <procSelfFD>/<fd> (an O_PATH fd takes no fchmod(2); the /proc
//     spelling reaches the directory the fd holds), so a umask that takes the
//     owner's bits (0277, 0477) does not fail every attempt: O_PATH opens the
//     directory even without read permission.
//  3. Create in it, through the fd, the symlink "s" to path (symlinkat(2)).
//  4. Connect through <procSelfFD>/<fd>/s. The kernel follows the fd, not the
//     name, so renaming the directory away does not matter; and the spelling
//     is short whatever the directory and the file name.
//  5. Remove "s" through the fd, and the directory only when its name still
//     names the directory held (lstat's (st_dev, st_ino) against the fd's).
//
// A bind never takes this: its spelling would be <procSelfFD>/<fd>/s/<name>,
// longer than the /proc route's, which has been tried already. errUnusable
// when /proc cannot be used or a step above fails; what was made is removed
// first.
func heldLinkRoute(base, path, procSelfFD string) (endpoint, error) {
	target, ok := absTarget(path)
	if !ok {
		return nil, errUnusable
	}
	// /proc is looked for before anything is made under base: without it
	// there is no fd-held form, and an unsafe place is passed over untouched.
	if !procFDUsable(base, procSelfFD) {
		return nil, errUnusable
	}
	dirPath, err := createHeldDir(base, randomName)
	if err != nil {
		return nil, errUnusable
	}
	dfd, err := unix.Open(dirPath, unix.O_PATH|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		// What is at the name now is not known to be ours: left alone.
		return nil, errUnusable
	}
	defer unix.Close(dfd)
	var held unix.Stat_t
	if err := unix.Fstat(dfd, &held); err != nil || !isOurPrivateDir(&held, uint32(os.Geteuid())) {
		return nil, errUnusable
	}
	defer removeHeldDir(dirPath, &held)
	fdDir := procSelfFD + "/" + strconv.Itoa(dfd)
	if err := unix.Chmod(fdDir, 0o700); err != nil {
		return nil, errUnusable
	}
	if err := unix.Symlinkat(target, dfd, heldLinkName); err != nil {
		return nil, errUnusable
	}
	defer unix.Unlinkat(dfd, heldLinkName, 0)
	if _, err := os.Stat(fdDir); err != nil {
		return nil, errUnusable
	}
	via := fdDir + "/" + heldLinkName
	if len(via) > directMax {
		return nil, errUnusable
	}
	return viaStd(opDial, via, false)
}

// procFDUsable reports whether <procSelfFD>/<fd> can be looked up for an fd
// of dir (opened O_PATH for the check), that is, whether /proc is there.
func procFDUsable(dir, procSelfFD string) bool {
	fd, err := unix.Open(dir, unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return false
	}
	defer unix.Close(fd)
	_, err = os.Stat(procSelfFD + "/" + strconv.Itoa(fd))
	return err == nil
}

// createHeldDir creates a directory of mode 0700 under base, named by
// nextName (randomName outside the tests; a fresh name on EEXIST, linkAttempts
// at most, as createLink), and returns its path.
func createHeldDir(base string, nextName func() (string, error)) (string, error) {
	for range linkAttempts {
		name, err := nextName()
		if err != nil {
			return "", err
		}
		dirPath := underBase(base, name)
		err = unix.Mkdir(dirPath, 0o700)
		if err == nil {
			return dirPath, nil
		}
		if err != unix.EEXIST {
			return "", err
		}
	}
	return "", errNoFreeName
}

// isOurPrivateDir reports whether st (an fstat) is a directory owned by us
// with no permission for group and others: the one created (mkdir 0700 under
// any umask), and not one swapped in for it.
func isOurPrivateDir(st *unix.Stat_t, us uint32) bool {
	return st.Mode&unix.S_IFMT == unix.S_IFDIR && st.Uid == us && st.Mode&0o077 == 0
}

// removeHeldDir removes the directory at dirPath when that name still names
// the directory held (held is its fstat). Best effort, as for the symlink.
func removeHeldDir(dirPath string, held *unix.Stat_t) {
	var st unix.Stat_t
	if unix.Lstat(dirPath, &st) == nil && st.Dev == held.Dev && st.Ino == held.Ino {
		unix.Rmdir(dirPath)
	}
}
