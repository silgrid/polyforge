package liveconfig

import (
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/mapping"
)

func TestStateDefaultsAndSwaps(t *testing.T) {
	var nilState *State
	if nilState.Mapping() == nil || nilState.ClaudeBin() != "" || nilState.StepTimeout() != 0 {
		t.Fatal("a nil state must answer harmless zero values")
	}
	p1, err := mapping.Build([]mapping.Entry{{Workspace: t.TempDir(), Projects: []string{"a"}}})
	if err != nil {
		t.Fatal(err)
	}
	s := New(p1, "claude-1", 30*time.Minute, 5*time.Minute)
	if s.Mapping() != p1 || s.ClaudeBin() != "claude-1" || s.StepTimeout() != 30*time.Minute ||
		s.StallWindow() != 5*time.Minute {
		t.Fatalf("New state = %+v", s)
	}
	p2, err := mapping.Build([]mapping.Entry{{Workspace: t.TempDir(), Projects: []string{"b"}}})
	if err != nil {
		t.Fatal(err)
	}
	s.SetMapping(p2)
	s.SetClaudeBin("claude-2")
	s.SetStepTimeout(45 * time.Minute)
	s.SetStallWindow(0)
	if s.Mapping() != p2 || s.ClaudeBin() != "claude-2" || s.StepTimeout() != 45*time.Minute ||
		s.StallWindow() != 0 {
		t.Fatalf("after swaps = %+v", s)
	}
	s.SetMapping(nil)
	if s.Mapping() == nil {
		t.Fatal("SetMapping(nil) must leave a non-nil plan")
	}
}

func TestStateConcurrent(t *testing.T) {
	s := New(&mapping.Plan{}, "", 0, 0)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 18; j++ {
				s.SetClaudeBin("x")
				_ = s.ClaudeBin()
				s.SetStepTimeout(time.Duration(j+1) * time.Minute)
				_ = s.StepTimeout()
				_ = s.Mapping()
			}
		}()
	}
	wg.Wait()
}
