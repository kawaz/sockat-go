// Package conformhook hands the conformance CLI (internal/cmd/sockat-conform)
// one bind / connect through the route order with what the routes report,
// which package sockat does not export.
package conformhook

// Outcome is one bind / connect's result as the conformance test reads it.
type Outcome struct {
	Err     error    // nil when it went through
	Route   string   // the route taken, or the one that bound / connected and failed
	Losses  []string // SPEC's names of what was lost
	Skipped []string // the routes found unusable, in the order tried
}

// Run binds (op "bind", leaving the listener open for the process's exit to
// close) or connects (op
// "connect", sending one byte "x" before closing) at path. Set by package
// sockat when it is initialised.
var Run func(op, path string) Outcome
