package daemon

// stephandoff_c2_test.go (workflow contract 4 stage C2):
// the rules `pf step handoff` and `pf step hub` take from the lane and the
// record once the driver reacts to them — a gate's fail without the
// owner's word refused once the rewind budget is spent (design #483
// §5.1), a done refused until its round registered every wrap attr the
// step declares (owner ruling R7), the hub whitelist being the lane's
// `hub:` list (and wrap-attrs for `wrap_attrs`), and each repo's start
// commit in the stamp being the step round's own.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/pendingwrites"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// writeExecRecord writes the execution record with steps and rewinds.
func (f *c4Fixture) writeExecRecord(t *testing.T, steps map[string]*capsule.Step, rewinds []capsule.RewindRecord) {
	t.Helper()
	ex := capsule.Execution{WorkItemID: f.wi, WorkflowStatus: capsule.WorkflowStatusRunning, Steps: steps,
		RewindStats: capsule.RewindStats{Count: int64(len(rewinds)), Rewinds: rewinds}}
	raw, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.capDir, capsule.ExecutionJSON), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestStepHandoffFailRefusedOnceTheBudgetIsSpent: with the lane's
// max_rewinds (3) spent by automatic rewinds, a gate's fail that does not
// cite the owner is refused naming the budget and `pf step ask`, and
// leaves the document; the same fail citing an owner message passes. The
// owner's own rewinds never spend the budget.
func TestStepHandoffFailRefusedOnceTheBudgetIsSpent(t *testing.T) {
	f := newC4Fixture(t, "wi_c2b")
	steps := map[string]*capsule.Step{"review": {Status: capsule.StepInProgress, Round: 4}}
	owner := []capsule.RewindRecord{{Gate: "review", ToStep: "build", Class: capsule.RewindOwner}, {Gate: "review", ToStep: "build", Class: capsule.RewindOwner}, {Gate: "review", ToStep: "build", Class: capsule.RewindOwner}}
	f.writeExecRecord(t, steps, owner)
	f.as("review", 1)
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "fail", "body": c4Body()}); code != 200 {
		t.Fatalf("owner rewinds spend no budget: a fail = %d %s", code, body)
	}
	charged := []capsule.RewindRecord{{Gate: "review", ToStep: "build", Class: capsule.RewindCharged}, {Gate: "verification", ToStep: "build", Class: capsule.RewindCharged}, {Gate: "review", ToStep: "build", Class: capsule.RewindCharged}}
	f.writeExecRecord(t, steps, charged)
	f.as("review", 2)
	before := f.handoffDoc(t)
	code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "fail", "body": c4Body(stepcall.ReentrySectionOf(stepcall.LangZhCN))})
	if code != 400 || !strings.Contains(body, "rewind budget is spent") || !strings.Contains(body, "3 of max_rewinds 3") || !strings.Contains(body, "pf step ask") || !strings.Contains(body, "--per-owner") {
		t.Fatalf("a fail on a spent budget = %d %s, want 400 naming the budget and pf step ask", code, body)
	}
	if f.handoffDoc(t) != before {
		t.Fatal("the refused fail changed handoff.md")
	}
	f.ownerSays(t, "review", "2026-11-11T12:00:00.000000001Z", "再给一轮。")
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "fail", "body": c4Body(stepcall.ReentrySectionOf(stepcall.LangZhCN)), "per_owner": "2026-11-11T12:00:00.000000001Z"}); code != 200 {
		t.Fatalf("a fail citing the owner on a spent budget = %d %s", code, body)
	}
	if rec := stepcall.HandoffOf(f.ledger(t), "review", 2); rec == nil || rec.Handoff.Status != "fail" || rec.Handoff.PerOwner == "" {
		t.Fatalf("the accepted fail's record: %+v", rec)
	}
}

