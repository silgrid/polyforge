package daemon

// w241_facts_proof_test.go (polyforge#241): a step whose lane declares
// `facts_proof` hands off done only with a passing mechanical facts
// summary at the repo's HEAD — the file `facts.py handoff` writes. The
// refusal names every hit the result carries, one line each, beside the
// result file; a refusal writes nothing and spends no rewind (polyforge#211
// left that rule untested). The tests run the production hand-off path over
// the c4 fixture plus the new key.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// w241Lane is c4Lane with the mechanical facts proof on build. The command
// is deliberately a name no tool has: the daemon only relays it.
const w241Lane = `max_rewinds: 3
uncounted_rewinds:
  reason_contains: the default branch moved since verification
  max: 1
steps:
  - id: build
    facts_proof:
      result: artifacts/build/handoff-facts/{repo}.json
      command: run-facts-handoff --repo {repo} --out artifacts/build/handoff-facts/{repo}.json
  - id: review
    gate: true
    on_fail: { target: build }
  - id: sync
  - id: verification
    gate: true
    waivable: true
    on_fail: { target: build }
  - id: publish
  - id: merge
    resources: { locks: [merge] }
    wrap_attrs: [integration]
`

func newW241Fixture(t *testing.T, wi string) *c4Fixture {
	t.Helper()
	f := newC4Fixture(t, wi)
	workflowtest.WriteLane(t, filepath.Join(f.capDir, capsule.DirWorkflows), "develop", w241Lane)
	return f
}

// writeW241Result writes a handoff result with the hits the refusal must
// return (verdict decides the exit code).
func writeW241Result(t *testing.T, capDir, rel, verdict string, heads map[string]string, hits []map[string]any) {
	t.Helper()
	exit := 0
	switch verdict {
	case "fail":
		exit = 1
	case "error":
		exit = 2
	}
	raw, err := json.Marshal(map[string]any{
		"schema": "pf-step-result/1", "command": "facts.py", "version": "test",
		"argv": []string{}, "round": "1",
		"started_at": "2026-11-23T00:00:00Z", "ended_at": "2026-11-23T00:00:01Z",
		"exit_code": exit, "verdict": verdict, "heads": heads,
		"summary": "2 hand-off gate hit(s): D6 repos/app pkg/foo_test.go: existing test; D1 repos/app unit u1: no red evidence",
		"details": map[string]any{"hits": hits, "not_judged": []any{}},
		"logs":    []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(capDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// rewindCount answers the execution record's rewind tally.
func rewindCount(t *testing.T, capDir string) int64 {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, capsule.ExecutionJSON))
	if err != nil {
		t.Fatal(err)
	}
	var ex capsule.Execution
	if err := json.Unmarshal(raw, &ex); err != nil {
		t.Fatal(err)
	}
	return ex.RewindStats.Count
}

func TestW241RefusesDoneWithoutAResult(t *testing.T) {
	f := newW241Fixture(t, "wi_241a")
	f.as("build", 1)
	before := f.handoffDoc(t)
	code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()})
	if code != 400 {
		t.Fatalf("build without a mechanical facts result = %d %s, want 400", code, body)
	}
	for _, want := range []string{
		"repos/app",
		"no mechanical facts result at artifacts/build/handoff-facts/app.json",
		"run-facts-handoff --repo app --out artifacts/build/handoff-facts/app.json",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, body)
		}
	}
	if f.handoffDoc(t) != before {
		t.Errorf("a refused hand-off wrote the document")
	}
}

func TestW241RefusesAFailingSummaryAndReturnsEveryHit(t *testing.T) {
	f := newW241Fixture(t, "wi_241b")
	f.as("build", 1)
	writeW241Result(t, f.capDir, "artifacts/build/handoff-facts/app.json", "fail",
		map[string]string{"app": f.c2}, []map[string]any{
			{"item": "D6", "repo": "app", "path": "pkg/foo_test.go",
				"reason": "existing test modified but the plan's write set does not declare it"},
			{"item": "D1", "repo": "app", "unit": "u1",
				"reason": "its run exited 0 on the tree of test_commit abcdef0"},
		})
	before := f.handoffDoc(t)
	rewinds := rewindCount(t, f.capDir)
	code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()})
	if code != 400 {
		t.Fatalf("build with a failing mechanical facts result = %d %s, want 400", code, body)
	}
	for _, want := range []string{
		"repos/app",
		"artifacts/build/handoff-facts/app.json",
		"D6",
		"pkg/foo_test.go",
		"existing test modified",
		"D1",
		"unit u1",
		"exited 0 on the tree of test_commit",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal does not return the hit %q:\n%s", want, body)
		}
	}
	if f.handoffDoc(t) != before {
		t.Errorf("a refused hand-off wrote a section")
	}
	if got := rewindCount(t, f.capDir); got != rewinds {
		t.Errorf("a refused done spent a rewind: %d -> %d", rewinds, got)
	}
}

func TestW241AcceptsAPassAtHead(t *testing.T) {
	f := newW241Fixture(t, "wi_241c")
	f.as("build", 1)
	writeW241Result(t, f.capDir, "artifacts/build/handoff-facts/app.json", "pass",
		map[string]string{"app": f.c2}, nil)
	code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()})
	if code != 200 {
		t.Fatalf("a pass at HEAD = %d %s, want 200", code, body)
	}
	if doc := f.handoffDoc(t); !strings.Contains(doc, "done") {
		t.Errorf("the accepted hand-off wrote no section:\n%s", doc)
	}
}

func TestW241RefusesAStaleHead(t *testing.T) {
	f := newW241Fixture(t, "wi_241d")
	f.as("build", 1)
	writeW241Result(t, f.capDir, "artifacts/build/handoff-facts/app.json", "pass",
		map[string]string{"app": f.c1}, nil)
	code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()})
	if code != 400 || !strings.Contains(body, f.c2) {
		t.Fatalf("a stale head = %d %s, want 400 naming HEAD %s", code, body, f.c2)
	}
}

func TestW241SkipsAnUnchangedRepo(t *testing.T) {
	f := newW241Fixture(t, "wi_241e")
	// build's round starts at c2, and HEAD is still c2: no change, no proof
	f.writeExec(t, map[string]*capsule.Step{
		"build": {Status: capsule.StepInProgress, Round: 1, StartHeads: &capsule.StartHeads{Round: 1, Heads: map[string]string{"app": f.c2}}},
	})
	f.as("build", 1)
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 200 {
		t.Fatalf("build at its start commit = %d %s, want 200", code, body)
	}
}

func TestW241OwnerRelease(t *testing.T) {
	const at = "2026-11-23T07:00:00.000000001Z"
	words := "the mechanical facts are the base's: release build"
	f := newW241Fixture(t, "wi_241f")
	f.as("build", 1)
	writeW241Result(t, f.capDir, "artifacts/build/handoff-facts/app.json", "fail",
		map[string]string{"app": f.c2}, nil)
	f.ownerSays(t, "build", at, words)
	code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body(), "per_owner": at})
	if code != 200 {
		t.Fatalf("the owner's release = %d %s, want 200", code, body)
	}
	rec := stepcall.HandoffOf(f.ledger(t), "build", 1)
	if rec == nil || rec.Handoff.PerOwner != at {
		t.Fatalf("the accepted hand-off does not record the cited message: %+v", rec)
	}
}
