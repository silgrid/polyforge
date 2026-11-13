package runtime

// spawn_message_test.go: the resident pi session (workflow contract v2,
// PR 6b). The scripted harness (TestHelperProcess, GO_BE_RPC=1) answers
// commands the way pi 1.4.0 does, so these pin the daemon's side of the
// protocol: a later message rides a `prompt` with streamingBehavior
// "steer" and the delivery marker, its disposition follows the run state
// pi reports, a refusal is an error, the wait is bounded, and a torn-down
// process takes no more messages.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/spawn"
)

type capturedCommand struct {
	ID                string `json:"id"`
	Type              string `json:"type"`
	Message           string `json:"message"`
	StreamingBehavior string `json:"streamingBehavior"`
}

// capturedCommands decodes the stdin lines the scripted harness captured.
func capturedCommands(t *testing.T, capDir string) []capturedCommand {
	t.Helper()
	raw, _ := os.ReadFile(filepath.Join(capDir, "stdin-capture.jsonl"))
	var out []capturedCommand
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var c capturedCommand
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("captured stdin line is not a command: %v (%q)", err, line)
		}
		out = append(out, c)
	}
	return out
}

func waitCond(t *testing.T, bound time.Duration, what string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(bound); ; time.Sleep(10 * time.Millisecond) {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s (within %s)", what, bound)
		}
	}
}

