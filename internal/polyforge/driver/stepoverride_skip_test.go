package driver

// stepoverride_skip_test.go (u3, polyforge#25): the overlay's skip switch
// is re-read before every step — a skip set mid-run (through the daemon's
// override route) governs the very next plan pass, the skipped step is
// never dispatched (its status records skipped, with a step_skipped
// event), the wrap attrs of an overlay-skipped tail move to the last step
// that runs, an un-skip dispatches the step again, and an overlay that
// names a step the lane does not declare skippable changes nothing (the
// write path refuses it; the read side stays defense-in-depth).

import (
	"context"
	"path/filepath"
	"reflect"
	"slices"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// overlaySkipEnv lays out a capsule whose lane snapshot is
// a → b(skippable) → c(skippable, tail, wrap attr), no fixed skip file,
// and builds a driver over it.
func overlaySkipEnv(t *testing.T, overlay string) (*Driver, *fakeSpawner, *eventLog, string) {
	t.Helper()
	ws := t.TempDir()
	dir := filepath.Join(ws, "capsules", "wi_ov3")
	workflowtest.WriteLane(t, filepath.Join(dir, capsule.DirWorkflows), "demo",
		"steps:\n  - { id: a }\n  - { id: b, skippable: true }\n  - { id: c, skippable: true, wrap_attrs: [rec] }\n")
	writeCapsuleFile(t, dir, capsule.WorkItemJSON, `{"id":"wi_ov3","slug":"demo-3","project":"proj","goal":"g"}`)
	if overlay != "" {
		writeCapsuleFile(t, dir, capsule.StepOverridesJSON, overlay)
	}
	sp := &fakeSpawner{script: []fakeEntry{done(`{}`), done(`{}`), done(`{}`)}}
	log := &eventLog{}
	d, err := New(Config{
		WI:      WIInfo{ID: "wi_ov3", CapsuleDir: dir, BundleName: "demo"},
		Spawner: sp,
		Log:     log,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, sp, log, dir
}

func overlaySkipWrite(t *testing.T, dir, skipJSON string) {
	t.Helper()
	writeCapsuleFile(t, dir, capsule.StepOverridesJSON, skipJSON)
}

func overlaySkipSteps(sp *fakeSpawner) []string {
	sp.mu.Lock()
	defer sp.mu.Unlock()
	var out []string
	for _, s := range sp.spawns {
		out = append(out, s.StepID)
	}
	return out
}

const ov3SkipC = `{"steps":{"c":{"skip":true,"skip_by":"usr_1","skip_at":"t"}}}`

// An overlay written before the run is honored from the first plan pass:
// c is never dispatched, records skipped with a step_skipped event, and
// the lane completes over done-or-skipped steps.
func TestStepOverrideSkipIsHonoredPerPlan(t *testing.T) {
	d, sp, log, dir := overlaySkipEnv(t, ov3SkipC)
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := overlaySkipSteps(sp); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("dispatched %v, want [a b] (the overlay skips c)", got)
	}
	x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
	if x.Steps["c"].Status != capsule.StepSkipped {
		t.Fatalf("c status %q, want skipped", x.Steps["c"].Status)
	}
	if x.WorkflowStatus != StatusCompleted {
		t.Fatalf("workflow_status %q, want completed", x.WorkflowStatus)
	}
	if n := log.count("step_skipped"); n != 1 || !log.has("step_skipped", "step", "c") {
		t.Fatalf("events %v: want one step_skipped {step: c}", log.kinds())
	}
	// the tail step's wrap attr moved to the last step that runs (b):
	// its round must register c's attr before done — the hub whitelist
	// the daemon's `pf step` checks read must grant wrap-attrs to b.
	if got := d.workflow.HubAllowed("b"); !slices.Contains(got, "wrap-attrs") {
		t.Fatalf("b's hub verbs %v, want wrap-attrs (c's wrap attr moved to the last running step)", got)
	}
	if !d.workflow.lastAgentStep("b") || d.workflow.lastAgentStep("c") {
		t.Fatal("the last step an agent runs is b")
	}
}

// A skip set MID-RUN — the overlay route's whole point — governs the
// next plan pass: step a runs, the overlay lands while it works, and b is
// never dispatched.
func TestStepOverrideSkipMidRun(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "capsules", "wi_ov3m")
	workflowtest.WriteLane(t, filepath.Join(dir, capsule.DirWorkflows), "demo",
		"steps:\n  - { id: a }\n  - { id: b, skippable: true }\n  - { id: c, skippable: true }\n")
	writeCapsuleFile(t, dir, capsule.WorkItemJSON, `{"id":"wi_ov3m","slug":"demo-4","project":"proj","goal":"g"}`)
	sp := &fakeSpawner{script: []fakeEntry{
		{res: done(`{}`).res, act: func() { overlaySkipWrite(t, dir, `{"steps":{"b":{"skip":true}}}`) }},
		done(`{}`), done(`{}`),
	}}
	log := &eventLog{}
	d, err := New(Config{WI: WIInfo{ID: "wi_ov3m", CapsuleDir: dir, BundleName: "demo"}, Spawner: sp, Log: log})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := overlaySkipSteps(sp); !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Fatalf("dispatched %v, want [a c] (the overlay landed while a worked; b is skipped from the next plan pass)", got)
	}
	x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
	if x.Steps["b"].Status != capsule.StepSkipped || x.Steps["c"].Status != capsule.StepDone {
		t.Fatalf("b=%q c=%q, want skipped done", x.Steps["b"].Status, x.Steps["c"].Status)
	}
}

// An un-skip (the aspect cleared) mid-run dispatches the step again, and
// an overlay naming a step the lane does not declare skippable changes
// nothing: the overlay starts by skipping c and naming a, and while a
// works it is rewritten with the skip cleared — every step runs, nothing
// is recorded skipped.
func TestStepOverrideUnskipDispatches(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "capsules", "wi_ov3u")
	workflowtest.WriteLane(t, filepath.Join(dir, capsule.DirWorkflows), "demo",
		"steps:\n  - { id: a }\n  - { id: b, skippable: true }\n  - { id: c, skippable: true }\n")
	writeCapsuleFile(t, dir, capsule.WorkItemJSON, `{"id":"wi_ov3u","slug":"demo-5","project":"proj","goal":"g"}`)
	writeCapsuleFile(t, dir, capsule.StepOverridesJSON, `{"steps":{"c":{"skip":true},"a":{"skip":true}}}`)
	sp := &fakeSpawner{script: []fakeEntry{
		{res: done(`{}`).res, act: func() {
			// the owner's un-skip: the skip aspect cleared while a works
			writeCapsuleFile(t, dir, capsule.StepOverridesJSON, `{"steps":{"a":{"skip":true}}}`)
		}},
		done(`{}`), done(`{}`),
	}}
	log := &eventLog{}
	d, err := New(Config{WI: WIInfo{ID: "wi_ov3u", CapsuleDir: dir, BundleName: "demo"}, Spawner: sp, Log: log})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := overlaySkipSteps(sp); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("dispatched %v, want [a b c] (a's non-skippable overlay entry is inert; c's skip was cleared before its turn)", got)
	}
	x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
	for _, id := range []string{"a", "b", "c"} {
		if x.Steps[id].Status != capsule.StepDone {
			t.Fatalf("%s status %q, want done", id, x.Steps[id].Status)
		}
	}
	if n := log.count("step_skipped"); n != 0 {
		t.Fatalf("events %v: no step may be recorded skipped", log.kinds())
	}
}
