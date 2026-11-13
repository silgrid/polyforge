package driver

// resources_test.go: the step-level admission and locks primitive
// (resources.go) and the per-step round and tool window the dispatch
// carries (workflow contract v2 O1 / O2 / O-2). The queue's own order is
// the daemon's (verifyqueue tests); here: the fixed order admission then
// merge locks, all or nothing, the release in reverse; a declared
// tool_minutes is the step's supervision window; the round advances on a
// fresh entry only and rides every dispatch of the round.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// orderForge is a Forge whose lock records, at each call, how many
// admission asks the fake admission had seen (the order proof), and that
// can be told to refuse.
type orderForge struct {
	adm    *fakeAdmission
	refuse error
	mu     sync.Mutex
	events []string
}

func (f *orderForge) CommandEnv() []string { return nil }

func (f *orderForge) LockRepo(_ context.Context, repo string) (func(), error) {
	f.adm.mu.Lock()
	asks := len(f.adm.asks)
	f.adm.mu.Unlock()
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse != nil {
		f.events = append(f.events, fmt.Sprintf("refuse %s (asks=%d)", repo, asks))
		return nil, f.refuse
	}
	f.events = append(f.events, fmt.Sprintf("lock %s (asks=%d)", repo, asks))
	return func() {
		f.mu.Lock()
		f.events = append(f.events, "unlock "+repo)
		f.mu.Unlock()
	}, nil
}

func (f *orderForge) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.events...)
}

