package runtime

// candidates_test.go: the F4/I2 candidate chain — spawn-time pinning of the
// bundle's defaults model_candidates, provider-error-only advancement
// (INV-1g), bounded wrap-around, crash-safe ordinal persistence across a
// restart, and the D5 claiming journal (durable claim replay +
// post-claim failure compensation). Candidate ids in fixtures are
// provider-qualified placeholders; no real model or vendor names appear
// anywhere here.

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

func TestSpawnModelPinRidesArgv(t *testing.T) {
	sp := newPiSpawner(&Config{PiBin: "/nonexistent-pi"})

	pinned := sp.buildCmd(SpawnParams{
		WorkItemID: "wi_pin", CapsuleDir: "/caps", SessionHandle: "ab12", SpawnN: 1,
		Model: "prov-a/model-a2",
	})
	joined := joinArgs(pinned.Args)
	if !strings.Contains(joined, "--model prov-a/model-a2") {
		t.Fatalf("pinned candidate must ride argv: %v", pinned.Args)
	}
	// the pin sits after the isolation profile, never before the transport
	if idxOf(pinned.Args, "--mode") > idxOf(pinned.Args, "--model") {
		t.Fatalf("transport flag must precede the pin: %v", pinned.Args)
	}

	plain := sp.buildCmd(SpawnParams{
		WorkItemID: "wi_pin2", CapsuleDir: "/caps", SessionHandle: "ab12", SpawnN: 1,
	})
	if strings.Contains(joinArgs(plain.Args), "--model") {
		t.Fatalf("an absent chain must not invent a pin: %v", plain.Args)
	}
}

// polyforge#176: a candidate's thinking effort rides each harness's own
// flag — pi --thinking, claude --effort — and --model stays the bare id
// (a model id with its own `:` is passed through untouched). No effort,
// no flag: the pre-#176 argv.
func TestSpawnEffortRidesHarnessFlag(t *testing.T) {
	pi := newPiSpawner(&Config{PiBin: "/nonexistent-pi"})
	got := joinArgs(pi.buildCmd(SpawnParams{CapsuleDir: "/caps", SpawnN: 1, Model: "ollama/llama3:8b", Effort: "minimal"}).Args)
	if !strings.Contains(got, "--model ollama/llama3:8b --thinking minimal") || strings.Contains(got, "--effort") {
		t.Fatalf("pi effort argv: %s", got)
	}
	if got := joinArgs(pi.buildCmd(SpawnParams{CapsuleDir: "/caps", SpawnN: 1, Model: "p/m"}).Args); strings.Contains(got, "--thinking") {
		t.Fatalf("no effort must pass no --thinking: %s", got)
	}
	cl := &claudeSpawner{cfg: &Config{ClaudeBin: "/opt/claude"}}
	cmd, err := cl.buildCmd(SpawnParams{CapsuleDir: "/caps", Model: "claude-opus-5-5", Effort: "high", HarnessSession: testSession})
	if err != nil {
		t.Fatal(err)
	}
	if got := joinArgs(cmd.Args); !strings.Contains(got, "--model claude-opus-5-5 --effort high") || strings.Contains(got, "--thinking") {
		t.Fatalf("claude effort argv: %s", got)
	}
	cmd, _ = cl.buildCmd(SpawnParams{CapsuleDir: "/caps", Model: "claude-opus-5-5", HarnessSession: testSession})
	if got := joinArgs(cmd.Args); strings.Contains(got, "--effort") {
		t.Fatalf("no effort must pass no --effort: %s", got)
	}
}

