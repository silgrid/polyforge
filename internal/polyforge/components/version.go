package components

// version.go — the version forms the components layout accepts and the
// comparison rules the install transaction obeys (polyforge#162). The
// settled forms (ticket Q2) are the public formal vX.Y.Z, the internal
// vX.Y.Z-gmi.N and the development vX.Y.Z-dev.N (polyforge#136: a dev
// version is a pre-release of the next formal patch). Versions compare
// by SemVer inside one channel ONLY: an implicit cross-channel
// comparison — public against internal, either against dev, or the
// same core in two channels — is refused, never ordered.

// The channels a version's suffix names. ChannelPublic is the formal
// line (no suffix); ChannelInternal is the -gmi.N line; ChannelDev is
// the -dev.N line.
const (
	ChannelPublic   = "public"
	ChannelInternal = "internal"
	ChannelDev      = "dev"
)

// Version is one parsed version of the accepted forms.
type Version struct {
	Major   int
	Minor   int
	Patch   int
	Channel string // one of the Channel* constants
	N       int    // the suffix number; 0 on the formal line
}

// ParseVersion parses one accepted version string. Anything else — a
// missing v, a two-part version, a fourth number, an unknown suffix, a
// negative or padded suffix — is an error naming the string.
func ParseVersion(s string) (Version, error) { return Version{}, nil }

// String rebuilds the canonical form of the parsed version.
func (v Version) String() string { return "" }

// Compare orders two versions of the SAME channel by SemVer (the
// suffix number N inside the gmi and dev channels). Versions of
// different channels never order: the answer is an error, whatever the
// numbers — an implicit cross-channel switch is exactly what the
// install transaction must refuse, so it is refused here too.
func (v Version) Compare(other Version) (int, error) { return 0, nil }

// BaseSatisfies reports whether a base (bootstrap pf) version meets a
// manifest's minimum base requirement. The rule is the core version:
// a newer core satisfies any older minimum, and the same core
// satisfies itself whatever the base's channel — the seed and the
// base of one image are the same build, so a -dev.N base of the same
// core carries the code that core names. An older core, or a version
// string of no accepted form (either side), does not satisfy: the
// answer is false or an error, never a guess.
func BaseSatisfies(base, minBase string) (bool, error) { return false, nil }
