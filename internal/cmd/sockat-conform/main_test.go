package main

import (
	"bytes"
	"strings"
	"testing"
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
