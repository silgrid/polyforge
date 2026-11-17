package test

// epic_lane_docs_contract_test.go (polyforge#117 u7, AC4): the epic
// lane's documents say the same thing as its code — concepts names the
// shipped lane and its flow, the workflow contract's target-branch
// section states the epic's own record (the checkout key, no target),
// the children snapshot, the running re-check, the resume reset and the
// one landed record, and docs/upgrade.md carries the section this
// change appends. The pinned sentences name no project and no lane but
// the epic's own.

import (
	"strings"
	"testing"
)

// TestEpicLaneDocsContract pins the epic lane's documents: the shipped
// concepts narrative, the reference's record contract and the upgrade
// note.
func TestEpicLaneDocsContract(t *testing.T) {
	concepts := w68Flat(t, "docs/concepts.md")
	for _, want := range []string{
		"the shipped lanes are **develop** — the",
		"**epic** — one work item per epic",
		"verify-children → sync → build → review →",
	} {
		if !strings.Contains(concepts, want) {
			t.Errorf("docs/concepts.md must state %q", want)
		}
	}

	ref := w68Flat(t, "docs/reference/workflow-contract.md")
	for _, want := range []string{
		"An epic's own record (polyforge#117)",
		"the record's `checkout` key and no `target`",
		"every reader keeps its default-branch logic",
		"`runtime-state/children.json`",
		"immutable while the epic runs",
		"the driver re-checks against aihub before every dispatch",
		"parks the workflow blocked on its own authority",
		"`runtime-state/epic-resume.json`",
		"one owner-class rewind to the step it names",
		"a `children` key carrying the claim's snapshot verbatim",
		"the ONE record the integrate pool takes",
	} {
		if !strings.Contains(ref, want) {
			t.Errorf("docs/reference/workflow-contract.md must state %q", want)
		}
	}

	upgrade := w68Flat(t, "docs/upgrade.md")
	for _, want := range []string{
		"## The epic lane (polyforge#117)",
	} {
		if !strings.Contains(upgrade, want) {
			t.Errorf("docs/upgrade.md must state %q", want)
		}
	}
}
