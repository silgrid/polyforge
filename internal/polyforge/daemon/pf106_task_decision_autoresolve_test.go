package daemon

// carries: internal/polyforge/daemon/decisions.go, internal/polyforge/daemon/helpers_test.go, internal/common/protocol/decisions.go, internal/polyforge/daemon/pf106_task_decision_autoresolve_test.go

// pf106_task_decision_autoresolve_test.go (polyforge#106): a background
// task's failed / capped / interrupted entries still count as needing the
// owner, but they auto-resolve once they are no longer needed: the ticket
// the task served ends or gains a confirmed analysis, the same ticket's
// analysis runs to completion, or the entry simply ages past three days.
// Every auto-close writes resolved — never dismissed — and persists.

import (
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/task"
)

// pf106AnalysisTask answers one terminal analysis task of ticket wi
// (empty wi = an ordinary spawned task).
func pf106AnalysisTask(id string, st task.State, wi string) task.Task {
	return task.Task{ID: id, State: st, Report: "the report", Spec: task.Spec{AnalysisOf: wi}}
}

// pf106OpenTaskEntries answers the feed's open task entries, keyed by
// task id.
func pf106OpenTaskEntries(t *testing.T, f *decisionFeed) map[string]protocol.Decision {
	t.Helper()
	out := map[string]protocol.Decision{}
	for _, e := range f.List().Decisions {
		if e.Kind == protocol.DecisionTask && e.Open() {
			out[e.TaskID] = e
		}
	}
	return out
}

// TestPF106TaskEntryCarriesItsTicket (AC1): the entry of a daemon-scheduled
// analysis task (Spec.AnalysisOf) names the ticket it served — whatever
// terminal state it ended with — and a failed, capped or interrupted one
// still counts as needing the owner (the table's rule, unchanged); an
// ordinary task's entry names no ticket.
func TestPF106TaskEntryCarriesItsTicket(t *testing.T) {
	f := testFeed(t)
	states := []task.State{task.StateFailed, task.StateCapped, task.StateInterrupted}
	for i, st := range states {
		wi := "wi_pf106_" + string(rune('a'+i))
		if err := f.RecordTask("default", pf106AnalysisTask("task_00000000000000a"+string(rune('1'+i)), st, wi)); err != nil {
			t.Fatal(err)
		}
	}
	// an ordinary task (no ticket binding) is still a needing-attention entry
	if err := f.RecordTask("default", pf106AnalysisTask("task_00000000000000ff", task.StateFailed, "")); err != nil {
		t.Fatal(err)
	}
	feed := f.List()
	if feed.Attention != 4 {
		t.Fatalf("attention = %d, want the three analysis failures plus the plain one: %+v", feed.Attention, feed)
	}
	for _, e := range feed.Decisions {
		if e.Kind != protocol.DecisionTask {
			t.Fatalf("unexpected entry: %+v", e)
		}
		switch e.TaskID {
		case "task_00000000000000a1", "task_00000000000000a2", "task_00000000000000a3":
			if e.WI == "" || !e.Attention {
				t.Fatalf("an analysis task's terminal entry must carry its ticket and stay attention: %+v", e)
			}
		case "task_00000000000000ff":
			if e.WI != "" || !e.Attention {
				t.Fatalf("an ordinary task's entry names no ticket: %+v", e)
			}
		default:
			t.Fatalf("unexpected task entry: %+v", e)
		}
	}
	// a completed analysis task's entry carries the ticket too — as the
	// informational completion, never needing attention
	if err := f.RecordTask("default", pf106AnalysisTask("task_00000000000000aa", task.StateCompleted, "wi_pf106_done")); err != nil {
		t.Fatal(err)
	}
	if e := newest(f); e.TaskID != "task_00000000000000aa" || e.WI != "wi_pf106_done" || e.Attention {
		t.Fatalf("a completed analysis entry = %+v", e)
	}
}

// TestPF106RerunSuccessResolvesEarlierFailures (AC2): a later terminal of
// the same Cell's ticket resolves the ticket's earlier open task entries —
// a rerun that completed ends the story (the feed's attention count drops
// with it), a newer failure stands in for the older one (at most one open
// entry per ticket); another ticket's, and another Cell's entries of the
// same ticket id stay untouched.
func TestPF106RerunSuccessResolvesEarlierFailures(t *testing.T) {
	f := testFeed(t)
	rec := func(profile, id string, st task.State, wi string) {
		t.Helper()
		if err := f.RecordTask(profile, pf106AnalysisTask(id, st, wi)); err != nil {
			t.Fatal(err)
		}
	}
	entry := func(id string) protocol.Decision {
		t.Helper()
		for _, e := range f.List().Decisions {
			if e.TaskID == id {
				return e
			}
		}
		t.Fatalf("no entry of task %s", id)
		return protocol.Decision{}
	}
	rec("default", "task_0000000000000101", task.StateFailed, "wi_pf106")
	rec("alpha", "task_0000000000000201", task.StateFailed, "wi_pf106")
	if got := f.List().Attention; got != 2 {
		t.Fatalf("setup: attention = %d, want both first failures: %v", got, f.List())
	}
	// a newer terminal stands in for the older failure: one open entry of
	// the ticket per Cell
	rec("default", "task_0000000000000102", task.StateInterrupted, "wi_pf106")
	if got := f.List().Attention; got != 2 {
		t.Fatalf("the interrupted terminal left the earlier failure open: attention = %d, %v", got, f.List())
	}
	if e := entry("task_0000000000000101"); !e.Resolved || e.Dismissed {
		t.Fatalf("the earlier failure of the ticket must be resolved by the newer terminal: %+v", e)
	}
	if e := entry("task_0000000000000201"); e.Resolved {
		t.Fatalf("another Cell's entry of the same ticket must stay: %+v", e)
	}
	// the rerun completes: the ticket's story ends here, attention drops
	rec("default", "task_0000000000000103", task.StateCompleted, "wi_pf106")
	if got := f.List().Attention; got != 1 {
		t.Fatalf("a completed rerun must leave only the other Cell's entry: attention = %d, %v", got, f.List())
	}
	for _, id := range []string{"task_0000000000000101", "task_0000000000000102"} {
		if e := entry(id); !e.Resolved {
			t.Fatalf("the ticket's earlier entries must be resolved: %+v", e)
		}
	}
	if e := entry("task_0000000000000103"); e.Attention || e.Resolved {
		t.Fatalf("the completed rerun is the informational completion: %+v", e)
	}
	if e := entry("task_0000000000000201"); e.Resolved || !e.Open() {
		t.Fatalf("another Cell's entry of the same ticket must still stand open: %+v", e)
	}
	// another ticket's failure never resolves: the binding is the ticket id
	rec("default", "task_0000000000000301", task.StateFailed, "wi_pf106_other")
	if got := f.List().Attention; got != 2 {
		t.Fatalf("another ticket's failure must be its own open entry: attention = %d, %v", got, f.List())
	}
}

