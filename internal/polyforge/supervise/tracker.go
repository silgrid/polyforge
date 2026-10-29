package supervise

// tracker.go: the per-dispatch harness state. The translator folds the
// native record stream into it (merge, never queue: only the latest
// state is kept), and the supervisor reads a snapshot each tick to apply
// the rules of rules.go. One Tracker exists per dispatch; it is created
// by whichever of step_started / first harness record arrives first and
// dropped on step_ended (Link.Release), which is what roots out the #124
// "turn state survives into the next step" kill.

import (
	"encoding/json"
	"strings"
	"sync"
	"time"
)

// ToolRun is one tool execution in flight.
type ToolRun struct {
	ID   string
	Name string
	// Args is the tool's arguments, cut for evidence text.
	Args string
	// Summary is the human-readable command: the bash command line, or the
	// tool name plus its key argument.
	Summary string
	Since   time.Time
}

// closedPhase is one model/tool phase the tracker closed since the last
// supervisor tick. It is kept only until that tick, so a declared
// observation gap can remove the span the supervisor never watched from
// the cumulative model/tool split (a phase open at the freeze that closed
// after the resume but before the tick).
type closedPhase struct {
	model      bool
	start, end time.Time
}

// maxClosedSinceTick bounds the pending list for a tracker whose monitor
// never ticks (unit tests). The phases that can straddle one gap are the
// first ones folded after the resume, so the list keeps the oldest.
const maxClosedSinceTick = 256

// Tracker implements Sink for one dispatch.
type Tracker struct {
	now func() time.Time

	mu     sync.Mutex
	spawn  int64
	born   time.Time
	begun  bool
	meta   StepStarted
	thresh Thresholds

	work      int64     // work-class signals seen
	noise     int64     // non-work records seen (UI chatter etc.)
	lastWork  time.Time // newest work-class signal
	lastNoise time.Time

	// model phase: waiting is true from WaitingModel until a tool starts
	// or the turn ends; lastOutput moves on every model output.
	waiting      bool
	waitingSince time.Time
	lastOutput   time.Time
	modelDur     time.Duration // closed model phases

	tools   map[string]*ToolRun
	toolDur time.Duration // closed tool runs

	// live progress of the dispatch (the watch face): how many tools ran,
	// and the last few files edit/write tools touched.
	toolCalls   int
	recentFiles []string

	// wait calls (wait.go): counted and surfaced, never
	// judged. waitPending holds the calls TakeWaitCalls has not handed out
	// yet; waitNotify (buffered, never blocks the reader) wakes the taker.
	waitCalls   int
	waitSecs    int64
	waitPending []WaitCall
	waitNotify  chan struct{}

	turnEnded   bool
	turnEndedAt time.Time
	turnErr     bool
	turnErrText string
	turnErrAt   time.Time

	// observation-gap bookkeeping (ObserveTick): the supervisor's own
	// pause must not count as dispatch time. closedSinceTick holds the
	// phases closed since the previous tick; ObserveTick removes the
	// declared gap from any phase that straddled its cutoff. It is cleared
	// every tick, so a live tracker's list stays tiny; the cap bounds a
	// tracker no monitor ever ticks.
	closedSinceTick []closedPhase

	lastError string
	exit      *ExitInfo
}

func newTracker(spawn int64, now func() time.Time) *Tracker {
	if now == nil {
		now = time.Now
	}
	return &Tracker{now: now, spawn: spawn, born: now(), tools: map[string]*ToolRun{}, waitNotify: make(chan struct{}, 1)}
}

// Begin attaches the dispatch metadata of step_started.
func (t *Tracker) Begin(f StepStarted) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.begun = true
	t.meta = f
	t.thresh = f.Thresholds
	if f.StartedAt.IsZero() {
		t.meta.StartedAt = t.born
	}
}

// --- Sink ---

func (t *Tracker) workLocked() {
	t.work++
	t.lastWork = t.now()
}

// Alive implements Sink.
func (t *Tracker) Alive() {
	t.mu.Lock()
	t.workLocked()
	t.mu.Unlock()
}

// Noise implements Sink.
func (t *Tracker) Noise() {
	t.mu.Lock()
	t.noise++
	t.lastNoise = t.now()
	t.mu.Unlock()
}

func (t *Tracker) closeModelLocked(now time.Time) {
	if t.waiting {
		t.addClosedLocked(true, t.waitingSince, now)
		t.waiting = false
	}
}

// addClosedLocked folds one finished phase into the cumulative split and
// keeps it pending for this tick's observation-gap correction. A phase
// open when the gap was declared has already had its start shifted by
// ObserveTick, so it is never corrected twice; a phase that closed after
// the previous tick but straddles the cutoff loses the gap span here.
func (t *Tracker) addClosedLocked(model bool, start, end time.Time) {
	d := end.Sub(start)
	if d < 0 {
		d = 0
	}
	if len(t.closedSinceTick) < maxClosedSinceTick {
		t.closedSinceTick = append(t.closedSinceTick, closedPhase{model: model, start: start, end: end})
	}
	if model {
		t.modelDur += d
	} else {
		t.toolDur += d
	}
}

