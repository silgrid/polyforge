package runtime

// claude_interactive_test.go: the interactive step session against the
// REAL Claude Code (workflow contract v2 §5.2, PR 6a). The spawner's
// own argv/env, the real binary, the harness root's credentials, a
// throwaway capsule. OPT-IN like the other real-harness probes (it burns
// model tokens): POLYFORGE_TEST_REAL_CLAUDE=1 and
// POLYFORGE_TEST_CLAUDE_HARNESS_ROOT naming the harness root.
//
// What it pins (measured on Claude Code 2.1.322, 2026-11-10 — the first
// task of PR 6a):
//   - the dispatch's prompt is the first stream-json user message and the
//     process stays alive after its turn's `result` (stdin open);
//   - a user message written MID-TURN (while the step's Bash call runs)
//     is delivered: Claude queues it and injects it into the running turn
//     right after the tool call returns — the reply arrives; the harness
//     acknowledges the message's uuid (command_lifecycle queued →
//     started) and SendMessage answers DispositionQueued at once; it is
//     neither dropped nor held back to a later turn;
//   - a message written while idle starts its own turn with its own
//     `result` (DispositionStarted);
//   - Terminate (stdin closed, then the group signalled) leaves nothing
//     of the process.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/spawn"
)

func TestClaudeInteractiveRealProbe(t *testing.T) {
	if os.Getenv("POLYFORGE_TEST_REAL_CLAUDE") != "1" {
		t.Skip("this probe launches a REAL claude process with the harness root's credentials; set POLYFORGE_TEST_REAL_CLAUDE=1 to run it")
	}
	if testing.Short() {
		t.Skip("short mode: skipping the real-claude probe")
	}
	bin := os.Getenv("POLYFORGE_TEST_CLAUDE_BIN")
	if bin == "" {
		bin = "claude"
	}
	root := os.Getenv("POLYFORGE_TEST_CLAUDE_HARNESS_ROOT")
	if root == "" {
		t.Skip("set POLYFORGE_TEST_CLAUDE_HARNESS_ROOT to the harness root whose claude-config/settings.json the probe runs with")
	}
	cfg := &Config{ClaudeBin: bin, HarnessRoot: root, MachineID: "probe"}
	if _, err := os.Stat(filepath.Join(cfg.ClaudeConfigDir(), "settings.json")); err != nil {
		t.Skipf("no claude-config settings.json under %s: %v", cfg.ClaudeConfigDir(), err)
	}
	capDir := t.TempDir()
	session := newProbeSession(t)
	projDir := filepath.Join(cfg.ClaudeConfigDir(), "projects", harness.ClaudeProjectDirName(capDir))
	t.Cleanup(func() {
		if !t.Failed() {
			os.RemoveAll(projDir)
		}
	})

	// every stdout record, stamped: assistant texts and results in order
	type rec struct {
		at   time.Time
		kind string // "result", "text", "tool_use"
		text string
	}
	var mu sync.Mutex
	var seen []rec
	note := func(line string) {
		var r struct {
			Type    string `json:"type"`
			IsError bool   `json:"is_error"`
			Result  string `json:"result"`
			Message struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
					Name string `json:"name"`
				} `json:"content"`
			} `json:"message"`
			ParentToolUseID *string `json:"parent_tool_use_id"`
		}
		if json.Unmarshal([]byte(line), &r) != nil {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		switch r.Type {
		case "result":
			seen = append(seen, rec{time.Now(), "result", strings.TrimSpace(r.Result)})
		case "assistant":
			if r.ParentToolUseID != nil && *r.ParentToolUseID != "" {
				return
			}
			for _, b := range r.Message.Content {
				switch b.Type {
				case "text":
					seen = append(seen, rec{time.Now(), "text", strings.TrimSpace(b.Text)})
				case "tool_use":
					seen = append(seen, rec{time.Now(), "tool_use", b.Name})
				}
			}
		}
	}
	count := func(kind, contains string) int {
		mu.Lock()
		defer mu.Unlock()
		n := 0
		for _, r := range seen {
			if r.kind == kind && strings.Contains(r.text, contains) {
				n++
			}
		}
		return n
	}
	firstAt := func(kind, contains string) (time.Time, bool) {
		mu.Lock()
		defer mu.Unlock()
		for _, r := range seen {
			if r.kind == kind && strings.Contains(r.text, contains) {
				return r.at, true
			}
		}
		return time.Time{}, false
	}
	wait := func(within time.Duration, cond func() bool, what string) {
		t.Helper()
		deadline := time.Now().Add(within)
		for !cond() {
			if time.Now().After(deadline) {
				mu.Lock()
				defer mu.Unlock()
				t.Fatalf("%s; records seen: %+v", what, seen)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}

	// claude's own scratch (claude-<uid>/) goes with the test: the step env
	// inherits TMPDIR
	t.Setenv("TMPDIR", t.TempDir())
	p := claudeProbeParams(capDir, session)
	p.SpawnN = 1
	p.Harness = harness.Claude
	p.InitialPrompt = "Use the Bash tool to run exactly this command: sleep 25; echo T1DONE . " +
		"After it returns, reply with exactly one line: TURN1-END"
	p.OnRecord = note
	h, err := DefaultSpawner(cfg).Spawn(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Terminate(5 * time.Second) })
	m := h.(spawn.Messenger)
	tr := h.(spawn.TurnReporter)
	// one bounded wait per message (the first turn's Bash call alone runs
	// 25s between the two sends)
	send := func(msg spawn.Message) (spawn.Delivery, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		return m.SendMessage(ctx, msg)
	}

	// mid-turn: the step's Bash call is running
	wait(2*time.Minute, func() bool { return count("tool_use", "Bash") > 0 }, "the step never started its Bash call")
	time.Sleep(2 * time.Second)
	if !tr.TurnRunning() {
		t.Error("TurnRunning is false while the step's Bash call runs")
	}
	const midKey = "2026-11-10T09:00:01.000000000Z"
	midD, err := send(spawn.Message{Key: midKey, Text: "Reply with exactly one line: MARKER-ALPHA-RECEIVED"})
	if err != nil {
		t.Fatal(err)
	}
	if midD.Disposition != spawn.DispositionQueued {
		t.Errorf("the mid-turn message's disposition is %q, want queued (absorbed into the running turn)", midD.Disposition)
	}
	wait(3*time.Minute, func() bool { return count("result", "") >= 1 }, "turn 1 never ended")
	// delivered live: acknowledged, and answered (inside the running turn
	// on 2.1.322; a later Claude Code may answer it in a turn of its own —
	// either is delivery, a missing reply is not)
	wait(time.Minute, func() bool { return count("text", "MARKER-ALPHA-RECEIVED") > 0 }, "the mid-turn message was never answered: not delivered")
	if alphaAt, ok := firstAt("text", "MARKER-ALPHA-RECEIVED"); ok {
		if resAt, _ := firstAt("result", ""); alphaAt.Before(resAt) {
			t.Logf("finding: the mid-turn message was absorbed into the running turn (answered %s before the turn's result)", resAt.Sub(alphaAt).Round(time.Millisecond))
		} else {
			t.Logf("finding: the mid-turn message was answered in a turn of its own, %s after the first result", alphaAt.Sub(resAt).Round(time.Millisecond))
		}
	}
	time.Sleep(2 * time.Second)
	if h.Done() || !h.Alive() {
		t.Fatal("the process exited after its turn: stdin was not kept open")
	}
	if tr.TurnRunning() {
		t.Error("TurnRunning is true after the turn's result")
	}

	// idle: the next message is its own turn
	results := count("result", "")
	const idleKey = "2026-11-10T09:00:02.000000000Z"
	idleD, err := send(spawn.Message{Key: idleKey, Text: "Reply with exactly one line: MARKER-BETA-RECEIVED"})
	if err != nil {
		t.Fatal(err)
	}
	if idleD.Disposition != spawn.DispositionStarted {
		t.Errorf("the idle message's disposition is %q, want started", idleD.Disposition)
	}
	wait(2*time.Minute, func() bool { return count("result", "") > results && count("text", "MARKER-BETA-RECEIVED") > 0 },
		"the idle message got no turn of its own")
	if h.Done() {
		t.Fatal("the process exited after the second turn")
	}

	// the daemon's teardown
	if err := h.Terminate(10 * time.Second); err != nil {
		t.Fatal(err)
	}
	wait(15*time.Second, h.Done, "the process is still there after Terminate")
	if h.Alive() {
		t.Fatal("the process is alive after Terminate")
	}
	// the native session record holds every message by the daemon's id
	// and by its text: a message that started a turn is a `user` record
	// with our uuid; one absorbed mid-turn is a `queue-operation` record
	// (reason absorbed_mid_turn) whose commandUuid is our id, its text
	// then inside the next request body — so a delivery proof that reads
	// the transcript must accept either shape (or key on the text marker)
	native := harness.LocateClaudeSession(session, cfg.ClaudeConfigDir(), capDir)
	if native == "" {
		t.Fatalf("no session record for %s under %s", session, projDir)
	}
	b, err := os.ReadFile(native)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{claudeInitMessageID(1), midD.CommandID, idleD.CommandID} {
		if !strings.Contains(string(b), `"uuid":"`+id+`"`) && !strings.Contains(string(b), `"commandUuid":"`+id+`"`) {
			t.Errorf("the session record carries neither a user record nor a queue-operation with the id %q: the command id is not in the transcript", id)
		}
	}
	for _, marker := range []string{spawn.MessageMarker(midKey), spawn.MessageMarker(idleKey)} {
		if !strings.Contains(string(b), marker) {
			t.Errorf("the session record carries no text with the delivery marker %q", marker)
		}
	}
}
