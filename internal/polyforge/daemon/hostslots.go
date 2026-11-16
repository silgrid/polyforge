package daemon

// hostslots.go: the two-level concurrency of a Host. Every Cell keeps its
// own queue and its own ceiling (the Host ceiling, lowered by the Cell's
// optional cell_max_concurrent_jobs); the Host ceiling bounds the SUM over
// all Cells. A Cell's dispatch takes its next entry through the Host gate:
// under the gate's mutex the Host counts the busy slots of every Cell
// (running jobs — a paused job, waiting for a human with no live process,
// holds none — plus claims in flight) and, when more than one Cell could
// start something, admits them round-robin in profile order. No weights,
// no preemption; an empty Cell holds no slot; a lowered limit kills
// nothing (it only stops new starts). One-off tasks take NO execution slot
// here (polyforge#74): max_concurrent_jobs and cell_max_concurrent_jobs
// bound work-item jobs only, and the task lanes of every Cell share their
// own bound — the Host's [daemon] max_concurrent_tasks (task.Slots, set
// once by NewHost and moved live by the default Cell's reload); a spawn
// past it is refused by the manager, never here. A summarize call takes
// none of it either (the summarize lane keeps its own per-work-item
// in-flight and interval guards).

import (
	"sort"
	"sync"

	"github.com/silgrid/polyforge/internal/polyforge/task"
)

// hostSlots is the Host's ceiling over every Cell's execution slots.
type hostSlots struct {
	mu  sync.Mutex
	max int
	// last is the profile of the Cell that started work last (the
	// round-robin cursor).
	last string
	// cells answers the serving Cells in profile order.
	cells func() []*Daemon
	// tasks is the shared task-lane bound (max_concurrent_tasks).
	tasks *task.Slots
}

// busyLocked answers the Host's taken slots: every Cell's running jobs
// and claims in flight. One-off tasks hold none (polyforge#74). s.mu held.
func (s *hostSlots) busyLocked(cells []*Daemon) int {
	busy := 0
	for _, c := range cells {
		busy += c.q.Busy()
	}
	return busy
}

// cellFullLocked reports whether d's own ceiling (the Host ceiling lowered
// by cell_max_concurrent_jobs) is taken by its jobs. One-off tasks hold
// none (polyforge#74). s.mu held.
func (s *hostSlots) cellFullLocked(d *Daemon) bool {
	return d.q.Busy() >= d.q.MaxConcurrent()
}

// Max answers the Host ceiling.
func (s *hostSlots) Max() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.max
}

// SetMax changes the Host ceiling (running work is never stopped).
func (s *hostSlots) SetMax(n int) {
	s.mu.Lock()
	s.max = n
	s.mu.Unlock()
}

// take answers d's next queue entry if the Host ceiling and the
// round-robin turn admit it (nil = not now).
func (s *hostSlots) take(d *Daemon) *qEntry {
	s.mu.Lock()
	defer s.mu.Unlock()
	cells := s.cells()
	busy := s.busyLocked(cells)
	var startable []string
	for _, c := range cells {
		if c.q.Startable() && !s.cellFullLocked(c) {
			startable = append(startable, c.profile())
		}
	}
	sort.Strings(startable)
	if busy >= s.max || s.cellFullLocked(d) {
		return nil
	}
	if turn := nextTurn(startable, s.last); turn != "" && turn != d.profile() {
		return nil
	}
	e := d.q.Take()
	if e != nil {
		s.last = d.profile()
	}
	return e
}

// nextTurn answers the first startable profile after last (wrapping);
// startable is sorted. "" = nobody can start.
func nextTurn(startable []string, last string) string {
	if len(startable) == 0 {
		return ""
	}
	for _, p := range startable {
		if p > last {
			return p
		}
	}
	return startable[0]
}
