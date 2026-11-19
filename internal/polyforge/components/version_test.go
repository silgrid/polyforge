package components

// version_test.go — the version forms the layout accepts and the
// comparison rules the install obeys, authored test-first against the
// ticket's settled Q2 (public vX.Y.Z, internal vX.Y.Z-gmi.N, dev
// vX.Y.Z-dev.N; SemVer inside one channel, cross-channel never ordered)
// and polyforge#136's memory (a dev version is a pre-release of the
// next formal patch). Every expected value below comes from those
// rules, never from the implementation's own output.

import (
	"strings"
	"testing"
)

// TestVersionFormsAndChannels pins which strings are versions at all
// and which channel each names. A comparator or a validator that
// accepted any dotted string would let an arbitrary directory name
// become a release; one that mislabeled a channel would let a cross-
// channel switch pass as an upgrade.
func TestVersionFormsAndChannels(t *testing.T) {
	legal := []struct {
		in      string
		want    Version
		channel string
	}{
		{"v0.2.4", Version{0, 2, 4, ChannelPublic, 0}, ChannelPublic},
		{"v1.20.0", Version{1, 20, 0, ChannelPublic, 0}, ChannelPublic},
		{"v0.2.4-gmi.3", Version{0, 2, 4, ChannelInternal, 3}, ChannelInternal},
		{"v0.2.4-gmi.10", Version{0, 2, 4, ChannelInternal, 10}, ChannelInternal},
		{"v0.2.4-dev.12", Version{0, 2, 4, ChannelDev, 12}, ChannelDev},
	}
	for _, c := range legal {
		got, err := ParseVersion(c.in)
		if err != nil {
			t.Fatalf("ParseVersion(%q): unexpected error %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("ParseVersion(%q) = %+v, want %+v", c.in, got, c.want)
		}
		if got.Channel != c.channel {
			t.Errorf("ParseVersion(%q).Channel = %q, want %q", c.in, got.Channel, c.channel)
		}
		if got.String() != c.in {
			t.Errorf("String() of %+v = %q, want the canonical %q", got, got.String(), c.in)
		}
	}

	illegal := []string{
		"",               // nothing at all
		"0.2.4",          // no v
		"V0.2.4",         // capital V
		"v0.2",           // two parts
		"v0.2.3.4",       // four parts
		"v0.2.4-gmi",     // suffix without a number
		"v0.2.4-dev",     // suffix without a number
		"v0.2.4-x.1",     // unknown suffix
		"v0.2.4-gmi.-1",  // negative
		"v0.2.4-gmi.1.2", // a second dot
		" v0.2.4",        // padding
		"v0.2.4 ",        // padding
		"vx.y.z",         // letters
		"dev",            // an unstamped build's raw version
		// leading zeros are not SemVer (review r1#N5): two spellings of
		// one number would compare equal while naming two different
		// releases/<ver> directories.
		"v0.02.4",
		"v0.2.04",
		"v0.2.4-gmi.01",
		"v0.2.4-dev.00",
	}
	for _, in := range illegal {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) accepted a string of no settled form", in)
		}
	}
}

// TestVersionCompareSameChannel pins the ordering inside one channel:
// numeric fields (never string ordering — gmi.10 must outrank gmi.2),
// the suffix number inside the suffixed channels, and equality.
func TestVersionCompareSameChannel(t *testing.T) {
	ordered := []struct {
		low, high string
	}{
		{"v0.2.4", "v0.2.10"}, // "10" < "2" as strings: the trap
		{"v0.2.4", "v0.3.0"},
		{"v0.2.9", "v1.0.0"},
		{"v0.2.4-gmi.2", "v0.2.4-gmi.10"},
		{"v0.2.4-gmi.9", "v0.2.5-gmi.1"},
		{"v0.2.4-dev.2", "v0.2.4-dev.10"},
		{"v0.2.4-dev.99", "v0.2.5-dev.1"},
	}
	for _, c := range ordered {
		l, err := ParseVersion(c.low)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", c.low, err)
		}
		h, err := ParseVersion(c.high)
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", c.high, err)
		}
		if got, err := l.Compare(h); err != nil || got != -1 {
			t.Errorf("%s.Compare(%s) = (%d, %v), want (-1, nil)", c.low, c.high, got, err)
		}
		if got, err := h.Compare(l); err != nil || got != 1 {
			t.Errorf("%s.Compare(%s) = (%d, %v), want (1, nil)", c.high, c.low, got, err)
		}
	}
	equal := [][2]string{
		{"v0.2.4", "v0.2.4"},
		{"v0.2.4-gmi.3", "v0.2.4-gmi.3"},
		{"v0.2.4-dev.1", "v0.2.4-dev.1"},
	}
	for _, c := range equal {
		a, err := ParseVersion(c[0])
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", c[0], err)
		}
		b, err := ParseVersion(c[1])
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", c[1], err)
		}
		if got, err := a.Compare(b); err != nil || got != 0 {
			t.Errorf("%s.Compare(%s) = (%d, %v), want (0, nil)", c[0], c[1], got, err)
		}
	}
}

