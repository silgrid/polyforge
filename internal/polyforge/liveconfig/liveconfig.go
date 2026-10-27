// Package liveconfig holds the daemon's reloadable settings behind one
// mutex so a running daemon can adopt a validated cli.toml change without
// a restart (workbench#3). It is deliberately tiny: a leaf that imports
// only mapping and stdlib, read by the daemon, the runtime and the step
// spawners.
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
)

// State is the daemon's reloadable configuration snapshot.
type State struct {
	mu          sync.RWMutex
	mapping     *mapping.Plan
	claudeBin   string
	stepTimeout time.Duration
	stallWindow time.Duration
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
