//go:build darwin || ios || linux || android

package sockat

import (
	"errors"
	"runtime"
)

// hasThreadCwd: this system has per-thread working directories.
const hasThreadCwd = true

// mainThread is the thread the package is initialized on: the Go runtime's
// main thread, on which the runtime runs every package's init (it throws
// otherwise), in a program and in a c-archive / c-shared library alike.
var mainThread = threadSelf()

// inThreadCwd binds (and listens) or connects through spelling, relative to
// the directory dirfd, from a thread whose own working directory is dirfd
// (threadChdir). No other thread's cwd changes, the caller's included.
//
// The work runs on a goroutine of its own, locked to its OS thread, that
// exits without unlocking once the thread's cwd may have changed: the Go
// runtime then terminates the thread instead of handing it to other
// goroutines, so the changed cwd dies with it. The runtime never starts new
// threads from a locked one, so none inherits the cwd either. The main thread
// is the exception: the runtime never terminates it but parks it for good,
// which would keep its cwd (on Linux /proc/<pid>/cwd) in the directory, so
// the work never runs there (inOwnThread).
//
// The thread belongs to this process: the peer a server sees is this process,
// and no file descriptor is duplicated. errUnusable when the thread cannot
// have a cwd of its own.
func inThreadCwd(o op, dirfd int, spelling string) (endpoint, error) {
	done := make(chan threadResult, 1)
	go inOwnThread(o, dirfd, spelling, done)
	r := <-done
	return r.ep, r.err
}

// threadResult is what the goroutine of inOwnThread hands back.
type threadResult struct {
	ep  endpoint
	err error
}

// inOwnThread is the goroutine of inThreadCwd: it locks itself to its thread,
// moves the thread's cwd to dirfd, binds or connects through spelling, and
// sends the outcome to done.
//
// A goroutine started from one that then waits (the main goroutine above
// all) often runs on the main thread. There it keeps the main thread locked,
// so that the runtime runs no other goroutine on it, and starts another
// inOwnThread, which therefore runs on another thread; once that one is done
// it unlocks the main thread, whose cwd it never changed.
func inOwnThread(o op, dirfd int, spelling string, done chan<- threadResult) {
	// Design rationale: no UnlockOSThread once threadChdir may have changed
	// the thread's cwd. The thread's cwd is then no longer the process's, so
	// the thread must not run anything else; exiting while locked makes the
	// runtime terminate it.
	runtime.LockOSThread()
	if onMainThread() {
		other := make(chan threadResult, 1)
		go inOwnThread(o, dirfd, spelling, other)
		r := <-other
		runtime.UnlockOSThread()
		done <- r
		return
	}
	if err := threadChdir(dirfd); err != nil {
		// errUnusable leaves the thread's cwd as it was (threadChdir): the
		// thread goes back to the runtime rather than being thrown away.
		if errors.Is(err, errUnusable) {
			runtime.UnlockOSThread()
		}
		done <- threadResult{nil, err}
		return
	}
	ep, err := viaStd(o, spelling, false)
	done <- threadResult{ep, err}
}
