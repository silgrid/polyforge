package driver

// w521_skip_test.go ( u4): the driver never dispatches a step
// the work item's skipped-step set (runtime-state/skipped-steps.json,
// fixed at the first claim) names when its lane snapshot declares it
// skippable: the step is recorded skipped with a step_skipped event, the
// lane completes over done-or-skipped steps, the last step an agent runs
// moves to the last step not skipped, and the hand-off document's step
// order says so. The driver reads the fixed file alone — never the
// workspace yaml — and a listed step the lane does not declare skippable
// runs as usual.

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// w521Env lays out a capsule whose lane snapshot is a → b(skippable) →
// c(skippable), with the given skipped-step set file, and builds a driver
// over it (the lane read from the snapshot, as a claim's driver does).
func w521Env(t *testing.T, skipped string) (*Driver, *fakeSpawner, *eventLog, string) {
	t.Helper()
	ws := t.TempDir()
	dir := filepath.Join(ws, "capsules", "wi_skip")
	workflowtest.WriteLane(t, filepath.Join(dir, capsule.DirWorkflows), "demo",
		"steps:\n  - { id: a }\n  - { id: b, skippable: true }\n  - { id: c, skippable: true }\n")
	writeCapsuleFile(t, dir, capsule.SkippedStepsJSON, skipped)
	writeCapsuleFile(t, dir, capsule.WorkItemJSON, `{"id":"wi_skip","slug":"demo-9","project":"proj","goal":"g"}`)
	// The workspace yaml says otherwise: the driver never reads it.
	if err := os.WriteFile(filepath.Join(ws, ".polyforge.yaml"), []byte("steps:\n  proj:\n    b: { skip: true }\n    c: { skip: false }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{}`), done(`{}`)}}
	log := &eventLog{}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_skip", CapsuleDir: dir, BundleName: "demo"},
		Spawner:  sp,
		Log:      log,
		Language: func() string { return stepcall.LangZhCN },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, sp, log, dir
}

func w521Steps(sp *fakeSpawner) []string {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	var out []string
	for _, s := range sp.spawns {
		out = append(out, s.StepID)
	}
	return out
}

func TestW521SkippedStepIsNeverDispatched(t *testing.T) {
	d, sp, log, dir := w521Env(t, `["c"]`+"\n")
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := w521Steps(sp); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("dispatched %v, want [a b] (c is skipped; the yaml's b is no source)", got)
	}
	x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
	if x.WorkflowStatus != StatusCompleted {
		t.Fatalf("workflow_status %q, want completed", x.WorkflowStatus)
	}
	if x.Steps["a"].Status != capsule.StepDone || x.Steps["b"].Status != capsule.StepDone || x.Steps["c"].Status != capsule.StepSkipped {
		t.Fatalf("step statuses a=%q b=%q c=%q, want done done skipped", x.Steps["a"].Status, x.Steps["b"].Status, x.Steps["c"].Status)
	}
	if n := log.count("step_skipped"); n != 1 || !log.has("step_skipped", "step", "c") {
		t.Fatalf("events %v: want one step_skipped {step: c}", log.kinds())
	}
	if !d.workflow.lastAgentStep("b") || d.workflow.lastAgentStep("c") {
		t.Fatal("the last step an agent runs is b, the last one not skipped")
	}
	doc := handoffDocOf(t, dir)
	if want := "## 车道步骤顺序\n\n1. `a`\n2. `b`（最后一步）\n3. `c`（已跳过）\n"; !strings.Contains(doc, want) {
		t.Fatalf("the opening's step order lacks %q:\n%s", want, doc)
	}

	// A fixed set naming a step its lane does not declare skippable: the
	// step runs as usual.
	d2, sp2, log2, dir2 := w521Env(t, `["a"]`+"\n")
	if err := d2.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := w521Steps(sp2); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("dispatched %v, want [a b c] (a is not declared skippable)", got)
	}
	if x2 := readExec(t, filepath.Join(dir2, capsule.ExecutionJSON)); x2.WorkflowStatus != StatusCompleted || x2.Steps["a"].Status != capsule.StepDone {
		t.Fatalf("a listed but not skippable: status %q, a %q", x2.WorkflowStatus, x2.Steps["a"].Status)
	}
	if log2.count("step_skipped") != 0 || !d2.workflow.lastAgentStep("c") {
		t.Fatalf("nothing skipped, c stays the last step: events %v", log2.kinds())
	}
}
