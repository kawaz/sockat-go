package sockat

import (
	"os"
	"sync"
)

// The environment the routes read (config), and what a process keeps of it
// (world): the B it made in each place, held by their fds for the life of the
// process (SPEC "置き場に残る物").

// config is what the route order reads from the environment, made explicit so
// the tests can vary it without touching the process environment.
type config struct {
	// tmp is the first place for B (SPEC-routes "実体の置き場"): /tmp.
	tmp string
	// getenv reads $XDG_RUNTIME_DIR and $TMPDIR, the second and third places,
	// at each call (os.Getenv outside the tests). An empty or relative value
	// leaves that place out.
	getenv func(string) string
	// proc is where the kernel's /proc is (procB's /proc/<pid>/fd/<fd>).
	proc string
	// privateTmps are the directories a systemd PrivateTmp (or the like)
	// replaces: a real-path spelling through one of them, or through its real
	// path, is not usable with a private /tmp (Spelling's
	// UsableWithPrivateTmp).
	privateTmps []string
	// bindFollows is whether bind(2) here follows a symlink in the last
	// component (link-file of a bind; the systems' table, bindFollowsHere).
	bindFollows follows
	// random names D, the E made for one call and a B fallen back to:
	// randomChars outside the tests.
	random func() (string, error)

	// dBases is where D may go for a dial, in the order tried:
	// ${TMPDIR:-/tmp} (a relative one too), then /tmp. The socket's directory
	// comes after them.
	dBases []string
	// tmpBases is where the temporary symlink of symlink-file (a dial's, and
	// a listen's where bind(2) follows it) may go, in the order tried: TMPDIR
	// when it is set to an absolute path, then /tmp.
	tmpBases []string
	// procSelfFD is where the kernel lists the fds of the process (Linux).
	// The tests point it elsewhere to stand in for a system without /proc.
	procSelfFD string
	// dirBases is where the symlink of symlink-dir (ListenEx's last resort)
	// may go, in the order tried within each kind of place: XDG_RUNTIME_DIR,
	// TMPDIR (when set to absolute paths), then /tmp.
	dirBases []string
	// trace, when set (ListenEx, DialEx), records the routes found unusable.
	trace *trace
	// threadCwd is whether to try the thread-cwd route. Always true outside
	// the tests, which turn it off to stand in for a system that refuses it
	// (a seccomp filter on unshare(2), for example).
	threadCwd bool
}

// follows is whether bind(2) follows a symlink in the last component.
type follows int

const (
	// followsUnknown: not measured here; a link-file bind is tried and told
	// at run time (SPEC-routes "link-file").
	followsUnknown follows = iota
	// followsYes: measured to follow (macOS, NetBSD).
	followsYes
	// followsNo: measured not to follow (Linux, FreeBSD, OpenBSD, illumos);
	// link-file is not in the bind order.
	followsNo
)

// envConfig is the configuration of the process's environment: the places
// /tmp, $XDG_RUNTIME_DIR and $TMPDIR (read at each call), /proc, and the
// system's table of whether bind(2) follows a symlink.
func envConfig() config {
	return config{
		tmp:         "/tmp",
		getenv:      os.Getenv,
		proc:        "/proc",
		privateTmps: []string{"/tmp", "/var/tmp"},
		bindFollows: bindFollowsHere,
		random:      randomChars,
		threadCwd:   true,
	}
}

func configFromEnv() *config {
	tmpdir, _ := os.LookupEnv("TMPDIR")
	return configFromTmpdir(tmpdir)
}

// configFromTmpdir is the configuration for a TMPDIR of tmpdir: TMPDIR, then
// /tmp. An unset or empty TMPDIR leaves /tmp alone; a relative one is left out
// (the link's place would depend on the cwd); one that is /tmp itself is tried
// once.
func configFromTmpdir(tmpdir string) *config {
	var bases []string
	if isAbs(tmpdir) && !sameComponents(tmpdir, "/tmp") {
		bases = append(bases, tmpdir)
	}
	bases = append(bases, "/tmp")
	dBases := []string{"/tmp"}
	if tmpdir != "" && !sameComponents(tmpdir, "/tmp") {
		dBases = []string{tmpdir, "/tmp"}
	}
	runtimeDir, _ := os.LookupEnv("XDG_RUNTIME_DIR")
	c := envConfig()
	c.dBases, c.tmpBases, c.dirBases = dBases, bases, dirBasesFrom(runtimeDir, tmpdir)
	c.procSelfFD = "/proc/self/fd"
	return &c
}

// dirBasesFrom is symlink-dir's places for the symlink: XDG_RUNTIME_DIR and
// TMPDIR when absolute, then /tmp, each spelling once.
func dirBasesFrom(runtimeDir, tmpdir string) []string {
	var bases []string
	for _, b := range []string{runtimeDir, tmpdir, "/tmp"} {
		if !isAbs(b) {
			continue
		}
		dup := false
		for _, seen := range bases {
			if sameComponents(seen, b) {
				dup = true
			}
		}
		if !dup {
			bases = append(bases, b)
		}
	}
	return bases
}

// sameComponents reports whether a and b are the same spelling component by
// component ("/tmp/" and "/tmp" are; "/private/tmp" and "/tmp" are not, even
// where they name the same directory, because their lengths differ).
func sameComponents(a, b string) bool {
	ca, cb := components(a), components(b)
	if len(ca) != len(cb) {
		return false
	}
	for i := range ca {
		if ca[i] != cb[i] {
			return false
		}
	}
	return true
}

// world is what a process keeps across calls: the B it made, each held by
// its fd (never closed), and the places whose ancestors were checked.
//
// A B that could be made is kept and used by every later call; one that
// could not is not remembered, and the next call tries the places again
// (SPEC-routes "実体の置き場").
type world struct {
	cfg config

	mu sync.Mutex
	// auto is the B of the places 1 to 3 (/tmp, $XDG_RUNTIME_DIR, $TMPDIR):
	// one per process.
	auto *base
	// autoCross is the B of a usable_cross_uid bind: the first of the places
	// 1 to 3 where B and every directory on the way to it have search
	// permission for others. auto itself when it is such a B.
	autoCross *base
	// sockdir is the B of the place 4, one per socket's directory (its
	// (st_dev, st_ino)).
	sockdir map[fileID]*base
	// given is the B of Options.Base, by its (st_dev, st_ino).
	given map[fileID]*base
	// checked is the real paths of the places whose ancestors passed the
	// check (SPEC-routes "B の作り方" step 2: once per process).
	checked map[string]bool
}

func newWorld(cfg config) *world {
	return &world{cfg: cfg, sockdir: map[fileID]*base{}, given: map[fileID]*base{}, checked: map[string]bool{}}
}

// defaultWorld is the process's: the public API's calls share its B and D.
var defaultWorld = newWorld(envConfig())

// bases is every B the world holds, each once.
func (w *world) bases() []*base {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []*base
	seen := map[*base]bool{}
	add := func(b *base) {
		if b != nil && !seen[b] {
			seen[b] = true
			out = append(out, b)
		}
	}
	add(w.auto)
	add(w.autoCross)
	for _, b := range w.sockdir {
		add(b)
	}
	for _, b := range w.given {
		add(b)
	}
	return out
}
