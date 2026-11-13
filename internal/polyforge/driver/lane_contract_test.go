package driver

// lane_contract_test.go: pins the shipped lanes' rewind
// contract — every gate edge of a shipped lane exhausts the lane's one
// budget, max_rewinds, constructively (contract 1: there is no per-edge
// bound). (The order and bound pins of the lanes removed
// went with them.)

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// laneOrder answers the lane's step ids in declaration order.
func laneOrder(t *testing.T, bundle string) []string {
	t.Helper()
	wf, err := LoadWorkflow(filepath.Join(shippedWorkflowsDir, bundle))
	if err != nil {
		t.Fatalf("%s: %v", bundle, err)
	}
	out := make([]string, 0, len(wf.Steps))
	for _, s := range wf.Steps {
		out = append(out, s.ID)
	}
	return out
}

// TestShippedMicroLaneShape (§15.2; workflow
// contract 2, change 10; contract 4): micro is fix → review → sync →
// evidence → publish → merge — no grill, plan or verification — every
// step an agent step: review, evidence and publish rewind to fix, merge to
// sync, sync is no gate; max_rewinds is 3. fix (mid) and review (mid)
// judge the bounds with facts.py micro-bounds (fix's skill, review's
// definition); fix recalls prior experience (the {{recall}} fragment its
// definition includes); the four mechanical steps run their skills on the
// cheap tier; evidence is the lane's one waivable gate, with the heavy
// admission and no lock; merge holds the merge lock and registers the
// landing as the wrap's `integration` attr (wrap_attrs). What each step
// reads first and hands off is prose since contract 4:
// TestShippedMicroLanePrompts.
func TestShippedMicroLaneShape(t *testing.T) {
	wf, err := LoadWorkflow(filepath.Join(shippedWorkflowsDir, "micro"))
	if err != nil {
		t.Fatal(err)
	}
	lane, err := workflow.LoadStrict(filepath.Join(shippedWorkflowsDir, "micro"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(laneOrder(t, "micro"), ","); got != "fix,review,sync,evidence,publish,merge" {
		t.Fatalf("micro's order must be fix → review → sync → evidence → publish → merge, got %v", got)
	}
	if wf.MaxRewinds != 3 {
		t.Fatalf("micro: max_rewinds %d, want 3", wf.MaxRewinds)
	}
	for id, target := range map[string]string{reviewStepID: "fix", "evidence": "fix", publishStepID: "fix", "merge": "sync"} {
		s := wf.step(id)
		if !s.Gate || s.OnFail == nil || s.OnFail.Target != target {
			t.Fatalf("micro/%s must be a gate with on_fail %s: %+v", id, target, s)
		}
	}
	for _, s := range wf.Steps {
		if s.ID != "evidence" && s.Waivable {
			t.Fatalf("micro/%s must not be waivable: only evidence is", s.ID)
		}
	}
	for id, want := range map[string][2]string{
		"fix": {"micro-bounds", "mid"}, reviewStepID: {"", "mid"}, "sync": {"sync", "cheap"},
		"evidence": {"evidence", "cheap"}, publishStepID: {"publish", "cheap"}, "merge": {"merge", "cheap"},
	} {
		if s := lane.Step(id); s.Skill != want[0] || s.ModelTier != want[1] || len(s.ModelCandidates) != 0 {
			t.Fatalf("micro/%s must follow skill %q on the %s tier: skill %q tier %q candidates %v", id, want[0], want[1], s.Skill, s.ModelTier, s.ModelCandidates)
		}
	}
	// fix recalls (contract 4: the {{recall}} fragment its definition
	// includes, not a `memory: recall` input).
	dir := filepath.Join(shippedWorkflowsDir, "micro")
	def, err := readStepDefinition(dir, "fix", shippedVars(t, dir, "fix"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "## Prior experience (`pf step recall`)") {
		t.Fatalf("micro/fix must recall prior experience (the {{recall}} fragment):\n%s", def)
	}
	fix := lane.Step("fix")
	if fix.Gate || fix.WallMinutes != 20 {
		t.Fatalf("micro/fix must be a non-gate with a 20-minute wall budget: %+v", fix)
	}
	if review := lane.Step(reviewStepID); review.WallMinutes != 15 {
		t.Fatalf("micro/review must declare a 15-minute wall budget: %+v", review)
	}
	sync := lane.Step("sync")
	if sync.Gate || sync.OnFailTarget != "" || sync.ToolMinutes != 20 {
		t.Fatalf("micro/sync must be a non-gate with a 20-minute tool window: %+v", sync)
	}
	ev := lane.Step("evidence")
	if !ev.Waivable || ev.Resources == nil || !ev.Resources.Heavy() || len(ev.Resources.Locks) != 0 || ev.ToolMinutes != 30 || ev.WallMinutes != 0 {
		t.Fatalf("micro/evidence must be waivable, take the heavy admission and no lock, and a 30-minute tool window: %+v", ev)
	}
	if p := lane.Step(publishStepID); p.Resources != nil || p.WallMinutes != 15 {
		t.Fatalf("micro/%s must take no resource and a 15-minute wall budget: %+v", publishStepID, p)
	}
	m := lane.Step("merge")
	if strings.Join(m.WrapAttrs, ",") != "integration" || m.Resources == nil || m.Resources.Heavy() || !m.Resources.Holds(workflow.LockMerge) || m.ToolMinutes != 10 {
		t.Fatalf("micro/merge must hold the merge lock, a 10-minute tool window and register the landing as the wrap's integration attr (wrap_attrs: [integration]): %+v", m)
	}
	if raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, "micro", workflow.WorkflowFile)); err != nil || strings.Contains(string(raw), "refile_lane") {
		t.Fatal("micro's out-of-bounds re-file is fix's question to the owner, never the daemon's refile_lane")
	}
}

// TestShippedMicroLanePrompts (workflow contract 2, change 10; contract
// 4): every micro step renders through the spawn adapter from a
// materialized capsule, carrying its skill (the pre-written command line
// verbatim) and what it checks of its upstream before it starts; the
// shared skills name micro's own result file and rewind target, never
// develop's verification or build. Phrases are matched with whitespace
// folded (a rewrap keeps them pinned); `pf step ask` alone is not
// asserted — the step header every prompt carries names it.
func TestShippedMicroLanePrompts(t *testing.T) {
	for step, wants := range map[string][]string{
		"fix": {"# Skill", "`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/facts.py\" micro-bounds --plan artifacts/fix/evidence.json --json --out artifacts/facts/micro-bounds.json`",
			"`artifacts/fix/evidence.json`", "re-file the work on the `develop` lane",
			// contract 3's `intent: {kind: cancel}` is the cancel hand-off on the owner's word
			"`--status cancel --per-owner <the reply's at>`",
			// contract 3's `pf ask`: the out-of-bounds question to the owner
			"`pf step ask` the owner — the refused repos and paths verbatim from `reason`",
			// contract 3's declared artifacts: what fix hands off as evidence
			// (its owner-review section: test/owner_review_gate_contract_test.go)
			"**Evidence**: `artifacts/fix/evidence.json`, `artifacts/facts/micro-bounds.json`"},
		reviewStepID: {"`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/facts.py\" micro-bounds --plan artifacts/fix/evidence.json --json --out artifacts/review/micro-bounds.json`",
			"artifacts/facts/micro-bounds.json",
			// contract 3's `class: code`: every blocking finding is fix's to repair
			"on this lane every blocking finding is `fix`'s to repair in its next round",
			"## Principles",
			// contract 3's `- step: fix.answers`: the owner replies quoted in fix's section header
			"an owner reply quoted in fix's section header that approved a narrower change",
			"what it drops is not missing"},
		"sync": {"# Skill", "`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/sync.py\" --json --out artifacts/sync/result.json`", "`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/sync.py\" --continue --json --out artifacts/sync/result.json`",
			// contract 3's verify-first of review's last_review_round: the heads review's section stamps
			"**The tree review judged.** Read the latest `review` section of `artifacts/handoff.md`: its header stamps each task repo's end commit",
			"micro's `fix`", "`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/sync.py\" --abort --json --out artifacts/sync/result.json`"},
		"evidence": {"# Skill", "`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/run.py\" --isolation host --plan-from artifacts/fix/evidence.json --json --out artifacts/run/result.json`",
			// the downstream check of sync's result file reads its argv
			"its `argv` is the arguments of the sync skill's start line or of its continue line",
			// contract 3's verify-first of sync's result
			"**The sync's hand-off.** Read sync's latest section of `artifacts/handoff.md` and `artifacts/sync/result.json`",
			"Same-tree rule",
			// contract 3's `intent: {kind: waive}`: the waive hand-off on the owner's word
			"the owner accepts the red: `waive --per-owner <the reply's at>`"},
		publishStepID: {"# Skill", "artifacts/run/result.json", "micro's `fix`",
			// contract 3's `steps.evidence.record.waived` and "belong to this round of the gate":
			// the waive stamped on the evidence's section, in the result's round
			"the verifying gate's latest section in `artifacts/handoff.md` is stamped `waive` **in the same round** as the result file's `round` (an earlier round's waiver never covers a later red)",
			"conflict resolved in sync: <files>", "artifacts/publish.json", "do not merge it by hand"},
		"merge": {"# Skill", "`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/merge.py\" --publish artifacts/publish.json --verified artifacts/run/result.json --landing-out artifacts/merge/landing.json --json --out artifacts/merge/result.json`",
			"**The verified result file is the gate's line's.** Its `argv` is exactly the arguments",
			// contract 3's verify-first of publish's repos against the evidence's heads
			"**publish's hand-off against the verified heads.**",
			// contract 3's `state: landed` handoff: the landing record and its registration
			"`{state: landed, repos: [{repo, pr, head, light_verified_head, merge_commit, accepted?}], merge_commits}`",
			"`pf step hub wrap-attrs integration @artifacts/merge/landing.json`",
			// the wrap summary is written before the pass is handed off
			"write the wrap summary (the step header's Memory section), then hand off `done`",
			"`accepted: true`"},
	} {
		p := spawnStepPrompt(t, "micro", step)
		flat := strings.Join(strings.Fields(p), " ")
		for _, w := range wants {
			if !strings.Contains(flat, strings.Join(strings.Fields(w), " ")) {
				t.Errorf("micro/%s prompt must carry %q", step, w)
			}
		}
		if strings.Contains(p, "{{") {
			t.Errorf("micro/%s prompt carries an unrendered placeholder", step)
		}
		for _, bad := range []string{"fast_lane", "pf bar-run", "resolved-conflict-review", "steps.verification."} {
			if strings.Contains(p, bad) {
				t.Errorf("micro/%s prompt names %q (the contract-1 micro path or develop's)", step, bad)
			}
		}
	}
}

// driveGateRound walks the lane to one gate FAIL: every step in
// [target, gate) completes (gates with a pass verdict), then the gate
// FAILs — one charged rewind of that gate→target edge. It answers the
// next spawn ordinal to use.
func driveGateRound(t *testing.T, d *Driver, wf *Workflow, gate string, n int64) int64 {
	t.Helper()
	gIdx, _ := wf.indexOf(gate)
	target := wf.step(gate).OnFail.Target
	tIdx, _ := wf.indexOf(target)
	for i := tIdx; i < gIdx; i++ {
		sid := wf.Steps[i].ID
		mustStart(t, d, sid, n)
		out := `{}`
		if wf.Steps[i].Gate {
			out = `{"verdict":"pass"}`
		}
		if err := d.transition(TransitionRequest{
			Kind: TransStepResult, StepID: sid, SpawnN: n,
			Result: &StepResult{WIID: d.wi.ID, StepID: sid, SpawnN: n, Outcome: OutcomeCompleted, Output: json.RawMessage(out)},
		}); err != nil {
			t.Fatalf("complete %s (%s, round at %d): %v", sid, gate, n, err)
		}
		n++
	}
	mustStart(t, d, gate, n)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: gate, SpawnN: n,
		Result: &StepResult{WIID: d.wi.ID, StepID: gate, SpawnN: n, Outcome: OutcomeGateFailed,
			Output: json.RawMessage(`{"verdict":"fail","fail_reason":"contract round"}`)},
	}); err != nil {
		t.Fatalf("fail %s (round %d): %v", gate, n, err)
	}
	return n + 1
}

