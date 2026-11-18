// Package anyhook connects package atanycost to the routes of package sockat,
// which are not exported.
package anyhook

import "net"

// Outcome is what a listen at any cost reports beside the listener.
type Outcome struct {
	Level   int    // atanycost.Level's values
	Route   string // the route taken
	Skipped []Skip // the routes tried and found unusable, in order
}

// Skip is a route found unusable and why.
type Skip struct{ Name, Reason string }

// Listen is sockat's listen at any cost, set by package sockat when it is
// initialised.
//
// Design rationale: the routes live unexported in package sockat, and the
// opt-in API lives in a package of its own (importing it is the opt-in), so
// the one function it needs is handed over here rather than exported from
// package sockat, where it would be part of the default API.
var Listen func(path string) (*net.UnixListener, Outcome, error)
