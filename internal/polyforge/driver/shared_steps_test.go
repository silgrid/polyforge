package driver

// shared_steps_test.go: the guard of decision 2026-10-16 G1 — step
// definitions shared across bundles live once under workflows/_shared/
// steps/, every declared step of every shipped bundle resolves to a
// definition (its own or the shared one), and no byte-identical copy
// creeps back into a bundle.

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

var shippedWorkflowsDir = filepath.Join("..", "..", "..", "workflows")

// shippedBundleDirs answers every shipped bundle (a workflows/ child with
// a workflow.md; _shared is not one).
func shippedBundleDirs(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(shippedWorkflowsDir, "*", "workflow.md"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no shipped bundles found: %v", err)
	}
	dirs := make([]string, 0, len(paths))
	for _, p := range paths {
		dirs = append(dirs, filepath.Dir(p))
	}
	return dirs
}

// shippedBundleSteps answers every (bundleDir, stepID) a shipped bundle
// declares.
func shippedBundleSteps(t *testing.T) [][2]string {
	t.Helper()
	var out [][2]string
	for _, dir := range shippedBundleDirs(t) {
		wf, err := LoadWorkflow(dir)
		if err != nil {
			t.Fatalf("%s: %v", dir, err)
		}
		for _, s := range wf.Steps {
			out = append(out, [2]string{dir, s.ID})
		}
	}
	return out
}

func TestShippedStepDefinitionsResolve(t *testing.T) {
	used := map[string]bool{}
	for _, bs := range shippedBundleSteps(t) {
		p := workflow.StepDefinitionPath(bs[0], bs[1])
		if _, err := os.Stat(p); err != nil {
			t.Errorf("%s/%s: no step definition (own or shared): %v", filepath.Base(bs[0]), bs[1], err)
		}
		used[filepath.Clean(p)] = true
		if _, err := readStepDefinition(bs[0], bs[1], shippedVars(t, bs[0], bs[1])); err != nil {
			t.Errorf("%s/%s: %v", filepath.Base(bs[0]), bs[1], err)
		}
	}
	shared, _ := filepath.Glob(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "steps", "*.md"))
	for _, p := range shared {
		if !used[filepath.Clean(p)] {
			t.Errorf("%s is used by no bundle (every bundle carries its own copy or omits the step)", p)
		}
	}
}

// TestShippedStepDefinitionsNotDuplicated: a step file byte-identical in
// two bundles, or a bundle copy identical to the shared one, must be
// moved to (or left in) workflows/_shared/steps/ instead — duplicated
// copies drift.
func TestShippedStepDefinitionsNotDuplicated(t *testing.T) {
	defs, err := filepath.Glob(filepath.Join(shippedWorkflowsDir, "*", "steps", "*.md"))
	if err != nil || len(defs) == 0 {
		t.Fatalf("no shipped step definitions found: %v", err)
	}
	byContent := map[string][]string{}
	for _, p := range defs {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		key := string(bytes.TrimSpace(raw))
		rel, _ := filepath.Rel(shippedWorkflowsDir, p)
		byContent[key] = append(byContent[key], filepath.ToSlash(rel))
	}
	for _, files := range byContent {
		if len(files) > 1 {
			sort.Strings(files)
			t.Errorf("identical step definitions %s: keep one copy under workflows/%s/steps/", strings.Join(files, ", "), workflow.SharedDir)
		}
	}
}

// Every shipped verification gate declares requires_command_evidence: the
// id no longer implies it (decision 2026-10-16 G10).
func TestShippedVerificationStepsDeclareCommandEvidence(t *testing.T) {
	n := 0
	for _, dir := range shippedBundleDirs(t) {
		wf, err := LoadWorkflow(dir)
		if err != nil {
			t.Fatal(err)
		}
		if s := wf.step(verificationStepID); s != nil {
			n++
			if !s.RequiresCommandEvidence {
				t.Errorf("%s: verification must declare requires_command_evidence: true", filepath.Base(dir))
			}
		}
	}
	if n == 0 {
		t.Fatal("no shipped verification step found")
	}
}

// capsuleWithShippedBundle copies the shipped bundle into a fresh capsule
// as workflows/<bundle>/ and answers the capsule dir.
func capsuleWithShippedBundle(t *testing.T, bundle string) string {
	t.Helper()
	capDir := t.TempDir()
	src := filepath.Join(shippedWorkflowsDir, bundle)
	err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		writeCapsuleFile(t, capDir, filepath.ToSlash(filepath.Join("workflows", bundle, rel)), string(raw))
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return capDir
}

func spawnDeliverPrompt(t *testing.T, capDir, bundle string) string {
	t.Helper()
	sp := &captureSpawner{}
	ad, err := NewStepSpawnAdapter(StepSpawnAdapter{
		WI:           WIInfo{ID: "wi_sh", CapsuleDir: capDir, BundleName: bundle, SessionHandle: "ab12cd34"},
		Spawner:      sp,
		PollInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStepSpawnAdapter: %v", err)
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "deliver", SpawnN: 1}); err != nil {
		t.Fatalf("SpawnStep deliver: %v", err)
	}
	p, _ := sp.last()
	return p.InitialPrompt
}

