package runtime

import (
	"os/exec"
	"syscall"
	"testing"
	"time"
)

// TestStepIdentityStampSurvivesASlowStamp: the identity a step process is
// journaled and torn down under must be its own birth, not the moment the
// Go side got round to stamping it. With the Go side stalled past the ±2 s
// identity window between fork and stamp, the recorded startedAt must
// still prove the live process ours (rootIdentityHolds, the same proof the
// restart reap and Cleanup's L2 apply) and the group provable
// (groupProvableOurs).
func TestStepIdentityStampSurvivesASlowStamp(t *testing.T) {
	const stall = 3 * time.Second // beyond the ±2 s window
	orig := afterHarnessStartFn
	afterHarnessStartFn = func() { time.Sleep(stall) }
	t.Cleanup(func() { afterHarnessStartFn = orig })

	var journaled string
	// the leader forks a member of its own group, then becomes the step
	cmd := exec.Command("sh", "-c", "sleep 300 & exec sleep 300")
	h, stdin, err := startHarness(cmd, SpawnParams{
		WorkItemID: "wi_stamp_delay",
		Journal:    func(_ int, startedAt string) error { journaled = startedAt; return nil },
	}, false)
	if err != nil {
		t.Fatalf("start the step process: %v", err)
	}
	stdin.Close()
	pid := h.cmd.Process.Pid
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL) })

	if journaled != h.StartedAt() {
		t.Fatalf("the journal got %q, the handle holds %q: one identity, two stamps", journaled, h.StartedAt())
	}
	target := CleanupTarget{PID: pid, Pgid: pid, StartedAt: h.StartedAt()}
	if !rootIdentityHolds(target) {
		t.Fatalf("a live step's own stamp %q fails its identity proof after a %s stall (kernel start ≠ stamp)", h.StartedAt(), stall)
	}
	if !JournaledProcessLive(pid, h.StartedAt()) {
		t.Fatalf("the journaled identity (%d, %q) does not name the live step", pid, h.StartedAt())
	}
	// negative control: a wall-clock stamp taken after the stall is what
	// the identity window rejects — the stall is really beyond it
	if late := nowStamp(); rootIdentityHolds(CleanupTarget{PID: pid, StartedAt: late}) {
		t.Fatalf("control: a stamp %s after birth (%q) still passes; the stall does not exercise the window", stall, late)
	}

	// the leader dies and is reaped; its group member lives on. The
	// post-reap group proof compares member births against the same
	// stamp: the step's own child must not read as "predating" it
	member := groupMembers(t, pid)
	if len(member) != 1 {
		t.Fatalf("want the step's one group member besides the leader, got %v", member)
	}
	_ = syscall.Kill(pid, syscall.SIGKILL)
	<-h.exited
	if !groupProvableOurs(target) {
		t.Fatalf("the reaped step's own group member %v is not provable under its stamp %q", member, h.StartedAt())
	}
	if err := h.Terminate(2 * time.Second); err != nil {
		t.Fatalf("post-reap Terminate of the step's own group: %v", err)
	}
	waitDead(t, member[0])
}

// groupMembers answers the live members of process group pgid other than
// its leader, waiting briefly for the leader's fork to land.
func groupMembers(t *testing.T, pgid int) []int {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var out []int
		pids, err := procListAll()
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range pids {
			if state, _, pgrp, _, ok := procStat(p); ok && state != "Z" && int(pgrp) == pgid && p != pgid {
				out = append(out, p)
			}
		}
		if len(out) > 0 || time.Now().After(deadline) {
			return out
		}
		time.Sleep(10 * time.Millisecond)
	}
}
