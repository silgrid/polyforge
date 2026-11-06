package test

// v2_6d_owner_relay_test.go: the owner-message relay (workflow contract
// v2 §2.2 / §5.2, PR 6d) against the REAL `pf daemon` and both fakepi
// personas. `pf answer <wi> --text "<words>"` works at any time without a
// question id: the daemon appends the reply (an ask open) or message (no
// ask) line to the capsule's asks.jsonl, emits owner_message, then
// delivers — into the live process while it is alive (a reply to the idle
// process after its ask; an interjection queued into a running turn and
// handed to the model at its next boundary), or by resuming the session
// after the warm window ended the process (claude --resume / pi
// --session, the new spawn ordinal, the message as the resumed session's
// first prompt). A message is marked delivered (the delivered line and
// event) only once its marker shows in the harness's stream; a daemon
// killed between the reply line and the delivery replays the line exactly
// once after the restart. A step's v1 question as its result (`pf step
// handoff --status needs_input`) is refused at the call (workflow
// contract 4): the session resumed after its turn ended without a
// hand-off hands off done.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/polyforge/crashpoint"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

const v26dReply = "/lean — and keep the existing tests"

// v26dLivePlan scripts a step that asks the owner, ends its turn, and
// answers the owner's later message (relayed into the resident process)
// with its done hand-off in the SAME dispatch; review is spawn 2.
func v26dLivePlan(question string) []map[string]any {
	return []map[string]any{
		{"do": "envelope", "id": "fix", "end_turn": true, "run": []string{"pf step ask " + shellQuote(question)},
			"on_message": map[string]any{"do": "envelope", "id": "fix", "outcome": "completed"}},
		{"do": "envelope", "id": "review", "outcome": "completed"},
	}
}

// v26dResumePlan scripts a step that asks the owner and ends its turn;
// the process is gone when the owner replies, so the resumed dispatch
// (spawn 2) hands fix off done and review is spawn 3.
func v26dResumePlan(question string) []map[string]any {
	return []map[string]any{
		{"do": "envelope", "id": "fix", "end_turn": true, "run": []string{"pf step ask " + shellQuote(question)}},
		{"do": "envelope", "id": "fix", "outcome": "completed"},
		{"do": "envelope", "id": "review", "outcome": "completed"},
	}
}

// v26dToolPlan scripts a step whose first turn runs one long tool call
// (toolMS) and then hands off after a pause: the interjection
// shape.
func v26dToolPlan(toolMS, ms int) []map[string]any {
	return []map[string]any{
		{"do": "envelope", "id": "fix", "tool_ms": toolMS, "ms": ms, "outcome": "completed"},
		{"do": "envelope", "id": "review", "outcome": "completed"},
	}
}

// v26dResumedHandoff asserts the resumed dispatch (spawn 2) handed fix
// off done — the call ledger's record the driver acted on — and the
// asking dispatch (spawn 1) handed off nothing.
func v26dResumedHandoff(t *testing.T, capDir string) {
	t.Helper()
	if ho := acceptedHandoff(t, capDir, "fix", 2); ho.Handoff.Status != stepcall.StatusDone {
		t.Fatalf("the hand-off of dispatch 2: %+v", *ho.Handoff)
	}
	if l := stepcall.HandoffOf(ledgerLines(t, capDir), "fix", 1); l != nil {
		t.Fatalf("the asking dispatch 1 handed off: %+v", *l)
	}
}