// A capsule materialized before G1 carries its own steps/deliver.md and no
// workflows/_shared/: deliver still resolves from the snapshot, schema
// included (same-daemon recovery of an old capsule).
func TestOldLayoutCapsuleOwnDeliverStillRuns(t *testing.T) {
	shared, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "steps", "deliver.md"))
	if err != nil {
		t.Fatal(err)
	}
	capDir := capsuleWithShippedBundle(t, "autopilot")
	// an old-layout snapshot predates the deliver step's {{principles}}
	// include and has no _shared/principles.md to render it
	writeCapsuleFile(t, capDir, "workflows/autopilot/steps/deliver.md", strings.ReplaceAll(string(shared), "{{principles}}", ""))
	if _, err := os.Stat(filepath.Join(capDir, "workflows", workflow.SharedDir)); !os.IsNotExist(err) {
		t.Fatalf("old layout must have no _shared: %v", err)
	}
	prompt := spawnDeliverPrompt(t, capDir, "autopilot")
	if !strings.Contains(prompt, strings.TrimSpace(stepDefBody(shared))[:80]) {
		t.Fatalf("deliver prompt does not carry the snapshot's deliver body\n%s", prompt)
	}
	s, err := loadStepOutputSchema(filepath.Join(capDir, "workflows", "autopilot"), "deliver", nil)
	if err != nil || s == nil || s.Schema == nil {
		t.Fatalf("old-layout deliver schema: %v", err)
	}
}

// New layout: deliver resolves from workflows/_shared/ in the capsule, and
// a bundle's own steps/deliver.md still wins over it.
func TestCapsuleSharedDeliverAndOwnFileWins(t *testing.T) {
	shared, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "steps", "deliver.md"))
	if err != nil {
		t.Fatal(err)
	}
	principles, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, workflow.PrinciplesFile))
	if err != nil {
		t.Fatal(err)
	}
	capDir := capsuleWithShippedBundle(t, "autopilot")
	writeCapsuleFile(t, capDir, "workflows/_shared/steps/deliver.md", string(shared))
	// materialization copies the whole _shared/, the principles deliver includes among it
	writeCapsuleFile(t, capDir, "workflows/_shared/"+workflow.PrinciplesFile, string(principles))
	if p := spawnDeliverPrompt(t, capDir, "autopilot"); !strings.Contains(p, "gh pr merge") {
		t.Fatalf("shared deliver not used\n%s", p)
	}
	writeCapsuleFile(t, capDir, "workflows/autopilot/steps/deliver.md", "bundle-own deliver override")
	if p := spawnDeliverPrompt(t, capDir, "autopilot"); !strings.Contains(p, "bundle-own deliver override") {
		t.Fatalf("the bundle's own deliver.md must win\n%s", p)
	}
}

// The step id `verification` alone implies no command evidence in the
// dispatch prompt either: only the frontmatter flag does (decision
// 2026-10-16 G10; TestCommandEvidenceFromDeclarationOnly pins the
// result-validation side).
func TestVerificationIDAloneOwesNoEvidenceInPrompt(t *testing.T) {
	ad, sp, capDir := spawnEnv(t) // its verification declares no flag
	grillDone(t, capDir)
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 1}); err != nil {
		t.Fatal(err)
	}
	p, _ := sp.last()
	if strings.Contains(p.InitialPrompt, "command_evidence") {
		t.Fatalf("an undeclared verification step must not be told to carry command_evidence\n%s", p.InitialPrompt)
	}
	for i := range ad.Workflow.Steps {
		if ad.Workflow.Steps[i].ID == "verification" {
			ad.Workflow.Steps[i].RequiresCommandEvidence = true
		}
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 2}); err != nil {
		t.Fatal(err)
	}
	if p, _ = sp.last(); !strings.Contains(p.InitialPrompt, "this step requires `command_evidence`") {
		t.Fatalf("a declared step must be told to carry command_evidence\n%s", p.InitialPrompt)
	}
}

// spawnStepPrompt dispatches stepID of the shipped bundle from a fresh
// capsule carrying the bundle and the shipped workflows/_shared/ (the
// materialized layout) and answers the assembled prompt.
func spawnStepPrompt(t *testing.T, bundle, stepID string) string {
	t.Helper()
	return spawnStepPromptWith(t, bundle, StepSpawn{StepID: stepID, SpawnN: 1})
}

