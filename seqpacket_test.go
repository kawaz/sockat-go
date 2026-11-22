//go:build linux || freebsd || netbsd || openbsd || illumos

// The systems where the net package makes "unixpacket" sockets (net's
// platform_test.go leaves out darwin, ios and android, among others), and
// that the CI of this package runs on.

package sockat_test

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kawaz/sockat-go"
	"golang.org/x/sys/unix"
)

// seqpacketWait bounds each accept, read and write of the SEQPACKET test, so
// that a lost record fails the test at once rather than at go test's timeout.
const seqpacketWait = 5 * time.Second

// sendRecord sends p as one record. MSG_EOR marks the end of the record:
// FreeBSD sets a record boundary only there, and write(2) (UnixConn.Write)
// cannot set it (FreeBSD socket(2), "SEQUENCED PACKET SOCKET TYPE"), so two
// Writes reach the reader as one record. The other systems keep each send
// as a record either way.
func sendRecord(c *net.UnixConn, p []byte) error {
	rc, err := c.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := rc.Write(func(fd uintptr) bool {
		serr = unix.Sendmsg(int(fd), p, nil, nil, unix.MSG_EOR)
		return !errors.Is(serr, unix.EAGAIN)
	}); err != nil {
		return err
	}
	return serr
}

// A "unixpacket" listener bound at the ResolveForBind spelling of a path
// takes the connection made at the ResolveForConnect spelling of the same
// path, and each record sent on it arrives whole and alone (two records
// come back as two reads), both for a short path (spelled as it is) and a
// long one (spelled through B: link-dir and link-file on FreeBSD, /proc on
// Linux). The SEQPACKET page of the manual tells users to hand these
// spellings to the net package's "unixpacket" sockets, so the spellings
// must reach the same listener as the path would; the standard library
// alone at a short path is the control that tells the system's own
// behaviour apart from the spellings'. Every wait has a deadline, so a
// record that never arrives fails here instead of stopping go test.
func TestSeqpacketThroughResolvedSpellingsKeepsRecords(t *testing.T) {
	cases := []struct {
		name  string
		path  func(dir string) string
		spell bool
	}{
		{"std-short", func(dir string) string { return filepath.Join(dir, "s.sock") }, false},
		{"resolve-short", func(dir string) string { return filepath.Join(dir, "s.sock") }, true},
		{"resolve-long", func(dir string) string {
			return filepath.Join(dir, strings.Repeat("d", 60), strings.Repeat("e", 60), "server.sock")
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir, err := os.MkdirTemp("/tmp", "sgt-seqpacket")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			path := tc.path(dir)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			bind, dial := path, path
			if tc.spell {
				fb, err := sockat.ResolveForBind(path, sockat.Options{})
				if err != nil {
					t.Fatal(err)
				}
				defer fb.Cleanup()
				fc, err := sockat.ResolveForConnect(path, sockat.Options{})
				if err != nil {
					t.Fatal(err)
				}
				defer fc.Cleanup()
				bind, dial = fb.Path, fc.Path
				t.Logf("bind %s (%v), dial %s (%v)", bind, fb.Spelling.Kind, dial, fc.Spelling.Kind)
			}
			seqpacketEcho(t, bind, dial)
		})
	}
}

// seqpacketEcho listens at bind, connects at dial, sends the records "ab"
// and "cde" and checks they come back one per read from a server that
// echoes each record it reads.
func seqpacketEcho(t *testing.T, bind, dial string) {
	t.Helper()
	l, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: bind, Net: "unixpacket"})
	if err != nil {
		t.Fatal(err)
	}
	l.SetUnlinkOnClose(false) // the test's directory goes with the file
	defer l.Close()
	served := make(chan error, 1)
	go func() {
		served <- func() error {
			l.SetDeadline(time.Now().Add(seqpacketWait))
			c, err := l.AcceptUnix()
			if err != nil {
				return err
			}
			defer c.Close()
			buf := make([]byte, 1024)
			for range 2 {
				c.SetDeadline(time.Now().Add(seqpacketWait))
				n, err := c.Read(buf)
				if err != nil {
					return err
				}
				if err := sendRecord(c, buf[:n]); err != nil {
					return err
				}
			}
			return nil
		}()
	}()

	c, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: dial, Net: "unixpacket"})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * seqpacketWait))
	want := []string{"ab", "cde"}
	for _, m := range want {
		if err := sendRecord(c, []byte(m)); err != nil {
			t.Fatalf("send %q: %v", m, err)
		}
	}
	buf := make([]byte, 1024)
	var got []string
	for range want {
		n, err := c.Read(buf)
		if err != nil {
			t.Errorf("read after %q: %v", got, err)
			break
		}
		got = append(got, string(buf[:n]))
	}
	if err := <-served; err != nil {
		t.Errorf("server: %v", err)
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("records %q, want %q", got, want)
	}
}