// v26dKind answers the events of one kind.
func v26dKind(evs []protocol.EventEntry, kind string) []protocol.EventEntry {
	var out []protocol.EventEntry
	for _, e := range evs {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// askRec is one line of the capsule's asks.jsonl as the test reads it
// (raw JSON: the record's wire shape, not the capsule package's type).
type askRec struct {
	At    string `json:"at"`
	Step  string `json:"step"`
	Spawn int64  `json:"spawn"`
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	AskAt string `json:"ask_at"`
	MsgAt string `json:"msg_at"`
	By    string `json:"by"`
}

// v26dAsks reads the capsule's asks.jsonl (raw lines, in order).
func v26dAsks(t *testing.T, capDir string) []askRec {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, "runtime-state", "asks.jsonl"))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatal(err)
	}
	var out []askRec
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var r askRec
		if err := json.Unmarshal([]byte(line), &r); err != nil {
			t.Fatalf("asks.jsonl line %q: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

// v26dLines answers the capsule's asks.jsonl lines of one kind.
func v26dLines(t *testing.T, capDir, kind string) []askRec {
	t.Helper()
	var out []askRec
	for _, l := range v26dAsks(t, capDir) {
		if l.Kind == kind {
			out = append(out, l)
		}
	}
	return out
}

// v26dWaitDelivered waits for the delivered event: a resumed session's
// delivery is confirmed by its turn_started, which the daemon reads after
// the dispatch's spawn record is written — reading the record at the
// spawn count alone races that confirmation.
func v26dWaitDelivered(t *testing.T, dataDir, wi string) {
	t.Helper()
	waitUntil(t, 30*time.Second, "the delivered event", func() bool {
		return len(v26dKind(v26cEvents(t, dataDir, wi), "delivered")) >= 1
	})
}

// v26dCheckRecord asserts the record of one relayed message: the owner
// line (kind, step, spawn, ask_at, words, writer), exactly one delivered
// line naming it under spawn, the owner_message event before the
// delivered event with the bodies the faces read.
func v26dCheckRecord(t *testing.T, dataDir, wi, capDir, kind, askAt string, spawn int64, delivered int64, disposition, confirmedBy string) askRec {
	t.Helper()
	owner := v26dLines(t, capDir, kind)
	if len(owner) != 1 || owner[0].Step != "fix" || owner[0].Spawn != spawn || owner[0].AskAt != askAt || owner[0].Text != v26dReply || owner[0].By != "usr_root" {
		t.Fatalf("the %s line: %+v", kind, owner)
	}
	line := owner[0]
	dl := v26dLines(t, capDir, "delivered")
	if len(dl) != 1 || dl[0].MsgAt != line.At || dl[0].Spawn != delivered || dl[0].Step != "fix" {
		t.Fatalf("the delivered line: %+v", dl)
	}
	evs := v26cEvents(t, dataDir, wi)
	om := v26dKind(evs, "owner_message")
	if len(om) != 1 {
		t.Fatalf("owner_message events: %+v", om)
	}
	b := v26cBody(om[0])
	if b["at"] != line.At || b["kind"] != kind || b["text"] != v26dReply || b["by"] != "usr_root" || b["step"] != "fix" || b["spawn_n"] != float64(spawn) || (askAt != "" && b["ask_at"] != askAt) {
		t.Fatalf("owner_message body: %v", b)
	}
	dv := v26dKind(evs, "delivered")
	if len(dv) != 1 {
		t.Fatalf("delivered events: %+v", dv)
	}
	b = v26cBody(dv[0])
	if b["msg_at"] != line.At || b["spawn_n"] != float64(delivered) || b["disposition"] != disposition || b["confirmed_by"] != confirmedBy || b["kind"] != kind {
		t.Fatalf("delivered body: %v", b)
	}
	if om[0].Seq >= dv[0].Seq {
		t.Fatalf("owner_message #%d must precede delivered #%d", om[0].Seq, dv[0].Seq)
	}
	return line
}

// Claude persona, a reply to the open ask while the process idles inside
// the warm window: `pf answer --text` (no question id) reports the reply
// delivered; the message reached the resident process as a stream-json
// user message framed with the delivery marker (the persona's journal
// holds it verbatim); the step left awaiting (the slot taken back: no
// question on the status face, the job counted running) and its hand-off
// — made by the SAME process in answer to the message — was accepted
// under dispatch 1 (the call ledger); `pf watch --once` lists the two events.
func TestV26dClaudeReplyDeliveredToLiveProcess(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, w159Yaml)
	d, _, _ := w159HarnessRoot(t, inst, ws, "", "--ask-warm-window", "2m")
	wi := w113NewWI(t, inst, "e2e", "v2 6d claude reply live", v26dLivePlan(v26cQuestion))
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	waitUntil(t, 90*time.Second, "the awaiting_owner event", func() bool {
		return len(v26cAwaiting(t, d.dataDir, wi)) == 1
	})
	ask := v26cOpenAsk(t, capDir)
	pid := w159WaitSpawns(t, capDir, 1, 10*time.Second)[0]["pid"]

	out, code := d.pf(inst, "answer", wi, "--text", v26dReply, "--by", "usr_root")
	if code != 0 || !strings.Contains(out, "reply to ask "+ask.At) || !strings.Contains(out, "delivered to the step's agent") {
		t.Fatalf("pf answer (%d): %s", code, out)
	}
	line := v26dCheckRecord(t, d.dataDir, wi, capDir, "reply", ask.At, 1, 1, "started", "lifecycle_started")
	// the watch face lists both events (read now: the snapshot shows the
	// newest events only)
	if out, code := d.pf(inst, "watch", wi, "--once"); code != 0 || !strings.Contains(out, "owner_message") || !strings.Contains(out, "delivered") {
		t.Fatalf("pf watch --once (%d) lacks the relay events: %s", code, out)
	}
	// the persona got the framed message as its own stream-json user
	// message: the marker, the frame naming the ask, the words verbatim,
	// into the SAME process
	var got map[string]any
	for _, e := range w159Journal(t, capDir, "fakeclaude.jsonl") {
		if e["type"] == "message" {
			got = e
		}
	}
	if got == nil || got["spawn_n"] != float64(1) {
		t.Fatalf("the persona never got the message: %v", got)
	}
	text, _ := got["text"].(string)
	if !strings.Contains(text, spawn.MessageMarker(line.At)) || !strings.Contains(text, "(ask "+ask.At+")") || !strings.HasSuffix(strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(text), spawn.MessageMarker(line.At))), v26dReply) {
		t.Fatalf("the delivered text: %q", text)
	}
	if strings.HasPrefix(text, "/") {
		t.Fatalf("the delivered text starts with a slash: %q", text)
	}
	// the same process answered with its hand-off: accepted as dispatch 1,
	// the workflow goes on to completion
	w113WaitCompleted(t, capDir, 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)
	if ho := acceptedHandoff(t, capDir, "fix", 1); ho.Handoff.Status != stepcall.StatusDone {
		t.Fatalf("the hand-off of dispatch 1: %+v", *ho.Handoff)
	}
	if cl := w159Spawns(t, capDir, "fakeclaude.jsonl"); len(cl) != 1 || cl[0]["pid"] != pid {
		t.Fatalf("claude spawns %v, want exactly the asking one", cl)
	}
	x := readExecution(t, capDir)
	if st := x.Steps["fix"]; st.Status != "done" || len(st.Attempts) != 1 || st.Attempts[0]["cause"] != "accepted" {
		t.Fatalf("the step (one accepted dispatch, no death): %+v", st)
	}
	kinds := v26cKinds(v26cEvents(t, d.dataDir, wi))
	if n := countOf(kinds, "step_started"); n != 2 { // fix once, review once
		t.Fatalf("step_started x%d: %v", n, kinds)
	}
	for _, k := range []string{"step_failed", "step_dead"} {
		if contains(kinds, k) {
			t.Fatalf("event %s: %v", k, kinds)
		}
	}
}

