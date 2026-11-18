package sockat

import (
	"crypto/rand"
	"errors"
)

// linkPrefix is what the symlink name starts with, so a link left behind by a
// process that died is recognisable as sockat's.
const linkPrefix = "sockat."

// linkRandomLen is the length of the random part of the symlink name.
const linkRandomLen = 10

// linkAttempts is how many names createLink tries before giving up. Each try
// is a fresh random name from 62^10 possibilities, so exhausting this means
// something other than bad luck (for example a base that refuses every name).
const linkAttempts = 16

// errNoFreeName is returned when createLink ran out of names.
var errNoFreeName = errors.New("could not find a free name for a temporary symlink")

// randomName returns "sockat." followed by linkRandomLen random letters and
// digits from the system's secure generator (crypto/rand).
func randomName() (string, error) {
	const alphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	var random [linkRandomLen]byte
	if _, err := rand.Read(random[:]); err != nil {
		return "", err
	}
	name := []byte(linkPrefix)
	// 256 is not a multiple of 62, so this is slightly biased; the name only
	// has to be hard to collide with, not uniform.
	for _, b := range random {
		name = append(name, alphabet[int(b)%len(alphabet)])
	}
	return string(name), nil
}
