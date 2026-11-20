package sockat

import "strconv"

// Spelling is what the spelling of a bind is good for afterwards: the
// spelling the kernel keeps for the socket (the listener's Addr(), and the
// peer's RemoteAddr()), which code may later unlink or look up (SPEC
// "Spelling properties"). Branch on it rather than on the diagnostics.
//
// It compares with ==. The fields other than Kind mean something only for
// Kind == Linked, and are false or zero otherwise.
type Spelling struct {
	Kind SpellingKind
	// Removable: unlinking the spelling removes the socket file (link-dir:
	// the E on the way is followed). False for link-file: the unlink removes
	// the E, the last component, and the file stays.
	Removable bool
	// Until is how long the spelling can be followed.
	Until Until
	// UsableCrossUID: a peer of another uid can follow the spelling.
	UsableCrossUID bool
	// UsableWithPrivateTmp: a peer can follow the spelling even when either
	// side has a /tmp of its own (systemd's PrivateTmp and the like). A peer
	// in another mount namespace altogether (a container) is not covered.
	UsableWithPrivateTmp bool
}

// SpellingKind is the kind of a Spelling.
type SpellingKind int

const (
	// Original: the path itself, absolute.
	Original SpellingKind = iota + 1
	// CwdRelative: the path itself, relative: after a change of the cwd it
	// names another place (the net package's own risk, reported all the
	// same).
	CwdRelative
	// Linked: through B, D and E, naming the same place as the path.
	Linked
	// Dangling: through an E already removed; it names nothing.
	Dangling
)

// String is the SPEC's name of the kind ("Original", "CwdRelative",
// "Linked", "Dangling").
func (k SpellingKind) String() string {
	switch k {
	case Original:
		return "Original"
	case CwdRelative:
		return "CwdRelative"
	case Linked:
		return "Linked"
	case Dangling:
		return "Dangling"
	}
	return "SpellingKind(" + strconv.Itoa(int(k)) + ")"
}

// Until is how long a Linked spelling can be followed.
type Until int

const (
	// UntilProcess: until the process ends (the spelling goes through
	// /proc/<pid>/fd).
	UntilProcess Until = iota + 1
	// UntilCleanup: until the E is cleaned up (SocketFile's Cleanup or
	// Remove); B and D stay until the system's cleaning of its temporary
	// directories after the process has ended.
	UntilCleanup
)

// String is the SPEC's name of the value ("Process", "Cleanup").
func (u Until) String() string {
	switch u {
	case UntilProcess:
		return "Process"
	case UntilCleanup:
		return "Cleanup"
	}
	return "Until(" + strconv.Itoa(int(u)) + ")"
}
