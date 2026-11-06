package test

// v2_6a_claude_interactive_e2e_test.go: a claude step runs as ONE
// interactive process per dispatch (workflow contract v2 §5.2, PR 6a):
// the prompt is the first stream-json user message on a stdin that stays
// open, the process never exits on its own after its turn, and the
// daemon ends it — after the accepted hand-off, after the third dispatch
// in a row whose turn ended without one (workflow contract 4: a refused
// `pf step handoff` records nothing, so the turn ends without a hand-off),
// after a rule-1b retry (TestW159ClaudeD7) — while a kill -9 mid-turn is
// still today's exit_other death. Against
// the REAL `pf daemon` and the fakepi claude persona (test/fakepi/
// claude.go), whose journal names why each process exited (stdin EOF or
// SIGTERM: both the daemon's teardown).

import (
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// w159Exits answers the claude persona's `exit` journal lines of a capsule.
func w159Exits(t *testing.T, capDir string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, e := range w159Journal(t, capDir, "fakeclaude.jsonl") {
		if e["type"] == "exit" {
			out = append(out, e)
		}
	}
	return out
}

// w159WaitSpawns polls the claude persona's journal until n spawn lines
// are there and answers them.
func w159WaitSpawns(t *testing.T, capDir string, n int, bound time.Duration) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(bound)
	for {
		if cl := w159Spawns(t, capDir, "fakeclaude.jsonl"); len(cl) >= n {
			return cl
		}
		if time.Now().After(deadline) {
			t.Fatalf("the claude persona never reached %d spawns", n)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// The accepted hand-off: the prompt arrived as the dispatch's stream-json
// user message, the process idled after its turn, and the daemon ended it
// (an exit line of the daemon's making: stdin EOF or SIGTERM); nothing of
// the work item is alive afterwards.
func TestV26aClaudeInteractiveDispatchEndsByTheDaemon(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, w159Yaml)
	d, _, _ := w159HarnessRoot(t, inst, ws, "")
	wi := w113NewWI(t, inst, "e2e", "v2 6a interactive dispatch", envelopeMicro())
	capDir := w079Run(t, d, inst, ws, wi)

	cl := w159Spawns(t, capDir, "fakeclaude.jsonl")
	if len(cl) != 1 || cl[0]["step"] != "fix" {
		t.Fatalf("claude spawns %v, want exactly the fix step", cl)
	}
	if cl[0]["stdin_stream_json"] != true || cl[0]["init_uuid"] != "pf-init-1" {
		t.Fatalf("the prompt did not arrive as the stream-json user message pf-init-1: %v", cl[0])
	}
	if !strings.Contains(w159Argv(cl[0]), "--input-format stream-json") {
		t.Fatalf("claude argv lacks --input-format stream-json: %s", w159Argv(cl[0]))
	}
	exits := w159Exits(t, capDir)
	if len(exits) != 1 || exits[0]["pid"] != cl[0]["pid"] {
		t.Fatalf("exit lines %v, want exactly one for the fix process %v", exits, cl[0]["pid"])
	}
	if r := exits[0]["reason"]; r != "stdin_eof" && r != "sigterm" {
		t.Fatalf("the fix process exited for %q, want the daemon's teardown (stdin_eof or sigterm)", r)
	}
	if pids := orphanPIDs(wi); len(pids) != 0 {
		t.Fatalf("processes still alive after the run: %v", pids)
	}
}

// kill -9 mid-turn is today's death: the dispatch ends exit_other (never
// ended_without_result — no ok turn, no clean exit), the step is retried
// in place on the same model with its session resumed, and the work
// item completes.
func TestV26aClaudeKill9MidTurnStaysExitOther(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, w159Yaml)
	d, _, _ := w159HarnessRoot(t, inst, ws, "")
	wi := w113NewWI(t, inst, "e2e", "v2 6a kill -9 mid-turn", []map[string]any{
		// 4s inside the turn before the envelope: the kill window
		{"do": "envelope", "id": "fix", "outcome": "completed", "ms": 4000},
		{"do": "envelope", "id": "fix", "outcome": "completed"},
		{"do": "envelope", "id": "review", "outcome": "completed"},
	})
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	first := w159WaitSpawns(t, capDir, 1, 60*time.Second)[0]
	pid, _ := first["pid"].(float64)
	if pid <= 0 {
		t.Fatalf("no pid in the spawn line %v", first)
	}
	if err := syscall.Kill(int(pid), syscall.SIGKILL); err != nil {
		t.Fatalf("kill -9 %d: %v", int(pid), err)
	}
	w113WaitCompleted(t, capDir, 240*time.Second)

	cl := w159Spawns(t, capDir, "fakeclaude.jsonl")
	if len(cl) != 2 || cl[1]["resume"] != true || cl[1]["model"] != cl[0]["model"] {
		t.Fatalf("claude spawns %v, want the killed dispatch and one resumed retry on the same model", cl)
	}
	var causes []string
	for _, a := range readExecution(t, capDir).Steps["fix"].Attempts {
		if c, _ := a["failure_cause"].(string); c != "" {
			causes = append(causes, c)
		}
	}
	if strings.Join(causes, ",") != "exit_other" {
		t.Fatalf("fix attempt causes %v, want exactly exit_other for the kill", causes)
	}
	// the killed process left no exit line (SIGKILL), the retry's was the daemon's
	exits := w159Exits(t, capDir)
	if len(exits) != 1 || exits[0]["pid"] != cl[1]["pid"] {
		t.Fatalf("exit lines %v, want exactly the retry's (%v)", exits, cl[1]["pid"])
	}
	if pids := orphanPIDs(wi); len(pids) != 0 {
		t.Fatalf("processes still alive after the run: %v", pids)
	}
}

// Three refused hand-offs (workflow contract 4: a malformed `pf step
// handoff` is refused at the call and records nothing): each dispatch's
// turn ends without a hand-off, the daemon tears its process down before
// resuming the session with the reminder, the third in a row blocks the
// work item, and no claude process outlives the block.
func TestV26aClaudeRefusedEnvelopesTearEveryProcessDown(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, w159Yaml)
	d, _, _ := w159HarnessRoot(t, inst, ws, "")
	// the v1 question as the result: `--status needs_input` is none of
	// the hand-off's statuses
	malformed := map[string]any{"do": "envelope", "id": "fix", "outcome": "needs_input", "question_id": "",
		"question": "the malformed ask (must be refused)"}
	wi := w113NewWI(t, inst, "e2e", "v2 6a refused hand-offs", []map[string]any{malformed, malformed, malformed,
		{"do": "envelope", "id": "fix", "outcome": "completed"}})
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	waitExecution(t, capDir, 240*time.Second, "the work item never blocked on three dispatches ended without a hand-off",
		func(x *executionView) bool {
			return x.WorkflowStatus == "blocked" && strings.Contains(x.BlockedReason, "step fix ended without `pf step handoff` 3 times in a row")
		})
	t.Cleanup(func() { killOrphans(wi) })

	cl := w159Spawns(t, capDir, "fakeclaude.jsonl")
	if len(cl) != 3 {
		t.Fatalf("claude spawns %d (%v), want the three refused dispatches and no fourth", len(cl), cl)
	}
	// each dispatch's one hand-off call was refused at the call; none was
	// accepted
	lines := ledgerLines(t, capDir)
	var refused []int64
	for _, l := range lines {
		if l.Kind == stepcall.KindResult && l.Verb == stepcall.VerbHandoff && l.Step == "fix" {
			if l.Outcome != stepcall.OutcomeRefused || !strings.Contains(l.Error, `"needs_input" is none of`) {
				t.Fatalf("a hand-off result other than the refusal: %+v", l)
			}
			refused = append(refused, l.Spawn)
		}
	}
	if len(refused) != 3 || refused[0] != 1 || refused[1] != 2 || refused[2] != 3 {
		t.Fatalf("refused hand-offs by spawn %v, want one per dispatch (1, 2, 3)", refused)
	}
	for i := 1; i < len(cl); i++ {
		if cl[i]["resume"] != true || cl[i]["session"] != cl[0]["session"] {
			t.Fatalf("dispatch %d is not the same session resumed: %v (first %v)", i+1, cl[i], cl[0])
		}
	}
	deadline := time.Now().Add(15 * time.Second)
	for len(w159Exits(t, capDir)) < 3 && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	exits := w159Exits(t, capDir)
	if len(exits) != 3 {
		t.Fatalf("exit lines %v, want one per refused dispatch", exits)
	}
	for i, e := range exits {
		if e["pid"] != cl[i]["pid"] {
			t.Fatalf("exit line %d is for pid %v, want dispatch %d's %v", i, e["pid"], i+1, cl[i]["pid"])
		}
		if r := e["reason"]; r != "stdin_eof" && r != "sigterm" {
			t.Fatalf("dispatch %d exited for %q, want the daemon's teardown", i+1, r)
		}
	}
	if pids := orphanPIDs(wi); len(pids) != 0 {
		t.Fatalf("processes still alive after the block: %v", pids)
	}
}
