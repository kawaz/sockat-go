//go:build linux || darwin

package sockat

// Every route of the system, each forced by its inputs and run through the
// same checks. The routes are listed per system by ways (linux_test.go,
// darwin_test.go): each way is one socket path and the route order's inputs
// chosen so that a listen and a dial there take one known route.

import (
	"net"
	"os"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// way is one socket path reached one fixed way.
type way struct {
	name string
	path string
	// cfg is the route order's inputs; nil for the path the net package takes
	// (run, through the public order).
	cfg *config
	// listen and dial are the routes a listen and a dial there must take.
	listen, dial route
}

func (w way) run(o op) (endpoint, route, error) {
	if w.cfg == nil {
		return run(o, w.path)
	}
	return runLong(o, w.path, w.cfg)
}

// sibling is a path next to w.path with a name of the same length, so it takes
// the same route.
func (w way) sibling(first byte) string {
	dir, name, _ := splitPath(w.path)
	return dir + "/" + string(first) + name[1:]
}

// usable reports whether the way can run here: the thread-cwd route only
// where the system allows a thread a cwd of its own.
func (w way) usable() bool {
	return threadCwdAvailable() || (w.listen != routeThreadCwd && w.dial != routeThreadCwd)
}

func (w way) listenOn(t *testing.T) *net.UnixListener {
	t.Helper()
	ep, r, err := w.run(opListen)
	l := must{t}.listener(ep, err)
	if r != w.listen {
		t.Fatalf("listen: %s, want %s", r, w.listen)
	}
	if !isSocket(w.path) {
		t.Fatalf("no socket at the path (%d bytes)", len(w.path))
	}
	return l
}

func (w way) dialTo(t *testing.T) *net.UnixConn {
	t.Helper()
	ep, r, err := w.run(opDial)
	c := must{t}.conn(ep, err)
	if r != w.dial {
		t.Fatalf("dial: %s, want %s", r, w.dial)
	}
	return c
}

// bases are the places for the symlink of the way, which must be left empty.
func (w way) bases() []string {
	if w.cfg == nil {
		return nil
	}
	return w.cfg.tmpBases
}

// On every route: the socket is at the given path, a dial the same way reaches
// that listener and data flows both ways, the listener and the connection are
// the net package's types in full working order (deadlines work, so they are
// registered with the runtime's poller), the addresses the kernel reports can
// be read without a panic, the server sees this process as the peer (no route
// connects from another process), and nothing is left in the places for the
// symlink.
func TestEveryRouteListensAndDialsAtThePath(t *testing.T) {
	t.Parallel()
	for _, w := range ways(t) {
		t.Run(w.name, func(t *testing.T) {
			if !w.usable() {
				t.Skip("this system refuses a thread a cwd of its own")
			}
			l := w.listenOn(t)
			c := w.dialTo(t)
			checkStdTypes(t, l, c)

			c2 := w.dialTo(t)
			l.SetDeadline(nowPlusGuard())
			s, err := l.AcceptUnix()
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if pid := peerPid(t, s); pid != os.Getpid() {
				t.Fatalf("the peer is pid %d, not this process (%d)", pid, os.Getpid())
			}
			pingConns(t, s, c2)
			t.Logf("listener %v, conn local %v remote %v, accepted local %v remote %v", l.Addr(), c2.LocalAddr(), c2.RemoteAddr(), s.LocalAddr(), s.RemoteAddr())
			for _, b := range w.bases() {
				assertEmpty(t, b)
			}
		})
	}
}

// relisten changes the backlog of a listening socket.
func relisten(t *testing.T, l *net.UnixListener, n int) {
	t.Helper()
	rc, err := l.SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var lerr error
	if err := rc.Control(func(fd uintptr) { lerr = unix.Listen(int(fd), n) }); err != nil {
		t.Fatal(err)
	}
	if lerr != nil {
		t.Fatal(lerr)
	}
}

// A dial must not wait for room in a full backlog on any route, as the net
// package's own does not: its connect is nonblocking, and Linux answers
// EAGAIN where a blocking connect would sleep until the listener accepts
// (macOS answers ECONNREFUSED either way). Each route must hand the net package
// the spelling, or adopt its own socket, so that this holds: a route that
// connected a blocking socket of its own would hang here. With the backlog
// shrunk to 1 and filled (Linux lets backlog + 1 connections wait, macOS
// fewer), the next dial fails at once with that error, and keeps failing while
// it stays full; every dial that got in took the way's route.
func TestADialFailsAtOnceOnAFullBacklogOnEveryRoute(t *testing.T) {
	t.Parallel()
	for _, w := range ways(t) {
		t.Run(w.name, func(t *testing.T) {
			if !w.usable() {
				t.Skip("this system refuses a thread a cwd of its own")
			}
			l := w.listenOn(t)
			relisten(t, l, 1)
			type result struct {
				ep  endpoint
				r   route
				err error
			}
			dial := func() result {
				return atOnce(t, "a dial to a full backlog", func() result {
					ep, r, err := w.run(opDial)
					return result{ep, r, err}
				})
			}
			queued := 0
			var full error
			for full == nil {
				if queued == 16 {
					t.Fatal("the backlog never filled")
				}
				res := dial()
				if res.err != nil {
					full = res.err
					break
				}
				defer res.ep.Close()
				if res.r != w.dial {
					t.Fatalf("dial: %s, want %s", res.r, w.dial)
				}
				queued++
			}
			if queued == 0 {
				t.Fatalf("nothing got in before the backlog was full: %v", full)
			}
			wantErrno(t, "dial to a full backlog", full, fullBacklog)
			wantErrno(t, "dial to a still full backlog", dial().err, fullBacklog)
		})
	}
}

// earlierThanSymlink: the routes that come before the symlink route in the
// order (and the symlink route itself, before a further place). A base added
// after the way's own places is never used by them, so it must stay untouched
// when their call fails.
var earlierThanSymlink = []route{routeProc, routeRelative, routeWide, routeWideRelative, routeSymlinkDir, routeSymlinkFile, routeSymlinkWide}

// On every route, a listen / dial that fails is the result: the kernel's
// error for the path (EADDRINUSE for an existing file, ENOENT for a missing
// socket, ECONNREFUSED for a socket nobody listens on) comes back as it is,
// and the order does not move on. Where a later route exists, it is a place
// for the symlink added after the way's own, and it stays untouched: no link
// was even created there. The siblings have names of the same length as the
// way's path, so they take the same route.
func TestAFailureOnEveryRouteIsReturnedAndNotRetried(t *testing.T) {
	t.Parallel()
	for _, w := range ways(t) {
		t.Run(w.name, func(t *testing.T) {
			if !w.usable() {
				t.Skip("this system refuses a thread a cwd of its own")
			}
			later := tmpDir(t)
			if w.cfg != nil {
				cfg := *w.cfg
				cfg.tmpBases = append(slices.Clone(cfg.tmpBases), later)
				w.cfg = &cfg
			}
			existing := w.sibling('e')
			if err := os.WriteFile(existing, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			stale := w.sibling('z')
			staleSocket(t, stale)
			cases := []struct {
				what  string
				o     op
				path  string
				errno syscall.Errno
				r     route
			}{
				{"listen on an existing file", opListen, existing, syscall.EADDRINUSE, w.listen},
				{"dial a missing socket", opDial, w.sibling('m'), syscall.ENOENT, w.dial},
				{"dial a socket nobody listens on", opDial, stale, syscall.ECONNREFUSED, w.dial},
			}
			for _, c := range cases {
				markUntouched(t, later)
				ww := w
				ww.path = c.path
				_, _, err := ww.run(c.o)
				wantErrno(t, c.what, err, c.errno)
				if slices.Contains(earlierThanSymlink, c.r) {
					assertUntouched(t, later)
				}
			}
			for _, b := range w.bases() {
				assertEmpty(t, b)
			}
		})
	}
}

// No route changes the cwd of the process: the relative spelling only reads
// it, the thread-cwd route changes the cwd of one thread, which is then
// discarded, and the others never look at it. Watcher goroutines compare
// getcwd(3) and (st_dev, st_ino) of "." with the starting values the whole
// time workers listen and dial on every route, each on its own set of paths.
// Afterwards a burst of goroutines locked to threads checks the same: had a
// thread whose cwd the thread-cwd route changed been handed back to the
// runtime, a goroutine scheduled on it would see the socket's directory.
func TestNoRouteChangesTheProcessCwd(t *testing.T) {
	t.Parallel()
	cwd, err := syscall.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	id, ok := statID(".")
	if !ok {
		t.Fatal("cannot stat the cwd")
	}
	check := func() string {
		now, err := syscall.Getwd()
		if err != nil {
			return err.Error()
		}
		if nowID, _ := statID("."); now != cwd || nowID != id {
			return now
		}
		return ""
	}

	var stop atomic.Bool
	var checks atomic.Int64
	var bad atomic.Value
	var watchers sync.WaitGroup
	for range 4 {
		watchers.Go(func() {
			for !stop.Load() {
				if s := check(); s != "" {
					bad.Store(s)
				}
				checks.Add(1)
			}
		})
	}

	var workers sync.WaitGroup
	for range 4 {
		ws := ways(t)
		workers.Go(func() {
			for round := range 5 {
				for _, w := range ws {
					if !w.usable() {
						continue
					}
					l, r, err := w.run(opListen)
					if err != nil || r != w.listen {
						t.Errorf("%s round %d: listen %s, %v", w.name, round, r, err)
						return
					}
					c, r, err := w.run(opDial)
					l.Close()
					if err != nil || r != w.dial {
						t.Errorf("%s round %d: dial %s, %v", w.name, round, r, err)
						return
					}
					c.Close()
					// A long path's listener leaves its socket file (a short
					// one's removes it): clear it for the next round.
					os.Remove(w.path)
				}
			}
		})
	}
	workers.Wait()
	stop.Store(true)
	watchers.Wait()
	if s := bad.Load(); s != nil {
		t.Fatalf("the cwd of the process changed to %v", s)
	}
	if checks.Load() == 0 {
		t.Fatal("the watchers never checked")
	}

	results := make(chan string, 64)
	for range 64 {
		go func() {
			runtime.LockOSThread()
			defer runtime.UnlockOSThread()
			results <- check()
		}()
	}
	for range 64 {
		if s := <-results; s != "" {
			t.Fatalf("a thread of the runtime has the cwd %s", s)
		}
	}
}
