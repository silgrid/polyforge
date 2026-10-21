package daemon

// fakes + shared scaffolding for the daemon package tests: an in-process
// fake agent handle (the runtime lane's own tests exercise REAL process
// death/kill semantics; here the control paths are the subject), a fake
// aihub, and builders for a daemon under test with real UDS + real git.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// ---------------------------------------------------------------------------
// fake agent handle

type fakeAgent struct {
	mu         sync.Mutex
	done       bool
	terminated bool
	exit       chan struct{} // closed by Terminate; created on first use
}

func (a *fakeAgent) PID() int          { return 0 } // runtime's kill guard no-ops on pid<=0
func (a *fakeAgent) StartedAt() string { return "" }
func (a *fakeAgent) Alive() bool       { return !a.isDone() }
func (a *fakeAgent) Done() bool        { return a.isDone() }
func (a *fakeAgent) isDone() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.done
}
func (a *fakeAgent) Exit() <-chan struct{} {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.exitLocked()
}
func (a *fakeAgent) exitLocked() chan struct{} {
	if a.exit == nil {
		a.exit = make(chan struct{})
	}
	return a.exit
}
func (a *fakeAgent) Terminate(time.Duration) error {
	a.mu.Lock()
	if !a.done {
		close(a.exitLocked())
	}
	a.done = true
	a.terminated = true
	a.mu.Unlock()
	return nil
}

// ---------------------------------------------------------------------------
// fake spawner

type fakeSpawner struct {
	mu      sync.Mutex
	calls   []runtime.SpawnParams
	spawned []*fakeAgent // one entry per Spawn, in order
}

func (s *fakeSpawner) Spawn(ctx context.Context, p runtime.SpawnParams) (runtime.AgentHandle, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, p)
	a := &fakeAgent{}
	s.spawned = append(s.spawned, a)
	return a, nil
}

func (s *fakeSpawner) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

// agentOf returns the fake agent of the n-th spawn (0-based).
func (s *fakeSpawner) agentOf(n int) *fakeAgent {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n >= len(s.spawned) {
		return nil
	}
	return s.spawned[n]
}

// ---------------------------------------------------------------------------
// fake aihub (v1.5 session model): the daemon-facing subset of the hub —
// session claim/verify/attach/end plus the queue and the generic Do the
// memory lanes proxy through. `held` maps wi → session secret; an UNHELD
// running WI answers verify matches=true for ANY secret (the reconciling
// daemon's own record), which is exactly what the seeded-record restart
// tests need; a HELD wi answers matches only for its own secret.

