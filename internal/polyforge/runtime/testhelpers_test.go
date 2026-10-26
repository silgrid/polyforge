package runtime

// Shared test scaffolding: a scripted aihub, a scripted spawner that spawns
// REAL (trivial) child processes so death/kill semantics are exercised
// honestly, and small builders for jobs and capsule git repos.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/atomicfile"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// ---------------------------------------------------------------------------
// fake aihub (v1.5 session model): a tiny in-memory hub. `held` maps a
// canonical wi → the session SECRET currently holding it ("" = unheld);
// SessionVerify compares against it, SessionClaim installs,
// SessionEnd clears (terminal) or keeps (paused) it, SessionAttach
// re-opens a paused+not-clean-pause row. `status` scripts the WI status
// verify answers; `lookupErr` scripts outages per wi.

type fakeHub struct {
	mu         sync.Mutex
	status     map[string]string
	held       map[string]string // wi → session secret ("" = unheld)
	cleanPause map[string]bool
	lookupErr  map[string]error
	claims     []api.SessionClaimRequest
	attaches   []api.SessionAttachRequest
	ends       []api.SessionEndRequest
	verifies   map[string]int    // canonical wi → answered SessionVerify calls
	end        map[string]error  // wi → end-error scripting (per wi)
	repos      []api.ProjectRepo // claim-served task-repo inventory (nil = none)
	// reposFor overrides repos per work item (canonical id).
	reposFor map[string][]api.ProjectRepo
	// canonical maps an id-or-slug form (e.g. "proj#7") to the canonical
	// work-item id the claim answer carries (F2 tests); nil entry = the
	// input echoes itself (the pre-F2 behavior).
	canonical map[string]string
	items     map[string]*api.WorkItem // canonical id → claim answer record (F1 tests)
	// capsuleRepoBase, when set, makes SessionClaim materialize the EMPTY
	// bare capsule repo aihub creates at claim (the production contract):
	// the runtime's claim-time clone path has a remote to clone. The
	// runtime's no-remote local init is a typed refusal (there is no
	// production local-init path).
	capsuleRepoBase string
	// claimFaults scripts SessionClaim failures, consumed in order: each
	// answers its err, after committing the claim when afterCommit is
	// set (the answer-lost-in-transit shape, repo review B-1).
	claimFaults []claimFault
	// endBlock, when non-nil, parks every SessionEnd until it is closed
	// (a hung hub).
	endBlock chan struct{}
	// endBlockFor parks the SessionEnds of one work item (canonical id)
	// until its channel is closed.
	endBlockFor map[string]chan struct{}
	// gates parks one kind of hub call ("probe", "verify", "claim",
	// "attach", "end") for one work item (canonical id) until released — a hung
	// hub for that call alone (installed with hangCall).
	gates map[string]*hubGate
	// machine records the claiming machine of each work item's session
	// and seals its blocked seal (aihub attrs.blocked_seal): a blocked
	// SessionEnd with a head records {head, session machine}, a blocked
	// one without keeps the standing seal, any other verdict clears it;
	// the claim answer carries it.
	machine map[string]string
	seals   map[string]api.SessionClaim
}

// hubGate is one parked hub call: entered closes once a call parked,
// release lets every parked (and later) call through.
type hubGate struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	rel     sync.Once
}

// hangCall installs a gate for kind calls of wi; the test waits on
// entered and releases it (released at cleanup at the latest).
func (h *fakeHub) hangCall(t *testing.T, kind, wi string) *hubGate {
	t.Helper()
	g := &hubGate{entered: make(chan struct{}), release: make(chan struct{})}
	h.mu.Lock()
	if h.gates == nil {
		h.gates = map[string]*hubGate{}
	}
	h.gates[kind+":"+wi] = g
	h.mu.Unlock()
	t.Cleanup(g.open)
	return g
}

func (g *hubGate) open() { g.rel.Do(func() { close(g.release) }) }

