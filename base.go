package sockat

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"

	"golang.org/x/sys/unix"
)

// B, the directory in a place that a user's processes share (SPEC-routes
// "B の作り方", "実体の置き場", "別の uid と PrivateTmp").

// The kinds of B, which are also the diagnostics' base values for a
// real-path spelling (SPEC-routes "綴りの形").
const (
	kindTmp     = "tmp"     // in /tmp
	kindRuntime = "runtime" // in $XDG_RUNTIME_DIR
	kindTmpdir  = "tmpdir"  // in $TMPDIR
	kindSockdir = "sockdir" // in the socket's directory
	kindGiven   = "given"   // Options.Base
)

// base is a B the world holds.
type base struct {
	fd   int    // B, opened O_RDONLY | O_DIRECTORY | O_NOFOLLOW | O_CLOEXEC (never closed)
	id   fileID // B's (st_dev, st_ino)
	real string // B's real path
	kind string
	// ancestorsX: every directory on the way to B has search permission for
	// others (mode & 0o001), judged when B was first held and kept.
	ancestorsX bool
	// ownX: B itself has search permission for others, judged when B was
	// first held. While it is false, every call with UsableCrossUID looks at
	// it again by the fstat of fd (crossNow); once true it is kept.
	ownX atomic.Bool
	// tmpPath: the place before its real path was resolved, or B's real path,
	// goes through a directory a private /tmp replaces.
	tmpPath bool

	mu    sync.Mutex
	d     *procD // this process's D in B, made at most once (ownD)
	nextE uint64 // the name of the next resolve E (base 36)
}

// crossUID reports whether a real-path spelling through B can be followed
// by another uid, as judged so far: B and every directory on the way to it
// have search permission for others.
func (b *base) crossUID() bool { return b.ancestorsX && b.ownX.Load() }

// crossNow is crossUID for a call with UsableCrossUID (SPEC-routes
// "実体の置き場"): where B itself was judged to have no search permission for
// others, the fstat of its fd looks at it again, and a B that has it now is
// judged to have it from then on. The directories on the way are not looked
// at again.
//
// Design rationale: two processes making B at once leave the mode B is
// first held with to chance: one may open B between the other's
// mkdirat(0700) and fchmodat(0711). Kept as it was, a B held at that moment
// would never serve UsableCrossUID for the life of the process.
func (b *base) crossNow() bool {
	if !b.ancestorsX {
		return false
	}
	if b.ownX.Load() {
		return true
	}
	var st unix.Stat_t
	if unix.Fstat(b.fd, &st) != nil || uint32(st.Mode)&0o001 == 0 {
		return false
	}
	b.ownX.Store(true)
	return true
}

// place is somewhere B may be made: its kind and its path as configured
// (before its real path is resolved).
type place struct {
	kind string
	path string
}

// autoPlaces are the places 1 to 3 in order: /tmp, $XDG_RUNTIME_DIR and
// $TMPDIR, the last two only when set to an absolute path.
func (w *world) autoPlaces() []place {
	ps := []place{{kindTmp, w.cfg.tmp}}
	if v := w.cfg.getenv("XDG_RUNTIME_DIR"); isAbs(v) {
		ps = append(ps, place{kindRuntime, v})
	}
	if v := w.cfg.getenv("TMPDIR"); isAbs(v) {
		ps = append(ps, place{kindTmpdir, v})
	}
	return ps
}

// bName is the name of B in a place of kind: sockat-<euid>, sockat under
// $XDG_RUNTIME_DIR (already the user's own).
func bName(kind string) string {
	if kind == kindRuntime {
		return "sockat"
	}
	return "sockat-" + strconv.Itoa(os.Geteuid())
}

// fitsFunc reports whether a spelling through a B of the kind, whose real
// path is real, can fit the sockaddr: the estimate before B is made (SPEC-routes
// "実体の置き場": no B is made where it is known not to fit).
type fitsFunc func(kind, real string) bool

// errSpellingTooLong is the B of the places 1 to 3 being one no spelling of
// the route fits through (estimated before it is made, or once it is held).
var errSpellingTooLong = unusable("spelling too long")

// errNoBase is no B of the places 1 to 3 being there or makeable.
var errNoBase = unusable("no base")