type fakeHub struct {
	mu       sync.Mutex
	status   map[string]string
	held     map[string]string
	claims   []api.SessionClaimRequest
	ends     []endCall
	attaches []api.SessionAttachRequest
	// verifies records the wi of every SessionVerify call (the M4
	// revalidation seam the -revalidate-every flag test pins).
	verifies []string
	// resumeBlockeds records the wi of every §6.4 owner resume call
	// (polyforge#116): the resume door (resume-blocked route).
	resumeBlockeds []string
	queues         []api.WorkItem
	// reporters maps a canonical id to the WI reporter (owner fallback).
	reporters map[string]string
	blockEnd  chan struct{} // non-nil: SessionEnd parks until closed
	// blockVerify, when non-nil, parks every SessionVerify until closed
	// (a hung hub at the restart verify); verifyEntered, when non-nil, is
	// closed once (verifyEnteredOnce) when a verify parked.
	blockVerify       chan struct{}
	verifyEntered     chan struct{}
	verifyEnteredOnce sync.Once
	// blockProbe, when non-nil, parks every GetWorkItem until closed.
	blockProbe chan struct{}
	// probeFails, when > 0, makes the next N GetWorkItem calls fail with a
	// transport-class error (a transient hub blip).
	probeFails int
	// gets counts GetWorkItem calls per canonical id.
	gets map[string]int
	// claimLost, when > 0, makes the next N SessionClaim calls COMMIT the
	// claim and then fail with a transport-class error (the answer lost
	// in transit, repo review 2026-10-16 B-1).
	claimLost int
	// claimRepos maps a canonical id to the task-repo inventory its
	// claim answer serves (B-8 tests: a hanging remote); unmapped = none.
	claimRepos map[string][]api.ProjectRepo
	// endFails, when > 0, makes the next N SessionEnd calls fail with a
	// transport-class error and no effect (a hub blip at the close).
	endFails int
	// unknown lists canonical ids GetWorkItem answers CORE-404 for.
	unknown map[string]bool
	// bundles maps a canonical id to the bundle GetWorkItem reports
	// (polyforge#175 pre-claim bundle check); unmapped ids report none.
	bundles map[string]string
	// canonical maps an id-or-slug form ("proj#7") to the canonical id
	// (F2 route tests); an unmapped input echoes itself.
	canonical map[string]string
	// slugs maps a canonical id to its "<project>#<seq>" slug (#144
	// display/naming tests); an unmapped id answers no slug.
	slugs map[string]string
	// memDo, when non-nil, serves the generic Do calls the F6 memory
	// adapters make (search + memory create); nil answers a typed
	// refusal. The tests that exercise the memory lanes script it.
	memDo func(method, path string, body, out any) error
	// memContent scripts GetMemoryContent (the step memory read).
	memContent func(id string) ([]byte, error)
	// queueErr, when non-nil, is what Queue answers instead of the
	// seeded list — the drain degraded-signal test scripts denials with
	// it.
	queueErr error
	// attachErr, when non-nil, is what SessionAttach answers (a
	// transport-class error: the hub was not reached).
	attachErr error
	// cleanPauseCleared records the operator's `pf resume` PATCH having
	// cleared clean_pause on this hub (the B6 repro's HTTP face writes
	// it; the daemon-side resume-paused route reads it through
	// SessionVerify's CleanPause answer below).
	cleanPauseCleared bool
	// cleanPause is the hub-side clean_pause state per work item
	// (wi_seWTgHeKtaa8): latched by a pause close's
	// SessionEnd(paused, CleanPause=true), cleared by the operator's
	// resume PATCH (cleanPauseCleared). SessionVerify answers it so the
	// live-holder resume door re-validates the flag exactly like the
	// startup reconciliation does against the real hub.
	cleanPause map[string]bool
	// queueBlock, when non-nil, parks each Queue call until the channel
	// closes; queueEntered, when non-nil, is closed exactly once when a
	// Queue call has parked. The B3 race repro (auto off landing while a
	// wave is mid-fetch) scripts the interleave with them; both are nil
	// in every other test.
	queueBlock   chan struct{}
	queueEntered chan struct{}
	// heartbeats/heartbeatErr are the M3 liveness seam: heartbeats
	// records every Heartbeat payload the daemon POSTed; heartbeatErr,
	// when non-nil, is what Heartbeat answers instead.
	// when non-nil, fails every call (the backoff test's fault
	// injector); heartbeatAttempts counts every call including failures
	// (the disabled-mode test asserts on attempts, not successes). All
	// guarded by mu.
	heartbeats        []aihubclient.MachineHeartbeat
	heartbeatErr      error
	heartbeatAttempts int
}

type endCall struct {
	WI  string
	Req api.SessionEndRequest
}

func newFakeHub() *fakeHub {
	return &fakeHub{status: map[string]string{}, held: map[string]string{}, cleanPause: map[string]bool{}}
}

func (h *fakeHub) canonicalID(in string) string {
	if c, ok := h.canonical[in]; ok {
		return c
	}
	return in
}

func (h *fakeHub) SessionClaim(req api.SessionClaimRequest) (*api.SessionClaimResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.claims = append(h.claims, req)
	id := h.canonicalID(req.WorkItemID)
	h.held[id] = req.SessionID
	h.status[id] = api.StatusRunning
	if h.claimLost > 0 {
		h.claimLost--
		return nil, errors.New("context deadline exceeded (Client.Timeout exceeded while awaiting headers)")
	}
	return &api.SessionClaimResponse{
		Claim: api.SessionClaim{
			WorkItemID:         id,
			SessionFingerprint: api.SessionFingerprint(req.SessionID),
			BundleName:         "demo-bundle",
			Repos:              h.claimRepos[id],
		},
		WorkItem: api.WorkItem{ID: id, Slug: h.slugs[id], ProjectName: "proj"},
	}, nil
}