// waitEntered blocks until a call parked at the gate.
func (g *hubGate) waitEntered(t *testing.T, what string) {
	t.Helper()
	select {
	case <-g.entered:
	case <-time.After(20 * time.Second):
		t.Fatalf("%s never reached the hub", what)
	}
}

// hangCallAs is hangCall for the calls naming wi under exactly this
// spelling (a slug's probe, not the canonical id's).
func (h *fakeHub) hangCallAs(t *testing.T, kind, spelling string) *hubGate {
	t.Helper()
	g := &hubGate{entered: make(chan struct{}), release: make(chan struct{})}
	h.mu.Lock()
	if h.gates == nil {
		h.gates = map[string]*hubGate{}
	}
	h.gates["as:"+kind+":"+spelling] = g
	h.mu.Unlock()
	t.Cleanup(g.open)
	return g
}

// park holds a kind call of idOrSlug while its gate is installed and
// closed (called before the fake takes its own lock).
func (h *fakeHub) park(kind, idOrSlug string) {
	h.mu.Lock()
	g := h.gates["as:"+kind+":"+idOrSlug]
	if g == nil {
		g = h.gates[kind+":"+h.canonicalID(idOrSlug)]
	}
	h.mu.Unlock()
	if g == nil {
		return
	}
	g.once.Do(func() { close(g.entered) })
	<-g.release
}

type claimFault struct {
	err         error
	afterCommit bool
}

func newFakeHub() *fakeHub {
	return &fakeHub{
		status:     map[string]string{},
		held:       map[string]string{},
		cleanPause: map[string]bool{},
		lookupErr:  map[string]error{},
		end:        map[string]error{},
		machine:    map[string]string{},
		seals:      map[string]api.SessionClaim{},
		verifies:   map[string]int{},
	}
}

// hold scripts the hub: wi is held by sessionID in the given status.
func (h *fakeHub) hold(wi, sessionID, status string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.held[wi] = sessionID
	if status != "" {
		h.status[wi] = status
	}
}

func (h *fakeHub) canonicalID(idOrSlug string) string {
	if c, ok := h.canonical[idOrSlug]; ok {
		return c
	}
	return idOrSlug
}

func (h *fakeHub) itemFor(id string) api.WorkItem {
	item := api.WorkItem{ID: id, ProjectName: "proj"}
	if h.items != nil {
		if it, ok := h.items[id]; ok && it != nil {
			item = *it
		}
	}
	return item
}

func (h *fakeHub) claimResponse(wi, sessionID string) *api.SessionClaimResponse {
	repos := h.repos
	if rs, ok := h.reposFor[wi]; ok {
		repos = rs
	}
	return &api.SessionClaimResponse{
		Claim: api.SessionClaim{
			WorkItemID:         wi,
			SessionFingerprint: api.SessionFingerprint(sessionID),
			BundleName:         "demo-bundle",
			Repos:              repos,
			SealedHead:         h.seals[wi].SealedHead,
			SealedMachine:      h.seals[wi].SealedMachine,
		},
		WorkItem: h.itemFor(wi),
	}
}

func (h *fakeHub) SessionClaim(req api.SessionClaimRequest) (*api.SessionClaimResponse, error) {
	h.park("claim", req.WorkItemID)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.claims = append(h.claims, req)
	var fault *claimFault
	if len(h.claimFaults) > 0 {
		f := h.claimFaults[0]
		h.claimFaults = h.claimFaults[1:]
		fault = &f
		if !f.afterCommit {
			return nil, f.err
		}
	}
	// The aihub claim contract: aihub creates the bare repo at claim. The
	// fake hub materializes it when the test wired capsuleRepoBase, so
	// the runtime's clone path has a remote.
	if h.capsuleRepoBase != "" {
		id := h.canonicalID(req.WorkItemID)
		bare := filepath.Join(h.capsuleRepoBase, id+".git")
		if _, err := os.Stat(bare); err != nil {
			if out, cerr := exec.Command("git", "init", "--bare", "-q", bare).CombinedOutput(); cerr != nil {
				return nil, &api.Error{Code: "CORE-500", Detail: fmt.Sprintf("bare capsule repo %s: %v: %s", bare, cerr, out)}
			}
		}
	}
	// the holder's replay (the aihub contract, D4): the session that
	// holds the WI is answered the same claim again
	id := h.canonicalID(req.WorkItemID)
	if cur := h.held[id]; cur != "" && cur != req.SessionID {
		return nil, &api.Error{Code: "CORE-409", Detail: "ALREADY_HELD"}
	}
	resp := h.claimResponse(id, req.SessionID)
	h.held[id] = req.SessionID
	h.machine[id] = req.MachineID
	h.status[id] = api.StatusRunning
	if fault != nil {
		return nil, fault.err // committed, answer lost
	}
	return resp, nil
}

