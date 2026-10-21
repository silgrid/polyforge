package runtime

// recover_candidates_test.go: the recovery lane's polyforge#79
// rehydration of the effective candidate map. A LIVE job relaunches
// under its claim-time snapshot (an edit governs the NEXT claim, never
// an in-flight job's recomputation); a restart-RECONSTRUCTED job (nil
// snapshot) re-derives the lists from the workspace yaml FRESH through
// the same claim-time compose, so the archived-attempt recomputation
// indexes the re-derived list. The interrupted-dispatch archive signal
// rides the relaunch as the raw recovery cause.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// recordingStarter wraps the scripted engine starter and records every
// AgentStartInput a relaunch carries — the recovery lane's rehydration
// is observable at exactly this seam.
type recordingStarter struct {
	mu     sync.Mutex
	inputs []AgentStartInput
	base   AgentStartFunc
}

func (rs *recordingStarter) start(ctx context.Context, in AgentStartInput) (AgentHandle, error) {
	rs.mu.Lock()
	rs.inputs = append(rs.inputs, in)
	rs.mu.Unlock()
	return rs.base(ctx, in)
}

func (rs *recordingStarter) last() AgentStartInput {
	rs.mu.Lock()
	defer rs.mu.Unlock()
	if len(rs.inputs) == 0 {
		return AgentStartInput{}
	}
	return rs.inputs[len(rs.inputs)-1]
}

