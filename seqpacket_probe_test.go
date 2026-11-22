//go:build linux || freebsd || netbsd || openbsd || illumos

package sockat_test

import (
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/kawaz/sockat-go"
	"golang.org/x/sys/unix"
)

// probeWait bounds each accept and read of the probe so a stall fails the
// test with a diagnosis instead of hanging until go test's own timeout.
const probeWait = 5 * time.Second

// probeAddrs spells the listening and the connecting address of one stage.
// It returns the paths to bind and to dial, and a cleanup.
type probeAddrs func(t *testing.T, dir string) (bind, dial string, cleanup func())

// TestSeqpacketProbe sends two messages over a connection made at each stage
// and checks they come back, for each order of the client's writes and the
// server's accept, on "unixpacket" and (as the control) "unix".
func TestSeqpacketProbe(t *testing.T) {
	stages := []struct {
		name  string
		addrs probeAddrs
	}{
		{"std-short", func(t *testing.T, dir string) (string, string, func()) {
			p := filepath.Join(dir, "s.sock")
			return p, p, func() {}
		}},
		{"std-symlink", func(t *testing.T, dir string) (string, string, func()) {
			p := filepath.Join(dir, "s.sock")
			l := filepath.Join(dir, "l.sock")
			if err := os.Symlink(p, l); err != nil {
				t.Fatal(err)
			}
			return p, l, func() {}
		}},
		{"resolve-short", resolveAddrs(func(dir string) string { return filepath.Join(dir, "s.sock") })},
		{"resolve-long", resolveAddrs(func(dir string) string {
			long := filepath.Join(dir, strings.Repeat("d", 60), strings.Repeat("e", 60))
			if err := os.MkdirAll(long, 0o700); err != nil {
				panic(err)
			}
			return filepath.Join(long, "server.sock")
		})},
	}
	for _, network := range []string{"unixpacket", "unix"} {
		for _, st := range stages {
			for _, order := range []string{"write-then-accept", "accept-then-write", "racy"} {
				t.Run(network+"/"+st.name+"/"+order, func(t *testing.T) {
					for i := range 10 {
						dir, err := os.MkdirTemp("/tmp", "sgt-probe")
						if err != nil {
							t.Fatal(err)
						}
						bind, dial, cleanup := st.addrs(t, dir)
						probeExchange(t, network, bind, dial, order)
						cleanup()
						os.RemoveAll(dir)
						if t.Failed() {
							t.Fatalf("failed at round %d", i)
						}
					}
				})
			}
		}
	}
}

// resolveAddrs spells the path made by mk with ResolveForBind and
// ResolveForConnect.
func resolveAddrs(mk func(dir string) string) probeAddrs {
	return func(t *testing.T, dir string) (string, string, func()) {
		path := mk(dir)
		fb, err := sockat.ResolveForBind(path, sockat.Options{})
		if err != nil {
			t.Fatal(err)
		}
		fc, err := sockat.ResolveForConnect(path, sockat.Options{})
		if err != nil {
			fb.Cleanup()
			t.Fatal(err)
		}
		t.Logf("path %s\n bind %s (%v route %s base %s)\n dial %s (%v route %s)", path, fb.Path, fb.Spelling.Kind, fb.Route, fb.Base, fc.Path, fc.Spelling.Kind, fc.Route)
		return fb.Path, fc.Path, func() {
			fc.Cleanup()
			os.Remove(path)
			fb.Cleanup()
		}
	}
}

