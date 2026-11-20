package sockat_test

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
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