// Admission first, the merge locks after
// in key order and each key once; the release gives the locks back in
// reverse order, then the slot.
func TestAcquireResourcesOrderAndRelease(t *testing.T) {
	d, _, _ := runEnv(t, spec("a"))
	adm := &fakeAdmission{open: true}
	forge := &orderForge{adm: adm}
	d.cfg.Admission, d.cfg.Forge = adm, forge
	release, err := d.acquireResources(context.Background(), "a", ResourceRequest{
		Admission: true, MergeKeys: []string{"o/b", "o/a", "o/b"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(adm.asks) != 1 || adm.asks[0].Step != "a" {
		t.Fatalf("one admission ask for the step: %+v", adm.asks)
	}
	if got := fmt.Sprint(forge.log()); got != "[lock o/a (asks=1) lock o/b (asks=1)]" {
		t.Fatalf("the merge locks are taken after the admission, in key order, once each: %s", got)
	}
	if adm.tickets[0].released {
		t.Fatal("the slot is held while the locks are")
	}
	release()
	release() // idempotent
	if got := fmt.Sprint(forge.log()); got != "[lock o/a (asks=1) lock o/b (asks=1) unlock o/b unlock o/a]" {
		t.Fatalf("the locks are released in reverse order, once: %s", got)
	}
	if !adm.tickets[0].released {
		t.Fatal("the release gives the admission slot back")
	}
}

// All or nothing: a merge lock the forge refuses releases the admission
// already held, and the error names the lock.
func TestAcquireResourcesLockFailureReleasesAdmission(t *testing.T) {
	d, _, _ := runEnv(t, spec("a"))
	adm := &fakeAdmission{open: true}
	forge := &orderForge{adm: adm, refuse: errors.New("forge down")}
	d.cfg.Admission, d.cfg.Forge = adm, forge
	release, err := d.acquireResources(context.Background(), "a", ResourceRequest{Admission: true, MergeKeys: []string{"o/a"}})
	if err == nil || release != nil || err.Error() != "taking the merge lock of o/a: forge down" {
		t.Fatalf("the refused lock fails the whole acquisition: release=%v err=%v", release != nil, err)
	}
	if len(adm.tickets) != 1 || !adm.tickets[0].released {
		t.Fatalf("the admission held before the refused lock is given back: %+v", adm.tickets)
	}
}

// A dispatch that needs no admission takes none (no ask); a merge lock
// without a forge binding is refused; an admission with no queue bound is
// granted at once.
func TestAcquireResourcesBindings(t *testing.T) {
	d, _, _ := runEnv(t, spec("a"))
	adm := &fakeAdmission{open: true}
	d.cfg.Admission = adm
	if _, err := d.acquireResources(context.Background(), "a", ResourceRequest{MergeKeys: []string{"o/a"}}); err == nil ||
		err.Error() != "this daemon has no forge binding: the per-repo merge lock cannot be taken" {
		t.Fatalf("a merge lock needs the forge binding: %v", err)
	}
	if len(adm.asks) != 0 {
		t.Fatalf("a request without admission asks the queue nothing: %+v", adm.asks)
	}
	release, err := d.acquireResources(context.Background(), "a", ResourceRequest{})
	if err != nil || release == nil {
		t.Fatalf("nothing to take is an empty, releasable grant: %v", err)
	}
	release()
	d.cfg.Admission = nil
	release, err = d.acquireResources(context.Background(), "a", ResourceRequest{Admission: true})
	if err != nil || release == nil {
		t.Fatalf("no queue bound: admitted at once, %v", err)
	}
	release()
}

// A dispatch whose context ends while it queues leaves the queue with
// nothing held.
func TestAcquireResourcesLeavesQueueOnCancel(t *testing.T) {
	d, _, _ := runEnv(t, spec("a"))
	adm := &fakeAdmission{} // never grants
	d.cfg.Admission = adm
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := d.acquireResources(ctx, "a", ResourceRequest{Admission: true})
		done <- err
	}()
	waitUntil(t, "the ask", func() bool {
		adm.mu.Lock()
		defer adm.mu.Unlock()
		return len(adm.asks) == 1
	})
	cancel()
	select {
	case err := <-done:
		if err == nil || !errors.Is(err, context.Canceled) {
			t.Fatalf("the cancelled wait answers the context's error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the queued acquisition never returned after the cancel")
	}
	if !adm.tickets[0].released {
		t.Fatal("a cancelled wait leaves the queue")
	}
}

// A declared tool_minutes is the step's supervision tool window Y and
// rides its dispatch; a step declaring none keeps the global window (the
// supervision default here) and a zero spawn window.
func TestToolMinutesIsTheStepToolWindow(t *testing.T) {
	d, sp, _ := runEnv(t, StepSpec{ID: "gate", ToolMinutes: 120}, spec("plain"))
	if tool, _ := d.stepBases("gate"); tool != 120*time.Minute {
		t.Fatalf("the declared window is the step's Y: %v", tool)
	}
	if tool, _ := d.stepBases("plain"); tool != 30*time.Minute {
		t.Fatalf("no declaration keeps the global window: %v", tool)
	}
	d.mu.Lock()
	th := d.effectiveThresholdsLocked("gate", supervise.GranularityToken)
	d.mu.Unlock()
	if th.Tool != 120*time.Minute {
		t.Fatalf("the dispatch thresholds carry the declared window: %+v", th)
	}
	sp.script = []fakeEntry{done(`{}`), done(`{}`)}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := sp.spawnAt(0).ToolWindow; got != 120*time.Minute {
		t.Fatalf("the declared window rides the spawn: %v", got)
	}
	if got := sp.spawnAt(1).ToolWindow; got != 0 {
		t.Fatalf("an undeclared step spawns with no window of its own: %v", got)
	}
}

// The round: 1 on every step's first entry, carried by every dispatch of
// the round, and advanced by the rewind's re-entry — for the rewound
// target (even though it resumes its conversation), the superseded step
// between and the gate alike; execution.json records it.
func TestRoundAdvancesOnRewindReentry(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("build"), spec("polish"), gateSpec("check", "build"))
	sp.script = []fakeEntry{
		done(`{"b":1}`), done(`{"p":1}`), gateFAIL(),
		done(`{"b":2}`), done(`{"p":2}`), done(`{"verdict":"pass"}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var got []string
	for i := 0; i < 6; i++ {
		s := sp.spawnAt(i)
		got = append(got, fmt.Sprintf("%s@%d:r%d", s.StepID, s.SpawnN, s.Round))
	}
	want := "[build@1:r1 polish@2:r1 check@3:r1 build@4:r2 polish@5:r2 check@6:r2]"
	if fmt.Sprint(got) != want {
		t.Fatalf("rounds per dispatch:\n got  %v\n want %s", got, want)
	}
	x := readExec(t, execPath)
	for _, id := range []string{"build", "polish", "check"} {
		if x.Steps[id].Round != 2 {
			t.Fatalf("%s records round 2 after the rewind: %+v", id, x.Steps[id])
		}
	}
}

// A resume of an in_progress step (a crash re-dispatch) stays in its
// round: the round advanced at the fresh entry only.
func TestRoundKeptAcrossResume(t *testing.T) {
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	exec := skeletonExecution(WIInfo{ID: "wi_run"}, testLane(0, []StepSpec{spec("a")}...))
	exec.Steps["a"].Status = capsule.StepInProgress
	exec.Steps["a"].Round = 1
	exec.CurrentStep = "a"
	exec.Version = 2
	blob, _ := json.MarshalIndent(exec, "", "  ")
	if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(execPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	sp := &fakeSpawner{script: []fakeEntry{done(`{}`)}}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: testLane(4, []StepSpec{spec("a")}...),
		Spawner:  sp,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if s := sp.spawnAt(0); !s.Resume || s.Round != 1 {
		t.Fatalf("the resumed dispatch stays in round 1: %+v", s)
	}
	if x := readExec(t, execPath); x.Steps["a"].Round != 1 {
		t.Fatalf("the resume advanced the round: %+v", x.Steps["a"])
	}
}

// fakeAdmission grants when open, else never; it records every ask.
type fakeAdmission struct {
	mu      sync.Mutex
	open    bool
	asks    []AdmissionRequest
	tickets []*fakeTicket
}

type fakeTicket struct {
	ready    chan struct{}
	released bool
	a        *fakeAdmission
}

func (a *fakeAdmission) Enter(req AdmissionRequest) AdmissionTicket {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.asks = append(a.asks, req)
	t := &fakeTicket{ready: make(chan struct{}), a: a}
	if a.open {
		close(t.ready)
	}
	a.tickets = append(a.tickets, t)
	return t
}

func (t *fakeTicket) Ready() <-chan struct{} { return t.ready }
func (t *fakeTicket) State() (int, string)   { return 2, "heavy-run slots full (2/2)" }
func (t *fakeTicket) Release() {
	t.a.mu.Lock()
	t.released = true
	t.a.mu.Unlock()
}

// The merge lock's key is the repository, not the spelling of its URL
// (resources.go originKey, mergeLockKey): https and ssh clones, with or
// without `.git`, a trailing `/`, a user or a port, in any case, give one
// key; another repository another. Every step declaring `locks: [merge]`
// (develop's and micro's merge) takes its locks by these keys.
func TestMergeLockKeyNormalizesTheOrigin(t *testing.T) {
	for _, u := range []string{
		"https://github.com/Owner/Repo.git", "https://github.com/owner/repo", "https://github.com/owner/repo/",
		"https://user@github.com:443/owner/repo.git", "git@github.com:owner/repo.git", "git@github.com:owner/repo",
		"ssh://git@github.com/owner/repo.git", "ssh://git@github.com:22/owner/repo", "git@github.com:/owner/repo.git",
	} {
		if got := originKey(u); got != "github.com/owner/repo" {
			t.Errorf("%s: key %q", u, got)
		}
	}
	for _, u := range []string{"https://github.com/owner/other.git", "git@github.com:other/repo.git", "https://example.com/owner/repo"} {
		if got := originKey(u); got == "github.com/owner/repo" {
			t.Errorf("%s names another repository, yet keys %q", u, got)
		}
	}
	if a, b := originKey("/srv/origins/app.git"), originKey("file:///srv/origins/app/"); a != "/srv/origins/app" || a != b {
		t.Errorf("a local origin keys by its path: %q %q", a, b)
	}
	if originKey("  ") != "" {
		t.Error("a blank origin keys nothing")
	}
	// two clones of one repository over https and ssh take one lock; a
	// clone with no origin is an error the caller parks on
	clone := func(origin string) string {
		dir := t.TempDir()
		tgit(t, dir, "init", "-q")
		if origin != "" {
			tgit(t, dir, "remote", "add", "origin", origin)
		}
		return dir
	}
	a, b := clone("https://github.com/Owner/App.git"), clone("git@github.com:owner/app")
	ka, errA := mergeLockKey(context.Background(), nil, a)
	kb, errB := mergeLockKey(context.Background(), nil, b)
	if errA != nil || errB != nil || ka != kb || ka != "github.com/owner/app" {
		t.Fatalf("one repository, one lock: %q %q (%v %v)", ka, kb, errA, errB)
	}
	keys, err := mergeLockKeys(context.Background(), nil, []string{a, b})
	if err != nil || len(keys) != 2 || keys[0] != ka || keys[1] != kb {
		t.Fatalf("mergeLockKeys = %v, %v", keys, err)
	}
	if _, err := mergeLockKey(context.Background(), nil, clone("")); err == nil {
		t.Fatal("a clone without an origin has no merge-lock key")
	}
}
