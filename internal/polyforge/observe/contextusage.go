package observe

// contextusage.go: the running step session's context usage
// — the tokens the current dispatch's conversation fills,
// counted from the session transcript its harness writes the way the chat
// header counts an interactive session (sessionjsonl.ContextReader), with
// the model id and the transcript timestamp the count was taken from.
//
// Each dispatch owns one contextSource, built at step_started from the
// fact's immutable values and published with the dispatch state, so a new
// step reads its own session and a dispatch's end drops the reading. It is
// read on demand by the status and watch faces in the caller's goroutine,
// under the source's own lock only — never under stateMu, never under the
// runtime's job-table lock — and reads only the bytes the harness appended
// since the previous read.
//
// Reach: `context` rides GET /status's job rows and GET /jobs/{wi}/watch's
// `progress` block — GETs any local peer can read, the step and task
// process trees included. It carries only tokens/window/percent (numbers),
// the model id and as_of — no transcript content. It does not reach aihub,
// reads and passes on no credential, and leaves the step process's
// environment untouched.

import (
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/sessionjsonl"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// contextSource is one dispatch's context-usage reader.
type contextSource struct {
	// immutable after construction
	capsuleDir string
	spawnAt    time.Time
	harness    string
	session    string
	locate     func(capsuleDir string, spawnAt time.Time, harness, session string) string

	mu     sync.Mutex
	path   string // the located live transcript ("" until found)
	reader *sessionjsonl.ContextReader
}

// newContextSource builds the dispatch's source from its step_started
// fact: a resumed pi dispatch appends to the capsule transcript the fact
// names; every other dispatch's live transcript is located on demand.
func newContextSource(capsuleDir string, f supervise.StepStarted, locate func(string, time.Time, string, string) string) *contextSource {
	h := "pi"
	if harness.Of(f.Harness) == harness.Claude {
		h = "claude"
	}
	s := &contextSource{capsuleDir: capsuleDir, spawnAt: f.StartedAt, harness: f.Harness, session: f.HarnessSession,
		locate: locate, reader: sessionjsonl.NewContextReader(h)}
	if f.Resumed && h != "claude" {
		s.path = f.TranscriptPath
	}
	return s
}

// read answers the dispatch's context usage; nil when nothing is known.
// windows are the context windows the harness reported per model
// (supervise.Snapshot.ContextWindows): the session model's window, when
// reported, is carried with the percent of it the tokens fill.
func (s *contextSource) read(windows map[string]int64) *protocol.ContextUsage {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.path == "" && s.locate != nil {
		s.path = s.locate(s.capsuleDir, s.spawnAt, s.harness, s.session)
	}
	if s.path == "" {
		return nil
	}
	c := s.reader.Read(s.path)
	u := &protocol.ContextUsage{Model: c.Model}
	if c.TokensKnown {
		tokens := c.Tokens
		u.Tokens = &tokens
		if _, err := time.Parse(time.RFC3339Nano, c.At); err == nil {
			u.AsOf = c.At
		}
	}
	if u.Tokens == nil && u.Model == "" {
		return nil
	}
	if w := windows[c.Model]; c.Model != "" && w > 0 {
		u.Window = &w
		if u.Tokens != nil {
			pct := float64(*u.Tokens) / float64(w) * 100
			u.Percent = &pct
		}
	}
	return u
}

// contextUsage answers the published dispatch's context usage (any
// goroutine), with the windows its tracker's snapshot carries.
func (d *dispatchState) contextUsage(snap supervise.Snapshot) *protocol.ContextUsage {
	return d.ctx.read(snap.ContextWindows)
}