// forceEnd scripts the aihub-side force-end-session recovery door: the
// held session is dropped and the WI returns to queued, so the next plain
// claim succeeds.
func (h *fakeHub) forceEnd(wi string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.canonicalID(wi)
	h.held[id] = ""
	h.status[id] = api.StatusQueued
	h.cleanPause[id] = false
}

func (h *fakeHub) SessionAttach(wi string, req api.SessionAttachRequest) (*api.SessionClaimResponse, error) {
	h.park("attach", wi)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.attaches = append(h.attaches, req)
	id := h.canonicalID(wi)
	if err, ok := h.lookupErr[id]; ok {
		return nil, err
	}
	if h.held[id] != req.SessionID {
		return nil, &api.Error{Code: "CORE-403", Detail: "STALE_SESSION"}
	}
	if h.status[id] != api.StatusPaused || h.cleanPause[id] {
		return nil, &api.Error{Code: "CORE-409", Detail: "NOT_ATTACHABLE"}
	}
	h.status[id] = api.StatusRunning
	return h.claimResponse(id, req.SessionID), nil
}

func (h *fakeHub) SessionVerify(wi, sessionID string) (*api.SessionVerifyResponse, error) {
	h.park("verify", wi)
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.canonicalID(wi)
	if err, ok := h.lookupErr[id]; ok {
		return nil, err
	}
	h.verifies[id]++
	st := h.status[id]
	if st == "" {
		st = api.StatusRunning
	}
	out := &api.SessionVerifyResponse{WorkItemID: id, Status: st, CleanPause: h.cleanPause[id]}
	if h.held[id] == sessionID && sessionID != "" {
		out.Matches = true
	} else if h.held[id] != "" {
		out.CurrentFingerprint = api.SessionFingerprint(h.held[id])
	}
	return out, nil
}

func (h *fakeHub) SessionEnd(wi string, req api.SessionEndRequest) error {
	h.park("end", wi)
	h.mu.Lock()
	block := h.endBlock
	blockWI := h.endBlockFor[h.canonicalID(wi)]
	h.mu.Unlock()
	if block != nil {
		<-block
	}
	if blockWI != nil {
		<-blockWI
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.canonicalID(wi)
	if err, ok := h.lookupErr[id]; ok {
		return err
	}
	if err, ok := h.end[id]; ok {
		return err
	}
	// fenced: another session's row is not ours to end
	if cur := h.held[id]; cur != "" && cur != req.SessionID {
		return &api.Error{Code: "CORE-403", Detail: "STALE_SESSION"}
	}
	h.ends = append(h.ends, req)
	switch req.Status {
	case api.StatusWrapped, api.StatusFailed, api.StatusBlocked, api.StatusCanceled:
		h.held[id] = "" // terminal + blocked clear the session (M2)
	default:
		// paused keeps the session (the design's paused-with-session)
	}
	switch {
	case req.Status == api.StatusBlocked && req.HeadSHA != "":
		h.seals[id] = api.SessionClaim{SealedHead: req.HeadSHA, SealedMachine: h.machine[id]}
	case req.Status != api.StatusBlocked:
		delete(h.seals, id)
	}
	h.status[id] = req.Status
	return nil
}

func (h *fakeHub) GetWorkItem(idOrSlug string) (*api.WorkItem, error) {
	h.park("probe", idOrSlug)
	h.mu.Lock()
	defer h.mu.Unlock()
	id := h.canonicalID(idOrSlug)
	if err, ok := h.lookupErr[id]; ok {
		return nil, err
	}
	st := h.status[id]
	if st == "" {
		st = api.StatusRunning
	}
	item := h.itemFor(id)
	item.Status = st
	return &item, nil
}

// verifyCount is the number of SessionVerify calls answered for wi.
func (h *fakeHub) verifyCount(wi string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.verifies[h.canonicalID(wi)]
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
	return h.ends[len(h.ends)-1]
}

func (h *fakeHub) claimCount() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.claims)
}

