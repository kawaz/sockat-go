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
		if err != nil {
			return nil, anyhook.Outcome{}, err
		}
		o := anyhook.Outcome{Level: int(out.loss), Route: string(out.route)}
		for _, s := range out.skipped {
			o.Skipped = append(o.Skipped, anyhook.Skip{Name: string(s.name), Reason: s.reason})
		}
		return ep.(*net.UnixListener), o, nil
	}
}

// loss is what a route gave up compared with the standard library (SPEC
// "失ったものの段階"); atanycost.Loss mirrors the values.
type loss int

const (
	lossNone          loss = iota // the standard library itself
	lossRespelled                 // the kernel keeps a spelling, not the path
	lossForked                    // and a fork's side effects (no fork route in Go)
	lossSharedSymlink             // and others can make the spelling again
)

// lossOf is the loss of a route other than ListenAny's symlink.
func lossOf(r route) loss {
	if r == routeStandard {
		return lossNone
	}
	return lossRespelled
}

// anyOutcome is what listenAny reports beside the listener: the loss, the
// route taken and the routes tried and found unusable before it.
type anyOutcome struct {
	loss    loss
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
		return ep, anyOutcome{lossOf(r), r, c.trace.skipped}, nil
	}
	if !errors.Is(err, syscall.ENAMETOOLONG) || len(path) <= directMax || abstractName(path) {
		return nil, anyOutcome{}, err
	}
	ep, shared, err2 := symlinkBind(path, &c)
	switch {
	case errors.Is(err2, errUnusable):
		return nil, anyOutcome{}, err
	case err2 != nil:
		return nil, anyOutcome{}, opError(opListen, path, err2)
	case shared:
		return ep, anyOutcome{lossSharedSymlink, routeSymlink, c.trace.skipped}, nil
	default:
		return ep, anyOutcome{lossRespelled, routeSymlink, c.trace.skipped}, nil
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
