package driver

// rootcausefalse_test.go: the bug-fix false routing a
// completed fix reporting root_cause_addressed: false rewinds to
// root-cause once (charged fix→root-cause, its summary the rewind
// input), a second false parks on the owner question, root-cause's own
// fail still rewinds to repro, and a lane without root-cause is
// untouched.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func bugFixLane() []StepSpec {
	return []StepSpec{spec("repro"), gateSpec("root-cause", "repro"), spec("fix"), gateSpec("review", "fix")}
}

func fixDone(addressed bool, summary string) fakeEntry {
	commits := `[]` // a false report that changed nothing: the one legal empty list
	if addressed {
		commits = `[{"sha":"f1x","message":"fix"}]` // a true fix lists its commit
	}
	return done(fmt.Sprintf(`{"build_status":"green","commits":%s,"root_cause_addressed":%v,"summary":%q}`, commits, addressed, summary))
}

func dispatchedSteps(sp *fakeSpawner) string {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	ids := make([]string, 0, len(sp.spawns))
	for _, s := range sp.spawns {
		ids = append(ids, s.StepID)
	}
	return strings.Join(ids, ",")
}

func TestRootCauseFalseFirstRewindsToRootCause(t *testing.T) {
	d, sp, execPath := runEnv(t, bugFixLane()...)
	const summary = "the diagnosed nil map is not the cause; the race on reload is"
	sp.script = []fakeEntry{done(`{}`), done(`{"verdict":"pass"}`), fixDone(false, summary),
		done(`{"verdict":"pass"}`), fixDone(true, "fixed the reload race"), done(`{"verdict":"pass"}`)}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := dispatchedSteps(sp); got != "repro,root-cause,fix,root-cause,fix,review" {
		t.Fatalf("dispatch order %s: a false fix must re-enter root-cause before review", got)
	}
	if ri := sp.spawnAt(3).RewindInput; ri == nil || ri.ToStep != "root-cause" || !strings.Contains(ri.Reason, summary) {
		t.Fatalf("the re-run root-cause must carry the fix summary as its rewind input: %+v", ri)
	}
	if f := sp.spawnAt(4); f.Resume || !f.Fresh {
		t.Fatalf("the fix re-runs fresh against the new diagnosis: %+v", f)
	}
	x := readExec(t, execPath)
	if x.WorkflowStatus != StatusCompleted {
		t.Fatalf("status %q", x.WorkflowStatus)
	}
	want := []capsule.RewindRecord{{Gate: "fix", ToStep: "root-cause", Class: capsule.RewindCharged}}
	if x.RewindStats.Count != 1 || fmt.Sprint(x.RewindStats.Rewinds) != fmt.Sprint(want) {
		t.Fatalf("rewind ledger %+v, want one charged fix→root-cause record", x.RewindStats)
	}
	var archived bool
	for _, a := range x.Steps["root-cause"].Attempts {
		if a.Cause == CauseRootCauseUnaddressed && a.RewindGate == "fix" {
			archived = true
		}
	}
	if !archived {
		t.Fatalf("root-cause must carry the root_cause_unaddressed archive: %+v", x.Steps["root-cause"].Attempts)
	}
}

func TestRootCauseFalseSecondParksOnOwnerQuestion(t *testing.T) {
	d, sp, execPath := runEnv(t, bugFixLane()...)
	sp.script = []fakeEntry{done(`{}`), done(`{"verdict":"pass"}`), fixDone(false, "first"),
		done(`{"verdict":"pass"}`), fixDone(false, "second"),
		fixDone(true, "fixed with the owner's guidance"), done(`{"verdict":"pass"}`)}
	doneCh := make(chan error, 1)
	go func() { doneCh <- d.Run(context.Background()) }()
	waitUntil(t, "the owner question", func() bool {
		x := tryReadExec(execPath)
		return x != nil && x.Steps["fix"].PendingQuestion != nil
	})
	x := readExec(t, execPath)
	if q := x.Steps["fix"].PendingQuestion; q.ID != RootCauseFalseQuestionID || !strings.Contains(q.Question, "cancel") {
		t.Fatalf("the second false must ask the owner: %+v", q)
	}
	if x.WorkflowStatus != StatusRunning || x.RewindStats.Count != 1 {
		t.Fatalf("no second rewind, never exhausted: status %q, ledger %+v", x.WorkflowStatus, x.RewindStats)
	}
	if got := dispatchedSteps(sp); got != "repro,root-cause,fix,root-cause,fix" {
		t.Fatalf("dispatch order %s: the second false must neither rewind again nor reach review", got)
	}
	// The driver persists the pending question before emitting needs_input,
	// so the event can trail the disk state: wait for it through the
	// log's locked lookup, never the raw slice.
	waitUntil(t, "the needs_input event for the owner question", func() bool {
		return d.log.(*eventLog).has("needs_input", "question_id", RootCauseFalseQuestionID)
	})
	if err := d.Answer(RootCauseFalseQuestionID, "the cause is the reload race; fix that", "owner"); err != nil {
		t.Fatalf("answer: %v", err)
	}
	if err := <-doneCh; err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := dispatchedSteps(sp); got != "repro,root-cause,fix,root-cause,fix,fix,review" {
		t.Fatalf("dispatch order %s: the answer re-dispatches the fix in place", got)
	}
	if a := sp.spawnAt(5).Answers; len(a) != 1 || a[0].QuestionID != RootCauseFalseQuestionID {
		t.Fatalf("the re-dispatched fix carries the owner's answer: %+v", sp.spawnAt(5))
	}
	if x := readExec(t, execPath); x.WorkflowStatus != StatusCompleted {
		t.Fatalf("status %q", x.WorkflowStatus)
	}
}

