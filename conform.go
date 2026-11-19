package sockat

import (
	"net"

	"github.com/kawaz/sockat-go/internal/conformhook"
)

func init() {
	conformhook.Run = conformRun
}

// conformListener keeps a bound listener reachable until the process exits,
// so that no finalizer closes it first.
var conformListener endpoint

// conformRun is one bind / connect for the conformance CLI: the default
// route order (Listen / Dial), with the routes recorded as atanycost records
// them. A bind's listener is left open for the process's exit to close; a
// connect sends "x" and closes.
//
// Design rationale: the hub's conformance rule is to end the process without
// closing a bound listener. Closing would remove the socket file for a short
// path (Go's standard unlinks on Close), a difference between languages'
// standards that the conformance test does not mean to compare.
func conformRun(opName, path string) conformhook.Outcome {
	o := opListen
	if opName == "connect" {
		o = opDial
	}
	cfg := configFromEnv()
	cfg.trace = &trace{}
	ep, r, err := runWith(o, path, cfg)
	out := conformhook.Outcome{Err: err, Route: string(r), Losses: []string{}, Skipped: []string{}}
	for _, s := range cfg.trace.skipped {
		out.Skipped = append(out.Skipped, string(s.name))
	}
	if err != nil {
		return out
	}
	if o == opListen {
		out.Losses = append(out.Losses, lossesOf(r, path)...)
		conformListener = ep
		return out
	}
	c := ep.(*net.UnixConn)
	c.Write([]byte("x"))
	c.Close()
	return out
}
