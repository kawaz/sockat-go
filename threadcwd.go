//go:build darwin || ios || linux || android

package sockat

import "runtime"

// hasThreadCwd: this system has per-thread working directories.
const hasThreadCwd = true

// inThreadCwd binds (and listens) or connects through spelling, relative to
// the directory dirfd, from a thread whose own working directory is dirfd
// (threadChdir). No other thread's cwd changes, the caller's included.
//
// The work runs on a goroutine of its own, locked to its OS thread, that
// exits without unlocking: the Go runtime then terminates the thread instead
// of handing it to other goroutines, so the changed cwd dies with it. The
// runtime never starts new threads from a locked one, so none inherits the cwd
// either.
//
// The thread belongs to this process: the peer a server sees is this process,
// and no file descriptor is duplicated. errUnusable when the thread cannot
// have a cwd of its own.
func inThreadCwd(o op, dirfd int, spelling string) (endpoint, error) {
	type result struct {
		ep  endpoint
		err error
	}
	done := make(chan result, 1)
	go func() {
		// Design rationale: never UnlockOSThread. The thread's cwd is no
		// longer the process's once threadChdir succeeds, so the thread must
		// not run anything else; exiting while locked makes the runtime
		// terminate it.
		runtime.LockOSThread()
		if err := threadChdir(dirfd); err != nil {
			done <- result{nil, err}
			return
		}
		ep, err := viaStd(o, spelling, false)
		done <- result{ep, err}
	}()
	r := <-done
	return r.ep, r.err
}
