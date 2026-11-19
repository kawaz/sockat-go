package sockat

import (
	"errors"
	"os"
	"strconv"
)

// The connect order of a dial (SPEC "connect の順"): the routes that need no
// writing (D, R, a symlink) and no thread first, the routes that make D or
// start a thread after them. The order is the system's (dialSteps, in the
// os_*.go files):
//
//	| system                 | order                                                |
//	|------------------------|------------------------------------------------------|
//	| Linux with /proc       | proc-direct, thread-cwd, proc, proc-held, symlink    |
//	| Linux without /proc    | relative, thread-cwd, symlink                        |
//	| macOS                  | relative, widened (not made, DR-0005), thread-cwd, symlink |
//	| FreeBSD                | at-direct, at                                        |
//	| NetBSD, OpenBSD, other | relative, symlink                                    |
//
// A connect leaves no spelling behind with the peer (the connecting socket is
// not bound), so the routes that do without R (proc-direct, relative,
// at-direct, proc-held, symlink) are safe for a dial; a listen never takes
// them.

// dialStep is one route of the connect order: its name and how it is tried.
// try returns an unusable error when the route cannot be used here, and
// otherwise the outcome of the connect it made.
type dialStep struct {
	route route
	try   func(*dialer) (endpoint, error)
}

// unusable is errUnusable with the reason the route could not be used, which
// the trace records (diagnostics only).
type unusable string

func (u unusable) Error() string        { return "sockat: route unusable: " + string(u) }
func (u unusable) Is(target error) bool { return target == errUnusable }

// dialer is one dial of a long path through the connect order.
type dialer struct {
	path, dir, name string
	real            string   // the socket's directory as a real path, for R
	sdir            *os.File // the socket's directory, nil when it could not be opened
	cfg             *config
	// procUsable is whether /proc can be used (Linux): it picks the order,
	// and how D is opened.
	procUsable bool
	// nextD and nextR name D and R (randomPart outside the tests).
	nextD, nextR func() (string, error)
	// D, made at most once by the first route that uses it (dTried), and
	// shared by the later ones; nil when it could be made nowhere.
	dTried bool
	dh     *held
}

func newDialer(path, dir, real, name string, sdir *os.File, cfg *config) *dialer {
	return &dialer{path: path, dir: dir, name: name, real: real, sdir: sdir, cfg: cfg,
		procUsable: procFDUsableFor(sdir, cfg.procSelfFD), nextD: randomPart, nextR: randomPart}
}

// run tries steps in order. A route that cannot be used is recorded in the
// trace and the next one tried; the first route that connects decides, its
// failure included (the next routes are not tried). errUnusable when no
// route can be used.
func (d *dialer) run(steps []dialStep) (endpoint, route, error) {
	for _, s := range steps {
		ep, err := s.try(d)
		if errors.Is(err, errUnusable) {
			reason := "unusable"
			var u unusable
			if errors.As(err, &u) {
				reason = string(u)
			}
			d.cfg.trace.add(s.route, reason)
			continue
		}
		return ep, s.route, err
	}
	return nil, "", errUnusable
}

// close removes D (and R) when they were made. Best effort.
func (d *dialer) close() {
	if d.dh != nil {
		d.dh.cleanup()
	}
}

// theD is D for the routes that use it (thread-cwd, proc, at, proc-held),
// with R in it when withR (proc-held needs none). D is made once, by the first
// of them that gets this far; when it could be made nowhere, every later one
// is unusable too and D is not tried again (SPEC "D と R").
func (d *dialer) theD(withR bool) (*held, error) {
	if !d.dTried {
		d.dTried = true
		nextR := d.nextR
		if !withR {
			nextR = nil
		}
		if h, err := makeD(d.cfg.dBases, d.sdir, d.real, d.procUsable, d.cfg.procSelfFD, d.nextD, nextR); err == nil {
			d.dh = h
		}
	}
	if d.dh == nil {
		return nil, unusable("no place for D")
	}
	if withR && d.dh.r == "" {
		// D was made without R (by proc-held); R is made in it now.
		if err := d.dh.makeR(d.real, d.nextR); err != nil {
			return nil, unusable("R not made")
		}
	}
	return d.dh, nil
}

// spellingFits reports whether R/<name> fits the sockaddr.
func (d *dialer) spellingFits() bool { return rNameLen+1+len(d.name) <= directMax }

// threadCwd connects through R/<name> from a thread whose own cwd is D
// (inThreadCwd: Linux, macOS).
func (d *dialer) threadCwd() (endpoint, error) {
	if !d.spellingFits() {
		return nil, unusable("spelling too long")
	}
	if !d.cfg.threadCwd {
		return nil, unusable("thread cwd off")
	}
	if !threadCwdPossible() {
		return nil, unusable("seccomp filter")
	}
	h, err := d.theD(true)
	if err != nil {
		return nil, err
	}
	ep, err := inThreadCwd(opDial, h.fd, h.r+"/"+d.name)
	if errors.Is(err, errUnusable) {
		return nil, unusable("thread cwd refused")
	}
	return ep, err
}

// proc connects through /proc/self/fd/<D>/R/<name> (Linux with /proc). Its
// length depends on D's fd: estimated with a one-digit fd before D is made,
// checked again once D is open.
func (d *dialer) proc() (endpoint, error) {
	spellingLen := rNameLen + 1 + len(d.name)
	if len(d.cfg.procSelfFD)+len("/0/")+spellingLen > directMax {
		return nil, unusable("spelling too long")
	}
	h, err := d.theD(true)
	if err != nil {
		return nil, err
	}
	via := d.cfg.procSelfFD + "/" + strconv.Itoa(h.fd) + "/" + h.r + "/" + d.name
	if len(via) > directMax {
		return nil, unusable("spelling too long")
	}
	return viaStd(opDial, via, false)
}
