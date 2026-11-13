package test

// v2 PR 6b: pi rpc steps stay resident across turns (design §5.2). The
// step process does not exit at a turn boundary; a later message reaches
// the SAME process over the rpc command channel (prompt, steered while a
// run is active) and is written to its transcript with the delivery
// marker; the daemon tears the still-alive process down when the step's
// result envelope is accepted (lifecycle (iv)); a process killed mid-turn
// is the C2 death the driver classifies today (ii). The first scenario
// drives the production spawner against the fakepi binary directly (no
// daemon: today's lanes never send a later message — the owner-message
// relay is PR 6d); the other two run the daemon.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// spawnResidentFakepi derives one fakepi step process through the
// production pi spawner: the capsule layout fakepi reads (its spawn
// journal entry, written by the Journal hook the runtime calls before the
// initial prompt), the plan file for wi, and an OnRecord tap counting
// agent_settled records.
func spawnResidentFakepi(t *testing.T, wi string, plan map[string]any) (capDir string, h spawn.Handle, settled *atomic.Int32) {
	t.Helper()
	bins := v1Bins(t)
	plans := t.TempDir()
	t.Setenv("FAKEPI_PLANS", plans)
	blob, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(plans, wi+".json"), blob, 0o644); err != nil {
		t.Fatal(err)
	}
	capDir = t.TempDir()
	for _, d := range []string{"runtime-state", "logs", "session"} {
		if err := os.MkdirAll(filepath.Join(capDir, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	transcript := filepath.Join(capDir, "session", "session-6b.jsonl")
	var count atomic.Int32
	sp := runtime.DefaultSpawner(&runtime.Config{PiBin: filepath.Join(bins, "fakepi")})
	h, err = sp.Spawn(context.Background(), spawn.Params{
		WorkItemID: wi, CapsuleDir: capDir, SessionHandle: "6b6b6b6b", SpawnN: 1,
		TranscriptPath: transcript, InitialPrompt: "begin the step",
		OnRecord: func(line string) {
			if strings.Contains(line, `"agent_settled"`) {
				count.Add(1)
			}
		},
		Journal: func(pid int, startedAt string) error {
			entry := map[string]any{"pid": pid, "step_id": "fix", "work_item": wi,
				"session_handle": "6b6b6b6b", "spawn_n": 1, "transcript": transcript, "started_at": startedAt}
			raw, _ := json.Marshal(entry)
			f, err := os.OpenFile(filepath.Join(capDir, "runtime-state", "spawn-journal.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
			if err != nil {
				return err
			}
			defer f.Close()
			_, err = f.Write(append(raw, '\n'))
			return err
		},
	})
	if err != nil {
		t.Fatalf("spawn fakepi: %v", err)
	}
	t.Cleanup(func() {
		_ = h.Terminate(5 * time.Second)
		killOrphans(wi)
	})
	return capDir, h, &count
}

// journalHas reports whether the fakepi journal holds a record of kind
// whose fields match want (every listed field, string-compared).
func journalHas(t *testing.T, capDir, kind string, want map[string]string) bool {
	t.Helper()
	for _, r := range fakeJournal(t, capDir) {
		if r["type"] != kind {
			continue
		}
		ok := true
		for k, v := range want {
			if fmt.Sprint(r[k]) != v {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

// transcriptUserLines answers the fakepi transcript's user lines' texts.
func transcriptUserLines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(string(raw), "\n") {
		var rec struct {
			T    string `json:"t"`
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil && rec.T == "user" {
			out = append(out, rec.Text)
		}
	}
	return out
}

// A later message into the resident session: idle, it starts a turn and
// is on the transcript with its marker; mid-run (the park shape keeps a
// turn open), it is queued — acknowledged, journaled, and NOT on the
// transcript, which is why the marker, not the acknowledgement, proves
// delivery. The process is alive after every turn until Terminate, and a
// kill -9 mid-run ends it as a signaled exit.
func TestV26bPiResidentSessionTakesLaterMessages(t *testing.T) {
	t.Run("idle", func(t *testing.T) {
		const wi = "wi_6b_idle"
		capDir, h, settled := spawnResidentFakepi(t, wi, map[string]any{"actions": []any{}})
		turns := h.(spawn.TurnReporter)
		msgr := h.(spawn.Messenger)
		transcript := filepath.Join(capDir, "session", "session-6b.jsonl")
		waitUntil(t, 15*time.Second, "the initial prompt's turn never settled", func() bool {
			return journalHas(t, capDir, "prompt", map[string]string{"n_in_session": "1"}) && settled.Load() >= 2 && !turns.TurnRunning()
		})
		if !h.Alive() {
			t.Fatal("the step process exited at the turn boundary: not resident")
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		const key = "2026-11-10T12:00:00.000Z"
		d, err := msgr.SendMessage(ctx, spawn.Message{Key: key, Text: "owner: carry on with option b"})
		if err != nil {
			t.Fatalf("SendMessage into the idle session: %v", err)
		}
		if d.Disposition != spawn.DispositionStarted {
			t.Fatalf("idle disposition %q, want started", d.Disposition)
		}
		waitUntil(t, 15*time.Second, "the message never reached the process", func() bool {
			return journalHas(t, capDir, "prompt", map[string]string{"n_in_session": "2"})
		})
		users := transcriptUserLines(t, transcript)
		if len(users) != 2 || !strings.Contains(users[1], spawn.MessageMarker(key)) || !strings.HasPrefix(users[1], "owner: carry on with option b") {
			t.Fatalf("transcript user lines %q: the delivered message with its marker is not the second", users)
		}
		waitUntil(t, 15*time.Second, "the message's turn never settled", func() bool { return settled.Load() >= 3 && !turns.TurnRunning() })
		if !h.Alive() {
			t.Fatal("the step process exited after the message's turn: not resident")
		}
		// lifecycle (iv): the owner of the dispatch tears it down
		if err := h.Terminate(5 * time.Second); err != nil {
			t.Fatalf("Terminate: %v", err)
		}
		waitUntil(t, 10*time.Second, "the step process survived Terminate", func() bool { return h.Done() && len(orphanPIDs(wi)) == 0 })
	})
	t.Run("mid-run", func(t *testing.T) {
		const wi = "wi_6b_park"
		capDir, h, _ := spawnResidentFakepi(t, wi, map[string]any{"actions": []map[string]any{
			{"do": "envelope", "id": "fix", "park": true},
		}})
		turns := h.(spawn.TurnReporter)
		msgr := h.(spawn.Messenger)
		transcript := filepath.Join(capDir, "session", "session-6b.jsonl")
		waitUntil(t, 15*time.Second, "the parked turn never opened", func() bool {
			return journalHas(t, capDir, "park", nil) && turns.TurnRunning()
		})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		const key = "2026-11-10T12:05:00.000Z"
		d, err := msgr.SendMessage(ctx, spawn.Message{Key: key, Text: "owner: stop, read this first"})
		if err != nil {
			t.Fatalf("SendMessage mid-run: %v", err)
		}
		if d.Disposition != spawn.DispositionQueued {
			t.Fatalf("mid-run disposition %q, want queued", d.Disposition)
		}
		waitUntil(t, 15*time.Second, "the queued message was not journaled", func() bool {
			return journalHas(t, capDir, "message_queued", map[string]string{"command": "prompt"})
		})
		for _, u := range transcriptUserLines(t, transcript) {
			if strings.Contains(u, spawn.MessageMarker(key)) {
				t.Fatalf("a queued (undelivered) message carries its marker on the transcript: %q", u)
			}
		}
		if !turns.TurnRunning() || !h.Alive() {
			t.Fatal("a queued message ended the run or the process")
		}
		// (ii) killed mid-run: a signaled exit, nothing else
		if err := syscall.Kill(h.PID(), syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		waitUntil(t, 10*time.Second, "the killed process was not reaped", h.Done)
		info := h.(spawn.ExitReporter).ExitInfo()
		if info.Signal != "killed" || info.Code != -1 {
			t.Fatalf("exit after kill -9: %+v, want signal killed, code -1", info)
		}
	})
}

// Lifecycle (iv) on the daemon: the fix step's process is gone once its
// accepted envelope moved the workflow on (the review spawn is journaled
// only after fix's delivery, which tears fix's process down), and no step
// process of the work item outlives the wrap.
func TestV26bPiStepProcessTornDownAtResult(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "6b: process torn down at the envelope",
		[]map[string]any{
			{"do": "envelope", "id": "fix", "outcome": "completed"},
			{"do": "envelope", "id": "review", "outcome": "completed"},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	spawns := waitSpawnCount(t, d, wi, 2, 90*time.Second)
	pid1, _ := spawns[0]["pid"].(float64)
	if pid1 <= 0 {
		t.Fatalf("fix's spawn carries no pid: %+v", spawns[0])
	}
	waitUntil(t, 10*time.Second, "fix's process outlived its accepted envelope", func() bool {
		return syscall.Kill(int(pid1), 0) == syscall.ESRCH
	})
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	waitUntil(t, 10*time.Second, "a step process outlived the wrap", func() bool { return len(orphanPIDs(wi)) == 0 })
}

// Lifecycle (ii) on the daemon: a step process killed with SIGKILL while
// its turn is OPEN (the park shape: agent_start, turn_start, message_start,
// then nothing) is today's C2 death — exit_other, a same-model retry in
// place under the next spawn ordinal — and the workflow completes.
func TestV26bPiKill9MidTurnIsExitOther(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "6b: killed mid-turn",
		[]map[string]any{
			{"do": "envelope", "id": "fix", "park": true},
			{"do": "envelope", "id": "fix", "outcome": "completed"},
			{"do": "envelope", "id": "review", "outcome": "completed"},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	waitSpawnCount(t, d, wi, 1, 30*time.Second)
	waitSpawnTranscript(t, d, wi, 1, 30*time.Second)
	waitUntil(t, 30*time.Second, "the turn never opened", func() bool {
		return journalHas(t, d.capsuleDir(wi), "park", nil)
	})
	pid := latestSpawnPID(t, d, wi)
	if err := syscall.Kill(pid, syscall.SIGKILL); err != nil {
		t.Fatalf("kill -9 %d: %v", pid, err)
	}
	rec := waitForEvent(t, d, wi, "step_failed", 60*time.Second)
	var body struct {
		Cause       string `json:"cause"`
		Disposition string `json:"disposition"`
		Count       int    `json:"failure_count"`
	}
	eventBody(t, rec, &body)
	if body.Cause != "exit_other" || body.Disposition != "retry_same_model" || body.Count != 1 {
		t.Fatalf("step_failed = %+v, want exit_other/retry_same_model/1 for the process killed mid-turn", body)
	}
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	spawns := waitSpawnCount(t, d, wi, 3, 60*time.Second)
	if spawns[1]["resume"] != true {
		t.Fatalf("the mid-turn kill's retry was not a resume: %+v", spawns[1])
	}
}