// spawnStepPromptWith is spawnStepPrompt for one dispatch shape (the
// tier, a re-entry fact): the same shipped bundle, the given StepSpawn.
func spawnStepPromptWith(t *testing.T, bundle string, spawn StepSpawn) string {
	t.Helper()
	stepID := spawn.StepID
	capDir := capsuleWithShippedBundle(t, bundle)
	// the whole top-level _shared/ — the step definitions it ships beside
	// the fragments a definition may include ({{principles}} and the
	// fragment set); a missed file would fail a prompt test
	// with a load error the moment a step uses its fragment.
	shared, _ := filepath.Glob(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "*.md"))
	stepDefs, _ := filepath.Glob(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "steps", "*.md"))
	shared = append(shared, stepDefs...)
	for _, p := range shared {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(shippedWorkflowsDir, p)
		writeCapsuleFile(t, capDir, "workflows/"+filepath.ToSlash(rel), string(raw))
	}
	sp := &captureSpawner{}
	ad, err := NewStepSpawnAdapter(StepSpawnAdapter{
		WI:           WIInfo{ID: "wi_sh", CapsuleDir: capDir, BundleName: bundle, SessionHandle: "ab12cd34"},
		Spawner:      sp,
		PollInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStepSpawnAdapter: %v", err)
	}
	if _, err := ad.SpawnStep(context.Background(), spawn); err != nil {
		t.Fatalf("SpawnStep %s/%s: %v", bundle, stepID, err)
	}
	p, _ := sp.last()
	return p.InitialPrompt
}

// TestSharedStepPromptsKeepLaneFacts (decision 2026-10-16 G1 follow-up):
// verification and review are one shared text each, the lane-specific
// facts rendered from the workflow.md step entry's vars. Every bundle's
// dispatched prompt must still carry the facts its pre-dedup step file
// carried (artifact names, lane checks, rewind target, schema
// requirements) — and none of another lane's.
func TestSharedStepPromptsKeepLaneFacts(t *testing.T) {
	type facts struct{ want []string }
	lanes := map[string]map[string]facts{
		"verification": {
			"chore": {[]string{"`plan-conformance`", "`artifacts/plan.md`'s", "any test the plan names as pinning this change",
				"there is no release slot behind you",
				// the two-way facts and their severities.
				"plan-conformance facts section", "coupled obligation left unchanged, fails this check",
				"declared plain write left unchanged is recorded, not failed"}},
			"autopilot": {[]string{"`memo-conformance`", "`artifacts/fix-memo.md`'s `## Scope` and `## Non-goals`",
				"the memo's `## What is broken or missing` case", "no separate review step behind you"}},
			"bug-fix": {[]string{"Read `artifacts/repro.md` and execute its `## Steps` verbatim", "(`reproduces: true`)",
				"a check named `repro` with status `pass`", "every test in `steps.repro.output.test_files`",
				"`steps.fix.output.regression_test`", "*the artifact that originally failed now passes*"}},
			"feature-full": {[]string{"Copy `steps.e2e-author.output.scenario_titles`", "(`authored-scenarios`)",
				"required: [verdict, checks, authored_titles, scenarios]", "the authored acceptance tests (`steps.e2e-author.output.test_files`)",
				"`fail_reason` names its title and\nexpected vs observed"}},
		},
		"review": {
			"chore": {[]string{"`artifacts/code-summary.md`", "Judge the deletion-type acceptance bar",
				"concepts the WI and the plan did not request", "(the WI and the plan)",
				// the two-way facts.
				"Judge plan conformance in both directions from the plan-conformance facts"}},
			"bug-fix": {[]string{"Read, in this order: `artifacts/root-cause.md`", "`root_cause_addressed: false`",
				"the *diagnosis* was wrong",
				// a false fix never reaches review; the one-time
				// driver rewind and the owner question on a second false.
				"never reaches you", "a second false asks the", "concepts the WI and the root-cause scope guidance did not request"}},
			"feature-full": {[]string{"Read, in this order: `artifacts/spec.md`", "`steps.e2e-author.output.already_satisfied`",
				"400-line", "concepts the spec did not request",
				"**Plan conformance, both directions.**"}},
		},
	}
	common := map[string][]string{
		"verification": {
			// the command-evidence obligation lives once, in the
			// driver's delivery contract (every shipped verification declares
			// requires_command_evidence).
			"Run at least one real check command", "never completes on reading code alone",
			"## Pre-existing failures", "`test_bar`", "`no-untracked-test-files`",
			"Write `artifacts/verification-report.md`", "which plan step or memo scope item"},
		//'s scoped facts (`steps.review.output.scope`, the
		// automatic-merge block on disagreement) ride a SCOPED dispatch
		// only since TestW80VerificationPromptShapes.
		"review": {"No faked concurrency semantics", "**Unruled destination change.**", "**I/O under a lock.**", "**Weak tests.**", "**Stale docs.**", "**Leaked credentials**", "**Guess as fact.**", "**Do not run tests or builds.**", "Write `artifacts/review.md`", "If any of the change's own requirements",
			// review runs BEFORE verification — it never reads a
			// verification report, and the project-rules violation keeps its
			// blocking status at any position in any round.
			"The machine gate is `verification`, the step AFTER you",
			"a violation of any AGENTS.md rule", "blocks at any position, in any round",
			"summarizes every `non-blocking` item into",
			// polyforge#222: the text-only boundary (defined once in the shared
			// review body) rides every lane's review prompt — the marker the
			// driver's light-rewind eligibility reads.
			"comments and documentation in any file, test files included",
			"code, tests and assertions", "`workflows/**` `.md` files", "prompts drive step agents",
			"`text_only: true` only when its fix touches nothing but the",
			"non-blocking findings never enter the rule",
			// the round contract, the re-entry faces and the
			// light-round mis-marking duty ride a RE-ENTRY dispatch only
			// (TestW80ReviewPromptShapes); every round names the boundary.
			"first round"},
	}
	for stepID, byBundle := range lanes {
		for bundle, f := range byBundle {
			wf, err := LoadWorkflow(filepath.Join(shippedWorkflowsDir, bundle))
			if err != nil {
				t.Fatal(err)
			}
			spec := wf.step(stepID)
			if spec == nil || spec.OnFail == nil {
				t.Fatalf("%s/%s: no gate with on_fail", bundle, stepID)
			}
			if p := workflow.StepDefinitionPath(filepath.Join(shippedWorkflowsDir, bundle), stepID); filepath.Base(filepath.Dir(filepath.Dir(p))) != workflow.SharedDir {
				t.Errorf("%s/%s resolves to %s, want the shared definition", bundle, stepID, p)
			}
			prompt := spawnStepPrompt(t, bundle, stepID)
			if strings.Contains(prompt, "{{") {
				t.Errorf("%s/%s prompt carries an unrendered placeholder", bundle, stepID)
			}
			// the rewind target the text names is the gate's declared on_fail target
			target := "`" + spec.OnFail.Target + "`'s"
			if !strings.Contains(prompt, target+" `pending_rewind_input`") && !strings.Contains(prompt, target+" next attempt") {
				t.Errorf("%s/%s prompt does not name its on_fail target %s as the rewind receiver", bundle, stepID, spec.OnFail.Target)
			}
			for _, w := range append(f.want, common[stepID]...) {
				if !strings.Contains(prompt, w) {
					t.Errorf("%s/%s prompt lost lane fact %q", bundle, stepID, w)
				}
			}
			// review runs before verification — its prompt never
			// names the verification report as its evidence.
			if stepID == reviewStepID && strings.Contains(prompt, "verification-report") {
				t.Errorf("%s/review prompt still reads a verification report", bundle)
			}
			for other, of := range byBundle {
				if other == bundle {
					continue
				}
				for _, w := range of.want {
					if strings.Contains(prompt, w) {
						t.Errorf("%s/%s prompt carries %s's lane fact %q", bundle, stepID, other, w)
					}
				}
			}
		}
	}
}