// probeExchange listens on bind, dials dial and sends "ab" and "cde", which
// the server echoes, in the given order of the client's writes and the
// server's accept.
func probeExchange(t *testing.T, network, bind, dial, order string) {
	writeFirst := order == "write-then-accept"
	l, err := net.ListenUnix(network, &net.UnixAddr{Name: bind, Net: network})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false)
	defer l.Close()
	diag := func(what string) {
		t.Logf("--- %s", what)
		t.Logf("listener Addr %v", l.Addr())
		probeRaw(t, "listener", l)
		probeFile(t, "bind", bind)
		probeFile(t, "dial", dial)
		if place := os.Getenv("SOCKAT_TEST_PLACE"); place != "" {
			probeTree(t, place)
		}
	}

	written := make(chan struct{})
	accepted := make(chan struct{})
	served := make(chan error, 1)
	go func() {
		if writeFirst {
			<-written
		}
		l.SetDeadline(time.Now().Add(probeWait))
		c, err := l.AcceptUnix()
		close(accepted)
		if err != nil {
			served <- fmt.Errorf("accept: %w", err)
			return
		}
		defer c.Close()
		t.Logf("server conn local %v remote %v", c.LocalAddr(), c.RemoteAddr())
		buf := make([]byte, 1024)
		for total := 0; total < len("abcde"); {
			c.SetReadDeadline(time.Now().Add(probeWait))
			n, err := c.Read(buf)
			total += n
			if err != nil {
				probeRaw(t, "server conn after read error", c)
				served <- fmt.Errorf("server read: %w", err)
				return
			}
			if _, err := c.Write(buf[:n]); err != nil {
				served <- fmt.Errorf("server write: %w", err)
				return
			}
		}
		served <- nil
	}()

	c, err := net.DialUnix(network, nil, &net.UnixAddr{Name: dial, Net: network})
	if err != nil {
		close(written)
		diag("dial failed")
		t.Fatal(err)
	}
	defer c.Close()
	probeRaw(t, "client conn after dial", c)
	if order == "accept-then-write" {
		<-accepted
	}
	for _, m := range []string{"ab", "cde"} {
		if _, err := c.Write([]byte(m)); err != nil {
			close(written)
			t.Fatalf("client write: %v", err)
		}
	}
	close(written)
	var got []string
	buf := make([]byte, 1024)
	for len(strings.Join(got, "")) < len("abcde") {
		c.SetReadDeadline(time.Now().Add(2 * probeWait))
		n, err := c.Read(buf)
		if err != nil {
			probeRaw(t, "client conn after read error", c)
			if serr := <-served; serr != nil {
				t.Errorf("server: %v", serr)
			}
			diag("client read failed")
			t.Fatalf("client read: %v (got %q)", err, got)
		}
		got = append(got, string(buf[:n]))
	}
	if serr := <-served; serr != nil {
		t.Errorf("server: %v", serr)
	}
	want := []string{"ab", "cde"}
	if network == "unix" {
		// A stream may merge the two writes.
		if strings.Join(got, "") != "abcde" {
			t.Errorf("got %q", got)
		}
		return
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// probeRaw logs what the kernel says of the socket: its type, its own and
// its peer's address, whether poll and kqueue see it readable, and what a
// non-blocking peek finds.
func probeRaw(t *testing.T, what string, s syscall.Conn) {
	rc, err := s.SyscallConn()
	if err != nil {
		t.Logf("%s: %v", what, err)
		return
	}
	rc.Control(func(fd uintptr) {
		f := int(fd)
		typ, terr := unix.GetsockoptInt(f, unix.SOL_SOCKET, unix.SO_TYPE)
		sn, serr := unix.Getsockname(f)
		pn, perr := unix.Getpeername(f)
		pfd := []unix.PollFd{{Fd: int32(f), Events: unix.POLLIN}}
		pn2, pollErr := unix.Poll(pfd, 0)
		kq := kqueueReadable(f)
		buf := make([]byte, 64)
		n, _, rerr := unix.Recvfrom(f, buf, unix.MSG_PEEK|unix.MSG_DONTWAIT)
		peek := ""
		if n > 0 {
			peek = string(buf[:n])
		}
		t.Logf("%s fd %d: SO_TYPE %d (%v) name %s (%v) peer %s (%v) poll n=%d revents=%#x (%v) %s peek n=%d %q (%v)",
			what, f, typ, terr, sockName(sn), serr, sockName(pn), perr, pn2, pfd[0].Revents, pollErr, kq, n, peek, rerr)
	})
}

func sockName(sa unix.Sockaddr) string {
	if u, ok := sa.(*unix.SockaddrUnix); ok {
		return fmt.Sprintf("%q", u.Name)
	}
	return fmt.Sprintf("%v", sa)
}

// probeFile logs what path is: the file it names and, for a symlink, what
// it points to.
func probeFile(t *testing.T, what, path string) {
	fi, err := os.Lstat(path)
	if err != nil {
		t.Logf("%s %s: %v", what, path, err)
		return
	}
	s := fmt.Sprintf("%s %s: %v", what, path, fi.Mode())
	if fi.Mode()&fs.ModeSymlink != 0 {
		to, err := os.Readlink(path)
		s += fmt.Sprintf(" -> %s (%v)", to, err)
		if st, err := os.Stat(path); err == nil {
			s += fmt.Sprintf(" target %v", st.Mode())
		} else {
			s += fmt.Sprintf(" target %v", err)
		}
	}
	t.Log(s)
}

// probeTree logs every entry under root.
func probeTree(t *testing.T, root string) {
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			t.Logf("tree %s: %v", p, err)
			return nil
		}
		s := fmt.Sprintf("tree %s %v", p, d.Type())
		if d.Type()&fs.ModeSymlink != 0 {
			to, err := os.Readlink(p)
			if err != nil {
				s += fmt.Sprintf(" (%v)", err)
			}
			s += " -> " + to
		}
		t.Log(s)
		return nil
	})
}
