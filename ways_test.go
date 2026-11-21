//go:build linux || darwin

package sockat

// Every connect route of the system, each forced by its inputs and run
// through the same checks. The routes are listed per system by ways
// (os_linux_test.go, os_darwin_test.go): each way is one socket path and a
// test world (testWorld) chosen so that a dial there takes one known route;
// the listener is made by listenAt, except for the path the net package
// takes, which Listen binds. One way is a socket near the cwd (the relative
// spelling), so the tests that go through every way run in a child process
// whose cwd is a temporary directory (inTmpCwd).

import (
	"net"
	"os"
	"runtime"
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
	// w is the world the dial goes through, nil for the path the net package
	// takes (defaultWorld: nothing is made in the real places for it).
	w *world
	// places are w's places, which hold nothing after a dial but an empty B
	// that link-file made.
	places testPlaces
	// listen is the route a listen there must take; "" where the listener is
	// made by listenAt. dial is the route a dial must take.
	listen, dial route
}

// run listens (the standard way only: the others listen with listenAt) or
// dials the way.
func (w way) run(o op) (endpoint, route, error) {
	if o == opListen {
		l, b, err := defaultWorld.listen(w.path, nil)
		if err != nil {
			return nil, b.route, err
		}
		return l, b.route, nil
	}
	wd := w.w
	if wd == nil {
		wd = defaultWorld
	}
	c, r, err := wd.dial(w.path, Options{}, nil)
	if err != nil {
		return nil, r, err
	}
	return c, r, nil
}

// sibling is a path next to w.path with a name of the same length, so it takes
// the same route.
func (w way) sibling(first byte) string {
	dir, name, _ := splitPath(w.path)
	return dir + "/" + string(first) + name[1:]
}

// usable reports whether the way can run here: thread-cwd-direct only
// where the system allows a thread a cwd of its own.
func (w way) usable() bool {
	return threadCwdAvailable() || w.dial != routeThreadCwdDirect
}

func (w way) listenOn(t *testing.T) *net.UnixListener {
	t.Helper()
	if w.listen == "" {
		return listenAt(t, w.path)
	}
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

// checkPlaces checks what the way's dials left in its places: no D and no E
// (assertNoDOrE), and nothing in a place but the B link-file made there.
func (w way) checkPlaces(t *testing.T) {
	t.Helper()
	if w.w == nil {
		return
	}
	assertNoDOrE(t, w.w)
	for _, p := range []struct{ kind, dir string }{{kindTmp, w.places.tmp}, {kindRuntime, w.places.runtime}, {kindTmpdir, w.places.tmpdir}} {
		for _, e := range entries(t, p.dir) {
			if w.dial != routeLinkFile || e != bName(p.kind) {
				t.Fatalf("left in %s: %s", p.dir, e)
			}
		}
	}
}

// markPlaces marks the way's places untouched (markUntouched).
func (w way) markPlaces(t *testing.T) {
	t.Helper()
	if w.w != nil {
		for _, d := range []string{w.places.tmp, w.places.runtime, w.places.tmpdir} {
			markUntouched(t, d)
		}
	}
}

// assertPlacesUntouched checks that nothing was made in or removed from the
// way's places since markPlaces: no route that writes in a place was tried.
func (w way) assertPlacesUntouched(t *testing.T) {
	t.Helper()
	if w.w != nil {
		for _, d := range []string{w.places.tmp, w.places.runtime, w.places.tmpdir} {
			assertUntouched(t, d)
		}
	}
}

// On every route: the socket is at the given path, a dial the same way reaches
// that listener and data flows both ways, the listener and the connection are
// the net package's types in full working order (deadlines work, so they are
// registered with the runtime's poller), the addresses the kernel reports can
// be read without a panic, the server sees this process as the peer (no route
// connects from another process), and nothing is left in the places: no D,
// no E, and no B but the one link-file made (a dial keeps no state but the
// process's B).
func TestEveryRouteListensAndDialsAtThePath(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
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
			w.checkPlaces(t)
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
// (macOS answers ECONNREFUSED either way). Every route hands the net package
// a spelling, from the caller's goroutine or a thread of its own, so this
// holds; a route that connected a blocking socket of its own would hang here.
// With the backlog shrunk to 1 and filled (Linux lets backlog + 1 connections
// wait, macOS fewer), the next dial fails at once with that error, and keeps
// failing while it stays full; every dial that got in took the way's route,
// and the places hold no D or E afterwards (link-file unlinks its E after a
// failed connect too).
func TestADialFailsAtOnceOnAFullBacklogOnEveryRoute(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
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
			still := dial()
			wantErrno(t, "dial to a still full backlog", still.err, fullBacklog)
			if still.r != w.dial {
				t.Fatalf("the failed dial: %s, want %s", still.r, w.dial)
			}
			w.checkPlaces(t)
		})
	}
}