// TestMechanicalClassifySectionRendersOnArmedLanesOnly (polyforge#260):
// the shared classification duty (workflows/_shared/mechanical-fix.md)
// rides an armed verification prompt as its own section - feature-full
// declares mechanical_fix: true - and no unarmed lane's verification
// prompt carries any of it (an unarmed FAIL rewinds exactly as today, so
// its agent is never told to classify).
func TestMechanicalClassifySectionRendersOnArmedLanesOnly(t *testing.T) {
	p := spawnStepPrompt(t, "feature-full", "verification")
	for _, want := range []string{
		"# Mechanical classification (this lane is armed)",
		"mechanical fast lane",
		"FAIL mechanical: write `mechanical_bound`",
		"the rule is all-or-nothing",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("feature-full's armed verification prompt lost the classification fact %q", want)
		}
	}
	for _, bundle := range []string{"bug-fix", "chore", "autopilot"} {
		p := spawnStepPrompt(t, bundle, "verification")
		for _, noise := range []string{
			"# Mechanical classification (this lane is armed)",
			"FAIL mechanical: write `mechanical_bound`",
			"the rule is all-or-nothing",
		} {
			if strings.Contains(p, noise) {
				t.Errorf("%s's unarmed verification prompt carries the classification duty %q", bundle, noise)
			}
		}
	}
}

