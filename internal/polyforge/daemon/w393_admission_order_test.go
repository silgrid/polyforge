package daemon

// w393_admission_order_test.go: a queued heavy run is
// admitted in the admission order — priority, then the time it first
// queued, then arrival — and the displayed position is that order. (The
// per-repo bar lock's grant-order cases left with the lock,.)

import (
	"fmt"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

// positionsOf answers each ticket's displayed queue position (0 = admitted).
func positionsOf(ts ...driver.AdmissionTicket) []int {
	out := make([]int, len(ts))
	for i, t := range ts {
		out[i], _ = t.State()
	}
	return out
}

// onlyAdmitted answers the index of the one admitted ticket of ts (-1 when
// none or several are).
func onlyAdmitted(ts ...driver.AdmissionTicket) int {
	at := -1
	for i, tk := range ts {
		if admitted(tk) {
			if at >= 0 {
				return -1
			}
			at = i
		}
	}
	return at
}

// Six normal runs queue behind the one slot, then a high one arrives: it
// is shown first and is the next admitted (the 2026-11-08 live case).
func TestAdmissionHighRunGoesAheadOfEarlierQueued(t *testing.T) {
	q := newVerifyQueue(1, 100)
	q.load = func() (float64, bool) { return 0, true }
	base := time.Date(2026, 11, 8, 3, 13, 0, 0, time.UTC)
	run := q.enter(driver.AdmissionRequest{WI: "wi_run", Priority: "normal", Since: base.Add(-time.Hour)})
	if !admitted(run) {
		t.Fatal("the first run is admitted")
	}
	var all []driver.AdmissionTicket
	for i := range 6 {
		all = append(all, q.enter(driver.AdmissionRequest{WI: fmt.Sprintf("wi_n%d", i), Priority: "normal",
			Since: base.Add(time.Duration(i) * 4 * time.Minute)}))
	}
	high := q.enter(driver.AdmissionRequest{WI: "wi_high", Priority: "high", Since: base.Add(37 * time.Minute)})
	all = append([]driver.AdmissionTicket{high}, all...)
	if got := positionsOf(all...); fmt.Sprint(got) != "[1 2 3 4 5 6 7]" {
		t.Fatalf("positions (high, n0..n5) %v, want high first then first-queued order", got)
	}
	run.Release()
	if i := onlyAdmitted(all...); i != 0 {
		t.Fatalf("the slot went to entry %d, not to the high run", i)
	}
	if got := positionsOf(all[1:]...); fmt.Sprint(got) != "[1 2 3 4 5 6]" {
		t.Fatalf("positions after the admission %v", got)
	}
	for _, tk := range all {
		tk.Release()
	}
}

// Same priority: the time a run first queued decides — a restart re-queue
// passes its recorded stamp and takes its old place ahead of runs that
// queued later but entered first; equal stamps keep arrival order.
func TestAdmissionSamePriorityFirstQueuedOrder(t *testing.T) {
	q := newVerifyQueue(1, 100)
	q.load = func() (float64, bool) { return 0, true }
	base := time.Date(2026, 11, 8, 10, 0, 0, 0, time.UTC)
	holder := q.enter(driver.AdmissionRequest{WI: "wi_holder", Since: base.Add(-time.Hour)})
	a := q.enter(driver.AdmissionRequest{WI: "wi_a", Since: base})
	b := q.enter(driver.AdmissionRequest{WI: "wi_b", Since: base.Add(5 * time.Minute)})
	b2 := q.enter(driver.AdmissionRequest{WI: "wi_b2", Since: base.Add(5 * time.Minute)})
	// a daemon restart re-queues a run that first queued before all of them
	re := q.enter(driver.AdmissionRequest{WI: "wi_re", Since: base.Add(-5 * time.Minute)})
	order := []driver.AdmissionTicket{re, a, b, b2}
	if got := positionsOf(order...); fmt.Sprint(got) != "[1 2 3 4]" {
		t.Fatalf("positions (re, a, b, b2) %v", got)
	}
	holder.Release()
	for i, tk := range order {
		if j := onlyAdmitted(order[i:]...); j != 0 {
			t.Fatalf("admission %d went to entry %d of the rest", i, j)
		}
		tk.Release()
	}
}
