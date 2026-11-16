package daemon

import (
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/task"
)

// w74_no_jobslot_bind_test.go (polyforge#74, AC7): one-off tasks take no
// job slot. `max_concurrent_jobs` (and a Cell's cell_max_concurrent_jobs)
// bound only work-item jobs: a spawn is never refused at either job
// ceiling, and a running task never holds back a job start.

// TestW74TasksTakeNoJobSlots: with the Host ceiling taken by running jobs
// a task spawn is admitted; with the task bound taken by admitted tasks a
// job still starts; a Cell's own taken ceiling refuses no task either.
func TestW74TasksTakeNoJobSlots(t *testing.T) {
	def := slotCell("default", 4, "wi_d1", "wi_d2")
	alpha := slotCell("alpha", 4, "wi_a1")
	cells := func() []*Daemon { return []*Daemon{def, alpha} }

	// jobs fill the Host ceiling of 2 — a task spawn passes it anyway
	s := &hostSlots{max: 2, tasks: &task.Slots{}, cells: cells}
	if e := s.take(def); e != nil {
		t.Fatal("default must wait its round-robin turn")
	}
	if e := s.take(alpha); e == nil || e.WorkItemID != "wi_a1" {
		t.Fatalf("alpha's turn must start (the Host ceiling fills): %+v", e)
	}
	def.cfg.HostSlots = s
	if err := def.admitTask(); err != nil {
		t.Fatalf("a task must not be refused by the job ceilings: %v", err)
	}

	// the reverse bind: tasks fill the task bound — a job start passes
	s2 := &hostSlots{max: 2, tasks: &task.Slots{}, cells: cells}
	def2 := slotCell("default", 4)
	def2.cfg.HostSlots = s2
	for i := 0; i < task.DefaultLimit; i++ {
		if err := def2.admitTask(); err != nil {
			t.Fatalf("admit %d: %v", i, err)
		}
	}
	if e := s2.take(alpha); e == nil {
		t.Fatal("a job must start while every task slot is taken")
	}

	// a Cell's own taken ceiling refuses no task either
	beta := slotCell("beta", 1, "wi_b1")
	s3 := &hostSlots{max: 4, tasks: &task.Slots{}, cells: func() []*Daemon { return []*Daemon{beta} }}
	if e := s3.take(beta); e == nil {
		t.Fatal("beta's job must start and take its own ceiling of 1")
	}
	beta.cfg.HostSlots = s3
	if err := beta.admitTask(); err != nil {
		t.Fatalf("a task must not be refused by its Cell's job ceiling: %v", err)
	}
}
