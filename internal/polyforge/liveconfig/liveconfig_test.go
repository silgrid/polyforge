package liveconfig

import (
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
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

// TestSummarizeAndPolicyCopies pins the two cli.toml table families on the
// live state: nil means absent (lane off / built-in
// policy), getters hand out copies, a swap is whole.
func TestSummarizeAndPolicyCopies(t *testing.T) {
	s := New(nil, "", 0, 0)
	if models, lang, iv := s.Summarize(); models != nil || lang != workspace.DefaultSummarizeLanguage || iv != workspace.DefaultSummarizeIntervalSec {
		t.Fatalf("an absent table = %v %q %d", models, lang, iv)
	}
	in := &workspace.SummarizeSettings{Models: []string{"pi:a"}, Language: "English", IntervalSec: 30}
	s.SetSummarize(in)
	in.Models[0] = "pi:mutated"
	models, lang, iv := s.Summarize()
	if models[0] != "pi:a" || lang != "English" || iv != 30 {
		t.Fatalf("summarize = %v %q %d", models, lang, iv)
	}
	models[0] = "pi:mutated"
	if again, _, _ := s.Summarize(); again[0] != "pi:a" {
		t.Fatal("Summarize must answer a copy")
	}
	s.SetMemoryPolicy(map[string]map[string]workspace.PolicyEntry{"/ws": {"default": {Record: []string{"r"}}}})
	got := s.MemoryPolicyOf("/ws")
	got["default"].Record[0] = "mutated"
	if s.MemoryPolicyOf("/ws")["default"].Record[0] != "r" || s.MemoryPolicyOf("/ws")["default"].Skip != nil {
		t.Fatal("MemoryPolicyOf must answer a copy keeping undeclared lists nil")
	}
	if s.MemoryPolicyOf("/other") != nil {
		t.Fatal("an undeclared path must answer nil")
	}
}
