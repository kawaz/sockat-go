package sockat_test

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/kawaz/sockat-go"
)

// The Example functions of this package run the sample code of the user
// manual (docs/manual/) and the reference (docs/REFERENCE.md): the code
// there is a part of an Example function, word for word. What an Example
// prints (its Output) is what the sample is for, so the Example fails when
// the sample stops doing it.

// longDir makes a directory whose path is too long for sun_path, inside a
// temporary directory that the caller removes.
func longDir() (tmp, long string) {
	tmp, err := os.MkdirTemp("", "sgt-example")
	if err != nil {
		log.Fatal(err)
	}
	long = filepath.Join(tmp, strings.Repeat("d", 60), strings.Repeat("e", 60))
	if err := os.MkdirAll(long, 0o700); err != nil {
		log.Fatal(err)
	}
	return tmp, long
}

// echoServer listens at path and writes back each line it reads, for the
// Examples that show the connecting side. Close the listener to stop it.
func echoServer(path string) io.Closer {
	l, err := sockat.Listen(path)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				io.Copy(c, c)
			}()
		}
	}()
	return l
}

// readLine reads one line from r, without the newline.
func readLine(r io.Reader) string {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil {
		log.Fatal(err)
	}
	return strings.TrimSuffix(line, "\n")
}

// Branch on Bound.Spelling, not on the route's name, to know what the
// spelling the kernel keeps (the listener's Addr()) is good for: a Linked
// spelling names the socket file until Until says, a Dangling one names
// nothing. The long path here is bound through link-dir, whose spelling is
// Linked and removes the file when unlinked.
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

// DialEx reports the route taken and the routes skipped before it, for a log;
// a failure is a *RouteError with the same report. The route names are for
// diagnostics: do not branch on them. A long path has a route other than the
// standard one's, so Route is not empty.
func ExampleDialEx() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")
	defer echoServer(path).Close()

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

// sockat makes a relative path absolute before the bind, so the spelling the
// kernel keeps does not change with the cwd; the relative string your code
// keeps does. To keep an absolute path yourself, put the cwd in front as it
// is, not through filepath.Abs: that cleans the path ("a/sym/.." becomes
// "a"), while the kernel follows sym before "..", so a cleaned path can name
// another directory. The spelling of such a path is not CwdRelative.
func ExampleListenEx_absolutePath() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock") // may be relative in your code

	abs := path
	if !filepath.IsAbs(path) {
		wd, err := os.Getwd()
		if err != nil {
			log.Fatal(err)
		}
		abs = wd + "/" + path
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