// TestSharedFragmentRendersOncePerDefinition: a shared
// fragment a step file includes renders exactly once in that step's
// rendered definition — the one-source rule (a second copy of a block
// the fragment owns, or a step file that stopped including it, drifts).
// The fragment's text is matched whole, so a partial re-copy inside a
// step file is not mistaken for the include.
func TestSharedFragmentRendersOncePerDefinition(t *testing.T) {
	fragmentOnce := map[string][][2]string{
		// the per-unit fast-check bar's definition: the four build steps
		// that run it and the two plan steps that declare it.
		"fast_checks.md": {
			{"chore", "code"}, {"feature-full", "code"}, {"bug-fix", "fix"}, {"autopilot", "fix"},
			{"chore", "plan"}, {"feature-full", "plan"},
		},
		// the run-time imperatives of that bar ( W2): only the
		// four build steps that run it — a plan step declares the bar and
		// runs nothing.
		"fast_checks_build.md": {
			{"chore", "code"}, {"feature-full", "code"}, {"bug-fix", "fix"}, {"autopilot", "fix"},
		},
		// the handoff duty of a handoff-declaring step: the three shipped
		// steps that carried the block. The other handoff-declaring steps
		// (bug-fix/root-cause, feature-full/grill-review) carry no handoff
		// text of their own; the driver's delivery contract line states the
		// capsule-root-bound path and the path convention for every one.
		"handoff_duty.md": {
			{"chore", "plan"}, {"feature-full", "grill"}, {"autopilot", "grill"},
		},
		// the deliverable-carrier duty: the two plan steps
		// map every downstream deliverable to the step contract carrying it.
		"deliverable_carrier.md": {
			{"chore", "plan"}, {"feature-full", "plan"},
		},
		// the owner-review declaration: the two plan steps
		// and the four build steps may declare material deliver's
		// owner-review gate shows the owner before merging.
		"owner_review.md": {
			{"chore", "plan"}, {"feature-full", "plan"},
			{"chore", "code"}, {"feature-full", "code"}, {"bug-fix", "fix"}, {"autopilot", "fix"},
		},
	}
	for frag, targets := range fragmentOnce {
		raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, frag))
		if err != nil {
			t.Fatalf("fragment %s: %v", frag, err)
		}
		text := strings.TrimSpace(string(raw))
		for _, bs := range targets {
			bundle, step := bs[0], bs[1]
			def, err := readStepDefinition(filepath.Join(shippedWorkflowsDir, bundle), step, shippedVars(t, filepath.Join(shippedWorkflowsDir, bundle), step))
			if err != nil {
				t.Fatalf("%s/%s: %v", bundle, step, err)
			}
			if n := strings.Count(def, text); n != 1 {
				t.Errorf("%s/%s renders the %s fragment %d times, want exactly 1", bundle, step, frag, n)
			}
		}
	}
	// W2: the plan steps DECLARE the bar — they never carry
	// the build step's run-time imperatives, and each keeps its own
	// declaration of where the full suite belongs (feature-full's "not
	// even for the final unit" included).
	build, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "fast_checks_build.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ bundle, want string }{
		{"chore", "The full suite is not a per-unit bar — it belongs to `verification`."},
		{"feature-full", "The full suite is NOT a per-unit bar, not even for the final unit - it belongs to `verification`"},
	} {
		def, err := readStepDefinition(filepath.Join(shippedWorkflowsDir, c.bundle), "plan", shippedVars(t, filepath.Join(shippedWorkflowsDir, c.bundle), "plan"))
		if err != nil {
			t.Fatalf("%s/plan: %v", c.bundle, err)
		}
		flat := strings.Join(strings.Fields(def), " ")
		for _, imperative := range []string{strings.Join(strings.Fields(string(build)), " "), "do not run the full suite", "do not proceed on top of a red fast check"} {
			if strings.Contains(flat, imperative) {
				t.Errorf("%s/plan carries the build step's run-time imperative %q — a plan step declares the bar, it runs nothing", c.bundle, imperative)
			}
		}
		if !strings.Contains(flat, c.want) {
			t.Errorf("%s/plan lost its full-suite declaration %q", c.bundle, c.want)
		}
	}
}

// TestHandoffDutyFragmentAndContractPath: the handoff
// duty block lives once in workflows/_shared/handoff_duty.md (the
// exactly-once pin is TestSharedFragmentRendersOncePerDefinition); the
// driver's delivery contract stays self-sufficient for every
// handoff-declaring step — the capsule-root-bound path of the handoff file
// plus the path-convention clause — whether or not the step includes the
// fragment (chore/plan does; bug-fix/root-cause carries no handoff text).
func TestHandoffDutyFragmentAndContractPath(t *testing.T) {
	for _, bs := range [][2]string{{"chore", "plan"}, {"bug-fix", "root-cause"}} {
		bundle, step := bs[0], bs[1]
		capDir := capsuleWithShippedBundle(t, bundle)
		for _, name := range []string{"fast_checks.md", "handoff_duty.md", "principles.md", "deliverable_carrier.md", "owner_review.md"} {
			raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, name))
			if err != nil {
				t.Fatal(err)
			}
			writeCapsuleFile(t, capDir, "workflows/"+workflow.SharedDir+"/"+name, string(raw))
		}
		sp := &captureSpawner{}
		ad, err := NewStepSpawnAdapter(StepSpawnAdapter{
			WI:           WIInfo{ID: "wi_hd", CapsuleDir: capDir, BundleName: bundle, SessionHandle: "ab12cd34"},
			Spawner:      sp,
			PollInterval: 2 * time.Millisecond,
		})
		if err != nil {
			t.Fatalf("NewStepSpawnAdapter: %v", err)
		}
		if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: step, SpawnN: 1}); err != nil {
			t.Fatalf("SpawnStep %s/%s: %v", bundle, step, err)
		}
		p, _ := sp.last()
		want := "write the handoff file at `" + capDir + "/artifacts/handoff-" + step + ".md` — the next inherit step reads it by that path convention"
		if !strings.Contains(p.InitialPrompt, want) {
			t.Errorf("%s/%s: the handoff-declaring prompt lost the absolute handoff path or its path-convention clause %q\n%s", bundle, step, want, p.InitialPrompt)
		}
		if strings.Contains(p.InitialPrompt, "handoff duty section states") {
			t.Errorf("%s/%s: the delivery contract points at a handoff duty section the step may not carry\n%s", bundle, step, p.InitialPrompt)
		}
	}
}

