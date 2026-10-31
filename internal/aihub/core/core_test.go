package core

// Core tests: CRUD, terminal transitions, ready-queue ordering,
// dependencies and admin faces. The claim/ownership red lines live in
// session_test.go (the v1.5 session model replaced the generation-era
// machinery these tests once exercised); this file keeps the WI-manager
// behaviors that are model-agnostic. Every assertion is red when the
// behavior it names is broken.

import (
	"encoding/json"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

// testEnv is a fresh core environment.
type testEnv struct {
	DB      *DB
	Admin   *AdminManager
	WIs     *WIManager
	Owners  *OwnerManager
	UserID  string // admin user id
	UserIDB string // second (writer) user id
	Project string // project id
}

func newEnv(t *testing.T) *testEnv {
	t.Helper()
	return newEnvAt(t, t.TempDir())
}

// newEnvAt opens (or creates) the database in dataDir and seeds the
// admin user, a writer and project alpha through the live write paths.
func newEnvAt(t *testing.T, dataDir string) *testEnv {
	t.Helper()
	db, err := Open(dataDir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	env := &testEnv{
		DB:     db,
		Admin:  NewAdminManager(db),
		WIs:    NewWIManager(db),
		Owners: NewOwnerManager(db),
	}
	u, err := env.Admin.CreateUser("root", "root@example.com", "admin", "human")
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	env.UserID = u.ID
	u2, err := env.Admin.CreateUser("second", "second@example.com", "writer", "human")
	if err != nil {
		t.Fatalf("create user 2: %v", err)
	}
	env.UserIDB = u2.ID
	p, err := env.Admin.CreateProject(CreateProjectRequest{Name: "alpha"})
	if err != nil {
		t.Fatalf("create project: %v", err)
	}
	env.Project = p.ID
	return env
}

// mustCreateWI creates a queued REGISTRATION ticket in project alpha
// (no workflow bundle: never claimable, #17).
func mustCreateWI(t *testing.T, env *testEnv, goal string, attrs map[string]any, priority string) string {
	t.Helper()
	if priority == "" {
		priority = api.PriorityNormal
	}
	wi, err := env.WIs.Create(CreateRequest{
		Project:  "alpha",
		Goal:     goal,
		Attrs:    attrs,
		Priority: priority,
		Reporter: env.UserID,
	})
	if err != nil {
		t.Fatalf("create wi: %v", err)
	}
	return wi.ID
}

// mustCreateExecWI creates a queued EXECUTION ticket carrying a workflow
// bundle reference (claimable). A client-pinned digest is included so the
// pin is explicit in tests; the default stand-in path is covered by
// TestWorkItemCRUD.
func mustCreateExecWI(t *testing.T, env *testEnv, goal string, attrs map[string]any, priority string) string {
	t.Helper()
	if priority == "" {
		priority = api.PriorityNormal
	}
	wi, err := env.WIs.Create(CreateRequest{
		Project:    "alpha",
		Goal:       goal,
		Attrs:      attrs,
		Priority:   priority,
		Reporter:   env.UserID,
		BundleName: strPtrTest("demo-bundle"),
	})
	if err != nil {
		t.Fatalf("create exec wi: %v", err)
	}
	return wi.ID
}

func strPtrTest(s string) *string { return &s }

func TestWorkItemCRUD(t *testing.T) {
	env := newEnv(t)
	wi, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "do the thing", Reporter: env.UserID,
		Labels: []string{"x"}, Priority: api.PriorityHigh,
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if wi.Status != api.StatusQueued {
		t.Fatalf("new WI status = %q, want queued", wi.Status)
	}
	if wi.Slug == "" || wi.Slug == "alpha#0" {
		t.Fatalf("slug not assigned: %q", wi.Slug)
	}
	// Get by id and by slug.
	got, err := env.WIs.Get(wi.ID, GetOptions{})
	if err != nil || got.ID != wi.ID {
		t.Fatalf("get by id: %v %v", got, err)
	}
	got, err = env.WIs.Get(wi.Slug, GetOptions{})
	if err != nil || got.ID != wi.ID {
		t.Fatalf("get by slug: %v %v", got, err)
	}
	// The path-safe "<project>-<seq>" spelling resolves too (the capsule
	// directory / delivery branch name the Messages anchors carry).
	pathName := api.PathName(wi.Slug, wi.ID)
	got, err = env.WIs.Get(pathName, GetOptions{})
	if err != nil || got.ID != wi.ID {
		t.Fatalf("get by path name %q: %v %v", pathName, got, err)
	}
	// Patch.
	newGoal := "do it better"
	updated, err := env.WIs.Patch(wi.ID, map[string]json.RawMessage{
		"goal":   json.RawMessage(`"` + newGoal + `"`),
		"labels": json.RawMessage(`["a","b"]`),
	}, env.UserID)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if updated.Goal != newGoal || len(updated.Labels) != 2 {
		t.Fatalf("patch did not apply: %+v", updated)
	}
	// List filter.
	items, err := env.WIs.List(ListFilters{Project: "alpha", Status: api.StatusQueued})
	if err != nil || len(items) != 1 {
		t.Fatalf("list: %v %d", err, len(items))
	}
	// blocked_seal is Core-written (the blocked close): a create cannot
	// plant one.
	if _, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "fake seal", Reporter: env.UserID,
		Attrs: map[string]any{"blocked_seal": map[string]any{"head": "abc123", "machine": "m9"}},
	}); !IsCode(err, api.ErrCoreBadInput) || ErrorReason(err) != "EXECUTION_STATE_REJECTED" {
		t.Fatalf("a planted blocked_seal not rejected: %v", err)
	}
	// Only the top-level reserved key is refused: other attrs, and a
	// nested blocked_seal (never read as a seal), are business data.
	if _, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "free attrs", Reporter: env.UserID,
		Attrs: map[string]any{"current_step": "x", "custom": map[string]any{"blocked_seal": "y"}},
	}); err != nil {
		t.Fatalf("attrs without a top-level blocked_seal refused: %v", err)
	}
	// Execution ticket: bundle_name pins the workflow reference (the name
	// is the reference — the digest chain is deleted, polyforge#149). The
	// reference freezes at FIRST CLAIM (the R2 window, w140): this ticket
	// is queued and never claimed, so the PATCH renames the reference.
	exec, err := env.WIs.Create(CreateRequest{
		Project: "alpha", Goal: "exec ticket", Reporter: env.UserID,
		BundleName: strPtrTest("demo-bundle"),
	})
	if err != nil {
		t.Fatalf("exec create: %v", err)
	}
	if exec.BundleName == nil || *exec.BundleName != "demo-bundle" {
		t.Fatalf("bundle reference not persisted: %+v", exec)
	}
	patched, err := env.WIs.Patch(exec.ID, map[string]json.RawMessage{"bundle_name": json.RawMessage(`"other"`)}, env.UserID)
	if err != nil {
		t.Fatalf("bundle patch inside the R2 window (queued, never claimed) refused: %v", err)
	}
	if patched.BundleName == nil || *patched.BundleName != "other" {
		t.Fatalf("bundle patch did not rename the reference: %+v", patched)
	}
	// Empty goal, unknown project.
	if _, err := env.WIs.Create(CreateRequest{Project: "alpha", Goal: "  ", Reporter: env.UserID}); !IsCode(err, api.ErrCoreBadInput) {
		t.Fatalf("empty goal not rejected: %v", err)
	}
	if _, err := env.WIs.Create(CreateRequest{Project: "nope", Goal: "g", Reporter: env.UserID}); !IsCode(err, api.ErrCoreNotFound) {
		t.Fatalf("unknown project not 404: %v", err)
	}
}

