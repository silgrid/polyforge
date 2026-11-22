package task

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

type fakeRunner struct {
	fn func(ctx context.Context, spec RunSpec) (RunResult, error)
}

func (f fakeRunner) Run(ctx context.Context, spec RunSpec, _ func(int64)) (RunResult, error) {
	return f.fn(ctx, spec)
}

func newTestManager(t *testing.T, r Runner, cands []string) (*Manager, func() int) {
	t.Helper()
	dir := t.TempDir()
	var mu sync.Mutex
	var terms []Task
	m := NewManager(Config{
		Store:   NewStore(dir),
		Runner:  r,
		Resolve: func(string) ([]string, string, error) { return cands, "steps.p.task.models", nil },
		WorkDir: func(string) (string, error) { return t.TempDir(), nil },
		OnTerminal: func(t Task) {
			mu.Lock()
			terms = append(terms, t)
			mu.Unlock()
		},
	})
	t.Cleanup(m.Close)
	return m, func() int {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			mu.Lock()
			n := len(terms)
			mu.Unlock()
			if n > 0 {
				return n
			}
			time.Sleep(5 * time.Millisecond)
		}
		return 0
	}
}

func waitState(t *testing.T, m *Manager, id string, want State) Task {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		tk, err := m.Show(id)
		if err == nil && tk.State == want {
			return tk
		}
		time.Sleep(10 * time.Millisecond)
	}
	tk, _ := m.Show(id)
	t.Fatalf("task %s state = %s, want %s", id, tk.State, want)
	return tk
}

func TestManagerCompleted(t *testing.T) {
	m, terms := newTestManager(t, fakeRunner{func(_ context.Context, s RunSpec) (RunResult, error) {
		return RunResult{Report: "all good", Tokens: 7}, nil
	}}, []string{"pi:a/b"})
	tk, err := m.Spawn(Spec{Project: "p", Prompt: "hi"})
	if err != nil || !ValidID(tk.ID) || tk.State != StateRunning {
		t.Fatalf("spawn: %+v %v", tk, err)
	}
	done := waitState(t, m, tk.ID, StateCompleted)
	if done.Report != "all good" || done.TokensConsumed != 7 {
		t.Fatalf("done = %+v", done)
	}
	if terms() != 1 {
		t.Fatalf("OnTerminal calls = %d", terms())
	}
}

func TestManagerDropsFinishedTasksFromMemory(t *testing.T) {
	m, terms := newTestManager(t, fakeRunner{func(context.Context, RunSpec) (RunResult, error) {
		return RunResult{Report: "done", Tokens: 1}, nil
	}}, []string{"pi:a/b"})
	var ids []string
	for i := 0; i < 4; i++ {
		tk, err := m.Spawn(Spec{Project: "p", Prompt: "hi"})
		if err != nil {
			t.Fatal(err)
		}
		waitState(t, m, tk.ID, StateCompleted)
		ids = append(ids, tk.ID)
	}
	terms()
	m.mu.Lock()
	inMem := len(m.tasks)
	m.mu.Unlock()
	if inMem != 0 {
		t.Fatalf("finished tasks must be dropped from memory; %d entries left", inMem)
	}
	// Every finished task is still readable from the store (the one home
	// of a finished task), and the list is the store's list.
	for _, id := range ids {
		got, err := m.Show(id)
		if err != nil || got.State != StateCompleted {
			t.Fatalf("show %s = %+v %v", id, got, err)
		}
	}
	listed, err := m.List()
	if err != nil || len(listed) != len(ids) {
		t.Fatalf("list = %d entries (%v), want %d", len(listed), err, len(ids))
	}
}