// Pi persona, the same reply live: the message reached the resident pi
// process as a later `prompt` (the persona's second prompt of the
// session, the marker in its user line), the status face shows the job
// running again with no question, the hand-off is dispatch 1's, and a v1
// question as another work item's result (`--status needs_input`) is
// refused at the call on the same daemon; its resumed session completes.
func TestV26dPiReplyDeliveredToLiveProcessAndV1QuestionRefused(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "v2 6d pi reply live", v26dLivePlan(v26cQuestion), nil)
	t.Cleanup(func() { killOrphans(wi) })
	d := newV1Daemon(t, inst, func(d *v1Daemon) {
		d.extraArgs = append(d.extraArgs, "--ask-warm-window", "2m")
	})
	d.start()
	d.pf(t, "submit", wi)
	waitSpawnCount(t, d, wi, 1, 30*time.Second)
	capDir := d.capsuleDir(wi)
	waitUntil(t, 60*time.Second, "the awaiting_owner event", func() bool {
		return len(v26cAwaiting(t, d.dataDir, wi)) == 1
	})
	ask := v26cOpenAsk(t, capDir)
	waitQuestion(t, d, wi, ask.At, 30*time.Second)
	if st := d.status(t); st.Queue.Running != 0 {
		t.Fatalf("a step awaiting the owner holds a running slot: %+v", st.Queue)
	}

	out := d.pf(t, "answer", wi, "--text", v26dReply, "--by", "usr_root")
	if !strings.Contains(out, "reply to ask "+ask.At) || !strings.Contains(out, "delivered to the step's agent") {
		t.Fatalf("pf answer: %s", out)
	}
	line := v26dCheckRecord(t, d.dataDir, wi, capDir, "reply", ask.At, 1, 1, "started", "user_message")
	// the persona: the message was its second prompt in the session, and
	// its scripted on_message turn answered it in this dispatch
	var second, onMessage bool
	for _, e := range fakeJournal(t, capDir) {
		switch e["type"] {
		case "prompt":
			if e["n_in_session"] == float64(2) && e["spawn_n"] == float64(1) {
				second = true
			}
		case "on_message":
			if m, _ := e["message"].(string); strings.Contains(m, spawn.MessageMarker(line.At)) && strings.Contains(m, v26dReply) && !strings.HasPrefix(m, "/") {
				onMessage = true
			}
		}
	}
	if !second || !onMessage {
		t.Fatalf("the persona's record (second prompt %v, on_message %v)", second, onMessage)
	}
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	if ho := acceptedHandoff(t, capDir, "fix", 1); ho.Handoff.Status != stepcall.StatusDone {
		t.Fatalf("the hand-off of dispatch 1: %+v", *ho.Handoff)
	}
	if spawns := spawnJournal(t, capDir); len(spawns) != 2 { // fix, review
		t.Fatalf("spawns: %v", spawns)
	}
	// (e) a v1 question as the dispatch's result (`pf step handoff
	// --status needs_input`) is refused at the call: the ledger records
	// the refusal naming the status set and nothing else, no question is
	// persisted, and the session resumed after the turn ended without a
	// hand-off (spawn 2) hands off done
	wi2 := inst.newWI(t, "v2 6d v1 question", []map[string]any{
		{"do": "envelope", "id": "fix", "outcome": "needs_input", "question_id": "q_v26d_v1", "question": "the v1 way?"},
		{"do": "envelope", "id": "fix", "outcome": "completed"},
		{"do": "envelope", "id": "review", "outcome": "completed"},
	}, nil)
	t.Cleanup(func() { killOrphans(wi2) })
	d.pf(t, "submit", wi2)
	inst.waitWIStatus(t, wi2, "wrapped", 240*time.Second)
	cap2 := d.capsuleDir(wi2)
	var refused []stepcall.Line
	for _, l := range ledgerLines(t, cap2) {
		if l.Kind == stepcall.KindResult && l.Verb == stepcall.VerbHandoff && l.Outcome == stepcall.OutcomeRefused {
			refused = append(refused, l)
		}
	}
	if len(refused) != 1 || refused[0].Step != "fix" || refused[0].Spawn != 1 || !strings.Contains(refused[0].Error, `"needs_input" is none of done, fail`) {
		t.Fatalf("SYMPTOM: the v1 needs_input was not refused at the call: %+v", refused)
	}
	if l := stepcall.HandoffOf(ledgerLines(t, cap2), "fix", 1); l != nil {
		t.Fatalf("the refused call recorded a hand-off: %+v", *l)
	}
	if ho := acceptedHandoff(t, cap2, "fix", 2); ho.Handoff.Status != stepcall.StatusDone {
		t.Fatalf("the resumed dispatch's hand-off: %+v", *ho.Handoff)
	}
	if lines := v26dAsks(t, cap2); len(lines) != 0 {
		t.Fatalf("a refused v1 question leaves no ask record: %+v", lines)
	}
	if n := len(v26cAwaiting(t, d.dataDir, wi2)); n != 0 {
		t.Fatalf("a refused v1 question parks nothing on the owner: %d awaiting_owner events", n)
	}
}