// ---------------------------------------------------------------------------
// scripted spawner: records every call and spawns a real `sleep` child as
// the "agent" (own process group, PF_WI_ID marker env) so kill semantics
// are real.

type scriptedSpawner struct {
	mu         sync.Mutex
	calls      []SpawnParams
	failResume int
	failFresh  int
}

func (s *scriptedSpawner) Spawn(ctx context.Context, p SpawnParams) (AgentHandle, error) {
	s.mu.Lock()
	s.calls = append(s.calls, p)
	fr, ff := s.failResume, s.failFresh
	if p.Resume && fr > 0 {
		s.failResume--
		s.mu.Unlock()
		return nil, fmt.Errorf("scripted resume spawn failure")
	}
	if !p.Resume && ff > 0 {
		s.failFresh--
		s.mu.Unlock()
		return nil, fmt.Errorf("scripted fresh spawn failure")
	}
	s.mu.Unlock()

	cmd := exec.Command("sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), "PF_WI_ID="+p.WorkItemID, "PF_DAEMON_ID=m-test", testOwnerEnv)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	trackTestGroup(cmd.Process.Pid)
	// fidelity with the production spawner: the identity journal runs
	// before Spawn answers (its failure tears the fresh process down).
	if p.Journal != nil {
		startedAt := nowStamp()
		if jerr := p.Journal(cmd.Process.Pid, startedAt); jerr != nil {
			_ = killGroup(cmd.Process.Pid, cleanupBound)
			return nil, fmt.Errorf("scripted spawn journal failed: %w", jerr)
		}
	}
	return newProc(cmd, stdin, stdout), nil
}

func (s *scriptedSpawner) callCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.calls)
}

func (s *scriptedSpawner) call(i int) SpawnParams {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls[i]
}

// ---------------------------------------------------------------------------
// builders

func setGitIdentity(t *testing.T) {
	t.Helper()
	t.Setenv("GIT_AUTHOR_NAME", "pf-test")
	t.Setenv("GIT_AUTHOR_EMAIL", "pf-test@invalid")
	t.Setenv("GIT_COMMITTER_NAME", "pf-test")
	t.Setenv("GIT_COMMITTER_EMAIL", "pf-test@invalid")
}

// mappingFor builds the tests' one-workspace plan: the given workspace
// mapping the named projects (default "proj" — the fakes' project).
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

// testWorkspace answers the tests' single mapped workspace (project
// "proj") — the unseal/quarantine probes' base.
func testWorkspace(r *Runtime) string {
	ws, err := r.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		panic(err)
	}
	return ws
}

// testCapsuleDir derives the capsule dir of wi under the test runtime's
// mapping (the tests' single project "proj") — the unit replacement for
// the deleted single-workspace capsuleDir.
func testCapsuleDir(r *Runtime, wi string) string {
	ws, err := r.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		panic(err)
	}
	return filepath.Join(ws, "capsules", wi)
}

