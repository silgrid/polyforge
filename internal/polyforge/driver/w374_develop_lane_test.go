package driver

// w374_develop_lane_test.go (D1): the develop lane — the
// default development lane that supersedes feature-full, chore, bug-fix
// and autopilot — loads through the same LoadWorkflow / spawn-adapter path
// as every shipped lane and runs on today's driver: build → review → sync
// → verification → publish → merge, every gate fresh and rewinding to
// build — merge's to sync. (The declared output schemas and verifies went
// with workflow contract 4: what each step reads first and hands off is
// its skill's prose, pinned in the rendered prompts.)

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

const developBundle = "develop"

func TestShippedDevelopLaneShape(t *testing.T) {
	dir := filepath.Join(shippedWorkflowsDir, developBundle)
	wf, err := LoadWorkflow(dir)
	if err != nil {
		t.Fatalf("develop must load through LoadWorkflow: %v", err)
	}
	lane, err := workflow.LoadStrict(dir)
	if err != nil {
		t.Fatalf("develop must load strict: %v", err)
	}
	want := []string{"build", reviewStepID, "sync", verificationStepID, publishStepID, "merge"}
	if got := laneOrder(t, developBundle); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("develop's order must be %v, got %v", want, got)
	}
	// rewind: ONE total budget of 3 (the max_rewinds; the
	// contract has no per-edge bound; each gate names its rewind target
	// in on_fail)
	if wf.MaxRewinds != 3 {
		t.Fatalf("develop must load with max_rewinds: 3, got %d", wf.MaxRewinds)
	}
	// build (workflow contract v2 PR 12): a recalling non-gate with a wall
	// budget on the build skill — no check: its runs are its own, recorded
	// in units.json (contract 4: it recalls through the {{recall}} fragment
	// its definition includes; what it hands off — units.json among it — is
	// its skill's, TestShippedDevelopLanePromptsRender)
	b := lane.Step("build")
	def, err := readShippedDefinition(t, dir, "build")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(def, "## Prior experience (`pf step recall`)") || b.Gate || b.WallMinutes <= 0 || b.Skill != "build" {
		t.Fatalf("develop/build must be a recalling non-gate with a wall budget on the build skill: %+v", b)
	}
	// review (PR 12): the review skill's facts.py lines under the heavy
	// admission (the replays) and a 20-minute tool window (what it checks
	// of build's hand-off first is its skill's since contract 4)
	r := lane.Step(reviewStepID)
	if r.Skill != "review" || r.Resources == nil || !r.Resources.Heavy() || len(r.Resources.Locks) != 0 || r.ToolMinutes != 20 || r.WallMinutes != 30 {
		t.Fatalf("develop/review must follow the review skill under the heavy admission (tool 20 / wall 30): %+v", r)
	}
	// the gates and their rewind edges: review, verification and publish
	// FAIL to build, merge to sync; sync is no gate (workflow contract 2:
	// a real conflict is resolved in place by the sync agent)
	for id, target := range map[string]string{reviewStepID: "build", verificationStepID: "build", publishStepID: "build", "merge": "sync"} {
		s := lane.Step(id)
		if !s.Gate || s.OnFailTarget != target {
			t.Fatalf("develop/%s must be a gate with on_fail %s: %+v", id, target, s)
		}
	}
	// every step is an agent step; the four mechanical ones run on the
	// cheap tier and follow a shared skill (workflow contract 2, design
	// §4.1)
	for id, skill := range map[string]string{"sync": "sync", verificationStepID: "verify", publishStepID: "publish", "merge": "merge"} {
		s := lane.Step(id)
		if s.Skill != skill {
			t.Fatalf("develop/%s must follow the %s skill: %+v", id, skill, s)
		}
		if s.ModelTier != "cheap" {
			t.Fatalf("develop/%s must run on the cheap tier", id)
		}
	}
	// (contract 3's verifies — sync of review's last_review_round,
	// verification of sync's result, publish of the verified result or its
	// waiver and review's verdict, merge of publish's repos — are each
	// skill's 开工前读什么、核对什么 since contract 4:
	// TestShippedDevelopLanePromptsRender.)
	if sync := lane.Step("sync"); sync.Gate || sync.OnFailTarget != "" || sync.ToolMinutes != 20 {
		t.Fatalf("develop/sync must be a non-gate with a 20-minute tool window: %+v", sync)
	}
	// polyforge#52: the window fits the full bar an integration batch's fix
	// ticket runs (its attrs carry the integration_fix marker); a light run
	// ends in its own time — the window is a bound, never a spend
	if v := lane.Step(verificationStepID); !v.Waivable || v.Resources == nil || !v.Resources.Heavy() || len(v.Resources.Locks) != 0 || v.ToolMinutes != 90 || v.WallMinutes != 150 {
		t.Fatalf("develop/%s must be waivable, take the heavy admission and no lock (tool 90 / wall 150): %+v", verificationStepID, v)
	}
	if p := lane.Step(publishStepID); p.Waivable || p.Resources != nil {
		t.Fatalf("develop/%s must be a non-waivable gate taking no resource: %+v", publishStepID, p)
	}
	// merge registers the landing as the wrap's `integration` attr (contract
	// 3's `handoff: landing`): wrap_attrs: [integration]
	if m := lane.Step("merge"); m.Waivable || strings.Join(m.WrapAttrs, ",") != "integration" || m.Resources == nil || m.Resources.Heavy() || !m.Resources.Holds(workflow.LockMerge) || m.ToolMinutes != 10 {
		t.Fatalf("develop/merge must hold the merge lock, a 10-minute tool window and register the landing as the wrap's integration attr (wrap_attrs: [integration]): %+v", m)
	}
}

