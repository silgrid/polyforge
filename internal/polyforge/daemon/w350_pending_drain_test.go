package daemon

// w350_pending_drain_test.go (D4): while a verified
// same-identity rotation is pending, a task spawn is refused DAEMON-409
// naming the pending rotation and leaves no record; a running one-off task
// keeps the Cell from being safe-idle; and an admission half-way under
// drainGate holds the switch until it landed, which it then sees.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/task"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

func w350PendingDaemon(t *testing.T) *testDaemon {
	t.Helper()
	hub := keyUserHub(t, map[string]string{"k-old": "usr_1", "k-new": "usr_1"})
	td := newTestDaemon(t, func(c *Config) {
		c.Server, c.APIKey = hub.URL, "k-old"
		c.TaskRunner = blockRunner{}
	})
	<-td.ready
	if err := writeIdentity(td.cfg.DataDir, cellIdentity{Server: hub.URL, User: "usr_1"}); err != nil {
		t.Fatal(err)
	}
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, workspace.FileName), []byte(taskYaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return td
}

// w350GoPending makes the rotation pending while the Cell is held busy
// (one in-flight queue entry, released by the returned func).
func w350GoPending(t *testing.T, td *testDaemon) func() {
	t.Helper()
	td.q.mu.Lock()
	td.q.inflight["wi_busy"] = qEntry{}
	td.q.mu.Unlock()
	if out, err := td.checkConnectionChange(td.cfg.Server, "k-new"); err != nil || !strings.Contains(out, "pending") {
		t.Fatalf("a same-identity rotation must go pending: %q %v", out, err)
	}
	return func() {
		td.q.mu.Lock()
		delete(td.q.inflight, "wi_busy")
		td.q.mu.Unlock()
	}
}

func TestW350PendingRotationRefusesTaskSpawn(t *testing.T) {
	td := w350PendingDaemon(t)
	release := w350GoPending(t, td)
	defer release()
	code, body := stepDo(t, td.sock, "POST", "/task/spawn", `{"project":"proj","prompt":"x"}`)
	if code != 409 || !strings.Contains(body, "pending connection rotation") || !strings.Contains(body, "nothing is queued") {
		t.Fatalf("a task spawn while the rotation is pending = %d %s", code, body)
	}
	if ts, _ := td.tasks.List(); len(ts) != 0 || td.tasksHeld.Load() != 0 {
		t.Fatalf("the refused spawn left %d record(s), %d held", len(ts), td.tasksHeld.Load())
	}
}

func TestW350RunningTaskBlocksTheSwitch(t *testing.T) {
	td := w350PendingDaemon(t)
	code, body := stepDo(t, td.sock, "POST", "/task/spawn", `{"project":"proj","prompt":"x"}`)
	if code != 200 {
		t.Fatalf("spawn = %d %s", code, body)
	}
	var tk task.Task
	if err := json.Unmarshal([]byte(body), &tk); err != nil {
		t.Fatal(err)
	}
	w350GoPending(t, td)()            // pending, and the queue is idle again at once
	time.Sleep(60 * time.Millisecond) // several dispatch ticks (10 ms)
	if td.pendingChange() == "" {
		t.Fatal("the rotation switched while a one-off task was running")
	}
	if err := td.tasks.Cancel(tk.ID); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for td.pendingChange() != "" {
		if time.Now().After(deadline) {
			t.Fatal("the Cell never switched once its task ended")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestW350SwitchWaitsForAnAdmissionHalfWay(t *testing.T) {
	td := w350PendingDaemon(t)
	w350GoPending(t, td)()
	// An admission that passed its pending check before the rotation went
	// pending is still under the gate: the switch cannot decide meanwhile.
	td.drainGate.Lock()
	time.Sleep(60 * time.Millisecond)
	if td.pendingChange() == "" {
		td.drainGate.Unlock()
		t.Fatal("the switch decided while an admission held the gate")
	}
	td.tasksHeld.Add(1) // the admission lands
	td.drainGate.Unlock()
	time.Sleep(60 * time.Millisecond)
	if td.pendingChange() == "" {
		t.Fatal("the switch did not see the admission that landed under the gate")
	}
	td.tasksHeld.Add(-1)
	deadline := time.Now().Add(5 * time.Second)
	for td.pendingChange() != "" {
		if time.Now().After(deadline) {
			t.Fatal("the idle Cell never switched")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// A submit or auto-wave entry persists its queue entry outside drainGate
// under an admitting reservation: the open reservation keeps the Cell from
// being safe-idle, so the switch waits for it.
func TestW350SwitchWaitsForAnAdmittingReservation(t *testing.T) {
	td := w350PendingDaemon(t)
	w350GoPending(t, td)()
	td.admitting.Add(1) // passed its pending check, persisting outside the gate
	time.Sleep(60 * time.Millisecond)
	if td.pendingChange() == "" {
		td.admitting.Add(-1)
		t.Fatal("the switch decided while an admission was persisting its entry")
	}
	if !td.drainGate.TryLock() {
		td.admitting.Add(-1)
		t.Fatal("the reservation must not hold drainGate")
	}
	td.drainGate.Unlock()
	td.admitting.Add(-1)
	deadline := time.Now().Add(5 * time.Second)
	for td.pendingChange() != "" {
		if time.Now().After(deadline) {
			t.Fatal("the idle Cell never switched")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
