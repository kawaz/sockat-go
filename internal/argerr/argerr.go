// Package argerr marks the argument errors sockat finds itself (SPEC
// "エラー": an empty path, a NUL, a long path not ending with a file name, an
// Options.Base of the wrong form), so that the conformance CLI
// (internal/cmd/sockat-conform) can tell them from an EINVAL that a system
// call, or the standard library below sockat, returned.
//
// The mark changes nothing a caller of sockat sees: a marked error reads as
// the error it marks and unwraps to it (errors.Is(err, syscall.EINVAL)
// holds), and the mark's type cannot be named outside this package.
package argerr

import "errors"

// marked is an error sockat returns for an argument of the wrong form.
type marked struct{ err error }

func (m *marked) Error() string { return m.err.Error() }
func (m *marked) Unwrap() error { return m.err }

// Mark returns err marked as an argument error.
func Mark(err error) error { return &marked{err} }

// Is reports whether err, or an error it wraps, is marked as an argument
// error.
func Is(err error) bool {
	var m *marked
	return errors.As(err, &m)
}