// autoBase is the world's B of the places 1 to 3 (cross: autoCross, one
// others can follow): the first place in order where B is or can be made
// (with cross, the first such place that lets others search all the way),
// whatever the length of the path, so that every process of the user finds
// its B in the same place. A held B is returned as it is (the route checks
// its spelling). Before B is made in that place, fits estimates the spelling
// through it: where it cannot fit, no B is made and the error is
// errSpellingTooLong; the next place is not tried for it (it is not the
// process's B). errNoBase when no place has or can make one.
func (w *world) autoBase(cross bool, fits fitsFunc) (*base, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.dropGoneAuto()
	if !cross && w.auto != nil {
		return w.auto, nil
	}
	if cross {
		if w.autoCross != nil {
			return w.autoCross, nil
		}
		if w.auto != nil && w.auto.crossNow() {
			w.autoCross = w.auto
			return w.auto, nil
		}
	}
	for _, p := range w.autoPlaces() {
		real, err := realDir(p.path)
		if err != nil || !w.ancestorsPass(real) {
			continue
		}
		if cross && !crossPlace(real, bName(p.kind)) {
			continue
		}
		if !fits(p.kind, real+"/"+bName(p.kind)) {
			return nil, errSpellingTooLong
		}
		b, err := w.makeBase(p, real, false)
		if err != nil {
			continue
		}
		b = w.known(b)
		if cross {
			if !b.crossNow() {
				// Not for this call; held only when it is auto already.
				if b != w.auto {
					unix.Close(b.fd)
				}
				continue
			}
			w.autoCross = b
			return b, nil
		}
		w.auto = b
		return b, nil
	}
	return nil, errNoBase
}

// dropGoneAuto forgets the B of the places 1 to 3 the world holds when it
// was removed (gone), so this call makes B again (SPEC-routes "実体の置き場":
// an old systemd-tmpfiles, macOS's cleaning of $TMPDIR, or a hand may remove
// it). Under w.mu.
//
// Design rationale: the fds of a B (and its D) forgotten are left open, not
// closed: an E handed out through it (SocketFile's Cleanup) still unlinks by
// D's fd, which a closed and reused fd number would point elsewhere. They
// are a few fds for the life of the process, as every B's are.
func (w *world) dropGoneAuto() {
	if w.auto != nil && w.auto.gone() {
		if w.autoCross == w.auto {
			w.autoCross = nil
		}
		w.auto = nil
	}
	if w.autoCross != nil && w.autoCross.gone() {
		w.autoCross = nil
	}
}

// gone reports whether B was removed since it was opened: its fd's st_nlink
// is 0, or its real path no longer names it (missing, or another (st_dev,
// st_ino)).
func (b *base) gone() bool {
	return dirGone(b.fd, b.id, func(st *unix.Stat_t) error { return unix.Lstat(b.real, st) })
}

// dirGone reports whether the directory fd, of (st_dev, st_ino) id, was
// removed: st_nlink 0, or its name (looked up by lookup, not following a
// symlink) gone or naming something else. The name is looked up even when
// st_nlink is not 0: a removed directory keeps st_nlink 2 on macOS (APFS),
// where only Linux gives 0.
func dirGone(fd int, id fileID, lookup func(*unix.Stat_t) error) bool {
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Nlink == 0 {
		return true
	}
	if lookup(&st) != nil {
		return true
	}
	return (fileID{uint64(st.Dev), uint64(st.Ino)}) != id
}

// ancestorsPass reports whether the directories from the root down to real
// pass checkAncestors, checked once per process (w.checked). Under w.mu.
func (w *world) ancestorsPass(real string) bool {
	if w.checked[real] {
		return true
	}
	if checkAncestors(real, false) != nil {
		return false
	}
	w.checked[real] = true
	return true
}

// crossPlace reports whether a B named name in the place real (whose
// ancestors passed the check) can be one others can follow, before it is
// made: every directory down to real has search permission for others, and a
// B of ours already there has it too (one that fails B's check is passed
// over for a fresh B of mode 0711, which has).
func crossPlace(real, name string) bool {
	if checkAncestors(real, true) != nil {
		return false
	}
	var st unix.Stat_t
	if unix.Lstat(joinUnder(real, name), &st) != nil {
		return true
	}
	if !isOurDir(&st) || uint32(st.Mode)&0o022 != 0 {
		return true
	}
	return uint32(st.Mode)&0o001 != 0
}

