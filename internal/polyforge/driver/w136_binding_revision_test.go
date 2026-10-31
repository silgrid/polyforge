package driver

// w136_binding_revision_test.go: the owner revision face of
//. The reported second failure is that a canonical
// execution.json cannot be corrected: transition is the single writer,
// a hand edit is overwritten at the next write or lost at the next
// claim's fresh clone, and no owner verb exists.
//
// This test pins the required contract at the driver's single write
// entry point: one revision batch corrects every named binding command,
// each correction lands in execution.json with its own audit record,
// nothing else in the output changes, the corrected bytes survive a fresh
// load (the materialization re-claim performs), and a batch that would
// reintroduce a malformed command is refused without touching the record.
// The all-malformed fixture is the yingfang#37 shape: validating the whole
// output after each single-index revision would make every call refuse
// while any sibling is still malformed, so the batch must apply together.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func w136RevisionOutput(t *testing.T, commands ...string) map[string]any {
	t.Helper()
	titles := make([]string, len(commands))
	bindings := make([]map[string]any, len(commands))
	for i := range commands {
		titles[i] = fmt.Sprintf("S%d", i+1)
		bindings[i] = map[string]any{
			"title":   titles[i],
			"repo":    "yingfang",
			"command": commands[i],
			"unit":    fmt.Sprintf("TestS%d", i+1),
			"marker":  fmt.Sprintf("^PASS S%d$", i+1),
		}
	}
	out := map[string]any{"scenario_titles": stringSliceAny(titles)}
	list := make([]any, len(bindings))
	for i, b := range bindings {
		list[i] = b
	}
	out["bindings"] = list
	return out
}

func stringSliceAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}