// Every develop step renders through the spawn adapter from a materialized
// capsule (the bundle beside the shipped _shared/), carrying its lane facts
// — since workflow contract 4 among them what it reads and checks first
// (contract 3's verifies and # Verify first) and what it hands off.
// Phrases are matched with whitespace folded (a rewrap keeps them pinned);
// `pf step ask` alone is not asserted — the step header every prompt
// carries names it.
func TestShippedDevelopLanePromptsRender(t *testing.T) {
	for step, wants := range map[string][]string{
		// (what build.md, its skill and its fragments state that
		// internal/contracttest/w374_develop_driver_contract_test.go,
		// internal/contracttest/w403_develop_grill_e2e_contract_test.go and
		// internal/contracttest/owner_review_gate_contract_test.go pin — confirm_plan, the
		// `carries:` header, the owner-review section, units.json and its
		// replay/verify commands, when build asks, the owner-replied plan —
		// is pinned there once)
		"build": {
			"the judged list — each list item one criterion",
			"red on an assertion", "`artifacts/self-review.md`",
			"`bug`, `ac_gap`,",
			// workflow contract v2 PR 12: the build skill records the runs
			"# Skill",
			"\"run\": {\"repo\": \"<name under repos/>\"",
			// contract 3's `memory: recall`
			"## Prior experience (`pf step recall`)",
			// contract 3's declared artifact units.json: what build hands off
			// (polyforge#112: the close analysis check adds artifacts/facts/analysis.json;
			// polyforge#131: the close's facts checks add build.json and carries.json)
			"**Evidence**: `artifacts/plan.md`, `artifacts/build/units.json`, `artifacts/build/change-analysis.json`, `artifacts/build/units-check.json`, `artifacts/build/fast-checks/<repo>.json`, `artifacts/build/scoped-close/<repo>.json`, `artifacts/facts/plan-conformance.json`, `artifacts/facts/build.json`, `artifacts/facts/carries.json`, `artifacts/facts/analysis.json`, `artifacts/self-review.md`",
			// contract 3's "never complete on the same tree … with the same
			// units.json": since contract 4 build may hand an unchanged tree
			// off only with a reason per point, and review judges it
			"you may hand off on the same tree only with, per point, the evidence for that reading in 回应上一轮",
			"hand off a round a FAIL opened on an unchanged tree with an unchanged units.json and no reason per point",
			// a FAIL naming a unit's record is fixed in units.json, outside the tree
			"rewriting that unit in `artifacts/build/units.json` is the change",
			"never one the empty string matches (`.*`, `^`, `a*`: verification fails such a unit without running it)",
			// the build close's scoped selection (polyforge#164): the same
			// light-tier selection verification plans, run over the final
			// tree; a red or incomplete result never hands off done
			"the build close's scoped selection — the same light-tier selection verification plans, one shared implementation, over the final tree",
			"for a work item whose verification runs the light tier (an integration batch's fix ticket runs the full bar and closes without one)",
			"A red or `incomplete` result (the engine answered full, or a selected command cannot run) never hands off `done` without the owner's ruling",
			"Merge each touched repo's `details.selected` of its green result into the change analysis's `selected`",
		},
		reviewStepID: {
			"## Blocking classes",
			"Coverage, criterion by criterion (`ac_gap`)", "Tests after their red commit (`test_weakened`)",
			"the work item's acceptance criteria (or the plan's owner-confirmed `## Acceptance` list)",
			// workflow contract v2 PR 12: the review skill's facts (its three
			// facts.py lines are internal/contracttest/w374_develop_driver_contract_test.go's)
			"# Skill",
			"artifacts/build/units.json",
			// contract 3's verifies of build's plan, units, summary and answers
			"`artifacts/plan.md` and `artifacts/build/units.json` exist and the units file parses; build's 关键决定和理由 records every decision it asked the owner for, with the owner's words.",
			// contract 3's `steps.build.answers`: the owner messages quoted in build's section headers
			"in its header the owner messages build received (verbatim, stamped)",
			// contract 3's last_review_round: the heads the earlier review section's header stamps
			"its header stamps the heads that round judged (each repo's end commit)",
			"When every task repo's `git -C repos/<name> rev-parse HEAD` equals the head that round judged and `sha256sum artifacts/build/units.json` equals that line, the build since changed nothing",
			"the line `units.json sha256: <digest>` (`sha256sum artifacts/build/units.json`)", "Every round rewrites it.",
			"A `test` entry must exercise that criterion's behaviour and fail without the change.",
			"An `existing` entry passes without the change by definition: it must already assert exactly the behaviour the criterion names",
		},
		publishStepID: {"**You do not merge", "# Skill", "**`## Follow-up`**", "conflict resolved in sync: <files>", "artifacts/publish.json", "do not merge it by hand",
			// contract 3's verifies of the verified result (or its waiver) and review's verdict
			"**The verified result file.** Per task repo, its `heads` entry equals `git -C repos/<name> rev-parse HEAD` — always, waived or not.",
			"**The review passed.** Review's latest section is stamped `done`",
			// contract 3's `pf ask`: the owner-review question
			"`pf step ask` the owner once — per repo the PR link, each line verbatim and whether each path exists",
			"on develop, the judged acceptance list (the ticket's, else the plan's `## Acceptance`) and the plan's `## Coverage` table"},
		"sync": {"# Skill", "`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/sync.py\" --json --out artifacts/sync/result.json`", "`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/sync.py\" --continue --json --out artifacts/sync/result.json`", "`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/sync.py\" --abort --json --out artifacts/sync/result.json`",
			// contract 3's verify-first of review's last_review_round
			"**The tree review judged.** Read the latest `review` section of `artifacts/handoff.md`: its header stamps each task repo's end commit",
			"Keep **both** sides' intent",
			"Write or edit `artifacts/sync/result.json` or `runtime-state/asks.jsonl`; commit, push, reset, rebase, stash, cherry-pick, check out or `git merge`"},
		verificationStepID: {"# Skill", "`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/verify.py\" --tier auto --unit-runs artifacts/build/units.json --analysis artifacts/build/change-analysis.json --json --out artifacts/verify/result.json`",
			// contract 3's verify-first of sync's result
			"**The sync's hand-off.** Read sync's latest section of `artifacts/handoff.md` and `artifacts/sync/result.json`",
			// the downstream check of sync's result file reads its argv
			"its `argv` is the arguments of the sync skill's start line or of its continue line",
			"Same-tree rule",
			// contract 3's `intent: {kind: waive}`
			"the owner accepts the red: `waive --per-owner <the reply's at>`",
			"is not run again when `artifacts/build/units.json` is unchanged too (its sha256 equals the `units.json sha256:` line of `artifacts/verify/report.md`) and the change analysis is unchanged (its `change-analysis sha256:` line of that report)",
			// contract 3's optional input artifacts/plan.md
			"`artifacts/plan.md` (`## Units`, when the lane has a plan), build's change analysis `artifacts/build/change-analysis.json` (what the change altered and which existing tests already pin it) and build's latest section: what the change touched, so a red can be placed.",
			"a missing or unparseable analysis is this gate's FAIL",
			"A red you can place on the change — a unit of the plan (a failing `<repo>:unit-run:<id>` check is that unit's own run), a listed test of the change analysis (a failing `<repo>:analysis:<id>` check is a test the analysis listed as already pinning this change: the change's, on build's own word), a file the change touched"},
		"merge": {"# Skill", "`\"$PF_PYTHON\" \"$PF_SHARED_DIR/scripts/merge.py\" --publish artifacts/publish.json --verified artifacts/verify/result.json --landing-out artifacts/merge/landing.json --json --out artifacts/merge/result.json`",
			"**The verified result file is the gate's line's.** Its `argv` is exactly the arguments",
			// contract 3's verify-first of publish's repos against the verified heads
			"**publish's hand-off against the verified heads.**",
			// contract 3's `state: landed` handoff: the landing record the command writes, registered for the wrap
			"`{state: landed, repos: [{repo, pr, head, light_verified_head, merge_commit, accepted?}], merge_commits}`",
			"`pf step hub wrap-attrs integration @artifacts/merge/landing.json`",
			// contract 3's `merged_elsewhere` mapping is the command's: the landing rules
			"a PR already merged is taken only by the landing rules (its branch side the verified head, or a merge commit on the default branch containing it)",
			"you never write the landing record yourself",
			"`accepted: true`"},
	} {
		p := spawnStepPrompt(t, developBundle, step)
		flat := strings.Join(strings.Fields(p), " ")
		for _, w := range wants {
			if !strings.Contains(flat, strings.Join(strings.Fields(w), " ")) {
				t.Errorf("develop/%s prompt must carry %q", step, w)
			}
		}
		if strings.Contains(p, "{{") {
			t.Errorf("develop/%s prompt carries an unrendered placeholder", step)
		}
	}
}

