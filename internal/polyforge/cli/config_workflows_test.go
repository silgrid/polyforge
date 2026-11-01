package cli

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// wfFixture is one workspace-face fixture: a cli.toml mapping ws to the
// projects alpha and beta, an installed workflows dir (two bundles sharing
// the grill and code step ids, the shared dir beside them) and a fake
// daemon whose /status reports that dir and the given capabilities.
type wfFixture struct {
	env          map[string]string
	ws           string
	workflowsDir string
}

func newWfFixture(t *testing.T, yaml string, caps []string) wfFixture {
	t.Helper()
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	wf := filepath.Join(dir, "workflows")
	xdg := filepath.Join(dir, "xdg")
	for _, d := range []string{ws, filepath.Join(xdg, "polyforge"), filepath.Join(wf, "_shared")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cfg := "server = \"http://wf.invalid:1\"\n\n[workspace.\"" + ws + "\"]\nprojects = [\"alpha\", \"beta\"]\n"
	if err := os.WriteFile(filepath.Join(xdg, "polyforge", "cli.toml"), []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	bundles := map[string]string{
		"feature-full": "name: feature-full\ndefaults:\n  model_candidates: [pi:ff-default]\nsteps:\n  - id: grill\n    model_candidates: [pi:ff-grill]\n    attrs:\n      question_batch_size: { type: int, default: 0 }\n  - id: spec\n  - id: plan\n    model_candidates: [pi:ff-plan]\n  - id: code\n    model_candidates: [pi:ff-code]\n",
		"autopilot":    "name: autopilot\nsteps:\n  - id: grill\n    model_candidates: [pi:ap-grill]\n    attrs:\n      question_batch_size: { type: int, default: 0 }\n  - id: code\n    model_candidates: [pi:ap-code]\n",
	}
	for name, front := range bundles {
		if err := os.MkdirAll(filepath.Join(wf, name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(wf, name, "workflow.md"), []byte("---\n"+front+"---\n# fixture\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if yaml != "" {
		if err := os.WriteFile(filepath.Join(ws, ".polyforge.yaml"), []byte(yaml), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	sock := newFakeDaemon(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		st := map[string]any{"machine_id": "m", "workflows_dir": wf, "jobs": []any{}}
		if caps != nil {
			st["capabilities"] = caps
		}
		_, _ = w.Write([]byte(jsonBody(st)))
	}))
	return wfFixture{env: map[string]string{"XDG_CONFIG_HOME": xdg, "PF_DAEMON_SOCKET": sock}, ws: ws, workflowsDir: wf}
}

func decodeWorkflows(t *testing.T, stdout string) workflowsDoc {
	t.Helper()
	var doc workflowsDoc
	if err := json.Unmarshal([]byte(stdout), &doc); err != nil {
		t.Fatalf("pf workflows did not print one JSON document: %v\n%s", err, stdout)
	}
	return doc
}

func findBundle(doc workflowsDoc, name string) *workflowBundle {
	for i := range doc.Bundles {
		if doc.Bundles[i].Name == name {
			return &doc.Bundles[i]
		}
	}
	return nil
}

// TestWorkflowsReadListsBundlesAndSteps pins the read face: every bundle
// (the shared dir never), its steps in declared order with their
// candidates and declared attrs, and — with --cwd — each mapped project's
// effective chain and its source level.
func TestWorkflowsReadListsBundlesAndSteps(t *testing.T) {
	f := newWfFixture(t, "steps:\n  alpha:\n    grill: { models: [claude:declared] }\n    default: { models: [pi:ws-default] }\n", []string{CapabilityStepsAttrs})
	code, stdout, stderr := runPf(t, f.env, "--json", "workflows", "--cwd", f.ws)
	if code != 0 {
		t.Fatalf("pf workflows exited %d: %s", code, stderr)
	}
	doc := decodeWorkflows(t, stdout)
	if !doc.DaemonReachable || doc.WorkflowsDir != f.workflowsDir || !doc.WriteCapable || doc.Reason != "" {
		t.Fatalf("daemon facts wrong: %+v", doc)
	}
	var names []string
	for _, b := range doc.Bundles {
		names = append(names, b.Name)
	}
	if !reflect.DeepEqual(names, []string{"autopilot", "feature-full"}) {
		t.Fatalf("bundles %v, want [autopilot feature-full] (no _shared)", names)
	}
	ff := findBundle(doc, "feature-full")
	var ids []string
	for _, s := range ff.Steps {
		ids = append(ids, s.ID)
	}
	if !reflect.DeepEqual(ids, []string{"grill", "spec", "plan", "code"}) {
		t.Fatalf("feature-full steps %v, want the declared order", ids)
	}
	grill := ff.Steps[0]
	if !reflect.DeepEqual(grill.ModelCandidates, []string{"pi:ff-grill"}) || len(grill.Attrs) != 1 || grill.Attrs[0].Name != "question_batch_size" || grill.Attrs[0].Type != "int" {
		t.Fatalf("grill facts wrong: %+v", grill)
	}
	if ff.Steps[1].ModelCandidates != nil {
		t.Fatalf("spec declares no candidates: %+v", ff.Steps[1])
	}
	if !reflect.DeepEqual(doc.Projects, []string{"alpha", "beta"}) || doc.Workspace == nil || *doc.Workspace != f.ws {
		t.Fatalf("workspace/projects wrong: %v %v", doc.Workspace, doc.Projects)
	}
	want := map[string]effectiveChain{
		"alpha": {Models: []string{"claude:declared"}, From: "steps.alpha.grill.models"},
		"beta":  {Models: []string{"pi:ff-grill"}, From: "bundle feature-full step grill model_candidates"},
	}
	if !reflect.DeepEqual(grill.Effective, want) {
		t.Fatalf("grill effective %+v, want %+v", grill.Effective, want)
	}
	spec := ff.Steps[1].Effective
	if spec["alpha"].From != "steps.alpha.default.models" || spec["beta"].From != "bundle feature-full defaults.model_candidates" {
		t.Fatalf("spec effective falls back wrong: %+v", spec)
	}
	if ff.Defaults.Effective["alpha"].From != "steps.alpha.default.models" || ff.Defaults.Effective["beta"].From != "bundle feature-full defaults.model_candidates" {
		t.Fatalf("defaults effective wrong: %+v", ff.Defaults.Effective)
	}
	ap := findBundle(doc, "autopilot")
	if _, ok := ap.Defaults.Effective["beta"]; ok {
		t.Fatalf("no level governs autopilot's beta default: %+v", ap.Defaults.Effective)
	}
	if doc.Task["alpha"].From != "steps.alpha.default.models" {
		t.Fatalf("task effective %+v, want the project default", doc.Task)
	}
	if _, ok := doc.Task["beta"]; ok {
		t.Fatalf("beta declares no task/default list: %+v", doc.Task)
	}
}

// TestWorkflowsReadDegradesOneBrokenBundle pins that one unparseable
// bundle is reported in bundle_errors while the others render, exit 0.
func TestWorkflowsReadDegradesOneBrokenBundle(t *testing.T) {
	f := newWfFixture(t, "", []string{CapabilityStepsAttrs})
	if err := os.MkdirAll(filepath.Join(f.workflowsDir, "broken"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.workflowsDir, "broken", "workflow.md"), []byte("---\nsteps: [\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runPf(t, f.env, "workflows")
	if code != 0 {
		t.Fatalf("a broken bundle failed the verb: %d %s", code, stderr)
	}
	doc := decodeWorkflows(t, stdout)
	if len(doc.BundleErrors) != 1 || doc.BundleErrors[0].Bundle != "broken" || doc.BundleErrors[0].Reason == "" {
		t.Fatalf("bundle_errors %+v, want broken with a reason", doc.BundleErrors)
	}
	if len(doc.Bundles) != 2 {
		t.Fatalf("the healthy bundles must still render: %+v", doc.Bundles)
	}
}

// TestWorkflowsReadDaemonUnreachable pins the unreachable degradation:
// exit 0, daemon_reachable false, a reason, no invented bundle.
func TestWorkflowsReadDaemonUnreachable(t *testing.T) {
	f := newWfFixture(t, "", nil)
	f.env["PF_DAEMON_SOCKET"] = filepath.Join(t.TempDir(), "none.sock")
	code, stdout, stderr := runPf(t, f.env, "workflows", "--cwd", f.ws)
	if code != 0 {
		t.Fatalf("an unreachable daemon failed the verb: %d %s", code, stderr)
	}
	doc := decodeWorkflows(t, stdout)
	if doc.DaemonReachable || doc.Reason == "" || len(doc.Bundles) != 0 || len(doc.BundleErrors) != 0 || doc.WriteCapable {
		t.Fatalf("unreachable doc wrong: %+v", doc)
	}
	if !strings.Contains(stdout, `"bundles": []`) {
		t.Fatalf("bundles must be an empty list, not null: %s", stdout)
	}
}

// TestWorkflowsReadReportsCapability pins the write gate fact: a daemon
// without the steps_attrs capability makes write_capable false with the
// floor named; an invalid yaml is the reason, never a failure.
func TestWorkflowsReadReportsCapability(t *testing.T) {
	f := newWfFixture(t, "steps:\n  alpha:\n    grill: { attrs: { mystery_knob: 1 } }\n", nil)
	code, stdout, stderr := runPf(t, f.env, "workflows", "--cwd", f.ws)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	doc := decodeWorkflows(t, stdout)
	if doc.WriteCapable || !strings.Contains(doc.WriteReason, StepsAttrsMinimumVersion) {
		t.Fatalf("write gate wrong: %+v", doc)
	}
	if !strings.Contains(doc.Reason, "undeclared attr") || len(doc.Bundles) != 2 {
		t.Fatalf("an invalid yaml must keep the bundles and name the refusal: %+v", doc)
	}
}
