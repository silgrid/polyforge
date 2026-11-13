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

// laneRootOverride, when set (useLaneTree), is the workflows root a test
// materializes every bundle from instead of the shipped tree: the
// test-local lanes the generic-mechanism tests run on (attrs, handoff) —
// no shipped lane declares those keys since, and the frozen
// copies of the lanes that did went with. Driver tests run
// serially (none calls t.Parallel), so one package-level root is safe.
var laneRootOverride string

// workflowsRootFor answers the workflows root a test materializes bundle
// from: the shipped tree, or the test's own lane tree (useLaneTree).
func workflowsRootFor(t *testing.T, bundle string) string {
	t.Helper()
	if laneRootOverride != "" {
		return laneRootOverride
	}
	return shippedWorkflowsDir
}

// newTestSpawnAdapter is NewStepSpawnAdapter (the tests' one entry).
func newTestSpawnAdapter(a StepSpawnAdapter) (*StepSpawnAdapter, error) {
	return NewStepSpawnAdapter(a)
}

// useLaneTree copies the shipped workflows tree, applies edits (a
// tree-relative file → its {old, new} text; an old text the file does not
// carry fails the test) and makes the copy the root every bundle of this
// test materializes from (workflowsRootFor), until the test ends.
func useLaneTree(t *testing.T, edits map[string][2]string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "workflows")
	err := filepath.Walk(shippedWorkflowsDir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(shippedWorkflowsDir, p)
		out := filepath.Join(dst, rel)
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	for rel, e := range edits {
		p := filepath.Join(dst, filepath.FromSlash(rel))
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(raw), e[0]) {
			t.Fatalf("useLaneTree: %s does not carry %q", rel, e[0])
		}
		if err := os.WriteFile(p, []byte(strings.Replace(string(raw), e[0], e[1], 1)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	laneRootOverride = dst
	t.Cleanup(func() { laneRootOverride = "" })
	return dst
}

// shippedBundleDirs answers every shipped bundle (a workflows/ child with
// a workflow.yaml; _shared is not one).
func shippedBundleDirs(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(shippedWorkflowsDir, "*", workflow.WorkflowFile))
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
// shippedDefinitions answers the definitions an agent reads: every
// shipped bundle's agent steps (no shipped lane dispatches a
// sub-definition any more: the integration lane's ruling and fix-ticket
// writer went with its migration to agent steps, workflow contract v2).
func shippedDefinitions(t *testing.T) [][2]string {
	t.Helper()
	return shippedBundleSteps(t)
}

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

// capsuleWithShippedBundle copies the shipped bundle into a fresh capsule
// as workflows/<bundle>/ and answers the capsule dir.
func capsuleWithShippedBundle(t *testing.T, bundle string) string {
	t.Helper()
	capDir := t.TempDir()
	root := workflowsRootFor(t, bundle)
	for _, tree := range []string{bundle, workflow.SharedDir} {
		src := filepath.Join(root, tree)
		err := filepath.Walk(src, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			raw, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(src, p)
			writeCapsuleFile(t, capDir, filepath.ToSlash(filepath.Join("workflows", tree, rel)), string(raw))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return capDir
}

func spawnPublishPrompt(t *testing.T, capDir, bundle string) string {
	t.Helper()
	sp := &captureSpawner{}
	ad, err := newTestSpawnAdapter(StepSpawnAdapter{
		WI:           WIInfo{ID: "wi_sh", CapsuleDir: capDir, BundleName: bundle, SessionHandle: "ab12cd34"},
		Spawner:      sp,
		PollInterval: 2 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("NewStepSpawnAdapter: %v", err)
	}
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: publishStepID, SpawnN: 1}); err != nil {
		t.Fatalf("SpawnStep publish: %v", err)
	}
	p, _ := sp.last()
	return p.InitialPrompt
}

// The shared layout: micro's publish resolves from workflows/_shared/ in
// the capsule, and a bundle's own steps/publish.md still wins over it.
func TestCapsuleSharedPublishAndOwnFileWins(t *testing.T) {
	shared, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, "steps", "publish.md"))
	if err != nil {
		t.Fatal(err)
	}
	principles, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, workflow.SharedDir, workflow.PrinciplesFile))
	if err != nil {
		t.Fatal(err)
	}
	capDir := capsuleWithShippedBundle(t, "micro")
	writeCapsuleFile(t, capDir, "workflows/_shared/steps/publish.md", string(shared))
	// materialization copies the whole _shared/, the principles publish includes among it
	writeCapsuleFile(t, capDir, "workflows/_shared/"+workflow.PrinciplesFile, string(principles))
	if p := spawnPublishPrompt(t, capDir, "micro"); !strings.Contains(p, "gate (develop's `verification`, micro's `evidence`) ran over the result") || !strings.Contains(p, "# Skill") {
		t.Fatalf("shared publish not used\n%s", p)
	}
	// (a definition declares no output schema since workflow contract 4)
	writeCapsuleFile(t, capDir, "workflows/micro/steps/publish.md", "---\nname: publish\ndescription: probe step publish\n---\n\nbundle-own publish override")
	if p := spawnPublishPrompt(t, capDir, "micro"); !strings.Contains(p, "bundle-own publish override") {
		t.Fatalf("the bundle's own publish.md must win\n%s", p)
	}
}