// On every route, a listen / dial that fails is the result: the kernel's
// error for the path (EADDRINUSE for an existing file, ENOENT for a missing
// socket, ECONNREFUSED for a socket nobody listens on) comes back as it is,
// with the way's route as the route that failed, and the order does not move
// on: a way before link-file leaves its places untouched (link-file, later,
// was not tried), and link-file leaves no E. Only the standard way listens
// through sockat here (the bind's routes have their own:
// TestAFailureOfABindIsReturnedAndNotRetried). The siblings have names of the
// same length as the way's path, so they take the same route.
func TestAFailureOnEveryRouteIsReturnedAndNotRetried(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	for _, w := range ways(t) {
		t.Run(w.name, func(t *testing.T) {
			if !w.usable() {
				t.Skip("this system refuses a thread a cwd of its own")
			}
			existing := w.sibling('e')
			if err := os.WriteFile(existing, nil, 0o600); err != nil {
				t.Fatal(err)
			}
			stale := w.sibling('z')
			staleSocket(t, stale)
			type failure struct {
				what  string
				o     op
				path  string
				errno syscall.Errno
			}
			cases := []failure{
				{"dial a missing socket", opDial, w.sibling('m'), syscall.ENOENT},
				{"dial a socket nobody listens on", opDial, stale, syscall.ECONNREFUSED},
			}
			if w.listen != "" {
				cases = append(cases, failure{"listen on an existing file", opListen, existing, syscall.EADDRINUSE})
			}
			for _, c := range cases {
				w.markPlaces(t)
				ww := w
				ww.path = c.path
				_, r, err := ww.run(c.o)
				wantErrno(t, c.what, err, c.errno)
				if want := map[op]route{opDial: w.dial, opListen: w.listen}[c.o]; r != want {
					t.Fatalf("%s: route %q, want %s", c.what, r, want)
				}
				if w.dial != routeLinkFile {
					w.assertPlacesUntouched(t)
				}
			}
			w.checkPlaces(t)
		})
	}
}

// No route changes the cwd of the process: the relative spelling only reads
// it, thread-cwd-direct changes the cwd of one thread, which is then
// discarded, and the others never look at it. Watcher goroutines compare
// getcwd(3) and (st_dev, st_ino) of "." with the starting values the whole
// time workers listen and dial on every route, each on its own set of paths.
// Afterwards a burst of goroutines locked to threads checks the same: had a
// thread whose cwd thread-cwd-direct changed been handed back to the
// runtime, a goroutine scheduled on it would find that cwd (the socket's
// directory).
func TestNoRouteChangesTheProcessCwd(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
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
		var ws []way
		for _, w := range ways(t) {
			if !w.usable() {
				continue
			}
			if w.listen == "" {
				listenAt(t, w.path)
			}
			ws = append(ws, w)
		}
		workers.Go(func() {
			for round := range 5 {
				for _, w := range ws {
					var l endpoint
					if w.listen != "" {
						var r route
						var err error
						l, r, err = w.run(opListen)
						if err != nil || r != w.listen {
							t.Errorf("%s round %d: listen %s, %v", w.name, round, r, err)
							return
						}
					}
					c, r, err := w.run(opDial)
					if l != nil {
						l.Close()
					}
					if err != nil || r != w.dial {
						t.Errorf("%s round %d: dial %s, %v", w.name, round, r, err)
						return
					}
					c.Close()
					if l != nil {
						// A long path's listener leaves its socket file (a
						// short one's removes it): clear it for the next round.
						os.Remove(w.path)
					}
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

// A datagram socket is connected by the connect order of a stream socket
// (SPEC "connect の順"): every way of the system, a socket bound at its path,
// a datagram socket connected there the way DialUnixgram connects takes the
// way's route, and what it sends arrives; nothing is left in the places but
// the B link-file made.
func TestEveryConnectRouteReachesADatagramSocket(t *testing.T) {
	t.Parallel()
	if !inTmpCwd(t) {
		return
	}
	for _, w := range ways(t) {
		t.Run(w.name, func(t *testing.T) {
			if !w.usable() {
				t.Skip("this system refuses a thread a cwd of its own")
			}
			s := gramAt(t, w.path)
			wd := w.w
			if wd == nil {
				wd = defaultWorld
			}
			c, r := connectGramIn(t, wd, w.path)
			if r != w.dial {
				t.Fatalf("connect: %s, want %s", r, w.dial)
			}
			if _, err := c.Write([]byte(w.name)); err != nil {
				t.Fatal(err)
			}
			recv(t, s, w.name)
			w.checkPlaces(t)
		})
	}
}