// TestNoStaticLightRewindSection: the light re-entry
// instruction is the driver's LightRewindNote/MechanicalRewindNote,
// rendered as its own prompt section exactly on a light rewind — no
// shipped build-step definition carries a static second copy (which
// would render on every normal dispatch too). The light-rewind
// presence side is spawn_test.go's note-render pin.
func TestNoStaticLightRewindSection(t *testing.T) {
	for _, bs := range [][2]string{{"bug-fix", "fix"}, {"chore", "code"}, {"feature-full", "code"}, {"autopilot", "fix"}} {
		bundle, step := bs[0], bs[1]
		raw, err := workflow.ReadStepDefinition(filepath.Join(shippedWorkflowsDir, bundle), step, shippedVars(t, filepath.Join(shippedWorkflowsDir, bundle), step))
		if err != nil {
			t.Fatalf("%s/%s: %v", bundle, step, err)
		}
		body := stepDefBody(raw)
		if strings.Contains(body, "Light rewind re-entry") {
			t.Errorf("%s/%s carries a static light re-entry section (the driver's note is the single copy)", bundle, step)
		}
	}
}

// TestW19TierLaneTextContentContract ( B4): pins the phrases
// this change adds to the SHIPPED lane text and docs. It reads the files
// themselves - never a step's echo of a question it scripted - and every
// phrase below is absent at base 314ec6e6. This is the content contract
// the authored acceptance suite's text checks were too weak to assert.
func TestW19TierLaneTextContentContract(t *testing.T) {
	repoRoot := filepath.Join("..", "..", "..")
	read := func(rel string) string {
		t.Helper()
		raw, err := os.ReadFile(filepath.Join(repoRoot, filepath.FromSlash(rel)))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(raw)
	}
	deliver := read("workflows/_shared/steps/deliver.md")
	for _, want := range []string{
		"全量重验",
		"rewind_to: verification",
		"exactly three answers",
		"steps.verification.output.scope",
	} {
		if !strings.Contains(deliver, want) {
			t.Errorf("shipped deliver.md lost %q", want)
		}
	}
	// The rewind_to override must be declared in the output schema, not
	// only in prose (N1).
	if !strings.Contains(deliver, "rewind_to:\n      type: string\n      enum: [verification]") {
		t.Errorf("shipped deliver.md output schema does not declare rewind_to enum [verification]")
	}
	// Phrases new at base 314ec6e6 in the docs the tier must reach.
	for _, rel := range []string{"docs/concepts.md", "docs/user-guide.md", "workflows/bug-fix/workflow.md"} {
		if !strings.Contains(read(rel), "verification: scoped") {
			t.Errorf("%s does not name the opt-in marker `verification: scoped`", rel)
		}
	}
	// the scoped rules live in the shared scoped material
	// the driver renders on a scoped verification dispatch only.
	if !strings.Contains(read("workflows/_shared/scoped_verification.md"), "narrows breadth, never the contract") {
		t.Errorf("scoped_verification.md does not state the scoped run narrows breadth, never the contract")
	}
}

// w80SchemaBlock answers the output-schema block a dispatched prompt
// shows ("" when it shows none).
func w80SchemaBlock(prompt string) string {
	const open = "```yaml\n"
	i := strings.Index(prompt, "# Output schema")
	if i < 0 {
		return ""
	}
	j := strings.Index(prompt[i:], open)
	if j < 0 {
		return ""
	}
	rest := prompt[i+j+len(open):]
	if k := strings.Index(rest, "\n```"); k >= 0 {
		return rest[:k]
	}
	return rest
}

// w80ScopedVerificationPhrases are phrases of the shared scoped-tier
// material (workflows/_shared/scoped_verification.md) only a scoped
// verification dispatch carries.
var w80ScopedVerificationPhrases = []string{
	"narrows breadth, never the contract",
	"Fast-gate scope comes from the release-shipped engine",
	"Any engine failure falls back to the full tier",
	"**Incremental re-verification.**",
	"the driver blocks the automatic merge when the two",
	"`steps.review.output.scope`",
	"`.polyforge/verify-scope.conf`",
}

