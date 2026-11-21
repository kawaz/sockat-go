// Command sockat-conform is the conformance CLI of sockat-go (the hub's
// docs/design/conformance.md, "試験用 CLI の決まり"): it reads {"op", "path",
// "usable_cross_uid", "base"} from stdin, binds or connects once with
// sockat.ListenEx / sockat.DialEx and the Options given, and writes {"ok",
// "error", "route", "base", "spelling", "skipped"} to stdout. Not part of the
// module's API.
//
// Neither Remove nor Cleanup is called (the hub looks at what is left in the
// places). A bind's listener is left open for the process's exit to close; a
// connect sends one byte "x" before closing.
//
// Design rationale: the hub's conformance rule is to end the process without
// closing a bound listener. Closing would remove the socket file where the
// standard library unlinks on Close, a difference between languages'
// standards that the conformance test does not mean to compare.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"syscall"

	"github.com/kawaz/sockat-go"
	"github.com/kawaz/sockat-go/internal/argerr"
	"golang.org/x/sys/unix"
)

type input struct {
	Op             string  `json:"op"`
	Path           *string `json:"path"`
	UsableCrossUID bool    `json:"usable_cross_uid"`
	Base           *string `json:"base"`
}

type spelling struct {
	Kind                 string `json:"kind"`
	Removable            *bool  `json:"removable,omitempty"`
	Until                string `json:"until,omitempty"`
	UsableCrossUID       *bool  `json:"usable_cross_uid,omitempty"`
	UsableWithPrivateTmp *bool  `json:"usable_with_private_tmp,omitempty"`
}

type output struct {
	OK       bool      `json:"ok"`
	Error    *string   `json:"error"`
	Route    *string   `json:"route"`
	Base     *string   `json:"base"`
	Spelling *spelling `json:"spelling"`
	Skipped  []string  `json:"skipped"`
}

// keep holds a bound listener until the process exits, so that no finalizer
// closes it first.
var keep *net.UnixListener

func main() {
	os.Exit(run(os.Stdin, os.Stdout, os.Stderr))
}

// run reads the input from stdin, does the bind or connect and writes the
// output to stdout. It returns the exit status: 0 once the output is
// written, 2 for an input it cannot take (nothing written to stdout).
func run(stdin io.Reader, stdout, stderr io.Writer) int {
	var in input
	// path has no default: an input without it (or with it null) is not
	// one the CLI takes, unlike usable_cross_uid and base.
	if err := json.NewDecoder(stdin).Decode(&in); err != nil || (in.Op != "bind" && in.Op != "connect") || in.Path == nil {
		fmt.Fprintln(stderr, `sockat-conform: want {"op": "bind"|"connect", "path": ..., "usable_cross_uid": true|false, "base": null|...} on stdin`)
		return 2
	}
	opts := sockat.Options{UsableCrossUID: in.UsableCrossUID}
	if in.Base != nil {
		opts.Base = *in.Base
	}
	var out output
	if in.Op == "bind" {
		var b *sockat.Bound
		var err error
		keep, b, err = sockat.ListenEx(*in.Path, opts)
		out = bindReport(b, err)
	} else {
		c, r, err := sockat.DialEx(*in.Path, opts)
		if err == nil {
			c.Write([]byte("x"))
			c.Close()
		}
		out = report(r.Route, r.Skipped, err)
	}
	if err := json.NewEncoder(stdout).Encode(out); err != nil {
		return 2
	}
	return 0
}

// bindReport is the output for a bind: report, and on success the base and
// the spelling.
func bindReport(b *sockat.Bound, err error) output {
	if err != nil {
		return report("", nil, err)
	}
	out := report(b.Route, b.Skipped, nil)
	if b.Base != "" {
		out.Base = &b.Base
	}
	s := b.Spelling
	out.Spelling = &spelling{Kind: s.Kind.String()}
	if s.Kind == sockat.Linked {
		out.Spelling.Removable = &s.Removable
		out.Spelling.Until = s.Until.String()
		out.Spelling.UsableCrossUID = &s.UsableCrossUID
		out.Spelling.UsableWithPrivateTmp = &s.UsableWithPrivateTmp
	}
	return out
}

// report is the output for one bind / connect without its base and
// spelling: the route taken on success; on failure the error's name and, from
// the *sockat.RouteError, the route that bound / connected and failed (null
// when none did). The routes skipped either way, without their reasons (not
// compared).
func report(route string, skipped []sockat.Skip, err error) output {
	out := output{OK: err == nil, Skipped: []string{}}
	if err != nil {
		name := errorName(err)
		out.Error = &name
		var re *sockat.RouteError
		if errors.As(err, &re) {
			route, skipped = re.Route, re.Skipped
		}
	}
	if route != "" {
		out.Route = &route
	}
	for _, s := range skipped {
		out.Skipped = append(out.Skipped, s.Name)
	}
	return out
}

// errorName is "invalid-argument" for sockat's own argument errors (marked
// by internal/argerr: an empty path, a NUL, a long path not ending with a
// file name, an Options.Base of the wrong form), the errno's C name for any
// other error with an errno (EAGAIN, EOPNOTSUPP and EDEADLK for the values
// with two names; an EINVAL that a system call or the standard library
// returned is "EINVAL"), errno-<n> for an errno without a name, or "general"
// for an error without an errno.
func errorName(err error) string {
	if argerr.Is(err) {
		return "invalid-argument"
	}
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return "general"
	}
	switch errno {
	case unix.EAGAIN:
		return "EAGAIN"
	case unix.EOPNOTSUPP:
		return "EOPNOTSUPP"
	case unix.EDEADLK:
		return "EDEADLK"
	}
	if name := unix.ErrnoName(errno); name != "" {
		return name
	}
	return fmt.Sprintf("errno-%d", int(errno))
}
