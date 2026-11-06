package driver

// ask_resources_test.go: a step declaring `resources` (contract 2) gives
// its admission and locks back while it awaits the owner and takes them
// again before the owner's message is delivered (design §2.1, Q9 ③). Such
// a dispatch keeps no warm window: at its ask the idle process ends, the
// loop releases what the dispatch held, and the owner's message resumes
// the session through a dispatch that acquires them first.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// countingAdmission grants at once and records, per Enter, how many
// dispatches the spawner had started (the ordering proof), and the slots
// held now.
type countingAdmission struct {
	mu      sync.Mutex
	sp      *fakeSpawner
	atEnter []int
	held    int
}

func (a *countingAdmission) Enter(req AdmissionRequest) AdmissionTicket {
	n := len(a.sp.dispatched())
	a.mu.Lock()
	defer a.mu.Unlock()
	a.atEnter = append(a.atEnter, n)
	a.held++
	return &countingTicket{a: a}
}

func (a *countingAdmission) state() ([]int, int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return append([]int(nil), a.atEnter...), a.held
}

type countingTicket struct {
	a    *countingAdmission
	once sync.Once
}

func (t *countingTicket) Ready() <-chan struct{} {
	c := make(chan struct{})
	close(c)
	return c
}
func (t *countingTicket) State() (int, string) { return 0, "" }
func (t *countingTicket) Release() {
	t.once.Do(func() {
		t.a.mu.Lock()
		t.a.held--
		t.a.mu.Unlock()
	})
}

func TestAskReleasesTheDeclaredResourcesUntilTheReply(t *testing.T) {
	dir := t.TempDir()
	sp := &fakeSpawner{script: []fakeEntry{{hold: true}, {hold: true, res: relayCompleted()}, done(`{}`)}}
	// a warm window far past the test: only the declared resources end
	// the idle process
	d, log, _, _ := askEnv(t, dir, sp, nil, time.Hour)
	d.workflow.step("fix").Resources = &workflow.Resources{Admission: workflow.AdmissionHeavy}
	adm := &countingAdmission{sp: sp}
	d.cfg.Admission = adm
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	waitUntil(t, "the dispatch to start", func() bool { return len(sp.dispatched()) == 1 })
	if at, held := adm.state(); len(at) != 1 || at[0] != 0 || held != 1 {
		t.Fatalf("the heavy slot is taken before the dispatch: %v held=%d", at, held)
	}

	writeAsk(t, dir, "fix", 1, "lean or compatible?")
	waitUntil(t, "SYMPTOM: the idle process of a dispatch holding resources ends at its ask", func() bool { return sp.runs[0].terminated() })
	waitUntil(t, "the slot to be given back while the owner is asked", func() bool { _, held := adm.state(); return held == 0 })
	if got := sp.dispatched(); len(got) != 1 {
		t.Fatalf("re-dispatched before any reply: %v", got)
	}

	ack, err := d.OwnerMessage(ctx, "lean", "usr_root")
	if err != nil || ack.Kind != capsule.AskKindReply || ack.Disposition != dispositionResume {
		t.Fatalf("the reply is recorded and carried by the resume: %+v %v", ack, err)
	}
	waitUntil(t, "the resume dispatch", func() bool { return len(sp.dispatched()) == 2 })
	if at, held := adm.state(); len(at) != 2 || at[1] != 1 || held != 1 {
		t.Fatalf("the slot is taken again before the resume that delivers the reply: %v held=%d", at, held)
	}
	if sp2 := sp.spawns[1]; !sp2.Resume || len(sp2.OwnerMessages) != 1 || sp2.OwnerMessages[0].At != ack.At {
		t.Fatalf("the resume carries the reply: %+v", sp2)
	}
	close(sp.runs[1].rel)
	waitUntil(t, "the step to complete", func() bool { return stepDone(log, "fix") })
	waitUntil(t, "the slot to be free after the dispatch", func() bool { _, held := adm.state(); return held == 0 })
	cancel()
	<-errCh
}
