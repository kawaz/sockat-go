package sockat

import (
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// symlink-file of a dial (SPEC "symlink の置き場と名前", until the connect
// order is rebuilt): a symlink <place>/sockat.XXXXXXXXXX to the socket file,
// which a connect follows; removed before the route returns, whatever the
// outcome.

// symlinkFile connects to path through a symlink to the socket file,
// <base>/sockat.XXXXXXXXXX, in the first of bases where the spelling fits and
// nobody else can replace the symlink (safeBase); both are checked before
// anything is made there. The link holds the caller's own spelling (made
// absolute against the cwd), so the kernel resolves it as it resolves path.
// unusable when no base can be used.
func symlinkFile(path string, bases []string) (endpoint, error) {
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
		ep, err := viaStd(opDial, link, false)
		os.Remove(link)
		return ep, err
	}
	return nil, unusable("no safe place")
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