// TestPF106TicketEndResolvesTaskEntries (AC3): the Cell's reconcile reads
// its own open task entries' tickets at aihub: a ticket that ended —
// wrapped, canceled, failed, or gone (CORE-404) — resolves its entries;
// a queued, running or blocked one, a CORE-403 and a transport blip keep
// them for the next round; another Cell's ticket is never read through
// this Cell's hub (the read counts).
func TestPF106TicketEndResolvesTaskEntries(t *testing.T) {
	td := newTestDaemon(t, nil)
	d := td.Daemon
	f := d.feed
	rec := func(profile, wi string) {
		t.Helper()
		if err := f.RecordTask(profile, pf106AnalysisTask("task_0000000000000"+wi, task.StateFailed, wi)); err != nil {
			t.Fatal(err)
		}
	}
	for _, wi := range []string{"wi_pf106_blip", "wi_pf106_block", "wi_pf106_cancel", "wi_pf106_deny",
		"wi_pf106_end", "wi_pf106_fail", "wi_pf106_gone", "wi_pf106_live", "wi_pf106_run"} {
		rec("default", wi)
	}
	rec("alpha", "wi_pf106_alpha") // another Cell's entry: this hub never reads it
	td.hub.mu.Lock()
	td.hub.status["wi_pf106_end"] = api.StatusWrapped
	td.hub.status["wi_pf106_cancel"] = api.StatusCanceled
	td.hub.status["wi_pf106_fail"] = api.StatusFailed
	td.hub.unknown["wi_pf106_gone"] = true
	td.hub.status["wi_pf106_live"] = api.StatusQueued
	td.hub.status["wi_pf106_block"] = api.StatusBlocked
	td.hub.probe403["wi_pf106_deny"] = true
	td.hub.probeFails = 1 // the first read (wi_pf106_blip, sorted first) is a transport blip
	td.hub.mu.Unlock()
	// wi_pf106_run carries no status: the hub answers running

	open := map[string]bool{}
	for _, e := range f.List().Decisions {
		if e.Kind == protocol.DecisionTask && e.Profile == "default" {
			open[e.WI] = e.Open()
		}
	}
	d.reconcileDecisions()
	for wi, want := range map[string]bool{
		"wi_pf106_end": false, "wi_pf106_cancel": false, "wi_pf106_fail": false, "wi_pf106_gone": false,
		"wi_pf106_live": true, "wi_pf106_run": true, "wi_pf106_block": true,
		"wi_pf106_deny": true, "wi_pf106_blip": true,
	} {
		got := false
		for _, e := range f.List().Decisions {
			if e.Kind == protocol.DecisionTask && e.Profile == "default" && e.WI == wi {
				got = e.Open()
			}
		}
		if got != want {
			t.Fatalf("%s after the reconcile: open = %v, want %v", wi, got, want)
		}
	}
	// only this Cell's tickets were read, once each; the transport blip
	// never reached the hub's counter and is re-read the next round
	td.hub.mu.Lock()
	for wi, n := range td.hub.gets {
		t.Logf("hub gets %s = %d", wi, n)
	}
	if n := td.hub.gets["wi_pf106_alpha"]; n != 0 {
		t.Fatalf("another Cell's ticket was read through this Cell's hub: %d reads", n)
	}
	if n := td.hub.gets["wi_pf106_blip"]; n != 0 {
		t.Fatalf("the transport blip counted a read it never made: %d", n)
	}
	if n := td.hub.gets["wi_pf106_end"]; n != 1 {
		t.Fatalf("a resolved ticket must not be re-read: %d reads", n)
	}
	td.hub.mu.Unlock()
	// the kept entries wait for the next round: it reads them again
	d.reconcileDecisions()
	td.hub.mu.Lock()
	if n := td.hub.gets["wi_pf106_live"]; n != 2 {
		t.Fatalf("a kept ticket must be re-read each round: %d reads", n)
	}
	if n := td.hub.gets["wi_pf106_blip"]; n != 1 {
		t.Fatalf("the blipped ticket must be read again once the hub answers: %d reads", n)
	}
	td.hub.mu.Unlock()
	// and the alpha entry still stands: this Cell's reconcile never touched it
	for _, e := range f.List().Decisions {
		if e.Profile == "alpha" && (!e.Open() || e.Resolved) {
			t.Fatalf("another Cell's entry was resolved by this Cell's reconcile: %+v", e)
		}
	}
}
