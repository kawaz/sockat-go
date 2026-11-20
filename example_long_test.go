package sockat_test

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/kawaz/sockat-go"
)

// longDir makes a directory whose path is too long for sun_path, inside a
// temporary directory that the caller removes.
func longDir() (tmp, long string) {
	tmp, err := os.MkdirTemp("", "sockat-example")
	if err != nil {
		log.Fatal(err)
	}
	long = filepath.Join(tmp, strings.Repeat("d", 60), strings.Repeat("e", 60))
	if err := os.MkdirAll(long, 0o700); err != nil {
		log.Fatal(err)
	}
	return tmp, long
}

// The listener of ListenEx does not remove the socket file on Close: remove
// it with Bound.File's Remove, before closing the listener (Remove does not
// close it). Remove also cleans up the symlink the spelling goes through.
func ExampleListenEx() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")

	func() {
		l, b, err := sockat.ListenEx(path, sockat.Options{})
		if err != nil {
			log.Fatal(err)
		}
		defer l.Close()
		defer b.File.Remove() // runs before l.Close
	}()

	_, err := os.Stat(path)
	fmt.Println("removed:", errors.Is(err, fs.ErrNotExist))
	// Output: removed: true
}

// A relative path is made absolute before the bind: the spelling of a
// relative path names another place once the cwd changes (CwdRelative), and
// so does the relative path itself. Remove, which recorded the file at the
// bind, and the absolute path stay right.
func ExampleListenEx_absolutePath() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock") // may be relative in your code

	abs, err := filepath.Abs(path)
	if err != nil {
		log.Fatal(err)
	}
	l, b, err := sockat.ListenEx(abs, sockat.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	defer b.File.Remove()
	fmt.Println("relative spelling:", b.Spelling.Kind == sockat.CwdRelative)
	// Output: relative spelling: false
}

// Branch on Bound.Spelling, not on the route's name, to know what the
// spelling the kernel keeps (the listener's Addr()) is good for: a Linked
// spelling names the socket file until Until says, a Dangling one names
// nothing.
func ExampleSpelling() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")
	l, b, err := sockat.ListenEx(path, sockat.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	defer b.File.Remove()

	switch sp := b.Spelling; sp.Kind {
	case sockat.Original, sockat.CwdRelative:
		fmt.Println("the path itself")
	case sockat.Linked:
		// sp.Until: how long l.Addr() can be followed.
		// sp.UsableCrossUID: a peer of another uid can follow it.
		fmt.Println("Linked; unlinking it removes the socket file:", sp.Removable)
	case sockat.Dangling:
		fmt.Println("names nothing: keep path")
	}
	// Output: Linked; unlinking it removes the socket file: true
}

// ResolveForBind spells a long path for the net package to bind: bind
// f.Path, then take the SocketFile (before closing the listener) to remove
// the socket file later.
func ExampleResolveForBind() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")

	func() {
		f, err := sockat.ResolveForBind(path, sockat.Options{})
		if err != nil {
			log.Fatal(err) // ENAMETOOLONG where only Listen's thread-cwd / at reach
		}
		l, err := net.ListenUnix("unix", &net.UnixAddr{Name: f.Path, Net: "unix"})
		if err != nil {
			f.Cleanup()
			log.Fatal(err)
		}
		l.SetUnlinkOnClose(false) // the file is SocketFile's to remove
		defer l.Close()
		sf, err := f.SocketFile()
		if err != nil {
			log.Fatal(err)
		}
		defer sf.Remove() // removes the file, then cleans up f's E
	}()

	_, err := os.Stat(path)
	fmt.Println("removed:", errors.Is(err, fs.ErrNotExist))
	// Output: removed: true
}

// ResolveForConnect spells a long path once for as many connects as you
// like; Cleanup removes the symlink the spelling goes through.
func ExampleResolveForConnect() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")
	l, err := sockat.Listen(path)
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()

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

// DialUnixgram connects a datagram socket that is not bound to a long path.
func ExampleDialUnixgram() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	srv, err := sockat.ListenUnixgram(filepath.Join(dir, "server.sock"))
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()

	c, err := sockat.DialUnixgram(filepath.Join(dir, "server.sock"))
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
// ResolveForConnect's spelling; the receiver sees the sender's bind spelling
// as the address, and a reply to it arrives while that spelling is Linked.
func ExampleListenUnixgram() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	srv, err := sockat.ListenUnixgram(filepath.Join(dir, "server.sock"))
	if err != nil {
		log.Fatal(err)
	}
	defer srv.Close()
	cl, err := sockat.ListenUnixgram(filepath.Join(dir, "client.sock"))
	if err != nil {
		log.Fatal(err)
	}
	defer cl.Close()

	to, err := sockat.ResolveForConnect(filepath.Join(dir, "server.sock"), sockat.Options{})
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

// DialEx reports the route taken and the routes skipped before it, for a log.
// The route names are for diagnostics: do not branch on them.
func ExampleDialEx() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")
	l, b, err := sockat.ListenEx(path, sockat.Options{})
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	defer b.File.Remove()

	c, rep, err := sockat.DialEx(path, sockat.Options{})
	if err != nil {
		var re *sockat.RouteError
		if errors.As(err, &re) {
			log.Printf("dial %s: route %q after %v", path, re.Route, re.Skipped)
		}
		log.Fatal(err)
	}
	defer c.Close()
	fmt.Println("connected through a route:", rep.Route != "")
	// Output: connected through a route: true
}
