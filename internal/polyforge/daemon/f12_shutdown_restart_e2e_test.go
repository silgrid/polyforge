package daemon

// f12_shutdown_restart_e2e_test.go — the full-chain pin for work item
// polyforge#46 (the shutdown-window commit-dirty push race): a graceful
// stop with an in-flight recovery push, an operator restart landing while
// the OLD process still holds the socket, and the fresh process's restart
// reconciliation — the exact chain the incident log captured — must end
// with every same-WI push either landed or answering up-to-date, no
// "cannot lock ref" rejection anywhere, and a consistent server ref.
//
// Staging (all surfaces real except the agent, which is the fake handle
// the package's other tests use; the push path — runtime recovery →
// commitDirty → gitops.CommitPush → the real git binary against a real
// bare repo — is the production one):
//
//   - daemon 1 (the OLD process) starts with two recorded jobs. Its
//     restart reconciliation resume-spawns wi_e2e_live quickly, then
//     hits wi_e2e_push's commit-dirty push, which a slowed pre-receive
//     hook keeps in flight (the mid-shutdown push of the incident).
//   - its graceful shutdown is triggered and PARKS in the stop-notice
//     ack (the fake agent never acks until the test releases it), so
//     the UDS listener — which closes only at step 5 — is still open
//     while the push is still running.
//   - daemon 2 (the NEW process of the operator restart) starts with
//     the SAME data-dir and socket. It must abort on the
//     single-instance guard BEFORE restart reconciliation: no second
//     push lane may open while the old process is still publishing.
//   - daemon 3 starts after the old process fully exited: its restart
//     reconciliation re-derives both work items, and the commit-dirty
//     pushes answer up-to-date (idempotent duplicate publishes).

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// parkedSpawner spawns fake agents whose control acks park until the
// release channel closes — the stand-in for an agent that is still
// working through its stop boundary, keeping the graceful shutdown (and
// therefore the UDS listener) alive. Spawn runs on the runtime's
// message-loop goroutine while the test goroutine polls callCount, so
// the counter is atomic.
type parkedSpawner struct {
	release chan struct{}
	calls   atomic.Int64
}

func (s *parkedSpawner) Spawn(ctx context.Context, p runtime.SpawnParams) (runtime.AgentHandle, error) {
	s.calls.Add(1)
	return &fakeAgent{}, nil
}

func (s *parkedSpawner) callCount() int { return int(s.calls.Load()) }

// slowPreReceive installs a sleeping pre-receive hook on a bare repo
// (test scaffolding): it widens the window between ref advertisement
// and the ref transaction so the old process's push is still in flight
// while the shutdown parks and the new process starts — the exact
// overlap window the production incident lived in.
func slowPreReceive(t *testing.T, bare string, d time.Duration) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(bare, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	body := "#!/bin/sh\nsleep " + fmt.Sprint(int(d.Seconds())) + "\nexit 0\n"
	if err := os.WriteFile(filepath.Join(bare, "hooks", "pre-receive"), []byte(body), 0o755); err != nil {
		t.Fatalf("write pre-receive: %v", err)
	}
}

// e2eRecord builds one runtime state.json record in the reference.md §3.5 shape:
// the session secret plus its public fingerprint, the derivation counter,
// and none of the deleted attempt/generation/instance/credential fields.
func e2eRecord(wi, capDir, gitDir, bare string) map[string]any {
	session := e2eSession(wi)
	return map[string]any{
		"work_item_id":        wi,
		"project":             "proj",
		"session_id":          session,
		"session_fingerprint": apiSessionFingerprint(session),
		"state":               "running",
		"capsule_dir":         capDir,
		"git_dir":             gitDir,
		"remote_url":          bare,
		"clean_pause":         false,
		"transcript":          filepath.Join(capDir, "session", "session-"+apiSessionFingerprint(session)+".jsonl"),
		"bundle_name":         "demo-bundle",
		"agent_pid":           0,
		"spawn_n":             1,
	}
}

