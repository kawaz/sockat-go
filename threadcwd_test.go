//go:build darwin || ios || linux || android

package sockat

import (
	"errors"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

func init() {
	mainChildren["thread-cwd-from-the-main-goroutine"] = threadCwdFromTheMainGoroutine
	mainChildren["thread-cwd-unusable-keeps-the-thread"] = threadCwdUnusableKeepsTheThread
}

// The thread cwd runs on a goroutine of its own that exits locked to its
// thread, so that the runtime terminates the thread whose cwd changed. The
// runtime never terminates its main thread: it parks it for good instead,
// cwd and all. A goroutine started from the main goroutine, which then
// waits, runs on the main thread (measured on Linux whatever GOMAXPROCS),
// so the first thread cwd of a program called from main would leave the
// main thread parked in the directory it moved to: on Linux
// /proc/<pid>/cwd (the cwd of the thread group's leader, the main thread)
// would show that directory for the rest of the process, and the directory
// would stay in use (a mount on it could not be unmounted); on any system
// the main thread would be lost to the program.
//
// So, in a child process whose main goroutine (on the main thread) binds
// three times through the thread cwd: a goroutine started from it runs on
// the main thread before (the premise: the case is the one described), the
// sockets appear in the directory, and afterwards a goroutine started from
// it still runs on the main thread (not parked) and /proc/<pid>/cwd, where
// there is one, is what it was.
func TestThreadCwdFromTheMainGoroutineLeavesTheMainThreadAsItWas(t *testing.T) {
	t.Parallel()
	if !threadCwdAvailable() {
		t.Skip("no thread cwd here (a seccomp filter); just test-linux's unconfined pass runs this")
	}
	out := runMainChild(t, "thread-cwd-from-the-main-goroutine")
	if got, want := readChildReport(t, out), "before=true after=true cwd-kept=true"; got != want {
		t.Fatalf("got %q, want %q\n%s", got, want, out)
	}
}

func threadCwdFromTheMainGoroutine() {
	before := spawnedOnMainThread()
	d, err := os.MkdirTemp("/tmp", "sgt-")
	if err != nil {
		mainChildFail("%v", err)
	}
	defer os.RemoveAll(d)
	fd, err := unix.Open(d, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		mainChildFail("%v", err)
	}
	defer unix.Close(fd)
	cwd := processCwd()
	for i := range 3 {
		name := fmt.Sprintf("s%d.sock", i)
		ep, err := inThreadCwd(opListen, fd, name)
		if err != nil {
			mainChildFail("bind %s: %v", name, err)
		}
		if !isSocket(d + "/" + name) {
			mainChildFail("no socket at %s", name)
		}
		ep.Close()
	}
	after := spawnedOnMainThread()
	childReport("before=%t after=%t cwd-kept=%t", before, after, processCwd() == cwd)
}

// The thread of the thread cwd is thrown away only once its cwd may have
// changed: when the route finds the thread cwd unusable (errUnusable: under a
// seccomp filter on Linux, or a failing pthread_fchdir_np on macOS, here for
// a directory without search permission), the thread's cwd is still the
// process's and the thread goes back to the runtime, which then needs no new
// thread. In a child process with one P (GOMAXPROCS=1) whose main goroutine
// holds the main thread, a goroutine on another thread calls the route,
// whose goroutine runs on that same thread while the caller waits: the
// route must be unusable and the caller must go on on the thread it was on.
// Where the thread cwd is usable (Linux without a seccomp filter, as root
// in the container) no failure of it can be brought about, and this is
// skipped.
func TestAnUnusableThreadCwdGivesTheThreadBack(t *testing.T) {
	t.Parallel()
	if runtime.GOOS != "darwin" && threadCwdAvailable() {
		t.Skip("the thread cwd is usable here; just test-linux's default seccomp pass runs this")
	}
	out := runMainChild(t, "thread-cwd-unusable-keeps-the-thread", "GOMAXPROCS=1")
	if got, want := readChildReport(t, out), "unusable=true same-thread=true"; got != want {
		t.Fatalf("got %q, want %q\n%s", got, want, out)
	}
}

func threadCwdUnusableKeepsTheThread() {
	// Held by the main goroutine, the main thread runs no other goroutine.
	runtime.LockOSThread()
	d, err := os.MkdirTemp("/tmp", "sgt-")
	if err != nil {
		mainChildFail("%v", err)
	}
	defer os.RemoveAll(d)
	fd, err := unix.Open(d, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		mainChildFail("%v", err)
	}
	defer unix.Close(fd)
	if err := os.Chmod(d, 0o600); err != nil {
		mainChildFail("%v", err)
	}
	defer os.Chmod(d, 0o700)
	report := make(chan string, 1)
	go func() {
		was := threadSelf()
		_, err := inThreadCwd(opDial, fd, "s.sock")
		report <- fmt.Sprintf("unusable=%t same-thread=%t", errors.Is(err, errUnusable), threadSelf() == was)
	}()
	childReport("%s", <-report)
}

// spawnedOnMainThread reports whether a goroutine started now from the
// calling one runs on the main thread (onMainThread).
func spawnedOnMainThread() bool {
	on := make(chan bool, 1)
	go func() {
		runtime.LockOSThread()
		m := onMainThread()
		runtime.UnlockOSThread()
		on <- m
	}()
	return <-on
}

// processCwd is /proc/<pid>/cwd, the cwd of the thread group's leader, as
// tools that look at the process (lsof, fuser) see it; "" where there is no
// /proc.
func processCwd() string {
	s, _ := os.Readlink("/proc/" + strconv.Itoa(os.Getpid()) + "/cwd")
	return s
}

// mainChildFail ends a main child (mainChildren) with a failure.
func mainChildFail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "main child: "+format+"\n", args...)
	os.Exit(1)
}