// Claude persona, the reply after the warm window ended the process: the
// step is parked on its ask with no process; `pf answer --text` reports
// that the session is resumed; the daemon re-dispatches the step as a
// `--resume` of the same native session (spawn 2, the same uuid) whose
// first user message is the owner's reply alone (framed, the marker),
// marks it delivered once that dispatch's first turn starts, and the
// hand-off the resumed dispatch makes is recorded as spawn 2's
// (lifecycle (iii)). The resume-after-the-warm-window relay is one
// e2e for both harnesses: the relay's part — the reply
// alone as the resumed session's first prompt, delivered at turn_started
// — is the same on both (the driver's
// TestRelayResumesTheSessionAfterTheWarmWindow is its detailed test), and
// pi's session resume carrying a reply is
// TestV26dPiCrashBetweenReplyAndDeliveryReplaysOnce's.
func TestV26dClaudeReplyAfterWarmWindowResumesSession(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, w159Yaml)
	d, _, _ := w159HarnessRoot(t, inst, ws, "", "--ask-warm-window", "3s")
	wi := w113NewWI(t, inst, "e2e", "v2 6d claude resume", v26dResumePlan(v26cQuestion))
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	waitUntil(t, 90*time.Second, "the awaiting_owner event", func() bool {
		return len(v26cAwaiting(t, d.dataDir, wi)) == 1
	})
	ask := v26cOpenAsk(t, capDir)
	waitUntil(t, 30*time.Second, "the warm window to end the process", func() bool {
		return len(w159Exits(t, capDir)) == 1 && len(orphanPIDs(wi)) == 0
	})
	first := w159Spawns(t, capDir, "fakeclaude.jsonl")[0]

	out, code := d.pf(inst, "answer", wi, "--text", v26dReply, "--by", "usr_root")
	if code != 0 || !strings.Contains(out, "resumes the session") {
		t.Fatalf("pf answer (%d): %s", code, out)
	}
	spawns := w159WaitSpawns(t, capDir, 2, 60*time.Second)
	second := spawns[1]
	if second["resume"] != true || second["session"] != first["session"] || second["spawn_n"] != float64(2) || second["step"] != "fix" {
		t.Fatalf("the resume dispatch: %v (first %v)", second, first)
	}
	// the resumed session's first message: the owner's reply alone
	prompt, err := os.ReadFile(filepath.Join(capDir, "logs", "fakeclaude-prompt-2.txt"))
	if err != nil {
		t.Fatal(err)
	}
	v26dWaitDelivered(t, d.dataDir, wi)
	line := v26dCheckRecord(t, d.dataDir, wi, capDir, "reply", ask.At, 1, 2, "prompt", "turn_started")
	if p := string(prompt); !strings.HasSuffix(strings.TrimSpace(p), spawn.MessageMarker(line.At)) || !strings.Contains(p, v26dReply) || strings.Contains(p, "# Step definition") || strings.HasPrefix(p, "/") {
		t.Fatalf("the resume prompt: %q", p)
	}
	w113WaitCompleted(t, capDir, 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)
	v26dResumedHandoff(t, capDir)
	x := readExecution(t, capDir)
	if st := x.Steps["fix"]; st.Status != "done" || len(st.Attempts) != 1 || st.Attempts[0]["cause"] != "accepted" {
		t.Fatalf("the step (the resumed dispatch accepted, no death archived): %+v", st)
	}
}