// e2eSession mints the deterministic per-WI test secret (64-hex).
func e2eSession(wi string) string {
	sum := sha256.Sum256([]byte("daemon-e2e-session:" + wi))
	return hex.EncodeToString(sum[:])
}

func apiSessionFingerprint(sessionID string) string {
	return api.SessionFingerprint(sessionID)
}

func seedStateJobs(t *testing.T, dataDir, machineID string, recs ...map[string]any) {
	t.Helper()
	blob, err := json.MarshalIndent(map[string]any{
		"version":    1,
		"machine_id": machineID,
		"jobs":       recs,
	}, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dataDir, "runtime-state.json"), blob, 0o600); err != nil {
		t.Fatal(err)
	}
}

func seedTranscript(t *testing.T, capDir, attempt string) {
	t.Helper()
	path := filepath.Join(capDir, "session", "session-"+attempt+".jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"type":"message_end"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func gitHead(t *testing.T, gitDir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", gitDir, "rev-parse", "--verify", ref).CombinedOutput()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func waitFor(t *testing.T, what string, bound time.Duration, probe func() bool) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if probe() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%s never happened within %v", what, bound)
}

func e2eConfig(sock, ws, data, hubBase string, hub *fakeHub, sp runtime.Spawner) Config {
	return Config{
		MachineID:     "m-e2e",
		SocketPath:    sock,
		Mapping:       mappingFor(ws),
		DataDir:       data,
		GitRemoteBase: hubBase,
		MaxConcurrent: 4,
		Hub:           hub,
		Spawner:       sp,
		// polyforge#106: the engine seam is bound like every other
		// in-process test (the driver is the only engine; the fake starter
		// scripts it through the test's spawner).
		AgentStarter:  fakeEngineStarter(),
		ObservePoll:   10 * time.Millisecond,
		DrainInterval: 10 * time.Millisecond,
		DispatchTick:  10 * time.Millisecond,
	}
}