// TestStepHandoffDoneNeedsTheDeclaredWrapAttrs (owner ruling R7): a step
// declaring wrap_attrs hands off done only once its round registered each
// one; a registration of another round, or one a hand-off dropped, does
// not count; the declaration alone lets the step call wrap-attrs and
// nothing else.
func TestStepHandoffDoneNeedsTheDeclaredWrapAttrs(t *testing.T) {
	f := newC4Fixture(t, "wi_c2w")
	f.writeExecRecord(t, map[string]*capsule.Step{"merge": {Status: capsule.StepInProgress, Round: 2}}, nil)
	f.as("merge", 1)
	refused := func(why string) {
		t.Helper()
		before := f.handoffDoc(t)
		code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()})
		if code != 400 || !strings.Contains(body, "wrap attr(s) integration") || !strings.Contains(body, "pf step hub wrap-attrs") {
			t.Fatalf("%s: done = %d %s, want 400 naming the missing wrap attr", why, code, body)
		}
		if f.handoffDoc(t) != before {
			t.Fatalf("%s: the refused done changed handoff.md", why)
		}
	}
	refused("nothing registered")
	// the declaration allows wrap-attrs, never another write
	if code, body := c4Call(t, f.td, "hub.file", map[string]any{"key": "k", "lane": "develop", "goal": "g", "content": "c"}); code != 403 {
		t.Fatalf("a file from a step whose hub list is empty = %d %s", code, body)
	}
	// a registration of an earlier round does not count
	if _, err := stepcall.Append(f.capDir, stepcall.Line{Kind: stepcall.KindResult, Step: "merge", Round: 1, Spawn: 1, Verb: "hub.wrap-attrs", Outcome: stepcall.OutcomeOK,
		Pending: []stepcall.Op{{ID: "1.1", Verb: stepcall.HubWrapAttrs, Name: "integration", Value: json.RawMessage(`{}`)}}}); err != nil {
		t.Fatal(err)
	}
	refused("an earlier round's registration")
	// this round's registration, then dropped by a hand-off: does not count
	if code, body := c4Call(t, f.td, "hub.wrap-attrs", map[string]any{"name": "integration", "value": json.RawMessage(`{"state":"landed"}`)}); code != 200 {
		t.Fatalf("wrap-attrs = %d %s", code, body)
	}
	if _, err := pendingwrites.Drop(f.capDir, "merge", 2, "the round handed off blocked"); err != nil {
		t.Fatal(err)
	}
	refused("a dropped registration")
	if code, body := c4Call(t, f.td, "hub.wrap-attrs", map[string]any{"name": "integration", "value": json.RawMessage(`{"state":"landed"}`)}); code != 200 {
		t.Fatalf("wrap-attrs = %d %s", code, body)
	}
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 200 {
		t.Fatalf("done after registering the wrap attr = %d %s", code, body)
	}
}

// TestStepHubWhitelistIsTheLanesHub: the production whitelist is the
// calling step's `hub.verbs` in the capsule's lane snapshot, plus
// wrap-attrs for `wrap_attrs` and attrs for `hub.attrs` (whose keys are
// the attrs whitelist); a step declaring none calls no verb.
func TestStepHubWhitelistIsTheLanesHub(t *testing.T) {
	f := newC4Fixture(t, "wi_c2h")
	lane := filepath.Join(f.capDir, capsule.DirWorkflows, "develop", workflow.WorkflowFile)
	raw, err := os.ReadFile(lane)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(lane, []byte(strings.Replace(string(raw), "  - id: sync\n", "  - id: sync\n    hub: { verbs: [get, batch], attrs: [integration] }\n", 1)), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		step string
		want []string
	}{{"sync", []string{"get", "batch", "attrs"}}, {"merge", []string{"wrap-attrs"}}, {"build", nil}, {"nowhere", nil}} {
		got := laneHubAllow(stepJob{capsuleDir: f.capDir, step: c.step})
		if strings.Join(got, ",") != strings.Join(c.want, ",") {
			t.Errorf("%s: whitelist %v, want %v", c.step, got, c.want)
		}
	}
	if got := laneHubAttrs(stepJob{capsuleDir: f.capDir, step: "sync"}); strings.Join(got, ",") != "integration" {
		t.Errorf("sync: attrs whitelist %v, want [integration]", got)
	}
	if got := laneHubAttrs(stepJob{capsuleDir: f.capDir, step: "merge"}); got != nil {
		t.Errorf("merge: attrs whitelist %v, want none", got)
	}
	if got := laneHubAllow(stepJob{capsuleDir: t.TempDir(), step: "sync"}); got != nil {
		t.Errorf("an unreadable lane snapshot allows %v, want nothing", got)
	}
	f.as("build", 1)
	if code, body := c4Call(t, f.td, "hub.get", map[string]any{"wi": "wi_x"}); code != 403 || !strings.Contains(body, "may not call it") {
		t.Fatalf("a get from a step declaring no hub = %d %s", code, body)
	}
}

