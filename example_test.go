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

// Respelled (SPEC "respelled"): Addr is not the path passed, and Close leaves
// the socket file. Remove the file with discard (or by the path passed), not
// by the spelling Addr reports.
func ExampleListenEx_respelled() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")

	func() {
		l, discard, res, err := sockat.ListenEx(path, sockat.UseLastResort)
		if err != nil {
			log.Fatal(err)
		}
		defer l.Close()
		defer discard() // not os.Remove(l.Addr().String())
		fmt.Println("respelled:", res.Has(sockat.Respelled))
	}()

	_, err := os.Stat(path)
	fmt.Println("removed:", errors.Is(err, fs.ErrNotExist))
	// Output:
	// respelled: true
	// removed: true
}

// CwdRelative (SPEC "cwd-relative"): a relative path is kept relative to the
// cwd by the kernel. Make it absolute before the ListenEx.
func ExampleListenEx_cwdRelative() {
	tmp, err := os.MkdirTemp("", "sockat-example")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(tmp)
	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	path, err := filepath.Rel(cwd, filepath.Join(tmp, "server.sock")) // a relative path
	if err != nil {
		log.Fatal(err)
	}

	func() {
		abs, err := filepath.Abs(path)
		if err != nil {
			log.Fatal(err)
		}
		l, discard, res, err := sockat.ListenEx(abs, sockat.UseLastResort)
		if err != nil {
			log.Fatal(err)
		}
		defer l.Close()
		defer discard()
		fmt.Println("cwd-relative:", res.Has(sockat.CwdRelative))
	}()
	// Output: cwd-relative: false
}

// RecreatableSpelling (SPEC "recreatable-spelling"): the last resort went
// through a place others can write to. Provide a place only the owner can
// write to as XDG_RUNTIME_DIR, which is tried before the shared ones, and
// remove the file with discard, not by the spelling Addr reports.
func ExampleListenEx_recreatableSpelling() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")
	func() {
		l, discard, res, err := sockat.ListenEx(path, sockat.UseLastResort)
		if err != nil {
			log.Fatal(err)
		}
		defer l.Close()
		defer discard() // not os.Remove(l.Addr().String())
		if res.Has(sockat.RecreatableSpelling) {
			log.Printf("%s: bound through a shared place", path)
		}
	}()
	_, err := os.Stat(path)
	fmt.Println("removed:", errors.Is(err, fs.ErrNotExist))
	// Output: removed: true
}

// Where Listen has no route for a long path (OpenBSD and illumos have none;
// elsewhere, when no means is usable), Listen and ListenEx with NoLastResort
// are ENAMETOOLONG. UseLastResort then binds through a symlink to the socket's
// directory in a place (SPEC "symlink-dir"), losing what Losses lists: take it
// only when that is acceptable, and remove the file with discard.
func ExampleListenEx_lastResort() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")

	func() {
		l, discard, res, err := sockat.ListenEx(path, sockat.UseLastResort)
		if err != nil {
			log.Fatal(err)
		}
		defer l.Close()
		defer discard()
		fmt.Println("respelled:", res.Has(sockat.Respelled))
	}()

	_, err := os.Stat(path)
	fmt.Println("removed:", errors.Is(err, fs.ErrNotExist))
	// Output:
	// respelled: true
	// removed: true
}
