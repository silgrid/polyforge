package test

// v2_workflows_dir_test.go: the workflows directory a scenario's daemon
// runs. The shipped bundles declare `requires_human_answer: true` on
// their human-confirmation steps (the driver's human-gate guarantee,
// polyforge#138), which an unattended scripted plan (completed grill with
// no question) can never satisfy. Scenarios that are not about the gate
// therefore run a copy with that one frontmatter flag stripped (both its
// `true` and its `if_asked` form); scenarios that are
// (TestV2HumanGateGuaranteeEndToEnd) set inst.gateAutopilotGrill. The
// step definitions' `output:` schemas stay: the driver validates in
// record-only mode (polyforge#164 H3), so the fake harness's placeholder
// outputs are recorded as output_schema_violation events, never refused.
// Likewise the delivery bundles end with the `deliver` step (polyforge#166),
// which pushes to the task repos' origins: scenarios that are not about
// delivery run without that step line; the delivery scenarios set
// inst.keepDeliver.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

var humanGateFlag = regexp.MustCompile(`,\s*requires_human_answer:\s*(true|if_asked)`)

// deliverStepLine matches the deliver step's frontmatter line.
var deliverStepLine = regexp.MustCompile(`(?m)^\s*- \{ id: deliver,.*\}\n`)

// stepWallBudget matches one step-level `budget: { wall_minutes: N }`
// override (the flow-map holds ONLY wall_minutes).
var stepWallBudget = regexp.MustCompile(`,\s*budget:\s*\{\s*wall_minutes:\s*\d+\s*\}`)

// workflowsPath answers the --workflows-dir the daemon should use.
func (u *v1Install) workflowsPath() string {
	shipped := filepath.Join(u.root, "workflows")
	if u.strippedWF == "" {
		u.strippedWF = copyWorkflowsDir(u.t, shipped, !u.keepDeliver)
		if u.gateAutopilotGrill {
			addGrillHumanGate(u.t, u.strippedWF)
		}
		if u.stripWallBudgets {
			stripStepWallBudgets(u.t, u.strippedWF)
		}
	}
	return u.strippedWF
}

// addGrillHumanGate declares requires_human_answer on the autopilot grill
// of an already-copied workflows tree (the flag the shipped bundle no
// longer carries there), for the human-gate end-to-end scenario.
func addGrillHumanGate(t testing.TB, dir string) {
	t.Helper()
	p := filepath.Join(dir, "autopilot", "workflow.md")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	out := strings.Replace(string(raw), "{ id: grill, context_policy: fresh, handoff: true", "{ id: grill, context_policy: fresh, handoff: true, requires_human_answer: true", 1)
	if out == string(raw) {
		t.Fatal("autopilot grill line not found for the human-gate copy")
	}
	if err := os.WriteFile(p, []byte(out), 0o644); err != nil {
		t.Fatal(err)
	}
}

// strippedWorkflowsDir copies the shipped workflows tree into a test temp
// dir with the requires_human_answer flag and the deliver step removed from
// every workflow.md, for daemons started by harnesses other than v1Install
// (w113/w079, v3).
func strippedWorkflowsDir(t testing.TB, shipped string) string {
	t.Helper()
	return copyWorkflowsDir(t, shipped, true)
}

// copyWorkflowsDir is strippedWorkflowsDir with the deliver step kept when
// stripDeliver is false.
func copyWorkflowsDir(t testing.TB, shipped string, stripDeliver bool) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "workflows")
	err := filepath.Walk(shipped, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(shipped, p)
		out := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(out, 0o755)
		}
		raw, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if strings.HasSuffix(p, "workflow.md") {
			raw = humanGateFlag.ReplaceAll(raw, nil)
			if stripDeliver {
				raw = deliverStepLine.ReplaceAll(raw, nil)
			}
		}
		return os.WriteFile(out, raw, info.Mode().Perm())
	})
	if err != nil {
		t.Fatalf("stripped workflows copy: %v", err)
	}
	return dst
}

// stripStepWallBudgets removes the step-level wall_minutes overrides from
// every workflow.md of an already-copied workflows tree.
func stripStepWallBudgets(t testing.TB, dir string) {
	t.Helper()
	err := filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(p, "workflow.md") {
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
