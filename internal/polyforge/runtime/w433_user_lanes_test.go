package runtime

// w433_user_lanes_test.go (workflow contract "User lanes"):
// one daemon serving two workspaces resolves each work item's lane in its
// own workspace's lane set — the built-in lanes plus that workspace's user
// lanes — so two user lanes of the same name in two workspaces each run
// their own; the claim snapshots the resolved lane with its source and
// content hash, and a later edit of the workspace's lane file reaches no
// work item in flight (neither its snapshot nor its dispatch-time
// candidates). A workspace without the lane refuses the claim (WFC-123);
// a user lane named like a built-in one never shadows it (WFC-122).

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// w433Lane is a user lane `demo-bundle` whose one step is id, with its
// own candidates.
func w433Lane(t *testing.T, ws, id, candidate string) string {
	t.Helper()
	dir := filepath.Join(workflow.UserLanesRoot(ws), "demo-bundle")
	front := workflowtest.Header("demo-bundle") + "steps:\n  - { id: " + id + ", model_candidates: [" + candidate + "] }\n"
	for rel, body := range map[string]string{workflow.WorkflowFile: front, "steps/" + id + ".md": workflowtest.Definition(id) + "{{principles}}\n"} {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// w433Runtime is a runtime whose mapping serves three workspaces (pa →
// wsA, pb → wsB, pc → wsC) over a built-in lanes root holding only the
// shared tree and an unrelated lane.
func w433Runtime(t *testing.T) (*Runtime, *fakeHub, map[string]string) {
	t.Helper()
	setGitIdentity(t)
	builtin := t.TempDir()
	if err := os.MkdirAll(filepath.Join(builtin, workflow.SharedDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(builtin, workflow.SharedDir, "principles.md"), []byte("Built-in principles.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	workflowtest.WriteLane(t, builtin, "other-lane", "steps:\n  - { id: fix }\n")
	wss := map[string]string{}
	var entries []mapping.Entry
	for _, p := range []string{"pa", "pb", "pc"} {
		ws := filepath.Join(t.TempDir(), "ws-"+p)
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
		wss[p] = ws
		entries = append(entries, mapping.Entry{Workspace: ws, Projects: []string{p}})
	}
	plan, err := mapping.Build(entries)
	if err != nil {
		t.Fatal(err)
	}
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r := New(Config{MachineID: "m-test", Mapping: plan, DataDir: filepath.Join(t.TempDir(), "data"), AgentStarter: scriptedEngineStarter(sp), MaxRecoveries: 2},
		hub, sp, supervise.NewQueue[protocol.RTMsg](), supervise.NewQueue[protocol.OBMsg]())
	t.Cleanup(func() { r.Close(); settleOps(t, r, 10e9) })
	hub.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = hub.capsuleRepoBase
	r.cfg.WorkflowsDir = builtin
	hub.items = map[string]*api.WorkItem{}
	return r, hub, wss
}

// bind files wi on project bound to the lane demo-bundle.
func (h *fakeHub) bind(wi, project string) {
	name := "demo-bundle"
	h.mu.Lock()
	defer h.mu.Unlock()
	h.items[wi] = &api.WorkItem{ID: wi, ProjectName: project, BundleName: &name}
}

func w433Start(t *testing.T, r *Runtime, wi, project string) string {
	t.Helper()
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start %s: %v", wi, err)
	}
	t.Cleanup(func() { _ = r.Stop(context.Background(), wi, protocol.FinishWrapped) })
	ws, err := r.plan().WorkspaceFor(project)
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Join(ws, "capsules", wi)
}

func TestW433EachWorkspaceRunsItsOwnUserLane(t *testing.T) {
	r, hub, wss := w433Runtime(t)
	dirA := w433Lane(t, wss["pa"], "alpha", "pi:prov/a")
	w433Lane(t, wss["pb"], "beta", "pi:prov/b")
	hub.bind("wi_w433a", "pa")
	hub.bind("wi_w433b", "pb")
	capA := w433Start(t, r, "wi_w433a", "pa")
	capB := w433Start(t, r, "wi_w433b", "pb")

	for _, c := range []struct{ capDir, step, other string }{{capA, "alpha", "beta"}, {capB, "beta", "alpha"}} {
		lane, err := workflow.LoadStrict(filepath.Join(c.capDir, capsule.DirWorkflows, "demo-bundle"))
		if err != nil {
			t.Fatalf("the snapshot of %s must load: %v", c.capDir, err)
		}
		if lane.Step(c.step) == nil || lane.Step(c.other) != nil {
			t.Fatalf("%s runs the wrong workspace's lane: steps %+v", c.capDir, lane.Steps)
		}
		// the built-in shared tree is snapshotted beside the user lane
		if _, err := os.Stat(filepath.Join(c.capDir, capsule.DirWorkflows, workflow.SharedDir, "principles.md")); err != nil {
			t.Fatalf("the built-in shared tree is not in the snapshot: %v", err)
		}
		m := readMarker(t, c.capDir)
		hash, err := workflow.ContentHash(c.capDir, m.Targets)
		if err != nil {
			t.Fatal(err)
		}
		if m.Source != workflow.SourceWorkspace || m.ContentHash != hash || m.Contract != workflow.ContractVersion {
			t.Fatalf("marker of %s = source %q hash %q contract %d, want workspace %q %d", c.capDir, m.Source, m.ContentHash, m.Contract, hash, workflow.ContractVersion)
		}
	}

	// the owner edits wsA's lane after the claim: the work item in flight
	// keeps its snapshot and its candidates
	j := r.jobOf("wi_w433a")
	before := liveCands(t, r, j)
	if !reflect.DeepEqual(before["alpha"], []string{"pi:prov/a"}) {
		t.Fatalf("alpha's candidates = %v", before["alpha"])
	}
	markerBefore := readMarker(t, capA)
	edited := strings.Replace(mustRead(t, filepath.Join(dirA, workflow.WorkflowFile)), "pi:prov/a", "pi:prov/edited", 1)
	if err := os.WriteFile(filepath.Join(dirA, workflow.WorkflowFile), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := liveCands(t, r, j); !reflect.DeepEqual(got, before) {
		t.Fatalf("an edit of the workspace lane reached the work item in flight: %v, want %v", got, before)
	}
	if strings.Contains(mustRead(t, filepath.Join(capA, capsule.DirWorkflows, "demo-bundle", workflow.WorkflowFile)), "pi:prov/edited") {
		t.Fatal("the edit reached the capsule's snapshot")
	}
	if hash, err := workflow.ContentHash(capA, markerBefore.Targets); err != nil || hash != markerBefore.ContentHash {
		t.Fatalf("the snapshot's content changed under its marker: %s vs %s (%v)", hash, markerBefore.ContentHash, err)
	}
	// a new work item of the workspace claims the edited lane
	hub.bind("wi_w433a2", "pa")
	capA2 := w433Start(t, r, "wi_w433a2", "pa")
	if !strings.Contains(mustRead(t, filepath.Join(capA2, capsule.DirWorkflows, "demo-bundle", workflow.WorkflowFile)), "pi:prov/edited") {
		t.Fatal("the next claim must snapshot the edited lane")
	}
	if readMarker(t, capA2).ContentHash == markerBefore.ContentHash {
		t.Fatal("two different lane snapshots hash alike")
	}
}

// WFC-123: a workspace whose lane set lacks the work item's lane refuses
// the claim — nothing is claimed — though another workspace defines it.
func TestW433WorkspaceWithoutTheLaneRefusesTheClaim(t *testing.T) {
	r, hub, wss := w433Runtime(t)
	w433Lane(t, wss["pa"], "alpha", "pi:prov/a")
	hub.bind("wi_w433c", "pc")
	err := r.Start(context.Background(), "wi_w433c", StartOpts{})
	ae, ok := err.(*api.Error)
	if !ok || ae.Code != api.ErrDaemonWorkspaceYaml || !strings.Contains(ae.Detail, "WFC-123 the work item's lane demo-bundle is not in this daemon's lane set") || !strings.Contains(ae.Detail, workflow.UserLanesRoot(wss["pc"])) {
		t.Fatalf("a claim on a lane its workspace lacks must refuse with WFC-123 naming the user lanes: %v", err)
	}
	if n := hub.claimCount(); n != 0 {
		t.Fatalf("refused after %d claim call(s)", n)
	}
	if bad := r.RefusedBundles([][2]string{{"pc", "demo-bundle"}, {"pa", "demo-bundle"}}); len(bad) != 1 || bad["pc demo-bundle"] == nil {
		t.Fatalf("the auto-pull skip must name only pc's work items: %v", bad)
	}
}

// WFC-122: a user lane named like a built-in lane never shadows it — the
// work item runs the built-in lane.
func TestW433UserLaneNeverShadowsABuiltin(t *testing.T) {
	r, hub, wss := w433Runtime(t)
	workflowtest.WriteLane(t, r.cfg.WorkflowsDir, "demo-bundle", "steps:\n  - { id: builtin-step }\n")
	w433Lane(t, wss["pa"], "alpha", "pi:prov/a")
	hub.bind("wi_w433s", "pa")
	capS := w433Start(t, r, "wi_w433s", "pa")
	lane, err := workflow.LoadStrict(filepath.Join(capS, capsule.DirWorkflows, "demo-bundle"))
	if err != nil || lane.Step("builtin-step") == nil || lane.Step("alpha") != nil {
		t.Fatalf("the built-in lane must run: %+v %v", lane, err)
	}
	if m := readMarker(t, capS); m.Source != workflow.SourceBuiltin {
		t.Fatalf("marker source %q, want builtin", m.Source)
	}
}

func mustRead(t *testing.T, p string) string {
	t.Helper()
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
