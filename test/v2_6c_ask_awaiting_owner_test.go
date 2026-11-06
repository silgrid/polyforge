package test

// v2_6c_ask_awaiting_owner_test.go: `pf step ask` and the awaiting-owner state
// (workflow contract v2 §2.2 / §5.2, PR 6c) against the REAL `pf daemon`
// and both fakepi personas. A step whose agent runs `pf step ask "<question>"`
// and ends its turn is parked AWAITING THE OWNER: the `awaiting_owner`
// event carries the question, the faces show it as the step's pending
// question (id = the ask's stamp), the running slot is released (another
// queued job starts under max_concurrent_jobs 1), nothing is judged
// (no rule-1b retry after the grace), the warm window ends the idle
// process and the step stays awaiting on the same dispatch, and a daemon
// restart parks it again without a new dispatch (lifecycle (i)). A turn
// that ends with neither a result nor an ask stays rule 1b
// (TestW159ClaudeD7, v1_s06).

import (
	"encoding/json"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

const v26cQuestion = "Which design should the fix take: lean or compatible?"

// v26cAskPlan scripts a step that asks the owner and ends its turn (the
// later completed envelopes only guard against an accidental re-dispatch:
// a second spawn of fix would complete the work item).
func v26cAskPlan(question string) []map[string]any {
	return []map[string]any{
		{"do": "envelope", "id": "fix", "end_turn": true, "run": []string{"pf step ask " + shellQuote(question)}},
		{"do": "envelope", "id": "fix", "outcome": "completed"},
		{"do": "envelope", "id": "review", "outcome": "completed"},
	}
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// v26cPfStderr runs the real CLI against sock and answers its exit code
// and stderr (the harness's pfExit answers stdout only).
func v26cPfStderr(t *testing.T, inst *v1Install, sock string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(inst.pfPath, append([]string{"--config", hermeticConfigPath(t)}, args...)...)
	cmd.Env = append(os.Environ(), "PF_SERVER="+inst.url, "PF_API_KEY="+inst.adminKey, "PF_USER_ID=usr_root", "PF_DAEMON_SOCKET="+sock)
	v1BusinessCmd(t, cmd)
	var stdout, stderr strings.Builder
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("pf %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return code, stderr.String()
}

func hasPID(pids []int, pid int) bool {
	for _, p := range pids {
		if p == pid {
			return true
		}
	}
	return false
}

// v26cEvents answers the daemon's durable events of wi (any rig: the
// Cell's events dir under dataDir).
func v26cEvents(t *testing.T, dataDir, wi string) []protocol.EventEntry {
	t.Helper()
	var out []protocol.EventEntry
	for _, line := range strings.Split(eventsRaw(t, dataDir), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var e protocol.EventEntry
		if json.Unmarshal([]byte(line), &e) == nil && e.WI == wi {
			out = append(out, e)
		}
	}
	return out
}

func v26cKinds(evs []protocol.EventEntry) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.Kind)
	}
	return out
}

// v26cAwaiting answers the awaiting_owner events of wi.
func v26cAwaiting(t *testing.T, dataDir, wi string) []protocol.EventEntry {
	t.Helper()
	var out []protocol.EventEntry
	for _, e := range v26cEvents(t, dataDir, wi) {
		if e.Kind == "awaiting_owner" {
			out = append(out, e)
		}
	}
	return out
}

func v26cBody(e protocol.EventEntry) map[string]any {
	var b map[string]any
	_ = json.Unmarshal(e.Body, &b)
	return b
}

// v26cOpenAsk answers the capsule record's one open ask of (fix, 1).
func v26cOpenAsk(t *testing.T, capDir string) capsule.AskLine {
	t.Helper()
	lines, err := capsule.ReadAsks(capDir)
	if err != nil || len(lines) != 1 {
		t.Fatalf("asks.jsonl: %+v %v", lines, err)
	}
	open := capsule.OpenAsk(lines, "fix", 1)
	if open == nil || open.Kind != "ask" || open.Text != v26cQuestion || open.Step != "fix" || open.Spawn != 1 {
		t.Fatalf("the open ask: %+v", open)
	}
	return *open
}