func TestManagerRotationFailedAndCapped(t *testing.T) {
	calls := 0
	m, _ := newTestManager(t, fakeRunner{func(_ context.Context, s RunSpec) (RunResult, error) {
		calls++
		if calls == 1 {
			return RunResult{Tokens: 10}, errors.New("model unavailable")
		}
		return RunResult{Report: "second", Tokens: 5}, nil
	}}, []string{"pi:a/b", "pi:c/d"})
	tk, _ := m.Spawn(Spec{Project: "p", Prompt: "hi"})
	done := waitState(t, m, tk.ID, StateCompleted)
	if done.TokensConsumed != 15 || done.Report != "second" {
		t.Fatalf("rotation shares one budget: %+v", done)
	}

	m2, _ := newTestManager(t, fakeRunner{func(context.Context, RunSpec) (RunResult, error) {
		return RunResult{}, errors.New("boom")
	}}, []string{"pi:a/b"})
	tk, _ = m2.Spawn(Spec{Project: "p", Prompt: "hi"})
	f := waitState(t, m2, tk.ID, StateFailed)
	if !strings.Contains(f.Error, "boom") {
		t.Fatalf("failed error = %q", f.Error)
	}

	m3, _ := newTestManager(t, fakeRunner{func(context.Context, RunSpec) (RunResult, error) {
		return RunResult{Report: "partial", Tokens: 100, TokenCapped: true}, nil
	}}, []string{"pi:a/b"})
	tk, _ = m3.Spawn(Spec{Project: "p", Prompt: "hi", MaxTokens: 100})
	c := waitState(t, m3, tk.ID, StateCapped)
	if !c.Truncated || c.Report != "partial" {
		t.Fatalf("capped keeps truncated output: %+v", c)
	}
}