func (h *fakeHub) SessionAttach(wi string, req api.SessionAttachRequest) (*api.SessionClaimResponse, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.attachErr != nil {
		return nil, h.attachErr
	}
	h.attaches = append(h.attaches, req)
	id := h.canonicalID(wi)
	h.status[id] = api.StatusRunning
	return &api.SessionClaimResponse{
		Claim:    api.SessionClaim{WorkItemID: id, SessionFingerprint: api.SessionFingerprint(req.SessionID)},
		WorkItem: api.WorkItem{ID: id, Slug: h.slugs[id], ProjectName: "proj"},
	}, nil
}

func (h *fakeHub) SessionVerify(wi, sessionID string) (*api.SessionVerifyResponse, error) {
	h.mu.Lock()
	block, entered := h.blockVerify, h.verifyEntered
	h.mu.Unlock()
	if block != nil {
		if entered != nil {
			h.verifyEnteredOnce.Do(func() { close(entered) })
		}
		<-block
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.verifies = append(h.verifies, h.canonicalID(wi))
	id := h.canonicalID(wi)
	st := h.status[id]
	if st == "" {
		st = api.StatusRunning
	}
	out := &api.SessionVerifyResponse{WorkItemID: id, Status: st}
	// the hub-side operator-pause flag (wi_seWTgHeKtaa8): latched by the
	// pause close's SessionEnd, cleared by the operator's resume PATCH
	// (cleanPauseCleared — the B6 repro's HTTP face writes it).
	if st == api.StatusPaused && h.cleanPause[id] && !h.cleanPauseCleared {
		out.CleanPause = true
	}
	if cur := h.held[id]; cur != "" {
		if cur == sessionID {
			out.Matches = true
		} else {
			out.CurrentFingerprint = api.SessionFingerprint(cur)
		}
	} else if st == api.StatusRunning && sessionID != "" {
		// unheld-but-running: the reconciling record's own session (the
		// seeded-restart tests' shape)
		out.Matches = true
	}
	return out, nil
}

func (h *fakeHub) SessionEnd(wi string, req api.SessionEndRequest) error {
	h.mu.Lock()
	block := h.blockEnd
	h.mu.Unlock()
	if block != nil {
		<-block
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.endFails > 0 {
		h.endFails--
		return errors.New("connection refused")
	}
	id := h.canonicalID(wi)
	h.ends = append(h.ends, endCall{WI: id, Req: req})
	switch req.Status {
	case api.StatusWrapped, api.StatusFailed, api.StatusBlocked, api.StatusCanceled:
		h.held[id] = ""
		h.cleanPause[id] = false
	case api.StatusPaused:
		// the pause close's operator-pause marker (wi_seWTgHeKtaa8)
		h.cleanPause[id] = req.CleanPause
	}
	h.status[id] = req.Status
	return nil
}

func (h *fakeHub) GetWorkItem(idOrSlug string) (*api.WorkItem, error) {
	h.mu.Lock()
	block := h.blockProbe
	h.mu.Unlock()
	if block != nil {
		<-block
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.probeFails > 0 {
		h.probeFails--
		return nil, errors.New("connection refused")
	}
	id := h.canonicalID(idOrSlug)
	if h.gets == nil {
		h.gets = map[string]int{}
	}
	h.gets[id]++
	if h.unknown[id] {
		return nil, &api.Error{Code: api.ErrCoreNotFound, Detail: "work item not found"}
	}
	st := h.status[id]
	if st == "" {
		st = api.StatusRunning
	}
	wi := &api.WorkItem{ID: id, Slug: h.slugs[id], Status: st, ProjectName: "proj", Reporter: h.reporters[id]}
	if b, ok := h.bundles[id]; ok {
		wi.BundleName = &b
	}
	return wi, nil
}

// ResumeBlockedWorkItem is the §6.4 owner resume door (polyforge#116):
// the resume door returns the WI to claimable (queued, session cleared).
// It records the transition in the status map; the returned view is
// unused by the routes (only the error matters).
func (h *fakeHub) ResumeBlockedWorkItem(idOrSlug string) (json.RawMessage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.canonicalID(idOrSlug)
	h.status[id] = api.StatusQueued
	delete(h.held, id)
	h.resumeBlockeds = append(h.resumeBlockeds, id)
	return json.RawMessage(`{"id":"` + id + `","status":"` + api.StatusQueued + `"}`), nil
}

func (h *fakeHub) Queue(project string) ([]api.WorkItem, error) {
	h.mu.Lock()
	block, entered := h.queueBlock, h.queueEntered
	h.queueEntered = nil
	h.mu.Unlock()
	if entered != nil {
		close(entered)
	}
	if block != nil {
		<-block
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.queueErr != nil {
		return nil, h.queueErr
	}
	out := make([]api.WorkItem, 0, len(h.queues))
	out = append(out, h.queues...)
	return out, nil
}

// Heartbeat records the M3 liveness POSTs; heartbeats carries the received
// payloads (inspection seam for the heartbeat tests) and heartbeatErr, if
// set, fails every call (the backoff test's fault injector).
func (h *fakeHub) Heartbeat(req aihubclient.MachineHeartbeat) (*aihubclient.MachineHeartbeatResult, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.heartbeatAttempts++
	if h.heartbeatErr != nil {
		return nil, h.heartbeatErr
	}
	h.heartbeats = append(h.heartbeats, req)
	return &aihubclient.MachineHeartbeatResult{MachineID: req.MachineID, UserID: "mac_test"}, nil
}

func (h *fakeHub) setHeartbeatErr(err error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.heartbeatErr = err
}

func (h *fakeHub) heartbeatsSnapshot() []aihubclient.MachineHeartbeat {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]aihubclient.MachineHeartbeat, len(h.heartbeats))
	copy(out, h.heartbeats)
	return out
}

// Do serves the generic typed-request face the F6 memory adapters use.
func (h *fakeHub) Do(method, path string, body, out any) error {
	h.mu.Lock()
	fn := h.memDo
	h.mu.Unlock()
	if fn == nil {
		return &api.Error{Code: api.ErrDaemonInternal, Detail: "fakeHub: Do call not scripted"}
	}
	return fn(method, path, body, out)
}

// GetMemoryContent serves the per-id memory body read (memContent).
func (h *fakeHub) GetMemoryContent(id string) ([]byte, error) {
	h.mu.Lock()
	fn := h.memContent
	h.mu.Unlock()
	if fn == nil {
		return nil, &api.Error{Code: api.ErrDaemonInternal, Detail: "fakeHub: GetMemoryContent not scripted"}
	}
	return fn(id)
}

func (h *fakeHub) verifyCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.verifies)
}

