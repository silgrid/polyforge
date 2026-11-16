package test

// carries: docs/reference/workflow-contract.md, docs/design/polyforge-vnext/v2-converge.md, internal/common/capsule/capsule.go

// w80_target_branch_contract_test.go (polyforge#80, AC5): the target
// branch's two documents, pinned sentence by sentence — the capsule
// fixed-file paragraph of docs/reference/workflow-contract.md (beside
// the skipped-step set's and the step modes') and the capsule layout
// table's new row in docs/design/polyforge-vnext/v2-converge.md (E.5),
// which the w256 Han bar holds to English in place.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// w80DocPath is the repository root (this package's cwd is the repo's
// test/ dir).
func w80DocRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("repo root: %v", err)
	}
	return root
}

// w80DocRead answers a repo-relative file's body.
func w80DocRead(t *testing.T, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(w80DocRoot(t), rel))
	if err != nil {
		t.Fatalf("polyforge#80: read %s: %v", rel, err)
	}
	return string(raw)
}

// w80Pins are the sentences the two documents must carry verbatim: each
// names one rule of the fixed target branch (the ticket's own wording
// for AC5), and the workflow-contract paragraph's tokens the other
// contract tests hold the lane key's tables to.
var w80ContractPins = []string{
	"**A sub-ticket's target branch (polyforge#80).**",
	"the first claim fixes it into `runtime-state/target-branch.json`",
	"the first claim writes it, a re-claim or `pf resume` keeps it, and the repo checkout and the carrier restore read the file, never the lane",
	"BEFORE the task repos are cloned",
	"from the default branch tip with a non-forced push",
	"a push another claim won loses to the remote's copy",
	"is refused naming why (`DAEMON-507`, the blocked note carries the reason), never a silent fall-back to the default branch",
	"a parent whose lane declares none leaves the sub-ticket untouched",
}

var w80ConvergePins = []string{
	"| `runtime-state/target-branch.json` |",
	"the work item's fixed target branch (`{\"target\", \"parent\"}`",
	"the daemon creates the branch on each origin from the default branch tip with a non-forced push when the remote does not carry it",
}

// TestW80TargetBranchDocsContract pins both documents: every sentence of
// the paragraph set and the row above must survive byte-for-byte, and
// the capsule constants' file must name the path (the layout table's
// row and the constant are one fact, e5's own rule).
func TestW80TargetBranchDocsContract(t *testing.T) {
	contract := w80DocRead(t, "docs/reference/workflow-contract.md")
	for _, pin := range w80ContractPins {
		if !strings.Contains(contract, pin) {
			t.Errorf("docs/reference/workflow-contract.md does not carry the pinned sentence %q", pin)
		}
	}
	converge := w80DocRead(t, "docs/design/polyforge-vnext/v2-converge.md")
	for _, pin := range w80ConvergePins {
		if !strings.Contains(converge, pin) {
			t.Errorf("docs/design/polyforge-vnext/v2-converge.md does not carry the pinned row text %q", pin)
		}
	}
	capsuleSrc := w80DocRead(t, "internal/common/capsule/capsule.go")
	if !strings.Contains(capsuleSrc, `TargetBranchJSON = "runtime-state/target-branch.json"`) {
		t.Errorf("internal/common/capsule/capsule.go does not name runtime-state/target-branch.json as TargetBranchJSON")
	}
}
