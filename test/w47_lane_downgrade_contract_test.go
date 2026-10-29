package test

// w47_lane_downgrade_contract_test.go: text contract for.
//
// Work item #47 adds a down-lane mechanism: the filing skill judges a ticket on
// two axes (expected diff size and lane risk surface) and prefers the lighter
// chore/autopilot lanes for a small, low-risk ticket, and the feature-full grill
// answers a mandatory per-round Lane proposal (keep feature-full, or down-lane
// via re-file). Work on the "never unattended" list is the exception: it is a
// high lane risk surface and is never down-laned off feature-full, whatever its
// diff size.
//
// The change is prompt material (skill fragments and a workflow step body): no
// endpoint, verb, flag or protocol moves, so the only mechanical contract is the
// shipped text itself. This test pins the exact markers the three files must
// carry. It is red on the base tree (none of the marker sets existed there) and
// green only once the down-lane mechanism shipped. The qualitative half — that a
// real filing proposal records a down-lane judgment and a real grill round asks
// the owner to re-file or waive — cannot be machine-verified from text and is
// observed on real WIs, recorded in the capsule's code summary, the same way
// and #44 handled their non-machine-verifiable residual.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW47LaneDowngradeContract(t *testing.T) {
	root := v1Root(t)
	cases := []struct {
		rel  string
		want []string
	}{
		{
			filepath.Join("skills", "polyforge", "verbs", "file.md"),
			[]string{
				"Down-lane check",
				"expected diff size",
				"lane risk surface",
				"never down-laned",
			},
		},
		{
			filepath.Join("skills", "polyforge", "verbs", "execute-intent.md"),
			[]string{
				"Down-lane check",
				"expected diff size",
				"lane risk surface",
			},
		},
		{
			filepath.Join("workflows", "feature-full", "steps", "grill.md"),
			[]string{
				"Lane proposal",
				"expected diff size",
				"lane risk surface",
				"never unattended",
				"artifacts/grill-qa.md",
				"re-file",
			},
		},
	}
	for _, c := range cases {
		b, err := os.ReadFile(filepath.Join(root, c.rel))
		if err != nil {
			t.Fatalf("%s is a file the down-lane mechanism must extend: %v", c.rel, err)
		}
		s := string(b)
		for _, want := range c.want {
			if !strings.Contains(s, want) {
				t.Errorf("%s must carry the down-lane marker %q — the filing skill defines the `Down-lane check` on `expected diff size` and `lane risk surface` (a small, low-risk ticket prefers `chore`/`autopilot` over `feature-full`; work on the `never unattended` list is a high `lane risk surface` and is `never down-laned`, whatever its diff size), and the feature-full grill answers a per-round `Lane proposal` (down-lane by re-file, recorded in `artifacts/grill-qa.md`; the `never unattended` list gets no down-lane proposal)", c.rel, want)
			}
		}
	}
}
