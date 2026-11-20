package sockat

import (
	"os"
	"strconv"
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
// estimate before B is opened. "" where there is no /proc, or no
// /proc/<pid>/fd to look up (procB then cannot be used whatever B is).
func (w *world) procBShortest() string {
	if !hasProc {
		return ""
	}
	if _, ok := statID(w.procFD()); !ok {
		return ""
	}
	return w.procFD() + "/0"
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
	return Spelling{Kind: Linked, Removable: removable, Until: UntilCleanup, UsableCrossUID: b.crossUID, UsableWithPrivateTmp: !b.tmpPath}
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