// spawnRPC derives the scripted harness in rpc mode (turn script per
// GO_BE_TURN) and returns the proc plus a counter of agent_settled
// records the OnRecord tap saw.
func spawnRPC(t *testing.T, turn string) (string, *proc, *atomic.Int32) {
	t.Helper()
	capDir, sp := scriptedHarness(t)
	t.Setenv("GO_BE_RPC", "1")
	t.Setenv("GO_BE_TURN", turn)
	var settled atomic.Int32
	h, err := sp.Spawn(context.Background(), SpawnParams{
		WorkItemID: "wi_resident", CapsuleDir: capDir, SessionHandle: "ab12cd34", SpawnN: 1,
		InitialPrompt: "begin the step",
		Journal:       func(pid int, startedAt string) error { return nil },
		OnRecord: func(line string) {
			if strings.Contains(line, `"agent_settled"`) {
				settled.Add(1)
			}
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	pr := h.(*proc)
	t.Cleanup(func() {
		_ = pr.Terminate(3 * time.Second)
		<-pr.exited
	})
	return capDir, pr, &settled
}

// The handle is a Messenger and a TurnReporter (the faces the owner-message
// relay type-asserts); the initial prompt's wire shape is unchanged.
func TestPiHandleFaces(t *testing.T) {
	capDir, pr, _ := spawnRPC(t, "")
	var h AgentHandle = pr
	if _, ok := h.(spawn.Messenger); !ok {
		t.Fatal("the pi handle is not a spawn.Messenger")
	}
	if _, ok := h.(spawn.TurnReporter); !ok {
		t.Fatal("the pi handle is not a spawn.TurnReporter")
	}
	waitCond(t, 5*time.Second, "initial prompt never captured", func() bool { return len(capturedCommands(t, capDir)) >= 1 })
	init := capturedCommands(t, capDir)[0]
	if init.ID != "pf-init" || init.Type != "prompt" || init.StreamingBehavior != "" || init.Message != "begin the step" {
		t.Fatalf("initial prompt wire shape changed: %+v", init)
	}
}

// Idle process: the message starts a new run; the process stays resident
// and takes the next message too.
func TestPiSendMessageIdleStartsTurn(t *testing.T) {
	capDir, pr, settled := spawnRPC(t, "settle")
	// the initial prompt's run settled: idle
	waitCond(t, 5*time.Second, "the initial run never settled", func() bool { return settled.Load() >= 1 && !pr.TurnRunning() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, err := pr.SendMessage(ctx, spawn.Message{Key: "2026-11-10T12:00:00.000Z", Text: "owner: carry on\n"})
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if d.Disposition != spawn.DispositionStarted || d.CommandID != "pf-msg-1-1" {
		t.Fatalf("delivery %+v, want started on pf-msg-1-1", d)
	}
	cmds := capturedCommands(t, capDir)
	if len(cmds) != 2 {
		t.Fatalf("captured %d commands, want 2 (init, message): %+v", len(cmds), cmds)
	}
	msg := cmds[1]
	want := "owner: carry on\n\n" + spawn.MessageMarker("2026-11-10T12:00:00.000Z")
	if msg.ID != "pf-msg-1-1" || msg.Type != "prompt" || msg.StreamingBehavior != "steer" || msg.Message != want {
		t.Fatalf("message wire shape %+v, want prompt/steer with the marker %q", msg, want)
	}
	// resident: the process is alive after the turn and answers again
	waitCond(t, 5*time.Second, "the message's run never settled", func() bool { return settled.Load() >= 2 && !pr.TurnRunning() })
	if !pr.Alive() {
		t.Fatal("the process is gone after a turn: not resident")
	}
	d2, err := pr.SendMessage(ctx, spawn.Message{Key: "2026-11-10T12:01:00.000Z", Text: "owner: and this"})
	if err != nil || d2.CommandID != "pf-msg-1-2" || d2.Disposition != spawn.DispositionStarted {
		t.Fatalf("second message: %+v %v", d2, err)
	}
}

// Mid-run: the message is queued (pi's steer path), never refused, and
// the run state stays running.
func TestPiSendMessageMidRunQueued(t *testing.T) {
	_, pr, _ := spawnRPC(t, "open")
	waitCond(t, 5*time.Second, "the initial run never opened", func() bool { return pr.TurnRunning() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, err := pr.SendMessage(ctx, spawn.Message{Key: "k1", Text: "owner: stop and read this"})
	if err != nil {
		t.Fatalf("SendMessage mid-run: %v", err)
	}
	if d.Disposition != spawn.DispositionQueued {
		t.Fatalf("mid-run disposition %q, want queued", d.Disposition)
	}
	if !pr.TurnRunning() {
		t.Fatal("a queued message ended the run state")
	}
}

// A refused command (pi's mid-run refusal of a bare prompt, or any
// success=false response) is an error carrying the harness's text: the
// message did not reach the model.
func TestPiSendMessageRefusedIsError(t *testing.T) {
	t.Setenv("GO_BE_REFUSE", "1")
	_, pr, _ := spawnRPC(t, "")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := pr.SendMessage(ctx, spawn.Message{Key: "k1", Text: "hello"})
	if err == nil || !strings.Contains(err.Error(), "Agent is already processing") {
		t.Fatalf("refusal not surfaced: %v", err)
	}
	pr.pend.Lock()
	_, pending := pr.pending["pf-msg-1-1"]
	pr.pend.Unlock()
	if pending {
		t.Fatal("a refused command stayed pending")
	}
}

// No response within ctx: the wait ends with ctx's error and the waiter
// is dropped (no response ever arrives for it).
func TestPiSendMessageUnansweredIsBoundedByCtx(t *testing.T) {
	capDir, sp := scriptedHarness(t) // GO_BE_RPC unset: the harness never answers
	h, err := sp.Spawn(context.Background(), SpawnParams{
		WorkItemID: "wi_silent", CapsuleDir: capDir, SessionHandle: "ab12cd34", SpawnN: 1,
		InitialPrompt: "begin", Journal: func(int, string) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	pr := h.(*proc)
	t.Cleanup(func() { _ = pr.Terminate(3 * time.Second); <-pr.exited })
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = pr.SendMessage(ctx, spawn.Message{Key: "k1", Text: "anyone there"})
	if err == nil || !strings.Contains(err.Error(), "unanswered") {
		t.Fatalf("unanswered message not reported: %v", err)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatalf("the wait ignored ctx: %s", time.Since(started))
	}
	pr.pend.Lock()
	_, pending := pr.pending["pf-msg-1-1"]
	pr.pend.Unlock()
	if pending {
		t.Fatal("an unanswered command stayed pending after ctx ended")
	}
}

// Teardown closes the command channel first (lifecycle (iv)): the harness
// reads EOF, and no later message can be written.
func TestPiTerminateClosesCommandChannel(t *testing.T) {
	_, pr, _ := spawnRPC(t, "settle")
	if err := pr.Terminate(3 * time.Second); err != nil {
		t.Fatalf("Terminate: %v", err)
	}
	<-pr.exited
	pr.mu.Lock()
	open := pr.stdin != nil
	pr.mu.Unlock()
	if open {
		t.Fatal("Terminate left the command channel open")
	}
	_, err := pr.SendMessage(context.Background(), spawn.Message{Key: "k1", Text: "too late"})
	if err == nil {
		t.Fatal("a message into a torn-down process was accepted")
	}
}

// The command channel's close alone is pi's orderly shutdown request:
// the scripted harness (like pi) exits 0 on stdin EOF.
func TestPiCloseStdinIsOrderlyShutdown(t *testing.T) {
	_, pr, _ := spawnRPC(t, "settle")
	pr.closeStdin()
	select {
	case <-pr.exited:
	case <-time.After(5 * time.Second):
		t.Fatal("the harness did not exit on stdin EOF")
	}
	if info := pr.ExitInfo(); info.Code != 0 || info.Signal != "" {
		t.Fatalf("exit after stdin close: %+v, want code 0 unsignaled", info)
	}
	if err := pr.writeCommand(piCommand{ID: "x", Type: "prompt", Message: "late"}); err == nil {
		t.Fatal("a command after the close was accepted")
	}
}

// The run state follows the stream: agent_start sets it, agent_settled
// clears it; a process that exits mid-run is reported by Done/ExitInfo
// as today (the C2 death the driver classifies), not by the run state.
func TestPiTurnRunningFollowsRunBoundary(t *testing.T) {
	_, pr, settled := spawnRPC(t, "settle")
	// the scripted harness emits agent_start at derivation, then the
	// settle sequence for the initial prompt
	waitCond(t, 5*time.Second, "agent_settled never cleared the run state", func() bool { return settled.Load() >= 1 && !pr.TurnRunning() })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := pr.SendMessage(ctx, spawn.Message{Text: "again"}); err != nil {
		t.Fatal(err)
	}
	waitCond(t, 5*time.Second, "the second run never settled", func() bool { return settled.Load() >= 2 && !pr.TurnRunning() })
}

// MessageText embeds the marker on its own trailing line; without a key
// the text is delivered verbatim.
func TestMessageTextMarker(t *testing.T) {
	if got := spawn.MessageText(spawn.Message{Key: "2026-11-10T00:00:00Z", Text: "hello\n\n"}); got != "hello\n\n[pf-msg:2026-11-10T00:00:00Z]" {
		t.Fatalf("MessageText = %q", got)
	}
	if got := spawn.MessageText(spawn.Message{Text: "bare"}); got != "bare" {
		t.Fatalf("MessageText without key = %q", got)
	}
}
