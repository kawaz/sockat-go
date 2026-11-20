//go:build linux || android

package sockat

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// dialSteps is the connect order on Linux (SPEC "connect の順"): with /proc,
// proc-direct, thread-cwd-direct, link-file; without it, relative,
// thread-cwd-direct, link-file. Which row is the call's is decided once, by
// whether /proc can be used (procUsable), so a route of the other row is
// never tried or skipped.
func dialSteps(proc bool) []dialStep {
	if proc {
		return []dialStep{
			{routeProcDirect, (*dialCall).procDirect},
			{routeThreadCwdDirect, (*dialCall).threadCwdDirect},
			{routeLinkFile, (*dialCall).linkFile},
		}
	}
	return []dialStep{
		{routeRelative, (*dialCall).relative},
		{routeThreadCwdDirect, (*dialCall).threadCwdDirect},
		{routeLinkFile, (*dialCall).linkFile},
	}
}

// procUsable reports whether /proc can be used (SPEC "用語"): a stat of
// <proc>/self/fd/<fd> has the (st_dev, st_ino) of the directory the fd holds,
// for the fd of the socket's directory (sdir), or of / opened for the check
// when sdir could not be opened. Where the PID namespace and the /proc
// mounted disagree, or /proc is not there, it does not.
func procUsable(sdir *os.File, proc string) bool {
	fd := -1
	if sdir != nil {
		fd = int(sdir.Fd())
	} else {
		var err error
		if fd, err = unix.Open("/", unix.O_PATH|unix.O_DIRECTORY|unix.O_CLOEXEC, 0); err != nil {
			return false
		}
		defer unix.Close(fd)
	}
	var held unix.Stat_t
	if unix.Fstat(fd, &held) != nil {
		return false
	}
	id, ok := statID(proc + "/self/fd/" + strconv.Itoa(fd))
	return ok && id == fileID{uint64(held.Dev), uint64(held.Ino)}
}

// threadCwdPossible reports whether the thread-cwd route may be tried: not
// under a seccomp filter (prctl(PR_GET_SECCOMP) other than 0, or failing),
// which may answer unshare(2) by killing the process.
func threadCwdPossible() bool {
	mode, err := unix.PrctlRetInt(unix.PR_GET_SECCOMP, 0, 0, 0, 0)
	return err == nil && mode == 0
}

// procDirect connects through <proc>/self/fd/<fd of the socket's
// directory>/<name>, with no E: a connect leaves no spelling behind. Unusable
// when the socket's directory was not opened or the spelling does not fit
// sun_path (a file name of about 90 bytes or more). Only in the row of the
// connect order where /proc can be used (procUsable).
func (c *dialCall) procDirect() (endpoint, error) {
	if c.sdir == nil {
		return nil, unusable("socket directory not opened")
	}
	via := c.w.cfg.proc + "/self/fd/" + strconv.Itoa(int(c.sdir.Fd())) + "/" + c.name
	if len(via) > directMax {
		return nil, unusable("spelling too long")
	}
	return viaStd(opDial, via, false)
}

// openDir opens dir O_PATH | O_DIRECTORY, which needs only search permission.
// The fd serves both proc-direct (/proc/self/fd/<fd> names the directory)
// and thread-cwd-direct (fchdir(2) takes an O_PATH directory fd since Linux
// 3.5).
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

// hasProc: /proc may be there.
const hasProc = true

// bindFollowsHere: bind(2) here does not follow a symlink in the last
// component (measured, SPEC "bind の順").
const bindFollowsHere = followsNo