// The step id `verification` alone implies no command evidence in the
// dispatch prompt (decision 2026-10-16 G10), and since the notary left
// (workflow contract v2 change 18) no declaration does either.
func TestVerificationIDAloneOwesNoEvidenceInPrompt(t *testing.T) {
	ad, sp, capDir := spawnEnv(t) // its verification declares no flag
	grillDone(t, capDir)
	if _, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 1}); err != nil {
		t.Fatal(err)
	}
	p, _ := sp.last()
	if strings.Contains(p.InitialPrompt, "command_evidence") || strings.Contains(p.InitialPrompt, "pf bar-run") {
		t.Fatalf("a verification step must not be told to carry command_evidence or run pf bar-run\n%s", p.InitialPrompt)
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
	root := workflowsRootFor(t, bundle)
	shared, _ := filepath.Glob(filepath.Join(root, workflow.SharedDir, "*.md"))
	stepDefs, _ := filepath.Glob(filepath.Join(root, workflow.SharedDir, "steps", "*.md"))
	shared = append(shared, stepDefs...)
	for _, p := range shared {
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(root, p)
		writeCapsuleFile(t, capDir, "workflows/"+filepath.ToSlash(rel), string(raw))
	}
	sp := &captureSpawner{}
	ad, err := newTestSpawnAdapter(StepSpawnAdapter{
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
// facts rendered from the workflow.yaml step entry's vars. Every bundle's
// dispatched prompt must still carry the facts its pre-dedup step file
// carried (artifact names, lane checks, rewind target) — and none of
// another lane's. (The rows of the lanes removed went with
// them; the schema requirements and the # Verify first section with
// workflow contract 4: what review reads first is its skill's
// 开工前读什么、核对什么.) Facts are matched with whitespace folded.
func TestSharedStepPromptsKeepLaneFacts(t *testing.T) {
	type facts struct{ want []string }
	lanes := map[string]map[string]facts{
		"review": {
			// develop judges coverage per criterion and the
			// unit tests after their red commit — since workflow contract
			// v2 PR 12 on the facts its skill's facts.py lines write (the
			// owner-replies check and the facts.py lines themselves are
			// test/w403_develop_grill_e2e_contract_test.go's and
			// test/w374_develop_driver_contract_test.go's,)
			"develop": {[]string{"**Coverage, criterion by criterion (`ac_gap`).**", "**Tests after their red commit (`test_weakened`).**",
				"concepts the work item's acceptance criteria (or the plan's owner-confirmed `## Acceptance` list) did not request"}},
		},
	}
	common := map[string][]string{
		//'s scoped facts (`steps.review.output.scope`, the
		// automatic-merge block on disagreement) ride a SCOPED dispatch
		// only since TestW80VerificationPromptShapes.
		"review": {"No faked concurrency semantics", "**Unruled destination change.**", "**I/O under a lock.**", "**Weak tests.**", "**Stale docs.**", "**Leaked credentials**", "**Guess as fact.**", "**Do not run tests or builds.**", "Write `artifacts/review.md`", "If any of the change's own requirements",
			// review runs BEFORE verification — it never reads a
			// verification report, and the project-rules violation keeps its
			// blocking status at any position in any round.
			"The machine gate is `verification`, the step AFTER you",
			"a violation of any AGENTS.md rule", "blocks at any position, in any round",
			// contract 3's "summarizes every `non-blocking` item into" (publish's
			// summary): publish lists them in the delivery note and the PR body
			"`publish` lists every `non-blocking` item in the delivery note and the PR body",
			// (polyforge#222's text-only boundary left the shared review with
			// the text-only sweep, workflow contract v2 PR 12: no shipped lane
			// declares a sweep)
			// the round contract and the two faces (the review skill since
			// workflow contract 4: TestW80ReviewPromptShapes); every round
			// names the boundary.
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
			raw := spawnStepPrompt(t, bundle, stepID)
			if strings.Contains(raw, "{{") {
				t.Errorf("%s/%s prompt carries an unrendered placeholder", bundle, stepID)
			}
			prompt := strings.Join(strings.Fields(raw), " ")
			// the rewind target the text names is the gate's declared on_fail
			// target (contract 3's `pending_rewind_input`: the target's next
			// round reads the gate's section first)
			target := "`" + spec.OnFail.Target + "`"
			if !strings.Contains(prompt, target+"'s next round does not have to guess") || !strings.Contains(prompt, "A FAIL sends the lane back to "+target+", which resumes its previous session and reads your section first") {
				t.Errorf("%s/%s prompt does not name its on_fail target %s as the rewind receiver", bundle, stepID, spec.OnFail.Target)
			}
			for _, w := range append(f.want, common[stepID]...) {
				if !strings.Contains(prompt, strings.Join(strings.Fields(w), " ")) {
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

// TestSharedFragmentRendersOncePerDefinition: a shared
// fragment a step file includes renders exactly once in that step's
// rendered definition — the one-source rule (a second copy of a block
// the fragment owns, or a step file that stopped including it, drifts).
// The fragment's text is matched whole, so a partial re-copy inside a
// step file is not mistaken for the include.
func TestSharedFragmentRendersOncePerDefinition(t *testing.T) {
	fragmentOnce := map[string][][2]string{
		// the per-unit fast-check bar's definition and its run-time
		// imperatives ( W2): the shipped build step that runs
		// it. (The retired lanes' plan, code and fix steps and their
		// handoff_duty fragment went with, the unused
		// deliverable_carrier fragment with workflow contract 3.)
		"fast_checks.md":       {{"develop", "build"}},
		"fast_checks_build.md": {{"develop", "build"}},
		// the owner-review declaration: the shipped build
		// steps may declare material deliver's owner-review gate shows the
		// owner before merging.
		"owner_review.md": {{"develop", "build"}, {"micro", "fix"}},
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
}

// w80ReviewLaterRoundPhrases are the facts of the review's later-round
// material — contract 3's shared re-entry fragment
// (workflows/_shared/review_reentry.md), in the review skill since
// workflow contract 4:'s round contract and's
// two faces (the earlier round's findings inherited, never its
// conversation), each residual named by its place without re-deriving it.
// The fix surface is TestW109ReviewReentryFixSurfaceIsFirstParentNonMerge's.
var w80ReviewLaterRoundPhrases = []string{
	// contract 3's "**true bug anywhere**" and "new discovery on the fix surface"
	"a `bug` anywhere in the change", "a new defect on the fix surface",
	// contract 3's archived findings under `artifacts/steps/<this step's id>/`
	"What you inherit is that round's findings (its `artifacts/review.md`, read before you rewrite it, and its section)",
	"**Checklist face**", "**Fresh face**",
	"each finding of the latest earlier round", "is a **residual**", "**without re-deriving it**",
	// contract 3's "the conversation of a prior round is never inherited"
	"You always run fresh: nothing of an earlier round's conversation is yours.",
	"## A later round: the two faces and the round contract",
}

// TestW80ReviewPromptShapes (; workflow contract 4): the
// review's later-round material — the round contract and the two faces —
// lives in the review skill since contract 4 (review_reentry.md and the
// re-entry dispatch flag that rendered it on a re-entry only are gone), so
// every dispatch of the develop review carries it exactly once, and the
// step definition tells the agent which round it is in: a later round is
// one whose artifacts/handoff.md carries an earlier review section, else
// the first round, every checklist hit blocking. (contract 3's output
// schema, whose removed fields this test also pinned absent, is gone.)
func TestW80ReviewPromptShapes(t *testing.T) {
	// the shipped lanes whose review resolves to the shared definition
	// (micro ships its own review; the retired lanes went with)
	for _, bundle := range []string{"develop"} {
		raw := spawnStepPrompt(t, bundle, reviewStepID)
		if strings.Contains(raw, "{{") {
			t.Errorf("%s/review prompt carries an unrendered placeholder", bundle)
		}
		p := strings.Join(strings.Fields(raw), " ")
		for _, w := range w80ReviewLaterRoundPhrases {
			if !strings.Contains(p, strings.Join(strings.Fields(w), " ")) {
				t.Errorf("%s/review prompt lost the later-round fact %q", bundle, w)
			}
		}
		if n := strings.Count(p, "## A later round: the two faces and the round contract"); n != 1 {
			t.Errorf("%s/review prompt carries the round contract %d times, want 1", bundle, n)
		}
		for _, w := range []string{
			"When `artifacts/handoff.md` carries an earlier `review` section, this is a later round: your skill's two faces and round contract apply. Without one this is the first round — the full judgment, every checklist hit blocking.",
			"From the second round on, a blocking finding is one of: a residual; a new defect on the fix surface; a `bug` anywhere in the change; a `project_rule` violation anywhere. Every other hit is non-blocking.",
		} {
			if !strings.Contains(p, w) {
				t.Errorf("%s/review prompt lost %q", bundle, w)
			}
		}
	}
}
