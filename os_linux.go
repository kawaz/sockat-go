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
