package driver

// workflow_test.go: the driver's view of a lane — LoadWorkflow reads it
// through the one lane loader (workflow.Load) and maps it onto the
// execution graph; a lane the contract refuses refuses the driver with
// every WFC-* defect.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// TestLoadWorkflowMapsTheLane: every key the contract accepts reaches the
// execution graph as declared.
func TestLoadWorkflowMapsTheLane(t *testing.T) {
	dir := workflowtest.WriteLane(t, t.TempDir(), "probe", `max_rewinds: 3
steps:
  - id: build
    budget: { wall_minutes: 45 }
    hub: { verbs: [get, list], attrs: [integration] }
  - id: review
    gate: true
    on_fail: { target: build }
  - id: merge
    gate: true
    on_fail: { target: build }
    resources: { locks: [merge] }
    budget: { tool_minutes: 10 }
    wrap_attrs: [integration]
`)
	wf, err := LoadWorkflow(dir)
	if err != nil {
		t.Fatalf("LoadWorkflow: %v", err)
	}
	if wf.MaxRewinds != 3 || len(wf.Steps) != 3 {
		t.Fatalf("workflow = %+v", wf)
	}
	b, r, m := wf.step("build"), wf.step("review"), wf.step("merge")
	if b.WallMinutes != 45 || b.Gate || b.OnFail != nil || strings.Join(wf.HubAllowed("build"), ",") != "get,list,attrs" || strings.Join(wf.HubAttrs("build"), ",") != "integration" {
		t.Errorf("build = %+v", b)
	}
	if !r.Gate || r.OnFail == nil || r.OnFail.Target != "build" || wf.HubAllowed("review") != nil || wf.HubAttrs("review") != nil {
		t.Errorf("review = %+v", r)
	}
	if !m.Gate || m.OnFail.Target != "build" || m.Resources == nil || !m.Resources.Holds(workflow.LockMerge) || m.ToolMinutes != 10 ||
		strings.Join(wf.WrapAttrs("merge"), ",") != "integration" || strings.Join(wf.HubAllowed("merge"), ",") != "wrap-attrs" {
		t.Errorf("merge = %+v", m)
	}
}

