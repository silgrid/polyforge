package task

// manager.go: the in-memory task manager (workbench#53, carried from #40).
// It owns the fixed two-slot limiter (outside the job slots), the spawn
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
)

// ErrLimit is the refusal of a spawn beyond the fixed concurrency limit.
var ErrLimit = fmt.Errorf("task spawn refused: %d tasks already running (the limit is %d per daemon; nothing is queued)", Limit, Limit)

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
}

type entry struct {
	t      Task
	cancel context.CancelFunc
	// userCancel is set when Cancel (not the wall budget) ended the run.
	userCancel bool
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
	// pending is chat id -> ids of terminal tasks whose summary the chat
	// has not acked (bounded to the newest RetentionMaxEntries).
	pending map[string][]string
}

// NewManager builds a manager.
func NewManager(cfg Config) *Manager {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	base, stop := context.WithCancel(context.Background())
	return &Manager{cfg: cfg, base: base, stop: stop, tasks: map[string]*entry{}, pending: map[string][]string{}}
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
	m.mu.Lock()
	if m.running >= Limit {
		m.mu.Unlock()
		return Task{}, ErrLimit
	}
	m.running++
	m.mu.Unlock()
	release := func() {
		m.mu.Lock()
		m.running--
		m.mu.Unlock()
	}
	now := m.cfg.Now()
	t := Task{ID: id, Spec: spec, State: StateRunning, CreatedAt: now, StartedAt: now}
	if err := m.cfg.Store.Save(t); err != nil {
		release()
		return Task{}, err
	}
	ctx, cancel := context.WithTimeout(m.base, spec.MaxDuration)
	e := &entry{t: t, cancel: cancel}
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

// run walks the candidates in order inside the one shared budget and
// finishes the task in exactly one terminal state.
func (m *Manager) run(ctx context.Context, e *entry, dir string) {
	t := e.t
	var report strings.Builder
	var tokens int64
	var lastErr error
	capped, completed := false, false
	for _, cand := range t.Candidates {
		if ctx.Err() != nil {
			break
		}
		c, _ := harness.ParseCandidate(cand)
		m.mu.Lock()
		e.t.Candidate = cand
		m.mu.Unlock()
		res, err := m.cfg.Runner.Run(ctx, RunSpec{
			Candidate: c, Prompt: t.Prompt, MaxTokens: t.MaxTokens - tokens, WorkDir: dir, TaskID: t.ID,
		}, func(n int64) {
			m.mu.Lock()
			e.t.TokensConsumed = tokens + n
			m.mu.Unlock()
		})
		tokens += res.Tokens
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
	m.finish(ctx, e, report.String(), tokens, capped, completed, lastErr)
}

func (m *Manager) finish(ctx context.Context, e *entry, report string, tokens int64, capped, completed bool, lastErr error) {
	m.mu.Lock()
	user := e.userCancel
	m.mu.Unlock()
	t := e.t
	switch {
	case completed:
		t.State = StateCompleted
	case user:
		t.State = StateInterrupted
		t.Error = "cancelled by `pf task cancel`"
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
	t.EndedAt = now
	t.WallConsumed = now.Sub(t.StartedAt)
	if err := m.cfg.Store.Save(t); err != nil && t.Error == "" {
		t.Error = "report store: " + err.Error()
	}
	_, _ = m.cfg.Store.Prune(now)
	m.mu.Lock()
	e.t = t
	m.running--
	m.addPending(t)
	// The store is the ONLY home of a finished task: dropping the
	// in-memory entry here is what keeps pf task list/show/summary inside
	// the store's 50-entry / 7-day retention bound and the daemon's memory
	// flat over a long run (polyforge#40 review B6). A running task keeps
	// its entry (Cancel and the progress counter) until this point.
	delete(m.tasks, t.ID)
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
			if !t.Delivered {
				m.mu.Lock()
				m.addPending(t)
				m.mu.Unlock()
			}
			continue
		}
		t.State = StateInterrupted
		t.Error = "the daemon restarted while the task ran"
		t.EndedAt = now
		if err := m.cfg.Store.Save(t); err != nil {
			return err
		}
		m.mu.Lock()
		m.addPending(t)
		m.mu.Unlock()
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
	s := Summary{Limit: Limit}
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

// addPending queues a terminal task's summary for its origin chat (none
// without one). The caller holds mu.
func (m *Manager) addPending(t Task) {
	if t.OriginChat == "" {
		return
	}
	ids := append(m.pending[t.OriginChat], t.ID)
	if len(ids) > RetentionMaxEntries {
		ids = ids[len(ids)-RetentionMaxEntries:]
	}
	m.pending[t.OriginChat] = ids
}

// Notice is one terminal summary awaiting its chat's ack.
type Notice struct {
	TaskID string `json:"task_id"`
	Text   string `json:"text"`
}

// Notices answers the chat's unacked terminal summaries, oldest first
// (fetch-then-ack: nothing counts as delivered until AckNotices).
func (m *Manager) Notices(chat string) []Notice {
	m.mu.Lock()
	ids := append([]string(nil), m.pending[chat]...)
	m.mu.Unlock()
	var out []Notice
	for _, id := range ids {
		if t, err := m.Show(id); err == nil && t.State.Terminal() {
			out = append(out, Notice{TaskID: id, Text: t.Summary()})
		}
	}
	return out
}

// AckNotices marks the chat's summaries delivered (persisted, so a
// restart does not replay them).
func (m *Manager) AckNotices(chat string, ids []string) error {
	acked := map[string]bool{}
	for _, id := range ids {
		acked[id] = true
	}
	m.mu.Lock()
	var keep []string
	for _, id := range m.pending[chat] {
		if !acked[id] {
			keep = append(keep, id)
		}
	}
	m.pending[chat] = keep
	m.mu.Unlock()
	var first error
	for id := range acked {
		t, ok, err := m.cfg.Store.Load(id)
		if err != nil || !ok || t.OriginChat != chat || t.Delivered {
			continue
		}
		t.Delivered = true
		if err := m.cfg.Store.Save(t); err != nil && first == nil {
			first = err
		}
		m.mu.Lock()
		if e, ok := m.tasks[id]; ok {
			e.t.Delivered = true
		}
		m.mu.Unlock()
	}
	return first
}
