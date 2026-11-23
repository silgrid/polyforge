// carries: docs/bundles.md, docs/concepts.md, docs/reference/commands.md, docs/upgrade.md, workflows/develop/README.md, internal/contracttest/w241_handoff_docs_contract_test.go

package test

// w241_handoff_docs_contract_test.go (polyforge#241): the documents that
// describe what the change altered state the mechanical facts gate — the
// `facts_proof` lane key and the `facts.py handoff` summary the daemon
// reads at a build's done: the key's shape, the refusal returneing every
// hit, the develop README's build entry, and the upgrade note. Whitespace
// flattened, so a rewrap keeps the sentences pinned; the key table itself
// is pinned by the contract registry test under internal/common/workflow.

import (
	"strings"
	"testing"
)

func TestW241HandoffDocsContract(t *testing.T) {
	for rel, wants := range map[string][]string{
		"docs/bundles.md": {
			"`facts_proof:`",
			"the mechanical facts summary `facts.py handoff` writes",
		},
		"docs/concepts.md": {
			"develop's `build` does (`facts_proof`)",
			"the mechanical facts summary it writes lists a deterministic hit",
		},
		"docs/reference/commands.md": {
			"`done` while the lane declares `facts_proof`",
			"the refusal returns each hit the result lists",
		},
		"workflows/develop/README.md": {
			"The mechanical facts gate (`facts_proof`, `facts.py handoff`)",
			"refuses `done` while the result lists a deterministic hit",
		},
		"docs/upgrade.md": {
			"polyforge#241",
			"`facts_proof`",
		},
	} {
		flat := w68Flat(t, rel)
		for _, want := range wants {
			if !strings.Contains(flat, strings.Join(strings.Fields(want), " ")) {
				t.Errorf("%s must state %q", rel, want)
			}
		}
	}
}
