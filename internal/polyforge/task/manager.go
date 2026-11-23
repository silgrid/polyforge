package task

// manager.go: the in-memory task manager (carried from #40).
// It owns the task lane's concurrency bound (the Host's
// max_concurrent_tasks, DefaultLimit when unset — outside the job
// slots), the spawn
// path (live candidate resolution, the project's working directory, a
// background runner goroutine), candidate rotation inside ONE shared
// wall/token budget, the four terminal states, truncated-output capture and
// List/Show/Cancel.
//
// Locking: mu guards only the in-memory table and counters; every store and
// runner call happens outside it.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// ErrLimit is the sentinel of a spawn refused at the task lane's
// concurrency bound: every refusal wraps it (errLimit), so
// errors.Is(err, ErrLimit) keeps holding — the daemon maps it to 409.
var ErrLimit = errors.New("nothing is queued")

// errLimit answers the typed refusal at the bound: it names the running
// count, the configured key and its current value, keeps `limit` and
// `nothing is queued` (the words the daemon's clients and mocks match),
// and wraps the sentinel.
func errLimit(running, max int) error {
	return fmt.Errorf("task spawn refused: %d tasks already running (the limit is %d, max_concurrent_tasks on this Host): %w", running, max, ErrLimit)
}

// ErrNotFound / ErrNotRunning are Show/Cancel's typed answers.
var (
	ErrNotFound   = errors.New("no such task")
	ErrNotRunning = errors.New("task is not running")
)

// Config wires the manager. Resolve answers the project's candidate list
// read LIVE (one source, never a snapshot) and refuses an unmapped project;
// WorkDir answers the directory the harness runs in (the project's
// workspace); OnTerminal (optional) is called once per terminal transition,
// outside the lock.
type Config struct {
	Store      *Store
	Runner     Runner
	Resolve    func(project string) ([]string, string, error)
	WorkDir    func(project string) (string, error)
	OnTerminal func(Task)
	Now        func() time.Time
	// Slots is the bound several managers share (the Host's one
	// max_concurrent_tasks bound over all its Cells); nil = this
	// manager's own check reads the default only.
	Slots *Slots
	// Admit (optional) is a spawn-time refusal seam outside the bound
	// (the daemon's execution-plane and connection-rotation checks)
	// or nil; Done returns the reservation when the task ends.
	// Both are called with no manager lock held.
	Admit func() error
	Done  func()
	// Limits (optional) answers the stuck-detection bounds of every
	// dispatch (AC9: the step lane's rule 0/2/3 thresholds, injected
	// live — the daemon's liveLimits); nil = the documented defaults
	// (10m silence for token-level harnesses, 30m tool window).
	Limits func() supervise.Defaults
	// PromptFor (optional) answers the prompt ONE candidate runs with,
	// consulted before every candidate dispatch (polyforge#72, AC10: the
	// daemon re-reads the ticket and rebuilds the prompt so the next
	// candidate continues what the previous one already wrote into
	// `## 分析`); nil, or an empty answer, = dispatchedPrompt, the one
	// prompt the spec carried.
	PromptFor func(t Task) string
}

// Slots is the Host's one shared concurrency bound of the task lane
// (max_concurrent_tasks; DefaultLimit when unset or 0), adjustable live.
type Slots struct {
	mu  sync.Mutex
	n   int
	max int // 0 = DefaultLimit
}

// SetMax changes the bound (n <= 0 restores the default). Running work
// is never stopped: a lowered bound refuses only new spawns.
func (s *Slots) SetMax(n int) {
	if s == nil {
		return
	}
	if n < 0 {
		n = 0
	}
	s.mu.Lock()
	s.max = n
	s.mu.Unlock()
}

// Max answers the current bound (DefaultLimit for a nil or unset Slots).
func (s *Slots) Max() int {
	if s == nil {
		return DefaultLimit
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.limitLocked()
}

// limitLocked answers the bound; s.mu held.
func (s *Slots) limitLocked() int {
	if s.max <= 0 {
		return DefaultLimit
	}
	return s.max
}

func (s *Slots) acquire() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.n >= s.limitLocked() {
		return false
	}
	s.n++
	return true
}

func (s *Slots) release() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.n--
	s.mu.Unlock()
}

// Running answers how many slots are taken.
func (s *Slots) Running() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