// TestShippedLaneExhaustionIsConstructive: for every shipped lane and
// every gate→target edge, a constructive sequence of FAILs on that one
// edge lands max_rewinds rewinds, and the NEXT FAIL parks the workflow
// on the spent budget — never an infinite loop.
func TestShippedLaneExhaustionIsConstructive(t *testing.T) {
	for _, bundle := range []string{"micro"} {
		wf, err := LoadWorkflow(filepath.Join(shippedWorkflowsDir, bundle))
		if err != nil {
			t.Fatalf("%s: %v", bundle, err)
		}
		for _, s := range wf.Steps {
			if s.OnFail == nil {
				continue
			}
			t.Run(bundle+"/"+s.ID, func(t *testing.T) {
				dir := t.TempDir()
				d, err := New(Config{
					WI:       WIInfo{ID: "wi_lc", CapsuleDir: dir, BundleName: bundle},
					ExecPath: filepath.Join(dir, capsule.ExecutionJSON),
					Workflow: wf,
					Spawner:  &fakeSpawner{},
					Log:      &eventLog{},
				})
				if err != nil {
					t.Fatalf("New: %v", err)
				}
				mustInit(t, d)
				n := int64(1)
				for round := 0; round < wf.MaxRewinds; round++ {
					n = driveGateRound(t, d, wf, s.ID, n)
					if x := d.Snapshot(); x.RewindStats.Count != int64(round+1) {
						t.Fatalf("%s: round %d: count %d, want %d", s.ID, round, x.RewindStats.Count, round+1)
					}
				}
				// the budget is spent: the next FAIL not citing the owner is
				// refused at the call (RewindBudgetSpent)
				x := d.Snapshot()
				if RewindBudgetSpent(wf, x) == "" {
					t.Fatalf("%s: max_rewinds %d is spent after %d rewinds: %+v", s.ID, wf.MaxRewinds, x.RewindStats.Count, x.RewindStats)
				}
				for _, r := range x.RewindStats.Rewinds {
					if r.Gate != s.ID || r.ToStep != s.OnFail.Target || r.Class != capsule.RewindCharged {
						t.Fatalf("%s: every landed rewind must attribute to its edge: %+v", s.ID, x.RewindStats.Rewinds)
					}
				}
			})
		}
	}
}
