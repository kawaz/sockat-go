package sockat

import (
	"crypto/rand"
	"errors"
	"os"
	"syscall"
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

// createLink creates a symlink under base pointing to target, named by
// nextName (called once per try; its error ends the attempt), and returns its
// path.
//
// symlink(2) fails with EEXIST instead of replacing an existing entry, which
// is the O_EXCL of symlinks: the link that is created is always one nobody
// else had, and a name already taken (by anyone) is skipped for the next one.
// Any other error ends the attempt.
func createLink(base, target string, nextName func() (string, error)) (string, error) {
	for range linkAttempts {
		name, err := nextName()
		if err != nil {
			return "", err
		}
		link := underBase(base, name)
		err = os.Symlink(target, link)
		if err == nil {
			return link, nil
		}
		if !errors.Is(err, syscall.EEXIST) {
			return "", err
		}
	}
	return "", errNoFreeName
}

// underBase spells the entry name of the directory base as base followed by
// it, without cleaning base (a lexical clean would resolve ".." differently
// from the kernel where base goes through a symlink).
func underBase(base, name string) string {
	for len(base) > 1 && base[len(base)-1] == '/' {
		base = base[:len(base)-1]
	}
	if base == "/" {
		return "/" + name
	}
	return base + "/" + name
}