// A review FAIL on develop rewinds to build — one charged rewind on the
// review→build edge — on today's driver.
func TestDevelopReviewFailRewindsBuild(t *testing.T) {
	wf, err := LoadWorkflow(filepath.Join(shippedWorkflowsDir, developBundle))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_dev", CapsuleDir: dir, BundleName: developBundle},
		ExecPath: filepath.Join(dir, capsule.ExecutionJSON),
		Workflow: wf,
		Spawner:  &fakeSpawner{},
		Log:      &eventLog{},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	mustInit(t, d)
	driveGateRound(t, d, wf, reviewStepID, 1)
	x := d.Snapshot()
	if x.WorkflowStatus != StatusRunning || x.RewindStats.Count != 1 {
		t.Fatalf("a review FAIL must rewind once, got %q count %d", x.WorkflowStatus, x.RewindStats.Count)
	}
	if r := x.RewindStats.Rewinds[0]; r.Gate != reviewStepID || r.ToStep != "build" {
		t.Fatalf("the rewind must go review → build: %+v", r)
	}
	if st := x.Steps["build"]; st == nil || st.Status != capsule.StepPending {
		t.Fatalf("build must be pending: %+v", st)
	}
}

// TestDevelopMergeWritesTheWrapBeforeItDelivers: merge is the lane's last
// agent step, so it owes the wrap summary — and the merge skill orders it
// BEFORE the pass is handed off: once `pf step handoff` is accepted the
// step stops (no further tool call; the step header), so a summary written
// after handing off never lands. Contract 3's "deliver `completed`,
// `verdict: pass`" is the exit-0 route's `hand off done` since contract 4.
func TestDevelopMergeWritesTheWrapBeforeItDelivers(t *testing.T) {
	p := spawnStepPrompt(t, developBundle, "merge")
	at := strings.Index(p, "# Skill")
	if at < 0 {
		t.Fatalf("develop/merge renders its skill:\n%s", p)
	}
	skill := p[at:]
	flat := strings.ToLower(strings.Join(strings.Fields(skill), " "))
	// the exit-0 route of the skill's procedure
	pass := strings.Index(flat, "- **0** —")
	end := strings.Index(flat, "- **1** —")
	if pass < 0 || end < pass {
		t.Fatalf("the merge skill must route on exit 0 then exit 1:\n%s", skill)
	}
	route := flat[pass:end]
	wrap := strings.Index(route, "write the wrap summary")
	deliver := strings.Index(route, "hand off `done`")
	if wrap < 0 || deliver < 0 || wrap > deliver {
		t.Fatalf("the merge skill's pass route must have the wrap summary written before the pass is handed off (wrap at %d, hand-off at %d):\n%s", wrap, deliver, route)
	}
}
