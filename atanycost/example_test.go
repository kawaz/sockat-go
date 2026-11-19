package atanycost_test

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/kawaz/sockat-go/atanycost"
)

// longDir makes a directory whose path is too long for sun_path, inside a
// temporary directory that the caller removes.
func longDir() (tmp, long string) {
	tmp, err := os.MkdirTemp("", "atanycost-example")
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
// the socket file. Keep the path passed and remove the file by it.
func ExampleListen_respelled() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")

	func() {
		l, res, err := atanycost.Listen(path)
		if err != nil {
			log.Fatal(err)
		}
		defer os.Remove(path) // the path passed, not l.Addr().String()
		defer l.Close()
		fmt.Println("respelled:", res.Has(atanycost.Respelled))
	}()

	_, err := os.Stat(path)
	fmt.Println("removed:", errors.Is(err, fs.ErrNotExist))
	// Output:
	// respelled: true
	// removed: true
}

// CwdRelative (SPEC "cwd-relative"): a relative path is kept relative to the
// cwd by the kernel. Make it absolute before the Listen, and remove the file
// by the absolute path.
func ExampleListen_cwdRelative() {
	tmp, err := os.MkdirTemp("", "atanycost-example")
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
		l, res, err := atanycost.Listen(abs)
		if err != nil {
			log.Fatal(err)
		}
		defer os.Remove(abs)
		defer l.Close()
		fmt.Println("cwd-relative:", res.Has(atanycost.CwdRelative))
	}()
	// Output: cwd-relative: false
}

// RecreatableSpelling (SPEC "recreatable-spelling"): the symlink went
// through a place others can write to. Provide a place only the owner can
// write to as XDG_RUNTIME_DIR, which is tried before the shared ones, and
// remove the file by the path passed, not by the spelling Addr reports.
func ExampleListen_recreatableSpelling() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")
	func() {
		l, res, err := atanycost.Listen(path)
		if err != nil {
			log.Fatal(err)
		}
		defer os.Remove(path) // the path passed, not l.Addr().String()
		defer l.Close()
		if res.Has(atanycost.RecreatableSpelling) {
			log.Printf("%s: bound through a shared place", path)
		}
	}()
	_, err := os.Stat(path)
	fmt.Println("removed:", os.IsNotExist(err))
	// Output: removed: true
}
