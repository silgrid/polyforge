package observe

// fake_test.go: the test doubles. The supervisor consumes the driver's
// facts and the harness Tracker (fed here directly through the standard
// signals); the record-level translation is pinned in package supervise.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// --- fakeHandle ---

type fakeHandle struct {
	exit chan struct{}
	once sync.Once
}

// newFakeHandle builds one fake driver handle. The label argument is
// documentation only (which derivation the handle plays in the test) —
// the v1.5 fence key is the spawn counter carried on the RT messages,
// not anything the handle owns.
func newFakeHandle(label string) *fakeHandle {
	return &fakeHandle{exit: make(chan struct{})}
}

func (h *fakeHandle) Exit() <-chan struct{} { return h.exit }

// dying marks the process as exited (confirmed death signal).
func (h *fakeHandle) die() { h.once.Do(func() { close(h.exit) }) }

// agentTable is a fake Config.Agent resolver: wi → live handle + capsule.
type agentTable struct {
	mu sync.Mutex
	m  map[string]agentEntry
}

type agentEntry struct {
	h   Handle
	dir string
}

func newAgentTable() *agentTable { return &agentTable{m: map[string]agentEntry{}} }

func (a *agentTable) set(wi string, h Handle, capsuleDir string) {
	a.mu.Lock()
	a.m[wi] = agentEntry{h, capsuleDir}
	a.mu.Unlock()
}

func (a *agentTable) lookup(wi string) (Handle, WIInfo, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.m[wi]
	if !ok {
		return nil, WIInfo{}, false
	}
	return e.h, WIInfo{WorkItem: wi, CapsuleDir: e.dir}, true
}

// --- monitor construction ---

func testCfg() monitorCfg {
	return monitorCfg{
		PollInterval:     5 * time.Millisecond,
		ModelErrorGrace:  40 * time.Millisecond,
		DriverStall:      time.Hour, // inert unless a test shrinks it
		PushRetryBackoff: 2 * time.Millisecond,
		PushBackoffMax:   20 * time.Millisecond,
		PushBound:        time.Second,
		push: func(ctx context.Context, o gitops.Opts, message string) (string, bool, gitops.PushTimings, error) {
			return "sha_default", true, gitops.PushTimings{}, nil
		},
	}
}

// newTestMonitor builds a monitor wired to a temp capsule + events log
// and runs it. The returned cleanup stops it.
func newTestMonitor(t *testing.T, h Handle, mod func(*monitorCfg)) (*Monitor, *supervise.Queue[protocol.OBMsg]) {
	t.Helper()
	dir := t.TempDir()
	log, err := NewEventLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	capsuleDir := filepath.Join(dir, "capsule")
	if err := os.MkdirAll(filepath.Join(capsuleDir, "runtime-state"), 0o755); err != nil {
		t.Fatal(err)
	}
	ob := supervise.NewQueue[protocol.OBMsg]()
	cfg := testCfg()
	if mod != nil {
		mod(&cfg)
	}
	m := &Monitor{
		wi:         WIInfo{WorkItem: "wi_test", CapsuleDir: capsuleDir},
		cfg:        cfg,
		log:        log,
		ob:         ob,
		link:       supervise.NewLink("wi_test"),
		rtQ:        supervise.NewQueue[Attach](),
		stopCh:     make(chan struct{}),
		gate:       make(chan struct{}, 1),
		pushDone:   make(chan pushResult, 1),
		verifyDone: make(chan verifyResult, 1),
	}
	m.attach(Attach{
		Msg:    protocol.RTMsg{Kind: protocol.RTStarted, WorkItem: "wi_test", SpawnN: 1},
		Handle: h,
	})
	m.phase = phaseBetween
	m.phaseSince = m.now()
	m.announceStart()
	go m.run()
	t.Cleanup(func() {
		m.requestStop()
		_ = log.Close()
	})
	return m, ob
}

// startDispatch announces one dispatch with the given thresholds through
// the link (the driver's step_started fact) and returns its tracker.
func startDispatch(m *Monitor, spawn int64, step string, th supervise.Thresholds) *supervise.Tracker {
	m.link.Fact(supervise.StepStarted{
		Step: step, SpawnN: spawn, Model: "m", Harness: "pi",
		StartedAt: m.link.Clock(), Thresholds: th,
	})
	return m.link.Tracker(spawn)
}

