// Package liveconfig holds the daemon's reloadable settings behind one
// mutex so a running daemon can adopt a validated cli.toml change without
// a restart. It is deliberately tiny: a leaf that imports
// only mapping, the workspace value types and stdlib, read by the daemon,
// the runtime and the step spawners.
//
// Every getter is safe for concurrent use; the daemon writes the whole
// state once per successful reload. The plan is never mutated in place —
// a reload swaps the pointer under the lock, so a reader that already
// holds the old plan keeps a consistent view.
package liveconfig

import (
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// State is the daemon's reloadable configuration snapshot.
type State struct {
	mu          sync.RWMutex
	mapping     *mapping.Plan
	claudeBin   string
	stepTimeout time.Duration
	stallWindow time.Duration
	// summarize is cli.toml's [summarize] table (nil = absent: the lane
	// is off); memoryPolicy is the [memory_policy."<path>".<entry>] family
	//. Both are swapped whole, never mutated in place.
	summarize    *workspace.SummarizeSettings
	memoryPolicy map[string]map[string]workspace.PolicyEntry
}

// New builds a state from the startup values. A nil mapping is treated as
// an empty plan (never nil on the read side). A zero duration is
// "unset": the reader resolves it to the documented default.
func New(m *mapping.Plan, claudeBin string, stepTimeout, stallWindow time.Duration) *State {
	if m == nil {
		m = &mapping.Plan{}
	}
	return &State{
		mapping:     m,
		claudeBin:   claudeBin,
		stepTimeout: stepTimeout,
		stallWindow: stallWindow,
	}
}

// Mapping answers the current workspace→projects plan (never nil).
func (s *State) Mapping() *mapping.Plan {
	if s == nil {
		return &mapping.Plan{}
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.mapping == nil {
		return &mapping.Plan{}
	}
	return s.mapping
}

// SetMapping swaps the plan (a reload's next-claim half).
func (s *State) SetMapping(m *mapping.Plan) {
	if s == nil {
		return
	}
	if m == nil {
		m = &mapping.Plan{}
	}
	s.mu.Lock()
	s.mapping = m
	s.mu.Unlock()
}

// ClaudeBin answers the current claude binary ("" = the built-in default).
func (s *State) ClaudeBin() string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.claudeBin
}

// SetClaudeBin swaps the claude binary.
func (s *State) SetClaudeBin(v string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.claudeBin = v
	s.mu.Unlock()
}

// StepTimeout answers T (0 = the documented default).
func (s *State) StepTimeout() time.Duration {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stepTimeout
}

// StallWindow answers N (0 = the documented default).
func (s *State) StallWindow() time.Duration {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.stallWindow
}

// SetStepTimeout swaps T on its own (0 = unset: the default applies).
func (s *State) SetStepTimeout(v time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stepTimeout = v
	s.mu.Unlock()
}

// SetStallWindow swaps N on its own (0 = unset: the default applies;
// negative disables the check).
func (s *State) SetStallWindow(v time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.stallWindow = v
	s.mu.Unlock()
}

// Summarize answers the summarize lane's configuration: nil models when
// the [summarize] table is absent (the lane is off), else its values with
// the documented defaults for an absent optional key. No I/O.
func (s *State) Summarize() (models []string, language string, intervalSec int) {
	if s == nil {
		return (*workspace.SummarizeSettings)(nil).Lane()
	}
	s.mu.RLock()
	cur := s.summarize
	s.mu.RUnlock()
	return cur.Lane()
}

// SetSummarize swaps the [summarize] table (nil = absent).
func (s *State) SetSummarize(v *workspace.SummarizeSettings) {
	if s == nil {
		return
	}
	if v != nil {
		c := *v
		c.Models = append([]string{}, v.Models...)
		v = &c
	}
	s.mu.Lock()
	s.summarize = v
	s.mu.Unlock()
}

// MemoryPolicyOf answers one workspace path's declared memory_policy
// entries (a copy; nil = none declared: the built-in default applies).
func (s *State) MemoryPolicyOf(workspacePath string) map[string]workspace.PolicyEntry {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	src := s.memoryPolicy[workspacePath]
	s.mu.RUnlock()
	if src == nil {
		return nil
	}
	out := make(map[string]workspace.PolicyEntry, len(src))
	for k, e := range src {
		out[k] = workspace.PolicyEntry{Record: cloneList(e.Record), Skip: cloneList(e.Skip)}
	}
	return out
}

// SetMemoryPolicy swaps the whole memory_policy family (a reload's
// next-claim half; the caller hands over a freshly parsed map).
func (s *State) SetMemoryPolicy(v map[string]map[string]workspace.PolicyEntry) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.memoryPolicy = v
	s.mu.Unlock()
}

// cloneList copies a list, keeping nil (undeclared) nil.
func cloneList(l []string) []string {
	if l == nil {
		return nil
	}
	return append([]string{}, l...)
}