func newTestRuntime(t *testing.T, hub Aihub, sp Spawner) (*Runtime, *supervise.Queue[protocol.RTMsg], *supervise.Queue[protocol.OBMsg]) {
	t.Helper()
	cfg := Config{
		MachineID: "m-test",
		Mapping:   mappingFor(filepath.Join(t.TempDir(), "ws")),
		DataDir:   filepath.Join(t.TempDir(), "data"),
		// polyforge#106: the driver is the only engine — the rig scripts it
		// through the single engine seam (a derivation is a Spawn call).
		AgentStarter:  scriptedEngineStarter(sp),
		MaxRecoveries: 2,
		// Test episodes are separated by handleOB calls, not real time:
		// the entry-rate storm guard's window is collapsed to nothing so
		// every recovery models a fresh episode (the storm tests override
		// this with a real window to drive the guard itself).
	}
	toOB := supervise.NewQueue[protocol.RTMsg]()
	fromOB := supervise.NewQueue[protocol.OBMsg]()
	r := New(cfg, hub, sp, toOB, fromOB)
	// Before the temp dirs go, stop the retries and let an operation still
	// under way finish its message — bounded, a hung one is the test's own
	// business.
	t.Cleanup(func() { r.Close(); settleOps(t, r, 10*time.Second) })
	// The claim-time capsule remote: aihub creates the bare repo at claim
	// (the fake hub implements the contract; the runtime's no-remote
	// local init is a typed refusal).
	if fh, ok := hub.(*fakeHub); ok {
		fh.capsuleRepoBase = filepath.Join(r.cfg.DataDir, "capsule-remotes")
		r.cfg.GitRemoteBase = fh.capsuleRepoBase
	}
	return r, toOB, fromOB
}

// settleOps waits, at most bound, until no work item's actor has work
// and no claim sits CLAIMING; an operation still under way at the bound
// fails the test.
func settleOps(t testing.TB, r *Runtime, bound time.Duration) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		r.mu.Lock()
		idle := len(r.boxes) == 0
		for _, j := range r.jobs {
			if st, _ := j.snapshot(); st == StateClaiming {
				idle = false
			}
		}
		r.mu.Unlock()
		if idle {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Errorf("an operation was still under way %s later (an actor busy or a claim CLAIMING)", bound)
}

// fakeSession mints a deterministic per-WI session SECRET for tests
// (64-hex, the real shape — sha256 of the wi id).
func fakeSession(wi string) string {
	sum := sha256.Sum256([]byte("test-session:" + wi))
	return hex.EncodeToString(sum[:])
}

// scriptedEngineStarter binds the runtime's single engine seam to the
// test's spawner (polyforge#106: the driver is the only engine, so the
// in-process rig scripts it — a derivation is one Spawn call, the same
// shape the daemon tests' fake engine uses).
func scriptedEngineStarter(sp Spawner) AgentStartFunc {
	return func(ctx context.Context, in AgentStartInput) (AgentHandle, error) {
		handle := in.SessionHandle
		p := SpawnParams{
			WorkItemID:    in.WI,
			CapsuleDir:    in.CapsuleDir,
			SessionHandle: handle,
			SpawnN:        int(in.SpawnN),
			InitialPrompt: "engine",
		}
		h, err := sp.Spawn(ctx, p)
		if err != nil {
			return nil, err
		}
		// Like the real engine, journal the derivation: the capsule spawn
		// journal is the B2 fence ledger every derivation appends to.
		token := fmt.Sprintf("%s#%d", handle, in.SpawnN-1)
		if jerr := writeSpawnJournal(p, h.PID(), h.StartedAt(), token); jerr != nil {
			fmt.Fprintf(os.Stderr, "runtime: %s: scripted engine journal: %v\n", in.WI, jerr)
		}
		return h, nil
	}
}

func seedJob(r *Runtime, wi string) *job {
	capDir := testCapsuleDir(r, wi)
	_ = os.MkdirAll(capDir, 0o755)
	sess := fakeSession(wi)
	fp := api.SessionFingerprint(sess)
	j := &job{
		wi:                 wi,
		project:            "proj",
		sessionID:          sess,
		sessionFingerprint: fp,
		state:              StateRunning,
		capsuleDir:         capDir,
		git:                gitops.Opts{WorkItemID: wi, Username: wi, Secret: sess, CapsuleDir: capDir, GitDir: filepath.Join(r.cfg.DataDir, "git", wi+".git")},
		transcript:         sessionPathFor(capDir, fp),
		bundleName:         "demo-bundle",
		spawnN:             1,
		verified:           true, // a live claimed job
	}
	r.mu.Lock()
	r.jobs[wi] = j
	r.mu.Unlock()
	return j
}