func (h *fakeHub) endCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.ends)
}

func (h *fakeHub) lastEnd() api.SessionEndRequest {
	h.mu.Lock()
	defer h.mu.Unlock()
	if len(h.ends) == 0 {
		return api.SessionEndRequest{}
	}
	return h.ends[len(h.ends)-1].Req
}

// ---------------------------------------------------------------------------
// git scaffolding: a bare "aihub" remote per work item + a seeded capsule

func setGitIdentity(t *testing.T) {
	t.Helper()
	for _, kv := range [][2]string{
		{"GIT_AUTHOR_NAME", "pf-test"},
		{"GIT_AUTHOR_EMAIL", "pf-test@invalid"},
		{"GIT_COMMITTER_NAME", "pf-test"},
		{"GIT_COMMITTER_EMAIL", "pf-test@invalid"},
	} {
		t.Setenv(kv[0], kv[1])
	}
}

// mkBare creates the aihub-side bare repo for wi so runtime's clone and
// every push succeed. Push options are advertised: every push from the
// gitops choke point carries --push-option=attempt-id=… (§六 备层).
func mkBare(t *testing.T, base, wi string) string {
	t.Helper()
	dir := filepath.Join(base, wi+".git")
	if out, err := exec.Command("git", "init", "--bare", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "--git-dir", dir, "config", "receive.advertisePushOptions", "true").CombinedOutput(); err != nil {
		t.Fatalf("git config advertisePushOptions: %v: %s", err, out)
	}
	return dir
}

