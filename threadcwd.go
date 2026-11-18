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
// name goes in the plain sockaddr, or on macOS (wide) in the widened one when
// it is too long for the plain one. errUnusable when the thread cannot have a
// cwd of its own.
func threadCwdRoute(o op, dir *os.File, name string, wide bool) (endpoint, error) {
	spelling := asSpelling(name)
	useWide := false
	if len(spelling) > directMax {
		if !wide || len(spelling) > wideMax {
			return nil, errNameTooLong(o)
		}
		useWide = true
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
		var r result
		if useWide {
			r.ep, r.err = wideOp(o, spelling)
		} else {
			r.ep, r.err = viaStd(o, spelling, false)
		}
		done <- r
	}()
	r := <-done
	runtime.KeepAlive(dir)
	return r.ep, r.err
}
