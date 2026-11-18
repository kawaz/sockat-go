//go:build darwin || ios || linux || android

package sockat

import (
	"os"
	"runtime"
)

// threadCwdSupported: this system has per-thread working directories.
const threadCwdSupported = true

// threadCwdRoute binds (and listens) or connects to name inside the directory
// dir from a thread whose own working directory is dir (threadChdir), so the
// file name alone is handed to the kernel. No other thread's cwd changes.
//
// The work runs on a goroutine locked to its OS thread that exits without
// unlocking: the Go runtime then terminates the thread instead of handing it
// to other goroutines, so the changed cwd dies with it. The runtime never
// starts new threads from a locked one, so none inherits the cwd either.
//
// Unlike a forked child, the thread belongs to this process: the peer a
// server sees is this process, and no file descriptor is duplicated.
//
// name goes in the sockaddr: ENAMETOOLONG when it does not fit. errUnusable
// when the thread cannot have a cwd of its own.
func threadCwdRoute(o op, dir *os.File, name string) (endpoint, error) {
	spelling := asSpelling(name)
	if len(spelling) > directMax {
		return nil, errNameTooLong(o)
	}
	type result struct {
		ep  endpoint
		err error
	}
	done := make(chan result, 1)
	dirfd := int(dir.Fd())
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
	runtime.KeepAlive(dir)
	return r.ep, r.err
}
