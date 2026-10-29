package runtime

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestTerminateKillsLiveProcessDespiteSkewedStartStamp: startedAt is
// stamped after cmd.Start, so a stall between fork and stamp leaves it
// seconds away from the kernel's start time. While the process is alive
// and unreaped its pid cannot have been reused, so Terminate must signal
// the group — never answer "pid reuse; not signaled" for a live step.
func TestTerminateKillsLiveProcessDespiteSkewedStartStamp(t *testing.T) {
	cmd := exec.Command("sleep", "300")
	h, stdin, err := startHarness(cmd, SpawnParams{WorkItemID: "wi_stamp_skew"}, false)
	if err != nil {
		t.Fatalf("start the step process: %v", err)
	}
	stdin.Close()
	pid := h.cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })
	mustBeAlive(t, pid)

	// the stalled stamp: far from the real start (any skew beyond the
	// ±2 s identity window reproduces the refusal)
	h.startedAt = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)

	if terr := h.Terminate(2 * time.Second); terr != nil {
		t.Fatalf("Terminate refused a live, unreaped step: %v", terr)
	}
	waitDead(t, pid)
	select {
	case <-h.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the exit watcher never observed the terminated step")
	}
}