// TestShutdownRestartFullChainPushIdempotency is the work item's
// requested end-to-end pin: shutdown + restart of the same data-dir,
// with the old process's commit-dirty push mid-flight, ends with no
// rejected push and a consistent server ref.
func TestShutdownRestartFullChainPushIdempotency(t *testing.T) {
	setGitIdentity(t)
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	data := filepath.Join(dir, "data")
	hubBase := filepath.Join(dir, "hub")
	sock := filepath.Join(dir, "d.sock")
	for _, d := range []string{ws, data, hubBase} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Capture everything the daemons write to stderr: the rejection the
	// incident logged ("commit-dirty-on-recover push failed ... cannot
	// lock ref") must never appear in the whole chain's output.
	capFile, err := os.CreateTemp(t.TempDir(), "stderr-*.log")
	if err != nil {
		t.Fatal(err)
	}
	realStderr := os.Stderr
	os.Stderr = capFile
	restored := false
	restore := func() {
		if !restored {
			restored = true
			os.Stderr = realStderr
			capFile.Close()
		}
	}
	t.Cleanup(func() {
		failed := t.Failed()
		restore()
		if failed {
			if body, rerr := os.ReadFile(capFile.Name()); rerr == nil {
				fmt.Fprintf(realStderr, "captured daemon stderr:\n%s\n", body)
			}
		}
	})

	const wiLive = "wi_e2e_live"
	const wiPush = "wi_e2e_push"
	bareLive := mkBare(t, hubBase, wiLive)
	barePush := mkBare(t, hubBase, wiPush)
	slowPreReceive(t, barePush, 3*time.Second)

	capLive := filepath.Join(ws, "capsules", wiLive)
	gitLive := filepath.Join(data, "git", wiLive+".git")
	seedCapsuleGit(t, capLive, gitLive, bareLive)
	seedTranscript(t, capLive, apiSessionFingerprint(e2eSession(wiLive)))

	capPush := filepath.Join(ws, "capsules", wiPush)
	gitPush := filepath.Join(data, "git", wiPush+".git")
	seedCapsuleGit(t, capPush, gitPush, barePush)
	seedTranscript(t, capPush, apiSessionFingerprint(e2eSession(wiPush)))
	// The unflushed state the old process must commit and publish
	// mid-shutdown (the commit-dirty capsule of the incident).
	if err := os.WriteFile(filepath.Join(capPush, "dirty.txt"), []byte("unflushed\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Both records run through daemon 1's restart reconciliation; the
	// order puts the fast-resuming job first so its spawn is the
	// observable "recovery is under way, the push lane is live" signal.
	seedStateJobs(t, data, "m-e2e",
		e2eRecord(wiLive, capLive, gitLive, bareLive),
		e2eRecord(wiPush, capPush, gitPush, barePush),
	)

	basePush := gitHead(t, gitPush, "HEAD")

	// --- the OLD process: starts, recovers, and begins publishing. ---
	release := make(chan struct{})
	sp1 := &parkedSpawner{release: release}
	d1, err := New(e2eConfig(sock, ws, data, hubBase, newFakeHub(), sp1))
	if err != nil {
		restore()
		t.Fatalf("old daemon New: %v", err)
	}
	runDone1 := make(chan error, 1)
	go func() { runDone1 <- d1.Run(context.Background()) }()

	// wi_e2e_live is resume-spawned; its agent is installed in the
	// runtime — at this point daemon 1's reconciliation has moved on to
	// wi_e2e_push, whose commit-dirty push is now parked in the slowed
	// pre-receive hook.
	waitFor(t, "old daemon's first recovery spawn", 5*time.Second, func() bool {
		return sp1.callCount() >= 1
	})
	waitFor(t, "recovered agent visible in the runtime", 5*time.Second, func() bool {
		for _, ji := range d1.rt.List() {
			if ji.WorkItem == wiLive && ji.Agent != nil {
				return true
			}
		}
		return false
	})
	// ...and wi_e2e_push's commit-dirty commit stands locally: its
	// reconciliation step (the push now parked in the hook, then the
	// relaunch) is in flight. The shutdown below does not wait for that
	// step: its push lands on its own, and its relaunch meets the latch
	// and starts nothing — without this wait the latch could come down
	// before the lane reached wi_e2e_push at all.
	waitFor(t, "old daemon's commit-dirty commit of "+wiPush, 10*time.Second, func() bool {
		h := gitHead(t, gitPush, "HEAD")
		return h != "" && h != basePush
	})

	// --- the graceful stop: parks in the stop-notice ack, keeping the
	// UDS listener open while the commit-dirty push is still in flight.
	// ---
	shutErr := make(chan error, 1)
	go func() { shutErr <- d1.Shutdown(context.Background(), DirectShutdownCause()) }()

	// --- the NEW process of the operator restart: same data-dir, same
	// socket, started while the old process is mid-shutdown. ---
	sp2 := &parkedSpawner{release: make(chan struct{})}
	d2, err := New(e2eConfig(sock, ws, data, hubBase, newFakeHub(), sp2))
	if err != nil {
		restore()
		t.Fatalf("new daemon New: %v", err)
	}
	runDone2 := make(chan error, 1)
	go func() { runDone2 <- d2.Run(context.Background()) }()

	// The single-instance guard must fire, and BEFORE any restart
	// reconciliation: the new process never opened a push lane (zero
	// recoveries), so no second same-WI push existed to race the old
	// process's in-flight one.
	var runErr2 error
	select {
	case runErr2 = <-runDone2:
	case <-time.After(15 * time.Second):
		restore()
		t.Fatal("new daemon never returned")
	}
	sp2Calls := sp2.callCount()
	restore()
	// v1.5 M5: the guard now has TWO halves — the data-dir flock (which
	// fires first here: the old process still holds it) and the socket
	// dial probe. Either refusal is the guard doing its job.
	if runErr2 == nil || !strings.Contains(runErr2.Error(), "another daemon") {
		t.Fatalf("new daemon Run = %v, want the single-instance guard refusal", runErr2)
	}
	if sp2Calls != 0 {
		t.Fatalf("new daemon ran restart reconciliation before the guard (%d recoveries); it could have pushed the same work items from a second process", sp2Calls)
	}

	// --- the old process's push lands (it was the only lane), and its
	// recovery relaunches nothing behind the shutdown latch (the record
	// stays recovering for the next start); then the stop sequence is
	// released. ---
	waitFor(t, "old process's mid-shutdown commit-dirty push to land", 15*time.Second, func() bool {
		return gitHead(t, gitPush, "HEAD") != "" &&
			gitHead(t, barePush, "refs/heads/main") == gitHead(t, gitPush, "HEAD")
	})
	stillSpawned(t, sp1.callCount, 1)
	if n := sp1.callCount(); n != 1 {
		t.Fatalf("old process spawned %d agent(s); the relaunch behind the shutdown latch must start nothing", n)
	}
	close(release)
	select {
	case err := <-shutErr:
		if err != nil {
			t.Fatalf("old daemon shutdown: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("old daemon shutdown never finished")
	}
	select {
	case err := <-runDone1:
		if err != nil {
			t.Fatalf("old daemon Run: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("old daemon Run never returned")
	}

	pushedHead := gitHead(t, gitPush, "HEAD")
	if got := gitHead(t, barePush, "refs/heads/main"); got != pushedHead {
		t.Fatalf("server main=%s, want the old process's commit-dirty commit %s", got, pushedHead)
	}

	// --- the fresh start after the old process is fully gone: restart
	// reconciliation re-derives both work items; every commit-dirty
	// push answers up-to-date (idempotent duplicate publishes across
	// the restart boundary). ---
	sp3rel := make(chan struct{})
	sp3 := &parkedSpawner{release: sp3rel}
	d3, err := New(e2eConfig(sock, ws, data, hubBase, newFakeHub(), sp3))
	if err != nil {
		t.Fatalf("third daemon New: %v", err)
	}
	runDone3 := make(chan error, 1)
	go func() { runDone3 <- d3.Run(context.Background()) }()
	waitFor(t, "third daemon's restart reconciliation", 10*time.Second, func() bool {
		return sp3.callCount() >= 2
	})
	close(sp3rel) // let the third daemon's stop notices ack
	if err := d3.Shutdown(context.Background(), DirectShutdownCause()); err != nil {
		t.Fatalf("third daemon shutdown: %v", err)
	}
	select {
	case err := <-runDone3:
		if err != nil {
			t.Fatalf("third daemon Run: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("third daemon Run never returned")
	}

	// The chain ends consistent and rejection-free: the server ref and
	// the capsule agree (the fresh process's commit-dirty may have
	// advanced both — the recovery engine's F1/F7 repair files are new
	// local state its own publish is obliged to carry), and the whole
	// chain's output holds none of the incident's rejection lines.
	if got, want := gitHead(t, barePush, "refs/heads/main"), gitHead(t, gitPush, "HEAD"); got != want {
		t.Fatalf("server main=%s disagrees with capsule HEAD=%s after the full chain", got, want)
	}

	// The whole chain's output holds no rejection: the graceful-stop
	// log lines of the incident must be absent.
	body, err := os.ReadFile(capFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(body), "cannot lock ref") {
		t.Fatalf("the shutdown+restart chain logged a ref-lock rejection:\n%s", body)
	}
	if strings.Contains(string(body), "commit-dirty-on-recover push failed") {
		t.Fatalf("the shutdown+restart chain logged a failed commit-dirty push:\n%s", body)
	}
}

// ctxSpawner spawns plain (immediately-acking) fake agents while the
// context lives and refuses once it is cancelled — the shape a real
// spawner has (the derivation exec rides the context). It backs the
// shutdown-during-reconciliation corner below: a reconciliation whose
// context the shutdown sequence already cancelled must not mint a live
// agent after the sequence's terminate sweep has run.
type ctxSpawner struct {
	calls atomic.Int64
}

func (s *ctxSpawner) Spawn(ctx context.Context, p runtime.SpawnParams) (runtime.AgentHandle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.calls.Add(1)
	return &fakeAgent{}, nil
}

func (s *ctxSpawner) callCount() int { return int(s.calls.Load()) }

// TestShutdownDuringStartupReconciliationNeverServesFace pins the
// shutdown that runs to completion WHILE a work item's restart
// reconciliation is still mid-push: Run returns nil, no connection is
// accepted afterwards, and the cancelled reconciliation spawns no agent
// behind the sequence's back.
func TestShutdownDuringStartupReconciliationNeverServesFace(t *testing.T) {
	setGitIdentity(t)
	dir := t.TempDir()
	ws := filepath.Join(dir, "ws")
	data := filepath.Join(dir, "data")
	hubBase := filepath.Join(dir, "hub")
	sock := filepath.Join(dir, "d.sock")
	for _, d := range []string{ws, data, hubBase} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	const wi = "wi_e2e_corner"
	bare := mkBare(t, hubBase, wi)
	slowPreReceive(t, bare, 3*time.Second)

	capDir := filepath.Join(ws, "capsules", wi)
	gitDir := filepath.Join(data, "git", wi+".git")
	seedCapsuleGit(t, capDir, gitDir, bare)
	seedTranscript(t, capDir, apiSessionFingerprint(e2eSession(wi)))
	baseline := gitHead(t, gitDir, "HEAD")
	if baseline == "" {
		t.Fatal("no baseline commit after seeding")
	}
	// The unflushed state whose commit-dirty push parks in the slowed
	// hook while the shutdown sequence runs.
	if err := os.WriteFile(filepath.Join(capDir, "dirty.txt"), []byte("unflushed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seedStateJobs(t, data, "m-e2e", e2eRecord(wi, capDir, gitDir, bare))

	sp := &ctxSpawner{}
	d, err := New(e2eConfig(sock, ws, data, hubBase, newFakeHub(), sp))
	if err != nil {
		t.Fatalf("daemon New: %v", err)
	}
	runDone := make(chan error, 1)
	go func() { runDone <- d.Run(context.Background()) }()

	// The reconciliation's commit-dirty commit stands locally BEFORE
	// its push parks in the slowed hook — that is the observable
	// "reconciliation is mid-push" signal.
	waitFor(t, "commit-dirty commit to stand locally", 5*time.Second, func() bool {
		return gitHead(t, gitDir, "HEAD") != baseline
	})

	// The shutdown sequence completes (no live agent yet: nothing to
	// notice, flush or terminate) while the push is still in flight.
	if err := d.Shutdown(context.Background(), DirectShutdownCause()); err != nil {
		t.Fatalf("shutdown during reconciliation: %v", err)
	}

	// Run must return nil — refusing to serve on the face the sequence
	// closed — and never spawn an agent for the cancelled reconciliation.
	select {
	case err := <-runDone:
		if err != nil {
			t.Fatalf("Run after mid-reconciliation shutdown = %v, want nil", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Run never returned after the mid-reconciliation shutdown")
	}
	if n := sp.callCount(); n != 0 {
		t.Fatalf("reconciliation spawned %d agent(s) after the shutdown sequence completed", n)
	}
	if conn, derr := net.DialTimeout("unix", sock, time.Second); derr == nil {
		conn.Close()
		t.Fatal("the face still accepts connections after a shutdown that closed it mid-reconciliation")
	}

	// The in-flight push was the only lane: the shutdown does not wait
	// for it (it starts no agent behind the latch), and it lands — the
	// server ref and the capsule agree, the same idempotent end state the
	// full-chain test asserts.
	waitFor(t, "the in-flight push to land", 15*time.Second, func() bool {
		return gitHead(t, bare, "refs/heads/main") == gitHead(t, gitDir, "HEAD")
	})
	stillSpawned(t, sp.callCount, 0)
	if n := sp.callCount(); n != 0 {
		t.Fatalf("the reconciliation spawned %d agent(s) once its push landed behind the shutdown", n)
	}
}

// stillSpawned checks, over a short window, that count stays at want: a
// reconciliation whose push just landed decides its relaunch right after.
func stillSpawned(t *testing.T, count func() int, want int) {
	t.Helper()
	for end := time.Now().Add(500 * time.Millisecond); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		if n := count(); n != want {
			t.Fatalf("%d agent(s) spawned, want %d", n, want)
		}
	}
}
