// Command sockat-conform is the conformance CLI of sockat-go (the hub's
// docs/design/conformance.md): it reads {"op", "path"} from stdin, binds or
// connects once through the route order, and writes {"ok", "error", "route",
// "losses", "skipped"} to stdout. Not part of the module's API.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"syscall"

	_ "github.com/kawaz/sockat-go" // sets conformhook.Run
	"github.com/kawaz/sockat-go/internal/conformhook"
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

func main() {
	var in input
	if err := json.NewDecoder(os.Stdin).Decode(&in); err != nil || (in.Op != "bind" && in.Op != "connect") {
		fmt.Fprintln(os.Stderr, "sockat-conform: want {\"op\": \"bind\"|\"connect\", \"path\": ...} on stdin")
		os.Exit(2)
	}
	r := conformhook.Run(in.Op, in.Path)
	out := output{OK: r.Err == nil, Losses: r.Losses, Skipped: r.Skipped}
	if r.Route != "" {
		out.Route = &r.Route
	}
	if r.Err != nil {
		name := errorName(r.Err)
		out.Error = &name
	}
	if err := json.NewEncoder(os.Stdout).Encode(out); err != nil {
		os.Exit(2)
	}
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