// TestTerminalTransitions walks the non-claim state machine over the
// session model: session-end wrap terminalizes (and the session row is
// cleared), a terminal WI is read-only, the pause → attach → blocked →
// cancel cycle holds, and a running WI cannot be cancelled directly.
func TestTerminalTransitions(t *testing.T) {
	env := newEnv(t)
	m := NewSessionManager(env.DB)
	id := mustCreateExecWI(t, env, "goal four", nil, "")
	secret := "0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d0d"
	if _, err := m.Claim(id, api.SessionClaimRequest{SessionID: secret}, env.UserID); err != nil {
		t.Fatalf("claim: %v", err)
	}
	if err := m.End(id, api.SessionEndRequest{SessionID: secret, Status: api.StatusWrapped, Note: "done"}, env.UserID); err != nil {
		t.Fatalf("wrap: %v", err)
	}
	wi, err := env.WIs.Get(id, GetOptions{})
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if wi.Status != api.StatusWrapped || wi.ClosedAt == nil {
		t.Fatalf("wrap did not terminalize: %+v", wi)
	}
	status, hash, _, _, _, _ := rowSession(t, env, id)
	if status != api.StatusWrapped || hash != nil {
		t.Fatalf("wrap did not clear the session row: status=%s hash=%v", status, hash)
	}
	// Terminal is read-only.
	if _, err := env.WIs.Patch(id, map[string]json.RawMessage{"goal": json.RawMessage(`"x"`)}, env.UserID); !IsCode(err, api.ErrCoreGone) {
		t.Fatalf("terminal patch allowed: %v", err)
	}
	if _, err := m.Claim(id, api.SessionClaimRequest{SessionID: "0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e0e"}, env.UserID); !IsCode(err, api.ErrCoreGone) {
		t.Fatalf("claim on terminal allowed: %v", err)
	}

	// pause → attach → block → cancel cycle on a second WI.
	id2 := mustCreateExecWI(t, env, "goal five", nil, "")
	secret2 := "0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f0f"
	if _, err := m.Claim(id2, api.SessionClaimRequest{SessionID: secret2}, env.UserID); err != nil {
		t.Fatalf("claim2: %v", err)
	}
	if err := m.End(id2, api.SessionEndRequest{SessionID: secret2, Status: api.StatusPaused}, env.UserID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	wi2, _ := env.WIs.Get(id2, GetOptions{})
	if wi2.Status != api.StatusPaused {
		t.Fatalf("pause status = %q", wi2.Status)
	}
	if _, err := m.Attach(id2, api.SessionAttachRequest{SessionID: secret2}, env.UserID); err != nil {
		t.Fatalf("attach: %v", err)
	}
	if err := m.End(id2, api.SessionEndRequest{SessionID: secret2, Status: api.StatusBlocked, Note: "gate failed"}, env.UserID); err != nil {
		t.Fatalf("block: %v", err)
	}
	wi2, _ = env.WIs.Get(id2, GetOptions{})
	if wi2.Status != api.StatusBlocked || wi2.WaitingOn == nil || *wi2.WaitingOn != "owner" {
		t.Fatalf("block mapping wrong: %+v", wi2)
	}
	canceled, err := env.WIs.Cancel(id2, env.UserID, api.CancelRequest{})
	if err != nil || canceled.Status != api.StatusCanceled {
		t.Fatalf("cancel: %v", err)
	}
	// A terminal WI cannot be cancelled again: CORE-410 TERMINAL_STATE.
	if _, err := env.WIs.Cancel(id2, env.UserID, api.CancelRequest{}); !IsCode(err, api.ErrCoreGone) || ErrorReason(err) != "TERMINAL_STATE" {
		t.Fatalf("cancel of terminal not refused CORE-410: %v", err)
	}
	// Running WI cannot be cancelled directly.
	id3 := mustCreateExecWI(t, env, "goal six", nil, "")
	secret3 := "10101010101010101010101010101010"
	if _, err := m.Claim(id3, api.SessionClaimRequest{SessionID: secret3}, env.UserID); err != nil {
		t.Fatalf("claim3: %v", err)
	}
	if _, err := env.WIs.Cancel(id3, env.UserID, api.CancelRequest{}); !IsCode(err, api.ErrCoreConflict) {
		t.Fatalf("cancel of running allowed: %v", err)
	}
}

// TestRegistrationTicketNeverQueued pins #17's queue half: a
// registration ticket (bundle_name IS NULL) never appears in the
// queue's executable segments. (The claim-gate half — NOT_CLAIMABLE —
// is pinned in session_test.go's TestSessionClaimGates.)
func TestRegistrationTicketNeverQueued(t *testing.T) {
	env := newEnv(t)
	exec := mustCreateExecWI(t, env, "queue exec", nil, "")
	reg := mustCreateWI(t, env, "queue registry", nil, "")
	q, err := env.WIs.ReadyQueue("alpha")
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	if len(q.Items) != 1 || q.Items[0].ID != exec {
		t.Fatalf("executable segment = %+v, want only %s", q.Items, exec)
	}
	for _, seg := range [][]WIView{q.Items} {
		for _, wi := range seg {
			if wi.ID == reg {
				t.Fatalf("registration ticket appeared in an executable segment")
			}
		}
	}
}

func TestReadyQueueOrdering(t *testing.T) {
	env := newEnv(t)
	m := NewSessionManager(env.DB)
	// Created in an order that does NOT match priority order. These are
	// EXECUTION tickets: registration tickets never appear in the
	// executable segment.
	mustCreateExecWI(t, env, "low", nil, api.PriorityLow)
	urgID := mustCreateExecWI(t, env, "urgent", nil, api.PriorityUrgent)
	normID := mustCreateExecWI(t, env, "normal", nil, api.PriorityNormal)
	// A running and a paused WI for the visibility segments.
	rid := mustCreateExecWI(t, env, "running one", nil, "")
	if _, err := m.Claim(rid, api.SessionClaimRequest{SessionID: "20202020202020202020202020202020"}, env.UserID); err != nil {
		t.Fatalf("claim running: %v", err)
	}
	pid := mustCreateExecWI(t, env, "paused one", nil, "")
	psecret := "21212121212121212121212121212121"
	if _, err := m.Claim(pid, api.SessionClaimRequest{SessionID: psecret}, env.UserID); err != nil {
		t.Fatalf("claim paused: %v", err)
	}
	if err := m.End(pid, api.SessionEndRequest{SessionID: psecret, Status: api.StatusPaused}, env.UserID); err != nil {
		t.Fatalf("pause: %v", err)
	}
	// A waiting_on-tagged queued WI is not executable.
	wid := mustCreateWI(t, env, "waiting", nil, "")
	if _, err := env.WIs.Patch(wid, map[string]json.RawMessage{"waiting_on": json.RawMessage(`"owner"`)}, env.UserID); err != nil {
		t.Fatalf("patch waiting_on: %v", err)
	}

	q, err := env.WIs.ReadyQueue("alpha")
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	// items: exactly the three executables, ordered
	// urgent → normal → low by priority (created_at is a tiebreak).
	if len(q.Items) != 3 {
		t.Fatalf("items = %d, want 3: %+v", len(q.Items), q.Items)
	}
	if q.Items[0].ID != urgID || q.Items[1].ID != normID {
		t.Fatalf("priority ordering wrong: %s, %s", q.Items[0].ID, q.Items[1].ID)
	}
	if len(q.Running) != 1 || q.Running[0].ID != rid {
		t.Fatalf("running segment wrong: %+v", q.Running)
	}
	if len(q.Paused) != 1 || q.Paused[0].ID != pid {
		t.Fatalf("paused segment wrong: %+v", q.Paused)
	}
	// The waiting_on-tagged WI is in NO executable segment.
	for _, seg := range [][]WIView{q.Items} {
		for _, wi := range seg {
			if wi.ID == wid {
				t.Fatalf("waiting WI appeared in an executable segment")
			}
		}
	}
	// Unknown project is a clean 404.
	if _, err := env.WIs.ReadyQueue("nope"); !IsCode(err, api.ErrCoreNotFound) {
		t.Fatalf("unknown project: %v", err)
	}
}

func TestDependencyBlocksClaimAndQueue(t *testing.T) {
	env := newEnv(t)
	blocker := mustCreateExecWI(t, env, "blocker", nil, "")
	blocked := mustCreateExecWI(t, env, "blocked", nil, "")
	if err := env.WIs.AddDependency(blocker, blocked, "blocks"); err != nil {
		t.Fatalf("add dep: %v", err)
	}
	q, err := env.WIs.ReadyQueue("alpha")
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	for _, wi := range q.Items {
		if wi.ID == blocked {
			t.Fatalf("dependency-blocked WI appeared in items")
		}
	}
	// Cycle detection.
	if err := env.WIs.AddDependency(blocked, blocker, "blocks"); !IsCode(err, api.ErrCoreConflict) || ErrorReason(err) != "DEPENDENCY_CYCLE" {
		t.Fatalf("cycle not detected: %v", err)
	}
	third := mustCreateExecWI(t, env, "third", nil, "")
	if err := env.WIs.AddDependency(third, blocker, "blocks"); err != nil {
		t.Fatalf("legit edge rejected: %v", err)
	}
	if err := env.WIs.RemoveDependency(third, blocker, "blocks"); err != nil {
		t.Fatalf("remove dep: %v", err)
	}
}

func TestAdminFaces(t *testing.T) {
	env := newEnv(t)
	if _, err := env.Admin.CreateProject(CreateProjectRequest{Name: "beta"}); err != nil {
		t.Fatalf("project: %v", err)
	}
	if _, err := env.Admin.CreateProject(CreateProjectRequest{Name: "beta"}); !IsCode(err, api.ErrCoreConflict) {
		t.Fatalf("duplicate project allowed: %v", err)
	}
	// Machine heartbeat: first registers; second refreshes. Registration
	// mints no machine user: the operator is the machine's one identity
	//.
	proof := HeartbeatProof{CallerID: env.UserID, Signed: true, KeyB64: "pk-m1"}
	h1, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m1", PublicKey: "pk-m1"}, proof, nil)
	if err != nil || !h1.Registered {
		t.Fatalf("heartbeat register: %+v %v", h1, err)
	}
	h2, err := env.Admin.HeartbeatAuthorized(MachineHeartbeat{MachineID: "m1"}, proof, nil)
	if err != nil || h2.Registered {
		t.Fatalf("heartbeat repeat: %+v %v", h2, err)
	}
	users, _, err := env.Admin.ListUsers()
	if err != nil || len(users) != 2 { // admin + writer: no machine user
		t.Fatalf("users: %v %d", err, len(users))
	}
	var minted int
	if err := env.DB.QueryRow(`SELECT COUNT(*) FROM users WHERE email LIKE '%@machines.local'`).Scan(&minted); err != nil || minted != 0 {
		t.Fatalf("a heartbeat minted a machine user: n=%d err=%v", minted, err)
	}
}

// TestSessionClaimCarriesProjectRepos pins the v0 claim semantics
// restored for v1.5: the session claim response carries the project's
// task-repo inventory, served from the projects row the claim
// transaction reads.
func TestSessionClaimCarriesProjectRepos(t *testing.T) {
	env := newEnv(t)
	if _, err := env.Admin.CreateProject(CreateProjectRequest{
		Name: "bravo",
		Repos: []api.ProjectRepo{
			{Name: "app", URL: "file:///srv/app.git"},
			{Name: "lib", URL: "file:///srv/lib.git"},
		},
	}); err != nil {
		t.Fatalf("project: %v", err)
	}
	wi, err := env.WIs.Create(CreateRequest{
		Project:    "bravo",
		Goal:       "repos ride the claim",
		Reporter:   env.UserID,
		BundleName: strPtrTest("demo-bundle"),
	})
	if err != nil {
		t.Fatalf("create wi: %v", err)
	}
	m := NewSessionManager(env.DB)
	res, err := m.Claim(wi.ID, api.SessionClaimRequest{SessionID: "30303030303030303030303030303030"}, env.UserID)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(res.Repos) != 2 ||
		res.Repos[0].Name != "app" || res.Repos[0].URL != "file:///srv/app.git" ||
		res.Repos[1].Name != "lib" || res.Repos[1].URL != "file:///srv/lib.git" {
		t.Fatalf("claim repos wrong: %+v", res.Repos)
	}
}

// TestPatchProjectAtomic: a project patch is one transaction — when a
// later member entry is refused (unknown user), the earlier upsert and
// the description change do not land.
func TestPatchProjectAtomic(t *testing.T) {
	env := newEnv(t)
	desc := "changed"
	_, err := env.Admin.PatchProject("alpha", ProjectPatchRequest{
		Description: &desc,
		Members: &[]api.ProjectMember{
			{UserID: env.UserIDB, Role: "writer"},
			{UserID: "usr_missing", Role: "writer"},
		},
	})
	if !IsCode(err, api.ErrCoreNotFound) {
		t.Fatalf("patch with unknown user: want CORE-404, got %v", err)
	}
	p, err := env.Admin.GetProject("alpha")
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range p.Members {
		if m.UserID == env.UserIDB {
			t.Errorf("refused patch still added member %s", env.UserIDB)
		}
	}
	if p.Description == desc {
		t.Errorf("refused patch still wrote description")
	}
}
