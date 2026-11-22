//go:build freebsd || netbsd || openbsd

package sockat_test

import (
	"fmt"

	"golang.org/x/sys/unix"
)

// kqueueReadable asks a fresh kqueue (level-triggered) whether fd is
// readable now, the way the runtime's poller would learn it.
func kqueueReadable(fd int) string {
	kq, err := unix.Kqueue()
	if err != nil {
		return fmt.Sprintf("kqueue: %v", err)
	}
	defer unix.Close(kq)
	var ch [1]unix.Kevent_t
	unix.SetKevent(&ch[0], fd, unix.EVFILT_READ, unix.EV_ADD)
	var ev [1]unix.Kevent_t
	ts := unix.Timespec{}
	n, err := unix.Kevent(kq, ch[:], ev[:], &ts)
	if err != nil {
		return fmt.Sprintf("kevent: %v", err)
	}
	if n == 0 {
		return "kevent none"
	}
	return fmt.Sprintf("kevent n=%d flags=%#x data=%d fflags=%#x", n, ev[0].Flags, ev[0].Data, ev[0].Fflags)
}
