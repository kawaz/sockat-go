//go:build linux || android || darwin || ios || freebsd || netbsd

package sockat_test

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"

	"github.com/kawaz/sockat-go"
)

// The examples here listen on a long path without the last resort, so they
// run only where Listen has a route for one (OpenBSD and illumos have none:
// there a long path is ENAMETOOLONG unless ListenEx is given UseLastResort,
// as in ExampleListenEx_lastResort).

// The listener of a long path does not remove the socket file on Close, and
// its Addr is not the path passed (SPEC "respelled"): remove the file with
// discard, not by the spelling Addr reports. NoLastResort tries the routes
// Listen tries, and no more.
func ExampleListenEx() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")

	func() {
		l, discard, _, err := sockat.ListenEx(path, sockat.NoLastResort)
		if err != nil {
			log.Fatal(err)
		}
		defer l.Close()
		defer discard() // removes the socket file at path (does not close l)
	}()

	_, err := os.Stat(path)
	fmt.Println("removed:", errors.Is(err, fs.ErrNotExist))
	// Output: removed: true
}

// DialEx reports the route taken and the routes skipped before it, for a log;
// a connect gives nothing up. The route names are for diagnostics: branch on
// the losses, not on them.
func ExampleDialEx() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")
	l, discard, _, err := sockat.ListenEx(path, sockat.NoLastResort)
	if err != nil {
		log.Fatal(err)
	}
	defer l.Close()
	defer discard()

	c, res, err := sockat.DialEx(path, sockat.NoLastResort)
	if err != nil {
		var re *sockat.RouteError
		if errors.As(err, &re) {
			log.Printf("dial %s: route %q after %v", path, re.Route, re.Skipped)
		}
		log.Fatal(err)
	}
	defer c.Close()
	fmt.Println("losses:", len(res.Losses))
	// Output: losses: 0
}
