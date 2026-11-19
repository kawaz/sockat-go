package sockat

import (
	"net"

	"github.com/kawaz/sockat-go/internal/conformhook"
)

func init() {
	conformhook.Run = conformRun
}

// conformRun is one bind / connect for the conformance CLI: the default
// route order (Listen / Dial), with the routes recorded as atanycost records
// them. A bind's listener is closed after (the file is left); a connect sends
// "x" and closes.
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
		ep.Close()
		return out
	}
	c := ep.(*net.UnixConn)
	c.Write([]byte("x"))
	c.Close()
	return out
}
