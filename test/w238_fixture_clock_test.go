package test

// w238_fixture_clock_test.go (polyforge#238 AC2): the verify-scope e2e
// fixtures must carry distinct base commits without leaning on the wall
// clock.
//
// polyforge#85 fixed the w111 scenario to pass each fixture its OWN --base,
// but forced the docs fixture into a later wall-clock second with a sleep so
// a stale base would still be detectable. Two same-content fixtures built in
// one second share their base SHA, so a scenario that reused the previous
// fixture's base would silently pass; the sleep made the run depend on the
// host clock. The fix gives every fixture base its own commit second (the
// helper's clock), so the distinction holds even when the process inherits a
// pinned GIT_COMMITTER_DATE — the case this test pins.

import (
	"testing"
)

func TestW238E2EFixturesCarryDistinctBases(t *testing.T) {
	// A caller may run the suite under a pinned identity/date (CI, a git
	// hook, a bisect); the fixture must not let that collapse two fixtures
	// into one commit.
	t.Setenv("GIT_AUTHOR_DATE", "2026-01-02T03:04:05Z")
	t.Setenv("GIT_COMMITTER_DATE", "2026-01-02T03:04:05Z")
	files := map[string]string{
		"go.mod":     "module fixture.test/wi238\n\ngo 1.21\n",
		"lib/lib.go": "package lib\n\nfunc Value() int { return 1 }\n",
		"docs/x.md":  "# x\n",
	}
	first, base1 := w25e2eFixture(t, files)
	second, base2 := w25e2eFixture(t, files)
	if base1 == base2 {
		t.Fatalf("two same-content fixtures share base %s: a scenario cannot tell a stale --base from its own (first %s, second %s)",
			base1, first, second)
	}
}
