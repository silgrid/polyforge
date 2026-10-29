package driver

// rewind_gate_guard_test.go: criteria 5 and 6 at the driver
// level.
//
// Criterion 5 (the misleading continue-in-place prompt): a retry inside a
// rewound round whose task-repo tree is still the one the rewinding gate
// judged made no code progress; its rendered prompt must keep the
// from-scratch reference beside the FAIL verdict instead of claiming there
// is a previous attempt's work to continue.
//
// Criterion 6 (the unchanged-tree gate re-run): once the rewound build step
// has resubmitted the identical task-repo tree — after a crashed attempt's
// retry or, since D3, without any crash — the next gate is not
// dispatched at all: the workflow parks blocked with a human-visible reason
// (no change, the rewinding verdict, the build summary, the crashed attempt
// when there was one), and no rewind slot is charged. The end-to-end
// fake-harness twin is test/w67_unchanged_gate_tree_acceptance_test.go.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// stubRepoHash swaps the task-repo hash seam for one test.
func stubRepoHash(t *testing.T, fn func(string) (string, error)) {
	t.Helper()
	old := reposTreeHashFn
	t.Cleanup(func() { reposTreeHashFn = old })
	reposTreeHashFn = fn
}

// TestSpawnStepRewindNoProgressKeepsColdStart: the fast path's own
// preconditions still hold (new conversation, death-triggered retry,
// readable reference, matching capsule hash), but the rewound round never
// left the gate's task-repo tree. The prompt must carry the verdict and
// today's from-scratch reference, never "continue the previous attempt".
func TestSpawnStepRewindNoProgressKeepsColdStart(t *testing.T) {
	ad, sp, _, prev := fastPathEnv(t)
	old := treeHashFn
	t.Cleanup(func() { treeHashFn = old })
	treeHashFn = func(string) (string, error) { return "capsule-h", nil }
	stubRepoHash(t, func(string) (string, error) { return "repos-h", nil })

	if _, err := ad.SpawnStep(context.Background(), StepSpawn{
		StepID: "verification", SpawnN: 4, Fresh: true,
		RecordedTreeHash: "capsule-h", RetryTriggered: true,
		RewindInput:    &capsule.RewindInput{ToStep: "fix", Reason: "FAIL: tests red"},
		RewindRepoHash: "repos-h",
	}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p, _ := sp.last()
	assertColdStartReference(t, p.InitialPrompt, prev)
	if !strings.Contains(p.InitialPrompt, "FAIL: tests red") {
		t.Fatalf("the no-progress retry lost the rewind verdict:\n%s", p.InitialPrompt)
	}
	if strings.Contains(p.InitialPrompt, "continue the previous attempt") {
		t.Fatalf("the no-progress rewound retry still claims there is work to continue:\n%s", p.InitialPrompt)
	}
}

// TestSpawnStepRewindWithProgressKeepsFastPath: when the rewound round DID
// move the task-repo tree (the retry's current repos hash differs from the
// gate's judged one), the fast path is untouched.
func TestSpawnStepRewindWithProgressKeepsFastPath(t *testing.T) {
	ad, sp, _, prev := fastPathEnv(t)
	old := treeHashFn
	t.Cleanup(func() { treeHashFn = old })
	treeHashFn = func(string) (string, error) { return "capsule-h", nil }
	stubRepoHash(t, func(string) (string, error) { return "repos-progress", nil })

	if _, err := ad.SpawnStep(context.Background(), StepSpawn{
		StepID: "verification", SpawnN: 4, Fresh: true,
		RecordedTreeHash: "capsule-h", RetryTriggered: true,
		RewindInput:    &capsule.RewindInput{ToStep: "fix", Reason: "FAIL: tests red"},
		RewindRepoHash: "repos-h",
	}); err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	p, _ := sp.last()
	assertContinueInPlace(t, p.InitialPrompt, prev)
}

// TestRewindRetryCarriesGateJudgedRepoHash drives the real dispatch
// preparation through the review-rewind/model-error lane: the retry's
// StepSpawn must carry the tree the review gate judged, the fact criterion
// 5's suppression reads.
func TestRewindRetryCarriesGateJudgedRepoHash(t *testing.T) {
	oldT, oldR := treeHashFn, reposTreeHashFn
	t.Cleanup(func() { treeHashFn, reposTreeHashFn = oldT, oldR })
	treeHashFn = func(string) (string, error) { return "capsule-h", nil }
	reposTreeHashFn = func(string) (string, error) { return "repos-h", nil }

	env := infraRetryEnv(t)
	env.sp.script = []fakeEntry{
		done(`{}`), // code
		gateFAIL(), // review — rewinds code
		exitErr(1, "502 Bad Gateway: upstream error", true), // the rewound code attempt crashes
		done(`{}`),                 // the retry
		done(`{"verdict":"PASS"}`), // review
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	retry := env.sp.spawnAt(3)
	if retry.RewindInput == nil {
		t.Fatalf("setup: the retry carries no rewind verdict: %+v", retry)
	}
	if retry.RewindRepoHash != "repos-h" {
		t.Fatalf("the retry carries judged repo hash %q, want %q (the rewinding gate's last-judged tree)", retry.RewindRepoHash, "repos-h")
	}
}

// TestUnchangedGateTreeBlocksInsteadOfRerunning: the full driver lane.
// verification judges the tree and passes, review FAILs on the same tree,
// the rewound code attempt crashes model-class, its retry completes on the
// identical task-repo tree — and the driver must NOT dispatch verification
// again. It parks blocked with a reason naming the crashed code attempt, and
// the original review FAIL remains the only rewind slot.
func TestUnchangedGateTreeBlocksInsteadOfRerunning(t *testing.T) {
	oldT, oldR := treeHashFn, reposTreeHashFn
	t.Cleanup(func() { treeHashFn, reposTreeHashFn = oldT, oldR })
	treeHashFn = func(string) (string, error) { return "capsule-h", nil }
	reposTreeHashFn = func(string) (string, error) { return "repos-h", nil }

	env := newSupEnv(t, func(c *Config) {
		c.Sleep = func(context.Context, time.Duration) {}
	}, spec("code"), gateSpec("verification", "code"), gateSpec("review", "code"))
	env.sp.script = []fakeEntry{
		done(`{}`),                 // code
		done(`{"verdict":"PASS"}`), // verification judges "repos-h"
		gateFAIL(),                 // review FAIL on "repos-h", rewinds code (slot 1)
		exitErr(1, "502 Bad Gateway: upstream error", true), // rewound code attempt crashes
		done(`{}`), // retry resubmits the identical tree
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	spawns := env.sp.dispatched()
	if len(spawns) != 5 {
		t.Fatalf("dispatched %v, want exactly 5 — the gate must not re-run on the identical tree", spawns)
	}
	verifications := 0
	for _, got := range spawns {
		if strings.HasPrefix(got, "verification@") {
			verifications++
		}
	}
	if verifications != 1 {
		t.Fatalf("verification was dispatched %d times, want once (no re-run on the unchanged tree): %v", verifications, spawns)
	}
	x := env.d.Snapshot()
	if x.WorkflowStatus != StatusBlocked {
		t.Fatalf("workflow status %q, want blocked (the human-visible result)", x.WorkflowStatus)
	}
	for _, want := range []string{gateUnchangedBlockPrefix, "verification", "code", "exit_model_class"} {
		if !strings.Contains(x.BlockedReason, want) {
			t.Errorf("blocked reason misses %q: %s", want, x.BlockedReason)
		}
	}
	if n := x.RewindStats.Count; n != 1 {
		t.Fatalf("rewind_stats.count = %d, want 1 (the unchanged-tree guard charges no extra slot)", n)
	}
}

// TestUnchangedGateTreeUncrashedResubmissionBlocks is D3: the
// rewound build step does NOT crash — it receives the review FAIL, changes
// nothing and completes. Re-judging the identical tree can only repeat the
// verdict or pass by chance, so the gate tail is not started: the workflow
// parks blocked, no rewind slot is charged, and the reason says the build
// round produced no change, names the rewinding gate and its verdict round,
// and quotes the build step's own summary so the human sees why.
func TestUnchangedGateTreeUncrashedResubmissionBlocks(t *testing.T) {
	oldT, oldR := treeHashFn, reposTreeHashFn
	t.Cleanup(func() { treeHashFn, reposTreeHashFn = oldT, oldR })
	treeHashFn = func(string) (string, error) { return "capsule-h", nil }
	reposTreeHashFn = func(string) (string, error) { return "repos-h", nil }

	long := strings.Repeat("padding the excerpt bound. ", 40)
	env := newSupEnv(t, func(c *Config) {
		c.Sleep = func(context.Context, time.Duration) {}
	}, spec("code"), gateSpec("verification", "code"), gateSpec("review", "code"))
	env.sp.script = []fakeEntry{
		done(`{}`),                 // code
		done(`{"verdict":"PASS"}`), // verification judges "repos-h"
		gateFAIL(),                 // review FAIL on "repos-h", rewinds code (slot 1)
		done(`{"summary":"The review finding is a false positive:\nthe guard already exists, so nothing was changed. ` + long + `"}`), // no crash, no change
		// the gate tail, scripted so a re-run is a recorded dispatch (the
		// pre-fix driver re-judged the identical tree and completed)
		done(`{"verdict":"PASS"}`),
		done(`{"verdict":"PASS"}`),
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	spawns := env.sp.dispatched()
	if len(spawns) != 4 {
		t.Fatalf("dispatched %v, want exactly 4 — no gate may re-run on the identical tree", spawns)
	}
	x := env.d.Snapshot()
	if x.WorkflowStatus != StatusBlocked {
		t.Fatalf("workflow status %q, want blocked (the human-visible result): %s", x.WorkflowStatus, x.BlockedReason)
	}
	for _, want := range []string{
		gateUnchangedBlockPrefix,
		"produced no change",
		`"verification"`, // the gate that is not re-run
		`"review"`,       // the rewinding gate
		"round 1",        // its verdict round
		`"code"`,         // the build step
		"The review finding is a false positive: the guard already exists, so nothing was changed.", // the summary, one line
	} {
		if !strings.Contains(x.BlockedReason, want) {
			t.Errorf("blocked reason misses %q: %s", want, x.BlockedReason)
		}
	}
	if strings.Contains(x.BlockedReason, "crashed with") {
		t.Errorf("no attempt crashed, yet the reason claims one: %s", x.BlockedReason)
	}
	if strings.Contains(x.BlockedReason, long) || len(x.BlockedReason) > 2000 {
		t.Errorf("the summary excerpt is not bounded (%d bytes): %s", len(x.BlockedReason), x.BlockedReason)
	}
	if n := x.RewindStats.Count; n != 1 {
		t.Fatalf("rewind_stats.count = %d, want 1 (the unchanged-tree guard charges no extra slot)", n)
	}
}

// TestUnchangedGateTreeOwnerResumeOverrides: the block is the owner's
// decision point, not a dead end. `pf resume` is the explicit override: it
// forgets every gate's last-judged tree, so the whole gate tail runs once on
// the same tree instead of blocking again at the next gate.
func TestUnchangedGateTreeOwnerResumeOverrides(t *testing.T) {
	oldT, oldR := treeHashFn, reposTreeHashFn
	t.Cleanup(func() { treeHashFn, reposTreeHashFn = oldT, oldR })
	treeHashFn = func(string) (string, error) { return "capsule-h", nil }
	reposTreeHashFn = func(string) (string, error) { return "repos-h", nil }

	env := newSupEnv(t, func(c *Config) {
		c.Sleep = func(context.Context, time.Duration) {}
	}, spec("code"), gateSpec("verification", "code"), gateSpec("review", "code"))
	env.sp.script = []fakeEntry{
		done(`{}`),
		done(`{"verdict":"PASS"}`),
		gateFAIL(),
		exitErr(1, "502 Bad Gateway: upstream error", true),
		done(`{}`),
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if x := env.d.Snapshot(); x.WorkflowStatus != StatusBlocked {
		t.Fatalf("setup: workflow %q, want blocked before the override", x.WorkflowStatus)
	}
	if err := env.d.ResumeCurrent(); err != nil {
		t.Fatalf("owner resume: %v", err)
	}
	env.sp.script = []fakeEntry{
		done(`{"verdict":"PASS"}`), // verification, now explicitly allowed
		done(`{"verdict":"PASS"}`), // review
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("resumed Run: %v", err)
	}
	x := env.d.Snapshot()
	if x.WorkflowStatus != StatusCompleted {
		t.Fatalf("workflow status %q after the owner's override, want completed (%s)", x.WorkflowStatus, x.BlockedReason)
	}
	if n := x.RewindStats.Count; n != 1 {
		t.Fatalf("rewind_stats.count = %d, want 1 (the override charges no slot)", n)
	}
}

// TestUnchangedGateTreeNonBuildArtifactTargetNotBlocked is the B1 shape:
// a gate rewinds a step whose product is an ARTIFACT (feature-full's
// grill-review -> spec, bug-fix's root-cause -> repro). The target crashes
// once, its retry revises only the artifact (the task-repo tree stays the
// gate's judged tree), and the gate must be re-dispatched — the guard
// compares task-repo trees, so only a rewind of the lane's build step is
// its business.
func TestUnchangedGateTreeNonBuildArtifactTargetNotBlocked(t *testing.T) {
	oldT, oldR := treeHashFn, reposTreeHashFn
	t.Cleanup(func() { treeHashFn, reposTreeHashFn = oldT, oldR })
	treeHashFn = func(string) (string, error) { return "capsule-h", nil }
	reposTreeHashFn = func(string) (string, error) { return "repos-h", nil }

	env := newSupEnv(t, func(c *Config) {
		c.Sleep = func(context.Context, time.Duration) {}
	}, spec("spec"), spec("code"), gateSpec("grill-review", "spec"), gateSpec("verification", "code"))
	// The spec retry revises only its artifact, exactly like the real lane.
	capsuleRoot := filepath.Dir(filepath.Dir(env.execPath))
	reviseSpec := func() {
		dir := filepath.Join(capsuleRoot, "artifacts")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Errorf("mkdir artifacts: %v", err)
			return
		}
		if err := os.WriteFile(filepath.Join(dir, "spec.md"), []byte("revised spec\n"), 0o644); err != nil {
			t.Errorf("write spec.md: %v", err)
		}
	}
	env.sp.script = []fakeEntry{
		done(`{}`), // spec
		done(`{}`), // code
		gateFAIL(), // grill-review FAIL rewinds spec
		exitErr(1, "502 Bad Gateway: upstream error", true),                                           // the rewound spec attempt crashes
		{res: &StepResult{Outcome: OutcomeCompleted, Output: json.RawMessage(`{}`)}, act: reviseSpec}, // spec retry: artifact only
		done(`{}`),                 // code re-runs (superseded by the rewind)
		done(`{"verdict":"PASS"}`), // grill-review re-entry: MUST be dispatched
		done(`{"verdict":"PASS"}`), // verification
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := env.d.Snapshot()
	if x.WorkflowStatus != StatusCompleted {
		t.Fatalf("workflow status %q, want completed (the artifact-only rewind must not block the gate): %s", x.WorkflowStatus, x.BlockedReason)
	}
	grill := 0
	for _, got := range env.sp.dispatched() {
		if strings.HasPrefix(got, "grill-review@") {
			grill++
		}
	}
	if grill != 2 {
		t.Fatalf("grill-review was dispatched %d times, want 2 (the rewound gate must run again): %v", grill, env.sp.dispatched())
	}
	if n := x.RewindStats.Count; n != 1 {
		t.Fatalf("rewind_stats.count = %d, want 1", n)
	}
}

// TestUnchangedGateTreeSelfRewindBuildStepBlocksUnderFixedClock is B2's
// missing shape: the rewinding gate IS the guarded gate (verification FAIL
// -> fix crash -> unchanged retry -> verification re-entry). The verdict's
// time is derived from the same-write rewind archive, so the comparison is
// inclusive and a fixed clock — the worst case the old strict
// judged_at comparison missed — still blocks the re-entry.
func TestUnchangedGateTreeSelfRewindBuildStepBlocksUnderFixedClock(t *testing.T) {
	oldT, oldR := treeHashFn, reposTreeHashFn
	t.Cleanup(func() { treeHashFn, reposTreeHashFn = oldT, oldR })
	treeHashFn = func(string) (string, error) { return "capsule-h", nil }
	reposTreeHashFn = func(string) (string, error) { return "repos-h", nil }
	fixed := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

	env := newSupEnv(t, func(c *Config) {
		c.Sleep = func(context.Context, time.Duration) {}
		c.Now = func() time.Time { return fixed }
	}, spec("fix"), gateSpec("verification", "fix"))
	env.sp.script = []fakeEntry{
		done(`{}`), // fix
		gateFAIL(), // verification FAIL rewinds fix (same write as its verdict)
		exitErr(1, "502 Bad Gateway: upstream error", true), // the rewound fix attempt crashes
		done(`{}`), // fix retry resubmits the identical tree
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := env.d.Snapshot()
	if x.WorkflowStatus != StatusBlocked {
		t.Fatalf("workflow status %q, want blocked (verification must not re-run on the identical tree under a fixed clock)", x.WorkflowStatus)
	}
	if !strings.Contains(x.BlockedReason, gateUnchangedBlockPrefix) || !strings.Contains(x.BlockedReason, "fix") {
		t.Fatalf("blocked reason does not name the crashed fix attempt: %s", x.BlockedReason)
	}
	verifications := 0
	for _, got := range env.sp.dispatched() {
		if strings.HasPrefix(got, "verification@") {
			verifications++
		}
	}
	if verifications != 1 {
		t.Fatalf("verification was dispatched %d times, want 1 (only the FAIL that rewound; no re-run on the identical tree): %v", verifications, env.sp.dispatched())
	}
	if n := x.RewindStats.Count; n != 1 {
		t.Fatalf("rewind_stats.count = %d, want 1", n)
	}
}

// TestUnchangedGateTreeDeliverOverrideRerunsVerification covers the deliver
// 「全量重验」 override: the deliver gate rewinds to verification, an
// artifact-producing target that is NOT the build step. One crashed
// verification attempt in that re-run must not block the re-run — the
// human explicitly asked for it.
func TestUnchangedGateTreeDeliverOverrideRerunsVerification(t *testing.T) {
	oldT, oldR := treeHashFn, reposTreeHashFn
	t.Cleanup(func() { treeHashFn, reposTreeHashFn = oldT, oldR })
	treeHashFn = func(string) (string, error) { return "capsule-h", nil }
	reposTreeHashFn = func(string) (string, error) { return "repos-h", nil }

	env := newSupEnv(t, func(c *Config) {
		c.Sleep = func(context.Context, time.Duration) {}
	}, spec("code"), gateSpec("verification", "code"), gateSpec("review", "code"), gateSpec("deliver", "code"))
	env.sp.script = []fakeEntry{
		done(`{}`),                 // code
		done(`{"verdict":"PASS"}`), // verification
		done(`{"verdict":"PASS"}`), // review
		{ // deliver's human 「全量重验」 answer: rewind to verification at full
			res: &StepResult{Outcome: OutcomeGateFailed, Output: json.RawMessage(`{"verdict":"fail","fail_reason":"human: 全量重验","rewind_to":"verification"}`)},
			act: func() {
				// The override is honored only while the recorded tier is
				// scoped; set that fact the deliver verdict reads.
				env.d.mu.Lock()
				env.d.exec.ScopeTier = tierScoped
				env.d.mu.Unlock()
			},
		},
		exitErr(1, "502 Bad Gateway: upstream error", true), // the overridden verification re-run crashes
		done(`{"verdict":"PASS"}`),                          // verification retry: MUST be dispatched
		done(`{"verdict":"PASS"}`),                          // review re-runs
		done(`{"verdict":"pass","repos":[]}`),               // deliver completes
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := env.d.Snapshot()
	if x.WorkflowStatus != StatusCompleted {
		t.Fatalf("workflow status %q, want completed (the deliver override must re-run verification): %s", x.WorkflowStatus, x.BlockedReason)
	}
	verifications := 0
	for _, got := range env.sp.dispatched() {
		if strings.HasPrefix(got, "verification@") {
			verifications++
		}
	}
	if verifications != 3 {
		t.Fatalf("verification was dispatched %d times, want 3 (first pass, the override re-run and its crash retry): %v", verifications, env.sp.dispatched())
	}
}

// TestUnchangedGateTreeGuardsVerificationAfterAbandonedLightRecheck
// ( S2): the unchanged-tree guard exempts a light recheck and
// its IN-PLACE escalation — the designed same-tree fast lanes. The
// abandonment of a pending light recheck by a fresh re-derivation archives
// the same audit-only light_recheck_escalated attempt, but the full
// verification after it follows a fresh build round, not a same-tree
// re-run, so the guard must still judge it. Chain: verification FAILs
// mechanically on tree h1 → code's light re-entry moves to h2 → review
// FAILs structurally (the recheck abandoned) → code's fresh round goes
// back to h1 → review PASSes. Verification would re-judge the identical h1
// it already failed: the workflow parks instead of dispatching it.
func TestUnchangedGateTreeGuardsVerificationAfterAbandonedLightRecheck(t *testing.T) {
	oldT, oldR := treeHashFn, reposTreeHashFn
	t.Cleanup(func() { treeHashFn, reposTreeHashFn = oldT, oldR })
	var mu sync.Mutex
	hash := "h1"
	setHash := func(h string) func() {
		return func() {
			mu.Lock()
			hash = h
			mu.Unlock()
		}
	}
	treeHashFn = func(string) (string, error) { return "capsule-h", nil }
	reposTreeHashFn = func(string) (string, error) {
		mu.Lock()
		defer mu.Unlock()
		return hash, nil
	}

	env := newSupEnv(t, func(c *Config) {
		c.Sleep = func(context.Context, time.Duration) {}
		c.Workflow.MaxTotal = 6
	}, spec("code"), gateSpec("review", "code"), mechGateSpec("code"))
	env.sp.script = []fakeEntry{
		done(`{}`),                 // code: h1
		done(`{"verdict":"PASS"}`), // review judges h1
		mechanicalFAIL(),           // verification FAILs mechanically on h1: the marker, a light rewind of code
		{res: done(`{}`).res, act: setHash("h2")}, // the mechanical light re-entry changes the tree
		gateFAIL(), // review FAILs structurally on h2: the light recheck is abandoned, code rewound fresh
		{res: done(`{}`).res, act: setHash("h1")}, // the fresh round lands back on h1
		done(`{"verdict":"PASS"}`),                // review judges h1
		// scripted so a verification re-run is a recorded dispatch (the
		// exempt guard let the abandoned recheck's full run re-judge h1)
		done(`{"verdict":"pass"}`),
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := env.d.Snapshot()
	var abandoned bool
	if ver := x.Steps[verificationStepID]; ver != nil {
		for _, a := range ver.Attempts {
			abandoned = abandoned || a.Cause == CauseLightRecheckEscalated
		}
	}
	if !abandoned {
		t.Fatalf("setup: the structural review FAIL must have abandoned the light recheck: %+v", x.Steps[verificationStepID])
	}
	var verifications int
	for _, got := range env.sp.dispatched() {
		if strings.HasPrefix(got, verificationStepID+"@") {
			verifications++
		}
	}
	if verifications != 1 {
		t.Fatalf("verification dispatched %d times, want once — it must not re-judge the identical tree after an abandoned recheck: %v",
			verifications, env.sp.dispatched())
	}
	if x.WorkflowStatus != StatusBlocked || !strings.Contains(x.BlockedReason, gateUnchangedBlockPrefix) {
		t.Fatalf("the workflow must park on the unchanged tree: %q %s", x.WorkflowStatus, x.BlockedReason)
	}
}

// TestInPlaceEscalationContextKeysOnTheAbandonedFlag ( S2):
// the two light_recheck_escalated shapes split on the audit record's
// abandoned flag. Both order the escalation note (escalatedRecheckContext);
// only the in-place one — and a record written before the flag — is the
// unchanged-tree guard's same-tree exemption.
func TestInPlaceEscalationContextKeysOnTheAbandonedFlag(t *testing.T) {
	esc := func(result string) []capsule.Attempt {
		return []capsule.Attempt{
			{Cause: CauseGateFailRewindMechanical},
			{Cause: CauseLightRecheckEscalated, Result: json.RawMessage(result)},
			{Cause: CauseInfraFailure}, // audit-only interruptions are skipped
		}
	}
	for _, tc := range []struct {
		name            string
		attempts        []capsule.Attempt
		escalated, inPl bool
	}{
		{"in place", esc(`{"escalated":true,"abandoned":false}`), true, true},
		{"in place, written before the flag", esc(`{"escalated":true}`), true, true},
		{"abandoned by a fresh re-derivation", esc(`{"escalated":true,"abandoned":true}`), true, false},
		{"no escalation", []capsule.Attempt{{Cause: "accepted"}}, false, false},
	} {
		if got := escalatedRecheckContext(tc.attempts); got != tc.escalated {
			t.Errorf("%s: escalatedRecheckContext = %v, want %v", tc.name, got, tc.escalated)
		}
		if got := inPlaceEscalationContext(tc.attempts); got != tc.inPl {
			t.Errorf("%s: inPlaceEscalationContext = %v, want %v", tc.name, got, tc.inPl)
		}
	}
}
