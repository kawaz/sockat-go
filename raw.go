//go:build darwin || ios || freebsd

package sockat

import (
	"fmt"
	"net"
	"os"

	"golang.org/x/sys/unix"
)

// rawOp binds (and listens) or connects a fresh socket with call, which
// makes the bind / connect system call with an address built by hand (one the
// standard library cannot build: macOS's widened sockaddr, FreeBSD's
// bindat / connectat), and hands the socket over to the net package.
func rawOp(o op, call func(sock int) error) (endpoint, error) {
	sock, err := newSocket()
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(sock), "")
	defer f.Close()
	if err := call(sock); err != nil {
		return nil, err
	}
	if o == opListen {
		if err := unix.Listen(sock, listenBacklog()); err != nil {
			return nil, syscallError("listen", err)
		}
	}
	return adopt(o, f)
}

// adopt hands a bound and listening (opListen) or connected (opDial) socket
// over to the net package, which takes a duplicate of it (close-on-exec,
// nonblocking, registered with the runtime's poller). The caller still closes
// f.
//
// The listener does not remove a socket file on Close: its address is the
// spelling the kernel was given, not the caller's path.
func adopt(o op, f *os.File) (endpoint, error) {
	switch o {
	case opListen:
		l, err := net.FileListener(f)
		if err != nil {
			return nil, err
		}
		ul, ok := l.(*net.UnixListener)
		if !ok {
			l.Close()
			return nil, fmt.Errorf("sockat: unexpected listener type %T", l)
		}
		return ul, nil
	default:
		c, err := net.FileConn(f)
		if err != nil {
			return nil, err
		}
		uc, ok := c.(*net.UnixConn)
		if !ok {
			c.Close()
			return nil, fmt.Errorf("sockat: unexpected connection type %T", c)
		}
		return uc, nil
	}
}

// somaxconn reads the listen backlog the net package uses on the BSDs: the
// sysctl name, capped to what fits the kernel's 16-bit field, or SOMAXCONN.
func somaxconn(name string) int {
	n, err := unix.SysctlUint32(name)
	if n == 0 || err != nil {
		return unix.SOMAXCONN
	}
	if n > 1<<16-1 {
		n = 1<<16 - 1
	}
	return int(n)
}
