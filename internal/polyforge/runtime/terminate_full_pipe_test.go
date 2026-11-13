package runtime

import (
	"context"
	"os/exec"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/spawn"
)

// A harness that never drains its stdin parks an owner message's Write on
// the full pipe: a message of capsule.MaxOwnerMessageBytes, framed, is
// past the 64 KiB pipe buffer. The relay's ctx cannot interrupt a Write,
// so the teardown must not wait on it (PR 6d review): Terminate — the
// dispatch's teardown, racing the relay's SendMessage — closes the command
// channel at once, which fails the parked Write, and signals the group.
// Before the fix the Write held the lock closeStdin takes, and Terminate
// parked behind it until the harness died on its own.
func TestTerminateIsNotWedgedByAWriteParkedOnAFullPipe(t *testing.T) {
	cmd := exec.Command("sleep", "300") // never reads its stdin
	pr, err := startHarness(cmd, SpawnParams{WorkItemID: "wi_full_pipe", SpawnN: 1, Harness: harness.Pi})
	if err != nil {
		t.Fatalf("start the step process: %v", err)
	}
	pid := pr.cmd.Process.Pid
	t.Cleanup(func() {
		// the wedged (unfixed) shape: killing the group fails the parked
		// Write (EPIPE), which releases everything parked behind it
		_ = syscall.Kill(-pid, syscall.SIGKILL)
		select {
		case <-pr.exited:
		case <-time.After(10 * time.Second):
		}
	})

	sent := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second) // relayAckBound
		defer cancel()
		_, err := pr.SendMessage(ctx, spawn.Message{Key: "k1", Text: strings.Repeat("x", capsule.MaxOwnerMessageBytes)})
		sent <- err
	}()
	// the command is registered just before its Write; the Write then
	// fills the pipe and parks (the message never returns on its own)
	waitCond(t, 5*time.Second, "the message never reached its write", func() bool {
		pr.pend.Lock()
		defer pr.pend.Unlock()
		_, ok := pr.pending["pf-msg-1-1"]
		return ok
	})
	time.Sleep(200 * time.Millisecond)
	select {
	case err := <-sent:
		t.Fatalf("the message's write did not park on the full pipe: %v", err)
	default:
	}

	started := time.Now()
	terminated := make(chan error, 1)
	go func() { terminated <- pr.Terminate(3 * time.Second) }()
	select {
	case err := <-terminated:
		if err != nil {
			t.Fatalf("Terminate: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("Terminate wedged behind the stdin write parked on the full pipe (%s)", time.Since(started).Round(time.Millisecond))
	}
	// the parked write ended with the channel's close: the message failed
	select {
	case err := <-sent:
		if err == nil {
			t.Fatal("a message whose write the teardown cut was reported taken")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the parked write was never released by the teardown")
	}
}