type entry struct {
	t      Task
	cancel context.CancelFunc
	// userCancel is set when Cancel (not the wall budget) ended the run.
	userCancel bool
	// scratchTask / scratchRaw are the task's scratch directories the
	// spawn created (workspace.TaskScratch); empty when none were wired.
	scratchTask, scratchRaw string
}

// Manager is the task manager of one daemon.
type Manager struct {
	cfg  Config
	base context.Context
	stop context.CancelFunc
	wg   sync.WaitGroup

	mu      sync.Mutex
	tasks   map[string]*entry
	running int
	// waiters is task id -> the Wait calls blocked on that live task; the
	// terminal transition hands each the finished record and drops them.
	waiters map[string][]chan Task
}

// NewManager builds a manager.
func NewManager(cfg Config) *Manager {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	base, stop := context.WithCancel(context.Background())
	return &Manager{cfg: cfg, base: base, stop: stop, tasks: map[string]*entry{},
		waiters: map[string][]chan Task{}}
}

// Close ends every running task (interrupted) and waits for the runners.
func (m *Manager) Close() {
	m.stop()
	m.wg.Wait()
}

// Spawn starts one task and answers it (state running) immediately. A
// zero MaxDuration/MaxTokens takes the fixed default; any budget above the
// ceiling is refused naming it.
func (m *Manager) Spawn(spec Spec) (Task, error) {
	if strings.TrimSpace(spec.Project) == "" {
		return Task{}, errors.New("task spawn: --project is required")
	}
	if strings.TrimSpace(spec.Prompt) == "" {
		return Task{}, errors.New("task spawn: a prompt is required")
	}
	if spec.MaxDuration == 0 {
		spec.MaxDuration = DefaultMaxDuration
	}
	if spec.MaxTokens == 0 {
		spec.MaxTokens = DefaultMaxTokens
	}
	if err := ValidateDuration(spec.MaxDuration); err != nil {
		return Task{}, err
	}
	if err := ValidateTokens(spec.MaxTokens); err != nil {
		return Task{}, err
	}
	cands, _, err := m.cfg.Resolve(spec.Project)
	if err != nil {
		return Task{}, err
	}
	for _, c := range cands {
		pc, perr := harness.ParseCandidate(c)
		if perr != nil {
			return Task{}, perr
		}
		if !SupportsUsage(harness.Of(pc.Harness)) {
			return Task{}, fmt.Errorf("task spawn refused: candidate %s cannot report token usage", c)
		}
	}
	spec.Candidates = cands
	dir, err := m.cfg.WorkDir(spec.Project)
	if err != nil {
		return Task{}, err
	}
	id, err := NewID()
	if err != nil {
		return Task{}, err
	}
	if m.cfg.Admit != nil {
		if err := m.cfg.Admit(); err != nil {
			return Task{}, err
		}
	}
	m.mu.Lock()
	if m.running >= m.limit() || !m.cfg.Slots.acquire() {
		own := m.running
		m.mu.Unlock()
		m.done()
		return Task{}, m.refuseLimit(own)
	}
	m.running++
	m.mu.Unlock()
	release := func() {
		m.mu.Lock()
		m.running--
		m.mu.Unlock()
		m.cfg.Slots.release()
		m.done()
	}
	// the task's scratch directories are created BEFORE the dispatch
	// (polyforge#121, owner ruling Q3), after the admission and the
	// bound: a refusal — a broken scratch — fails the spawn with no slot
	// held and no empty directories left behind an admission that never
	// ran. A read-only errand (the message analyzer) writes nothing: it
	// runs without them, keeping its own invariant that not one path of
	// the workspace changes.
	var taskDir, rawDir string
	if !spec.ReadOnlyTools {
		var err error
		if taskDir, rawDir, err = workspace.TaskScratch(dir, id, m.cfg.Now()); err != nil {
			release()
			return Task{}, err
		}
	}
	now := m.cfg.Now()
	t := Task{ID: id, Spec: spec, State: StateRunning, CreatedAt: now, StartedAt: now}
	if err := m.cfg.Store.Save(t); err != nil {
		release()
		return Task{}, err
	}
	ctx, cancel := context.WithTimeout(m.base, spec.MaxDuration)
	e := &entry{t: t, cancel: cancel, scratchTask: taskDir, scratchRaw: rawDir}
	m.mu.Lock()
	m.tasks[id] = e
	m.mu.Unlock()
	m.wg.Add(1)
	go func() {
		defer m.wg.Done()
		defer cancel()
		m.run(ctx, e, dir)
	}()
	return t.Clone(), nil
}

