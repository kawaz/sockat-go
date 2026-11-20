package sockat

import (
	"os"
	"testing"

	"golang.org/x/sys/unix"
)

// TestMain runs the tests, then takes away what this test process made
// through the public API (defaultWorld) in the real places: its D and the E
// in them, and a B of a default name (/tmp/sockat-<euid>,
// $XDG_RUNTIME_DIR/sockat, $TMPDIR/sockat-<euid>) that was not there when
// the process started, once it is empty. So running the tests leaves no
// directory or symlink behind in /tmp, $XDG_RUNTIME_DIR or $TMPDIR. Every
// child process of the tests runs this too.
func TestMain(m *testing.M) {
	var made []string
	for _, b := range defaultBNames() {
		if !exists(b) {
			made = append(made, b)
		}
	}
	code := m.Run()
	takeAway(defaultWorld)
	for _, b := range made {
		// Only an empty B goes: another process may have put its D in it.
		unix.Rmdir(b)
	}
	os.Exit(code)
}

// defaultBNames are the paths of the B of a default name in the places 1 to
// 3 of the process's environment.
func defaultBNames() []string {
	w := newWorld(envConfig())
	var out []string
	for _, p := range w.autoPlaces() {
		out = append(out, joinUnder(p.path, bName(p.kind)))
	}
	return out
}

// takeAway removes the D w holds (with the symlinks in it) and closes the fds
// of its B and D.
func takeAway(w *world) {
	for _, b := range w.bases() {
		if b.d != nil {
			dpath := b.real + "/" + b.d.name
			if es, err := os.ReadDir(dpath); err == nil {
				for _, e := range es {
					if e.Type()&os.ModeSymlink != 0 {
						unix.Unlinkat(b.d.fd, e.Name(), 0)
					}
				}
			}
			unix.Unlinkat(b.fd, b.d.name, unix.AT_REMOVEDIR)
			unix.Close(b.d.fd)
		}
		unix.Close(b.fd)
	}
}
