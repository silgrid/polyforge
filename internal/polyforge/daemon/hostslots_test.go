package daemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/summarize"
	"github.com/silgrid/polyforge/internal/polyforge/task"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// slotCell is a bare Cell runtime: a profile and a queue (nothing running).
func slotCell(profile string, max int, wis ...string) *Daemon {
	q := NewJobQueue(max, func() map[string]bool { return map[string]bool{} })
	for _, wi := range wis {
		q.pending = append(q.pending, qEntry{WorkItemID: wi})
	}
	return &Daemon{cfg: Config{Profile: profile}, q: q}
}

// TestHostSlotsBoundTheSumAndRoundRobin: the Host ceiling bounds the sum
// over Cells, startable Cells alternate, an empty Cell holds no turn, and a
// lowered ceiling stops new starts without touching running ones.
func TestHostSlotsBoundTheSumAndRoundRobin(t *testing.T) {
	def := slotCell("default", 4, "wi_d1", "wi_d2", "wi_d3")
	alpha := slotCell("alpha", 1, "wi_a1", "wi_a2")
	empty := slotCell("beta", 4)
	s := &hostSlots{max: 3, tasks: &task.Slots{}, cells: func() []*Daemon { return []*Daemon{def, alpha, empty} }}

	// round-robin in profile order: alpha first (after ""), then default
	if e := s.take(def); e != nil {
		t.Fatalf("default jumped alpha's turn: %+v", e)
	}
	if e := s.take(alpha); e == nil || e.WorkItemID != "wi_a1" {
		t.Fatalf("alpha's turn: %+v", e)
	}
	if e := s.take(alpha); e != nil {
		t.Fatalf("alpha's own ceiling of 1 was passed: %+v", e)
	}
	if e := s.take(def); e == nil {
		t.Fatal("default must start once alpha is full")
	}
	if e := s.take(empty); e != nil {
		t.Fatalf("an empty Cell started something: %+v", e)
	}
	if e := s.take(def); e == nil {
		t.Fatal("default's second start (sum 3 = ceiling)")
	}
	if e := s.take(def); e != nil {
		t.Fatalf("the Host ceiling 3 was passed: %+v", e)
	}
	s.SetMax(1)
	if got := def.q.Busy() + alpha.q.Busy(); got != 3 {
		t.Fatalf("lowering the ceiling touched running work: busy %d", got)
	}
	if e := s.take(def); e != nil {
		t.Fatal("no new start above a lowered ceiling")
	}
}

// TestTasksTakeNoExecutionSlot (polyforge#74, AC7): a one-off task
// takes no execution slot at either ceiling — superseded
// TestTasksCountAgainstBothCeilings, which pinned the old bind; the
// task lane's own bound (max_concurrent_tasks) is the manager's check.
// The full both-directions pin (a spawn past full job ceilings, a job
// start past a full task bound) is TestW74TasksTakeNoJobSlots in
// w74_no_jobslot_bind_test.go; this one keeps the local, queue-only
// observation: busyLocked and cellFullLocked count jobs only, and a
// daemon's admitted tasks move neither.
func TestTasksTakeNoExecutionSlot(t *testing.T) {
	def := slotCell("default", 4, "wi_d1", "wi_d2")
	alpha := slotCell("alpha", 1, "wi_a1")
	s := &hostSlots{max: 4, tasks: &task.Slots{}, cells: func() []*Daemon { return []*Daemon{def, alpha} }}

	// alpha's job takes its own ceiling of 1; a task of alpha is admitted
	// anyway (the old code refused it at the Cell ceiling)
	if e := s.take(alpha); e == nil || e.WorkItemID != "wi_a1" {
		t.Fatalf("alpha's job must start: %+v", e)
	}
	alpha.cfg.HostSlots = s
	if err := alpha.admitTask(); err != nil {
		t.Fatalf("a task must take no Cell execution slot: %v", err)
	}

	// the Host/Cell counts are jobs only: admitted tasks move neither
	def.cfg.HostSlots = s
	if err := def.admitTask(); err != nil {
		t.Fatalf("a task must take no execution slot: %v", err)
	}
	if busy := s.busyLocked(s.cells()); busy != 1 {
		t.Fatalf("the Host count is jobs only, got %d", busy)
	}
	if s.cellFullLocked(def) {
		t.Fatal("the Cell ceiling counts jobs only — a task must not fill it")
	}

	// the Host ceiling taken by jobs refuses no task either
	s.SetMax(2)
	if e := s.take(def); e == nil {
		t.Fatal("default's job must start and take the Host ceiling of 2")
	}
	if err := def.admitTask(); err != nil {
		t.Fatalf("a task must take no Host execution slot: %v", err)
	}
	alpha.taskDone()
	def.taskDone()
	def.taskDone()
}