// limit answers the concurrency bound this manager's own check reads:
// the shared bound when one is wired, else the default (the Host's
// reload sets the shared Slots; without one there is nothing to set).
func (m *Manager) limit() int {
	return m.cfg.Slots.Max()
}

// refuseLimit answers the spawn refusal at the bound: the running count
// (own when no shared Slots is wired, else the Host-wide count it read)
// and the bound's current value come from the same source the check
// read, never a second copy.
func (m *Manager) refuseLimit(own int) error {
	running := own
	if s := m.cfg.Slots; s != nil {
		running = s.Running()
	}
	return errLimit(running, m.limit())
}

// done returns an Admit reservation.
func (m *Manager) done() {
	if m.cfg.Done != nil {
		m.cfg.Done()
	}
}

// run walks the candidates in order inside the one shared budget and
// finishes the task in exactly one terminal state. A candidate the stuck
// detection ends (a *StuckError: rules 0/2/3, AC9) is recorded as an
// Attempt and the walk goes on to the next candidate — its tokens stay
// spent, the shared budget is the one that was fixed at spawn.
func (m *Manager) run(ctx context.Context, e *entry, dir string) {
	t := e.t
	var report strings.Builder
	var tokens int64
	var lastErr error
	capped, completed := false, false
	var attempts []Attempt
	limits := func() supervise.Defaults {
		if m.cfg.Limits != nil {
			return m.cfg.Limits()
		}
		return supervise.Defaults{}
	}
	// The prompt of ONE candidate: the spec's own text behind the
	// prohibited-actions section, or — for an analysis task (AC10) — the
	// hook's answer, consulted before EVERY dispatch so a candidate that
	// takes over continues from the section its predecessor already wrote
	// (the daemon re-reads the ticket there).
	promptOf := func() string {
		p := dispatchedPrompt(t.Prompt, dir)
		if m.cfg.PromptFor != nil {
			if q := m.cfg.PromptFor(t); q != "" {
				p = q
			}
		}
		// the # Scratch section closes every dispatched prompt (the
		// analysis re-read path included — it lands here, where the two
		// paths join)
		if e.scratchTask != "" {
			p += scratchNote(e.scratchTask, e.scratchRaw)
		}
		return p
	}
	for _, cand := range t.Candidates {
		if ctx.Err() != nil {
			break
		}
		c, _ := harness.ParseCandidate(cand)
		// a candidate starts with no context of its own: the previous
		// attempt's value belongs to that attempt's conversation and is
		// dropped until this one reports its own (polyforge#230 AC1)
		m.mu.Lock()
		e.t.Candidate = cand
		e.t.Context = nil
		m.mu.Unlock()
		res, err := m.cfg.Runner.Run(ctx, RunSpec{
			Candidate: c, Prompt: promptOf(), MaxTokens: t.MaxTokens - tokens, WorkDir: dir, TaskID: t.ID,
			ScratchTaskDir: e.scratchTask, ScratchRawDir: e.scratchRaw,
			ReadOnlyTools: t.ReadOnlyTools, Limits: limits(),
			OnContext: func(u *protocol.ContextUsage) {
				m.mu.Lock()
				e.t.Context = u
				m.mu.Unlock()
			},
		}, func(n int64) {
			m.mu.Lock()
			e.t.TokensConsumed = tokens + n
			m.mu.Unlock()
		})
		tokens += res.Tokens
		var stuck *StuckError
		if errors.As(err, &stuck) && ctx.Err() == nil {
			attempts = append(attempts, Attempt{Candidate: cand, Cause: string(stuck.Cause), Evidence: stuck.Evidence})
			m.mu.Lock()
			e.t.Attempts = append([]Attempt(nil), attempts...)
			m.mu.Unlock()
			if lastErr == nil {
				lastErr = fmt.Errorf("%s judged stuck (%s): %s", cand, stuck.Cause, stuck.Evidence)
			}
			continue // the next candidate, inside the same budget
		}
		report.Reset()
		report.WriteString(res.Report)
		if res.TokenCapped || tokens >= t.MaxTokens {
			capped = true
			break
		}
		if err != nil && ctx.Err() == nil {
			lastErr = fmt.Errorf("%s: %w", cand, err)
			continue
		}
		completed = err == nil && ctx.Err() == nil
		break
	}
	m.finish(ctx, e, report.String(), tokens, capped, completed, lastErr, attempts)
}

