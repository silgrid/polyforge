package test

// w205_filing_acceptance_carrier_contract_test.go: text contract for.
//
// Work item #205 adds a fourth mandatory slot to the filing proposal,
// `Acceptance carrier`: every acceptance criterion is written
// "<criterion> — carried by <step id>", the step id taken from the chosen
// lane's step table; a criterion no step carries either drops to the evidence
// that graph actually runs, proposed to the owner as an explicit waiver, or
// the ticket moves to a lane that carries it. It also aligns the two rules that
// pulled in opposite directions: the external-oracle clause now requires the
// oracle to be carried by a step of the chosen lane's step table, and the
// Down-lane check never down-lanes onto a lane that carries no step for the
// acceptance criteria.
//
// The change is prompt material (skill fragments): no endpoint, verb, flag or
// protocol moves, so the only mechanical contract is the shipped text itself.
// This test pins the exact markers both files must carry. It is red on the
// base tree (none of the markers existed there) and green only once the slot
// shipped. The qualitative half — that a real filing proposal actually names a
// carrier step for every criterion — cannot be machine-verified from text and
// is observed on real WIs, the same way and handled
// their residual.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW205FilingAcceptanceCarrierContract(t *testing.T) {
	root := v1Root(t)
	cases := []struct {
		rel  string
		want []string
	}{
		{
			filepath.Join("skills", "polyforge", "verbs", "file.md"),
			[]string{
				"four mandatory",
				"Lane + reason",
				"Tier + reason",
				"Entry confirmation",
				"Acceptance carrier",
				"carried by <step id>",
				"step table",
				"explicit waiver",
				"never down-lanes onto a lane",
			},
		},
		{
			filepath.Join("skills", "polyforge", "verbs", "execute-intent.md"),
			[]string{
				"four mandatory",
				"Lane + reason",
				"Tier + reason",
				"Entry confirmation",
				"Acceptance carrier",
				"carried by <step id>",
				"step table",
				"explicit waiver",
				"never down-lanes onto a lane",
				"carried by a step of the chosen lane's step table",
			},
		},
	}
	for _, c := range cases {
		b, err := os.ReadFile(filepath.Join(root, c.rel))
		if err != nil {
			t.Fatalf("%s is filing-skill text the acceptance-carrier slot must extend: %v", c.rel, err)
		}
		s := string(b)
		for _, want := range c.want {
			if !strings.Contains(s, want) {
				t.Errorf("%s must carry the acceptance-carrier marker %q — the filing proposal fills a fourth mandatory slot `Acceptance carrier` (every criterion written \"<criterion> — carried by <step id>\" from the chosen lane's step table; an uncarried criterion becomes an explicit waiver proposed to the owner or moves the ticket to a lane that carries it), the external oracle must be carried by a step of the chosen lane's step table, and the Down-lane check never down-lanes onto a lane whose step table carries no step for the acceptance criteria", c.rel, want)
			}
		}
	}

	// The oracle clause itself must carry the carrier requirement: the marker
	// has to sit in the external-oracle paragraph, not anywhere in the file.
	b, err := os.ReadFile(filepath.Join(root, "skills", "polyforge", "verbs", "execute-intent.md"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	i := strings.Index(s, "**external oracle**")
	if i < 0 {
		t.Fatalf("execute-intent.md lost its **external oracle** bullet")
	}
	end := strings.Index(s[i:], "Points ③ and ④")
	if end < 0 {
		t.Fatalf("execute-intent.md external-oracle bullet lost its closing sentence")
	}
	if !strings.Contains(s[i:i+end], "carried by a step of the chosen lane's step table") {
		t.Errorf("the execute-intent.md external-oracle bullet must require the oracle to be carried by a step of the chosen lane's step table (the lane-agnostic half of the oracle/down-lane conflict)")
	}
}
