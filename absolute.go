package sockat

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// The absolute candidates of a path (SPEC "相対パスを絶対パスにする",
// SPEC-routes "絶対パスの候補の作り方"). A bind keeps its spelling, and a
// resolve's spelling may be used long after, so bind and resolve hand the
// standard library an absolute path naming the same place as the caller's:
// the spelling the kernel keeps then names the socket file whatever the cwd
// becomes. A connect keeps no spelling and is not concerned (dial.go).
//
//	| candidate | made from                                              | for                              |
//	|-----------|--------------------------------------------------------|----------------------------------|
//	| real path | the socket's directory's real path, then the file name | a relative path, a long absolute |
//	| $PWD      | $PWD and the relative path, folded lexically, checked  | a relative path                  |
//
// The first that fits directMax is handed to the standard library (route
// standard, Original), and nothing is made in a place. A candidate that cannot
// be made is no error: there is just no such candidate. A relative path with
// none that fits takes the routes, and only when no route can be used is it
// handed to the standard library as it is (CwdRelative, bindAsIs).

// relCandidate is the first absolute candidate of the relative path dir/name
// (dir its directory part as splitPath gives it) that fits directMax: the
// real-path one, then the $PWD one. "" when neither can be made or fits.
func relCandidate(dir, name string) string {
	if real, err := realDir(dir); err == nil {
		if s := joinUnder(real, name); len(s) <= directMax {
			return s
		}
	}
	if s, ok := pwdCandidate(dir, name); ok && len(s) <= directMax {
		return s
	}
	return ""
}

// pwdCandidate is the $PWD candidate of the relative path dir/name: $PWD and
// dir joined and folded lexically, then the file name, when $PWD names the
// cwd and the folded directory is dir itself.
//
// $PWD is an environment variable the process inherited: it is stale after a
// chdir(2) that did not update it, and anyone can set it. It is used only when
// it is absolute and stat($PWD) and stat(".") agree on (st_dev, st_ino), as
// for the relative spelling of a connect (cwdSpellings).
//
// Design rationale: SPEC-routes elsewhere forbids folding a path lexically,
// since "a/sym/.." is, to the kernel, the parent of where sym leads; here the
// folding is the candidate's whole point (the spelling the shell used to come
// to the cwd, often through a short symlink, without "../" to grow it), so
// filepath.Clean does it, and the folded directory is used only when its
// (st_dev, st_ino) is that of dir looked up from the cwd, which rules out
// every folding that changed the place.
func pwdCandidate(dir, name string) (string, bool) {
	pwd := os.Getenv("PWD")
	if !isAbs(pwd) {
		return "", false
	}
	cwd, ok := statID(".")
	if !ok {
		return "", false
	}
	if id, ok := statID(pwd); !ok || id != cwd {
		return "", false
	}
	folded := filepath.Clean(pwd + "/" + dir)
	got, ok1 := statID(folded)
	want, ok2 := statID(dir)
	if !ok1 || !ok2 || got != want {
		return "", false
	}
	return joinUnder(folded, name), true
}

// pastPathMax reports whether err, from realDir of a relative directory,
// means that the directory has no real path within PATH_MAX (a cwd too deep
// to spell): then no route can be used for the path, rather than the call
// failing (SPEC "相対パスを絶対パスにする").
//
// Design rationale: SPEC names ENAMETOOLONG and ERANGE, the errors of
// realpath(3) past PATH_MAX. realDir is not realpath(3) but getcwd through
// syscall.Getwd, with a buffer of PATH_MAX, and filepath.EvalSymlinks. Past
// PATH_MAX, syscall.Getwd fails ENAMETOOLONG on Linux and EINVAL on macOS
// (both measured with Go 1.27, 2026-11-22: Linux from about 4200 bytes,
// macOS from 1027), and an lstat of the joined path ENAMETOOLONG; so EINVAL
// is taken too. ERANGE, POSIX's getcwd(3) error for a buffer too small, is
// for the other systems (not measured). syscall.Getwd has no other EINVAL to
// give here (its buffer is never empty), and an lstat none.
func pastPathMax(err error) bool {
	return errors.Is(err, syscall.ENAMETOOLONG) || errors.Is(err, syscall.ERANGE) || errors.Is(err, syscall.EINVAL)
}

// fitsAsIs reports whether the relative path can be handed to the standard
// library as it is (stdSpelling within directMax).
func fitsAsIs(path string) bool {
	return len(stdSpelling(path)) <= directMax
}
