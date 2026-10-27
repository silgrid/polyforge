package runtime

// Start/Stop end-to-end: claim → clone+external-git-dir → materialize →
// spawn → RTStarted, then seal → final push → end-generation → RTFinished,
// against a real capsule git repo and a real (scripted) child process.

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
)

func TestStartStopEndToEnd(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, hub, sp)

	// aihub's bare repo for this WI lives under a local dir standing in
	// for the git proxy base
	base := t.TempDir()
	r.cfg.GitRemoteBase = base
	wi := "wi_e2e"
	bare := filepath.Join(base, wi+".git")
	if out, err := run(t, "git", "init", "--bare", bare); err != nil {
		t.Fatalf("bare init: %s", out)
	}
	// every push carries --push-option=attempt-id=… (§六 备层): the
	// receiving end must advertise push options
	if out, err := run(t, "git", "--git-dir", bare, "config", "receive.advertisePushOptions", "true"); err != nil {
		t.Fatalf("bare advertisePushOptions: %s", out)
	}
	if out, err := run(t, "git", "--git-dir", bare, "config", "receive.shallowUpdate", "true"); err != nil {
		t.Fatalf("bare shallowUpdate: %s", out)
	}

	ctx := context.Background()
	if err := r.Start(ctx, wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// RTStarted carries the agent handle, the derivation counter and the
	// session identity: the fence token is the public fingerprint of the
	// in-process secret, spawn_n is 1 (the first derivation).
	started := recvRT(t, toOB)
	if started.Kind != protocol.RTStarted || started.WorkItem != wi {
		t.Fatalf("expected RTStarted for %s, got %+v", wi, started)
	}
	if started.AgentPID <= 0 || started.SpawnN != 1 {
		t.Fatalf("RTStarted missing handle/spawn_n: %+v", started)
	}
	if started.SessionID == "" || len(started.SessionID) != 64 {
		t.Fatalf("RTStarted must carry the 64-hex session secret in-process: %+v", started)
	}

	capDir := testCapsuleDir(r, wi)
	// capsule layout materialized
	for _, rel := range []string{
		capsule.CapsuleJSON, ".gitignore",
		capsule.DirWorkflows, capsule.DirRuntimeState, capsule.DirSession,
	} {
		if _, err := os.Stat(filepath.Join(capDir, filepath.FromSlash(rel))); err != nil {
			t.Fatalf("materialized %s missing: %v", rel, err)
		}
	}
	// capsule.json parses and carries the canonical identity (the
	// generation/attempt lineage keys are GONE — the fossil round deleted
	// the fields from the frozen shape, see capsule_test.go for the
	// old-capsule loose-parse pin)
	raw, err := os.ReadFile(filepath.Join(capDir, capsule.CapsuleJSON))
	if err != nil {
		t.Fatal(err)
	}
	var idn capsule.Identity
	if err := json.Unmarshal(raw, &idn); err != nil || idn.WorkItemID != wi {
		t.Fatalf("capsule.json wrong: %v %+v", err, idn)
	}
	// git-dir is EXTERNAL: .git is a one-line pointer, not a directory
	pointer := filepath.Join(capDir, ".git")
	if st, err := os.Stat(pointer); err != nil || !st.Mode().IsRegular() {
		t.Fatalf("capsule .git must be a pointer file (external git-dir ruling)")
	}
	if !dirExists(r.gitDirFor(wi)) {
		t.Fatalf("external git-dir missing at %s", r.gitDirFor(wi))
	}
	// state.json recorded the job: the reference.md §3.5 shape — the secret, the
	// fingerprint, the derivation counter, and NO attempt/generation/
	// instance/credential fields.
	recs := loadRecords(t, r)
	if len(recs) != 1 || recs[0].WorkItemID != wi {
		t.Fatalf("state.json should hold one record, got %+v", recs)
	}
	rec := recs[0]
	if rec.SessionID != started.SessionID || rec.SessionFingerprint != api.SessionFingerprint(started.SessionID) ||
		rec.SpawnN != 1 || rec.CleanPause {
		t.Fatalf("record must carry the session identity: %+v", rec)
	}
	// (the deleted attempt/generation/instance/credential fields are gone
	// from the record shape entirely — the jobRecord type has no such
	// fields to assert absent)

	// Stop: seal → push → session-end → RTFinished(wrapped)
	if err := os.WriteFile(filepath.Join(capDir, "final.txt"), []byte("done\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := r.Stop(ctx, wi, protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	fin := recvRT(t, toOB)
	if fin.Kind != protocol.RTFinished || fin.Reason != protocol.FinishWrapped {
		t.Fatalf("expected RTFinished(wrapped), got %+v", fin)
	}
	if hub.endCount() != 1 || hub.lastEnd().Status != protocol.FinishWrapped {
		t.Fatalf("session-end not called with wrapped: %+v", hub.lastEnd())
	}
	if hub.lastEnd().SessionID != started.SessionID {
		t.Fatalf("session-end must be fenced on our secret: %+v", hub.lastEnd())
	}
	if !bareHasMain(t, bare) {
		t.Fatal("final push never landed on the bare repo")
	}
	// job is gone everywhere
	if r.jobOf(wi) != nil {
		t.Fatal("job still in the live map after Stop")
	}
	if recs := loadRecords(t, r); len(recs) != 0 {
		t.Fatalf("state.json should be empty after Stop, got %+v", recs)
	}
	// the sealed scene is read-only: dirs 0555, files 0444 (an
	// anti-mistake guard — chmod does not bind a same-UID/root writer,
	// so the assertion is on the mode bits, not on a failed write)
	if st, err := os.Stat(capDir); err != nil || st.Mode().Perm() != 0o555 {
		t.Fatalf("capsule root must be sealed 0555, got %v (%v)", st.Mode().Perm(), err)
	}
	if st, err := os.Stat(filepath.Join(capDir, "final.txt")); err != nil || st.Mode().Perm() != 0o444 {
		t.Fatalf("sealed file must be 0444, got %v (%v)", st.Mode().Perm(), err)
	}
	// the stopped agent (a real child) was torn down
	pid := started.AgentPID
	waitDead(t, pid)

	unseal(t, capDir)
	unseal(t, testWorkspace(r))
}

func TestStopUnknownWorkItem(t *testing.T) {
	r, _, _ := newTestRuntime(t, newFakeHub(), &scriptedSpawner{})
	if err := r.Stop(context.Background(), "wi_none", protocol.FinishWrapped); err == nil {
		t.Fatal("Stop of an unknown work item must fail")
	}
}

// TestStopHubCloseFailureLeavesSealing pins the §五 wrap semantics: a
// failed hub close must NOT silently half-seal — the job stays
// SEALING (persisted with its reason) for the restart path to finish.
func TestStopHubCloseFailureLeavesSealing(t *testing.T) {
	hub := newFakeHub()
	hub.lookupErr["wi_seal"] = errLookupBoom
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	j := seedJob(r, "wi_seal")
	initGitCapsule(t, j)

	err := r.Stop(context.Background(), "wi_seal", protocol.FinishWrapped)
	if err == nil {
		t.Fatal("Stop must fail when the hub close fails")
	}
	state, _ := j.snapshot()
	if state != StateSealing {
		t.Fatalf("job must stay SEALING, got %s", state)
	}
	recs := loadRecords(t, r)
	if len(recs) != 1 || recs[0].State != string(StateSealing) || recs[0].StopReason != protocol.FinishWrapped {
		t.Fatalf("sealing intent not persisted: %+v", recs)
	}
	assertNoRT(t, toOB) // no WIFinished — the seal never landed
	unseal(t, testWorkspace(r))
}

func run(t *testing.T, name string, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command(name, args...).CombinedOutput()
	return string(out), err
}

// TestPushStanding pins the honest final-push retry: a standing local
// commit whose first push failed must be re-drivable directly (CommitPush
// itself now pushes the standing HEAD on a clean tree, so both paths are
// honest; the direct Push retry covers the failed-first-push case).
func TestPushStanding(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	j := seedJob(r, "wi_push")
	bare := initGitCapsule(t, j)

	// an unreachable remote answers a typed error and nothing lands
	bad := j.git
	bad.RemoteURL = filepath.Join(t.TempDir(), "nowhere.git")
	if pushed, err := gitops.Push(bad); err == nil || pushed {
		t.Fatal("push to a nonexistent remote must fail")
	}

	// the real remote accepts the standing HEAD
	pushed, err := gitops.Push(j.git)
	if err != nil || !pushed {
		t.Fatalf("gitops.Push of the standing HEAD = (%v, %v)", pushed, err)
	}
	if !bareHasMain(t, bare) {
		t.Fatal("the standing commit must land on the bare repo")
	}
	unseal(t, testWorkspace(r))
}
