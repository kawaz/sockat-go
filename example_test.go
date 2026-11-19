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

// The listener of a long path does not remove the socket file on Close, and
// its Addr is not the path passed (SPEC "respelled"): remove the file with
// discard, not by the spelling Addr reports.
func ExampleListenEx() {
	tmp, dir := longDir()
	defer os.RemoveAll(tmp)
	path := filepath.Join(dir, "server.sock")

	func() {
		l, discard, err := sockat.ListenEx(path)
		if err != nil {
			log.Fatal(err)
		}
		defer discard() // removes the socket file at path (does not close l)
		defer l.Close()
	}()

	_, err := os.Stat(path)
	fmt.Println("removed:", errors.Is(err, fs.ErrNotExist))
	// Output: removed: true
}