func TestRootCauseFailAfterFalseStillRewindsToRepro(t *testing.T) {
	d, sp, execPath := runEnv(t, bugFixLane()...)
	sp.script = []fakeEntry{done(`{}`), done(`{"verdict":"pass"}`), fixDone(false, "wrong cause"),
		gateFAIL(), done(`{}`), done(`{"verdict":"pass"}`), fixDone(true, "fixed"), done(`{"verdict":"pass"}`)}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := dispatchedSteps(sp); got != "repro,root-cause,fix,root-cause,repro,root-cause,fix,review" {
		t.Fatalf("dispatch order %s: root-cause's fail must still rewind to repro", got)
	}
	x := readExec(t, execPath)
	want := []capsule.RewindRecord{{Gate: "fix", ToStep: "root-cause", Class: capsule.RewindCharged}, {Gate: "root-cause", ToStep: "repro", Class: capsule.RewindCharged}}
	if x.WorkflowStatus != StatusCompleted || fmt.Sprint(x.RewindStats.Rewinds) != fmt.Sprint(want) {
		t.Fatalf("status %q ledger %+v", x.WorkflowStatus, x.RewindStats)
	}
}

// A lane without a root-cause step before the fix (autopilot) accepts a
// false report as before: no rewind, the gate judges it.
func TestRootCauseFalseWithoutRootCauseStepIsAccepted(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("fix"), gateSpec("review", "fix"))
	sp.script = []fakeEntry{fixDone(false, "symptom only"), done(`{"verdict":"pass"}`)}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := readExec(t, execPath)
	if got := dispatchedSteps(sp); got != "fix,review" || x.RewindStats.Count != 0 || x.WorkflowStatus != StatusCompleted {
		t.Fatalf("order %s ledger %+v status %q", got, x.RewindStats, x.WorkflowStatus)
	}
}

// The shipped root-cause step text describes only the re-entry inputs the
// driver can produce: a completed fix's report that the
// diagnosis did not hold arrives as `pending_rewind_input` (and demands
// re-deriving the cause, never re-affirming the old one), while a re-run
// after root-cause's own fail arrives with none — no gate rewinds to
// root-cause, so the old "previous fail_reason via repro or a gate FAIL"
// source is gone and the text points at the archived verdict instead.
func TestRootCauseStepTextNamesItsReEntryInputs(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "workflows", "bug-fix", "steps", "root-cause.md"))
	if err != nil {
		t.Fatalf("read shipped root-cause.md: %v", err)
	}
	s := strings.Join(strings.Fields(string(raw)), " ")
	for _, want := range []string{
		"`root_cause_addressed: false`",
		"did not hold",
		"re-derive the cause",
		"with no `pending_rewind_input`",
		"`artifacts/steps/root-cause/s<n>/result.json`",
	} {
		if !strings.Contains(s, want) {
			t.Errorf("shipped root-cause.md lost %q", want)
		}
	}
	for _, gone := range []string{
		"two sources",
		"or a gate FAIL",
		"carries the previous `fail_reason`",
	} {
		if strings.Contains(s, gone) {
			t.Errorf("shipped root-cause.md still names a rewind-input source the driver never produces (%q)", gone)
		}
	}
}

// The premise of that text, pinned on the driver: root-cause's own fail
// rewinds to repro carrying the verdict, and the re-run root-cause is a
// fresh dispatch with NO rewind input (the gate's verdict closed its own
// round); only the fix's false report reaches root-cause as one.
func TestRootCauseReRunAfterItsOwnFailCarriesNoRewindInput(t *testing.T) {
	d, sp, _ := runEnv(t, bugFixLane()...)
	failed := fakeEntry{res: &StepResult{Outcome: OutcomeGateFailed,
		Output: json.RawMessage(`{"verdict":"fail","fail_reason":"capture the reload log at debug"}`)}}
	sp.script = []fakeEntry{done(`{}`), failed, done(`{}`), done(`{"verdict":"pass"}`),
		fixDone(true, "fixed"), done(`{"verdict":"pass"}`)}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := dispatchedSteps(sp); got != "repro,root-cause,repro,root-cause,fix,review" {
		t.Fatalf("dispatch order %s: root-cause's fail rewinds to repro", got)
	}
	if ri := sp.spawnAt(2).RewindInput; ri == nil || ri.ToStep != "repro" || !strings.Contains(ri.Reason, "capture the reload log") {
		t.Fatalf("the re-run repro carries root-cause's fail_reason: %+v", ri)
	}
	rc := sp.spawnAt(3)
	if rc.RewindInput != nil {
		t.Fatalf("the re-run root-cause must carry no rewind input after its own fail, got %+v", rc.RewindInput)
	}
	if rc.Resume || !rc.Fresh {
		t.Fatalf("the re-run root-cause starts a fresh conversation: %+v", rc)
	}
}
