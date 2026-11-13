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

// TestTasksCountAgainstBothCeilings: a one-off task takes an execution
// slot at the Host and at its Cell — with a Host ceiling of 1 a running job
// refuses a task and a running task holds back every job start; a Cell's
// own ceiling counts its tasks too; a finished task frees its slot.
func TestTasksCountAgainstBothCeilings(t *testing.T) {
	def := slotCell("default", 4, "wi_d1", "wi_d2")
	alpha := slotCell("alpha", 1, "wi_a1")
	s := &hostSlots{max: 1, tasks: &task.Slots{}, cells: func() []*Daemon { return []*Daemon{def, alpha} }}

	if err := s.admitTask(def); err != nil {
		t.Fatalf("a free Host admits a task: %v", err)
	}
	if e := s.take(alpha); e != nil {
		t.Fatalf("a job started past the Host ceiling taken by a task: %+v", e)
	}
	if err := s.admitTask(alpha); err == nil || !strings.Contains(err.Error(), "Host ceiling") {
		t.Fatalf("a second task past the Host ceiling of 1 must be refused: %v", err)
	}
	s.taskDone(def)
	if e := s.take(alpha); e == nil {
		t.Fatal("the finished task must free its slot")
	}
	if err := s.admitTask(def); err == nil {
		t.Fatal("a task past the Host ceiling taken by a job must be refused")
	}

	// the Cell ceiling counts the Cell's tasks
	s.SetMax(4)
	if err := s.admitTask(alpha); err == nil || !strings.Contains(err.Error(), `Cell "alpha"`) {
		t.Fatalf("alpha's ceiling of 1 is taken by its job: %v", err)
	}
	beta := slotCell("beta", 1, "wi_b1")
	s.cells = func() []*Daemon { return []*Daemon{def, alpha, beta} }
	if err := s.admitTask(beta); err != nil {
		t.Fatalf("beta admits one task: %v", err)
	}
	if e := s.take(beta); e != nil {
		t.Fatalf("beta's job started past its ceiling of 1 taken by a task: %+v", e)
	}
	if got := s.taskRun["beta"]; got != 1 {
		t.Fatalf("beta's running tasks = %d", got)
	}
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
	for i := 0; i < task.Limit; i++ {
		if code, body := stepDo(t, td.sock, "POST", "/task/spawn", `{"project":"proj","prompt":"x"}`); code != 200 {
			t.Fatalf("spawn %d = %d %s", i, code, body)
		}
	}
	if n := slots.Running(); n != task.Limit {
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
