package test

// w30_filing_deepening_contract_test.go: text contract for.
//
// Work item #30 put the four-point "deepening check" (expert gap / raw
// material / methods and tools / external oracle) into the filing skill and
// gave the grill-family steps the standing to reject a non-oracle acceptance.
// The change is prompt material (skill fragments and workflow step bodies):
// no endpoint, verb, flag or protocol moves, so the only mechanical contract
// is the shipped text itself.
//
// This test pins the markers the two filing fragments and the two grill step
// texts must carry. It is red on the base tree (none of the marker sets
// existed there) and green only once the deepening check shipped. The
// qualitative half — that a deepened ticket's oracle maps to a falsifiable
// verification check — is walked through in the capsule's code summary and
// observed on real WIs, the same way handled its
// non-machine-verifiable residual.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW30FilingDeepeningContract(t *testing.T) {
	root := v1Root(t)
	cases := []struct {
		rel  string
		want []string
	}{
		{
			filepath.Join("skills", "polyforge", "verbs", "execute-intent.md"),
			[]string{
				"Deepening check",
				"expert gap",
				"raw material",
				"methods and tools",
				"external oracle",
				"gap and oracle",
				"light ticket",
				"falsifiable",
			},
		},
		{
			filepath.Join("skills", "polyforge", "verbs", "file.md"),
			[]string{
				"Deepening check",
				"gap and oracle",
				"external oracle",
				"falsifiable",
			},
		},
	}
	for _, c := range cases {
		b, err := os.ReadFile(filepath.Join(root, c.rel))
		if err != nil {
			t.Fatalf("%s is filing-skill text the deepening check must extend: %v", c.rel, err)
		}
		s := string(b)
		for _, want := range c.want {
			if !strings.Contains(s, want) {
				t.Errorf("%s must carry the deepening marker %q — before filing an execution ticket the four points (expert gap / raw material / methods and tools / external oracle) land in the ticket `content` as the gap and oracle sections, with acceptance that is external oracle-ized and falsifiable", c.rel, want)
			}
		}
	}
}
