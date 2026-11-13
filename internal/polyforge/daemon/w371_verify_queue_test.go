package daemon

// w371_verify_queue_test.go: the heavy-run admission —
// two gates (global slots, the load), priority-then-since order, and a
// queued run holds no job slot.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

func admitted(t driver.AdmissionTicket) bool {
	select {
	case <-t.Ready():
		return true
	default:
		return false
	}
}

func TestVerifyQueueGatesAndOrder(t *testing.T) {
	q := newVerifyQueue(1, 8)
	load, loadOK := 20.0, true
	q.load = func() (float64, bool) { return load, loadOK }
	base := time.Date(2026, 11, 7, 12, 0, 0, 0, time.UTC)

	// the first run is admitted even under a load above the threshold: no
	// admitted run is running, so the queue always moves
	a := q.enter(driver.AdmissionRequest{WI: "wi_a", Priority: "normal", Since: base})
	if !admitted(a) {
		t.Fatal("the first run is admitted whatever the load")
	}
	// the slot is taken: everything else queues, ordered by priority then since
	low := q.enter(driver.AdmissionRequest{WI: "wi_low", Priority: "low", Since: base.Add(-time.Hour)})
	late := q.enter(driver.AdmissionRequest{WI: "wi_late", Priority: "high", Since: base.Add(2 * time.Minute)})
	early := q.enter(driver.AdmissionRequest{WI: "wi_early", Priority: "high", Since: base.Add(time.Minute)})
	for _, c := range []struct {
		t   driver.AdmissionTicket
		pos int
	}{{early, 1}, {late, 2}, {low, 3}} {
		if pos, reason := c.t.State(); pos != c.pos || reason == "" || admitted(c.t) {
			t.Fatalf("queued position %d %q, want %d", pos, reason, c.pos)
		}
	}
	if !q.queuedFor("wi_low") || q.queuedFor("wi_a") {
		t.Fatal("queuedFor names exactly the queued work items")
	}
	if w, ok := q.waiting("wi_late"); !ok || w.position != 2 || !w.since.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("waiting: %+v %v", w, ok)
	}

	// a raise of the slots: the load gate now holds (a run is running and
	// the load is above the threshold)
	q.SetLimits(3, 8)
	if admitted(early) {
		t.Fatal("the load gate holds while a run runs")
	}
	if _, reason := early.State(); reason == "" || reason[:4] != "load" {
		t.Fatalf("reason %q", reason)
	}
	// an unreadable load ignores the gate: the head two are admitted
	loadOK = false
	q.Tick()
	if !admitted(early) || !admitted(late) || admitted(low) {
		t.Fatal("slots 3, no load reading: the two first in order run")
	}
	// a release admits the next one at once
	a.Release()
	if !admitted(low) {
		t.Fatal("a released slot admits the next run")
	}
	// a queued ask that leaves frees its place
	b := q.enter(driver.AdmissionRequest{WI: "wi_b"})
	if admitted(b) {
		t.Fatal("slots full")
	}
	b.Release()
	if q.queuedFor("wi_b") {
		t.Fatal("a released queued ask leaves the queue")
	}
}

func waitAdmitted(t *testing.T, tk driver.AdmissionTicket) {
	t.Helper()
	select {
	case <-tk.Ready():
	case <-time.After(5 * time.Second):
		pos, reason := tk.State()
		t.Fatalf("never admitted (#%d: %s)", pos, reason)
	}
}

// A queued verification holds no job slot: occupiedSlots and the status
// face's running count leave it out, the status job shows the queue (the
// watch snapshot does not), and the release gives the slot back.
func TestQueuedVerificationHoldsNoSlot(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_q")
	if !td.occupiedSlots()["wi_q"] {
		t.Fatal("a live job holds its slot")
	}
	td.verifyQ.SetLimits(1, 1000)
	filler := td.verifyQ.enter(driver.AdmissionRequest{WI: "wi_other"})
	if !admitted(filler) {
		t.Fatal("the filler takes the one slot")
	}
	tk := cellAdmission{td.Daemon}.Enter(driver.AdmissionRequest{WI: "wi_q", Step: "verification"})
	if admitted(tk) {
		t.Fatal("the slot is taken: wi_q queues")
	}
	if td.occupiedSlots()["wi_q"] {
		t.Fatal("a queued verification holds no job slot")
	}
	jobs, running := td.statusJobs()
	var face *statusJob
	for i := range jobs {
		if jobs[i].WorkItemID == "wi_q" {
			face = &jobs[i]
		}
	}
	if face == nil || face.VerifyQueue == nil || face.VerifyQueue.Position != 1 || running != 0 {
		t.Fatalf("status face %+v running %d", face, running)
	}
	// workflow contract v2 change 20: the watch snapshot no longer projects
	// the queue (its `verify_queued` event says it there)
	if raw, err := json.Marshal(td.watchSnapshot("wi_q", nil, false, 0)); err != nil || strings.Contains(string(raw), "verify_queue") {
		t.Fatalf("the watch snapshot carries no verify_queue projection: %s (%v)", raw, err)
	}
	filler.Release()
	if !admitted(tk) || !td.occupiedSlots()["wi_q"] {
		t.Fatal("admitted, the run holds its slot again")
	}
	tk.Release()
}

// The admission keys are live: a reload moves the admission's slots and
// load threshold.
func TestReloadAppliesVerifyKeysLive(t *testing.T) {
	td := newTestDaemon(t, nil)
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cli.toml")
	td.cfg.ConfigPath = path
	toml := "[workspace.\"" + ws + "\"]\nprojects = [\"proj\"]\n\n[daemon]\nmax_concurrent_bars = 3\nbar_load_threshold = 6.5\n"
	if err := os.WriteFile(path, []byte(toml), 0o600); err != nil {
		t.Fatal(err)
	}
	resp, err := td.reloadConfig()
	if err != nil {
		t.Fatal(err)
	}
	applied := map[string]bool{}
	for _, o := range resp.Outcomes {
		applied[o.Key] = o.Status == "applied"
	}
	if !applied["max_concurrent_bars"] || !applied["bar_load_threshold"] {
		t.Fatalf("outcomes %+v", resp.Outcomes)
	}
	td.verifyQ.mu.Lock()
	n, thr := td.verifyQ.limitsLocked()
	td.verifyQ.mu.Unlock()
	if n != 3 || thr != 6.5 {
		t.Fatalf("limits after reload %d %v", n, thr)
	}
}