// Claude persona, an interjection mid-turn (no ask open): the message is
// recorded as `message`, claude queues it at once (`pf answer` reports it
// queued) and hands it to the model after the running tool call (the
// started lifecycle: the delivered line and event, confirmed_by
// lifecycle_started); the turn goes on and its hand-off is dispatch 1's.
func TestV26dClaudeInterjectionMidTurn(t *testing.T) {
	inst := newV1Install(t)
	ws := t.TempDir()
	w079Yaml(t, ws, w159Yaml)
	d, _, _ := w159HarnessRoot(t, inst, ws, "")
	wi := w113NewWI(t, inst, "e2e", "v2 6d claude interjection", v26dToolPlan(8000, 3000))
	t.Cleanup(func() { killOrphans(wi) })
	d.pfWant(inst, -1, "submit", wi)
	capDir := w079Capsule(t, inst, ws, wi)
	w113WaitCapsule(t, capDir, 90*time.Second)
	waitUntil(t, 90*time.Second, "the tool call to start", func() bool {
		for _, e := range w159Journal(t, capDir, "fakeclaude.jsonl") {
			if e["type"] == "tool_start" {
				return true
			}
		}
		return false
	})
	out, code := d.pf(inst, "answer", wi, "--text", v26dReply, "--by", "usr_root")
	if code != 0 || !strings.Contains(out, "message recorded") || !strings.Contains(out, "queued by the step process") {
		t.Fatalf("pf answer (%d): %s", code, out)
	}
	waitUntil(t, 30*time.Second, "the delivered event", func() bool {
		return len(v26dKind(v26cEvents(t, d.dataDir, wi), "delivered")) == 1
	})
	v26dCheckRecord(t, d.dataDir, wi, capDir, "message", "", 1, 1, "queued", "lifecycle_started")
	var absorbed bool
	for _, e := range w159Journal(t, capDir, "fakeclaude.jsonl") {
		if e["type"] == "message_absorbed" {
			absorbed = true
		}
	}
	if !absorbed {
		t.Fatal("the persona never absorbed the queued message into its turn")
	}
	if len(v26cAwaiting(t, d.dataDir, wi)) != 0 {
		t.Fatal("an interjection parked the step")
	}
	w113WaitCompleted(t, capDir, 120*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)
	if cl := w159Spawns(t, capDir, "fakeclaude.jsonl"); len(cl) != 1 {
		t.Fatalf("claude spawns %v", cl)
	}
}

