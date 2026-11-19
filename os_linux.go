//go:build linux || android

package sockat

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// dialSteps is the connect order on Linux (SPEC "connect の順"): with /proc,
// proc-direct, thread-cwd, proc, proc-held, symlink; without it, relative,
// thread-cwd, symlink.
func dialSteps(proc bool) []dialStep {
	if proc {
		return []dialStep{
			{routeProcDirect, (*dialer).procDirect},
			{routeThreadCwd, (*dialer).threadCwd},
			{routeProc, (*dialer).proc},
			{routeProcHeld, (*dialer).procHeld},
			{routeSymlink, (*dialer).symlink},
		}
	}
	return []dialStep{
		{routeRelative, (*dialer).relative},
		{routeThreadCwd, (*dialer).threadCwd},
		{routeSymlink, (*dialer).symlink},
	}
}

// procFDUsableFor reports whether <procSelfFD>/<fd of dir> can be looked up,
// that is, whether /proc is there. With no dir (the socket's directory could
// not be opened), an fd of / stands in.
func procFDUsableFor(dir *os.File, procSelfFD string) bool {
	if dir == nil {
		return procFDUsable("/", procSelfFD)
	}
	_, err := os.Stat(procSelfFD + "/" + strconv.Itoa(int(dir.Fd())))
	return err == nil
}

// threadCwdPossible reports whether the thread-cwd route may be tried: not
// under a seccomp filter (prctl(PR_GET_SECCOMP) other than 0, or failing),
// which may answer unshare(2) by killing the process.
func threadCwdPossible() bool {
	mode, err := unix.PrctlRetInt(unix.PR_GET_SECCOMP, 0, 0, 0, 0)
	return err == nil && mode == 0
}

// dOpenFlags is how D is opened: O_PATH, which needs no read permission (a
// umask of 0477 leaves 0300), so D is checked (fstat) whatever its mode
// before anything is decided about it.
const dOpenFlags = unix.O_PATH | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

// setPrivate sets D (fd, an O_PATH fd that passed the check, whose fstat is
// held) to mode 0700. An O_PATH fd takes no fchmod(2): with /proc, chmod
// through /proc/self/fd/<fd>, which reaches the directory the fd holds;
// without it, open D by its name again O_RDONLY (the check having passed, the
// owner may lack only read permission, so this can still fail), confirm it is
// the same directory, and fchmod that.
func setPrivate(baseFD int, name string, fd int, held *unix.Stat_t, proc bool, procSelfFD string) error {
	if proc {
		return unix.Chmod(procSelfFD+"/"+strconv.Itoa(fd), 0o700)
	}
	rfd, err := unix.Openat(baseFD, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer unix.Close(rfd)
	var st unix.Stat_t
	if err := unix.Fstat(rfd, &st); err != nil {
		return err
	}
	if st.Dev != held.Dev || st.Ino != held.Ino {
		return errUnusable
	}
	return unix.Fchmod(rfd, 0o700)
}

// procDirect connects through <procSelfFD>/<fd of the socket's
// directory>/<name>, with no R: a connect leaves no spelling behind. Unusable
// when the socket's directory was not opened, <procSelfFD>/<fd> cannot be
// looked up (without this check the connect would fail with ENOENT, which
// reads as "the directory does not exist" although it does), or the spelling
// does not fit sun_path (a file name of about 90 bytes or more).
func (d *dialer) procDirect() (endpoint, error) {
	if d.sdir == nil {
		return nil, unusable("socket directory not opened")
	}
	fdDir := d.cfg.procSelfFD + "/" + strconv.Itoa(int(d.sdir.Fd()))
	if _, err := os.Stat(fdDir); err != nil {
		return nil, unusable("/proc unusable")
	}
	via := fdDir + "/" + d.name
	if len(via) > directMax {
		return nil, unusable("spelling too long")
	}
	return viaStd(opDial, via, false)
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

// heldLinkName is the name of proc-held's symlink inside D.
const heldLinkName = "s"

// procHeld connects through <procSelfFD>/<D>/s, s a symlink in D to the socket
// file (SPEC "proc-held"; D made with steps 1, 2 and 5 of "D と R", R not
// needed). The kernel follows D's fd, not a name, so neither where D is nor
// who else can write there matters, and the spelling is short whatever the
// directory and the file name. s holds the caller's own spelling (made
// absolute against the cwd), so the kernel resolves it as it resolves the
// path. s is removed (through D's fd) only when it was made; D with the rest
// of the dial. Unusable when D or s cannot be made.
func (d *dialer) procHeld() (endpoint, error) {
	target, ok := absTarget(d.path)
	if !ok {
		return nil, unusable("cwd unreadable")
	}
	h, err := d.theD(false)
	if err != nil {
		return nil, err
	}
	if err := unix.Symlinkat(target, h.fd, heldLinkName); err != nil {
		return nil, unusable("s not made")
	}
	defer unix.Unlinkat(h.fd, heldLinkName, 0)
	via := d.cfg.procSelfFD + "/" + strconv.Itoa(h.fd) + "/" + heldLinkName
	if len(via) > directMax {
		return nil, unusable("spelling too long")
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

// dOpenFlagsRetry: no second way of opening D here.
const dOpenFlagsRetry = 0

// hasProc: /proc may be there.
const hasProc = true