// ErrorCancelled is the Error of a task the owner ended with `pf task
// cancel` — an interrupted task that no daemon stop interrupted (the
// analysis re-queue tells the two apart, polyforge#72).
const ErrorCancelled = "cancelled by `pf task cancel`"

// dispatchedPrompt answers what one run's harness reads: the shared
// prohibited-actions section (harness.TaskProhibitedActions,
// — the list every step carries, scoped to the task's
// working directory, its exception naming the task alone), the concise
// output-discipline rule (harness.ConciseRule, the one source every
// step dispatch and harness context file also carries — polyforge#101)
// ahead of the task's own prompt. The record keeps the prompt as
// given: list, show and the prompt summary never carry the section.
func dispatchedPrompt(prompt, dir string) string {
	return harness.TaskProhibitedActions(dir) + "\n" + harness.ConciseRule() + "\n# Task\n\n" + prompt
}

// scratchNote is the section every dispatched task prompt ends with
// (polyforge#121, owner ruling Q3): the task's own scratch directories,
// already created, and the environment variables that carry them. The
// manager appends it to every candidate's prompt — the ordinary
// dispatchedPrompt and a PromptFor (analysis) answer alike.
func scratchNote(taskDir, rawDir string) string {
	return "\n# Scratch\n\n" +
		"This task's disposable output goes into its own scratch directories, both already created:\n\n" +
		"- other output: " + taskDir + " (the environment variable " + workspace.EnvScratchDir + " carries it);\n" +
		"- raw data — batch JSON, logs, downloaded archives, history exports: " + rawDir + " (" + workspace.EnvScratchRawDir + ").\n\n" +
		"Write nothing else into the workspace.\n"
}

func (m *Manager) finish(ctx context.Context, e *entry, report string, tokens int64, capped, completed bool, lastErr error, attempts []Attempt) {
	m.mu.Lock()
	user := e.userCancel
	m.mu.Unlock()
	t := e.t
	if len(attempts) > 0 {
		t.Attempts = append([]Attempt(nil), attempts...)
	}
	switch {
	case completed:
		t.State = StateCompleted
	case user:
		t.State = StateInterrupted
		t.Error = ErrorCancelled
	case capped:
		t.State = StateCapped
		t.Truncated = true
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		t.State = StateCapped
		t.Truncated = true
	case ctx.Err() != nil:
		t.State = StateInterrupted
		t.Error = "the daemon stopped while the task ran"
	default:
		t.State = StateFailed
		if lastErr != nil {
			t.Error = lastErr.Error()
		} else {
			t.Error = "no model candidate could complete the task"
		}
	}
	if len(report) > ReportCapBytes {
		report = report[:ReportCapBytes]
		t.Truncated = true
	}
	now := m.cfg.Now()
	t.Report = report
	t.TokensConsumed = tokens
	// a finished task carries no context usage: the value describes a live
	// conversation, and the terminal record is what list/show and the store
	// answer (polyforge#230 AC1)
	t.Context = nil
	t.EndedAt = now
	t.WallConsumed = now.Sub(t.StartedAt)
	if err := m.cfg.Store.Save(t); err != nil && t.Error == "" {
		t.Error = "report store: " + err.Error()
	}
	_, _ = m.cfg.Store.Prune(now)
	m.cfg.Slots.release()
	m.done()
	m.mu.Lock()
	e.t = t
	m.running--
	// The store is the ONLY home of a finished task: dropping the
	// in-memory entry here is what keeps pf task list/show/summary inside
	// the store's 50-entry / 7-day retention bound and the daemon's memory
	// flat over a long run (polyforge#40 code review). A running task keeps
	// its entry (Cancel and the progress counter) until this point.
	delete(m.tasks, t.ID)
	for _, ch := range m.waiters[t.ID] {
		ch <- t.Clone() // buffered 1: never blocks
	}
	delete(m.waiters, t.ID)
	m.mu.Unlock()
	if m.cfg.OnTerminal != nil {
		m.cfg.OnTerminal(t.Clone())
	}
}