// TestVersionCompareCrossChannelRefused pins that versions of two
// channels never order — even the same core with two suffixes, even a
// formally NEWER public version against an OLDER internal one. The
// install must refuse an implicit cross-channel switch, so the
// comparator refusing the ordering is what makes that refusal one
// code path instead of a caller-side special case.
func TestVersionCompareCrossChannelRefused(t *testing.T) {
	pairs := [][2]string{
		{"v0.2.5", "v0.2.4-gmi.1"},       // newer public vs older internal
		{"v0.2.4", "v0.2.4-gmi.1"},       // same core, other channel
		{"v0.2.4-gmi.1", "v0.2.4-dev.1"}, // internal vs dev
		{"v0.3.0", "v0.2.4-dev.1"},       // newer public vs older dev
	}
	for _, c := range pairs {
		a, err := ParseVersion(c[0])
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", c[0], err)
		}
		b, err := ParseVersion(c[1])
		if err != nil {
			t.Fatalf("ParseVersion(%q): %v", c[1], err)
		}
		got, err := a.Compare(b)
		if err == nil {
			t.Errorf("%s.Compare(%s) = %d, want an error (cross-channel comparison is never ordered)", c[0], c[1], got)
			continue
		}
		if !strings.Contains(err.Error(), a.Channel) || !strings.Contains(err.Error(), b.Channel) {
			t.Errorf("%s.Compare(%s) error = %q, want it to name both channels (%q, %q)", c[0], c[1], err, a.Channel, b.Channel)
		}
	}
}

// TestBaseSatisfies pins the minimum-base rule: the core decides. A
// newer core satisfies; the same core satisfies itself whatever the
// base's channel (the image's base and seed are one build, so a
// -dev.N base of that core carries the code); an older core never
// does. An unparseable version on either side is an error, not a
// satisfied check — an unstamped base ("dev") must not silently pass.
func TestBaseSatisfies(t *testing.T) {
	yes := [][2]string{
		{"v0.2.5", "v0.2.4"},
		{"v0.2.4", "v0.2.4"},
		{"v0.2.4-dev.1", "v0.2.4"}, // same core, dev base: carries the code
		{"v0.2.4-gmi.1", "v0.2.3"},
		{"v1.0.0", "v0.2.4"},
	}
	for _, c := range yes {
		got, err := BaseSatisfies(c[0], c[1])
		if err != nil {
			t.Fatalf("BaseSatisfies(%q, %q): unexpected error %v", c[0], c[1], err)
		}
		if !got {
			t.Errorf("BaseSatisfies(%q, %q) = false, want true", c[0], c[1])
		}
	}
	no := [][2]string{
		{"v0.2.3", "v0.2.4"},
		{"v0.2.4-dev.1", "v0.2.5"}, // same channel, older core
		{"v0.1.9", "v0.2.0"},
	}
	for _, c := range no {
		got, err := BaseSatisfies(c[0], c[1])
		if err != nil {
			t.Fatalf("BaseSatisfies(%q, %q): unexpected error %v", c[0], c[1], err)
		}
		if got {
			t.Errorf("BaseSatisfies(%q, %q) = true, want false (the core is older)", c[0], c[1])
		}
	}
	bad := [][2]string{
		{"dev", "v0.2.4"}, // an unstamped base never satisfies
		{"v0.2.4", "dev"}, // neither does an unstamped requirement
		{"v0.2.4", ""},
		{"", "v0.2.4"},
		{"v0.2", "v0.2.4"},
	}
	for _, c := range bad {
		got, err := BaseSatisfies(c[0], c[1])
		if err == nil {
			t.Errorf("BaseSatisfies(%q, %q) = %v, want an error (an unparseable version is never satisfied)", c[0], c[1], got)
		}
	}
}
