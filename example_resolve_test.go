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

// ResolveForBind spells a long path for the net package (or another library
// that takes a path) to bind: bind f.Path, then take the SocketFile (before
// closing the listener) to remove the socket file later. The file is gone
// after the function returns.
func ExampleResolveForBind() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")

	func() {
		f, err := sockat.ResolveForBind(path, sockat.Options{})
		if err != nil {
			log.Fatal(err) // ENAMETOOLONG where only Listen reaches
		}
		l, err := net.ListenUnix("unix", &net.UnixAddr{Name: f.Path, Net: "unix"})
		if err != nil {
			f.Cleanup()
			log.Fatal(err)
		}
		l.SetUnlinkOnClose(false) // the file is SocketFile's to remove
		defer l.Close()
		sf, err := f.SocketFile() // after the bind, before l.Close
		if err != nil {
			log.Fatal(err)
		}
		defer sf.Remove() // removes the file, then cleans up f's symlink
	}()

	_, err := os.Stat(path)
	fmt.Println("removed:", errors.Is(err, fs.ErrNotExist))
	// Output: removed: true
}

// ResolveForConnect spells a long path once for as many connects as you
// like; Cleanup removes the symlink the spelling goes through. Both connects
// here reach the listener.
func ExampleResolveForConnect() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")
	defer echoServer(path).Close()

	f, err := sockat.ResolveForConnect(path, sockat.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer f.Cleanup()
	for range 2 {
		c, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: f.Path, Net: "unix"})
		if err != nil {
			log.Fatal(err)
		}
		c.Close()
	}

	fmt.Println("connected twice")
	// Output: connected twice
}
