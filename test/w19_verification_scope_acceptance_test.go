package test

// w19_verification_scope_acceptance_test.go: end-to-end acceptance for
// (verification tiering P0 — lane text + envelope schema +
// merge/review/rewind rules). Authored against artifacts/spec.md BEFORE
// the implementation: this suite is the red bar `code` must turn green.
//
// Every scenario drives the SHIPPED bundles (feature-full / bug-fix /
// chore / autopilot) through the real daemon + driver and the fake
// harness (test/fakepi), with scripted result envelopes; it judges only
// the records the spec names — the recorded execution tier (`scope_tier`),
// the driver-written deliver `scope_chain`, the envelope-rejection event
// and the deliver human question — never an implementation-internal
// symbol. The opt-in marker is written into the work item's `content`
// (the spec's A1), the scope object carries the plan's field names
// (mode/declared_paths/rationale/fast_gates_ref) and the incremental
// carrier is `scope.prior` (plan A3). The deliver result's fourth-answer
// rewind rides `output.rewind_to` (the fake harness can only pass
// arbitrary fields through `output`).
//
// The lane-text / docs scenarios are content-contract checks over the
// shipped tree (the same acceptance level the repo uses for its workflow
// text); they name the exact phrases the handoff pins for `code`.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

// w19Marker is the exact opt-in line the spec pins (A1): a line in the
// work item content whose trimmed form is exactly this.
const w19Marker = "verification: scoped"

// w19ScenarioTitle is the fake authored acceptance title the feature-full
// verification envelope echoes (its output schema carries authored_titles).
const w19ScenarioTitle = "the scoped tier is recorded and carried to delivery"

// w19Bool is a pointer helper for the review scope_adequate boolean.
func w19Bool(b bool) *bool { return &b }

// w19Scope builds a complete scope object (design §2 / plan A2).
func w19Scope(paths ...string) map[string]any {
	if len(paths) == 0 {
		paths = []string{"internal/polyforge/driver/scope.go"}
	}
	return map[string]any{
		"mode":           "scoped",
		"declared_paths": paths,
		"rationale":      "only the scoped tier helper and its callers changed",
		"fast_gates_ref": "docs/contributing.md#scoped-verification",
	}
}

// w19VerPass is a verification pass output carrying the optional scope.
func w19VerPass(scope map[string]any) map[string]any {
	o := map[string]any{
		"verdict": "pass",
		"checks": []map[string]any{
			{"name": "build", "status": "pass"},
			{"name": "lint", "status": "pass"},
			{"name": "authored-scenarios", "status": "pass"},
		},
		"authored_titles": []string{w19ScenarioTitle},
		"scenarios":       []map[string]any{{"title": w19ScenarioTitle, "status": "pass"}},
	}
	if scope != nil {
		o["scope"] = scope
	}
	return o
}

// w19Ver is the verification envelope action.
func w19Ver(scope map[string]any) map[string]any {
	return map[string]any{"do": "envelope", "id": "verification", "outcome": "completed", "output": w19VerPass(scope)}
}

// w19Review is the review envelope action; a nil scopeAdequate omits the
// field (the "scoped review must answer" probe). on a
// scoped work item review also records its own `scope` — the scope the
// following verification must use for the two to form one merging
// credential (a nil scope records none).
func w19Review(scopeAdequate *bool, scope map[string]any) map[string]any {
	o := map[string]any{"verdict": "pass"}
	if scopeAdequate != nil {
		o["scope_adequate"] = *scopeAdequate
	}
	if scope != nil {
		o["scope"] = scope
	}
	return map[string]any{"do": "envelope", "id": "review", "outcome": "completed", "output": o}
}

