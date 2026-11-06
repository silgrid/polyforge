package workflow_test

// v3_removed_keys_test.go: a lane of an earlier contract still declaring
// the keys contract 3 removed — the lane property build_step, the step
// keys executor, action, with, checks, sweep and evidence, the facts
// input — is refused in one load for its version and for each of them
// (an unknown key: the rewrite hints of the keys earlier contracts removed
// left with workflow contract 4's stage C3,).

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// TestV3PreviousContractLaneIsRefusedOnEveryCount (design §6.2/§6.3, the
// drain): a user lane of contract 2 in the v1 shape — a build_step, daemon
// steps with their actions and with, a check, a sweep, notarized evidence,
// a facts input — is refused in one load naming its version (WFC-101, the
// upgrade section) and every removed key with its rewrite, so the owner
// sees the whole rewrite at once; moving the number alone does not load it.
func TestV3PreviousContractLaneIsRefusedOnEveryCount(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "old")
	write(t, filepath.Join(dir, workflow.WorkflowFile), fmt.Sprintf(`contract: %d
name: old
description: a user lane of the previous contract
max_rewinds: 2
build_step: build
steps:
  - id: build
    checks: [e2e-carries]
    evidence: { mode: notarized }
  - id: review
    gate: true
    on_fail: { target: build }
    sweep: { target: build }
    inputs: [ { facts: build-facts } ]
  - { id: verification, executor: daemon, action: verify, gate: true, on_fail: { target: build }, with: { tier: light } }
`, workflow.ContractVersion-1))
	write(t, filepath.Join(dir, "steps", "build.md"), workflowtest.Definition("build"))
	write(t, filepath.Join(dir, "steps", "review.md"), workflowtest.Definition("review"))
	write(t, filepath.Join(dir, "steps", "verification.md"), workflowtest.Definition("verification"))
	_, errs := workflow.Load(dir)
	got := map[string]string{}
	for _, e := range errs {
		got[e.Path] = e.Code
	}
	want := map[string]string{
		"contract":                        workflow.CodeContractVersion,
		"build_step":                      workflow.CodeUnknownKey,
		"steps[0](build).checks":          workflow.CodeUnknownKey,
		"steps[0](build).evidence":        workflow.CodeUnknownKey,
		"steps[1](review).sweep":          workflow.CodeUnknownKey,
		"steps[1](review).inputs":         workflow.CodeUnknownKey,
		"steps[2](verification).executor": workflow.CodeUnknownKey,
		"steps[2](verification).action":   workflow.CodeUnknownKey,
		"steps[2](verification).with":     workflow.CodeUnknownKey,
	}
	if len(errs) != len(want) {
		t.Fatalf("defects %v\nwant exactly %v", errs, want)
	}
	for p, code := range want {
		if got[p] != code {
			t.Errorf("%s: got %q, want %s (all: %v)", p, got[p], code, errs)
		}
	}
	for _, e := range errs {
		if e.Code == workflow.CodeContractVersion && (!strings.Contains(e.Msg, fmt.Sprintf("targets contract %d,", workflow.ContractVersion-1)) || !strings.Contains(e.Msg, workflow.UpgradeSection)) {
			t.Errorf("WFC-101 names neither the version nor the upgrade section: %s", e.Msg)
		}
	}
}