// Recover marks every persisted non-terminal task interrupted (never
// resumed) and reports each through OnTerminal. It runs once at daemon
// start.
func (m *Manager) Recover() error {
	all, err := m.cfg.Store.List()
	if err != nil {
		return err
	}
	now := m.cfg.Now()
	for _, t := range all {
		if t.State.Terminal() {
			continue
		}
		t.State = StateInterrupted
		t.Error = "the daemon restarted while the task ran"
		t.Context = nil
		t.EndedAt = now
		if err := m.cfg.Store.Save(t); err != nil {
			return err
		}
		if m.cfg.OnTerminal != nil {
			m.cfg.OnTerminal(t.Clone())
		}
	}
	return nil
}

// List answers every known task newest first: the persisted records
// overlaid with this daemon's live running tasks.
func (m *Manager) List() ([]Task, error) {
	stored, err := m.cfg.Store.List()
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	live := map[string]Task{}
	for id, e := range m.tasks {
		live[id] = e.t.Clone()
	}
	m.mu.Unlock()
	out := make([]Task, 0, len(stored)+len(live))
	for _, t := range stored {
		if l, ok := live[t.ID]; ok {
			t = l
			delete(live, t.ID)
		}
		out = append(out, t)
	}
	for _, l := range live {
		out = append(out, l)
	}
	sortNewestFirst(out)
	return out, nil
}

// Show answers one task by id.
func (m *Manager) Show(id string) (Task, error) {
	m.mu.Lock()
	if e, ok := m.tasks[id]; ok {
		t := e.t.Clone()
		m.mu.Unlock()
		return t, nil
	}
	m.mu.Unlock()
	t, ok, err := m.cfg.Store.Load(id)
	if err != nil {
		return Task{}, err
	}
	if !ok {
		return Task{}, ErrNotFound
	}
	return t, nil
}

// Wait blocks until task id reaches a terminal state, or ctx ends, and
// answers the terminal record. A live task registers a waiter the terminal
// transition answers (no polling); a task no longer live is read from the
// store, where its terminal record was saved before the live entry was
// dropped. An unknown id is ErrNotFound; ctx's end stops only the wait,
// never the task.
func (m *Manager) Wait(ctx context.Context, id string) (Task, error) {
	m.mu.Lock()
	if _, ok := m.tasks[id]; !ok {
		m.mu.Unlock()
		return m.Show(id)
	}
	ch := make(chan Task, 1)
	m.waiters[id] = append(m.waiters[id], ch)
	m.mu.Unlock()
	select {
	case t := <-ch:
		return t, nil
	case <-ctx.Done():
		m.mu.Lock()
		ws := m.waiters[id]
		for i, w := range ws {
			if w == ch {
				m.waiters[id] = append(ws[:i:i], ws[i+1:]...)
				break
			}
		}
		if len(m.waiters[id]) == 0 {
			delete(m.waiters, id)
		}
		m.mu.Unlock()
		return Task{}, fmt.Errorf("task %s was still running when the wait ended: %w", id, ctx.Err())
	}
}

// Cancel ends a running task (it ends interrupted).
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	e, ok := m.tasks[id]
	if !ok || e.t.State != StateRunning {
		m.mu.Unlock()
		if !ok {
			if _, err := m.Show(id); err != nil {
				return err
			}
		}
		return ErrNotRunning
	}
	e.userCancel = true
	cancel := e.cancel
	m.mu.Unlock()
	cancel()
	return nil
}

// Summary is the additive tasks block of GET /status.
type Summary struct {
	Running     int `json:"running"`
	Limit       int `json:"limit"`
	Completed   int `json:"completed"`
	Capped      int `json:"capped"`
	Failed      int `json:"failed"`
	Interrupted int `json:"interrupted"`
}

// Summarize counts the known tasks by state (a store failure counts only
// this daemon's live tasks).
func (m *Manager) Summarize() Summary {
	s := Summary{Limit: m.limit()}
	all, _ := m.List()
	for _, t := range all {
		switch t.State {
		case StateRunning:
			s.Running++
		case StateCompleted:
			s.Completed++
		case StateCapped:
			s.Capped++
		case StateFailed:
			s.Failed++
		case StateInterrupted:
			s.Interrupted++
		}
	}
	return s
}

func sortNewestFirst(ts []Task) {
	for i := 1; i < len(ts); i++ {
		for j := i; j > 0 && ts[j].CreatedAt.After(ts[j-1].CreatedAt); j-- {
			ts[j], ts[j-1] = ts[j-1], ts[j]
		}
	}
}