// A LIVE job relaunches under its CLAIM-TIME snapshot: the yaml edited
// mid-flight governs the NEXT claim, never this job's recovery
// recomputation, and the interrupted-dispatch archive signal rides the
// relaunch as the raw recovery cause.
func TestRecoveryRelaunchCarriesClaimSnapshot(t *testing.T) {
	setGitIdentity(t)
	wfDir := frontmatterBundle(t, w079Frontmatter)
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
	rec := &recordingStarter{base: scriptedEngineStarter(sp)}
	r.cfg.AgentStarter = rec.start

	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    fix: { models: [pi:w79/code-a, pi:w79/code-b] }\n")
	if err := r.Start(context.Background(), "wi_w079rec", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf("wi_w079rec")
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	j.mu.Lock()
	claimSnap := j.stepCandidates
	j.mu.Unlock()
	if claimSnap["fix"] == nil || claimSnap["fix"][0] != "pi:w79/code-a" {
		t.Fatalf("claim snapshot for fix %v, want [w79/code-a ...]", claimSnap["fix"])
	}
	// Drain the claim's own RTStarted before the recovery's message.
	if got := recvRT(t, toOB); got.Kind != protocol.RTStarted {
		t.Fatalf("expected the claim's RTStarted, got %+v", got)
	}

	// The mid-flight edit: the yaml on disk now pins a DIFFERENT list.
	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    fix: { models: [pi:w79/edited-a] }\n")

	// The death → recovery: the relaunch must carry the claim snapshot,
	// NOT a re-derivation of the edited yaml.
	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	msg := recvRT(t, toOB)
	if msg.Kind != protocol.RTRecovered {
		t.Fatalf("expected RTRecovered, got %+v", msg)
	}
	relaunch := rec.last()
	if len(relaunch.StepCandidates["fix"]) != 2 ||
		relaunch.StepCandidates["fix"][0] != "pi:w79/code-a" ||
		relaunch.StepCandidates["fix"][1] != "pi:w79/code-b" {
		t.Fatalf("relaunch StepCandidates for fix %v, want the claim snapshot [w79/code-a w79/code-b] — an edited yaml governs only the NEXT claim (the disk yaml now pins [w79/edited-a])", relaunch.StepCandidates["fix"])
	}
	if relaunch.InterruptedCause != protocol.RecoverDeath {
		t.Fatalf("relaunch InterruptedCause %q, want the raw recovery cause %q (the archive signal)", relaunch.InterruptedCause, protocol.RecoverDeath)
	}
}

// A restart-RECONSTRUCTED job rehydrates the claim snapshot from the
// daemon-private state.json record and never re-reads the yaml or the
// agent-writable capsule.json: an edited yaml, or a forged
// effective_candidates in the capsule, changes nothing.
func TestRecoveryReconstructedJobRehydratesRecordNotYamlOrCapsule(t *testing.T) {
	setGitIdentity(t)
	wfDir := frontmatterBundle(t, w079Frontmatter)
	sp := &scriptedSpawner{}
	cfg := Config{
		MachineID:     "m-test",
		Mapping:       mappingFor(filepath.Join(t.TempDir(), "ws")),
		DataDir:       filepath.Join(t.TempDir(), "data"),
		AgentStarter:  scriptedEngineStarter(sp),
		MaxRecoveries: 2,
	}
	toOB := supervise.NewQueue[protocol.RTMsg]()
	fromOB := supervise.NewQueue[protocol.OBMsg]()
	hub2 := newFakeHub()
	r := New(cfg, hub2, sp, toOB, fromOB)

	// The claim-time capsule remote (see newTestRuntime): aihub creates
	// the bare repo at claim; the fake hub implements the contract.
	hub2.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = hub2.capsuleRepoBase
	r.cfg.WorkflowsDir = wfDir
	ws, err := r.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	live := seedJob(r, "wi_w079rec2")
	live.stepCandidates = map[string][]string{
		"fix":   {"pi:w79/aud-a", "pi:w79/aud-b"},
		"grill": {"pi:prov/weak-a"},
	}
	j := jobFromRecord(live.record()) // the restart shape
	blob, err := json.Marshal(map[string]any{"effective_candidates": map[string][]string{"fix": {"pi:evil/x"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(j.capsuleDir, capsule.CapsuleJSON), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, y := range []string{
		"steps:\n  proj:\n    fix: { models: [pi:w79/edited] }\n",
		"steps:\n  proj:\n    fix: [\n", // invalid: recovery must not care
	} {
		writeWorkspaceYaml(t, ws, y)
		got := r.effectiveCandidatesForRecovery(j)
		if len(got["fix"]) != 2 || got["fix"][0] != "pi:w79/aud-a" || got["fix"][1] != "pi:w79/aud-b" {
			t.Fatalf("rehydrated fix list %v, want the record's [w79/aud-a w79/aud-b] regardless of the disk yaml %q and the capsule.json forgery", got["fix"], y)
		}
	}
}

// A record without a snapshot and no workspace yaml recomposes the
// bundle's own lists, exactly what the claim would compose (the driver
// reads no list of its own).
func TestRecoverySnapshotlessRecordRecomposesBundleLists(t *testing.T) {
	setGitIdentity(t)
	wfDir := frontmatterBundle(t, w079Frontmatter)
	sp := &scriptedSpawner{}
	cfg := Config{
		MachineID:     "m-test",
		Mapping:       mappingFor(filepath.Join(t.TempDir(), "ws")),
		DataDir:       filepath.Join(t.TempDir(), "data"),
		AgentStarter:  scriptedEngineStarter(sp),
		MaxRecoveries: 2,
	}
	toOB := supervise.NewQueue[protocol.RTMsg]()
	fromOB := supervise.NewQueue[protocol.OBMsg]()
	hub2 := newFakeHub()
	r := New(cfg, hub2, sp, toOB, fromOB)

	// The claim-time capsule remote (see newTestRuntime): aihub creates
	// the bare repo at claim; the fake hub implements the contract.
	hub2.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = hub2.capsuleRepoBase
	r.cfg.WorkflowsDir = wfDir
	j := seedJob(r, "wi_w079rec4")
	got := r.effectiveCandidatesForRecovery(j)
	if f := got["fix"]; len(f) != 2 || f[0] != "pi:prov/flash" || f[1] != "pi:prov/glm" {
		t.Fatalf("snapshot-less rehydration %v, want the bundle's lists (fix [pi:prov/flash pi:prov/glm])", got)
	}
}

// A record written before claims stored the candidate snapshot carries
// none: the recovery recomposes the lists exactly as a claim does (the
// driver reads no other candidate source), instead of relaunching on the
// harness default.
func TestRecoveryRecomposesARecordWithoutSnapshot(t *testing.T) {
	setGitIdentity(t)
	wfDir := frontmatterBundle(t, w079Frontmatter)
	hub := newFakeHub()
	ws := filepath.Join(t.TempDir(), "ws")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}
	sp := &scriptedSpawner{}
	toOB := supervise.NewQueue[protocol.RTMsg]()
	r := New(Config{
		MachineID:     "m-test",
		Mapping:       mappingFor(ws, "proj"),
		DataDir:       filepath.Join(t.TempDir(), "data"),
		MaxRecoveries: 2,
	}, hub, sp, toOB, supervise.NewQueue[protocol.OBMsg]())
	hub.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
	r.cfg.GitRemoteBase = hub.capsuleRepoBase
	r.cfg.WorkflowsDir = wfDir
	rec := &recordingStarter{base: scriptedEngineStarter(sp)}
	r.cfg.AgentStarter = rec.start

	writeWorkspaceYaml(t, ws, "steps:\n  proj:\n    fix: { models: [pi:w79/code-a, pi:w79/code-b] }\n")
	if err := r.Start(context.Background(), "wi_w079old", StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	j := r.jobOf("wi_w079old")
	if j == nil {
		t.Fatal("the claim must run to a live agent")
	}
	if got := recvRT(t, toOB); got.Kind != protocol.RTStarted {
		t.Fatalf("expected the claim's RTStarted, got %+v", got)
	}
	j.mu.Lock()
	j.stepCandidates = nil // the shape of a record without step_candidates
	j.mu.Unlock()

	r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTRecovered {
		t.Fatalf("expected RTRecovered, got %+v", msg)
	}
	got := rec.last().StepCandidates["fix"]
	if len(got) != 2 || got[0] != "pi:w79/code-a" || got[1] != "pi:w79/code-b" {
		t.Fatalf("relaunch StepCandidates for fix %v, want the recomposed [pi:w79/code-a pi:w79/code-b]", got)
	}
}