// seedCapsuleGit pre-materializes a capsule worktree + external git-dir
// + transcript for a runtime state.json record (the restart test's input).
func seedCapsuleGit(t *testing.T, capsuleDir, gitDir, remote string) {
	t.Helper()
	if err := os.MkdirAll(capsuleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", capsuleDir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.MkdirAll(filepath.Dir(gitDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(capsuleDir, ".git"), gitDir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(capsuleDir, ".git"), []byte("gitdir: "+gitDir+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := append(os.Environ(), "GIT_DIR="+gitDir, "GIT_WORK_TREE="+capsuleDir)
	for _, args := range [][]string{
		{"checkout", "-b", "main"},
		{"commit", "--allow-empty", "-m", "seed"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Env = env
		cmd.Dir = capsuleDir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
}

// seedStateFile writes a runtime state.json record (the runtime lane's
// own format, reconstructed field-for-field for the restart test).
func seedStateFile(t *testing.T, dataDir, machineID string, rec map[string]any) {
	t.Helper()
	blob, err := json.MarshalIndent(map[string]any{
		"version":    1,
		"machine_id": machineID,
		"jobs":       []map[string]any{rec},
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "runtime-state.json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
}

// fakeEngineStarter binds a scripted execution engine: the driver is
// the only engine (polyforge#106) and the config knob only swaps the
// starter, so the in-process tests script the engine through it. The
// starter delegates to the job's own spawner (the config knob), so the
// fake-agent handles and spawn-call assertions keep working — including
// for tests that swap cfg.Spawner for a wrapping variant.
func fakeEngineStarter() AgentStartFunc {
	return func(ctx context.Context, in runtime.AgentStartInput) (Agent, error) {
		h, err := in.Spawner.Spawn(ctx, runtime.SpawnParams{
			WorkItemID:    in.WI,
			CapsuleDir:    in.CapsuleDir,
			SessionHandle: in.SessionHandle,
			SpawnN:        int(in.SpawnN),
			InitialPrompt: "engine",
		})
		if err != nil {
			return nil, err
		}
		a, ok := h.(Agent)
		if !ok {
			return nil, fmt.Errorf("fake engine: spawned handle %T has no exit signal", h)
		}
		return a, nil
	}
}

// mappingFor builds the daemon tests' one-workspace plan: ws mapping the
// fakes' project "proj" (polyforge#113 — the Config carries the mapping,
// never a single workspace or project scope).
func mappingFor(ws string, projects ...string) *mapping.Plan {
	if len(projects) == 0 {
		projects = []string{"proj"}
	}
	p, err := mapping.Build([]mapping.Entry{{Workspace: ws, Projects: projects}})
	if err != nil {
		panic(err)
	}
	return p
}

// ---------------------------------------------------------------------------
// daemon under test

type testDaemon struct {
	*Daemon
	hub     *fakeHub
	spawner *fakeSpawner
	client  *cli.DaemonClient
	sock    string
	runDone chan error
}

func newTestDaemon(t *testing.T, mutate func(*Config)) *testDaemon {
	t.Helper()
	setGitIdentity(t)
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	data := filepath.Join(dir, "data")
	hubBase := filepath.Join(dir, "hub")
	for _, d := range []string{ws, data, hubBase} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hub := newFakeHub()
	sp := &fakeSpawner{}
	cfg := Config{
		MachineID:  "m-test",
		SocketPath: filepath.Join(dir, "s.sock"),
		// The driver is the only engine (polyforge#106); these tests bind
		// a FAKE engine starter that delegates to the fake spawner so the
		// scripted WI-Agent semantics (spawn call params, agent handles)
		// stay assertable without a real driver.
		Mapping:       mappingFor(ws),
		DataDir:       data,
		GitRemoteBase: hubBase,
		MaxConcurrent: 4,
		Hub:           hub,
		Spawner:       sp,
		AgentStarter:  fakeEngineStarter(),
		ObservePoll:   10 * time.Millisecond,
		DrainInterval: 10 * time.Millisecond,
		DispatchTick:  10 * time.Millisecond,
	}
	if mutate != nil {
		mutate(&cfg)
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	td := &testDaemon{
		Daemon:  d,
		hub:     hub,
		spawner: sp,
		sock:    cfg.SocketPath,
		runDone: make(chan error, 1),
	}
	go func() { td.runDone <- d.Run(context.Background()) }()
	// wait for the socket to be live
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(td.sock); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("daemon socket never appeared")
		}
		time.Sleep(5 * time.Millisecond)
	}
	td.client = cli.NewDaemonClient(td.sock)
	t.Cleanup(func() {
		_ = d.Shutdown(context.Background(), DirectShutdownCause())
		select {
		case <-td.runDone:
		case <-time.After(5 * time.Second):
		}
		// the shutdown waits for no claim: let one still under way (its
		// durable tail, its queue settle) finish before the temp dir goes
		settled := func() bool {
			d.q.mu.Lock()
			n := len(d.q.inflight)
			d.q.mu.Unlock()
			return n == 0 && d.rt.Idle()
		}
		for deadline := time.Now().Add(5 * time.Second); !settled() && time.Now().Before(deadline); {
			time.Sleep(5 * time.Millisecond)
		}
	})
	return td
}

// submitClaim submits one work item and waits for its job to go live.
func (td *testDaemon) submitClaim(t *testing.T, wi string) {
	t.Helper()
	mkBare(t, td.cfg.GitRemoteBase, wi)
	raw, err := td.client.ClaimWithModels(wi, "normal", nil)
	if err != nil {
		t.Fatalf("claim %s: %v", wi, err)
	}
	var sr cli.SubmitResponse
	if err := json.Unmarshal(raw, &sr); err != nil {
		t.Fatalf("submit answer undecodable: %v", err)
	}
	if !sr.Accepted || sr.WorkItemID != wi {
		t.Fatalf("submit answer = %+v, want accepted for %s", sr, wi)
	}
	td.waitLive(t, wi)
}

// seedPendingQuestion plays the driver's needs_input park: it writes an
// open pending_question for the step into the capsule's execution.json
// (Version bumped, atomic tmp+rename) and sends the exec_committed fact
// so the supervisor projects it.
func (td *testDaemon) seedPendingQuestion(t *testing.T, wi, qid, step, question string) {
	t.Helper()
	td.hub.mu.Lock()
	name := api.PathName(td.hub.slugs[wi], wi)
	td.hub.mu.Unlock()
	path := filepath.Join(td.cfg.Mapping.Workspaces()[0], "capsules", name, filepath.FromSlash(capsule.ExecutionJSON))
	exec := capsule.Execution{WorkItemID: wi, Steps: map[string]*capsule.Step{}}
	if raw, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(raw, &exec)
		if exec.Steps == nil {
			exec.Steps = map[string]*capsule.Step{}
		}
	}
	st := exec.Steps[step]
	if st == nil {
		st = &capsule.Step{Status: capsule.StepInProgress}
		exec.Steps[step] = st
	}
	st.PendingQuestion = &capsule.PendingQuestion{
		ID: qid, Step: step, Question: question,
		AskedAt: time.Now().UTC().Format(time.RFC3339Nano),
	}
	exec.CurrentStep = step
	exec.Version++
	blob, err := json.Marshal(exec)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	// the driver's commit-boundary notice: the supervisor projects the
	// pending question when the fact arrives (it no longer polls)
	td.sup.Link(wi).Fact(supervise.ExecCommitted{Version: exec.Version, Step: step})
}

// waitLive blocks until the work item holds a runtime job past its
// claim (a CLAIMING job is still cloning, B-8: not live yet).
func (td *testDaemon) waitLive(t *testing.T, wi string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, err := td.client.Status()
		if err == nil {
			var st cli.StatusResponse
			if json.Unmarshal(raw, &st) == nil {
				for _, j := range st.Jobs {
					if j.WorkItemID == wi && j.Status != string(runtime.StateClaiming) {
						return
					}
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s never went live", wi)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitMonitored blocks until the per-WI observe monitor is attached.
func (td *testDaemon) waitMonitored(t *testing.T, wi string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, ok := td.obs.Progress(wi); ok {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never became monitored", wi)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// DirectShutdownCause is the shutdown cause of a direct in-process
// Shutdown call (the tests).
func DirectShutdownCause() string { return "direct: in-process Shutdown call" }

// ---------------------------------------------------------------------------
// polyforge#224 — the demand-gated summarize lane's test seams.

// w224Dispatcher is the summarize lane's fake dispatch seam for the
// in-process daemon tests: it records every call and answers per
// candidate (nil answer = a plain line). block, when non-nil, keeps a
// call in flight until the test closes it.
type w224Dispatcher struct {
	mu     sync.Mutex
	calls  []string
	answer func(candidate string) (string, error)
	block  chan struct{}
}

func (f *w224Dispatcher) Dispatch(candidate, _ string) (string, error) {
	// the call is recorded BEFORE the optional block: a blocked call is
	// started, not yet landed — the tests count starts.
	f.mu.Lock()
	f.calls = append(f.calls, candidate)
	f.mu.Unlock()
	if f.block != nil {
		<-f.block
	}
	if f.answer == nil {
		return "w224: the agent is working", nil
	}
	return f.answer(candidate)
}

func (f *w224Dispatcher) n() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// w224SessionDir answers the capsule's own pi session directory under
// the harness session store — the directory the spawner hands the step
// process as PI_CODING_AGENT_SESSION_DIR and the shared locator scans.
// The dashified-name rule's home is runtime/pitranscript.go
// (piSessionDirName, unexported); it is replicated here because the
// rule is the runtime's own — TestSpawnPiSessionDirIsWhereTheLocatorLooks
// pins the two sides together, so a rule change breaks this loudly.
func w224SessionDir(storeRoot, capsuleDir string) string {
	real := capsuleDir
	if resolved, err := filepath.EvalSymlinks(capsuleDir); err == nil {
		real = resolved
	}
	trimmed := strings.TrimPrefix(real, "/")
	dashed := strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == ':' {
			return '-'
		}
		return r
	}, trimmed)
	return filepath.Join(storeRoot, "--"+dashed+"--")
}

// w224Rig is a daemon with the summarize lane's fake dispatch seam and
// one claimed, live work item whose current dispatch's live transcript
// is seeded in the harness session store (the store the shared locator
// scans — the same place a real pi writes its sessions).
type w224Rig struct {
	td         *testDaemon
	disp       *w224Dispatcher
	wi         string
	capDir     string
	transcript string // the seeded store file (append to grow)
}

// newW224Rig claims one work item under a workspace yaml whose root
// summarize key names models (nil = no key: the lane is off), and seeds
// the current dispatch's live transcript in the harness session store.
func newW224Rig(t *testing.T, models []string) *w224Rig {
	t.Helper()
	disp := &w224Dispatcher{}
	td := newTestDaemon(t, func(c *Config) { c.SummarizeDispatcher = disp })
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	body := "# w224 rig yaml\n"
	if models == nil {
		body += "steps:\n  proj:\n    default: { models: [pi:prov/weak-a] }\n"
	} else {
		body += "summarize: { models: [" + strings.Join(models, ", ") + "] }\n"
	}
	if err := os.WriteFile(filepath.Join(ws, workspace.FileName), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	td.submitClaim(t, "wi_w224")
	ji, ok := td.rt.Info("wi_w224")
	if !ok || ji.CapsuleDir == "" {
		t.Fatal("the claim must run to a live job with a capsule")
	}
	// the spawn journal's latest entry — the current dispatch the route
	// resolves. The fake engine bypasses the driver's journal callback
	// (fakeEngineStarter hands the spawner no Journal), so the rig writes
	// the entry itself: a pi dispatch born now, the token the lane keys
	// its reset on.
	journal := filepath.Join(ji.CapsuleDir, capsule.DirRuntimeState, capsule.SpawnJournalFile)
	if err := os.MkdirAll(filepath.Dir(journal), 0o755); err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC()
	entry := capsule.SpawnJournalEntry{
		At:            started.Format(time.RFC3339Nano),
		WorkItem:      "wi_w224",
		StepID:        "fix",
		SpawnToken:    "w224-rig#1",
		PID:           os.Getpid(),
		StartedAt:     started.Format(time.RFC3339Nano),
		SessionHandle: "w224rig",
		SpawnN:        1,
		Transcript:    filepath.Join(ji.CapsuleDir, capsule.DirSession, "session-w224rig.jsonl"),
	}
	blob, err := json.Marshal(entry)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(journal, append(blob, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
	// the execution record the A11 gate reads (the driver's own
	// single-writer authority on step state; the fake engine writes none —
	// the rig scripts it as it scripts the journal): the current step
	// in_progress, not parked on a question — a LIVE RUNNING STEP
	// DISPATCH's attempt half. w224WriteExecution mutates it into the
	// parked and other shapes.
	w224WriteExecution(t, ji.CapsuleDir, capsule.StepInProgress, "")
	// the current dispatch's live transcript, seeded in the harness
	// session store — the store the shared per-dispatch rule scans for a
	// fresh dispatch
	store := filepath.Join(td.cfg.HarnessRoot, runtime.StepAgentSessionsDirName)
	dir := w224SessionDir(store, ji.CapsuleDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tr := filepath.Join(dir, "2026-10-20T12-00-00-000000Z_w224.jsonl")
	if err := os.WriteFile(tr, []byte("{\"t\":\"turn\",\"text\":\"hello\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return &w224Rig{td: td, disp: disp, wi: "wi_w224", capDir: ji.CapsuleDir, transcript: tr}
}

// grow appends one record to the seeded transcript (the growth lever).
func (r *w224Rig) grow(t *testing.T, text string) {
	t.Helper()
	f, err := os.OpenFile(r.transcript, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("{\"t\":\"turn\",\"text\":\"" + text + "\"}\n"); err != nil {
		t.Fatal(err)
	}
	f.Close()
}

// w224WriteExecution writes the capsule's execution record: the current
// step "fix" in the given status, optionally parked on a question (the
// needs_input shape — the driver parks the step with a pending question
// in the same write that records it). The rig's default shape is
// in_progress with no question (a live running step attempt).
func w224WriteExecution(t *testing.T, capDir, stepStatus, questionID string) {
	t.Helper()
	step := &capsule.Step{Status: stepStatus}
	if questionID != "" {
		step.PendingQuestion = &capsule.PendingQuestion{
			ID: questionID, Step: "fix", Question: "w224: parked",
			AskedAt: time.Now().UTC().Format(time.RFC3339Nano),
		}
	}
	ex := capsule.Execution{
		WorkflowStatus: capsule.WorkflowStatusRunning, CurrentStep: "fix",
		Steps:     map[string]*capsule.Step{"fix": step},
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Version:   1,
	}
	blob, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(capDir, capsule.ExecutionJSON), blob, 0o644); err != nil {
		t.Fatal(err)
	}
}

// w224DemandChild runs the w224 demand helper as a REAL child process
// dialing the daemon's UDS face with demand=1 — the #122 seam: the
// kernel's SO_PEERCRED names exactly the peer the demand gate judges.
// env hands the child its step markers (PF_WI_ID + PF_DAEMON_ID of the
// TEST daemon = a step process tree of it); without them the child is
// a human-side caller (markers of another daemon — e.g. this very test
// running inside a real step — never count). It answers the HTTP status
// and the response body.
func w224DemandChild(t *testing.T, td *testDaemon, wi string, env ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestW224DemandHelperProcess$", "-test.count=1")
	cmd.Env = append(os.Environ(), append([]string{"W224_SOCK=" + td.sock, "W224_WI=" + wi}, env...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the demand child: %v\n%s", err, out)
	}
	s := strings.TrimSpace(string(out))
	const pfx = "W224-STATUS "
	i := strings.Index(s, pfx)
	if i < 0 {
		t.Fatalf("the demand child printed no status: %q", s)
	}
	rest := s[i+len(pfx):]
	sp := strings.Index(rest, " ")
	if sp < 0 {
		t.Fatalf("the demand child printed no code: %q", rest)
	}
	code, err := strconv.Atoi(rest[:sp])
	if err != nil {
		t.Fatalf("the demand child's status line is malformed: %q", rest)
	}
	// the body ends at the first newline: the helper's own Printf ends
	// the line, and the test binary's trailing output (PASS) follows
	body := rest[sp+1:]
	if nl := strings.Index(body, "\n"); nl >= 0 {
		body = body[:nl]
	}
	return code, body
}

// w224Snap issues one plain (unmarked) in-process snapshot fetch.
func w224Snap(t *testing.T, td *testDaemon, wi string) protocol.WatchSnapshot {
	t.Helper()
	snap, err := td.client.WatchSnapshot(wi, 0, false)
	if err != nil {
		t.Fatalf("the plain snapshot fetch failed: %v", err)
	}
	return *snap
}

// w224AwaitSummary polls plain fetches until a summary or an error lands.
func w224AwaitSummary(t *testing.T, td *testDaemon, wi string, bound time.Duration) protocol.WatchSnapshot {
	t.Helper()
	deadline := time.Now().Add(bound)
	var last protocol.WatchSnapshot
	for time.Now().Before(deadline) {
		last = w224Snap(t, td, wi)
		if last.Summary != nil || last.SummaryError != nil {
			return last
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("no summarize fact ever landed for %s within %v (last: %+v)", wi, bound, last)
	return last
}
