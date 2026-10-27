package test

// w44_filing_tier_slot_contract_test.go: text contract for workbench#44.
//
// Work item #44 extends workbench#23's hard requirement "state the lane plus a
// one-line reason" to "lane + tier + reason": the filing proposal gains a
// mandatory tier determination beside the lane, and every grill round answers a
// tier proposal mechanically (autopilot, which is not scoped-capable, always
// answers full).
//
// The change is prompt material (skill fragments and workflow step bodies): no
// endpoint, verb, flag or protocol moves, so the only mechanical contract is the
// shipped text itself. This test pins the exact markers the four files must
// carry. It is red on the base tree (none of the marker sets existed there) and
// green only once the tier slot shipped. The qualitative half — that a real
// filing proposal actually answers the three slots, and a real grill round
// actually proposes a tier — cannot be machine-verified from text and is
// observed on real WIs, recorded in the capsule's code summary, the same way
// workbench#30 handled its non-machine-verifiable residual.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW44FilingTierSlotContract(t *testing.T) {
	root := v1Root(t)
	cases := []struct {
		rel  string
		want []string
	}{
		{
			filepath.Join("skills", "polyforge", "verbs", "file.md"),
			[]string{
				"Lane + reason",
				"Tier + reason",
				"Entry confirmation",
				"propose `scoped` ",
				"driver/daemon core",
				"gate-type",
				"cannot determine",
				"scoped-capable",
				"docs/contributing.md『Verification tiers』",
				"scoped-gates readiness",
			},
		},
		{
			filepath.Join("skills", "polyforge", "verbs", "execute-intent.md"),
			[]string{
				"Lane + reason",
				"Tier + reason",
				"Entry confirmation",
				"propose `scoped` ",
				"driver/daemon core",
				"gate-type",
				"cannot determine",
				"scoped-capable",
				"not scoped-capable",
				"docs/contributing.md『Verification tiers』",
				"scoped-gates readiness",
			},
		},
	}
	for _, c := range cases {
		b, err := os.ReadFile(filepath.Join(root, c.rel))
		if err != nil {
			t.Fatalf("workbench#44: %s is filing-skill text the tier slot must extend: %v", c.rel, err)
		}
		s := string(b)
		for _, want := range c.want {
			if !strings.Contains(s, want) {
				t.Errorf("workbench#44: %s must carry the tier-slot marker %q — before `pf file create` the filing proposal fills `Lane + reason`, `Tier + reason` (propose `scoped` only for a text/docs change confined to one repository and a small surface; `full` for a cross-module change, the driver/daemon core or a gate-type change; `cannot determine` → `full`) and, when the tier is scoped, `Entry confirmation` (the `pf doctor` `scoped-gates readiness` row or the clone check; not ready → the marker is inert and the ticket falls back to full; the contract is docs/contributing.md『Verification tiers』)", c.rel, want)
			}
		}
	}
}

func TestW44GrillTierProposalContract(t *testing.T) {
	root := v1Root(t)
	cases := []struct {
		rel  string
		want []string
	}{
		{
			filepath.Join("workflows", "feature-full", "steps", "grill.md"),
			[]string{
				"Tier proposal",
				"veto a downgrade",
				"scoped",
			},
		},
		{
			filepath.Join("workflows", "autopilot", "steps", "grill.md"),
			[]string{
				"Tier proposal",
				"always full",
				"not scoped-capable",
			},
		},
	}
	for _, c := range cases {
		b, err := os.ReadFile(filepath.Join(root, c.rel))
		if err != nil {
			t.Fatalf("workbench#44: %s is a grill-family step text the tier-proposal clause must reach: %v", c.rel, err)
		}
		s := string(b)
		for _, want := range c.want {
			if !strings.Contains(s, want) {
				t.Errorf("workbench#44: %s must carry the tier-proposal marker %q — every grill round (including the zero-question pass) states the verification tier proposal and records it in artifacts/grill-qa.md; the owner may veto a downgrade; autopilot is not scoped-capable, so its mechanically required answer is always full", c.rel, want)
			}
		}
	}
}
