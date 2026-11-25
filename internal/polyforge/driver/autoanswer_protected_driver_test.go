package driver

// autoanswer_protected_driver_test.go: the protected stops at the driver
// level (polyforge#239): a non-gate step declaring `protected_stop` and a
// lane the build cannot verify keep waiting; an ordinary non-gate step of
// a verified lane is answered. The loader reads `protected_stop` from the
// lane file, and the shipped lanes declare it on their human stops.

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

func TestAutoAnswerProtectedStopDrivesBehavior(t *testing.T) {
	const step = "collect"
	develop := func(steps ...StepSpec) *Workflow {
		return &Workflow{Name: "develop", MaxRewinds: 4, Steps: steps}
	}
	verified := func(string) bool { return true }

	t.Run("protected_stop non-gate step", func(t *testing.T) {
		dir := t.TempDir()
		base := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		ask := seedAutoAnswerCapsule(t, dir, step, 1, base, true)
		clock := &autoAnswerClock{at: base}
		sp := &fakeSpawner{script: []fakeEntry{{hold: true}, {hold: true}}}
		d, err := New(Config{
			WI: WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"}, ExecPath: filepath.Join(dir, capsule.ExecutionJSON),
			Workflow: develop(StepSpec{ID: step, ProtectedStop: true}), Spawner: sp, Log: &safeLog{},
			Link: supervise.NewLink("wi_run"), Limits: supervise.Defaults{AskWarmWindow: 150 * time.Millisecond},
			AskPoll: 5 * time.Millisecond, OwnerWait: (&ownerWaits{}).set, Now: clock.now, LaneVerifiable: verified,
		})
		if err != nil {
			t.Fatal(err)
		}
		var checks atomic.Int64
		d.cfg.AutoAnswerChecked = func() { checks.Add(1) }
		stop := runDriver(t, d)
		clock.advance(60 * time.Minute)
		n0 := checks.Load()
		waitUntil(t, "the due checks", func() bool { return checks.Load() >= n0+5 })
		stop()
		if got := autoReplies(autoAnswerLines(t, dir), ask.At); len(got) != 0 {
			t.Fatalf("a protected_stop step was answered: %+v", got)
		}
	})

	t.Run("an undeclared non-gate step is answered", func(t *testing.T) {
		dir := t.TempDir()
		base := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		ask := seedAutoAnswerCapsule(t, dir, step, 1, base, true)
		clock := &autoAnswerClock{at: base}
		sp := &fakeSpawner{script: []fakeEntry{{hold: true}, {hold: true}}}
		d, err := New(Config{
			WI: WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"}, ExecPath: filepath.Join(dir, capsule.ExecutionJSON),
			Workflow: develop(StepSpec{ID: step}), Spawner: sp, Log: &safeLog{},
			Link: supervise.NewLink("wi_run"), Limits: supervise.Defaults{AskWarmWindow: 150 * time.Millisecond},
			AskPoll: 5 * time.Millisecond, OwnerWait: (&ownerWaits{}).set, Now: clock.now, LaneVerifiable: verified,
		})
		if err != nil {
			t.Fatal(err)
		}
		stop := runDriver(t, d)
		clock.advance(60 * time.Minute)
		waitUntil(t, "the automatic reply", func() bool { return len(autoReplies(autoAnswerLines(t, dir), ask.At)) == 1 })
		stop()
	})

	t.Run("an unverifiable lane keeps every stop", func(t *testing.T) {
		dir := t.TempDir()
		base := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
		ask := seedAutoAnswerCapsule(t, dir, step, 1, base, true)
		clock := &autoAnswerClock{at: base}
		sp := &fakeSpawner{script: []fakeEntry{{hold: true}, {hold: true}}}
		d, err := New(Config{
			WI: WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"}, ExecPath: filepath.Join(dir, capsule.ExecutionJSON),
			Workflow: develop(StepSpec{ID: step}), Spawner: sp, Log: &safeLog{},
			Link: supervise.NewLink("wi_run"), Limits: supervise.Defaults{AskWarmWindow: 150 * time.Millisecond},
			AskPoll: 5 * time.Millisecond, OwnerWait: (&ownerWaits{}).set, Now: clock.now,
			LaneVerifiable: func(string) bool { return false },
		})
		if err != nil {
			t.Fatal(err)
		}
		var checks atomic.Int64
		d.cfg.AutoAnswerChecked = func() { checks.Add(1) }
		stop := runDriver(t, d)
		clock.advance(60 * time.Minute)
		n0 := checks.Load()
		waitUntil(t, "the due checks", func() bool { return checks.Load() >= n0+5 })
		stop()
		if got := autoReplies(autoAnswerLines(t, dir), ask.At); len(got) != 0 {
			t.Fatalf("an unverifiable lane was answered: %+v", got)
		}
	})
}

// runDriver starts d and answers a stop function that cancels it and waits
// for the loop to end (bounded), so a test's TempDir cleanup never races
// the driver's writes.
func runDriver(t *testing.T, d *Driver) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(5 * time.Second):
			}
		})
	}
	t.Cleanup(stop)
	return stop
}

// The loader reads `protected_stop` from a lane file, and the shipped
// lanes declare it on their human stops (owner ruling 2026-11-23 Q1 A).
func TestProtectedStopLanesDeclare(t *testing.T) {
	root := t.TempDir()
	dir := workflowtest.WriteLane(t, root, "probe", "acceptance: none\nsteps:\n  - id: stop\n    protected_stop: true\n  - id: ordinary\n")
	lane, errs := workflow.Load(dir)
	if len(errs) > 0 {
		t.Fatalf("the probe lane does not load: %v", errs)
	}
	if !lane.Step("stop").ProtectedStop || lane.Step("ordinary").ProtectedStop {
		t.Fatalf("protected_stop = %v / %v, want true / false", lane.Step("stop").ProtectedStop, lane.Step("ordinary").ProtectedStop)
	}

	want := map[string][]string{
		"develop":   {"publish", "merge"},
		"epic":      {"publish", "merge"},
		"micro":     {"publish", "merge"},
		"integrate": {"collect", "revert"},
	}
	for laneName, steps := range want {
		wf, err := LoadWorkflow(filepath.Join("..", "..", "..", "workflows", laneName))
		if err != nil {
			t.Fatalf("%s: %v", laneName, err)
		}
		for _, step := range steps {
			if !wf.ProtectedStop(step) {
				t.Errorf("%s step %s does not declare protected_stop", laneName, step)
			}
		}
	}
}