// nextVerdict waits for one verdict on the link.
func nextVerdict(t *testing.T, m *Monitor, what string) supervise.Verdict {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if vs := m.link.Verdicts.Drain(); len(vs) > 0 {
			if len(vs) > 1 {
				t.Fatalf("%s: more than one verdict at once: %+v", what, vs)
			}
			return vs[0]
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for verdict %s", what)
	return nil
}

// expectNoVerdict asserts the supervisor stays silent for d.
func expectNoVerdict(t *testing.T, m *Monitor, d time.Duration, what string) {
	t.Helper()
	time.Sleep(d)
	if vs := m.link.Verdicts.Drain(); len(vs) > 0 {
		t.Fatalf("unexpected verdict (%s): %+v", what, vs)
	}
}

// writeExec writes capsule runtime-state/execution.json with the given
// version and current step (atomic tmp+rename, the capsule write shape).
func (m *Monitor) writeExec(t *testing.T, version int64, currentStep string) {
	t.Helper()
	exec := map[string]any{
		"capsule_id":      "cap_wi_test",
		"work_item_id":    "wi_test",
		"generation":      int64(1),
		"current_step":    currentStep,
		"workflow_status": "running",
		"steps": map[string]any{
			currentStep: map[string]any{
				"status":          "in_progress",
				"attempts":        []any{},
				"answers":         []any{},
				"budget_consumed": map[string]any{"usd": 0.1},
			},
		},
		"review_rounds": map[string]int64{},
		"updated_at":    time.Now().UTC().Format(time.RFC3339Nano),
		"version":       version,
	}
	raw, err := json.Marshal(exec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.wi.CapsuleDir, "runtime-state", "execution.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	// force a distinct mtime from any previous write
	os.Chtimes(tmp, time.Now(), time.Now())
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	// the driver's commit-boundary notice (the supervisor no longer polls)
	m.link.Fact(supervise.ExecCommitted{Version: version, Step: currentStep})
}

// writePendingQuestion seeds an open pending question the way the driver
// does: the step's pending_question lands in execution.json (Version
// bumped) and the monitor's poll picks it up (applyPendingFromExec).
// It waits until the monitor's pending projection reflects it.
func writePendingQuestion(t *testing.T, m *Monitor, qid, question, step string) {
	t.Helper()
	writePendingQuestionAt(t, m, qid, question, step, time.Now().UTC().Format(time.RFC3339Nano))
}

// writePendingQuestionAt is writePendingQuestion with an explicit
// asked_at (the one field an idempotent re-presentation moves).
func writePendingQuestionAt(t *testing.T, m *Monitor, qid, question, step, askedAt string) {
	t.Helper()
	path := filepath.Join(m.wi.CapsuleDir, "runtime-state", "execution.json")
	var version int64 = 1
	var prev time.Time
	if raw, err := os.ReadFile(path); err == nil {
		var old struct {
			Version int64 `json:"version"`
		}
		if json.Unmarshal(raw, &old) == nil {
			version = old.Version + 1
		}
		if st, err := os.Stat(path); err == nil {
			prev = st.ModTime()
		}
	}
	exec := map[string]any{
		"capsule_id":      "cap_wi_test",
		"work_item_id":    "wi_test",
		"generation":      int64(1),
		"current_step":    step,
		"workflow_status": "running",
		"steps": map[string]any{
			step: map[string]any{
				"status":   "in_progress",
				"attempts": []any{},
				"answers":  []any{},
				"pending_question": map[string]any{
					"id": qid, "step": step, "question": question, "asked_at": askedAt,
				},
			},
		},
		"review_rounds": map[string]int64{},
		"updated_at":    time.Now().UTC().Format(time.RFC3339Nano),
		"version":       version,
	}
	raw, err := json.Marshal(exec)
	if err != nil {
		t.Fatal(err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	mt := time.Now()
	if !mt.After(prev) {
		mt = prev.Add(time.Millisecond)
	}
	os.Chtimes(tmp, mt, mt)
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
	m.link.Fact(supervise.ExecCommitted{Version: version, Step: step})
	soon(t, 2*time.Second, "the commit boundary carrying "+qid+" published", func() bool {
		for _, e := range eventsOf(m, EvPublished) {
			var b struct {
				Version int64 `json:"version"`
			}
			if json.Unmarshal(e.Body, &b) == nil && b.Version == version {
				return true
			}
		}
		return false
	})
}

// waitOB reads one OB message or fails the test on timeout.
func waitOB(t *testing.T, ob *supervise.Queue[protocol.OBMsg], what string) protocol.OBMsg {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		if msg, ok := ob.Pop(); ok {
			return msg
		}
		select {
		case <-ob.Notify():
		case <-deadline:
			t.Fatalf("timed out waiting for OB message %s", what)
			return protocol.OBMsg{}
		}
	}
}

// expectNoOB asserts no OB message arrives within d.
func expectNoOB(t *testing.T, ob *supervise.Queue[protocol.OBMsg], d time.Duration, what string) {
	t.Helper()
	deadline := time.After(d)
	for {
		if msg, ok := ob.Pop(); ok {
			t.Fatalf("unexpected OB message %s: %+v", what, msg)
		}
		select {
		case <-ob.Notify():
		case <-deadline:
			return
		}
	}
}

// soon polls cond until it holds or fails after the bound.
func soon(t *testing.T, bound time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(bound)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("condition not met in time: %s", what)
}

// eventsOf reads the monitor's work item's events, filtered by kind.
func eventsOf(m *Monitor, kind string) []protocol.EventEntry {
	var out []protocol.EventEntry
	for _, e := range m.log.SinceAfter(m.wi.WorkItem, 0) {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// rawWrite rewrites execution.json WITHOUT the commit-boundary fact.
func rawWrite(t *testing.T, m *Monitor, version int64, step string) {
	t.Helper()
	exec := map[string]any{
		"capsule_id": "cap_wi_test", "work_item_id": "wi_test", "current_step": step,
		"workflow_status": "running", "version": version,
		"steps": map[string]any{step: map[string]any{"status": "in_progress", "attempts": []any{}, "answers": []any{}}},
	}
	raw, err := json.Marshal(exec)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(m.wi.CapsuleDir, "runtime-state", "execution.json")
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}