// A materialization failure closes the fresh generation BLOCKED — never
// an orphaned running generation (D5) — and settles the claiming
// journal. (The digest-divergence trigger is gone with the digest chain,
// polyforge#149: the failure here is a typed task-repo clone refusal —
// an unsafe repo name in the claim inventory.)
func TestMaterializeFailureBlocksGeneration(t *testing.T) {
	setGitIdentity(t)
	wfDir := pristineBundle(t)
	hub := newFakeHub()
	hub.repos = []api.ProjectRepo{{Name: "../escape"}}
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.WorkflowsDir = wfDir

	err := r.Start(context.Background(), "wi_cap405", StartOpts{})
	if err == nil {
		t.Fatal("Start must refuse an unmaterializable inventory")
	}
	if n := hub.endCount(); n != 1 {
		t.Fatalf("the failed start must close the generation exactly once, got %d", n)
	}
	end := hub.lastEnd()
	if end.Status != api.StatusBlocked || end.SessionID == "" || end.Note == "" {
		t.Fatalf("the materialization failure must close blocked on the session, got %+v", end)
	}
	// the claiming journal settled with it
	if items := r.loadClaiming(); len(items) != 0 {
		t.Fatalf("claiming journal must be empty after a compensated start, got %+v", items)
	}
	if r.jobOf("wi_cap405") != nil {
		t.Fatal("a compensated start leaves no live job")
	}
}

// A spawn failure after a successful claim is compensated the same way:
// the generation closes blocked, the journal settles.
// countingSpawner scripts the engine derivation through the shared
// scripted spawner and records the model pin each derivation carried.
type countingSpawner struct {
	scriptedSpawner
	models []string
}

func (s *countingSpawner) Spawn(ctx context.Context, p SpawnParams) (AgentHandle, error) {
	h, err := s.scriptedSpawner.Spawn(ctx, p)
	if err == nil {
		s.models = append(s.models, p.Model)
	}
	return h, err
}

func TestSpawnFailureBlocksGeneration(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	sp.failFresh = 1
	r, _, _ := newTestRuntime(t, hub, sp)

	if err := r.Start(context.Background(), "wi_spawnfail", StartOpts{}); err == nil {
		t.Fatal("Start must fail when the derivation cannot spawn")
	}
	if n := hub.endCount(); n != 1 || hub.lastEnd().Status != api.StatusBlocked {
		t.Fatalf("a spawn failure must close the generation blocked once, got %d %+v",
			hub.endCount(), hub.lastEnd())
	}
	if items := r.loadClaiming(); len(items) != 0 {
		t.Fatalf("claiming journal must be empty, got %+v", items)
	}
}

// A crash between the claim call and the spawn leaves a durable claiming
// record; Restart verifies the journaled session, replays the claim with
// the SAME session secret (D4: the same answer), adopts the session and
// finishes the start.
func TestClaimCrashRestartReplaysSameSecret(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	sp := &countingSpawner{}
	dataDir := filepath.Join(t.TempDir(), "data")
	r, _, _ := newTestRuntimeAt(t, hub, sp, dataDir)

	// the crash shape: the journal holds an unsettled claim intention
	// (the minted secret), state.json holds no job for it
	// (the crash predates registration) — but the claim DID land on the
	// hub, so the hub reports the journaled session as the holder.
	session := fakeSession("wi_crashd")
	if err := r.noteClaiming("wi_crashd", session, nil); err != nil {
		t.Fatal(err)
	}
	hub.hold("wi_crashd", session, api.StatusRunning)

	// a FRESH runtime over the same data dir replays it
	hub2 := newFakeHub()
	hub2.hold("wi_crashd", session, api.StatusRunning)
	sp2 := &countingSpawner{}
	r2, _, _ := newTestRuntimeAt(t, hub2, sp2, dataDir)
	if err := restartSettled(t, r2); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	settleReplays(t, r2) // the journal replays clone outside Restart
	if n := hub2.claimCount(); n != 1 {
		t.Fatalf("the replay must issue exactly one claim, got %d", n)
	}
	if got := hub2.claims[0].SessionID; got != session {
		t.Fatalf("the replay must carry the journaled session secret, got %q", got)
	}
	j := r2.jobOf("wi_crashd")
	if j == nil {
		t.Fatal("the replayed claim must run to a live agent")
	}
	if j.sessionID != session {
		t.Fatalf("the replay must adopt the SAME session, got %q", j.sessionID)
	}
	if n := sp2.callCount(); n != 1 {
		t.Fatalf("the replay must derive exactly one agent, got %d", n)
	}
	// settled: the journal is empty again
	if items := r2.loadClaiming(); len(items) != 0 {
		t.Fatalf("claiming journal must be empty after the replay, got %+v", items)
	}
	if err := r2.Stop(context.Background(), "wi_crashd", protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
}

// A stale journal entry (the session is no longer ours — e.g. the blocked
// close landed but the journal clear did not) is dropped, not replayed:
// SessionVerify answers matches=false and the entry goes.
func TestStaleClaimingEntryDropped(t *testing.T) {
	hub := newFakeHub()
	hub.status["wi_stalec"] = api.StatusBlocked
	sp := &countingSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)

	if err := r.noteClaiming("wi_stalec", fakeSession("wi_stalec"), nil); err != nil {
		t.Fatal(err)
	}
	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	settleReplays(t, r) // the journal replays clone outside Restart
	if n := hub.claimCount(); n != 0 {
		t.Fatalf("a stale entry must not be re-claimed, got %d claims", n)
	}
	if items := r.loadClaiming(); len(items) != 0 {
		t.Fatalf("the stale entry must be dropped, got %+v", items)
	}
	if r.jobOf("wi_stalec") != nil {
		t.Fatal("no job may be revived from a stale claiming entry")
	}
}