// w136RevisionEnv writes a canonical record shaped like a paused
// yingfang#37 capsule (done e2e-author, the given binding commands) and
// opens a driver over it. No Run loop is started: the revision is a
// record-only write, like a paused record's owner move.
func w136RevisionEnv(t *testing.T, commands ...string) (*Driver, *eventLog, string) {
	t.Helper()
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	exec := &capsule.Execution{
		CapsuleID:      "cap_wi_t1",
		WorkItemID:     "wi_t1",
		CurrentStep:    e2eAuthorStepID,
		WorkflowStatus: StatusBlocked,
		Steps: map[string]*capsule.Step{
			e2eAuthorStepID: {
				Status: capsule.StepDone,
				Output: w136RevisionOutput(t, commands...),
			},
		},
	}
	blob, err := json.Marshal(exec)
	if err != nil {
		t.Fatalf("marshal canonical execution.json: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
		t.Fatalf("mkdir runtime-state: %v", err)
	}
	if err := os.WriteFile(execPath, blob, 0o644); err != nil {
		t.Fatalf("write canonical execution.json: %v", err)
	}
	log := &eventLog{}
	clk := &clock{}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_t1", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: &Workflow{Steps: []StepSpec{spec(e2eAuthorStepID)}, MaxTotal: 4, MaxPerStep: 2},
		Spawner:  &fakeSpawner{},
		Log:      log,
		Now:      clk.now,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, log, execPath
}

func w136BindingCommand(t *testing.T, exec *capsule.Execution, index int) string {
	t.Helper()
	bindings, ok := exec.Steps[e2eAuthorStepID].Output["bindings"].([]any)
	if !ok || index >= len(bindings) {
		t.Fatalf("no bindings[%d] in the record", index)
	}
	b, ok := bindings[index].(map[string]any)
	if !ok {
		t.Fatalf("bindings[%d] is not an object", index)
	}
	return b["command"].(string)
}

func TestW136OwnerRevisionCorrectsCanonicalBinding(t *testing.T) {
	t.Log("single-index revision still works beside a well-formed sibling")
	d, log, execPath := w136RevisionEnv(t, "go test ./tests/e2e_cp37/ -count=1 -v", w136BadCommand)
	if err := d.ReviseBinding(e2eAuthorStepID, BindingRevisionRequest{
		Corrections: []capsule.BindingCorrection{{Index: 1, Command: w136GoodCommand}},
		By:          "usr_owner", Reason: "yingfang#37: pair the quote",
	}); err != nil {
		t.Fatalf("ReviseBinding: %v", err)
	}

	revised := readExec(t, execPath)
	if got := w136BindingCommand(t, revised, 1); got != w136GoodCommand {
		t.Fatalf("bindings[1].command after revision = %q, want the corrected command", got)
	}
	first, ok := revised.Steps[e2eAuthorStepID].Output["bindings"].([]any)[0].(map[string]any)
	if !ok || first["command"] != "go test ./tests/e2e_cp37/ -count=1 -v" {
		t.Fatalf("bindings[0] must stay byte-identical; got %v", first)
	}
	if titles, _ := revised.Steps[e2eAuthorStepID].Output["scenario_titles"].([]any); len(titles) != 2 {
		t.Fatalf("scenario_titles must stay untouched; got %v", titles)
	}
	revs := revised.Steps[e2eAuthorStepID].BindingRevisions
	if len(revs) != 1 {
		t.Fatalf("binding_revisions = %d, want the one audit record", len(revs))
	}
	if revs[0].Index != 1 || revs[0].Before != w136BadCommand || revs[0].After != w136GoodCommand ||
		revs[0].By != "usr_owner" || revs[0].Reason == "" || revs[0].At == "" {
		t.Fatalf("the revision audit record is incomplete: %+v", revs[0])
	}
	if !log.has(EvBindingRevised, "binding_index", 1) {
		t.Fatalf("the revision must be a durable binding_revised event; got %v", log.kinds())
	}

	// The re-claim's materialization is a fresh load of execution.json:
	// the corrected bytes must be what a new driver (or a fresh clone of
	// the published chain) sees.
	reloaded, err := New(Config{
		WI:       WIInfo{ID: "wi_t1", CapsuleDir: filepath.Dir(execPath), BundleName: "b"},
		ExecPath: execPath,
		Workflow: &Workflow{Steps: []StepSpec{spec(e2eAuthorStepID)}, MaxTotal: 4, MaxPerStep: 2},
		Spawner:  &fakeSpawner{},
	})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if got := w136BindingCommand(t, reloaded.Snapshot(), 1); got != w136GoodCommand {
		t.Fatalf("after re-materialization bindings[1].command = %q, want the correction to survive", got)
	}
}

// TestW136OwnerRevisionCorrectsEveryMalformedBinding is the yingfang#37
// shape that broke the single-index revision: every binding carries the
// same unpaired quote. One batch must correct all of them, because a
// whole-output validation after the first correction would still see the
// other malformed siblings.
func TestW136OwnerRevisionCorrectsEveryMalformedBinding(t *testing.T) {
	bad := make([]string, 3)
	good := make([]string, 3)
	corrections := make([]capsule.BindingCorrection, 3)
	for i := range bad {
		bad[i] = fmt.Sprintf("%s # binding %d", w136BadCommand, i)
		good[i] = fmt.Sprintf("%s # binding %d", w136GoodCommand, i)
		corrections[i] = capsule.BindingCorrection{Index: i, Command: good[i]}
	}
	d, log, execPath := w136RevisionEnv(t, bad...)
	if err := d.ReviseBinding(e2eAuthorStepID, BindingRevisionRequest{
		Corrections: corrections, By: "usr_owner", Reason: "yingfang#37: pair every quote in one pass",
	}); err != nil {
		t.Fatalf("a batch correcting every malformed binding must succeed: %v", err)
	}

	revised := readExec(t, execPath)
	for i := range good {
		if got := w136BindingCommand(t, revised, i); got != good[i] {
			t.Fatalf("bindings[%d].command = %q, want %q", i, got, good[i])
		}
	}
	revs := revised.Steps[e2eAuthorStepID].BindingRevisions
	if len(revs) != len(corrections) {
		t.Fatalf("binding_revisions = %d, want one audit record per corrected index (%d)", len(revs), len(corrections))
	}
	for i, r := range revs {
		if r.Index != i || r.Before != bad[i] || r.After != good[i] || r.By != "usr_owner" || r.At == "" {
			t.Fatalf("audit record %d is incomplete or misordered: %+v", i, r)
		}
	}
	if n := log.count(EvBindingRevised); n != len(corrections) {
		t.Fatalf("binding_revised events = %d, want one per corrected index (%d); got %v", n, len(corrections), log.kinds())
	}
	for i := range good {
		if !log.has(EvBindingRevised, "binding_index", i) {
			t.Fatalf("no binding_revised event for binding_index %d; got %v", i, log.kinds())
		}
	}

	// The corrected bytes must be what a re-claim's fresh load sees.
	reloaded, err := New(Config{
		WI:       WIInfo{ID: "wi_t1", CapsuleDir: filepath.Dir(execPath), BundleName: "b"},
		ExecPath: execPath,
		Workflow: &Workflow{Steps: []StepSpec{spec(e2eAuthorStepID)}, MaxTotal: 4, MaxPerStep: 2},
		Spawner:  &fakeSpawner{},
	})
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	for i := range good {
		if got := w136BindingCommand(t, reloaded.Snapshot(), i); got != good[i] {
			t.Fatalf("after re-materialization bindings[%d].command = %q, want %q", i, got, good[i])
		}
	}
}

func TestW136OwnerRevisionRefusesMalformedCommand(t *testing.T) {
	d, _, execPath := w136RevisionEnv(t, "go test ./tests/e2e_cp37/ -count=1 -v", w136BadCommand)
	before, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	for name, req := range map[string]BindingRevisionRequest{
		"same malformed command": {Corrections: []capsule.BindingCorrection{{Index: 1, Command: w136BadCommand}}, By: "usr_owner"},
		"another unpaired quote": {Corrections: []capsule.BindingCorrection{{Index: 1, Command: `echo "unterminated`}}, By: "usr_owner"},
		"empty command":          {Corrections: []capsule.BindingCorrection{{Index: 1, Command: "  "}}, By: "usr_owner"},
		"index out of range":     {Corrections: []capsule.BindingCorrection{{Index: 9, Command: w136GoodCommand}}, By: "usr_owner"},
		"duplicate index":        {Corrections: []capsule.BindingCorrection{{Index: 1, Command: w136GoodCommand}, {Index: 1, Command: w136GoodCommand}}, By: "usr_owner"},
		"no corrections":         {By: "usr_owner"},
	} {
		if err := d.ReviseBinding(e2eAuthorStepID, req); err == nil {
			t.Errorf("%s: must be refused", name)
		}
		after, rerr := os.ReadFile(execPath)
		if rerr != nil {
			t.Fatal(rerr)
		}
		if string(after) != string(before) {
			t.Fatalf("%s: a refused revision changed execution.json", name)
		}
	}
}

// TestW136OwnerRevisionRefusesPartialBatch: correcting one of two
// malformed bindings is refused because the corrected output as a whole
// still cannot pass verification. The record is untouched, and the owner
// corrects both in one batch instead.
func TestW136OwnerRevisionRefusesPartialBatch(t *testing.T) {
	d, _, execPath := w136RevisionEnv(t, w136BadCommand, w136BadCommand)
	before, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	err = d.ReviseBinding(e2eAuthorStepID, BindingRevisionRequest{
		Corrections: []capsule.BindingCorrection{{Index: 0, Command: w136GoodCommand}}, By: "usr_owner",
	})
	if err == nil || !strings.Contains(err.Error(), "still invalid") {
		t.Fatalf("a batch leaving a malformed sibling must be refused; got %v", err)
	}
	after, rerr := os.ReadFile(execPath)
	if rerr != nil {
		t.Fatal(rerr)
	}
	if string(after) != string(before) {
		t.Fatal("a refused partial batch changed execution.json")
	}
}

func TestW136OwnerRevisionRefusesUnknownStep(t *testing.T) {
	d, _, _ := w136RevisionEnv(t, w136BadCommand)
	if err := d.transition(TransitionRequest{
		Kind: TransStepRevision, StepID: "missing",
		BindingRevision: &BindingRevisionRequest{Corrections: []capsule.BindingCorrection{{Index: 0, Command: w136GoodCommand}}},
		RevisionOutput:  map[string]any{"bindings": []any{}},
		RevisionBefore:  []string{w136BadCommand},
		SpawnN:          d.currentSpawnN(),
	}); err == nil || !strings.Contains(err.Error(), "not in the workflow") {
		t.Fatalf("a revision of an unknown step must be refused; got %v", err)
	}
}

func TestW136OwnerRevisionRefusesNonDoneStep(t *testing.T) {
	d, _, _ := w136RevisionEnv(t, w136BadCommand)
	d.mu.Lock()
	d.exec.Steps[e2eAuthorStepID].Status = capsule.StepPending
	d.mu.Unlock()
	if err := d.transition(TransitionRequest{
		Kind: TransStepRevision, StepID: e2eAuthorStepID,
		BindingRevision: &BindingRevisionRequest{Corrections: []capsule.BindingCorrection{{Index: 0, Command: w136GoodCommand}}},
		RevisionOutput:  map[string]any{"bindings": []any{}},
		RevisionBefore:  []string{w136BadCommand},
		SpawnN:          d.currentSpawnN(),
	}); err == nil || !strings.Contains(err.Error(), "only a done step") {
		t.Fatalf("a revision of an existing step that is not done must be refused; got %v", err)
	}
}
