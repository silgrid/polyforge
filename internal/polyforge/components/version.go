package components

// version.go — the version forms the components layout accepts and the
// comparison rules the install transaction obeys (polyforge#162). The
// settled forms (ticket Q2) are the public formal vX.Y.Z, the internal
// vX.Y.Z-gmi.N and the development vX.Y.Z-dev.N (polyforge#136: a dev
// version is a pre-release of the next formal patch). Versions compare
// by SemVer inside one channel ONLY: an implicit cross-channel
// comparison — public against internal, either against dev, or the
// same core in two channels — is refused, never ordered.

import (
	"fmt"
	"regexp"
	"strconv"
)

// The channels a version's suffix names. ChannelPublic is the formal
// line (no suffix); ChannelInternal is the -gmi.N line; ChannelDev is
// the -dev.N line.
const (
	ChannelPublic   = "public"
	ChannelInternal = "internal"
	ChannelDev      = "dev"
)

// versionRe is the one accepted shape: v, three decimal fields, and
// either no suffix (public) or one of the two settled suffixed
// channels with its decimal number.
var versionRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(?:-(gmi|dev)\.(\d+))?$`)

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
func ParseVersion(s string) (Version, error) {
	m := versionRe.FindStringSubmatch(s)
	if m == nil {
		return Version{}, fmt.Errorf("version %q is of no accepted form (vX.Y.Z, vX.Y.Z-gmi.N or vX.Y.Z-dev.N)", s)
	}
	major, err := strconv.Atoi(m[1])
	if err != nil {
		return Version{}, fmt.Errorf("version %q: major field: %w", s, err)
	}
	minor, err := strconv.Atoi(m[2])
	if err != nil {
		return Version{}, fmt.Errorf("version %q: minor field: %w", s, err)
	}
	patch, err := strconv.Atoi(m[3])
	if err != nil {
		return Version{}, fmt.Errorf("version %q: patch field: %w", s, err)
	}
	v := Version{Major: major, Minor: minor, Patch: patch, Channel: ChannelPublic}
	if m[4] != "" {
		n, err := strconv.Atoi(m[5])
		if err != nil {
			return Version{}, fmt.Errorf("version %q: suffix number: %w", s, err)
		}
		v.N = n
		switch m[4] {
		case "gmi":
			v.Channel = ChannelInternal
		case "dev":
			v.Channel = ChannelDev
		}
	}
	return v, nil
}

// String rebuilds the canonical form of the parsed version.
func (v Version) String() string {
	s := fmt.Sprintf("v%d.%d.%d", v.Major, v.Minor, v.Patch)
	switch v.Channel {
	case ChannelInternal:
		s += fmt.Sprintf("-gmi.%d", v.N)
	case ChannelDev:
		s += fmt.Sprintf("-dev.%d", v.N)
	}
	return s
}

// Compare orders two versions of the SAME channel by SemVer (the
// suffix number N inside the gmi and dev channels). Versions of
// different channels never order: the answer is an error, whatever the
// numbers — an implicit cross-channel switch is exactly what the
// install transaction must refuse, so it is refused here too.
func (v Version) Compare(other Version) (int, error) {
	if v.Channel != other.Channel {
		return 0, fmt.Errorf("version %s (%s channel) and %s (%s channel) are of different channels and never order: an implicit cross-channel switch is refused",
			v.String(), v.Channel, other.String(), other.Channel)
	}
	for _, f := range [][2]int{
		{v.Major, other.Major},
		{v.Minor, other.Minor},
		{v.Patch, other.Patch},
		{v.N, other.N},
	} {
		if f[0] < f[1] {
			return -1, nil
		}
		if f[0] > f[1] {
			return 1, nil
		}
	}
	return 0, nil
}

// BaseSatisfies reports whether a base (bootstrap pf) version meets a
// manifest's minimum base requirement. The rule is the core version:
// a newer core satisfies any older minimum, and the same core
// satisfies itself whatever the base's channel — the seed and the
// base of one image are the same build, so a -dev.N base of the same
// core carries the code that core names. An older core, or a version
// string of no accepted form (either side), does not satisfy: the
// answer is false or an error, never a guess.
func BaseSatisfies(base, minBase string) (bool, error) {
	b, err := ParseVersion(base)
	if err != nil {
		return false, fmt.Errorf("base version: %w", err)
	}
	m, err := ParseVersion(minBase)
	if err != nil {
		return false, fmt.Errorf("minimum base requirement: %w", err)
	}
	for _, f := range [][2]int{
		{b.Major, m.Major},
		{b.Minor, m.Minor},
		{b.Patch, m.Patch},
	} {
		if f[0] < f[1] {
			return false, nil
		}
		if f[0] > f[1] {
			return true, nil
		}
	}
	return true, nil
}
