package driver

// v2_envelope_test.go: the contract-2 step keys contract 4 keeps, as the
// driver reads them: a step's declared resources taken for its dispatch,
// and `waivable` defaulting to false. (The envelope's effects, hand-off
// field and prompt sections left with workflow contract 4's stage C3.)

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// v2Lane writes a lane with a skill, resources and a waivable gate (the
// contract-2 keys contract 4 keeps), and loads it.
func v2Lane(t *testing.T) (string, *Workflow) {
	t.Helper()
	root := t.TempDir()
	write := func(rel, s string) {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(workflow.SharedDir, "skills", "tidy.md"), "---\nname: tidy\ndescription: tidy\ncommands: [pf-run]\n---\n\n1. Run `pf-run --json --out artifacts/run/result.json` as written.\n2. Exit 0: completed.\n")
	write(filepath.Join("other", workflow.WorkflowFile), workflowtest.Header("other")+"steps:\n  - id: work\n")
	workflowtest.FillMissing(t, filepath.Join(root, "other"))
	dir := workflowtest.WriteLaneUnchecked(t, root, "lane", `steps:
  - id: first
    skill: tidy
    resources: { admission: heavy }
  - id: second
    gate: true
    waivable: true
`)
	workflowtest.FillDefinitions(t, dir)
	wf, err := LoadWorkflow(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, wf
}

// v2Env is testEnv over the v2 lane.
func v2Env(t *testing.T) *Driver {
	t.Helper()
	_, wf := v2Lane(t)
	d, _, _, _ := testEnv(t, wf.Steps...)
	d.workflow = wf
	return d
}

// A step's declared resources are taken through the daemon's admission
// for its dispatch: `admission: heavy` enters the queue; a step declaring
// none holds nothing.
func TestV2ResourcesAreTakenForTheDispatch(t *testing.T) {
	d := v2Env(t)
	adm := &v2Admission{}
	d.cfg.Admission = adm
	release, err := d.acquireStepResources(context.Background(), "first")
	if err != nil {
		t.Fatal(err)
	}
	if len(adm.entered) != 1 || adm.entered[0].Step != "first" {
		t.Fatalf("the heavy admission is asked for the dispatch: %+v", adm.entered)
	}
	release()
	if adm.released != 1 {
		t.Fatalf("the release gives the slot back: %d", adm.released)
	}
	if _, err := d.acquireStepResources(context.Background(), "second"); err != nil || len(adm.entered) != 1 {
		t.Fatalf("a step declaring none takes nothing: %v %+v", err, adm.entered)
	}
}

// v2Admission grants at once and counts.
type v2Admission struct {
	entered  []AdmissionRequest
	released int
}

func (a *v2Admission) Enter(req AdmissionRequest) AdmissionTicket {
	a.entered = append(a.entered, req)
	return &v2Ticket{a: a}
}

type v2Ticket struct{ a *v2Admission }

func (t *v2Ticket) Ready() <-chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}
func (t *v2Ticket) State() (int, string) { return 0, "" }
func (t *v2Ticket) Release()             { t.a.released++ }

// waivable defaults to false on an agent gate: only a declared
// `waivable: true` takes a waiver.
func TestV2WaivableDefaultsToFalse(t *testing.T) {
	_, wf := v2Lane(t)
	if !wf.Waivable("second") {
		t.Fatal("a gate declaring waivable: true is waivable")
	}
	d, _, _, _ := testEnv(t, spec("build"), gateSpec("check", "build"))
	if d.workflow.Waivable("check") || d.workflow.Waivable("build") {
		t.Fatal("an agent gate without the key is not waivable (contract 2 default false); a non-gate never")
	}
}