// w19FeatureHead is the feature-full lane up to (excluding) verification.
func w19FeatureHead(origin string) []map[string]any {
	return []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "spec", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "grill-review", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "plan", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "e2e-author", "outcome": "completed", "handoff": true,
			"output": map[string]any{"scenario_titles": []string{w19ScenarioTitle}, "scenarios_path": "artifacts/e2e-scenarios.md"}},
		{"do": "envelope", "id": "code", "outcome": "completed", "handoff": true,
			"name": "demo", "origin": origin},
	}
}

// w19CreateWI files one work item whose content carries (or omits) the
// opt-in marker, sets an empty plan, and tracks it for the orphan sweep.
func w19CreateWI(t *testing.T, inst *v1Install, goal, bundle string, marked bool) string {
	t.Helper()
	var content *string
	if marked {
		c := w19Marker
		content = &c
	}
	raw, err := inst.client.CreateWorkItem(aihubclient.CreateWorkItemRequest{
		Project:    v1Project,
		Goal:       goal,
		Content:    content,
		BundleName: strPtr(bundle),
	})
	if err != nil {
		t.Fatalf("create work item: %v", err)
	}
	var wi struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(raw, &wi); err != nil {
		t.Fatalf("create work item answer: %v (%s)", err, raw)
	}
	inst.setPlanFull(t, wi.ID, []map[string]any{}, nil)
	inst.trackWI(wi.ID)
	return wi.ID
}

// w19Start starts one daemon, submits wi, and registers the orphan sweep.
func w19Start(t *testing.T, inst *v1Install, wi string) *v1Daemon {
	t.Helper()
	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	if out := d.pf(t, "submit", wi); !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}
	return d
}

// w19ScopeTier reads the top-level recorded tier from execution.json.
func w19ScopeTier(t *testing.T, capDir string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, "runtime-state", "execution.json"))
	if err != nil {
		t.Fatalf("execution.json: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("execution.json undecodable: %v", err)
	}
	s, _ := m["scope_tier"].(string)
	return s
}

// w19JSON marshals v for a diagnostic / byte-containment check.
func w19JSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// w19Chain answers the driver-written deliver scope_chain (nil when
// absent — the baseline red).
func w19Chain(t *testing.T, x *executionView) map[string]any {
	t.Helper()
	st := x.Steps["deliver"]
	if st == nil {
		t.Fatalf("deliver step missing from execution.json: %s", x.WorkflowStatus)
	}
	ch, _ := st.Output["scope_chain"].(map[string]any)
	if ch == nil {
		t.Fatalf("SYMPTOM: deliver output carries no scope_chain — the three-ring chain was never written: %s", w19JSON(st.Output))
	}
	return ch
}

// w19ChainHas asserts the chain carries a ring marker (its exact nested
// key shape is a `plan`-level choice; the ring facts are the contract).
func w19ChainHas(t *testing.T, chain map[string]any, needle string) {
	t.Helper()
	if !strings.Contains(w19JSON(chain), needle) {
		t.Errorf("SYMPTOM: scope_chain %s does not carry %q", w19JSON(chain), needle)
	}
}

