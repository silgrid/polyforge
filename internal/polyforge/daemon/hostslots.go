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
// nothing (it only stops new starts). One-off tasks count against both
// ceilings like work-item jobs: a task spawn reserves a slot of its Cell
// through admitTask (refused, nothing queued, at either ceiling) and a job
// start counts every Cell's running tasks. The task lanes of every Cell
// also share one background-model-call bound of task.Limit (task.Slots);
// a summarize call takes none of it (the summarize lane keeps its own
// per-work-item in-flight and interval guards).

import (
	"fmt"
	"sort"
	"sync"

	"github.com/silgrid/polyforge/internal/common/api"
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
	// tasks is the shared background-model-call bound.
	tasks *task.Slots
	// taskRun counts each Cell's running one-off tasks (profile -> n):
	// they hold execution slots at both levels.
	taskRun map[string]int
}

// busyLocked answers the Host's taken slots: every Cell's running jobs
// and claims in flight plus every running task. s.mu held.
func (s *hostSlots) busyLocked(cells []*Daemon) int {
	busy := 0
	for _, c := range cells {
		busy += c.q.Busy()
	}
	for _, n := range s.taskRun {
		busy += n
	}
	return busy
}

// cellFullLocked reports whether d's own ceiling (the Host ceiling lowered
// by cell_max_concurrent_jobs) is taken by its jobs and tasks. s.mu held.
func (s *hostSlots) cellFullLocked(d *Daemon) bool {
	return d.q.Busy()+s.taskRun[d.profile()] >= d.q.MaxConcurrent()
}

// admitTask reserves one execution slot for a one-off task of d, or
// refuses it (DAEMON-409, nothing queued) at the Host or the Cell ceiling.
func (s *hostSlots) admitTask(d *Daemon) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if busy := s.busyLocked(s.cells()); busy >= s.max {
		return &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf("task spawn refused: the Host ceiling (max_concurrent_jobs %d) is taken by running jobs and tasks over every Cell; nothing is queued — retry when a slot frees", s.max)}
	}
	if s.cellFullLocked(d) {
		return &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf("task spawn refused: Cell %q's ceiling (%d) is taken by its running jobs and tasks; nothing is queued — retry when a slot frees", d.profile(), d.q.MaxConcurrent())}
	}
	if s.taskRun == nil {
		s.taskRun = map[string]int{}
	}
	s.taskRun[d.profile()]++
	return nil
}

// taskDone returns a slot admitTask reserved.
func (s *hostSlots) taskDone(d *Daemon) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := d.profile()
	if s.taskRun[p]--; s.taskRun[p] <= 0 {
		delete(s.taskRun, p)
	}
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
