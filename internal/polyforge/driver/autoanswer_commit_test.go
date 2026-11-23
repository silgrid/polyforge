package driver

// autoanswer_commit_test.go: the automatic reply's deadline, content and
// dedupe over a fake clock (autoanswer.go, polyforge#239). The card's
// recommendations are adopted verbatim after the REAL elapsed wait; an
// owner reply before the deadline wins; a restart keeps the original T0,
// while a wait with no recorded T0 is never auto-answered.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

type autoAnswerClock struct {
	mu sync.Mutex
	at time.Time
}

func (c *autoAnswerClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.at
}

func (c *autoAnswerClock) advance(d time.Duration) {
	c.mu.Lock()
	c.at = c.at.Add(d)
	c.mu.Unlock()
}

func autoAnswerLines(t *testing.T, dir string) []capsule.AskLine {
	t.Helper()
	lines, err := capsule.ReadAsks(dir)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

func autoReplies(lines []capsule.AskLine, askAt string) []capsule.AskLine {
	var out []capsule.AskLine
	for _, l := range lines {
		if l.Kind == capsule.AskKindReply && l.AskAt == askAt && l.By == capsule.AutoAnswerBy {
			out = append(out, l)
		}
	}
	return out
}

func TestAutoAnswerDeadlineAndRecommendations(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	clock := &autoAnswerClock{at: base}
	sp := &fakeSpawner{script: []fakeEntry{{hold: true}, {hold: true}}}
	d, log, _, _ := askEnv(t, dir, sp, nil, 150*time.Millisecond)
	d.cfg.Now = clock.now
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()
	waitUntil(t, "the first dispatch", func() bool { return len(sp.dispatched()) == 1 })
	n := readExec(t, filepath.Join(dir, capsule.ExecutionJSON)).Steps["fix"].Dispatch
	ask := writeAsk(t, dir, "fix", n, autoAnswerCardTwoRows)

	waitUntil(t, "the awaiting_owner event", func() bool { return len(log.byKind(EvAwaitingOwner)) == 1 })
	if t0, ok := capsule.AwaitingSince(autoAnswerLines(t, dir), "fix", n, ask.At, clock.now()); !ok || !t0.Equal(base) {
		t.Fatalf("recorded T0 = %v, %v; want the first observation %v", t0, ok, base)
	}

	// Before the deadline nothing is answered. The warm window ends the
	// process (a real 150ms) and the plan poll runs again at 29m59s: still
	// no reply.
	clock.advance(29*time.Minute + 59*time.Second)
	waitUntil(t, "the warm window to end the process", func() bool { return sp.runs[0].terminated() })
	if got := autoReplies(autoAnswerLines(t, dir), ask.At); len(got) != 0 {
		t.Fatalf("answered before the deadline: %+v", got)
	}

	// At 31m5s the reply lands, once, with the real wait (never the 30m
	// threshold) and one numbered line per recommendation.
	clock.advance(time.Minute + 6*time.Second)
	waitUntil(t, "the automatic reply", func() bool { return len(autoReplies(autoAnswerLines(t, dir), ask.At)) == 1 })
	reply := autoReplies(autoAnswerLines(t, dir), ask.At)[0]
	want := "自动：按推荐（超时 31m5s，owner 2026-11-23 授权）\n1. A：JSON\n2. B：两次"
	if reply.Text != want {
		t.Fatalf("reply text = %q, want %q", reply.Text, want)
	}
	if reply.Step != "fix" || reply.Spawn != n || reply.By != capsule.AutoAnswerBy {
		t.Fatalf("reply identity = %+v", reply)
	}
	waitUntil(t, "the resumed dispatch", func() bool { return len(sp.dispatched()) == 2 })
	time.Sleep(50 * time.Millisecond)
	if got := autoReplies(autoAnswerLines(t, dir), ask.At); len(got) != 1 {
		t.Fatalf("the reply was duplicated: %+v", got)
	}
	events := log.byKind(EvAutoAnswered)
	if len(events) != 1 || events[0].body["ask_at"] != ask.At || events[0].body["by"] != capsule.AutoAnswerBy || events[0].body["elapsed"] != "31m5s" {
		t.Fatalf("auto_answered events = %v", events)
	}
}

func TestAutoAnswerOwnerWins(t *testing.T) {
	dir := t.TempDir()
	base := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)
	clock := &autoAnswerClock{at: base}
	sp := &fakeSpawner{script: []fakeEntry{{hold: true}}}
	d, log, _, _ := askEnv(t, dir, sp, nil, 150*time.Millisecond)
	d.cfg.Now = clock.now
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = d.Run(ctx) }()
	waitUntil(t, "the dispatch", func() bool { return len(sp.dispatched()) == 1 })
	n := readExec(t, filepath.Join(dir, capsule.ExecutionJSON)).Steps["fix"].Dispatch
	ask := writeAsk(t, dir, "fix", n, autoAnswerCardTwoRows)
	waitUntil(t, "the awaiting_owner event", func() bool { return len(log.byKind(EvAwaitingOwner)) == 1 })

	// The owner writes first (the relay appends the reply): the question is
	// closed and the standing authorization must not answer it too.
	owner := capsule.AskLine{At: clock.now().Add(time.Second).UTC().Format(time.RFC3339Nano), Step: "fix", Spawn: n,
		Kind: capsule.AskKindReply, AskAt: ask.At, Text: "我用 A，但不是 JSON", By: "alice"}
	if err := capsule.AppendAsk(dir, owner); err != nil {
		t.Fatal(err)
	}
	clock.advance(60 * time.Minute)
	time.Sleep(200 * time.Millisecond)
	if got := autoReplies(autoAnswerLines(t, dir), ask.At); len(got) != 0 {
		t.Fatalf("an owner reply did not win: %+v", got)
	}
}