// checkGiven checks Options.Base before any route is tried (SPEC-routes
// "B の作り方"), at every call, making nothing: an absolute path ending with a
// name (EINVAL otherwise, givenShape), whose parent exists (ENOENT) and is a
// directory (ENOTDIR), with every ancestor passing checkAncestors (EPERM),
// and B itself, when it is there, a directory of ours that others cannot
// write to (a symlink is EPERM, anything else ENOTDIR).
//
// With cross (UsableCrossUID of a bind), every one of them must also have
// search permission for others (EPERM): looked at now while the world does
// not hold the B there yet, and taken from when it was first held once it
// does, except that a B judged to have none itself is looked at again
// (base.crossNow; SPEC-routes "実体の置き場"). The error is an
// *os.PathError.
func (w *world) checkGiven(b string, cross bool) error {
	pathErr := func(err error) error { return &os.PathError{Op: "base", Path: b, Err: err} }
	if err := givenShape(b); err != nil {
		return err
	}
	parent, name, _ := splitPath(b)
	real, err := realDir(parent)
	if err != nil {
		return pathErr(errnoOf(err))
	}
	var st unix.Stat_t
	if err := unix.Stat(real, &st); err != nil {
		return pathErr(err)
	}
	if uint32(st.Mode)&unix.S_IFMT != unix.S_IFDIR {
		return pathErr(syscall.ENOTDIR)
	}
	if err := checkAncestors(real, false); err != nil {
		return pathErr(errnoOf(err))
	}
	switch err := unix.Lstat(joinUnder(real, name), &st); {
	case errors.Is(err, unix.ENOENT):
		if cross && checkAncestors(real, true) != nil {
			return pathErr(syscall.EPERM)
		}
		return nil
	case err != nil:
		return pathErr(err)
	}
	mode := uint32(st.Mode)
	switch {
	case mode&unix.S_IFMT == unix.S_IFLNK:
		return pathErr(syscall.EPERM)
	case mode&unix.S_IFMT != unix.S_IFDIR:
		return pathErr(syscall.ENOTDIR)
	case st.Uid != uint32(os.Geteuid()) || mode&0o022 != 0:
		return pathErr(syscall.EPERM)
	}
	if !cross {
		return nil
	}
	if held := w.heldGiven(fileID{uint64(st.Dev), uint64(st.Ino)}); held != nil {
		if !held.crossNow() {
			return pathErr(syscall.EPERM)
		}
		return nil
	}
	if mode&0o001 == 0 || checkAncestors(real, true) != nil {
		return pathErr(syscall.EPERM)
	}
	return nil
}

// heldGiven is the B of Options.Base the world holds at id, nil when none.
func (w *world) heldGiven(id fileID) *base {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.given[id]
}

// givenShape checks the form of Options.Base, whatever the length of the
// socket path (SPEC "エラー"): absolute and ending with a name ("dir/", "..",
// "/" do not). EINVAL, as an *os.PathError, otherwise.
func givenShape(b string) error {
	if _, _, err := splitPath(b); err != nil || !isAbs(b) {
		return &os.PathError{Op: "base", Path: b, Err: syscall.EINVAL}
	}
	return nil
}

// errnoOf is the errno inside err, or err itself when it has none.
func errnoOf(err error) error {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno
	}
	return err
}