// Pi persona, an interjection mid-turn: pi queues the `prompt` (steer) and
// hands it to the model at the turn's boundary (the user message carrying
// the marker: delivered, confirmed_by user_message).
func TestV26dPiInterjectionMidTurn(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "v2 6d pi interjection", v26dToolPlan(8000, 3000), nil)
	t.Cleanup(func() { killOrphans(wi) })
	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	waitSpawnCount(t, d, wi, 1, 30*time.Second)
	capDir := d.capsuleDir(wi)
	waitUntil(t, 60*time.Second, "the tool call to start", func() bool {
		for _, e := range fakeJournal(t, capDir) {
			if e["type"] == "tool_start" {
				return true
			}
		}
		return false
	})
	out := d.pf(t, "answer", wi, "--text", v26dReply, "--by", "usr_root")
	if !strings.Contains(out, "message recorded") || !strings.Contains(out, "queued by the step process") {
		t.Fatalf("pf answer: %s", out)
	}
	waitUntil(t, 30*time.Second, "the delivered event", func() bool {
		return len(v26dKind(v26cEvents(t, d.dataDir, wi), "delivered")) == 1
	})
	v26dCheckRecord(t, d.dataDir, wi, capDir, "message", "", 1, 1, "queued", "user_message")
	var queued, delivered bool
	for _, e := range fakeJournal(t, capDir) {
		switch e["type"] {
		case "message_queued":
			queued = true
		case "message_delivered":
			delivered = queued
		}
	}
	if !queued || !delivered {
		t.Fatalf("the persona's record (queued %v, delivered %v)", queued, delivered)
	}
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	if spawns := spawnJournal(t, capDir); len(spawns) != 2 {
		t.Fatalf("spawns: %v", spawns)
	}
}