// TestW80VerificationPromptShapes: the verification
// prompt carries its rare-path material only on the dispatch shape that
// needs it. A plain FULL dispatch shows no scoped-tier rules and no
// `scope` schema; an unarmed lane shows no `mechanical_bound`; a lane
// without an authored suite shows no `authored_titles`/`scenarios`. A
// SCOPED dispatch carries the scoped material exactly once plus the
// `scope` schema; the armed + authored lane (feature-full) shows its
// fragments. The x-prompt-when marker never reaches any prompt.
func TestW80VerificationPromptShapes(t *testing.T) {
	type shape struct{ armed, authored bool }
	lanes := map[string]shape{
		"chore":        {},
		"bug-fix":      {},
		"autopilot":    {},
		"feature-full": {armed: true, authored: true},
	}
	for bundle, sh := range lanes {
		full := spawnStepPromptWith(t, bundle, StepSpawn{StepID: verificationStepID, SpawnN: 1, ScopeTier: tierFull})
		schema := w80SchemaBlock(full)
		if schema == "" {
			t.Fatalf("%s/verification: no output schema rendered", bundle)
		}
		if strings.Contains(full, promptWhenKey) {
			t.Errorf("%s/verification prompt shows the %s marker", bundle, promptWhenKey)
		}
		if strings.Contains(schema, "\n    scope:") || strings.Contains(schema, "fast_gates_ref") {
			t.Errorf("%s/verification FULL prompt shows the scope schema:\n%s", bundle, schema)
		}
		for _, p := range w80ScopedVerificationPhrases[:4] {
			if strings.Contains(full, p) {
				t.Errorf("%s/verification FULL prompt carries scoped material %q", bundle, p)
			}
		}
		if !strings.Contains(full, "This work item runs the FULL tier") {
			t.Errorf("%s/verification FULL prompt lost the full-tier contract line", bundle)
		}
		if got := strings.Contains(schema, "mechanical_bound"); got != sh.armed {
			t.Errorf("%s/verification schema shows mechanical_bound = %v, want %v (armed)", bundle, got, sh.armed)
		}
		for _, f := range []string{"authored_titles", "\n    scenarios:"} {
			if got := strings.Contains(schema, f); got != sh.authored {
				t.Errorf("%s/verification schema shows %q = %v, want %v (authored)", bundle, f, got, sh.authored)
			}
		}
		// always-shown fields stay
		for _, f := range []string{"verdict:", "checks:", "test_bar:", "pre_existing_failures:", "oneOf:"} {
			if !strings.Contains(schema, f) {
				t.Errorf("%s/verification schema lost %q", bundle, f)
			}
		}
	}
	for _, bundle := range []string{"chore", "bug-fix", "feature-full"} {
		scoped := spawnStepPromptWith(t, bundle, StepSpawn{StepID: verificationStepID, SpawnN: 1, ScopeTier: tierScoped})
		for _, p := range w80ScopedVerificationPhrases {
			if !strings.Contains(scoped, p) {
				t.Errorf("%s/verification SCOPED prompt lost scoped fact %q", bundle, p)
			}
		}
		if n := strings.Count(scoped, "Any engine failure falls back to the full tier"); n != 1 {
			t.Errorf("%s/verification SCOPED prompt carries the scoped material %d times, want 1", bundle, n)
		}
		schema := w80SchemaBlock(scoped)
		if !strings.Contains(schema, "\n    scope:") || !strings.Contains(schema, "fast_gates_ref") {
			t.Errorf("%s/verification SCOPED prompt lost the scope schema", bundle)
		}
		if strings.Contains(scoped, promptWhenKey) {
			t.Errorf("%s/verification SCOPED prompt shows the %s marker", bundle, promptWhenKey)
		}
	}
	// another step never carries the scoped verification material
	review := spawnStepPromptWith(t, "chore", StepSpawn{StepID: reviewStepID, SpawnN: 1, ScopeTier: tierScoped})
	if strings.Contains(review, "Any engine failure falls back to the full tier") {
		t.Errorf("chore/review prompt carries the scoped verification material")
	}
}

// w80ReviewReentryPhrases are the facts of the shared re-entry material
// (workflows/_shared/review_reentry.md):'s round contract,
// polyforge#222's light-round mis-marking duty (keyed on the latest
// REWIND-ARCHIVE attempt and anchored to the archived pre-light record,
// never to output.commits) and's two re-entry faces (the
// archived findings list inherited, never the conversation).
var w80ReviewReentryPhrases = []string{
	"**true bug anywhere**", "new discovery on the fix surface",
	"cause: gate_fail_rewind_text_only",
	"the latest attempt whose cause is neither `accepted` nor `infra_failure`",
	"archived `result` holds the pre-light output",
	"what changed after the last sha that list names", "`git diff <that sha>..HEAD`",
	"BLOCKING mis-marking", "record it WITHOUT `text_only`",
	"`artifacts/steps/review/`", "checklist face", "fresh face",
	"prior round", "residual", "without re-deriving",
	"conversation of a prior review round is never inherited",
	"## The round contract",
}

// w80ScopedReviewPhrases are the facts of the shared scoped-review
// material (workflows/_shared/scoped_review.md).
var w80ScopedReviewPhrases = []string{
	"**Answer `scope_adequate` and record your `scope`",
	"automatic merge needs the two declarations to agree",
	"completed review of a scoped work item that omits `scope_adequate`",
}

