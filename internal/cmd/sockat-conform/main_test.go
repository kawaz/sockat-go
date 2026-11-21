package main

import (
	"bytes"
	"encoding/json"
	"net"
	"os"
	"runtime"
	"strings"
	"syscall"
	"testing"

	"github.com/kawaz/sockat-go"
)

// The input's path has no default (the hub's docs/design/conformance.md,
// "試験用 CLI の決まり": usable_cross_uid and base may be left out, path may
// not), so an input without it, or with it null, is an input the CLI cannot
// take: exit status 2 and nothing on stdout. Taking a missing path for ""
// would bind / connect the empty path and report its invalid-argument as if
// the script had asked for it. The same holds for an op other than bind and
// connect. An input with the path (here the empty path itself) is taken and
// reported, with status 0.
func TestAnInputWithoutAPathIsRefused(t *testing.T) {
	for _, in := range []string{
		`{"op": "bind"}`,
		`{"op": "connect", "usable_cross_uid": false, "base": null}`,
		`{"op": "bind", "path": null}`,
		`{"op": "listen", "path": "s.sock"}`,
		`not json`,
	} {
		var stdout, stderr bytes.Buffer
		if code := run(strings.NewReader(in), &stdout, &stderr); code != 2 || stdout.Len() != 0 {
			t.Errorf("%s: status %d, stdout %q, want 2 and nothing", in, code, stdout.String())
		}
	}
	var stdout, stderr bytes.Buffer
	code := run(strings.NewReader(`{"op": "connect", "path": ""}`), &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), `"error":"invalid-argument"`) {
		t.Fatalf("the empty path: status %d, stdout %q", code, stdout.String())
	}
}

// "invalid-argument" is for sockat's own argument errors only (the hub's
// docs/design/conformance.md: an argument error is invalid-argument, an
// errno is its C name): an empty path, a NUL, a long path not ending with a
// file name, an Options.Base of the wrong form, for a bind and a connect
// alike. They are EINVAL to errors.Is too, so the CLI must tell them by
// sockat's internal mark, not by the errno: each must come out as
// "invalid-argument", with status 0.
func TestSockatsArgumentErrorsAreInvalidArgument(t *testing.T) {
	d, err := os.MkdirTemp("/tmp", "sgt-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(d)
	long := d + "/" + strings.Repeat(strings.Repeat("d", 60)+"/", 3)
	for _, op := range []string{"bind", "connect"} {
		for _, in := range []map[string]any{
			{"path": ""},
			{"path": d + "/a\x00b"},
			{"path": long + "s.sock\x00b"},
			{"path": long},
			{"path": long + ".."},
			{"path": d + "/s.sock", "base": "rel"},
			{"path": long + "s.sock", "base": d + "/b/"},
			{"path": long + "s.sock", "base": d + "/b\x00c"},
		} {
			in["op"] = op
			if got := errorOf(t, in); got != "invalid-argument" {
				t.Errorf("%q: error %q, want invalid-argument", in, got)
			}
		}
	}
}

// An EINVAL that is not one of sockat's argument errors, one a system call
// returned (here in the shape sockat returns a failed bind in: a
// *RouteError around a *net.OpError around an *os.SyscallError), is reported
// by its errno's name, "EINVAL", as any other errno is. Where the kernel has
// abstract names (Linux, Android), an abstract name too long for sun_path is
// one too, end to end: sockat hands it to the net package as it is, and the
// standard library refuses it with an EINVAL of its own.
func TestAnEINVALNotSockatsIsEINVAL(t *testing.T) {
	sys := &sockat.RouteError{Route: "standard", Err: &net.OpError{Op: "listen", Net: "unix", Err: os.NewSyscallError("bind", syscall.EINVAL)}}
	if got := errorName(sys); got != "EINVAL" {
		t.Errorf("a system call's EINVAL: %q, want EINVAL", got)
	}
	if runtime.GOOS != "linux" && runtime.GOOS != "android" {
		return
	}
	for _, op := range []string{"bind", "connect"} {
		in := map[string]any{"op": op, "path": "@" + strings.Repeat("a", 200)}
		if got := errorOf(t, in); got != "EINVAL" {
			t.Errorf("%s of an abstract name too long: error %q, want EINVAL", op, got)
		}
	}
}

// errorOf runs the CLI on in and returns the output's error, failing the test
// unless the CLI took the input (status 0) and failed.
func errorOf(t *testing.T, in map[string]any) string {
	t.Helper()
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := run(bytes.NewReader(b), &stdout, &stderr); code != 0 {
		t.Fatalf("%s: status %d, stderr %q", b, code, stderr.String())
	}
	var out output
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("%s: %v: %q", b, err, stdout.String())
	}
	if out.OK || out.Error == nil {
		t.Fatalf("%s: not a failure: %q", b, stdout.String())
	}
	return *out.Error
}
