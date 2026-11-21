package sockat

import (
	"os"
	"strconv"

	"golang.org/x/sys/unix"
)

// The spellings through B (SPEC-routes "綴りの形"): procB,
// /proc/<pid>/fd/<B's fd>, and B's real path. What a spelling through B is
// good for afterwards (Spelling) and the diagnostics' base follow from which
// of the two it is.

// procB is B spelt through /proc, when /proc can be used for it: a stat of
// /proc/<pid>/fd/<B's fd> has B's (st_dev, st_ino). Where the PID namespace
// and the /proc mounted disagree, the path may name another process's fd,
// which this rules out.
func (w *world) procB(b *base) (string, bool) {
	if !hasProc {
		return "", false
	}
	p := w.procFD() + "/" + strconv.Itoa(b.fd)
	id, ok := statID(p)
	return p, ok && id == b.id
}

// procFD is /proc/<pid>/fd.
func (w *world) procFD() string {
	return w.cfg.proc + "/" + strconv.Itoa(os.Getpid()) + "/fd"
}

// procBShortest is the shortest procB can be (a one-digit fd), for the
// estimate before B is opened. "" where /proc cannot be used for procB, as
// procB itself checks it but with a directory opened for the check (SPEC
// "用語"): where /proc/<pid>/fd does not show it, procB cannot be used
// whatever B is.
func (w *world) procBShortest() string {
	if !hasProc || !procShows(w.procFD(), -1) {
		return ""
	}
	return w.procFD() + "/0"
}

// procUsable reports whether the connect takes the row of the connect order
// where /proc can be used (SPEC "用語"): <proc>/self/fd shows / opened for
// the check. Where the PID namespace and the /proc mounted disagree, or /proc
// is not there, it does not. Whether proc-direct can be used for the call's
// directory is checked apart (dialCall.procDirect).
func procUsable(proc string) bool {
	return hasProc && procShows(proc+"/self/fd", -1)
}

// procShows reports whether the entry <fds>/<fd> (fds a /proc/self/fd or a
// /proc/<pid>/fd) has the (st_dev, st_ino) of the directory fd holds; for fd
// -1, of / opened for the check.
func procShows(fds string, fd int) bool {
	if fd < 0 {
		f, err := openDir("/")
		if err != nil {
			return false
		}
		defer f.Close()
		fd = int(f.Fd())
	}
	var held unix.Stat_t
	if unix.Fstat(fd, &held) != nil {
		return false
	}
	id, ok := statID(fds + "/" + strconv.Itoa(fd))
	return ok && id == fileID{uint64(held.Dev), uint64(held.Ino)}
}

// linkDirLen is the length of link-dir's spelling <bspell>/<D>/<E>/<name>.
func linkDirLen(bspell, e, name string) int {
	return len(bspell) + 1 + linkRandomLen + 1 + len(e) + 1 + len(name)
}

// linkFileLen is the length of link-file's spelling <bspell>/<D>/<E>.
func linkFileLen(bspell, e string) int {
	return len(bspell) + 1 + linkRandomLen + 1 + len(e)
}

// danglingLen is the length of thread-cwd's and at's spelling <E>/<name>,
// relative to D.
func danglingLen(name string) int {
	return linkRandomLen + 1 + len(name)
}

// linkedSpelling is what a spelling through b is good for: through procB
// (viaProc) it lasts as long as the process, can be followed by the same uid
// only, and does not go through /tmp; through B's real path it lasts until
// the cleanup, and b's mode and place decide the rest.
func linkedSpelling(b *base, viaProc, removable bool) Spelling {
	if viaProc {
		return Spelling{Kind: Linked, Removable: removable, Until: UntilProcess, UsableCrossUID: false, UsableWithPrivateTmp: true}
	}
	return Spelling{Kind: Linked, Removable: removable, Until: UntilCleanup, UsableCrossUID: b.crossUID(), UsableWithPrivateTmp: !b.tmpPath}
}

// baseValue is the diagnostics' base of a spelling through b: the form of
// the spelling for a B in a temporary place (procB is "proc", a real path the
// place's kind), and the B itself otherwise ("sockdir", "given").
func baseValue(b *base, viaProc bool) string {
	if viaProc && (b.kind == kindTmp || b.kind == kindRuntime || b.kind == kindTmpdir) {
		return "proc"
	}
	return b.kind
}
