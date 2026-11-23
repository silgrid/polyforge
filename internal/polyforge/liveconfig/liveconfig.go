// Package liveconfig holds the daemon's reloadable settings behind one
// mutex so a running daemon can adopt a validated cli.toml change without
// a restart. It is deliberately tiny: a leaf that imports
// only mapping, the workspace value types, the agent-language vocabulary
// (stepcall) and stdlib, read by the daemon,
// the runtime and the step spawners.
//
// Every getter is safe for concurrent use; the daemon writes the whole
// state once per successful reload. The plan is never mutated in place —
// a reload swaps the pointer under the lock, so a reader that already
// holds the old plan keeps a consistent view.
package liveconfig

import (
	"fmt"
	"regexp"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
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
	// is off); memoryPolicy is the [memory_policy."<path>".<entry>] family.
	// Both are swapped whole, never mutated in place.
	summarize    *workspace.SummarizeSettings
	memoryPolicy map[string]map[string]workspace.PolicyEntry
	// agentLanguage is [daemon] agent_language ("" = unset: "en").
	agentLanguage string
	// stepTiers is cli.toml's [step_tiers] catalog (nil = absent: an
	// empty catalog), the Host-only step-tier candidates the dispatch
	// expands an overlay tier into (polyforge#81). Swapped whole, never
	// mutated in place.
	stepTiers map[string][]string
	// The polyforge#51 failure-classification additions: the compiled
	// Host-configured pattern lists (nil = none) and the slow-model
	// judgement's two single-call latency bounds (0 = unset: the
	// documented default).
	transientRules []*regexp.Regexp
	modelRules     []*regexp.Regexp
	slowMedian     time.Duration
	slowP90        time.Duration
	// agentLanguageFallback supplies the default Cell's live language for a
	// named Cell that leaves its own key unset.
	agentLanguageFallback func() string
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

// StepTiers answers the [step_tiers] catalog as a deep copy: tier name →
// its candidates (nil/empty = no catalog). The copy keeps a reader from
// mutating the live state through a returned map or slice.
func (s *State) StepTiers() map[string][]string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	src := s.stepTiers
	s.mu.RUnlock()
	if len(src) == 0 {
		return nil
	}
	out := make(map[string][]string, len(src))
	for tier, list := range src {
		out[tier] = cloneList(list)
	}
	return out
}

// SetStepTiers swaps the whole [step_tiers] catalog (a reload's live
// half; the caller hands over a freshly parsed map).
func (s *State) SetStepTiers(v map[string][]string) {
	if s == nil {
		return
	}
	var cp map[string][]string
	if v != nil {
		cp = make(map[string][]string, len(v))
		for tier, list := range v {
			cp[tier] = cloneList(list)
		}
	}
	s.mu.Lock()
	s.stepTiers = cp
	s.mu.Unlock()
}

// compilePatterns compiles one pattern list (the cli.toml parse has
// already validated every item compiles; an impossible error here names
// the key and changes nothing).
func compilePatterns(key string, pats []string) ([]*regexp.Regexp, error) {
	if pats == nil {
		return nil, nil
	}
	rules := make([]*regexp.Regexp, 0, len(pats))
	for _, p := range pats {
		re, err := regexp.Compile(p)
		if err != nil {
			return nil, fmt.Errorf("%s: pattern %q: %w", key, p, err)
		}
		rules = append(rules, re)
	}
	return rules, nil
}

// cloneRules copies a compiled rule list, keeping nil nil.
func cloneRules(l []*regexp.Regexp) []*regexp.Regexp {
	if l == nil {
		return nil
	}
	return append([]*regexp.Regexp{}, l...)
}

// cloneList copies a list, keeping nil (undeclared) nil.
func cloneList(l []string) []string {
	if l == nil {
		return nil
	}
	return append([]string{}, l...)
}

// AgentLanguage answers the live [daemon] agent_language (unset = the
// default, "en").
func (s *State) AgentLanguage() string {
	if s == nil {
		return stepcall.DefaultLanguage
	}
	s.mu.RLock()
	language := s.agentLanguage
	fallback := s.agentLanguageFallback
	s.mu.RUnlock()
	if language != "" {
		return language
	}
	if fallback != nil {
		if language = fallback(); language != "" {
			return language
		}
	}
	return stepcall.DefaultLanguage
}

// SetAgentLanguageFallback makes an unset named Cell follow the default
// Cell's live language. An explicit value on this Cell still wins.
func (s *State) SetAgentLanguageFallback(fallback func() string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.agentLanguageFallback = fallback
	s.mu.Unlock()
}

// SetAgentLanguage swaps the live [daemon] agent_language ("" = unset:
// the default applies).
func (s *State) SetAgentLanguage(v string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.agentLanguage = v
	s.mu.Unlock()
}

// ---------------------------------------------------------------------------
// polyforge#51: the additional failure-classification rules and the
// slow-model latency bounds. Live like step_timeout: the daemon seeds them
// from its startup file and a reload swaps them whole, so the next
// classification and the next slow-model judgement read the new values.
// ---------------------------------------------------------------------------

// SetTransientErrorPatterns compiles and swaps the transient pattern list
// (nil/empty = no additional rules). The cli.toml parse has already
// validated every item compiles; a compile error here answers as an error
// and changes nothing.
func (s *State) SetTransientErrorPatterns(pats []string) error {
	if s == nil {
		return nil
	}
	rules, err := compilePatterns("transient_error_patterns", pats)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.transientRules = rules
	s.mu.Unlock()
	return nil
}

// SetModelErrorPatterns compiles and swaps the model-class pattern list.
func (s *State) SetModelErrorPatterns(pats []string) error {
	if s == nil {
		return nil
	}
	rules, err := compilePatterns("model_error_patterns", pats)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.modelRules = rules
	s.mu.Unlock()
	return nil
}

// TransientErrorRules answers the compiled transient patterns (nil = none).
func (s *State) TransientErrorRules() []*regexp.Regexp {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneRules(s.transientRules)
}

// ModelErrorRules answers the compiled model-class patterns (nil = none).
func (s *State) ModelErrorRules() []*regexp.Regexp {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneRules(s.modelRules)
}

// SetSlowModelMedianLatency swaps the slow-model median bound (0 = unset).
func (s *State) SetSlowModelMedianLatency(v time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.slowMedian = v
	s.mu.Unlock()
}

// SetSlowModelP90Latency swaps the slow-model p90 bound (0 = unset).
func (s *State) SetSlowModelP90Latency(v time.Duration) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.slowP90 = v
	s.mu.Unlock()
}

// SlowModelMedianLatency answers the slow-model median bound (0 = unset).
func (s *State) SlowModelMedianLatency() time.Duration {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.slowMedian
}

// SlowModelP90Latency answers the slow-model p90 bound (0 = unset).
func (s *State) SlowModelP90Latency() time.Duration {
	if s == nil {
		return 0
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.slowP90
}
