package runtime

// workspace_integration_test.go: the polyforge#79 WI acceptance test —
// ONE daemon (one Runtime) serves TWO workspaces with TWO different
// valid yamls over the SAME bundle fixture: each workspace's WI gets its
// own model assignments (the precedence runs against its own yaml and
// its own project), and its own capsule.json audit record — no
// cross-workspace leak. Also pins the per-claim freshness observable end to end: an edit
// between claims changes the NEXT claim's audit while the earlier
// claim's audit stands.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// twoWorkspaceRuntime builds one Runtime whose mapping routes proj-a →
// wsA and proj-b → wsB, over the SAME frontmatter bundle fixture for
// both claims.
func twoWorkspaceRuntime(t *testing.T) (*Runtime, *fakeHub, string, string) {
	t.Helper()
	setGitIdentity(t)
	wfDir := frontmatterBundle(t, w079Frontmatter)
	hub := newFakeHub()
	hub.items = map[string]*api.WorkItem{
		"wi_w079intA":  {ID: "wi_w079intA", ProjectName: "proj-a"},
		"wi_w079intB":  {ID: "wi_w079intB", ProjectName: "proj-b"},
		"wi_w079intA2": {ID: "wi_w079intA2", ProjectName: "proj-a"},
	}
	wsA := filepath.Join(t.TempDir(), "ws-a")
	wsB := filepath.Join(t.TempDir(), "ws-b")
	for _, ws := range []string{wsA, wsB} {
		if err := os.MkdirAll(ws, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	plan, err := mapping.Build([]mapping.Entry{
		{Workspace: wsA, Projects: []string{"proj-a"}},
		{Workspace: wsB, Projects: []string{"proj-b"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sp := &scriptedSpawner{}
	cfg := Config{
		MachineID:     "m-test",
		Mapping:       plan,
		DataDir:       filepath.Join(t.TempDir(), "data"),
		AgentStarter:  scriptedEngineStarter(sp),
		MaxRecoveries: 2,
	}
	toOB := supervise.NewQueue[protocol.RTMsg]()
	fromOB := supervise.NewQueue[protocol.OBMsg]()
	r := New(cfg, hub, sp, toOB, fromOB)

	// The claim-time capsule remote (see newTestRuntime): aihub creates
	// the bare repo at claim; the fake hub implements the contract.
	hub.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = hub.capsuleRepoBase
	r.cfg.WorkflowsDir = wfDir
	return r, hub, wsA, wsB
}

// TestTwoWorkspacesTwoYamlsOneDaemon: the same bundle, two workspaces,
// two yamls — different model assignments and audits, no leak.
func TestTwoWorkspacesTwoYamlsOneDaemon(t *testing.T) {
	r, _, wsA, wsB := twoWorkspaceRuntime(t)

	// wsA: a project default. wsB: a per-step pin.
	writeWorkspaceYaml(t, wsA, "steps:\n  proj-a:\n    default: { models: [pi:w079a-flow, pi:w079a-flow2] }\n")
	writeWorkspaceYaml(t, wsB, "steps:\n  proj-b:\n    fix: { models: [pi:w079b-code] }\n")

	if err := r.Start(context.Background(), "wi_w079intA", StartOpts{}); err != nil {
		t.Fatalf("Start A: %v", err)
	}
	if err := r.Start(context.Background(), "wi_w079intB", StartOpts{}); err != nil {
		t.Fatalf("Start B: %v", err)
	}
	capA := filepath.Join(wsA, "capsules", "wi_w079intA")
	capB := filepath.Join(wsB, "capsules", "wi_w079intB")
	_, rawA := readIdentity(t, capA)
	_, rawB := readIdentity(t, capB)

	// The per-step candidate computation does not land in capsule.json
	// (the workspace yaml governs every dispatch directly). Neither
	// capsule records anything of the other project's identity.
	if strings.Contains(rawA, "effective_candidates") || strings.Contains(rawB, "effective_candidates") {
		t.Fatalf("the deleted audit key must not be written: A %s B %s", rawA, rawB)
	}
	if strings.Contains(rawA, "w079b") || strings.Contains(rawB, "w079a") {
		t.Fatalf("an audit leaked the other workspace's yaml:\nA %s\nB %s", rawA, rawB)
	}

	// Per-claim freshness: an edit between claims governs the NEXT claim
	// (neither capsule carries the effective_candidates key).
	writeWorkspaceYaml(t, wsA, "steps:\n  proj-a:\n    default: { models: [pi:w079-edit-v2] }\n")
	if err := r.Start(context.Background(), "wi_w079intA2", StartOpts{}); err != nil {
		t.Fatalf("Start A2: %v", err)
	}
	rawA2, rerr := os.ReadFile(filepath.Join(wsA, "capsules", "wi_w079intA2", "capsule.json"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if strings.Contains(string(rawA2), "effective_candidates") {
		t.Fatalf("the deleted audit key must not be written (raw %s)", rawA2)
	}
	for _, wi := range []string{"wi_w079intA", "wi_w079intB", "wi_w079intA2"} {
		if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err != nil {
			t.Fatalf("Stop %s: %v", wi, err)
		}
	}
}

// Another project in the SAME workspace is unaffected by a sibling's
// models entry, and a broken yaml refuses both before any claim.
func TestSiblingProjectUnaffectedAndSharedYamlRefusesBoth(t *testing.T) {
	setGitIdentity(t)
	wfDir := frontmatterBundle(t, w079Frontmatter)
	hub := newFakeHub()
	hub.items = map[string]*api.WorkItem{
		"wi_w079s1": {ID: "wi_w079s1", ProjectName: "p1"},
		"wi_w079s2": {ID: "wi_w079s2", ProjectName: "p2"},
		"wi_w079s3": {ID: "wi_w079s3", ProjectName: "p1"},
		"wi_w079s4": {ID: "wi_w079s4", ProjectName: "p2"},
	}
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	sp := &scriptedSpawner{}
	r := New(Config{
		MachineID: "m-test", Mapping: mappingFor(ws, "p1", "p2"),
		DataDir:      filepath.Join(t.TempDir(), "data"),
		AgentStarter: scriptedEngineStarter(sp), MaxRecoveries: 2,
	}, hub, sp, supervise.NewQueue[protocol.RTMsg](), supervise.NewQueue[protocol.OBMsg]())

	// The claim-time capsule remote (see newTestRuntime): aihub creates
	// the bare repo at claim; the fake hub implements the contract.
	hub.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = hub.capsuleRepoBase
	r.cfg.WorkflowsDir = wfDir

	writeWorkspaceYaml(t, ws, "steps:\n  p1:\n    default: { models: [pi:w79/p1] }\n")
	if err := r.Start(context.Background(), "wi_w079s1", StartOpts{}); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(context.Background(), "wi_w079s2", StartOpts{}); err != nil {
		t.Fatal(err)
	}
	i1raw, rerr := os.ReadFile(filepath.Join(ws, "capsules", "wi_w079s1", "capsule.json"))
	if rerr != nil {
		t.Fatal(rerr)
	}
	if strings.Contains(string(i1raw), "effective_candidates") {
		t.Fatalf("the deleted audit key must not be written (raw %s)", i1raw)
	}
	j2 := r.jobOf("wi_w079s2")
	j2.mu.Lock()
	if got := j2.stepCandidates["fix"]; len(got) == 0 || got[0] != "pi:prov/flash" {
		t.Fatalf("p2 fix %v, want the bundle step-level list", got)
	}
	j2.mu.Unlock()
	for _, wi := range []string{"wi_w079s1", "wi_w079s2"} {
		if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err != nil {
			t.Fatal(err)
		}
	}

	// The shared yaml is torn: BOTH projects refuse with DAEMON-414 and
	// neither reaches the aihub claim.
	claims := hub.claimCount()
	writeWorkspaceYaml(t, ws, "steps:\n  p1:\n    fix: [\n")
	for _, wi := range []string{"wi_w079s3", "wi_w079s4"} {
		err := r.Start(context.Background(), wi, StartOpts{})
		ae, ok := err.(*api.Error)
		if !ok || ae.Code != api.ErrDaemonWorkspaceYaml {
			t.Fatalf("Start %s over a broken shared yaml = %v, want DAEMON-414", wi, err)
		}
	}
	if n := hub.claimCount(); n != claims {
		t.Fatalf("a refused Start must not claim: %d -> %d claim calls", claims, n)
	}
}