// seedAutoAnswerCapsule writes a capsule whose step fix is in_progress on
// dispatch n, with an open ask and (optionally) its recorded T0.
func seedAutoAnswerCapsule(t *testing.T, dir, step string, n int64, base time.Time, withAwaiting bool) capsule.AskLine {
	t.Helper()
	ex := capsule.Execution{WorkItemID: "wi_run", CurrentStep: step, WorkflowStatus: capsule.WorkflowStatusRunning,
		Steps: map[string]*capsule.Step{step: {Status: capsule.StepInProgress, Dispatch: n}}}
	raw, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, capsule.ExecutionJSON)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, capsule.ExecutionJSON), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	ask := capsule.AskLine{At: base.Add(-time.Minute).UTC().Format(time.RFC3339Nano), Step: step, Spawn: n, Kind: capsule.AskKindAsk, Text: autoAnswerCardTwoRows}
	if err := capsule.AppendAsk(dir, ask); err != nil {
		t.Fatal(err)
	}
	if withAwaiting {
		if err := capsule.AppendAsk(dir, capsule.AskLine{At: base.UTC().Format(time.RFC3339Nano), Step: step, Spawn: n,
			Kind: capsule.AskKindAwaiting, AskAt: ask.At}); err != nil {
			t.Fatal(err)
		}
	}
	return ask
}

func TestAutoAnswerWarmColdRestart(t *testing.T) {
	base := time.Date(2026, 11, 23, 0, 0, 0, 0, time.UTC)

	t.Run("a restart keeps the original T0", func(t *testing.T) {
		dir := t.TempDir()
		clock := &autoAnswerClock{at: base.Add(20 * time.Minute)} // restarted 20m in
		ask := seedAutoAnswerCapsule(t, dir, "fix", 1, base, true)
		sp := &fakeSpawner{script: []fakeEntry{{hold: true}}}
		d, log, _, _ := askEnv(t, dir, sp, nil, 150*time.Millisecond)
		d.cfg.Now = clock.now
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = d.Run(ctx) }()
		waitUntil(t, "the resumed park", func() bool { return len(log.byKind(EvAwaitingOwner)) == 1 })
		clock.advance(10*time.Minute + time.Second) // 30m1s after the original T0
		waitUntil(t, "the automatic reply after the restart", func() bool {
			return len(autoReplies(autoAnswerLines(t, dir), ask.At)) == 1
		})
		reply := autoReplies(autoAnswerLines(t, dir), ask.At)[0]
		if !filepath.IsAbs(dir) || reply.Text == "" || reply.AskAt != ask.At {
			t.Fatalf("reply %+v", reply)
		}
	})

	t.Run("an old wait without a trustworthy T0 is never answered", func(t *testing.T) {
		dir := t.TempDir()
		clock := &autoAnswerClock{at: base}
		ask := seedAutoAnswerCapsule(t, dir, "fix", 1, base, false)
		sp := &fakeSpawner{script: []fakeEntry{{hold: true}}}
		d, log, _, _ := askEnv(t, dir, sp, nil, 150*time.Millisecond)
		d.cfg.Now = clock.now
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() { _ = d.Run(ctx) }()
		waitUntil(t, "the resumed park", func() bool { return len(log.byKind(EvAwaitingOwner)) == 1 })
		clock.advance(60 * time.Minute)
		time.Sleep(200 * time.Millisecond)
		if got := autoReplies(autoAnswerLines(t, dir), ask.At); len(got) != 0 {
			t.Fatalf("an old wait was auto-answered: %+v", got)
		}
	})
}