func TestManagerWallCapCancelAndLimit(t *testing.T) {
	block := make(chan struct{})
	m, _ := newTestManager(t, fakeRunner{func(ctx context.Context, s RunSpec) (RunResult, error) {
		select {
		case <-ctx.Done():
		case <-block:
		}
		return RunResult{Report: "so far"}, nil
	}}, []string{"pi:a/b"})
	defer close(block)
	a, _ := m.Spawn(Spec{Project: "p", Prompt: "1", MaxDuration: 300 * time.Millisecond})
	b, err := m.Spawn(Spec{Project: "p", Prompt: "2"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Spawn(Spec{Project: "p", Prompt: "3"}); !errors.Is(err, ErrLimit) {
		t.Fatalf("third spawn must be refused: %v", err)
	}
	waitState(t, m, a.ID, StateCapped)
	if err := m.Cancel(b.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, m, b.ID, StateInterrupted)
	if err := m.Cancel(b.ID); !errors.Is(err, ErrNotRunning) {
		t.Fatalf("cancel of an ended task: %v", err)
	}
	s := m.Summarize()
	if s.Running != 0 || s.Capped != 1 || s.Interrupted != 1 || s.Limit != 2 {
		t.Fatalf("summary = %+v", s)
	}
}

func TestManagerBudgetRefusalsAndResolve(t *testing.T) {
	m, _ := newTestManager(t, fakeRunner{}, []string{"pi:a/b"})
	if _, err := m.Spawn(Spec{Project: "p", Prompt: "x", MaxDuration: 2 * time.Hour}); err == nil {
		t.Fatal("2h must be refused")
	}
	if _, err := m.Spawn(Spec{Project: "p", Prompt: "x", MaxTokens: 2000000}); err == nil {
		t.Fatal("2000000 tokens must be refused")
	}
	m.cfg.Resolve = func(string) ([]string, string, error) { return nil, "", errors.New("steps.p.task.models missing") }
	if _, err := m.Spawn(Spec{Project: "p", Prompt: "x"}); err == nil || !strings.Contains(err.Error(), "steps.p.task.models") {
		t.Fatalf("err = %v", err)
	}
}

func TestManagerRecoverMarksInterrupted(t *testing.T) {
	m, terms := newTestManager(t, fakeRunner{}, []string{"pi:a/b"})
	running := Task{ID: "task_00000000000000aa", Spec: Spec{Project: "p"}, State: StateRunning, CreatedAt: time.Now()}
	if err := m.cfg.Store.Save(running); err != nil {
		t.Fatal(err)
	}
	if err := m.Recover(); err != nil {
		t.Fatal(err)
	}
	got, err := m.Show(running.ID)
	if err != nil || got.State != StateInterrupted {
		t.Fatalf("recovered = %+v %v", got, err)
	}
	if terms() != 1 {
		t.Fatalf("terminal notices = %d", terms())
	}
}

func TestManagerWaitAnswersTheTerminalRecord(t *testing.T) {
	release := make(chan struct{})
	var seen RunSpec
	var mu sync.Mutex
	m, _ := newTestManager(t, fakeRunner{func(_ context.Context, s RunSpec) (RunResult, error) {
		mu.Lock()
		seen = s
		mu.Unlock()
		<-release
		return RunResult{Report: "the report", Tokens: 4}, nil
	}}, []string{"pi:a/b"})
	tk, err := m.Spawn(Spec{Project: "p", Prompt: "hi", ReadOnlyTools: true})
	if err != nil {
		t.Fatal(err)
	}
	got := make(chan Task, 1)
	errs := make(chan error, 1)
	go func() {
		done, err := m.Wait(context.Background(), tk.ID)
		errs <- err
		got <- done
	}()
	// The waiter is registered on the live entry before the run ends.
	deadline := time.Now().Add(5 * time.Second)
	for {
		m.mu.Lock()
		n := len(m.waiters[tk.ID])
		m.mu.Unlock()
		if n == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Wait did not register a waiter on the running task")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	if err := <-errs; err != nil {
		t.Fatal(err)
	}
	done := <-got
	if done.State != StateCompleted || done.Report != "the report" || !done.ReadOnlyTools {
		t.Fatalf("wait answered %+v", done)
	}
	mu.Lock()
	ro := seen.ReadOnlyTools
	mu.Unlock()
	if !ro {
		t.Fatal("the runner must receive the spawn's read-only profile")
	}
	m.mu.Lock()
	left := len(m.waiters)
	m.mu.Unlock()
	if left != 0 {
		t.Fatalf("waiters left after the terminal transition: %d", left)
	}
	// A finished task is read from the store.
	again, err := m.Wait(context.Background(), tk.ID)
	if err != nil || again.State != StateCompleted || again.Report != "the report" {
		t.Fatalf("wait on a finished task = %+v %v", again, err)
	}
}

func TestManagerWaitContextAndUnknownID(t *testing.T) {
	release := make(chan struct{})
	m, _ := newTestManager(t, fakeRunner{func(ctx context.Context, s RunSpec) (RunResult, error) {
		select {
		case <-release:
		case <-ctx.Done():
		}
		return RunResult{Report: "late", Tokens: 1}, nil
	}}, []string{"pi:a/b"})
	defer close(release)
	if _, err := m.Wait(context.Background(), "task_00000000000000ff"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown id: %v", err)
	}
	tk, err := m.Spawn(Spec{Project: "p", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := m.Wait(ctx, tk.ID); err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("a bounded wait must end with ctx: %v", err)
	}
	// The wait's end never touches the task: it is still running.
	if cur, _ := m.Show(tk.ID); cur.State != StateRunning {
		t.Fatalf("task state after the wait ended = %s", cur.State)
	}
	m.mu.Lock()
	left := len(m.waiters)
	m.mu.Unlock()
	if left != 0 {
		t.Fatalf("an abandoned waiter must be dropped: %d left", left)
	}
}

// TestTaskContextLifecycle pins the running task's context window
// (polyforge#230 AC1): a value an attempt reports is on the record while it
// runs, a candidate switch starts the next attempt with none until it reports
// its own, the terminal transition leaves none, a recovered task carries none,
// and the wire value is the five keys only — the report's own text never
// bleeds into it.
func TestTaskContextLifecycle(t *testing.T) {
	second := make(chan struct{}, 1)
	r := fakeRunner{fn: func(ctx context.Context, s RunSpec) (RunResult, error) {
		if s.Candidate.Model == "a/one" {
			if s.OnContext != nil {
				tokens := int64(4300)
				s.OnContext(&protocol.ContextUsage{Tokens: &tokens, Model: "openai/m1", AsOf: "2026-11-22T10:00:00Z"})
			}
			return RunResult{Tokens: 3}, errors.New("first candidate failed")
		}
		second <- struct{}{}
		<-ctx.Done()
		return RunResult{Report: `{"context":{"tokens":1}} secret`, Tokens: 1}, nil
	}}
	m, _ := newTestManager(t, r, []string{"pi:a/one", "pi:a/two"})
	tk, err := m.Spawn(Spec{Project: "p", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	// while the first attempt runs: the reported value is on the record
	deadline := time.Now().Add(10 * time.Second)
	var live Task
	for time.Now().Before(deadline) {
		live, _ = m.Show(tk.ID)
		if live.Context != nil {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if live.Context == nil || live.Context.Tokens == nil || *live.Context.Tokens != 4300 || live.Context.Model != "openai/m1" ||
		live.Context.AsOf != "2026-11-22T10:00:00Z" {
		t.Fatalf("while running: context = %#v, want tokens 4300 of openai/m1 and its as_of", live.Context)
	}
	listed, _ := m.List()
	if len(listed) != 1 || listed[0].Context == nil {
		t.Fatalf("the list lacks the running context: %+v", listed)
	}
	// the candidate switch starts the next attempt with no context of its own
	select {
	case <-second:
	case <-time.After(10 * time.Second):
		t.Fatal("the second candidate never started")
	}
	if got, _ := m.Show(tk.ID); got.Context != nil {
		t.Fatalf("the switched attempt carries the previous context: %#v", got.Context)
	}
	if err := m.Cancel(tk.ID); err != nil {
		t.Fatal(err)
	}
	done := waitState(t, m, tk.ID, StateInterrupted)
	if done.Context != nil {
		t.Fatalf("a terminal task carries context: %#v", done.Context)
	}
	// a completed task carries none either
	m2, _ := newTestManager(t, fakeRunner{fn: func(context.Context, RunSpec) (RunResult, error) {
		return RunResult{Report: "done", Tokens: 1}, nil
	}}, []string{"pi:a/one"})
	tk2, err := m2.Spawn(Spec{Project: "p", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if got := waitState(t, m2, tk2.ID, StateCompleted); got.Context != nil {
		t.Fatalf("a completed task carries context: %#v", got.Context)
	}

	// a record the store still holds as running is recovered interrupted with
	// no inherited context
	now := time.Now().UTC()
	tokens := int64(99)
	stale := Task{ID: "task_0123456789abcdea", State: StateRunning, CreatedAt: now, Context: &protocol.ContextUsage{Tokens: &tokens, Model: "m"}}
	if err := m.cfg.Store.Save(stale); err != nil {
		t.Fatal(err)
	}
	if err := m.Recover(); err != nil {
		t.Fatal(err)
	}
	recovered, _ := m.Show(stale.ID)
	if recovered.State != StateInterrupted || recovered.Context != nil {
		t.Fatalf("recovered = %+v, want interrupted with no context", recovered)
	}

	// the wire shape is the five keys and never the report's text
	tokens = 4300
	pct := 2.15
	window := int64(200000)
	raw, err := json.Marshal(Task{ID: tk.ID, Report: `{"context":{"tokens":1}} secret`, Context: &protocol.ContextUsage{
		Tokens: &tokens, Window: &window, Percent: &pct, Model: "openai/m1", AsOf: "2026-11-22T10:00:00Z"}})
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Context map[string]json.RawMessage `json:"context"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	want := []string{"as_of", "model", "percent", "tokens", "window"}
	if len(doc.Context) != len(want) {
		t.Fatalf("context keys = %v, want exactly %v", doc.Context, want)
	}
	for _, k := range want {
		if _, ok := doc.Context[k]; !ok {
			t.Fatalf("context keys = %v, want %v", doc.Context, want)
		}
	}
}
