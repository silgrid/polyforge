package task

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// w74_limit_test.go: the adjustable concurrency bound (polyforge#74).
// The bound's default is DefaultLimit (the unset [daemon]
// max_concurrent_tasks value), SetMax changes it live and a lowered bound
// refuses only new spawns; the refusal names the configured key, its value
// and the running count, keeps `limit` and `nothing is queued`, and wraps
// the ErrLimit sentinel.

// blockRunnerW74 holds every run until its context ends, so spawned tasks
// stay running and hold their slots.
type blockRunnerW74 struct{}

func (blockRunnerW74) Run(ctx context.Context, _ RunSpec, _ func(int64)) (RunResult, error) {
	<-ctx.Done()
	return RunResult{}, nil
}

// w74Manager builds one manager (one "Cell" of a Host) sharing s (nil = the
// manager's own check only).
func w74Manager(t *testing.T, s *Slots) *Manager {
	t.Helper()
	m := NewManager(Config{
		Store:   NewStore(t.TempDir()),
		Runner:  blockRunnerW74{},
		Resolve: func(string) ([]string, string, error) { return []string{"pi:a/b"}, "steps.p.task.models", nil },
		WorkDir: func(string) (string, error) { return t.TempDir(), nil },
		Slots:   s,
	})
	t.Cleanup(m.Close)
	return m
}

// TestW74SlotsAdjustableBound: the zero value takes the default, SetMax
// raises and lowers the bound live, a lowered bound refuses only new
// acquires (holders keep running), and 0 restores the default.
func TestW74SlotsAdjustableBound(t *testing.T) {
	s := &Slots{}
	if got := s.Max(); got != DefaultLimit {
		t.Fatalf("an unset bound is the default %d, got %d", DefaultLimit, got)
	}
	s.SetMax(3)
	if got := s.Max(); got != 3 {
		t.Fatalf("SetMax(3) = %d", got)
	}
	for i := 0; i < 3; i++ {
		if !s.acquire() {
			t.Fatalf("slot %d refused below the raised bound", i)
		}
	}
	if s.acquire() {
		t.Fatal("the raised bound was passed")
	}
	// lowering never touches holders: the three keep running, a new
	// acquire is refused, and the bound reports the lowered value
	s.SetMax(2)
	if got := s.Running(); got != 3 {
		t.Fatalf("a lowered bound must not release holders, running %d", got)
	}
	if got := s.Max(); got != 2 {
		t.Fatalf("the lowered bound reports 2, got %d", got)
	}
	if s.acquire() {
		t.Fatal("a lowered bound must refuse new acquires")
	}
	s.release()
	if !s.acquire() {
		t.Fatal("a released slot is reusable below the lowered bound")
	}
	s.SetMax(0)
	if got := s.Max(); got != DefaultLimit {
		t.Fatalf("SetMax(0) must restore the default %d, got %d", DefaultLimit, got)
	}
	var none *Slots
	none.SetMax(3) // must not panic
	if got := none.Max(); got != DefaultLimit {
		t.Fatalf("a nil Slots reports the default %d, got %d", DefaultLimit, got)
	}
}

// TestW74ManagerSharedBoundAndRefusal: several managers share one bound;
// the refusal names the running count, the configured key and its value,
// keeps `limit` and `nothing is queued`, and wraps the sentinel. A raised
// bound admits the next spawn at once; a lowered one keeps the running
// tasks and refuses the next; one released slot admits one new spawn.
func TestW74ManagerSharedBoundAndRefusal(t *testing.T) {
	s := &Slots{}
	a, b := w74Manager(t, s), w74Manager(t, s)
	if _, err := a.Spawn(Spec{Project: "p", Prompt: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := b.Spawn(Spec{Project: "p", Prompt: "2"}); err != nil {
		t.Fatal(err)
	}
	_, err := a.Spawn(Spec{Project: "p", Prompt: "3"})
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("the third spawn at the default bound must refuse with ErrLimit: %v", err)
	}
	for _, want := range []string{"2 tasks already running", "the limit is 2", "max_concurrent_tasks", "nothing is queued"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal must name %q: %q", want, err.Error())
		}
	}
	// live raise: the next spawn is admitted at once
	s.SetMax(3)
	third, err := b.Spawn(Spec{Project: "p", Prompt: "3"})
	if err != nil {
		t.Fatal(err)
	}
	// live lower: the three keep running, the next spawn is refused
	// naming the running count and the configured value
	s.SetMax(2)
	_, err = a.Spawn(Spec{Project: "p", Prompt: "4"})
	if !errors.Is(err, ErrLimit) {
		t.Fatalf("a lowered bound must refuse new spawns with ErrLimit: %v", err)
	}
	for _, want := range []string{"3 tasks already running", "the limit is 2"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal must name %q: %q", want, err.Error())
		}
	}
	if err := b.Cancel(third.ID); err != nil {
		t.Fatal(err)
	}
	waitState(t, b, third.ID, StateInterrupted)
	if _, err := a.Spawn(Spec{Project: "p", Prompt: "4"}); err != nil {
		t.Fatalf("one released slot must admit one new spawn: %v", err)
	}
}

// TestW74ManagerOwnCheckReadsTheBound: without a shared Slots the
// manager's own check reads the same default source, and Summarize
// carries the bound's current value.
func TestW74ManagerOwnCheckReadsTheBound(t *testing.T) {
	m := w74Manager(t, nil)
	if _, err := m.Spawn(Spec{Project: "p", Prompt: "1"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Spawn(Spec{Project: "p", Prompt: "2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Spawn(Spec{Project: "p", Prompt: "3"}); !errors.Is(err, ErrLimit) {
		t.Fatalf("the own check reads the default bound: %v", err)
	}
	if got := m.Summarize().Limit; got != DefaultLimit {
		t.Fatalf("Summarize's limit is the default %d, got %d", DefaultLimit, got)
	}
}

// TestW74SummarizeCarriesLiveBound: Summarize reports the shared bound's
// current value, following SetMax live.
func TestW74SummarizeCarriesLiveBound(t *testing.T) {
	s := &Slots{}
	s.SetMax(5)
	m := w74Manager(t, s)
	if got := m.Summarize().Limit; got != 5 {
		t.Fatalf("Summarize's limit = %d, want the live bound 5", got)
	}
	s.SetMax(0)
	if got := m.Summarize().Limit; got != DefaultLimit {
		t.Fatalf("Summarize's limit = %d, want the restored default %d", got, DefaultLimit)
	}
}