// TestLoadWorkflowRefusesWithEveryDefect: a lane the contract refuses is
// refused with a *workflow.LoadError naming every defect, and New
// surfaces it instead of degrading.
func TestLoadWorkflowRefusesWithEveryDefect(t *testing.T) {
	capDir := t.TempDir()
	bundle := filepath.Join(capDir, "workflows", "b")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	md := fmt.Sprintf("contract: %d", workflow.ContractVersion) + "\nname: b\ndescription: refused\nsteps:\n  - { id: g, gate: true, context_policy: inherit }\n  - { id: h, handoff: true }\n"
	if err := os.WriteFile(filepath.Join(bundle, workflow.WorkflowFile), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadWorkflow(bundle)
	var le *workflow.LoadError
	if !errors.As(err, &le) || !le.HasCode(workflow.CodeUnknownKey) || !le.HasCode(workflow.CodeDefinitionMissing) || len(le.Errors) != 4 {
		t.Fatalf("LoadWorkflow = %v, want the two removed keys and the two missing definitions", err)
	}
	_, err = New(Config{WI: WIInfo{ID: "wi", CapsuleDir: capDir, BundleName: "b"}, Spawner: &fakeSpawner{}})
	if !errors.As(err, &le) || !strings.Contains(err.Error(), "context_policy") || !strings.Contains(err.Error(), "handoff") {
		t.Fatalf("New must surface the lane's load error, got %v", err)
	}
	if _, err := LoadWorkflow(filepath.Join(capDir, "absent")); err == nil {
		t.Fatal("a missing workflow.yaml must be an error")
	}
}

// TestShippedBundlesValidate: every shipped workflow bundle passes the
// load-time validation.
func TestShippedBundlesValidate(t *testing.T) {
	for _, dir := range shippedBundleDirs(t) {
		if _, err := LoadWorkflow(dir); err != nil {
			t.Errorf("%s: %v", filepath.Base(dir), err)
		}
	}
}

// TestNewRefusesExecutionWithRemovedSteps (polyforge#165 rollout): an
// in-flight feature-full WI's execution.json carries an entry for EVERY
// step of the bundle it was claimed on (the skeleton seeds them all),
// including e2e-baseline and e2e-run. Same-daemon recovery relaunches
// from the capsule's own materialized snapshot (the old bundle) and is
// unaffected; a re-claim materializes the daemon's current checkout,
// where those steps no longer exist. The driver must refuse that start
// loudly, naming every removed step (the claim then lands the WI
// blocked with this reason) — never crash or continue on a mismatched
// graph.
func TestNewRefusesExecutionWithRemovedSteps(t *testing.T) {
	capDir := t.TempDir()
	execJSON := `{"work_item_id":"wi_old","workflow_status":"blocked","current_step":"build","steps":{` +
		`"e2e-baseline":{"status":"done"},"build":{"status":"in_progress"},` +
		`"e2e-run":{"status":"pending"},"review":{"status":"pending"},"sync":{"status":"pending"},"verification":{"status":"pending"},` +
		`"publish":{"status":"pending"},"merge":{"status":"pending"}}}`
	execPath := filepath.Join(capDir, "execution.json")
	if err := os.WriteFile(execPath, []byte(execJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := New(Config{
		WI:        WIInfo{ID: "wi_old", CapsuleDir: capDir, BundleName: "develop"},
		ExecPath:  execPath,
		BundleDir: filepath.Join(shippedWorkflowsDir, "develop"),
		Spawner:   &fakeSpawner{},
		Log:       &eventLog{},
	})
	if err == nil {
		t.Fatal("an execution.json naming removed steps must refuse the driver start")
	}
	for _, want := range []string{`"e2e-baseline", "e2e-run"`, `"develop"`, "cancel it and re-file"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("refusal must name %s: %v", want, err)
		}
	}
}

// extExample is the probe lane the spawn tests run: an authoring step, a
// second step and a gate rewinding to it.
var extExample = `
contract: ` + strconv.Itoa(workflow.ContractVersion) + `
name: autopilot-x
description: probe lane of the spawn tests
max_rewinds: 3
defaults:
  model_candidates: [pi:prov/a, pi:prov/b]
steps:
  - id: grill
  - id: fix
  - id: verification
    gate: true
    on_fail: { target: fix }
`

// writeExtLane writes the extExample lane into capDir's workflows: its
// workflow.yaml, a definition per step (the body of each from bodies, the
// step id when absent) and the shared step header (writeStepHeader).
func writeExtLane(t *testing.T, capDir string, bodies map[string]string) {
	t.Helper()
	writeCapsuleFile(t, capDir, "workflows/autopilot-x/"+workflow.WorkflowFile, strings.TrimPrefix(extExample, "\n"))
	writeStepHeader(t, filepath.Join(capDir, "workflows"))
	for _, id := range []string{"grill", "fix", "verification"} {
		body, ok := bodies[id]
		if !ok {
			body = id
		}
		writeCapsuleFile(t, capDir, "workflows/autopilot-x/steps/"+id+".md", extDef(id, body))
	}
}

// extDef answers a definition file for id carrying body.
func extDef(id, body string) string {
	return "---\nname: " + id + "\ndescription: probe step " + id + "\n---\n\n" + body
}

// The probe step header and hand-off template (workflow contract 4: part
// 3 of every prompt, _shared/step-header.md then _shared/handoff.md).
const (
	probeStepHeader  = "# How a step runs (probe)\n\nRead artifacts/handoff.md first; end with one `pf step handoff`."
	probeHandoffBody = "# The hand-off body (probe)\n\n### 结论\n### 这一步要回答什么\n### 做了什么\n### 关键决定和理由\n### 验证\n### 发现的问题和风险\n### 给下游与未决"
)

// writeStepHeader writes the probe step header and hand-off template
// into the shared tree beside the lanes under workflowsDir.
func writeStepHeader(t *testing.T, workflowsDir string) {
	t.Helper()
	for name, body := range map[string]string{"step-header": probeStepHeader, "handoff": probeHandoffBody} {
		p := filepath.Join(workflowsDir, workflow.SharedDir, name+".md")
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}