// Claude persona: the ask parks the step awaiting the owner; the rule-1b
// grace (2 s on this rig) passes with no verdict; the warm window (4 s)
// ends the idle process by the daemon's hand and the step stays awaiting
// on dispatch 1 — no step_failed, no step_dead, no second spawn; the
// faces show the question.
func TestV26cClaudeAskParksAwaitingOwner(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, w159Yaml)
	d, _, _ := w159HarnessRoot(t, inst, ws, "", "--ask-warm-window", "4s")
	wi := w113NewWI(t, inst, "e2e", "v2 6c claude pf step ask", v26cAskPlan(v26cQuestion))
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	first := w159WaitSpawns(t, capDir, 1, 60*time.Second)[0]
	if first["step"] != "fix" {
		t.Fatalf("first spawn %v", first)
	}

	waitUntil(t, 60*time.Second, "the awaiting_owner event", func() bool {
		return len(v26cAwaiting(t, d.dataDir, wi)) == 1
	})
	ask := v26cOpenAsk(t, capDir)
	ev := v26cBody(v26cAwaiting(t, d.dataDir, wi)[0])
	if ev["step"] != "fix" || ev["question"] != v26cQuestion || ev["ask_at"] != ask.At || ev["question_id"] != ask.At || ev["spawn_n"] != float64(1) {
		t.Fatalf("awaiting_owner body: %v", ev)
	}
	// `pf step ask` itself: recorded as the turn's tool call, exit 0, and the
	// one instruction it prints
	var ran bool
	for _, e := range fakeJournal(t, capDir) { // the shared run journal (logs/fakepi.jsonl)
		if e["type"] == "run" && strings.Contains(e["cmd"].(string), "pf step ask") {
			ran = true
			if e["exit_code"] != float64(0) || !strings.Contains(e["output"].(string), "now end your turn") {
				t.Fatalf("pf step ask run: %v", e)
			}
		}
	}
	if !ran {
		t.Fatal("the persona never ran pf step ask")
	}
	// the faces: the watch snapshot's pending question is the ask (id =
	// its stamp), `pf watch --once` prints the question text, the status
	// face shows the job waiting on it
	out, code := d.pf(inst, "watch", wi, "--once")
	if code != 0 || !strings.Contains(out, "question:") || !strings.Contains(out, v26cQuestion) || !strings.Contains(out, ask.At) {
		t.Fatalf("pf watch --once (%d): %s", code, out)
	}
	if out, code := d.pf(inst, "ops", "status"); code != 0 || !strings.Contains(out, ask.At) {
		t.Fatalf("pf ops status (%d) lacks the question id: %s", code, out)
	}
	x := readExecution(t, capDir)
	if st := x.Steps["fix"]; st.Status != "in_progress" || st.Dispatch != 1 || st.PendingQuestion != nil {
		t.Fatalf("the asking step: %+v", st)
	}

	// the warm window ends the process (an exit line of the daemon's
	// making) and nothing else happens: the rule-1b grace passed with no
	// verdict, no retry, the step still awaiting on dispatch 1
	waitUntil(t, 30*time.Second, "the warm window to end the process", func() bool {
		return len(w159Exits(t, capDir)) == 1
	})
	if r := w159Exits(t, capDir)[0]["reason"]; r != "stdin_eof" && r != "sigterm" {
		t.Fatalf("the process exited for %q, want the daemon's teardown", r)
	}
	time.Sleep(4 * time.Second) // well past the 2 s grace and the 1 s ask poll
	x = readExecution(t, capDir)
	if st := x.Steps["fix"]; st.Status != "in_progress" || st.Dispatch != 1 || len(st.Attempts) != 0 || st.PendingQuestion != nil {
		t.Fatalf("the step moved after the warm window: %+v", st)
	}
	if x.WorkflowStatus != "running" {
		t.Fatalf("workflow %s", x.WorkflowStatus)
	}
	kinds := v26cKinds(v26cEvents(t, d.dataDir, wi))
	for _, k := range []string{"step_failed", "step_dead", "verdict_stale"} {
		if contains(kinds, k) {
			t.Fatalf("event %s after the ask: %v", k, kinds)
		}
	}
	if n := countOf(kinds, "step_started"); n != 1 {
		t.Fatalf("step_started x%d: the step was re-dispatched: %v", n, kinds)
	}
	if cl := w159Spawns(t, capDir, "fakeclaude.jsonl"); len(cl) != 1 {
		t.Fatalf("claude spawns %v, want exactly the asking one", cl)
	}
	if pids := orphanPIDs(wi); len(pids) != 0 {
		t.Fatalf("processes alive after the warm window: %v", pids)
	}
	// the question stays the faces' pending question with no process
	if snap := watchSoftW113(t, d, inst, wi); snap == nil || snap.Execution == nil || snap.Execution.PendingQuestion == nil ||
		snap.Execution.PendingQuestion.ID != ask.At || snap.Execution.PendingQuestion.Question != v26cQuestion {
		t.Fatalf("watch snapshot after the warm window: %+v", snap)
	}
}

// watchSoftW113 reads the watch snapshot through the real CLI (--json).
func watchSoftW113(t *testing.T, d *w113Daemon, inst *v1Install, wi string) *protocol.WatchSnapshot {
	t.Helper()
	out, code := d.pf(inst, "--json", "watch", wi, "--once")
	if code != 0 {
		t.Fatalf("pf --json watch (%d): %s", code, out)
	}
	var snap protocol.WatchSnapshot
	if err := json.Unmarshal([]byte(out), &snap); err != nil {
		t.Fatalf("watch snapshot: %v (%s)", err, out)
	}
	return &snap
}

