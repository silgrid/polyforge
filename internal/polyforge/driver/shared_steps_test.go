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
	writeCapsuleFile(t, capDir, "workflows/autopilot/steps/deliver.md", string(shared))
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
	capDir := capsuleWithShippedBundle(t, "autopilot")
	writeCapsuleFile(t, capDir, "workflows/_shared/steps/deliver.md", string(shared))
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
	capDir := capsuleWithShippedBundle(t, bundle)
	shared, _ := filepath.Glob(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "steps", "*.md"))
	shared = append(shared, filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "mechanical-fix.md"))
	shared = append(shared, filepath.Join(shippedWorkflowsDir, workflow.SharedDir, workflow.PrinciplesFile))
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
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: stepID, SpawnN: 1}); err != nil {
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
				"there is no release slot behind you"}},
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
				"concepts the WI and the plan did not request", "(the WI and the plan)"}},
			"bug-fix": {[]string{"Read, in this order: `artifacts/root-cause.md`", "`root_cause_addressed: false`",
				"the *diagnosis* was wrong", "concepts the WI and the root-cause scope guidance did not request"}},
			"feature-full": {[]string{"Read, in this order: `artifacts/spec.md`", "`steps.e2e-author.output.already_satisfied`",
				"400-line", "concepts the spec did not request"}},
		},
	}
	common := map[string][]string{
		"verification": {"command_evidence: [{argv, exit_code, duration_ms}]", "AT LEAST ONE entry", "## Pre-existing failures", "`test_bar`", "`no-untracked-test-files`",
			"Write `artifacts/verification-report.md`", "which plan step or memo scope item"},
		"review": {"No faked concurrency semantics", "**Unruled destination change.**", "**I/O under a lock.**", "**Weak tests.**", "**Stale docs.**", "**Leaked credentials**", "**Guess as fact.**", "**Do not run tests or builds.**", "Write `artifacts/review.md`", "If any of the change's own requirements",
			// polyforge#222: the text-only boundary (defined once in the shared
			// review body) and the mis-marking judgment duty ride every lane's
			// review prompt — the marker the driver's light-rewind eligibility
			// reads, and the record the re-run judges the light round by.
			"comments and documentation in any file, test files included",
			"code, tests and assertions", "`workflows/**` `.md` files", "prompts drive step agents",
			"`text_only: true` only when its fix touches nothing but the",
			"non-blocking findings never enter the rule", "cause: gate_fail_rewind_text_only",
			// review B1: the trigger keys on the latest REWIND-ARCHIVE attempt
			// (a normal light completion appends an accepted attempt after the
			// light archive, so "the latest archived attempt" never fires at
			// re-run time), and the light round is anchored to the archived
			// record — the diff after the pre-light commits — never to
			// output.commits, which a resumed session may re-list in full.
			"the latest attempt whose cause is neither `accepted` nor `infra_failure`",
			"archived `result` holds the pre-light output",
			"what changed after the last sha that list names", "`git diff <that sha>..HEAD`",
			"BLOCKING mis-marking", "record it WITHOUT `text_only`",
			// workbench#29: review re-entry mode rides every lane's review
			// prompt — the archived structured findings list is inherited
			// (never the conversation), the checklist face references a
			// still-open prior blocking finding by its position, and the
			// fresh face still runs over the whole change.
			"`artifacts/steps/review/`", "checklist face", "fresh face",
			"prior round", "residual", "without re-deriving", "first round",
			"conversation of a prior review round is never inherited"},
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

// TestW19TierLaneTextContentContract (workbench#19 B4): pins the phrases
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
	if !strings.Contains(read("workflows/_shared/steps/verification.md"), "contract") {
		t.Errorf("verification.md does not state the scoped run narrows breadth, never the contract")
	}
}
