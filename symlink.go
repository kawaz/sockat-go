package sockat

import (
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/unix"
)

// The routes through a symlink in a place (SPEC "symlink の置き場と名前"):
//
//   - symlink-file: a symlink <place>/sockat.XXXXXXXXXX to the socket file. A
//     connect follows a symlink in the last component; so does a bind on some
//     systems (macOS), where the socket is then made at the path. Its spelling
//     holds neither the directory nor the file name.
//   - symlink-dir: a symlink <place>/sockat.XXXXXXXXXX to the socket's
//     directory, the bind by <place>/sockat.XXXXXXXXXX/<name>. ListenEx's last
//     resort only: the spelling the kernel keeps names whatever the place
//     holds by that name later.
//
// Either symlink is removed before the route returns, whatever the outcome.

// symlinkFile binds (and listens) or connects to path through a symlink to
// the socket file, <base>/sockat.XXXXXXXXXX, in the first of bases where the
// spelling fits and nobody else can replace the symlink (safeBase); both are
// checked before anything is made there. The link holds the caller's own
// spelling (made absolute against the cwd), so the kernel resolves it as it
// resolves path. unusable when no base can be used.
//
// A bind follows the symlink only on some systems; one that does not answers
// EADDRINUSE and makes nothing (Linux). So when a bind fails, path itself is
// looked up: with something there, the failure is real (EADDRINUSE for a file
// already at path) and returned; with nothing there, or no telling, the
// kernel is taken not to have followed the symlink and the route as unusable.
// A real failure of that kind is then met again by the next route.
func symlinkFile(o op, path string, bases []string) (endpoint, error) {
	target, ok := absTarget(path)
	if !ok {
		return nil, unusable("cwd unreadable")
	}
	for _, base := range bases {
		if len(underBase(base, linkPrefix+strings.Repeat("x", linkRandomLen))) > directMax || !safeBase(base) {
			continue
		}
		link, err := createLink(base, target, randomName)
		if err != nil {
			continue
		}
		ep, err := viaStd(o, link, false)
		os.Remove(link)
		if err != nil && o == opListen {
			var st unix.Stat_t
			if unix.Lstat(target, &st) != nil {
				return nil, unusable("bind did not follow the symlink")
			}
		}
		return ep, err
	}
	return nil, unusable("no safe place")
}

// symlinkDir binds through <place>/sockat.XXXXXXXXXX/<name>, the symlink
// pointing to the socket's directory (real, its real path) and removed right
// after. The places (bases) that only the effective uid can write to are tried
// first, in order; then the shared ones (sticky, owned by root or us). shared
// reports that the bind went through a shared place, where others can make the
// spelling again. A real failure of the bind is returned as it is; unusable
// when no place can be used (nothing left behind).
func symlinkDir(real, name string, bases []string) (ep endpoint, shared bool, err error) {
	euid := uint32(os.Geteuid())
	for _, wantShared := range []bool{false, true} {
		for _, base := range bases {
			var st unix.Stat_t
			if unix.Stat(base, &st) != nil || uint32(st.Mode)&unix.S_IFMT != unix.S_IFDIR {
				continue
			}
			mode := uint32(st.Mode)
			if wantShared {
				if mode&0o1000 == 0 || (st.Uid != euid && st.Uid != 0) {
					continue
				}
			} else if st.Uid != euid || mode&0o022 != 0 {
				continue
			}
			if len(underBase(base, linkPrefix+strings.Repeat("x", linkRandomLen)))+1+len(name) > directMax {
				continue
			}
			link, err := createLink(base, real, randomName)
			if err != nil {
				continue
			}
			ep, err := viaStd(opListen, link+"/"+name, false)
			os.Remove(link)
			return ep, wantShared, err
		}
	}
	return nil, false, unusable("no usable place")
}

// absTarget is the caller's spelling made absolute against the cwd, for a
// symlink to hold: the kernel then resolves the link as it resolves the
// caller's path. false when the cwd cannot be read.
func absTarget(target string) (string, bool) {
	if isAbs(target) {
		return target, true
	}
	cwd, err := syscall.Getwd()
	if err != nil {
		return "", false
	}
	return cwd + "/" + target, true
}

// safeBase reports whether base, followed through symlinks (macOS's /tmp is
// one to /private/tmp), is a directory where no other user (root aside) can
// remove or rename an entry, so the symlink cannot be replaced between its
// creation and the bind / connect through it. false when it cannot be looked
// up.
func safeBase(base string) bool {
	var st unix.Stat_t
	if err := unix.Stat(base, &st); err != nil {
		return false
	}
	return st.Mode&unix.S_IFMT == unix.S_IFDIR && onlyWeCanReplace(uint32(st.Mode), st.Uid, uint32(os.Geteuid()))
}

// onlyWeCanReplace reports whether, in a directory of mode owned by owner,
// only the user us and root can remove or rename the entries us creates:
//
//   - sticky (as /tmp is) and owned by root or us: an entry can then be removed
//     or renamed only by its owner, the directory's owner and root. The owner
//     matters: in a sticky directory of another user, that user can replace
//     anything;
//   - owned by us with no write permission for group and others
//     (mode&0o022 == 0, as a private TMPDIR is): nobody else can create,
//     remove or rename entries there at all.
//
// What remains is a replacement by us itself or by root, which is outside what
// sockat guards against.
func onlyWeCanReplace(mode, owner, us uint32) bool {
	const sticky = 0o1000
	const groupOrOtherWrite = 0o022
	ours := owner == us || owner == 0
	return (mode&sticky != 0 && ours) || (owner == us && mode&groupOrOtherWrite == 0)
}