// Pi persona: the waiting step releases its running slot (a second work
// item starts and wraps under max_concurrent_jobs 1 while the first is
// awaiting), a daemon crash inside the warm window is reconciled into
// the same awaiting state — the crashed daemon's step process reaped, no
// new dispatch, the event re-announced, the question still on the faces
// — the owner's reply is the relay's (v2_6d_owner_relay_test.go).
func TestV26cPiAskReleasesSlotAndSurvivesRestart(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "v2 6c pi pf step ask", v26cAskPlan(v26cQuestion), nil)
	t.Cleanup(func() { killOrphans(wi) })
	d := newV1Daemon(t, inst, func(d *v1Daemon) {
		d.extraArgs = append(d.extraArgs, "--max-concurrent-jobs", "1", "--ask-warm-window", "2m")
	})
	d.start()
	d.pf(t, "submit", wi)
	waitSpawnCount(t, d, wi, 1, 30*time.Second)
	capDir := d.capsuleDir(wi)
	waitUntil(t, 60*time.Second, "the awaiting_owner event", func() bool {
		return len(v26cAwaiting(t, d.dataDir, wi)) == 1
	})
	ask := v26cOpenAsk(t, capDir)
	survivor := latestSpawnPID(t, d, wi)
	// the fake ran pf step ask as a tool call inside the turn, then ended it
	var ran, ended bool
	for _, e := range fakeJournal(t, capDir) {
		switch e["type"] {
		case "run":
			if strings.Contains(e["cmd"].(string), "pf step ask") && e["exit_code"] == float64(0) {
				ran = true
			}
		case "end_turn":
			ended = true
		}
	}
	if !ran || !ended {
		t.Fatalf("the persona did not ask then end its turn (ran=%v ended=%v)", ran, ended)
	}
	// the status face: the job waits on the ask (its stamp in the
	// QUESTION column) and holds no running slot
	waitQuestion(t, d, wi, ask.At, 30*time.Second)
	st := d.status(t)
	if st.Queue.Running != 0 {
		t.Fatalf("a step awaiting the owner holds a running slot: %+v", st.Queue)
	}

	// the slot release: under max_concurrent_jobs 1 a second work item
	// starts and wraps while the first waits
	wi2 := inst.newWI(t, "v2 6c slot taker", envelopeMicro(), nil)
	d.pf(t, "submit", wi2)
	inst.waitWIStatus(t, wi2, "wrapped", 120*time.Second)
	if questionIDOf(statusQuiet(d), wi) != ask.At {
		t.Fatal("the first work item stopped waiting while the second ran")
	}
	// (the owner's reply into the asking session is the relay's,
	// v2_6d_owner_relay_test.go: here the ask stays open)

	// a crash inside the warm window (the step process alive, idle)
	if !hasPID(orphanPIDs(wi), survivor) {
		t.Fatalf("the asking process %d is not alive inside the warm window", survivor)
	}
	d.crash()
	d2 := restartSameDaemon(t, d, "daemon2.log")
	// the restart reaps the crashed daemon's step process ...
	waitUntil(t, 30*time.Second, "the crashed daemon's step process survived the restart", func() bool {
		return !hasPID(orphanPIDs(wi), survivor)
	})
	// ... and parks the step on its open ask: the event re-announced for
	// the restarted driver, no new dispatch, the question on the faces
	waitUntil(t, 60*time.Second, "the re-announced awaiting_owner", func() bool {
		for _, e := range v26cAwaiting(t, d2.dataDir, wi) {
			if v26cBody(e)["resumed"] == true {
				return true
			}
		}
		return false
	})
	waitQuestion(t, d2, wi, ask.At, 30*time.Second)
	time.Sleep(3 * time.Second)
	if spawns := spawnJournal(t, capDir); len(spawns) != 1 {
		t.Fatalf("the restart re-dispatched the asking step: %v", spawns)
	}
	x := readExecution(t, capDir)
	if s := x.Steps["fix"]; s.Status != "in_progress" || s.Dispatch != 1 || len(s.Attempts) != 0 || s.PendingQuestion != nil {
		t.Fatalf("the step after the restart: %+v", s)
	}
	// the events after the restart (the file spans both daemons): the
	// re-announce and nothing of a dispatch
	kinds := d2.eventKinds(t, wi)
	after := kinds
	for i := len(kinds) - 1; i >= 0; i-- {
		if kinds[i] == "daemon_restarted" {
			after = kinds[i+1:]
			break
		}
	}
	for _, k := range []string{"step_failed", "step_dead", "step_started"} {
		if contains(after, k) {
			t.Fatalf("the restarted daemon %s: %v", k, kinds)
		}
	}
	if !contains(after, "awaiting_owner") {
		t.Fatalf("no awaiting_owner after the restart: %v", kinds)
	}
	snap := d2.watch(t, wi)
	if q := snap.Execution.PendingQuestion; q == nil || q.ID != ask.At || q.Question != v26cQuestion || q.Step != "fix" {
		t.Fatalf("watch pending question after the restart: %+v", snap.Execution)
	}
	if out := d2.pf(t, "watch", wi, "--once"); !strings.Contains(out, v26cQuestion) {
		t.Fatalf("pf watch --once after the restart: %s", out)
	}
	if lines, _ := capsule.ReadAsks(capDir); len(lines) != 1 || lines[0].At != ask.At {
		t.Fatalf("the record changed across the restart: %+v", lines)
	}
	if pids := orphanPIDs(wi); len(pids) != 0 {
		t.Fatalf("step processes alive after the restart parked the step: %v", pids)
	}
}