// w19Read reads one capsule-relative shipped file.
func w19Read(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(v1Root(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// ---------------------------------------------------------------------------
// Scenario 1: the marker opts a scoped-capable lane in; a complete scope is
// accepted, the tier is recorded scoped, and the three-ring chain rides the
// delivery. The declared path is deliberately non-existent/opaque (the
// driver classifies no paths).

func TestW19ScopedFullChainRecordsTierAndChain(t *testing.T) {
	inst := newV1Install(t)
	inst.keepDeliver = true
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi := w19CreateWI(t, inst, "w19: scoped feature-full chain", "feature-full", true)
	branch := "polyforge/" + wiPathName(t, inst, wi)
	scope := w19Scope("zzz/this/path/does/not/exist.go")
	plan := append(w19FeatureHead(origin),
		// review runs first and declares the scope; the
		// verification behind it uses the same one
		w19Review(w19Bool(true), scope),
		w19Ver(scope),
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "completed",
			"run":         literalBranch(branch, pushAndPR("demo"), mergeRecord("demo", ""), assemble),
			"output_file": deliverOutput},
	)
	inst.setPlanFull(t, wi, plan, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitWorkflowStatus(t, d, wi, "completed", 240*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	w.mergedInto(origin, branch)
	if got := w19ScopeTier(t, d.capsuleDir(wi)); got != "scoped" {
		t.Errorf("SYMPTOM: execution scope_tier = %q, want %q (the opt-in was never recorded)", got, "scoped")
	}
	chain := w19Chain(t, readExecution(t, d.capsuleDir(wi)))
	w19ChainHas(t, chain, "scoped")
	w19ChainHas(t, chain, "zzz/this/path/does/not/exist.go")
	w19ChainHas(t, chain, "scope_adequate")
}

// ---------------------------------------------------------------------------
// Scenario 2: a scoped claim on an unmarked work item is rejected (the
// marker is the only way to lower the tier).

func TestW19ScopeClaimWithoutMarkerRejected(t *testing.T) {
	inst := newV1Install(t)
	remote := newTaskRemote(t)
	wi := w19CreateWI(t, inst, "w19: scope claim without the marker", "feature-full", false)
	// review runs before verification; the unmarked work
	// item owes it nothing, so a plain pass precedes the scoped claim.
	inst.setPlanFull(t, wi, append(w19FeatureHead(remote.url), w19Review(nil, nil), w19Ver(w19Scope())), nil)
	d := w19Start(t, inst, wi)

	expectRejection(t, d, wi, "scope")
}

// ---------------------------------------------------------------------------
// Scenario 3: an incomplete scope is rejected (a required field missing).

func TestW19IncompleteScopeRejected(t *testing.T) {
	inst := newV1Install(t)
	remote := newTaskRemote(t)
	wi := w19CreateWI(t, inst, "w19: incomplete scope", "feature-full", true)
	incomplete := map[string]any{
		"mode":           "scoped",
		"declared_paths": []string{"internal/polyforge/driver/scope.go"},
		"fast_gates_ref": "docs/contributing.md#scoped-verification",
		// rationale deliberately absent
	}
	// the marked work item owes review a scope_adequate answer
	// before verification's incomplete declaration is judged.
	inst.setPlanFull(t, wi, append(w19FeatureHead(remote.url), w19Review(w19Bool(true), nil), w19Ver(incomplete)), nil)
	d := w19Start(t, inst, wi)

	expectRejection(t, d, wi, "scope")
}

// ---------------------------------------------------------------------------
// Scenario 4: autopilot stays full — its marker cannot lower the tier, so a
// scope claim is rejected.

func TestW19AutopilotMarkerStillFullRejectsScope(t *testing.T) {
	inst := newV1Install(t)
	remote := newTaskRemote(t)
	wi := w19CreateWI(t, inst, "w19: autopilot stays full", "autopilot", true)
	plan := []map[string]any{
		{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
		{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "name": "demo", "origin": remote.url},
		w19Ver(w19Scope()),
	}
	inst.setPlanFull(t, wi, plan, nil)
	d := w19Start(t, inst, wi)

	expectRejection(t, d, wi, "scope")
}

// ---------------------------------------------------------------------------
// Scenario 5: review must answer scope_adequate whenever verification was
// scoped.

func TestW19ReviewMustAnswerScopeAdequate(t *testing.T) {
	inst := newV1Install(t)
	remote := newTaskRemote(t)
	wi := w19CreateWI(t, inst, "w19: scoped review must answer scope_adequate", "feature-full", true)
	inst.setPlanFull(t, wi, append(w19FeatureHead(remote.url), w19Review(nil, nil), w19Ver(w19Scope())), nil)
	d := w19Start(t, inst, wi)

	expectRejection(t, d, wi, "scope_adequate")
}

// ---------------------------------------------------------------------------
// Scenario 6: a false scope_adequate passes review but blocks the automatic
// merge; the driver refuses a completed delivery that merged anyway, and the
// deliver question offers the fourth answer 「全量重验」.

func TestW19FalseScopeAdequateBlocksAutoMergeAndAsks(t *testing.T) {
	inst := newV1Install(t)
	inst.keepDeliver = true
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi := w19CreateWI(t, inst, "w19: a false scope_adequate blocks the merge", "feature-full", true)
	branch := "polyforge/" + wiPathName(t, inst, wi)
	const qid = "deliver-merge"
	plan := append(w19FeatureHead(origin),
		w19Review(w19Bool(false), w19Scope()),
		w19Ver(w19Scope()),
		// a lying dispatch that claims the PR is merged anyway
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "completed",
			"run": literalBranch(branch, pushAndPR("demo"),
				claimRecord("demo", `"merged":true,"merge_commit":"pending","tests_passed":true`), assemble),
			"output_file": deliverOutput},
		// the driver refused it; the re-dispatch asks the human
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "needs_input", "question_id": qid,
			"question": "PR https://github.com/fake/demo/pull/1: scope_adequate was false, so the merge rule does not hold. merge / don't merge / back to code / 全量重验?"},
	)
	inst.setPlanFull(t, wi, plan, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	expectRejection(t, d, wi, "scope")
	waitQuestion(t, d, wi, qid, 120*time.Second)
	x := readExecution(t, d.capsuleDir(wi))
	q := x.Steps["deliver"].PendingQuestion
	if q == nil || !strings.Contains(w19JSON(*q), "全量重验") {
		t.Errorf("SYMPTOM: the deliver question does not offer the fourth answer 全量重验: %s", w19JSON(q))
	}
}

// ---------------------------------------------------------------------------
// Scenario 7: answering 「全量重验」 rewinds to verification at full and the
// next deliver is judged with the full credential (no chain).

func TestW19FullReverifyAnswerRewindsToFull(t *testing.T) {
	inst := newV1Install(t)
	inst.keepDeliver = true
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi := w19CreateWI(t, inst, "w19: 全量重验 rewinds to full verification", "feature-full", true)
	branch := "polyforge/" + wiPathName(t, inst, wi)
	const qid = "deliver-merge"
	plan := append(w19FeatureHead(origin),
		w19Review(w19Bool(false), w19Scope()),
		w19Ver(w19Scope()),
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "needs_input", "question_id": qid,
			"question": "PR https://github.com/fake/demo/pull/1: the scoped merge rule does not hold. merge / don't merge / back to code / 全量重验?"},
		// the human answered 全量重验: rewind to the verification step at full
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "gate_failed",
			"output": map[string]any{"verdict": "fail", "fail_reason": "human: 全量重验", "rewind_to": "verification"}},
		// full re-verification (no scope; review ran before
		// verification and stays done — its earlier adequate=false no longer
		// binds a full pass), then the full delivery
		w19Ver(nil),
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "completed",
			"run":         literalBranch(branch, pushAndPR("demo"), mergeRecord("demo", ""), assemble),
			"output_file": deliverOutput},
	)
	inst.setPlanFull(t, wi, plan, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitQuestion(t, d, wi, qid, 120*time.Second)
	answerQuestion(t, d, wi, qid, "全量重验")
	waitWorkflowStatus(t, d, wi, "completed", 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	w.mergedInto(origin, branch)
	if got := w19ScopeTier(t, d.capsuleDir(wi)); got != "full" {
		t.Errorf("SYMPTOM: after 全量重验 the recorded tier = %q, want %q", got, "full")
	}
	if st := readExecution(t, d.capsuleDir(wi)).Steps["deliver"]; st != nil {
		if _, has := st.Output["scope_chain"]; has {
			t.Errorf("a full delivery must carry no scoped chain: %s", w19JSON(st.Output))
		}
	}
}

// ---------------------------------------------------------------------------
// Scenario 8: after a review FAIL an opted-in ticket re-verifies scoped and
// the merge credential is the union of the original and the increment.

func TestW19IncrementalReverifyUnionsCredential(t *testing.T) {
	inst := newV1Install(t)
	inst.keepDeliver = true
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi := w19CreateWI(t, inst, "w19: incremental re-verification unions the credential", "feature-full", true)
	branch := "polyforge/" + wiPathName(t, inst, wi)
	prior := map[string]any{"tier": "scoped", "declared_paths": []string{"internal/polyforge/driver/scope.go"}}
	increment := w19Scope("internal/polyforge/driver/result.go")
	increment["prior"] = prior
	plan := append(w19FeatureHead(origin),
		// review runs first; its FAIL cites the scope gap and
		// rewinds to code
		map[string]any{"do": "envelope", "id": "review", "outcome": "completed",
			"output": map[string]any{"verdict": "fail", "fail_reason": "scope gap: the increment must be re-verified",
				"findings": []map[string]any{{"severity": "blocking", "detail": "re-verify the fix increment"}}}},
		// the code re-entry (a rewind from review) commits the increment
		w67ChangedRound("code", "demo", 1),
		// the merging credential is the union of the original
		// and the increment, so the re-run review declares exactly that set
		w19Review(w19Bool(true), map[string]any{"mode": "scoped",
			"declared_paths": []string{"internal/polyforge/driver/scope.go", "internal/polyforge/driver/result.go"}}),
		// then the scoped increment verification (prior = the original scope)
		w19Ver(increment),
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "completed",
			"run":         literalBranch(branch, pushAndPR("demo"), mergeRecord("demo", ""), assemble),
			"output_file": deliverOutput},
	)
	inst.setPlanFull(t, wi, plan, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitWorkflowStatus(t, d, wi, "completed", 240*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	w.mergedInto(origin, branch)
	chain := w19Chain(t, readExecution(t, d.capsuleDir(wi)))
	w19ChainHas(t, chain, "internal/polyforge/driver/scope.go")
	w19ChainHas(t, chain, "internal/polyforge/driver/result.go")
}

// ---------------------------------------------------------------------------
// Scenario 9: a moved default branch at deliver re-runs the carried scoped
// test bar verbatim, and the scoped chain still rides the delivery.

func TestW19MovedBranchRerunsCarriedScopedBar(t *testing.T) {
	inst := newV1Install(t)
	inst.keepDeliver = true
	w := newDeliverWorld(t)
	origin := w.origin("demo")
	wi := w19CreateWI(t, inst, "w19: moved branch re-runs the carried scoped bar", "feature-full", true)
	branch := "polyforge/" + wiPathName(t, inst, wi)
	ver := w19VerPass(w19Scope())
	ver["test_bar"] = []map[string]any{{"repo": "demo", "command": deliverTestBar}}
	plan := append(w19FeatureHead(origin),
		map[string]any{"do": "envelope", "id": "review", "outcome": "completed",
			"output": map[string]any{"verdict": "pass", "scope_adequate": true,
				"scope": w19Scope()}},
		// verification is the last gate before deliver, so
		// main's move rides its run (the local HEAD review covered stays)
		map[string]any{"do": "envelope", "id": "verification", "outcome": "completed", "output": ver,
			"run": []string{moveMain(origin, "other.txt", "another PR")}},
		map[string]any{"do": "envelope", "id": "deliver", "outcome": "completed",
			"run":         literalBranch(branch, pushAndPR("demo"), deliverTestBarCmd, mergeRecord("demo", ""), assemble),
			"output_file": deliverOutput},
	)
	inst.setPlanFull(t, wi, plan, nil)
	d := startDeliverDaemon(t, inst, w, wi)

	waitWorkflowStatus(t, d, wi, "completed", 240*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	w.mergedInto(origin, branch)
	chain := w19Chain(t, readExecution(t, d.capsuleDir(wi)))
	w19ChainHas(t, chain, "scoped")
}

// ---------------------------------------------------------------------------
// Scenario 10: a fresh re-dispatch restores the recorded scoped tier (the
// first verification dispatch dies before its envelope; the re-dispatch must
// read the recorded tier, never recompute or ask).

func TestW19FreshRedispatchRestoresScopedTier(t *testing.T) {
	inst := newV1Install(t)
	remote := newTaskRemote(t)
	wi := w19CreateWI(t, inst, "w19: fresh re-dispatch restores the tier", "feature-full", true)
	plan := append(w19FeatureHead(remote.url),
		w19Review(w19Bool(true), w19Scope()),
		map[string]any{"do": "envelope", "id": "verification", "outcome": "completed", "crash": "before",
			"output": w19VerPass(w19Scope())},
		w19Ver(w19Scope()),
	)
	inst.setPlanFull(t, wi, plan, nil)
	d := w19Start(t, inst, wi)

	waitStepStatus(t, d, wi, "verification", "done", 180*time.Second)
	if got := w19ScopeTier(t, d.capsuleDir(wi)); got != "scoped" {
		t.Errorf("SYMPTOM: the re-dispatched scoped tier = %q, want %q (recomputed or lost)", got, "scoped")
	}
}

// ---------------------------------------------------------------------------
// Scenarios 11 + 12: the shipped lane text, docs and step definitions
// describe the tier, release stays untouched, and no gate list is hardcoded.

func TestW19LaneTextDocsAndReleaseDescribeTier(t *testing.T) {
	ver := w19Read(t, "workflows/_shared/steps/verification.md")
	for _, want := range []string{"verification: scoped", "scope", "declar"} {
		if !strings.Contains(ver, want) {
			t.Errorf("workflows/_shared/steps/verification.md must describe the tier and the repo's declaration (%q missing)", want)
		}
	}
	for _, lane := range []string{"feature-full", "bug-fix", "chore"} {
		if txt := w19Read(t, "workflows/"+lane+"/workflow.md"); !strings.Contains(txt, "scoped") {
			t.Errorf("workflows/%s/workflow.md must state the scoped tier and its declaration format", lane)
		}
	}
	autopilot := w19Read(t, "workflows/autopilot/workflow.md")
	if strings.Contains(autopilot, "verification: scoped") {
		t.Errorf("autopilot must not opt into the scoped tier")
	}
	if !strings.Contains(strings.ToLower(autopilot), "full") {
		t.Errorf("workflows/autopilot/workflow.md must state autopilot always verifies full")
	}
	for _, d := range []string{"docs/concepts.md", "docs/user-guide.md", "docs/bundles.md"} {
		if !strings.Contains(w19Read(t, d), "scoped") {
			t.Errorf("%s must describe the scoped tier and the marker opt-in", d)
		}
	}
	if strings.Contains(w19Read(t, "workflows/release/workflow.md"), "scoped") {
		t.Errorf("the release bundle must remain untouched: no scoped tier input")
	}
}

func TestW19ScopedObligationsPinnedInLaneText(t *testing.T) {
	ver := strings.ToLower(w19Read(t, "workflows/_shared/steps/verification.md"))
	for _, want := range []string{"contract", "declar", "fall"} {
		if !strings.Contains(ver, want) {
			t.Errorf("verification.md must state the scoped obligations (contract-linked tests, the repo declaration, fail-closed fallback) — %q missing", want)
		}
	}
	if !strings.Contains(strings.ToLower(w19Read(t, "workflows/bug-fix/workflow.md")), "repro") {
		t.Errorf("the bug-fix lane text must keep the repro mandatory inside a scoped run")
	}
	if !strings.Contains(strings.ToLower(w19Read(t, "workflows/feature-full/workflow.md")), "scenario") {
		t.Errorf("the feature-full lane text must keep the authored acceptance scenarios mandatory inside a scoped run")
	}
}
