package test

// v2_workflows_dir_test.go: the workflows directory a scenario's daemon
// runs: a copy of the shipped tree. The step definitions' `output:`
// schemas stay: the driver validates in record-only mode (polyforge#164
// H3), so the fake harness's placeholder outputs are recorded as
// output_schema_violation events, never refused. The delivery lanes end
// with the `deliver` step (polyforge#166) — the develop lane with
// `publish` and the `merge` that merges what publish opened
// — which push to the task repos' origins: scenarios that are not about
// delivery run without those step lines; the delivery scenarios set
// inst.keepDeliver. Every copy is a lane the contract accepts (the
// daemon refuses to start on one it refuses).

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// deliverStepBlock matches the delivery lanes' block-form delivery steps —
// develop's sync, verification, publish and merge, micro's sync, evidence,
// publish and merge, agent steps on their skills since workflow contract
// 2 — the `- id:` line and every continuation line (indented deeper than
// the list item, or blank), up to the next step entry or the
// frontmatter's end.
var deliverStepBlock = regexp.MustCompile(`(?m)^  - id: (sync|verification|evidence|publish|merge)\n(?:(?:    [^\n]*)?\n)*`)

// microFixOutputs matches micro fix's declared artifacts — the evidence
// plan the stripped evidence step would run and fix's own micro-bounds
// result, both written by the pre-written commands a scripted fix does
// not run: they leave with the delivery steps.
var microFixOutputs = regexp.MustCompile(`(?m)^    outputs:\n      artifacts: \[ \{ path: artifacts/fix/evidence\.json[^\n]*\n`)

// microReviewVerifies matches micro review's verifies of those two
// artifacts (the micro-bounds re-run), which leave with them.
var microReviewVerifies = regexp.MustCompile(`(?m)^    verifies:\n(?:      - \{ artifact: artifacts/(?:fix|facts)/[^\n]*\n)+`)

// stepWallBudget matches one step-level `budget: { wall_minutes: N }`
// override (the flow-map holds ONLY wall_minutes): an entry of a
// one-line step, or the whole `budget:` line of a block-form step.
var stepWallBudget = regexp.MustCompile(`,\s*budget:\s*\{\s*wall_minutes:\s*\d+\s*\}|(?m)^    budget: \{ wall_minutes: \d+ \}\n`)

// workflowsPath answers the --workflows-dir the daemon should use.
func (u *v1Install) workflowsPath() string {
	shipped := filepath.Join(u.root, "workflows")
	if u.strippedWF == "" && u.withoutLane != "" {
		u.strippedWF = copyWorkflowsTrees(u.t, nil, shipped)
		if err := os.RemoveAll(filepath.Join(u.strippedWF, u.withoutLane)); err != nil {
			u.t.Fatal(err)
		}
	}
	if u.strippedWF == "" {
		u.strippedWF = copyWorkflowsDir(u.t, shipped, !u.keepDeliver)
		if n := u.microMaxRewinds; n != 0 {
			setMicroMaxRewinds(u.t, u.strippedWF, n)
		}
		if u.stripWallBudgets {
			stripStepWallBudgets(u.t, u.strippedWF)
		}
	}
	return u.strippedWF
}

// setMicroMaxRewinds gives micro another max_rewinds in an already-copied
// workflows tree (v1Install.microMaxRewinds).
func setMicroMaxRewinds(t testing.TB, dir string, n int) {
	t.Helper()
	p := filepath.Join(dir, vehicleLane, workflow.WorkflowFile)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	at := regexp.MustCompile(`(?m)^max_rewinds: \d+\n`)
	loc := at.FindStringIndex(string(raw))
	if loc == nil {
		t.Fatal("micro max_rewinds not found for the test-local copy")
	}
	out := string(raw[:loc[0]]) + fmt.Sprintf("max_rewinds: %d\n", n) + string(raw[loc[1]:])
	if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}

// reviewStepCandidates is the step-level candidate list a test-local copy
// gives micro's review (reviewStepListWorkflowsDir): the model-precedence
// scenarios pin "a bundle step list outranks the project default", and no
// shipped micro or develop agent step declares a list of its own (micro's
// review names its lane's mid tier instead, a placeholder tier that never
// outranks the project default). Its names are no placeholders (a
// `<harness>:example-…` step list is undeclared), so the
// list really governs.
const reviewStepCandidates = "[pi:lane-step/model-a, pi:lane-step/model-b]"

// reviewStepListWorkflowsDir is strippedWorkflowsDir of the shipped tree
// whose micro review declares reviewStepCandidates.
func reviewStepListWorkflowsDir(t *testing.T) string {
	t.Helper()
	dir := strippedWorkflowsDir(t, filepath.Join(v1Root(t), "workflows"))
	p := filepath.Join(dir, vehicleLane, workflow.WorkflowFile)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	// the list replaces the review's tier (a step declares one of
	// model_candidates and model_tier, WFC-112)
	const at = "  - id: review\n    gate: true\n    on_fail: { target: fix }\n"
	out := strings.Replace(string(raw), at+"    model_tier: mid\n", at+"    model_candidates: "+reviewStepCandidates+"\n", 1)
	if out == string(raw) {
		t.Fatal("micro review block not found for the step-list copy")
	}
	if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// strippedWorkflowsDir copies the shipped workflows tree into a test temp
// dir with the delivery steps removed from every workflow.yaml, for daemons
// started by harnesses other than v1Install (w113/w079, v3).
func strippedWorkflowsDir(t testing.TB, shipped string) string {
	t.Helper()
	return copyWorkflowsDir(t, shipped, true)
}

// copyWorkflowsDir is strippedWorkflowsDir with the deliver step kept when
// stripDeliver is false.
func copyWorkflowsDir(t testing.TB, shipped string, stripDeliver bool) string {
	t.Helper()
	return copyWorkflowsTrees(t, func(raw []byte) []byte {
		if stripDeliver {
			raw = deliverStepBlock.ReplaceAll(raw, nil)
			raw = microFixOutputs.ReplaceAll(raw, nil)
			raw = microReviewVerifies.ReplaceAll(raw, nil)
		}
		return raw
	}, shipped)
}

// copyWorkflowsTrees copies each tree in order into one temp workflows dir
// (a later tree's file wins), passing every workflow.yaml through rewrite
// when it is set.
func copyWorkflowsTrees(t testing.TB, rewrite func([]byte) []byte, trees ...string) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "workflows")
	for _, tree := range trees {
		err := filepath.Walk(tree, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(tree, p)
			out := filepath.Join(dst, rel)
			if info.IsDir() {
				return os.MkdirAll(out, 0o755)
			}
			raw, rerr := os.ReadFile(p)
			if rerr != nil {
				return rerr
			}
			if rewrite != nil && filepath.Base(p) == workflow.WorkflowFile {
				raw = rewrite(raw)
			}
			return os.WriteFile(out, raw, info.Mode().Perm())
		})
		if err != nil {
			t.Fatalf("workflows copy of %s: %v", tree, err)
		}
	}
	return dst
}

// stripStepWallBudgets removes the step-level wall_minutes overrides from
// every workflow.yaml of an already-copied workflows tree.
func stripStepWallBudgets(t testing.TB, dir string) {
	t.Helper()
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || filepath.Base(p) != workflow.WorkflowFile {
			return err
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		return os.WriteFile(p, stepWallBudget.ReplaceAll(raw, nil), info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("strip step wall budgets: %v", err)
	}
}
