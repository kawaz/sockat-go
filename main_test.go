package sockat

import (
	"fmt"
	"os"
	"os/exec"
	"testing"

	"golang.org/x/sys/unix"
)

// placeEnv carries the test place of the test process to its child
// processes, so they share one B as the processes of a user do.
const placeEnv = "SOCKAT_TEST_PLACE"

// TestMain points the public API (defaultWorld) at a place of the tests' own
// instead of the real /tmp, $XDG_RUNTIME_DIR and $TMPDIR, runs the tests, and
// takes that place away with everything in it. The tests then never use nor
// remove the B other processes of the user share there (/tmp/sockat-<euid>),
// in which the tests of another implementation may be running at the same
// time. The place is a fresh directory under /tmp of mode 1777, as /tmp is,
// so a child process dropped to another user makes its B there too. Every
// child process of the tests uses its parent's place (placeEnv) and leaves it
// to the parent.
func TestMain(m *testing.M) {
	if name := os.Getenv(mainChildEnv); name != "" {
		f, ok := mainChildren[name]
		if !ok {
			fmt.Fprintln(os.Stderr, "no main child", name)
			os.Exit(1)
		}
		f()
		os.Exit(0)
	}
	place, own := os.Getenv(placeEnv), false
	if place == "" {
		d, err := os.MkdirTemp("/tmp", "sgt-t")
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		if err := unix.Chmod(d, 0o1777); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		place, own = d, true
		os.Setenv(placeEnv, place)
	}
	defaultWorld = newWorld(placeConfig(place))
	code := m.Run()
	takeAway(defaultWorld)
	if own {
		os.RemoveAll(place)
	}
	os.Exit(code)
}

// mainChildEnv names the function of mainChildren a child process of this
// binary runs (runMainChild) instead of the tests: on the main goroutine,
// which starts on the runtime's main thread, as a program's main does. For
// what depends on that thread, which a test goroutine cannot choose.
const mainChildEnv = "SOCKAT_TEST_MAIN_CHILD"

// mainChildren are the functions runMainChild may run. They report with
// childReport and end the process with a status other than 0 on a failure.
var mainChildren = map[string]func(){}

// runMainChild runs mainChildren[name] in a child process of this test binary
// with env added and returns its output, failing the test when it fails.
func runMainChild(t *testing.T, name string, env ...string) string {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^$")
	cmd.Env = append(os.Environ(), mainChildEnv+"="+name)
	cmd.Env = append(cmd.Env, env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("main child %s: %v\n%s", name, err, out)
	}
	return string(out)
}

// placeConfig is the process's configuration with place as /tmp and neither
// $XDG_RUNTIME_DIR nor $TMPDIR.
func placeConfig(place string) config {
	cfg := envConfig()
	cfg.tmp = place
	cfg.getenv = func(string) string { return "" }
	return cfg
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
