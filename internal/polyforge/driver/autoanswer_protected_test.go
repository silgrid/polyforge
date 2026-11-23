package driver

// autoanswer_protected_test.go: the protected stops (autoanswer.go;
// polyforge#239, owner ruling 2026-11-23 Q1 A). A low-star card on a
// confirm_plan build stop, a publish owner-review stop, a human merge, or
// a lane this build cannot verify keeps waiting for the owner; an ordinary
// step of a known lane is answered.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

func TestAutoAnswerProtectedStops(t *testing.T) {
	cases := []struct {
		name                  string
		protected, verifiable bool
		content               string
		want                  bool
	}{
		{"gate step", true, true, "ordinary", true},
		{"protected_stop step", true, true, "ordinary", true},
		{"unverifiable lane", false, false, "ordinary", true},
		{"ordinary non-gate step", false, true, "ordinary", false},
		{"confirm_plan ticket protects a non-gate step", false, true, "ordinary\n\nconfirm_plan: true\n\n## Acceptance\n- AC1: x\n", true},
		{"backticked confirm_plan with explanation", false, true, "the ticket says `confirm_plan: true` (plan confirmation)", true},
		{"case-insensitive confirm_plan", false, true, "CONFIRM_PLAN = TRUE", true},
		{"a mention without true does not protect", false, true, "confirm_plan is discussed here", false},
	}
	for _, tc := range cases {
		if got := autoAnswerProtectedStop(tc.protected, tc.verifiable, tc.content); got != tc.want {
			t.Errorf("%s: protected = %v, want %v", tc.name, got, tc.want)
		}
	}

	// The driver consults the rule: a publish stop with an all-★ card is
	// not answered even after 60m.
	dir := t.TempDir()
	base := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	clock := &autoAnswerClock{at: base.Add(20 * time.Minute)}
	ask := seedProtectedCapsule(t, dir, "publish", "ordinary ticket")
	sp := &fakeSpawner{script: []fakeEntry{{hold: true}}}
	log := &safeLog{}
	ow := &ownerWaits{}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: filepath.Join(dir, capsule.ExecutionJSON),
		Workflow: &Workflow{Name: "develop", MaxRewinds: 4, Steps: []StepSpec{{ID: "publish", Gate: true}}},
		Spawner:  sp, Log: log, Link: supervise.NewLink("wi_run"),
		Limits:    supervise.Defaults{AskWarmWindow: 150 * time.Millisecond},
		AskPoll:   5 * time.Millisecond,
		OwnerWait: ow.set,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d.cfg.Now = clock.now
	d.cfg.LaneVerifiable = func(string) bool { return true }
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = d.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	})
	waitUntil(t, "the publish park", func() bool { return len(log.byKind(EvAwaitingOwner)) == 1 })
	clock.advance(60 * time.Minute)
	time.Sleep(200 * time.Millisecond)
	if got := autoReplies(autoAnswerLines(t, dir), ask.At); len(got) != 0 {
		t.Fatalf("a protected publish stop was answered: %+v", got)
	}
}

// seedProtectedCapsule writes a capsule in progress on step with an open
// ask and its recorded T0, plus an ordinary work-item content.
func seedProtectedCapsule(t *testing.T, dir, step, content string) capsule.AskLine {
	t.Helper()
	base := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	writeAutoAnswerWorkItem(t, dir, content)
	fix := &capsule.Execution{WorkItemID: "wi_run", CurrentStep: step, WorkflowStatus: capsule.WorkflowStatusRunning,
		Steps: map[string]*capsule.Step{step: {Status: capsule.StepInProgress, Dispatch: 1}}}
	raw, err := json.Marshal(fix)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, capsule.ExecutionJSON)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, capsule.ExecutionJSON), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	ask := capsule.AskLine{At: base.Add(-time.Minute).UTC().Format(time.RFC3339Nano), Step: step, Spawn: 1, Kind: capsule.AskKindAsk, Text: autoAnswerCardTwoRows}
	if err := capsule.AppendAsk(dir, ask); err != nil {
		t.Fatal(err)
	}
	if err := capsule.AppendAsk(dir, capsule.AskLine{At: base.UTC().Format(time.RFC3339Nano), Step: step, Spawn: 1,
		Kind: capsule.AskKindAwaiting, AskAt: ask.At}); err != nil {
		t.Fatal(err)
	}
	return ask
}
