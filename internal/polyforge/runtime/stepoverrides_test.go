package runtime

// stepoverrides_test.go (u4, polyforge#25): the daemon-local pending
// overlay record — the changes recorded while the work item was held by
// no job (its blocked capsule sealed read-only, abandoned by the next
// claim's fresh clone) merge into the fresh capsule's overlay file, per
// aspect, when the claim materializes the scene, and the record is
// consumed; a terminal close clears an unconsumed record, a blocked
// close keeps it.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// pendingEnv builds a runtime over the demo bundle (workspace_compose_test
// harness) and answers its data dir.
func pendingEnv(t *testing.T) (*Runtime, string) {
	t.Helper()
	setGitIdentity(t)
	wfDir := laneBundle(t, w079Lane)
	hub := newFakeHub()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	sp := &scriptedSpawner{}
	cfg := Config{
		MachineID:     "m-test",
		Mapping:       mappingFor(ws, "proj"),
		DataDir:       filepath.Join(t.TempDir(), "data"),
		AgentStarter:  scriptedEngineStarter(sp),
		MaxRecoveries: 2,
	}
	toOB := supervise.NewQueue[protocol.RTMsg]()
	fromOB := supervise.NewQueue[protocol.OBMsg]()
	r := New(cfg, hub, sp, toOB, fromOB)
	hub.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = hub.capsuleRepoBase
	r.cfg.WorkflowsDir = wfDir
	return r, ws
}

func readPendingFile(t *testing.T, r *Runtime) map[string]map[string]*capsule.StepOverride {
	t.Helper()
	raw, err := os.ReadFile(r.pendingOverridesPath())
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]map[string]*capsule.StepOverride
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("the pending record does not parse: %v", err)
	}
	return m
}

// The claim materializes the pending record into the fresh capsule's
// overlay file (aspect merge over what the chain already carries) and
// consumes it.
func TestPendingOverrideMaterializesAtClaim(t *testing.T) {
	r, _ := pendingEnv(t)
	yes := true
	if err := r.savePendingOverrides("wi_ov4m", map[string]*capsule.StepOverride{
		"fix":     {Models: []string{"pi:ov/pending-a"}, ModelsBy: "usr_1"},
		"release": {Skip: &yes, SkipBy: "usr_1"},
	}); err != nil {
		t.Fatalf("save pending: %v", err)
	}
	if err := r.Start(context.Background(), "wi_ov4m", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf("wi_ov4m")
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	ov, err := capsule.ReadStepOverrides(j.capsuleDir)
	if err != nil {
		t.Fatalf("read the fresh capsule's overlay: %v", err)
	}
	if m := ov.Steps["fix"]; m == nil || len(m.Models) != 1 || m.Models[0] != "pi:ov/pending-a" || m.ModelsBy != "usr_1" {
		t.Fatalf("the materialized fix entry: %+v", m)
	}
	if s := ov.Steps["release"]; s == nil || s.Skip == nil || !*s.Skip {
		t.Fatalf("the materialized release entry: %+v", s)
	}
	if pend := readPendingFile(t, r); pend != nil && pend["wi_ov4m"] != nil {
		t.Fatalf("the claim must consume the pending record: %+v", pend["wi_ov4m"])
	}
	if err := r.Stop(context.Background(), "wi_ov4m", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// mergePendingAspects: a pending entry's aspects replace the capsule
// entry's same aspects and leave the others (an older chain the capsule
// already carries survives a pending skip, and vice versa).
func TestMergePendingAspects(t *testing.T) {
	yes := true
	no := false
	capsuleOv := capsule.StepOverrides{Steps: map[string]*capsule.StepOverride{
		"fix":     {Models: []string{"pi:ov/old-chain"}, ModelsBy: "usr_0", Skip: &no, SkipBy: "usr_0"},
		"review":  {Models: []string{"pi:ov/review-old"}},
		"release": {},
	}}
	pending := map[string]*capsule.StepOverride{
		"fix":     {Skip: &yes, SkipBy: "usr_1"}, // the skip lands, the old chain stays
		"review":  {Models: []string{"pi:ov/review-new"}, ModelsBy: "usr_1"},
		"release": {Skip: &yes, SkipBy: "usr_1"},
	}
	out := mergePendingAspects(capsuleOv, pending)
	if m := out.Steps["fix"]; m == nil || len(m.Models) != 1 || m.Models[0] != "pi:ov/old-chain" || m.Skip == nil || !*m.Skip || m.SkipBy != "usr_1" || m.ModelsBy != "usr_0" {
		t.Fatalf("fix after the merge: %+v (the pending skip replaces only the skip aspect)", m)
	}
	if rv := out.Steps["review"]; rv == nil || len(rv.Models) != 1 || rv.Models[0] != "pi:ov/review-new" {
		t.Fatalf("review after the merge: %+v", rv)
	}
	if _, ok := out.Steps["release"]; !ok {
		t.Fatal("an entry that gains an aspect from the pending record must stay")
	}
}

// A terminal close clears an unconsumed pending record; a blocked close
// keeps it (the re-claim reads it back).
func TestTerminalCloseClearsPendingRecord(t *testing.T) {
	r, _ := pendingEnv(t)
	if err := r.Start(context.Background(), "wi_ov4n", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := r.savePendingOverrides("wi_ov4n", map[string]*capsule.StepOverride{
		"fix": {Models: []string{"pi:ov/x"}},
	}); err != nil {
		t.Fatalf("save pending: %v", err)
	}
	// a blocked close keeps the record (the re-claim consumes it)
	if err := r.Stop(context.Background(), "wi_ov4n", protocol.FinishBlocked); err != nil {
		t.Fatalf("blocked Stop: %v", err)
	}
	if pend := readPendingFile(t, r); pend == nil || pend["wi_ov4n"] == nil {
		t.Fatal("a blocked close must keep the pending record")
	}
	// re-claim over the chain consumes it
	if err := r.Start(context.Background(), "wi_ov4n", StartOpts{}); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	if pend := readPendingFile(t, r); pend != nil && pend["wi_ov4n"] != nil {
		t.Fatalf("the re-claim must consume the pending record: %+v", pend["wi_ov4n"])
	}
	// a terminal close clears it
	if err := r.savePendingOverrides("wi_ov4n", map[string]*capsule.StepOverride{
		"fix": {Models: []string{"pi:ov/y"}},
	}); err != nil {
		t.Fatalf("save pending: %v", err)
	}
	if err := r.Stop(context.Background(), "wi_ov4n", protocol.FinishWrapped); err != nil {
		t.Fatalf("terminal Stop: %v", err)
	}
	if pend := readPendingFile(t, r); pend != nil && pend["wi_ov4n"] != nil {
		t.Fatalf("a terminal close must clear the pending record: %+v", pend["wi_ov4n"])
	}
}