func installOldAgent(t *testing.T, sp Spawner, j *job) AgentHandle {
	t.Helper()
	h, err := sp.Spawn(context.Background(), SpawnParams{
		WorkItemID:     j.wi,
		CapsuleDir:     j.capsuleDir,
		SessionHandle:  j.sessionFingerprint,
		SpawnN:         j.spawnN + 1,
		TranscriptPath: j.transcript,
		InitialPrompt:  "initial",
	})
	if err != nil {
		t.Fatalf("installOldAgent: %v", err)
	}
	j.mu.Lock()
	j.agent = h
	j.agentPID = h.PID()
	j.agentStartedAt = h.StartedAt()
	j.mu.Unlock()
	return h
}

func writeTranscript(t *testing.T, j *job) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(j.transcript), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(j.transcript, []byte("{\"type\":\"message_end\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// initGitCapsule gives j a real capsule worktree with an external git-dir
// and a local bare repo as its push remote, so CommitPush succeeds. The
// bare advertises push options: every push from the gitops choke point
// carries --push-option=attempt-id=… (§六 备层).
func initGitCapsule(t *testing.T, j *job) string {
	t.Helper()
	setGitIdentity(t)
	bare := filepath.Join(t.TempDir(), "hub.git")
	if out, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "--git-dir", bare, "config", "receive.advertisePushOptions", "true").CombinedOutput(); err != nil {
		t.Fatalf("git config advertisePushOptions: %v: %s", err, out)
	}
	if err := os.MkdirAll(j.git.CapsuleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	j.git.RemoteURL = bare
	j.git.Username = j.wi
	j.git.Secret = "push-secret"
	if err := os.MkdirAll(filepath.Dir(j.git.GitDir), 0o755); err != nil {
		t.Fatal(err)
	}
	// the production layout (gitops.Clone's): external git-dir, .git
	// pointer, working branch main
	if out, err := exec.Command("git", "init", "--initial-branch=main", "--separate-git-dir="+j.git.GitDir, j.git.CapsuleDir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	// one initial commit so rev-list/push have a HEAD to stand on
	if err := os.WriteFile(filepath.Join(j.capsuleDir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("git", "commit", "--allow-empty", "-m", "init")
	cmd.Env = j.git.Env()
	cmd.Dir = j.capsuleDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("initial commit: %v: %s", err, out)
	}
	return bare
}

func bareHasMain(t *testing.T, bare string) bool {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", bare, "rev-parse", "--verify", "refs/heads/main").Output()
	return err == nil && len(out) > 0
}

// ---------------------------------------------------------------------------
// assertions

func recvRT(t *testing.T, q *supervise.Queue[protocol.RTMsg]) protocol.RTMsg {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		if m, ok := q.Pop(); ok {
			return m
		}
		select {
		case <-q.Notify():
		case <-deadline:
			t.Fatal("timed out waiting for an RT message")
			return protocol.RTMsg{}
		}
	}
}

func assertNoRT(t *testing.T, q *supervise.Queue[protocol.RTMsg]) {
	t.Helper()
	if m, ok := q.Pop(); ok {
		t.Fatalf("unexpected RT message: %+v", m)
	}
}

func waitDead(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for procExists(pid) {
		if time.Now().After(deadline) {
			t.Fatalf("pid %d still alive", pid)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func mustBeAlive(t *testing.T, pid int) {
	t.Helper()
	if !procExists(pid) {
		t.Fatalf("pid %d unexpectedly dead", pid)
	}
}

// unseal re-writable bits so t.TempDir cleanup does not fight a sealed
// scene.
func unseal(t *testing.T, root string) {
	t.Helper()
	_ = filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			_ = os.Chmod(p, 0o755)
		} else if info.Mode().IsRegular() {
			_ = os.Chmod(p, 0o644)
		}
		return nil
	})
}

// spawnSleeper starts a plain sleeper as its own group leader (with the
// WI's env marker) without going through the scripted spawner, so restart
// tests can count real derivations.
func spawnSleeper(t *testing.T, wi string) (pid int, startedAt string) {
	t.Helper()
	cmd := exec.Command("sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), "PF_WI_ID="+wi, "PF_DAEMON_ID=m-test", testOwnerEnv)
	startedAt = nowStamp()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	trackTestGroup(cmd.Process.Pid)
	return cmd.Process.Pid, startedAt
}

// testGroups records the process groups the test helpers start: a test
// that ends on any path (a failure, a kill seam swapped for a fake, a
// handle nobody terminated) must not leave its `sleep 300` behind.
// reapTestGroups runs at the end of the package run (TestMain) and
// SIGKILLs, process by process, only members of a recorded group that
// still carry this run's testOwnerEnv marker — a pgid reused by an
// unrelated process since is never signalled (syscall.Kill directly,
// never the swappable killFn; /proc read directly, never procRoot).
var testGroups struct {
	mu    sync.Mutex
	pgids []int
}

// testOwnerEnv marks every process the helpers start with this test
// binary's pid.
var testOwnerEnv = "PF_TEST_OWNER=" + strconv.Itoa(os.Getpid())

func trackTestGroup(pgid int) {
	testGroups.mu.Lock()
	testGroups.pgids = append(testGroups.pgids, pgid)
	testGroups.mu.Unlock()
}

func reapTestGroups() error {
	testGroups.mu.Lock()
	defer testGroups.mu.Unlock()
	want := map[int]bool{}
	for _, pg := range testGroups.pgids {
		want[pg] = true
	}
	testGroups.pgids = nil
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil
	}
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 1 {
			continue
		}
		stat, err := os.ReadFile("/proc/" + e.Name() + "/stat")
		if err != nil {
			continue
		}
		// fields after the parenthesised comm: state ppid pgrp ...
		rest := string(stat)
		if i := strings.LastIndexByte(rest, ')'); i >= 0 {
			rest = rest[i+1:]
		}
		f := strings.Fields(rest)
		if len(f) < 3 || f[0] == "Z" {
			continue
		}
		pg, _ := strconv.Atoi(f[2])
		if !want[pg] {
			continue
		}
		env, _ := os.ReadFile("/proc/" + e.Name() + "/environ")
		for _, kv := range strings.Split(string(env), "\x00") {
			if kv == testOwnerEnv {
				_ = syscall.Kill(pid, syscall.SIGKILL)
				break
			}
		}
	}
	return nil
}

func writeStateFile(t *testing.T, r *Runtime, recs []*jobRecord) {
	t.Helper()
	blob, err := json.MarshalIndent(&stateFile{Version: 1, Jobs: recs}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := atomicfile.Write(r.statePath, blob, stateFilePerm); err != nil {
		t.Fatal(err)
	}
}

func loadRecords(t *testing.T, r *Runtime) []*jobRecord {
	t.Helper()
	st, err := loadState(r.statePath)
	if err != nil {
		t.Fatal(err)
	}
	return st.Jobs
}

var errLookupBoom = fmt.Errorf("aihub unreachable (scripted)")

func timeAfter() <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		time.Sleep(5 * time.Second)
		close(ch)
	}()
	return ch
}

// orphaningAgent is an "agent" whose leader dies immediately after forking
// a long-lived child: the child survives in the leader's process group as
// the orphan the six-layer cleanup must still collect.
type orphaningAgent struct {
	handle    AgentHandle
	orphanPID int
}

func spawnOrphaningSleeper(t *testing.T, wi string) (*orphaningAgent, error) {
	pidFile := filepath.Join(t.TempDir(), "child.pid")
	cmd := exec.Command("sh", "-c", "sleep 300 & echo $! > "+pidFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), "PF_WI_ID="+wi, "PF_DAEMON_ID=m-test", testOwnerEnv)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	trackTestGroup(cmd.Process.Pid) // the orphan stays in the leader's group
	h := newProc(cmd, stdin, stdout)
	// the leader exits on its own; wait for it and read the orphan's pid
	deadline := time.Now().Add(5 * time.Second)
	for !h.Done() {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("orphaning leader did not exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		return nil, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return nil, err
	}
	return &orphaningAgent{handle: h, orphanPID: pid}, nil
}

// writeSpawnJournal appends one journal line in the driver adapter's
// shape (the production writer lives in the driver; tests only need the
// ledger it leaves behind).
func writeSpawnJournal(p SpawnParams, pid int, startedAt string, spawnToken string) error {
	blob, err := json.Marshal(map[string]any{
		"at":             nowStamp(),
		"work_item":      p.WorkItemID,
		"spawn_token":    spawnToken,
		"pid":            pid,
		"started_at":     startedAt,
		"session_handle": p.SessionHandle,
		"spawn_n":        p.SpawnN,
		"resume":         p.Resume,
		"transcript":     p.TranscriptPath,
	})
	if err != nil {
		return err
	}
	path := filepath.Join(p.CapsuleDir, capsule.DirRuntimeState, capsule.SpawnJournalFile)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(blob, '\n'))
	return err
}

