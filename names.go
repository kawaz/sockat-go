package sockat

import (
	"crypto/rand"
	"errors"

	"golang.org/x/sys/unix"
)

// linkRandomLen is the length of a random name: D's, an E made for one call,
// and the random part of a B fallen back to (SPEC-routes "D と E").
const linkRandomLen = 10

// linkAttempts is how many names createAt tries before giving up. Each try
// is a fresh random name from 62^10 possibilities, so exhausting this means
// something other than bad luck (for example a directory that refuses every
// name).
const linkAttempts = 16

// errNoFreeName is returned when createAt ran out of names.
var errNoFreeName = errors.New("could not find a free name for a directory or symlink")

// randomChars returns linkRandomLen random letters and digits (62 kinds) from
// the system's secure generator (crypto/rand): the random names of D, of the
// E made for one call, and of the B fallen back to (SPEC-routes "D と E").
func randomChars() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	var random [linkRandomLen]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	name := make([]byte, 0, linkRandomLen)
	// 256 is not a multiple of 62, so this is slightly biased; the name only
	// has to be hard to collide with, not uniform.
	for _, b := range random {
		name = append(name, alphabet[int(b)%len(alphabet)])
	}
	return string(name), nil
}

// createAt calls create with names from next until one is not taken
// (EEXIST), linkAttempts at most, and returns the name created. mkdirat(2)
// and symlinkat(2) fail with EEXIST rather than reuse an existing entry, so
// what is created is always one nobody else had.
func createAt(next func() (string, error), create func(name string) error) (string, error) {
	for range linkAttempts {
		name, err := next()
		if err != nil {
			return "", err
		}
		err = create(name)
		if err == nil {
			return name, nil
		}
		if !errors.Is(err, unix.EEXIST) {
			return "", err
		}
	}
	return "", errNoFreeName
}
