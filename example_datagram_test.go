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

// ListenUnixgram binds a datagram socket at a path of any length, and
// DialUnixgram connects a datagram socket that is not bound itself (as
// net.DialUnix("unixgram", nil, ...) does). What the client writes arrives
// at the server.
func ExampleDialUnixgram() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")

	srv, err := sockat.ListenUnixgram(path) // *net.UnixConn
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()

	c, err := sockat.DialUnixgram(path) // *net.UnixConn, not bound
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte("ping")); err != nil {
		log.Fatal(err)
	}

	buf := make([]byte, 16)
	n, _, err := srv.ReadFromUnix(buf)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(buf[:n]))
	// Output: ping
}

// A datagram socket bound at a long path sends to another long path through
// ResolveForConnect's spelling. The receiver sees the sender's bind spelling
// as the address, and a reply to it arrives while that spelling is Linked.
func ExampleListenUnixgram() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	serverPath := filepath.Join(dir, "server.sock")
	clientPath := filepath.Join(dir, "client.sock")
	srv, err := sockat.ListenUnixgram(serverPath)
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()

	cl, err := sockat.ListenUnixgram(clientPath)
	if err != nil {
		log.Fatal(err)
	}
	defer cl.Close()
	to, err := sockat.ResolveForConnect(serverPath, sockat.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer to.Cleanup()
	if _, err := cl.WriteToUnix([]byte("ping"), &net.UnixAddr{Name: to.Path, Net: "unixgram"}); err != nil {
		log.Fatal(err)
	}

	buf := make([]byte, 16)
	_, from, err := srv.ReadFromUnix(buf)
	if err != nil {
		log.Fatal(err)
	}
	if _, err := srv.WriteToUnix([]byte("pong"), from); err != nil {
		log.Fatal(err)
	}

	n, _, err := cl.ReadFromUnix(buf)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println(string(buf[:n]))
	// Output: pong
}

// The socket of ListenUnixgram, like the net package's, leaves its file when
// closed. ListenUnixgramEx gives Bound.File to remove it, before closing the
// socket. The file is gone after the function returns.
func ExampleListenUnixgramEx() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")

	func() {
		srv, b, err := sockat.ListenUnixgramEx(path, sockat.Options{})
		if err != nil {
			log.Fatal(err)
		}
		defer srv.Close()
		defer b.File.Remove() // runs before srv.Close
	}()

	_, err := os.Stat(path)
	fmt.Println("removed:", errors.Is(err, fs.ErrNotExist))
	// Output: removed: true
}