func newProc(cmd *exec.Cmd, stdin io.WriteCloser, stdout io.ReadCloser, taps ...func(string)) *proc {
	return newProcOpts(cmd, stdin, stdout, procOpts{taps: taps})
}

// underLock runs fn as a message of wi's actor, as the runtime's own
// doors run the job operations, and waits for it.
func underLock(r *Runtime, wi string, fn func()) {
	_ = r.call(context.Background(), wi, func() error { fn(); return nil })
}

// settleReplays waits for the restart's journal replays (and every other
// operation under way) to settle.
func settleReplays(t testing.TB, r *Runtime) { settleOps(t, r, 20*time.Second) }

// replayClaims runs the claiming-journal replay as Restart does — a
// CLAIMING placeholder per entry with no job, each reconciled by its
// actor — and waits for every replay to settle.
func replayClaims(t testing.TB, r *Runtime) {
	t.Helper()
	for id, e := range r.loadClaiming() {
		if r.jobOf(id) == nil {
			r.putJob(&job{wi: id, sessionID: e.SessionID, state: StateClaiming})
		}
		r.post(id, func() { r.reconcile(id) })
	}
	settleReplays(t, r)
}

// actorBusy reports whether wi's actor has work (a message running or
// queued).
func actorBusy(r *Runtime, wi string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.boxes[wi] != nil
}

// restartSettled runs Restart and waits until every actor it started
// settled — the reconciliation as one step, for the tests that assert its
// outcome.
func restartSettled(t testing.TB, r *Runtime) error {
	t.Helper()
	t.Cleanup(r.Close) // no retry of this runtime outlives the test
	err := r.Restart(context.Background())
	settleOps(t, r, 20*time.Second)
	return err
}

// handleOB applies one OB message through its work item's actor and
// waits for it.
func (r *Runtime) handleOB(_ context.Context, msg protocol.OBMsg) {
	_ = r.call(context.Background(), msg.WorkItem, func() error { r.applyOB(msg); return nil })
}

// recoverOp runs one recovery of j's current derivation through its work
// item's actor (a job that settled meanwhile, or left RUNNING, drops it).
func (r *Runtime) recoverOp(_ context.Context, j *job, cause, detail string) {
	_ = r.call(context.Background(), j.wi, func() error {
		if r.jobOf(j.wi) == j && stateOf(j) == StateRunning {
			r.recoverCore(r.ctx, j, cause, detail)
		}
		return nil
	})
}
