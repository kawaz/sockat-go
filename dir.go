package sockat

import (
	"errors"
	"strconv"

	"golang.org/x/sys/unix"
)

// D, the directory a process holds in a B, and E, the symlinks in it
// (SPEC-routes "D と E").

// procD is a D: held open with a shared flock(2) for the life of the process,
// which tells sweep() that its process is alive.
type procD struct {
	fd   int    // D (never closed)
	name string // D's name in B
}

// dirAttempts is how many times D is made again when the D just made was
// taken away (by another process's sweep) before it was locked.
const dirAttempts = 16

// ownD is the process's D in b, made the first time (under b.mu). A failure
// is not remembered: the next call tries again.
func (b *base) ownD(cfg *config) (*procD, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.ownDLocked(cfg)
}

func (b *base) ownDLocked(cfg *config) (*procD, error) {
	if b.d != nil {
		return b.d, nil
	}
	for range dirAttempts {
		d, err := makeOwnD(b.fd, cfg.random)
		if errors.Is(err, errDTakenAway) {
			continue
		}
		if err != nil {
			return nil, err
		}
		b.d = d
		return d, nil
	}
	return nil, errDTakenAway
}

// errDTakenAway is D's name no longer naming the D just made and locked.
var errDTakenAway = errors.New("sockat: D was removed before it was locked")

// makeOwnD makes a D in B (bfd), SPEC-routes "D と E" steps 1 to 4: mkdirat 0700
// under a random name (another on EEXIST, linkAttempts at most), set to 0711,
// opened O_NOFOLLOW and checked to be a directory of ours (openMade), locked
// (LOCK_SH), and its name looked up again: a D taken away between the mkdirat
// and the lock (another process's sweep() saw it unlocked) is
// errDTakenAway, its fd closed.
func makeOwnD(bfd int, random func() (string, error)) (*procD, error) {
	name, err := createAt(random, func(n string) error { return unix.Mkdirat(bfd, n, 0o700) })
	if err != nil {
		return nil, err
	}
	fd, err := openMade(bfd, name)
	if err != nil {
		return nil, err
	}
	if err := lockD(fd, unix.LOCK_SH); err != nil {
		unix.Close(fd)
		unix.Unlinkat(bfd, name, unix.AT_REMOVEDIR)
		return nil, err
	}
	var held, now unix.Stat_t
	if err := unix.Fstat(fd, &held); err != nil {
		unix.Close(fd)
		return nil, err
	}
	if err := unix.Fstatat(bfd, name, &now, unix.AT_SYMLINK_NOFOLLOW); err != nil || now.Dev != held.Dev || now.Ino != held.Ino {
		unix.Close(fd)
		return nil, errDTakenAway
	}
	return &procD{fd: fd, name: name}, nil
}

// lockD is flock(2) for D (SPEC-routes "D と E" step 3).
//
// Design rationale: a variable rather than a direct call only so that a test
// (TestADTakenAwayBeforeItIsLockedIsMadeAgain, in a child process) can take D
// away between its mkdirat and the check after the lock, which no input of
// makeOwnD can bring about. Nothing else assigns it.
var lockD = unix.Flock

// link is an E this process made in its D of b.
type link struct {
	b    *base
	d    *procD
	name string
}

// remove unlinks E. A missing E is no error (removed already).
func (e *link) remove() error {
	err := unix.Unlinkat(e.d.fd, e.name, 0)
	if err == nil || errors.Is(err, unix.ENOENT) {
		return nil
	}
	return err
}

// path is E's real path (B's real path, D, E).
func (e *link) path() string {
	return e.b.real + "/" + e.d.name + "/" + e.name
}

// resolveE makes an E in d (b's D) pointing to target, named by b's counter
// in base 36 (SPEC-routes "D と E": D is this process's alone, so a short
// counter is unique). A name taken (by nothing of ours, so far) is passed
// over for the next.
func (b *base) resolveE(d *procD, target string) (*link, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for range linkAttempts {
		name := strconv.FormatUint(b.nextE, 36)
		b.nextE++
		err := symlinkat(target, d.fd, b.real+"/"+d.name, name)
		if err == nil {
			return &link{b, d, name}, nil
		}
		if !errors.Is(err, unix.EEXIST) {
			return nil, err
		}
	}
	return nil, errNoFreeName
}

// nextEName is the name the next resolveE of b will try first, for the
// estimate of a spelling's length.
func (b *base) nextEName() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strconv.FormatUint(b.nextE, 36)
}

// callE makes an E for one call in d (b's D) pointing to target, named by 10
// random characters (another on EEXIST, linkAttempts at most).
func (b *base) callE(d *procD, target string, random func() (string, error)) (*link, error) {
	name, err := createAt(random, func(n string) error { return symlinkat(target, d.fd, b.real+"/"+d.name, n) })
	if err != nil {
		return nil, err
	}
	return &link{b, d, name}, nil
}