// TestStepHandoffStampsTheRoundsStartCommits: each repo's start commit is
// the one the step round started from (Step.StartHeads of that round) —
// never a commit recorded for another round, which leaves it unknown.
func TestStepHandoffStampsTheRoundsStartCommits(t *testing.T) {
	f := newC4Fixture(t, "wi_c2s")
	f.writeExecRecord(t, map[string]*capsule.Step{
		"build":  {Status: capsule.StepInProgress, Round: 2, StartHeads: &capsule.StartHeads{Round: 2, Heads: map[string]string{"app": f.c1}}},
		"review": {Status: capsule.StepInProgress, Round: 2, StartHeads: &capsule.StartHeads{Round: 1, Heads: map[string]string{"app": f.c1}}},
	}, nil)
	f.as("build", 1)
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 200 {
		t.Fatalf("build = %d %s", code, body)
	}
	if doc := f.handoffDoc(t); !strings.Contains(doc, "`app`: `"+f.c1+"` → `"+f.c2+"`（1 file changed, 2 insertions(+)）") {
		t.Fatalf("build's section does not span its round:\n%s", doc)
	}
	f.as("review", 2)
	before := f.handoffDoc(t)
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 200 {
		t.Fatalf("review = %d %s", code, body)
	}
	if section := strings.TrimPrefix(f.handoffDoc(t), before); !strings.Contains(section, "`app`: `（起点未知）` → `"+f.c2+"`（起点未知）") {
		t.Fatalf("another round's start heads must not stamp this one:\n%s", section)
	}
}

// TestWatchBlockedNamesAWriteFailedPark: a park on pending hub writes the
// daemon could not make shows as kind write_failed, with what releases it.
func TestWatchBlockedNamesAWriteFailedPark(t *testing.T) {
	ex := &capsule.Execution{WorkflowStatus: capsule.WorkflowStatusBlocked, CurrentStep: "close",
		BlockedReason: "write failed: step close handed off done and the daemon could not make its pending hub writes — aihub unreachable"}
	b := watchBlocked(ex)
	if b == nil || b.Kind != "write_failed" || b.Step != "close" || !strings.Contains(b.RequiredAction, "pf resume") {
		t.Fatalf("watchBlocked = %+v, want kind write_failed naming the resume", b)
	}
}

// TestStepHandoffReadsOnlyTheStepGraph: the hand-off check reads the lane
// snapshot's step graph (workflow.yaml) alone. A broken definition of a
// later step — the driver's to meet when it dispatches that step — does
// not refuse this step's done, while a wrap attr workflow.yaml declares is
// still required (R7); a workflow.yaml that cannot be judged refuses every
// checked status as unreadable.
func TestStepHandoffReadsOnlyTheStepGraph(t *testing.T) {
	f := newC4Fixture(t, "wi_c2g")
	f.writeExecRecord(t, map[string]*capsule.Step{
		"build": {Status: capsule.StepInProgress, Round: 1},
		"merge": {Status: capsule.StepInProgress, Round: 1},
	}, nil)
	laneDir := filepath.Join(f.capDir, capsule.DirWorkflows, "develop")
	review := filepath.Join(laneDir, "steps", "review.md")
	if err := os.Remove(review); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(review, "not-a-file"), 0o755); err != nil {
		t.Fatal(err)
	}
	f.as("merge", 1)
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 400 || !strings.Contains(body, "wrap attr(s) integration") {
		t.Fatalf("merge's done without its wrap attr, review's definition broken = %d %s, want R7's 400", code, body)
	}
	f.as("build", 2)
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 200 {
		t.Fatalf("build's done with review's definition broken = %d %s, want 200 (the driver meets the broken definition at review's dispatch)", code, body)
	}
	if err := os.WriteFile(filepath.Join(laneDir, workflow.WorkflowFile), []byte("contract: [\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	f.as("merge", 3)
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 500 || !strings.Contains(body, "unreadable") {
		t.Fatalf("a done over an unjudgeable workflow.yaml = %d %s, want 500 unreadable", code, body)
	}
}
