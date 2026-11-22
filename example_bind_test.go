package sockat_test

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"path/filepath"

	"github.com/kawaz/sockat-go"
)

// Listen takes a path of any length where net.Listen("unix", path) would,
// and returns the net package's own *net.UnixListener: accept on it as
// usual. The client here gets back what it sent, so the listener accepts at
// the long path.
func ExampleListen() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")

	l, err := sockat.Listen(path) // *net.UnixListener
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return // l is closed
			}
			go func() {
				defer c.Close()
				io.Copy(c, c)
			}()
		}
	}()

	c, err := sockat.Dial(path)
	if err != nil {
		log.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintln(c, "ping")
	fmt.Println(readLine(c))
	// Output: ping
}

// The listener of Listen is a net.Listener, so http.Serve (and any server
// that takes a net.Listener) serves on it. The client here gets the
// handler's answer through the long path.
func ExampleListen_http() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")

	l, err := sockat.Listen(path)
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close() // http.Serve returns
	go http.Serve(l, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "hello")
	}))

	fmt.Println(httpGet(path))
	// Output: hello
}

// The listener of ListenEx does not remove the socket file on Close: remove
// it with Bound.File's Remove, before closing the listener (Remove does not
// close it). Remove also cleans up the symlink the spelling goes through.
// The file is gone after the function returns.
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

// Options.Base puts the directory sockat makes the short spelling in (B)
// where the application decides, instead of /tmp, $XDG_RUNTIME_DIR or
// $TMPDIR. The application sweeps it with SweepBase, for instance when it
// starts. sockat makes B at base, and the spelling goes through it.
func ExampleOptions_base() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")
	app, err := os.MkdirTemp("/tmp", "sgt-example")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(app)
	base := filepath.Join(app, "sockat")

	if err := sockat.SweepBase(base); err != nil {
		log.Print(err) // what could not be read or removed
	}
	l, b, err := sockat.ListenEx(path, sockat.Options{Base: base})
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	defer b.File.Remove()

	_, err = os.Stat(base)
	fmt.Println("base made:", err == nil, b.Spelling.Kind)
	// Output: base made: true Linked
}

// A peer of another uid can follow the spelling only when Options asks for
// it with UsableCrossUID; Spelling.UsableCrossUID tells whether it was met
// (where it cannot be, the bind takes a route whose spelling is Dangling).
// The places of these tests let others search them, so it is met here.
func ExampleOptions_usableCrossUID() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")

	l, b, err := sockat.ListenEx(path, sockat.Options{UsableCrossUID: true})
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	defer b.File.Remove()
	if !b.Spelling.UsableCrossUID {
		log.Print("another uid cannot follow l.Addr(): give it path")
	}

	fmt.Println("usable by another uid:", b.Spelling.UsableCrossUID)
	// Output: usable by another uid: true
}

// Sweep removes what processes that have ended left in this user's
// directories of /tmp, $XDG_RUNTIME_DIR and $TMPDIR. It is never called for
// you: call it once where the same program runs again and again (tests, a
// command run often). It leaves what live processes use.
func ExampleSweep() {
	if err := sockat.Sweep(); err != nil {
		log.Print(err) // what could not be read or removed
	}
	// Output:
}
