package task

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// TestSlotsBoundIsShared: the shared bound admits Limit holders in total,
// whichever manager asks, and a nil Slots never refuses.
func TestSlotsBoundIsShared(t *testing.T) {
	s := &Slots{}
	for i := 0; i < Limit; i++ {
		if !s.acquire() {
			t.Fatalf("slot %d refused below the bound", i)
		}
	}
	if s.acquire() {
		t.Fatal("the shared bound was passed")
	}
	s.release()
	if s.Running() != Limit-1 || !s.acquire() {
		t.Fatal("a released slot must be reusable")
	}
	var none *Slots
	if !none.acquire() {
		t.Fatal("a nil Slots must not refuse")
	}
	none.release()
}

// TestSpawnAdmitAndDone: a spawn asks Admit before it takes a slot (its
// refusal is the spawn's answer, nothing started) and a finished task
// returns the reservation through Done exactly once.
func TestSpawnAdmitAndDone(t *testing.T) {
	var mu sync.Mutex
	admitted, done := 0, 0
	refuse := errors.New("ceiling taken")
	var admitErr error
	m := NewManager(Config{
		Store:   NewStore(t.TempDir()),
		Runner:  fakeRunner{func(context.Context, RunSpec) (RunResult, error) { return RunResult{Report: "ok", Tokens: 1}, nil }},
		Resolve: func(string) ([]string, string, error) { return []string{"pi:a/b"}, "k", nil },
		WorkDir: func(string) (string, error) { return t.TempDir(), nil },
		Admit: func() error {
			mu.Lock()
			defer mu.Unlock()
			if admitErr != nil {
				return admitErr
			}
			admitted++
			return nil
		},
		Done: func() { mu.Lock(); done++; mu.Unlock() },
	})
	t.Cleanup(m.Close)
	admitErr = refuse
	if _, err := m.Spawn(Spec{Project: "p", Prompt: "hi"}); !errors.Is(err, refuse) {
		t.Fatalf("a refused admission must refuse the spawn: %v", err)
	}
	mu.Lock()
	admitErr = nil
	mu.Unlock()
	tk, err := m.Spawn(Spec{Project: "p", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, tk.ID, StateCompleted)
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		a, d := admitted, done
		mu.Unlock()
		if a == 1 && d == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("admitted %d, done %d — want 1 and 1", a, d)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
