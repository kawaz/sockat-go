// Command sockat-conform is the conformance CLI of sockat-go (the hub's
// docs/design/conformance.md): it reads {"op", "path"} from stdin, binds or
// connects once through the default route order without the last resort
// (sockat.ListenEx / sockat.DialEx with NoLastResort), and writes {"ok",
// "error", "route", "losses", "skipped"} to stdout. Not part of the module's
// API.
//
// A bind's listener is left open for the process's exit to close; a connect
// sends one byte "x" before closing.
//
// Design rationale: the hub's conformance rule is to end the process without
// closing a bound listener. Closing would remove the socket file for a short
// path where the standard library unlinks on Close (Go's does, unless told
// not to), a difference between languages' standards that the conformance
// test does not mean to compare.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"syscall"

	"github.com/kawaz/sockat-go"
	"golang.org/x/sys/unix"
)

type input struct {
	Op   string `json:"op"`
	Path string `json:"path"`
}

type output struct {
	OK      bool     `json:"ok"`
	Error   *string  `json:"error"`
	Route   *string  `json:"route"`
	Losses  []string `json:"losses"`
	Skipped []string `json:"skipped"`
}

// keep holds a bound listener until the process exits, so that no finalizer
// closes it first.
var keep *net.UnixListener

func main() {
	var in input
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil || (in.Op != "bind" && in.Op != "connect") {
		fmt.Fprintln(os.Stderr, "sockat-conform: want {\"op\": \"bind\"|\"connect\", \"path\": ...} on stdin")
		os.Exit(2)
	}
	var res sockat.Result
	var err error
	if in.Op == "bind" {
		keep, _, res, err = sockat.ListenEx(in.Path, sockat.NoLastResort)
	} else {
		var c *net.UnixConn
		c, res, err = sockat.DialEx(in.Path, sockat.NoLastResort)
		if err == nil {
			c.Write([]byte("x"))
			c.Close()
		}
	}
	if err := json.NewEncoder(os.Stdout).Encode(report(res, err)); err != nil {
		os.Exit(2)
	}
}

// report is the output for one bind / connect: the route taken and its losses
// on success; on failure the error's name and, from the *sockat.RouteError,
// the route that bound / connected and failed (null when none did). The routes
// skipped either way, without their reasons (not compared).
func report(res sockat.Result, err error) output {
	out := output{OK: err == nil, Losses: []string{}, Skipped: []string{}}
	route, skipped := res.Route, res.Skipped
	if err != nil {
		name := errorName(err)
		out.Error = &name
		var re *sockat.RouteError
		if errors.As(err, &re) {
			route, skipped = re.Route, re.Skipped
		}
	} else {
		for _, l := range res.Losses {
			out.Losses = append(out.Losses, string(l))
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

// errorName is the errno's name, "invalid-argument" for sockat's argument
// errors (EINVAL: a NUL, a long path not ending with a file name), or
// "general" for an error without an errno.
func errorName(err error) string {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return "general"
	}
	if errno == syscall.EINVAL {
		return "invalid-argument"
	}
	if name := unix.ErrnoName(errno); name != "" {
		return name
	}
	return "general"
}