// givenBase is the world's B at b (Options.Base, checked by checkGiven),
// made when there is none there yet. Before it is made, fits estimates the
// spelling through it as autoBase does: where it cannot fit, nothing is made
// and the error is errSpellingTooLong (the route is unusable). Otherwise the
// error is an *os.PathError: B could not be made or opened (the open's
// error, a umask taking the owner's bits for example), or what is at b is not
// a private directory of ours (EPERM).
func (w *world) givenBase(b string, fits fitsFunc) (*base, error) {
	pathErr := func(err error) error { return &os.PathError{Op: "base", Path: b, Err: err} }
	parent, name, err := splitPath(b)
	if err != nil {
		return nil, pathErr(err)
	}
	real, err := realDir(parent)
	if err != nil {
		return nil, pathErr(errnoOf(err))
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if id, ok := statID(b); ok {
		if k := w.given[id]; k != nil {
			if !k.gone() {
				return k, nil
			}
			delete(w.given, id)
		}
	}
	if !fits(kindGiven, joinUnder(real, name)) {
		return nil, errSpellingTooLong
	}
	nb, err := w.makeBase(place{kindGiven, b}, real, true)
	if errors.Is(err, errNotOurs) {
		err = syscall.EPERM
	}
	if err != nil {
		return nil, pathErr(errnoOf(err))
	}
	w.given[nb.id] = nb
	return nb, nil
}

// known is b, or the B the world already holds at the same (st_dev, st_ino)
// (b's fd then closed): auto and autoCross may be one directory.
func (w *world) known(b *base) *base {
	for _, k := range []*base{w.auto, w.autoCross} {
		if k != nil && k.id == b.id {
			unix.Close(b.fd)
			return k
		}
	}
	return b
}

// sockdirBase is the world's B in the socket's directory (real, its real
// path; id, its (st_dev, st_ino)), made when there is none yet: the place 4,
// for when none of the places 1 to 3 can be used. nil when it cannot be made.
func (w *world) sockdirBase(real string, id fileID) *base {
	w.mu.Lock()
	defer w.mu.Unlock()
	if b := w.sockdir[id]; b != nil {
		if !b.gone() {
			return b
		}
		delete(w.sockdir, id)
	}
	b, err := w.makeBase(place{kindSockdir, real}, real, false)
	if err != nil {
		return nil
	}
	w.sockdir[id] = b
	return b
}

// makeBase makes (or opens) B in the place p, whose real path is real
// (SPEC-routes "B の作り方" steps 2 to 5). Under w.mu.
//
// The place's ancestors are checked once per process. B is named
// sockat-<euid> (sockat in $XDG_RUNTIME_DIR, .sockat.<random> in the socket's
// directory); one already there that fails the check (not a directory of
// ours, or writable by others) is passed over for sockat-<euid>.<random>.
// given makes the error of B an Options.Base error rather than a reason to
// fall back.
func (w *world) makeBase(p place, real string, given bool) (*base, error) {
	if !w.ancestorsPass(real) {
		return nil, checkAncestors(real, false)
	}
	pfd, err := unix.Open(real, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer unix.Close(pfd)
	var name string
	var fd int
	switch {
	case p.kind == kindSockdir:
		name, fd, err = w.makeFresh(pfd, ".sockat.")
	case given:
		_, name, _ = splitPath(p.path)
		fd, err = makeOrOpen(pfd, name)
	default:
		name = bName(p.kind)
		fd, err = makeOrOpen(pfd, name)
		if errors.Is(err, errNotOurs) {
			name, fd, err = w.makeFresh(pfd, "sockat-"+strconv.Itoa(os.Geteuid())+".")
		}
	}
	if err != nil {
		return nil, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		unix.Close(fd)
		return nil, err
	}
	breal := joinUnder(real, name)
	b := &base{
		fd:         fd,
		id:         fileID{uint64(st.Dev), uint64(st.Ino)},
		real:       breal,
		kind:       p.kind,
		ancestorsX: checkAncestors(real, true) == nil,
		tmpPath:    w.throughPrivateTmp(p.path) || w.throughPrivateTmp(breal),
	}
	b.ownX.Store(uint32(st.Mode)&0o001 != 0)
	return b, nil
}

// errNotOurs is the check of B (or D) failing: not a directory, not owned by
// the effective uid, or writable by others.
var errNotOurs = errors.New("sockat: not a private directory of ours")

// makeOrOpen makes B named name in the directory pfd, or opens the one there
// (SPEC-routes "B の作り方" steps 3 to 5): mkdirat 0700, then openMade. One
// already there is used
// when it is a directory owned by us that others cannot write to, whatever
// its mode otherwise (left as it is); errNotOurs when it is not.
func makeOrOpen(pfd int, name string) (int, error) {
	err := unix.Mkdirat(pfd, name, 0o700)
	if err == nil {
		return openMade(pfd, name)
	}
	if !errors.Is(err, unix.EEXIST) {
		return -1, err
	}
	fd, err := unix.Openat(pfd, name, dirOpenFlags, 0)
	if err != nil {
		// A symlink (ELOOP), a file (ENOTDIR), ...
		return -1, errNotOurs
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || !isOurDir(&st) || uint32(st.Mode)&0o022 != 0 {
		unix.Close(fd)
		return -1, errNotOurs
	}
	return fd, nil
}

// dirOpenFlags is how B and D are opened: for reading (D takes flock(2), the
// tests list B), never through a symlink.
const dirOpenFlags = unix.O_RDONLY | unix.O_DIRECTORY | unix.O_NOFOLLOW | unix.O_CLOEXEC

// openMade sets the directory name just made in pfd (mkdirat 0700) to 0711
// by its name (fchmodat: whatever a umask took off is put back, so it can be
// opened), opens it O_DIRECTORY | O_NOFOLLOW and checks it is a directory
// owned by us (SPEC-routes "B の作り方" step 4, "D と E" step 2). The parent
// passed its check, so nobody else can replace the name in between.
//
// When the mode cannot be set or the directory cannot be opened, the
// directory made is removed, when the name still looks like it (a directory
// of ours; AT_REMOVEDIR takes only an empty one), and the error returned.
func openMade(pfd int, name string) (int, error) {
	drop := func() {
		var st unix.Stat_t
		if unix.Fstatat(pfd, name, &st, unix.AT_SYMLINK_NOFOLLOW) == nil && isOurDir(&st) {
			unix.Unlinkat(pfd, name, unix.AT_REMOVEDIR)
		}
	}
	if err := unix.Fchmodat(pfd, name, 0o711, 0); err != nil {
		drop()
		return -1, err
	}
	fd, err := unix.Openat(pfd, name, dirOpenFlags, 0)
	if err != nil {
		drop()
		return -1, err
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil || !isOurDir(&st) {
		unix.Close(fd)
		return -1, errNotOurs
	}
	return fd, nil
}

// makeFresh makes a directory named prefix + 10 random characters in pfd
// (another name on EEXIST, linkAttempts at most) as openMade does.
func (w *world) makeFresh(pfd int, prefix string) (string, int, error) {
	part, err := createAt(w.cfg.random, func(n string) error { return unix.Mkdirat(pfd, prefix+n, 0o700) })
	if err != nil {
		return "", -1, err
	}
	fd, err := openMade(pfd, prefix+part)
	return prefix + part, fd, err
}

// isOurDir reports whether st is a directory owned by the effective uid.
func isOurDir(st *unix.Stat_t) bool {
	return uint32(st.Mode)&unix.S_IFMT == unix.S_IFDIR && st.Uid == uint32(os.Geteuid())
}

// checkAncestors checks every directory from the root down to real, a real
// path (SPEC-routes "B の作り方" step 2): each is owned by root or the
// effective uid, and others cannot write to it (group included, mode & 022)
// unless it is sticky (as /tmp is). Someone who can write to an ancestor
// could put another directory in its place. With otherX, each must also have
// search permission for others (mode & 0o001), for a spelling another uid
// follows. EPERM when one fails; the lstat's error when one cannot be looked
// up. A sticky directory needs that owner too: its owner can rename what is
// in it.
func checkAncestors(real string, otherX bool) error {
	cs := components(real)
	p := ""
	us := uint32(os.Geteuid())
	for i, c := range cs {
		switch {
		case i == 0 && c == "/":
			p = "/"
		case p == "/":
			p += c
		default:
			p += "/" + c
		}
		var st unix.Stat_t
		if err := unix.Lstat(p, &st); err != nil {
			return &os.PathError{Op: "lstat", Path: p, Err: err}
		}
		mode := uint32(st.Mode)
		ok := mode&unix.S_IFMT == unix.S_IFDIR && (st.Uid == 0 || st.Uid == us) &&
			(mode&0o022 == 0 || mode&unix.S_ISVTX != 0)
		if ok && otherX {
			ok = mode&0o001 != 0
		}
		if !ok {
			return &os.PathError{Op: "check", Path: p, Err: syscall.EPERM}
		}
	}
	return nil
}

// throughPrivateTmp reports whether p goes through a directory a private
// /tmp replaces: component by component one of cfg.privateTmps or below it,
// or below its real path (macOS's /tmp is /private/tmp).
func (w *world) throughPrivateTmp(p string) bool {
	for _, t := range w.cfg.privateTmps {
		if sameComponents(p, t) || hasPrefixComponents(p, t) {
			return true
		}
		if real, err := realDir(t); err == nil && (sameComponents(p, real) || hasPrefixComponents(p, real)) {
			return true
		}
	}
	return false
}

// hasPrefixComponents reports whether p is below dir, component by component.
func hasPrefixComponents(p, dir string) bool {
	_, ok := below(p, dir)
	return ok
}

// joinUnder spells the entry name of the directory dir (a real path).
func joinUnder(dir, name string) string {
	return strings.TrimSuffix(dir, "/") + "/" + name
}