// Pi persona, a kill -9 of the daemon between the reply line and the
// delivery (the crash point after the owner_message event): the restart
// finds the replied ask and the undelivered reply, re-dispatches the step
// as a resume of its session carrying the reply in its prompt, marks it
// delivered exactly once (one delivered line, one delivered event, the
// marker once in the prompt), and the resumed dispatch's hand-off is
// accepted as spawn 2.
func TestV26dPiCrashBetweenReplyAndDeliveryReplaysOnce(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "v2 6d pi crash replay", v26dResumePlan(v26cQuestion), nil)
	t.Cleanup(func() { killOrphans(wi) })
	d := newV1Daemon(t, inst, optEnv(crashpoint.EnvVar+"=driver.owner_message:fix"), func(d *v1Daemon) {
		d.extraArgs = append(d.extraArgs, "--ask-warm-window", "2m")
	})
	d.start()
	d.pf(t, "submit", wi)
	waitSpawnCount(t, d, wi, 1, 30*time.Second)
	capDir := d.capsuleDir(wi)
	waitUntil(t, 60*time.Second, "the awaiting_owner event", func() bool {
		return len(v26cAwaiting(t, d.dataDir, wi)) == 1
	})
	ask := v26cOpenAsk(t, capDir)
	// the reply: the daemon records it, emits the event and dies at the
	// crash point — the CLI sees the connection drop
	if code, _ := v26cPfStderr(t, inst, d.sock, "answer", wi, "--text", v26dReply, "--by", "usr_root"); code == 0 {
		t.Fatal("pf answer succeeded although the daemon died at the crash point")
	}
	select {
	case <-d.waitedExit:
	case <-time.After(20 * time.Second):
		t.Fatal("the daemon did not die at the crash point")
	}
	d.cmd = nil
	reply := v26dLines(t, capDir, "reply")
	if len(reply) != 1 || reply[0].AskAt != ask.At || reply[0].Text != v26dReply {
		t.Fatalf("the reply line before the restart: %+v", reply)
	}
	if dl := v26dLines(t, capDir, "delivered"); len(dl) != 0 {
		t.Fatalf("delivered before any delivery: %+v", dl)
	}
	if n := len(v26dKind(v26cEvents(t, d.dataDir, wi), "owner_message")); n != 1 {
		t.Fatalf("owner_message events before the restart: %d", n)
	}

	d2 := restartSameDaemon(t, d, "daemon2.log") // the crash point is not armed again (env is not carried)
	spawns := waitSpawnCount(t, d2, wi, 2, 90*time.Second)
	if s := spawns[1]; s["resume"] != true || s["spawn_n"] != float64(2) {
		t.Fatalf("the restart's dispatch: %v", s)
	}
	v26dWaitDelivered(t, d2.dataDir, wi)
	line := v26dCheckRecord(t, d2.dataDir, wi, capDir, "reply", ask.At, 1, 2, "prompt", "turn_started")
	prompt, err := os.ReadFile(filepath.Join(capDir, "logs", "fakepi-prompt-2.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if p := string(prompt); strings.Count(p, spawn.MessageMarker(line.At)) != 1 || !strings.Contains(p, v26dReply) {
		t.Fatalf("the restart's prompt carries the reply %d times: %q", strings.Count(p, spawn.MessageMarker(line.At)), p)
	}
	inst.waitWIStatus(t, wi, "wrapped", 120*time.Second)
	v26dResumedHandoff(t, capDir)
	time.Sleep(time.Second)
	if dl := v26dLines(t, capDir, "delivered"); len(dl) != 1 {
		t.Fatalf("delivered lines after the restart: %+v", dl)
	}
	if n := len(v26dKind(v26cEvents(t, d2.dataDir, wi), "delivered")); n != 1 {
		t.Fatalf("delivered events after the restart: %d", n)
	}
}
