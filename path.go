package sockat

import (
	"strings"
	"syscall"
)

// Pure path arithmetic: splitting a socket path into its directory and file
// name, and spelling an absolute path relative to a working directory.
// Nothing here touches the file system.

// splitPath splits a socket path into the directory that holds the socket and
// the socket's file name, by the bytes after the last '/'.
//
// The split is done on the bytes rather than through path/filepath, which
// drops a trailing '/' and '.': "a/b/" and "a/b/." would both come out as file
// name "b", while the kernel resolves them as the directory "a/b" itself. Such
// paths do not name a socket file, so they are refused rather than silently
// retargeted.
//
//	| path        | directory | file name |
//	|-------------|-----------|-----------|
//	| /d/e/s.sock | /d/e      | s.sock    |
//	| /s.sock     | /         | s.sock    |
//	| s.sock      | .         | s.sock    |
//	| d//s.sock   | d/        | s.sock    |
//	| d/, d/., d/.., .. | (EINVAL) |       |
func splitPath(path string) (dir, name string, err error) {
	switch i := strings.LastIndexByte(path, '/'); {
	case i == 0:
		dir, name = "/", path[1:]
	case i > 0:
		dir, name = path[:i], path[i+1:]
	default:
		dir, name = ".", path
	}
	if name == "" || name == "." || name == ".." {
		return "", "", syscall.EINVAL
	}
	return dir, name, nil
}

// components splits a path into its components the way the kernel walks it:
// empty segments (doubled or trailing slashes) and "." are dropped, ".." is
// kept as it is (it is not interpreted lexically, which would be wrong across
// symlinks). An absolute path starts with the component "/".
func components(path string) []string {
	var out []string
	if strings.HasPrefix(path, "/") {
		out = append(out, "/")
	}
	for _, c := range strings.Split(path, "/") {
		if c != "" && c != "." {
			out = append(out, c)
		}
	}
	return out
}

func isAbs(path string) bool { return strings.HasPrefix(path, "/") }

// joinComponents joins components that do not start with "/".
func joinComponents(cs []string) string { return strings.Join(cs, "/") }

// below returns the part of target below the directory prefix, when target
// starts with prefix component by component ("/a/b" is a prefix of "/a/b/c"
// but not of "/a/bc").
//
// The result only descends from prefix along target's own components, so it
// resolves through the same symlinks target does. ok is false when either is
// not absolute or prefix is not a proper prefix (including target == prefix,
// which leaves no file name).
func below(target, prefix string) (rest string, ok bool) {
	if !isAbs(target) || !isAbs(prefix) {
		return "", false
	}
	t, p := components(target), components(prefix)
	if len(t) <= len(p) {
		return "", false
	}
	for i := range p {
		if t[i] != p[i] {
			return "", false
		}
	}
	return joinComponents(t[len(p):]), true
}

// relativeTo spells the absolute path target relative to the absolute
// directory cwd: climb with ".." out of the part of cwd that target does not
// share, then descend along the rest of target.
//
// This is component arithmetic and is only meaningful when both inputs are
// real paths (no symlinks, "." or ".."): cwd as getcwd(3) reports it and target
// with its directory resolved to a real path. Then each ".." climbs out of a
// real directory, which is what the kernel does. The caller still confirms
// that the result names the same directory before using it.
//
// ok is false when either is not absolute, or when target is cwd itself or
// one of its ancestors (there would be no file name left).
func relativeTo(target, cwd string) (rel string, ok bool) {
	if !isAbs(target) || !isAbs(cwd) {
		return "", false
	}
	t, c := components(target), components(cwd)
	common := 0
	for common < len(t) && common < len(c) && t[common] == c[common] {
		common++
	}
	if common >= len(t) {
		return "", false
	}
	var out []string
	for range c[common:] {
		out = append(out, "..")
	}
	out = append(out, t[common:]...)
	return joinComponents(out), true
}

// asSpelling makes a relative spelling safe to hand to the standard library:
// one that starts with '@' gets "./" in front, because Go's net package reads
// a leading '@' as an abstract socket name on Linux (and illumos).
func asSpelling(rel string) string {
	if strings.HasPrefix(rel, "@") {
		return "./" + rel
	}
	return rel
}
