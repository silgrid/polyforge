package test

// bugfix_dead_end_contract_test.go: the bug-fix prompts state the two
// dead-end owner questions with their three answers and the pre-fix base
// duty of a repro re-run.

import (
	"strings"
	"testing"
)

func TestBugfixDeadEndContract(t *testing.T) {
	answers := []string{"`re-run-repro: <evidence>`", "`continue: <guidance>`", "`cancel`"}
	for _, p := range []string{"workflows/bug-fix/workflow.md", "workflows/bug-fix/steps/root-cause.md"} {
		text := w68Flat(t, p)
		for _, want := range answers {
			if !strings.Contains(text, want) {
				t.Errorf("%s must offer %s", p, want)
			}
		}
	}
	lane := w68Flat(t, "workflows/bug-fix/workflow.md")
	for _, want := range []string{"a second one on this edge does not spend a charged rewind", "honestly reports `reproduces: false` is not handed to `root-cause`", "`pre_fix_heads`"} {
		if !strings.Contains(lane, want) {
			t.Errorf("the bug-fix lane must state %q", want)
		}
	}
	repro := w68Flat(t, "workflows/bug-fix/steps/repro.md")
	for _, want := range []string{"pre-fix base", "never against the current, mitigated tree", "instead of handing it to `root-cause`"} {
		if !strings.Contains(repro, want) {
			t.Errorf("repro must state %q", want)
		}
	}
	if !strings.Contains(w68Flat(t, "workflows/bug-fix/steps/fix.md"), "pre-fix base") {
		t.Error("fix must state that a repro re-run uses the pre-fix base")
	}
}
