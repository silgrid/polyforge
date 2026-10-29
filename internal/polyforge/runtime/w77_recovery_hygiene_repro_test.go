package runtime

// repro — recovery/cleanup hygiene:
//
//	1. recovery must not start a new driver while the previous driver's
//	   Run is still alive (two writers on one execution.json);
//	2. a step's own teardown must reap a child that escaped the process
//	   group with setsid (not wait for the WI-level sweep);
//	3. a handle whose process was already reaped must not signal a
//	   process group it cannot prove belongs to this dispatch.
//
// These encode the OBSERVED failure of the current tree; they are the
// reproducer, not the fix.

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
)

// stubbornDriverHandle is the shape of a driver Run that does not observe
// its cancel inside the teardown bound: Terminate reports the timeout
// while Run is still alive, and Run only exits once the test releases it.
type stubbornDriverHandle struct {
	mu       sync.Mutex
	term     int
	released bool
	termCh   chan struct{}
	once     sync.Once
}

func newStubbornDriverHandle() *stubbornDriverHandle {
	return &stubbornDriverHandle{termCh: make(chan struct{})}
}

func (h *stubbornDriverHandle) PID() int          { return 0 } // the bridge shape: no child pid
func (h *stubbornDriverHandle) StartedAt() string { return "2026-10-27T00:00:00Z" }

func (h *stubbornDriverHandle) Alive() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return !h.released
}

func (h *stubbornDriverHandle) Done() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.released
}

func (h *stubbornDriverHandle) Terminate(time.Duration) error {
	h.mu.Lock()
	h.term++
	h.mu.Unlock()
	h.once.Do(func() { close(h.termCh) })
	// the reported failure: the fuse fires while Run lives on
	return fmt.Errorf("driver: did not exit within the cleanup bound")
}

func (h *stubbornDriverHandle) termCalls() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.term
}

func (h *stubbornDriverHandle) release() {
	h.mu.Lock()
	h.released = true
	h.mu.Unlock()
}

// TestRecoverNoNewDriverWhileOldRunStillAlive is the double-writer
// reproducer: after the old handle's Terminate reports that its Run is
// still alive, recoverCore must NOT start the engine. Today it does, so
// two drivers sit on the same execution.json.
func TestRecoverNoNewDriverWhileOldRunStillAlive(t *testing.T) {
	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, newFakeHub(), sp)
	wi := "wi_w77_double_writer"
	j := seedJob(r, wi)
	old := newStubbornDriverHandle()
	j.mu.Lock()
	j.agent = old
	j.agentPID = 0 // the driver bridge shape
	j.mu.Unlock()

	var mu sync.Mutex
	var violations []string
	r.cfg.AgentStarter = func(ctx context.Context, in AgentStartInput) (AgentHandle, error) {
		mu.Lock()
		if !old.Done() {
			violations = append(violations, fmt.Sprintf(
				"engine spawn %d started while the previous driver Run was still alive", in.SpawnN))
		}
		mu.Unlock()
		return &recordingHandle{}, nil
	}

	done := make(chan struct{})
	go func() {
		r.handleOB(context.Background(), recoveryMsg(j, protocol.RecoverDeath))
		close(done)
	}()

	select {
	case <-old.termCh:
	case <-time.After(5 * time.Second):
		t.Fatal("recovery never tried to terminate the old driver handle")
	}
	if n := old.termCalls(); n == 0 {
		t.Fatal("recovery never attempted the old handle's teardown")
	}
	// a relaunch that ignores the still-live Run happens right here
	time.Sleep(time.Second)
	old.release()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("recovery never returned after the old Run exited")
	}

	mu.Lock()
	defer mu.Unlock()
	if len(violations) > 0 {
		t.Fatalf("%s: two writers on one execution.json", violations[0])
	}
}

