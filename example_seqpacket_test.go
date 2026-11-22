//go:build linux || freebsd || netbsd || openbsd || illumos

// The systems where the net package makes "unixpacket" sockets and tests
// them (net's platform_test.go leaves out darwin, ios and android, among
// others), and that the CI of this package runs on.

package sockat_test

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"

	"github.com/kawaz/sockat-go"
)

// sockat has no functions of its own for SEQPACKET: spell the path with
// ResolveForBind / ResolveForConnect and hand the spelling to the net
// package's "unixpacket" sockets. The listener leaves the file to the
// SocketFile (SetUnlinkOnClose(false)): unlinking the spelling would remove
// the symlink only, or leave it behind. The connecting side cleans up its
// spelling right after the connect, as the connection does not need it.
// Each message arrives whole, and the file is gone after the function
// returns.
func ExampleResolveForBind_seqpacket() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")

	func() {
		f, err := sockat.ResolveForBind(path, sockat.Options{})
		if err != nil {
			log.Fatal(err)
		}
		l, err := net.ListenUnix("unixpacket", &net.UnixAddr{Name: f.Path, Net: "unixpacket"})
		if err != nil {
			f.Cleanup()
			log.Fatal(err)
		}
		l.SetUnlinkOnClose(false) // the file is SocketFile's to remove
		defer l.Close()
		go func() {
			for {
				c, err := l.AcceptUnix()
				if err != nil {
					return // l is closed
				}
				go func() {
					defer c.Close()
					buf := make([]byte, 1024)
					for {
						n, err := c.Read(buf) // one message
						if err != nil {
							return
						}
						c.Write(buf[:n])
					}
				}()
			}
		}()

		sf, err := f.SocketFile() // after the bind, before l.Close
		if err != nil {
			log.Fatal(err)
		}
		defer sf.Remove() // removes the file, then cleans up f's symlink

		to, err := sockat.ResolveForConnect(path, sockat.Options{})
		if err != nil {
			log.Fatal(err)
		}
		c, err := net.DialUnix("unixpacket", nil, &net.UnixAddr{Name: to.Path, Net: "unixpacket"})
		to.Cleanup() // the connection does not need the spelling
		if err != nil {
			log.Fatal(err)
		}
		defer c.Close()

		buf := make([]byte, 1024)
		for _, msg := range []string{"ab", "cde"} {
			if _, err := c.Write([]byte(msg)); err != nil {
				log.Fatal(err)
			}
		}
		for range 2 {
			n, err := c.Read(buf)
			if err != nil {
				log.Fatal(err)
			}
			fmt.Println(string(buf[:n]))
		}
	}()

	_, err := os.Stat(path)
	fmt.Println("removed:", errors.Is(err, fs.ErrNotExist))
	// Output:
	// ab
	// cde
	// removed: true
}
