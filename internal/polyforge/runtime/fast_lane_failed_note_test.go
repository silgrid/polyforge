package runtime

// fast_lane_failed_note_test.go: the failed close's hub note carries the
// driver's termination_reason (a verification-less lane's
// out-of-bounds refusal names the lane to re-file on), bounded, and stays
// empty for every failure that records none.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func writeExecForNote(t *testing.T, ex capsule.Execution) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, capsule.ExecutionJSON)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestFailedWorkflowNote(t *testing.T) {
	const reason = "out of bounds for this verification-less lane: repos/demo: changed path(s) outside its declared micro_path: src/app.go. re-file the work on the autopilot lane."
	dir := writeExecForNote(t, capsule.Execution{WorkflowStatus: capsule.WorkflowStatusFailedGate, TerminationReason: reason})
	if got := failedWorkflowNote(dir); got != "workflow failed: "+reason {
		t.Fatalf("failed note %q", got)
	}
	plain := writeExecForNote(t, capsule.Execution{WorkflowStatus: capsule.WorkflowStatusFailedRewindExhausted})
	if got := failedWorkflowNote(plain); got != "" {
		t.Fatalf("a failure without a termination reason must keep the empty note, got %q", got)
	}
	if got := failedWorkflowNote(t.TempDir()); got != "" {
		t.Fatalf("an absent execution record must keep the empty note, got %q", got)
	}
	long := writeExecForNote(t, capsule.Execution{WorkflowStatus: capsule.WorkflowStatusFailedGate, TerminationReason: strings.Repeat("x", 2*blockedReasonNoteMax) + " autopilot"})
	got := failedWorkflowNote(long)
	if n := len([]rune(got)); n > blockedReasonNoteMax+len("workflow failed: ")+1 || !strings.HasSuffix(got, "…") {
		t.Fatalf("the note is not bounded: %d runes", n)
	}
}