func TestNextTurn(t *testing.T) {
	for _, c := range []struct {
		startable  []string
		last, want string
	}{
		{nil, "x", ""},
		{[]string{"alpha", "default"}, "", "alpha"},
		{[]string{"alpha", "default"}, "alpha", "default"},
		{[]string{"alpha", "default"}, "default", "alpha"},
		{[]string{"default"}, "default", "default"},
	} {
		if got := nextTurn(c.startable, c.last); got != c.want {
			t.Fatalf("nextTurn(%v, %q) = %q, want %q", c.startable, c.last, got, c.want)
		}
	}
}

// fixedDispatcher answers every summarize call with the same summary.
type fixedDispatcher struct{}

func (fixedDispatcher) Dispatch(candidate, prompt string) (string, error) {
	return "summary", nil
}

// TestSummarizeRunsWithTheTaskBoundFull: a summarize call takes none of
// the Host's background task slots — with every task slot taken by running
// one-off tasks, a summary demand still dispatches and lands its summary.
func TestSummarizeRunsWithTheTaskBoundFull(t *testing.T) {
	slots := &task.Slots{}
	td := newTestDaemon(t, func(c *Config) {
		c.HostSlots = &hostSlots{max: 8, cells: func() []*Daemon { return nil }, tasks: slots}
		c.TaskRunner = blockRunner{}
		c.SummarizeDispatcher = fixedDispatcher{}
	})
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, workspace.FileName), []byte(taskYaml), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < task.DefaultLimit; i++ {
		if code, body := stepDo(t, td.sock, "POST", "/task/spawn", `{"project":"proj","prompt":"x"}`); code != 200 {
			t.Fatalf("spawn %d = %d %s", i, code, body)
		}
	}
	if n := slots.Running(); n != task.DefaultLimit {
		t.Fatalf("premise: every task slot is taken, running=%d", n)
	}
	if code, body := stepDo(t, td.sock, "POST", "/task/spawn", `{"project":"proj","prompt":"x"}`); code != 409 {
		t.Fatalf("premise: a further spawn is refused at the full bound, got %d %s", code, body)
	}

	transcript := filepath.Join(t.TempDir(), "dispatch.jsonl")
	if err := os.WriteFile(transcript, []byte("{\"t\":\"turn\",\"text\":\"hello\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(transcript)
	if err != nil {
		t.Fatal(err)
	}
	wi := "wi_bound_full"
	td.lane.Marked(wi, []string{"pi:m"}, "", 0, summarize.Dispatch{Token: "t1", Path: transcript, Size: st.Size()})
	deadline := time.Now().Add(5 * time.Second)
	for {
		p := td.lane.Projection(wi)
		if p.Summary != nil || p.SummaryError != nil {
			if p.SummaryError != nil {
				t.Fatalf("with the task bound full the summary must still land, got error %q", *p.SummaryError)
			}
			if got := strings.TrimSpace(*p.Summary); got != "summary" {
				t.Fatalf("with the task bound full the summary must still land, got %q", got)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("the summarize lane never answered: %+v", p)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