// TestStepHandleTerminateReapsSetsidChild: the step-end teardown the
// driver invokes (the step handle's Terminate, then the runtime's
// StepEnded hook) kills the process group only. A child that called
// setsid left the group and survives the step — until the WI ends today.
// It must be gone at the step's own end.
func TestStepHandleTerminateReapsSetsidChild(t *testing.T) {
	wi := runMarker("wi_w77_setsid")
	daemon := runMarker("daemon-w77")
	r, _, _ := newTestRuntime(t, newFakeHub(), &scriptedSpawner{})
	r.cfg.MachineID = daemon
	j := seedJob(r, wi)
	pidFile := filepath.Join(t.TempDir(), "escapee.pid")
	// the step process is its own group leader (startHarness's Setpgid);
	// the background child starts a NEW session (setsid) — outside the group
	script := `setsid sleep 300 & echo $! > "$ESCAPEE_PID"; exec sleep 300`
	cmd := exec.Command("sh", "-c", script)
	cmd.Env = append(os.Environ(),
		"ESCAPEE_PID="+pidFile,
		"PF_WI_ID="+wi,
		"PF_DAEMON_ID="+daemon,
	)
	h, stdin, err := startHarness(cmd, SpawnParams{WorkItemID: wi}, false)
	if err != nil {
		t.Fatalf("start the step process: %v", err)
	}
	stdin.Close()
	leader := h.cmd.Process.Pid
	escapee := 0
	t.Cleanup(func() {
		_ = syscall.Kill(-leader, syscall.SIGKILL)
		if escapee > 0 {
			_ = syscall.Kill(escapee, syscall.SIGKILL)
		}
	})

	deadline := time.Now().Add(5 * time.Second)
	for escapee == 0 && time.Now().Before(deadline) {
		raw, rerr := os.ReadFile(pidFile)
		if rerr == nil {
			if pid, perr := strconv.Atoi(strings.TrimSpace(string(raw))); perr == nil {
				escapee = pid
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if escapee <= 0 {
		t.Fatal("the step process never reported its setsid child")
	}
	// the repro's precondition: the child really is outside the step group
	if _, _, pgrp, _, ok := procStat(escapee); !ok || int(pgrp) == leader {
		t.Fatalf("the child (pid %d) did not leave the step's process group %d", escapee, leader)
	}

	if terr := h.Terminate(5 * time.Second); terr != nil {
		t.Fatalf("step handle teardown: %v", terr)
	}
	// the runtime's own step-end hook, as the driver calls it after every
	// dispatch whose process is gone
	r.stepEnded(j.wi)(context.Background())
	waitDead(t, leader)
	if procExists(escapee) {
		t.Fatalf("setsid child %d survived the step's teardown (markers %s/%s): it runs to the WI's end, off the step single-writer area", escapee, wi, daemon)
	}
}

// TestHandleTerminateDoesNotSignalAReusedPgid: the handle still holds the
// pid of a step process that was reaped; the kernel has since handed the
// number to an innocent process group (born well after the recorded
// start). Terminating the handle must not signal that group.
func TestHandleTerminateDoesNotSignalAReusedPgid(t *testing.T) {
	victim := exec.Command("sleep", "300")
	victim.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := victim.Start(); err != nil {
		t.Fatal(err)
	}
	vpid := victim.Process.Pid
	t.Cleanup(func() {
		_ = syscall.Kill(-vpid, syscall.SIGKILL)
		_ = victim.Wait()
	})

	// the reaped step process: recorded well before the victim group was
	// born, exited channel closed (reaped), stale pid still on the handle
	p := &proc{
		cmd:       &exec.Cmd{Process: &os.Process{Pid: vpid}},
		startedAt: time.Now().Add(-10 * time.Second).UTC().Format(time.RFC3339Nano),
		exited:    make(chan struct{}),
	}
	p.reaped.Store(true)
	close(p.exited)

	_ = p.Terminate(2 * time.Second)
	mustBeAlive(t, vpid)
}