// TestW80ReviewPromptShapes: the review prompt carries
// its rare-path material only on the dispatch that needs it. A FIRST
// round (no prior verdict round on record) carries none of the re-entry,
// round-contract or mis-marking text and no residual_of schema; a
// RE-ENTRY carries all of it once, with the lane's build step rendered.
// A full-tier work item's review shows no scope material or schema; a
// scoped one shows both.
func TestW80ReviewPromptShapes(t *testing.T) {
	for _, bundle := range []string{"chore", "bug-fix", "feature-full"} {
		wf, err := LoadWorkflow(filepath.Join(shippedWorkflowsDir, bundle))
		if err != nil {
			t.Fatal(err)
		}
		build := wf.buildStep()
		first := spawnStepPromptWith(t, bundle, StepSpawn{StepID: reviewStepID, SpawnN: 1, ScopeTier: tierFull})
		for _, p := range w80ReviewReentryPhrases {
			if p == "prior round" || p == "residual" {
				continue // the always-rendered pointer and output duty name them
			}
			if strings.Contains(first, p) {
				t.Errorf("%s/review FIRST-round prompt carries re-entry material %q", bundle, p)
			}
		}
		schema := w80SchemaBlock(first)
		for _, f := range []string{"residual_of", "scope_adequate", "\n    scope:", promptWhenKey} {
			if strings.Contains(schema, f) {
				t.Errorf("%s/review FIRST-round full-tier schema shows %q", bundle, f)
			}
		}
		for _, p := range w80ScopedReviewPhrases {
			if strings.Contains(first, p) {
				t.Errorf("%s/review full-tier prompt carries scoped material %q", bundle, p)
			}
		}
		if strings.Contains(first, "\n# Review re-entry (") {
			t.Errorf("%s/review FIRST-round prompt renders the re-entry section", bundle)
		}

		again := spawnStepPromptWith(t, bundle, StepSpawn{StepID: reviewStepID, SpawnN: 2, ScopeTier: tierFull, ReviewReentry: true})
		for _, p := range w80ReviewReentryPhrases {
			if !strings.Contains(again, p) {
				t.Errorf("%s/review RE-ENTRY prompt lost %q", bundle, p)
			}
		}
		if n := strings.Count(again, "## The round contract"); n != 1 {
			t.Errorf("%s/review RE-ENTRY prompt carries the round contract %d times, want 1", bundle, n)
		}
		if strings.Contains(again, "{{") {
			t.Errorf("%s/review RE-ENTRY prompt carries an unrendered placeholder", bundle)
		}
		if !strings.Contains(again, "`steps."+build+".attempts`") {
			t.Errorf("%s/review RE-ENTRY prompt does not name its build step %q in the mis-marking duty", bundle, build)
		}
		if !strings.Contains(w80SchemaBlock(again), "residual_of") {
			t.Errorf("%s/review RE-ENTRY schema lost residual_of", bundle)
		}

		scoped := spawnStepPromptWith(t, bundle, StepSpawn{StepID: reviewStepID, SpawnN: 1, ScopeTier: tierScoped})
		for _, p := range w80ScopedReviewPhrases {
			if !strings.Contains(scoped, p) {
				t.Errorf("%s/review SCOPED prompt lost %q", bundle, p)
			}
		}
		ss := w80SchemaBlock(scoped)
		if !strings.Contains(ss, "scope_adequate") || !strings.Contains(ss, "\n    scope:") {
			t.Errorf("%s/review SCOPED schema lost scope/scope_adequate", bundle)
		}
		if strings.Contains(scoped, promptWhenKey) {
			t.Errorf("%s/review SCOPED prompt shows the %s marker", bundle, promptWhenKey)
		}
	}
}

// TestW80PrepareDispatchReviewFacts: the review dispatch
// is a re-entry exactly when a prior verdict-bearing round is on record
// (steps.review.last_review_round), and its scoped material keys on the
// work item's opt-in - never on the recorded verification tier a
// verification FAIL resets to full (the scope_adequate validation keys
// on the opt-in too).
func TestW80PrepareDispatchReviewFacts(t *testing.T) {
	d := harnessDriver(t, nil, nil)
	sp, _, err := d.prepareDispatch(reviewStepID)
	if err != nil {
		t.Fatal(err)
	}
	if sp.ReviewReentry {
		t.Errorf("a review with no round on record dispatched as a re-entry")
	}
	d.exec.Steps[reviewStepID].LastReviewRound = &capsule.ReviewRound{Round: 1, Blocking: 1}
	d.scopeOptIn = true
	d.wi.BundleName = "chore"
	d.exec.ScopeTier = tierFull // a verification FAIL reset the recorded tier
	sp, _, err = d.prepareDispatch(reviewStepID)
	if err != nil {
		t.Fatal(err)
	}
	if !sp.ReviewReentry {
		t.Errorf("a review with a verdict round on record did not dispatch as a re-entry")
	}
	if sp.ScopeTier != tierScoped {
		t.Errorf("an opted-in review dispatched at tier %q, want %q (the opt-in, not the reset verification tier)", sp.ScopeTier, tierScoped)
	}
}