// WaitingModel implements Sink.
func (t *Tracker) WaitingModel() {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.workLocked()
	if !t.waiting {
		t.waiting = true
		t.waitingSince = now
	}
	// a new model call supersedes an earlier error: the harness went on
	t.turnEnded = false
	t.turnErr = false
	t.turnErrText = ""
	t.lastError = "" // stale evidence of an earlier turn must not outlive it
}

// ModelOutput implements Sink.
func (t *Tracker) ModelOutput(OutputKind) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.workLocked()
	t.lastOutput = now
	t.turnErr = false // the harness produced more output: it went on
	t.lastError = ""
	if !t.waiting { // output without a seen turn_start: still a model phase
		t.waiting = true
		t.waitingSince = now
	}
}

// ToolStart implements Sink.
func (t *Tracker) ToolStart(id, name, args string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.workLocked()
	t.closeModelLocked(now) // the reply is complete once a tool starts
	if id == "" {
		id = name
	}
	summary, path := summarizeTool(name, args)
	evidence := args
	if len(evidence) > 240 {
		evidence = evidence[:240] + "..."
	}
	t.tools[id] = &ToolRun{ID: id, Name: name, Args: evidence, Summary: summary, Since: now}
	t.toolCalls++
	if path != "" && isFileWriteTool(name) {
		t.noteFileLocked(path)
	}
	if w, ok := detectWaitCall(id, name, summary, now); ok {
		t.noteWaitCallLocked(w)
	}
	t.turnEnded = false
	t.turnErr = false
}

const maxRecentFiles = 5

// noteFileLocked records a changed file path (most recent last, unique).
func (t *Tracker) noteFileLocked(path string) {
	for i, p := range t.recentFiles {
		if p == path {
			t.recentFiles = append(t.recentFiles[:i], t.recentFiles[i+1:]...)
			break
		}
	}
	t.recentFiles = append(t.recentFiles, path)
	if len(t.recentFiles) > maxRecentFiles {
		t.recentFiles = t.recentFiles[len(t.recentFiles)-maxRecentFiles:]
	}
}

// noteWaitCallLocked counts one wait call and queues it for the taker.
func (t *Tracker) noteWaitCallLocked(w WaitCall) {
	t.waitCalls++
	t.waitSecs += w.Seconds
	w.WaitCalls, w.WaitSeconds = t.waitCalls, t.waitSecs
	if len(t.waitPending) < maxPendingWaitCalls {
		t.waitPending = append(t.waitPending, w)
	}
	select {
	case t.waitNotify <- struct{}{}:
	default: // a wake-up is already pending; the taker drains everything
	}
}

// WaitCallNotify is signalled whenever a wait call was folded; the
// receiver takes them with TakeWaitCalls.
func (t *Tracker) WaitCallNotify() <-chan struct{} { return t.waitNotify }

// TakeWaitCalls hands out the wait calls folded since the previous take,
// oldest first; each one is handed out once.
func (t *Tracker) TakeWaitCalls() []WaitCall {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := t.waitPending
	t.waitPending = nil
	return out
}

// ToolEnd implements Sink.
func (t *Tracker) ToolEnd(id, name string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.workLocked()
	if id == "" {
		id = name
	}
	if run, ok := t.tools[id]; ok {
		t.addClosedLocked(false, run.Since, now)
		delete(t.tools, id)
	}
}

// TurnEnded implements Sink.
func (t *Tracker) TurnEnded(ok bool, errText string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.now()
	t.workLocked()
	t.closeModelLocked(now)
	t.turnEnded = true
	t.turnEndedAt = now
	if !ok {
		t.turnErr = true
		t.turnErrText = errText
		t.turnErrAt = now
		if errText != "" {
			t.lastError = errText
		}
	}
}

// ErrorSeen implements Sink.
func (t *Tracker) ErrorSeen(text string) {
	t.mu.Lock()
	t.lastError = text
	t.mu.Unlock()
}

// ProcessExit implements Sink.
func (t *Tracker) ProcessExit(info ExitInfo) {
	t.mu.Lock()
	c := info
	t.exit = &c
	t.mu.Unlock()
}

// ObserveTick records one supervisor observation. shift > 0 declares an
// observation gap: every instant stamped at or before cutoff is moved
// forward by shift (the span the supervisor did not watch), and the
// cumulative model/tool split loses the same span for each phase that
// straddles the cutoff. A zero shift just closes the window (the phases
// closed since the previous tick were fully observed).
func (t *Tracker) ObserveTick(cutoff time.Time, shift time.Duration) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if shift > 0 {
		at := func(ts *time.Time) {
			if !ts.IsZero() && !ts.After(cutoff) {
				*ts = ts.Add(shift)
			}
		}
		at(&t.meta.StartedAt)
		at(&t.waitingSince)
		at(&t.lastOutput)
		at(&t.turnEndedAt)
		at(&t.turnErrAt)
		for _, run := range t.tools {
			at(&run.Since)
		}
		for _, c := range t.closedSinceTick {
			if c.start.After(cutoff) || !c.end.After(cutoff) {
				continue
			}
			if c.model {
				t.modelDur = max(t.modelDur-shift, 0)
			} else {
				t.toolDur = max(t.toolDur-shift, 0)
			}
		}
	}
	t.closedSinceTick = t.closedSinceTick[:0]
}

