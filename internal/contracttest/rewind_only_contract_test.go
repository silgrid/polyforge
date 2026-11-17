package test

// carries: docs/reference/workflow-contract.md, docs/bundles.md, docs/reference/config.md, internal/polyforge/cli/config_yaml_edit.go, workflows/_schema/workflow-1.schema.json, internal/contracttest/rewind_only_contract_test.go

// rewind_only_contract_test.go (polyforge#116, AC4): the `rewind_only`
// step key is one contract across its four faces — the lane schema's
// step items carry it as a boolean, the workflow contract's step key
// table carries its row, and the three documents' prose says the same
// thing: the first round records the step skipped without a dispatch,
// any rewind that covers it (a gate FAIL's or an owner's) runs it, a
// project skip wins over it, the loader refuses a declaration no gate's
// range covers, and the absolute "a step not declared skippable always
// runs" wording is gone from the documents and the CLI's refusal
// message that carried it. The
// pinned sentences name no lane and no project.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pf116DocRead answers a repo-relative file's body (the contract package
// runs two levels below the module root).
func pf116DocRead(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v1Root(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatalf("polyforge#116: read %s: %v", rel, err)
	}
	return string(b)
}

// pf116Wants are the sentences each file must carry (flattened: a
// sentence the paragraph wraps across lines must still pin).
var pf116Wants = map[string][]string{
	"docs/reference/workflow-contract.md": {
		"| `rewind_only` |",
		"The first round records it skipped without a dispatch (a `step_skipped` event, no model session, no artifacts",
		"once any rewind that covers it resets it to pending it runs, on that round and on every later round another rewind resets it to",
		"a gate FAIL's rewind (automatic or `--per-owner`) and an owner's rewind record the same archives on the step's own attempts",
		"A project skip of the step (a `skippable` it declares) wins over the declaration",
		"The loader refuses a `rewind_only` step no gate's `[on_fail target, gate)` range covers (`WFC-112`, naming the step)",
		"When every gate covering it is switched off for a project, the step does not run on that project's work items",
		"A step that runs only after a rewind.",
		"the first round's pass records it `skipped`",
		"every later rewind that covers it again runs it once more",
		"a switched-off step is skipped on every round, the rewind or not",
		"a later step or gate that reads its artifacts in the first round finds none",
	},
	"docs/bundles.md": {
		"A step not declaring it runs on every round of the work item",
	},
	"docs/reference/config.md": {
		"a step not declared skippable runs on every round of the work item",
		"naming `skippable: true` and that such a step cannot be switched off",
	},
	"internal/polyforge/cli/config_yaml_edit.go": {
		"a step its lane does not declare skippable cannot be switched off",
	},
}

// pf116Banned is the absolute wording the rewound step key retired.
var pf116Banned = map[string][]string{
	"docs/bundles.md": {
		"A step not declaring it always runs",
	},
	"docs/reference/config.md": {
		"skippable always runs",
		"such a step always runs;",
	},
	"internal/polyforge/cli/config_yaml_edit.go": {
		"skippable always runs",
	},
}

// TestPF116RewindOnlyContract pins the key's four faces.
func TestPF116RewindOnlyContract(t *testing.T) {
	for rel, wants := range pf116Wants {
		flat := strings.Join(strings.Fields(pf116DocRead(t, rel)), " ")
		for _, w := range wants {
			if !strings.Contains(flat, w) {
				t.Errorf("polyforge#116: %s must carry %q (flattened), it does not", rel, w)
			}
		}
	}
	for rel, banned := range pf116Banned {
		flat := strings.Join(strings.Fields(pf116DocRead(t, rel)), " ")
		for _, b := range banned {
			if strings.Contains(flat, b) {
				t.Errorf("polyforge#116: %s must no longer carry %q, it does", rel, b)
			}
		}
	}

	// The lane schema's step items carry the key as a boolean.
	var schema struct {
		Properties map[string]struct {
			Items struct {
				Properties map[string]struct {
					Type string `json:"type"`
				} `json:"properties"`
			} `json:"items"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(pf116DocRead(t, "workflows/_schema/workflow-1.schema.json")), &schema); err != nil {
		t.Fatalf("polyforge#116: the lane schema is not JSON: %v", err)
	}
	if p, ok := schema.Properties["steps"].Items.Properties["rewind_only"]; !ok || p.Type != "boolean" {
		t.Errorf("polyforge#116: the lane schema's step rewind_only: %+v (present %v), want {type: boolean}", p, ok)
	}
}