// ---------------------------------------------------------------------------
// helpers

// newTestRuntimeAt builds a runtime whose DataDir (and therefore the
// state.json and claiming-journal paths) is the CALLER's directory, so two
// runtimes can share one bookkeeping tree across a simulated restart.
func newTestRuntimeAt(t *testing.T, hub Aihub, sp Spawner, dataDir string) (*Runtime, *supervise.Queue[protocol.RTMsg], *supervise.Queue[protocol.OBMsg]) {
	t.Helper()
	cfg := Config{
		MachineID:     "m-test",
		Mapping:       mappingFor(filepath.Join(t.TempDir(), "ws")),
		DataDir:       dataDir,
		MaxRecoveries: 2,
		// episodes, not wall time (see newTestRuntime)
		// polyforge#106: the driver is the only engine (see newTestRuntime)
		AgentStarter: scriptedEngineStarter(sp),
	}
	toOB := supervise.NewQueue[protocol.RTMsg]()
	fromOB := supervise.NewQueue[protocol.OBMsg]()
	r := New(cfg, hub, sp, toOB, fromOB)
	t.Cleanup(func() { r.Close(); settleOps(t, r, 10e9) }) // see newTestRuntime
	// The claim-time capsule remote (see newTestRuntime): aihub creates
	// the bare repo at claim; the fake hub implements the contract.
	if fh, ok := hub.(*fakeHub); ok {
		fh.capsuleRepoBase = filepath.Join(dataDir, "capsule-remotes")
		r.cfg.GitRemoteBase = fh.capsuleRepoBase
	}
	return r, toOB, fromOB
}

func joinArgs(args []string) string {
	out := ""
	for i, a := range args {
		if i > 0 {
			out += " "
		}
		out += a
	}
	return out
}

func idxOf(args []string, want string) int {
	for i, a := range args {
		if a == want {
			return i
		}
	}
	return -1
}

// When the compensation's hub close itself fails (an aihub outage at
// the worst moment), the session is KEPT as an end_pending record —
// persisted before the claiming entry goes — and its close is retried:
// the dangling claim window stays closed even across a failing close.
func TestAbandonedClaimKeepsSessionWhenCloseFails(t *testing.T) {
	setGitIdentity(t)
	wfDir := pristineBundle(t)
	hub := newFakeHub()
	hub.repos = []api.ProjectRepo{{Name: "../escape"}} // the materialize refusal
	hub.end["wi_closefail"] = errLookupBoom            // the hub close fails
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.WorkflowsDir = wfDir
	r.cfg.EndRetryBase = time.Hour // the retry stays out of the way

	if err := r.Start(context.Background(), "wi_closefail", StartOpts{}); err == nil {
		t.Fatal("Start must fail on the unmaterializable inventory")
	}
	recs := loadRecords(t, r)
	if len(recs) != 1 || recs[0].WorkItemID != "wi_closefail" || recs[0].State != string(StateEndPending) || recs[0].SessionID == "" {
		t.Fatalf("a failed close must keep the session as an end_pending record, got %+v", recs)
	}
	if items := r.loadClaiming(); len(items) != 0 {
		t.Fatalf("the end_pending record replaces the claiming entry, got %+v", items)
	}
}
