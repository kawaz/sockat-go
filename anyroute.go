package sockat

import (
	"errors"
	"net"
	"os"
	"syscall"

	"github.com/kawaz/sockat-go/internal/anyhook"
	"golang.org/x/sys/unix"
)

func init() {
	anyhook.Listen = func(path string) (*net.UnixListener, anyhook.Outcome, error) {
		ep, out, err := listenAny(path, nil)
		o := anyhook.Outcome{Losses: out.losses, Route: string(out.route)}
		for _, s := range out.skipped {
			o.Skipped = append(o.Skipped, anyhook.Skip{Name: string(s.name), Reason: s.reason})
		}
		if err != nil {
			return nil, o, err
		}
		return ep.(*net.UnixListener), o, nil
	}
}

// The names of what a route gives up compared with the standard library
// (SPEC "失うもの"); atanycost.Loss has the same values.
const (
	lossRespelled           = "respelled"            // the kernel keeps a spelling, not the path
	lossRecreatableSpelling = "recreatable-spelling" // the spelling goes through a shared place
	lossCwdRelative         = "cwd-relative"         // the kernel keeps a path relative to the cwd
)

// lossesOf is what a bind of path through r gave up: for standard, nothing,
// or cwd-relative when path is relative (the kernel keeps it as given, to be
// resolved against whatever the cwd is later; an abstract name is no path);
// respelled for the others (a shared symlink is told apart by listenAny).
func lossesOf(r route, path string) []string {
	if r == routeStandard {
		if !isAbs(path) && !abstractName(path) {
			return []string{lossCwdRelative}
		}
		return nil
	}
	return []string{lossRespelled}
}

// anyOutcome is what listenAny reports: on success the losses, the route
// taken and the routes tried and found unusable before it; on failure the
// route that bound and failed (if any) and the routes found unusable.
type anyOutcome struct {
	losses  []string
	route   route
	skipped []skipped
}

// listenAny binds as Listen does and, where that ends in ENAMETOOLONG, through
// a symlink in a place (symlinkBind): SPEC's bind_any.
func listenAny(path string, cfg *config) (endpoint, anyOutcome, error) {
	if cfg == nil {
		cfg = configFromEnv()
	}
	c := *cfg
	c.trace = &trace{}
	ep, r, err := runWith(opListen, path, &c)
	if err == nil {
		return ep, anyOutcome{lossesOf(r, path), r, c.trace.skipped}, nil
	}
	if !errors.Is(err, syscall.ENAMETOOLONG) || len(path) <= directMax || abstractName(path) {
		return nil, anyOutcome{route: r, skipped: c.trace.skipped}, err
	}
	ep, shared, err2 := symlinkBind(path, &c)
	switch {
	case errors.Is(err2, errUnusable):
		return nil, anyOutcome{skipped: c.trace.skipped}, err
	case err2 != nil:
		return nil, anyOutcome{route: routeSymlink, skipped: c.trace.skipped}, opError(opListen, path, err2)
	case shared:
		return ep, anyOutcome{[]string{lossRespelled, lossRecreatableSpelling}, routeSymlink, c.trace.skipped}, nil
	default:
		return ep, anyOutcome{[]string{lossRespelled}, routeSymlink, c.trace.skipped}, nil
	}
}

// symlinkBind binds through <place>/sockat.XXXXXXXXXX/<name>, the symlink
// pointing to the socket's directory (its real path) and removed right after.
// The places (cfg.anyBases) that only the effective uid can write to are tried
// first, in order; then the shared ones (sticky, owned by root or us). shared
// reports that the bind went through a shared place, where others can make the
// spelling again. A real failure of the bind is returned as it is;
// errUnusable when no place can be used (nothing left behind).
func symlinkBind(path string, cfg *config) (ep endpoint, shared bool, err error) {
	dir, name, err := splitPath(path)
	if err != nil {
		return nil, false, err
	}
	real, err := realDir(dir)
	if err != nil {
		return nil, false, err
	}
	euid := uint32(os.Geteuid())
	for _, wantShared := range []bool{false, true} {
		for _, base := range cfg.anyBases {
			var st unix.Stat_t
			if unix.Stat(base, &st) != nil || uint32(st.Mode)&unix.S_IFMT != unix.S_IFDIR {
				continue
			}
			mode := uint32(st.Mode)
			if wantShared {
				if mode&0o1000 == 0 || (st.Uid != euid && st.Uid != 0) {
					continue
				}
			} else if st.Uid != euid || mode&0o022 != 0 {
				continue
			}
			if len(underBase(base, linkPrefix+string(make([]byte, linkRandomLen))))+1+len(name) > directMax {
				continue
			}
			link, err := createLink(base, real, randomName)
			if err != nil {
				continue
			}
			ep, err := viaStd(opListen, link+"/"+name, false)
			os.Remove(link)
			return ep, wantShared, err
		}
	}
	cfg.trace.add(routeSymlink, "no usable base")
	return nil, false, errUnusable
}
