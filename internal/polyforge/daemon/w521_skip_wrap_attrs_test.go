package daemon

// w521_skip_wrap_attrs_test.go: the wrap attrs of the
// skipped steps at the lane's tail move to the last step that runs — the
// `pf step` checks (the hub whitelist and the done check) read the lane
// snapshot with the work item's skipped-step set (fixed at the first
// claim) applied, the same way the driver does.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// w521Lane: a → p → m, m skippable at the tail and declaring a wrap attr.
const w521Lane = `steps:
  - id: a
  - id: p
  - id: m
    skippable: true
    wrap_attrs: [rec]
`

// TestW521SkippedWrapAttrsMoveToTheLastRunningStep: with the skipped-step
// set naming the skippable tail step m, the step before it (p) — now the
// last step that runs — may call wrap-attrs and hands off done only once
// its round registered m's wrap attr; with an empty set p's whitelist and
// done stay as they are (no wrap-attrs).
func TestW521SkippedWrapAttrsMoveToTheLastRunningStep(t *testing.T) {
	f := newC4Fixture(t, "wi_w521")
	workflowtest.WriteLane(t, filepath.Join(f.capDir, capsule.DirWorkflows), "alpha", w521Lane)
	idn := capsule.Identity{WorkItemID: f.wi, Project: "proj", BundleName: "alpha"}
	raw, _ := json.Marshal(idn)
	if err := os.WriteFile(filepath.Join(f.capDir, capsule.CapsuleJSON), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	f.writeExecRecord(t, map[string]*capsule.Step{"p": {Status: capsule.StepInProgress, Round: 1}}, nil)
	setSkipped := func(ids ...string) {
		t.Helper()
		if ids == nil {
			ids = []string{}
		}
		raw, _ := json.Marshal(ids)
		path := filepath.Join(f.capDir, filepath.FromSlash(capsule.SkippedStepsJSON))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	rec := map[string]any{"name": "rec", "value": json.RawMessage(`{"state":"x"}`)}

	// an empty set: nothing moves
	setSkipped()
	if got := laneHubAllow(stepJob{capsuleDir: f.capDir, step: "p"}); got != nil {
		t.Fatalf("empty set: p's whitelist %v, want nothing", got)
	}
	if got := laneHubAllow(stepJob{capsuleDir: f.capDir, step: "m"}); strings.Join(got, ",") != "wrap-attrs" {
		t.Fatalf("empty set: m's whitelist %v, want [wrap-attrs]", got)
	}
	f.as("p", 1)
	if code, body := c4Call(t, f.td, "hub.wrap-attrs", rec); code != 403 {
		t.Fatalf("empty set: p's wrap-attrs = %d %s, want 403", code, body)
	}

	// the set names m: p, the last step that runs, owes m's wrap attr
	setSkipped("m")
	if got := laneHubAllow(stepJob{capsuleDir: f.capDir, step: "p"}); strings.Join(got, ",") != "wrap-attrs" {
		t.Fatalf("m skipped: p's whitelist %v, want [wrap-attrs]", got)
	}
	if got := laneHubAllow(stepJob{capsuleDir: f.capDir, step: "a"}); got != nil {
		t.Fatalf("m skipped: a's whitelist %v, want nothing", got)
	}
	before := f.handoffDoc(t)
	code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()})
	if code != 400 || !strings.Contains(body, "wrap attr(s) rec") || !strings.Contains(body, "pf step hub wrap-attrs") {
		t.Fatalf("m skipped: p's done without rec = %d %s, want 400 naming rec", code, body)
	}
	if f.handoffDoc(t) != before {
		t.Fatal("the refused done changed handoff.md")
	}
	if code, body := c4Call(t, f.td, "hub.wrap-attrs", rec); code != 200 {
		t.Fatalf("m skipped: p's wrap-attrs = %d %s, want 200", code, body)
	}
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 200 {
		t.Fatalf("m skipped: p's done after registering rec = %d %s, want 200", code, body)
	}
}