// --- Snapshot ---

// Snapshot is a point-in-time reading of one dispatch.
type Snapshot struct {
	SpawnN  int64
	Step    string
	Model   string
	Harness string
	PID     int

	StartedAt  time.Time
	Thresholds Thresholds
	Begun      bool

	WorkSignals int64
	NoiseCount  int64
	LastWork    time.Time // zero = no work-class signal yet
	LastNoise   time.Time

	Waiting      bool
	WaitingSince time.Time
	LastOutput   time.Time
	ModelTime    time.Duration // closed + current model phase
	Tools        []ToolRun     // running tools, oldest first
	ToolTime     time.Duration // closed + running tool time

	TurnEnded   bool
	TurnEndedAt time.Time
	TurnError   bool
	TurnErrText string
	TurnErrAt   time.Time

	// ToolCalls counts the tools this dispatch started; RecentFiles are
	// the last few paths edit/write tools touched (oldest first).
	ToolCalls   int
	RecentFiles []string

	// WaitCalls counts the dispatch's wait calls (wait.go) and WaitSeconds
	// sums their sleeps. Surfaced only: Judge never reads them.
	WaitCalls   int
	WaitSeconds int64

	LastError string
	Exit      *ExitInfo
}

// Snapshot reads the tracker at the supplied instant.
func (t *Tracker) Snapshot(now time.Time) Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := Snapshot{
		SpawnN:  t.spawn,
		Step:    t.meta.Step,
		Model:   t.meta.Model,
		Harness: t.meta.Harness,
		PID:     t.meta.PID,

		StartedAt:  t.meta.StartedAt,
		Thresholds: t.thresh,
		Begun:      t.begun,

		WorkSignals: t.work,
		NoiseCount:  t.noise,
		LastWork:    t.lastWork,
		LastNoise:   t.lastNoise,

		Waiting:      t.waiting,
		WaitingSince: t.waitingSince,
		LastOutput:   t.lastOutput,
		ModelTime:    t.modelDur,
		ToolTime:     t.toolDur,

		TurnEnded:   t.turnEnded,
		TurnEndedAt: t.turnEndedAt,
		TurnError:   t.turnErr,
		TurnErrText: t.turnErrText,
		TurnErrAt:   t.turnErrAt,
		ToolCalls:   t.toolCalls,
		RecentFiles: append([]string(nil), t.recentFiles...),
		WaitCalls:   t.waitCalls,
		WaitSeconds: t.waitSecs,
		LastError:   t.lastError,
	}
	if !t.begun {
		s.StartedAt = t.born
	}
	if t.waiting {
		s.ModelTime += now.Sub(t.waitingSince)
	}
	for _, run := range t.tools {
		s.Tools = append(s.Tools, *run)
		s.ToolTime += now.Sub(run.Since)
	}
	// oldest first
	for i := 1; i < len(s.Tools); i++ {
		for j := i; j > 0 && s.Tools[j].Since.Before(s.Tools[j-1].Since); j-- {
			s.Tools[j], s.Tools[j-1] = s.Tools[j-1], s.Tools[j]
		}
	}
	if t.exit != nil {
		c := *t.exit
		s.Exit = &c
	}
	return s
}

// summarizeTool renders a tool call for the live-progress face and extracts
// the file path it works on (when the arguments name one).
func summarizeTool(name, args string) (summary, path string) {
	var m map[string]any
	if args != "" && json.Unmarshal([]byte(args), &m) != nil {
		m = nil
	}
	str := func(keys ...string) string {
		for _, k := range keys {
			if v, ok := m[k].(string); ok && strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		}
		return ""
	}
	path = str("path", "file_path", "filePath", "file")
	if cmd := str("command", "cmd"); cmd != "" {
		return cmd, path
	}
	if argv, ok := m["argv"].([]any); ok && len(argv) > 0 {
		parts := make([]string, 0, len(argv))
		for _, a := range argv {
			if s, ok := a.(string); ok {
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, " "), path
		}
	}
	key := path
	if key == "" {
		key = str("pattern", "query", "url", "glob")
	}
	if key == "" {
		return name, path
	}
	return name + " " + key, path
}

// isFileWriteTool reports whether a tool changes the file it names.
func isFileWriteTool(name string) bool {
	switch strings.ToLower(name) {
	case "edit", "write", "multiedit", "multi_edit", "notebookedit", "notebook_edit", "str_replace", "str_replace_editor", "apply_patch":
		return true
	}
	return false
}
