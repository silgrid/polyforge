package driver

// relay_test.go: the owner-message relay (relay.go, workflow contract v2
// §5.2, PR 6d) over the scripted spawner: the framing that keeps an
// owner's `/word` from being read as a harness command and embeds the
// delivery marker; the delivery proofs the watch accepts per harness; a
// reply into the live, awaiting process (the record line before the
// event before the delivery, the state back to running, the slot taken
// back, the delivered line only on the marker); an interjection into a
// running turn (queued, then confirmed); a reply after the warm window
// (no live process: the plan pass resumes the session, the next
// dispatch carries the message and marks it delivered on its first
// turn, under its own spawn ordinal); a delivery proof that raced the
// result envelope (marked by the dispatch's last look); a reply the
// awaiting process refuses (the warm wait ends at once, the resume
// carries it); the two prompt forms; and the refusals when no step agent
// can be talked to.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// The relay face of the scripted run (the test lane dispatches on pi):
// the message is kept, answered with the entry's disposition (started by
// default) under a message command id, and — unless the entry refuses —
// the dispatch's watch is fed the user message carrying the delivered
// text at once when idle (pi's started shape), so a test feeds the watch
// itself only for the queued shape.
func (r *fakeRun) SendMessage(ctx context.Context, m spawn.Message) (spawn.Delivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.e.refuse != nil {
		return spawn.Delivery{}, r.e.refuse
	}
	r.sent = append(r.sent, m)
	id := fmt.Sprintf("pf-msg-%d-%d", r.sp.SpawnN, len(r.sent))
	disp := r.e.disposition
	if disp == "" {
		disp = spawn.DispositionStarted
	}
	if disp == spawn.DispositionStarted {
		r.watch.feed(piUserMessage(m))
	}
	return spawn.Delivery{CommandID: id, Disposition: disp}, nil
}

// piUserMessage is the stream record of the delivered text handed to the
// model (pi: message_start, role user).
func piUserMessage(m spawn.Message) string {
	raw, _ := json.Marshal(map[string]any{"type": "message_start", "message": map[string]any{
		"role": "user", "content": []any{map[string]any{"type": "text", "text": spawn.MessageText(m)}},
	}})
	return string(raw)
}

func (r *fakeRun) deliveryWatch() *deliveryWatch { return r.watch }

func (r *fakeRun) carriedMessages() []capsule.AskLine { return r.sp.OwnerMessages }

func (r *fakeRun) messages() []spawn.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]spawn.Message(nil), r.sent...)
}

// The framing: an owner's words that start with `/` never reach the
// harness as a line starting with `/` (pi would read an extension
// command, the steer path refuses one), the words themselves are verbatim
// inside, a reply names the ask it answers by its stamp, and the delivery
// marker is the delivered text's own last line, keyed by the message's
// `at` — never by a harness command id.
func TestOwnerMessageTextGuardsSlashAndCarriesTheMarker(t *testing.T) {
	reply := capsule.AskLine{At: "2026-11-10T21:00:00.000000001Z", Step: "fix", Spawn: 1, Kind: capsule.AskKindReply, AskAt: "2026-11-10T20:59:00Z", Text: "/lean\nand keep the tests", By: "usr_root"}
	m := ownerMessageText(reply)
	if m.Key != reply.At {
		t.Fatalf("the delivery key is the line's at, got %q", m.Key)
	}
	text := spawn.MessageText(m)
	if strings.HasPrefix(text, "/") || strings.HasPrefix(strings.TrimSpace(text), "/") {
		t.Fatalf("the delivered text starts with a slash: %q", text)
	}
	if !strings.Contains(text, "\n/lean\nand keep the tests") {
		t.Fatalf("the owner's words are not verbatim in: %q", text)
	}
	if !strings.Contains(text, "(ask "+reply.AskAt+")") || !strings.Contains(text, "from usr_root") {
		t.Fatalf("the reply frame names neither the ask nor the writer: %q", text)
	}
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if last := lines[len(lines)-1]; last != spawn.MessageMarker(reply.At) {
		t.Fatalf("the marker is not the last line: %q", last)
	}
	msg := capsule.AskLine{At: "2026-11-10T21:01:00Z", Step: "fix", Spawn: 1, Kind: capsule.AskKindMessage, Text: "also run the e2e"}
	if got := spawn.MessageText(ownerMessageText(msg)); !strings.HasPrefix(got, "Owner message:\nalso run the e2e") || strings.Contains(got, "(ask ") {
		t.Fatalf("the message frame: %q", got)
	}
}

// The delivery proofs: claude — the `started` lifecycle of the message's
// uuid (never `queued`), pi — the marker inside a user message_start
// (never an assistant message, never pi's queue_update, which echoes the
// text while it is still queued); the first turn start (claude's
// system/init, pi's agent_start) proves a prompt-carried message.
func TestDeliveryWatchProofs(t *testing.T) {
	const key = "2026-11-10T21:00:00.000000001Z"
	marker := spawn.MessageMarker(key)
	cl := newDeliveryWatch(harness.Claude)
	cl.feed(`{"type":"command_lifecycle","command_uuid":"pf-msg-1-1","state":"queued"}`)
	if how, ok := cl.confirmed(key, "pf-msg-1-1"); ok {
		t.Fatalf("queued is not delivery (%s)", how)
	}
	cl.feed(`{"type":"command_lifecycle","command_uuid":"pf-msg-1-1","state":"started"}`)
	if how, ok := cl.confirmed(key, "pf-msg-1-1"); !ok || how != confirmedLifecycle {
		t.Fatalf("started lifecycle: %v %s", ok, how)
	}
	if _, ok := cl.confirmed(key, "pf-msg-1-2"); ok {
		t.Fatal("another uuid's lifecycle must not confirm")
	}
	if cl.turnStarted() {
		t.Fatal("no turn started yet")
	}
	cl.feed(`{"type":"system","subtype":"init","session_id":"x"}`)
	if !cl.turnStarted() {
		t.Fatal("system/init is claude's turn start")
	}
	pi := newDeliveryWatch(harness.Pi)
	pi.feed(`{"type":"queue_update","steering":["Owner message:\nhi\n\n` + marker + `"],"followUp":[]}`)
	pi.feed(`{"type":"message_start","message":{"role":"assistant","content":[{"type":"text","text":"` + marker + `"}]}}`)
	if _, ok := pi.confirmed(key, "pf-msg-1-1"); ok {
		t.Fatal("a queued echo or an assistant message is not delivery")
	}
	pi.feed(`{"type":"command_lifecycle","command_uuid":"pf-msg-1-1","state":"started"}`)
	if _, ok := pi.confirmed(key, "pf-msg-1-1"); ok {
		t.Fatal("a claude lifecycle record never confirms a pi dispatch")
	}
	pi.feed(`{"type":"message_start","message":{"role":"user","content":[{"type":"text","text":"Owner message:\nhi\n\n` + marker + `"}]}}`)
	if how, ok := pi.confirmed(key, ""); !ok || how != confirmedUser {
		t.Fatalf("the user message carrying the marker: %v %s", ok, how)
	}
	if pi.turnStarted() {
		t.Fatal("no turn started yet")
	}
	pi.feed(`{"type":"agent_start"}`)
	if !pi.turnStarted() {
		t.Fatal("agent_start is pi's turn start")
	}
	if keys := markerKeys("a [pf-msg:k1] b [pf-msg:k2] [pf-msg:] [pf-msg:unterminated"); strings.Join(keys, ",") != "k1,k2" {
		t.Fatalf("marker keys: %v", keys)
	}
}

// A reply into the live process: the dispatch asked and awaits the owner
// (the slot released); the owner's message is recorded as a reply naming
// the ask, the owner_message event follows the line, the run takes it
// (framed, keyed by the line's at), the step is running again (the slot
// taken back, the supervisor told, no warm-window teardown), the
// delivered line and event follow the marker, and the result the run then
// delivers is accepted under the SAME dispatch — no re-dispatch.
func TestRelayDeliversReplyIntoTheLiveProcess(t *testing.T) {
	dir := t.TempDir()
	sp := &fakeSpawner{script: []fakeEntry{{hold: true, res: relayCompleted()}, done(`{}`)}}
	d, log, ow, link := askEnv(t, dir, sp, nil, 10*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	waitUntil(t, "the dispatch to start", func() bool { return len(sp.dispatched()) == 1 })
	ask := writeAsk(t, dir, "fix", 1, "lean or compatible?")
	waitUntil(t, "the awaiting_owner event", func() bool { return len(log.byKind(EvAwaitingOwner)) == 1 })
	if w, ok := ow.last(); !ok || !w {
		t.Fatalf("the slot was not released: %v", ow.calls)
	}
	drainFacts(link)

	ack, err := d.OwnerMessage(ctx, "/lean, keep the tests", "usr_root")
	if err != nil {
		t.Fatalf("OwnerMessage: %v", err)
	}
	if ack.Kind != capsule.AskKindReply || ack.AskAt != ask.At || ack.Step != "fix" || ack.SpawnN != 1 || !ack.Delivered || ack.Disposition != spawn.DispositionStarted {
		t.Fatalf("the ack: %+v", ack)
	}
	// the record: ask, reply (naming the ask, the step, the dispatch, the
	// writer, the words verbatim), delivered (naming the reply)
	lines := mustAsks(t, dir)
	if len(lines) != 3 || lines[1].Kind != capsule.AskKindReply || lines[1].AskAt != ask.At || lines[1].Step != "fix" || lines[1].Spawn != 1 ||
		lines[1].By != "usr_root" || lines[1].Text != "/lean, keep the tests" || lines[1].At != ack.At {
		t.Fatalf("the reply line: %+v", lines)
	}
	if dl := lines[2]; dl.Kind != capsule.AskKindDelivered || dl.MsgAt != ack.At || dl.Spawn != 1 || dl.Step != "fix" || dl.Text != "" {
		t.Fatalf("the delivered line: %+v", dl)
	}
	if open := capsule.OpenAsk(lines, "fix", 1); open != nil {
		t.Fatalf("the ask still reads open: %+v", open)
	}
	// the events, in order: owner_message (the words verbatim) then
	// delivered (the proof)
	om, dv := log.byKind(EvOwnerMessage), log.byKind(EvDelivered)
	if len(om) != 1 || om[0].body["kind"] != capsule.AskKindReply || om[0].body["ask_at"] != ask.At || om[0].body["text"] != "/lean, keep the tests" ||
		om[0].body["by"] != "usr_root" || om[0].body["at"] != ack.At || om[0].body["spawn_n"] != int64(1) {
		t.Fatalf("owner_message: %+v", om)
	}
	if len(dv) != 1 || dv[0].body["msg_at"] != ack.At || dv[0].body["spawn_n"] != int64(1) || dv[0].body["disposition"] != spawn.DispositionStarted ||
		dv[0].body["confirmed_by"] != confirmedUser || dv[0].body["command_id"] != "pf-msg-1-1" {
		t.Fatalf("delivered: %+v", dv)
	}
	if !eventBefore(log, EvOwnerMessage, EvDelivered) {
		t.Fatal("the owner_message event must precede the delivered event")
	}
	// the delivery: the framed text keyed by the line's at
	run := sp.runs[0]
	if sent := run.messages(); len(sent) != 1 || sent[0].Key != ack.At || !strings.Contains(sent[0].Text, "(ask "+ask.At+")") || !strings.HasSuffix(sent[0].Text, "\n/lean, keep the tests") {
		t.Fatalf("the run got: %+v", sent)
	}
	// the state: running again — the slot taken back, the supervisor told
	// (waiting false), the process not torn down
	if w, ok := ow.last(); !ok || w {
		t.Fatalf("the slot was not taken back: %v", ow.calls)
	}
	waitUntil(t, "the step_owner_wait false fact", func() bool {
		for _, f := range link.Facts.Drain() {
			if ow, ok := f.(interface{ FactKind() string }); ok && ow.FactKind() == "step_owner_wait" {
				return true
			}
		}
		return false
	})
	if run.terminated() {
		t.Fatal("the live process was torn down by the delivery")
	}
	// the run delivers its result: accepted under dispatch 1
	close(run.rel)
	waitUntil(t, "the step to complete", func() bool { return stepDone(log, "fix") })
	exec := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
	if st := exec.Steps["fix"]; st.Status != capsule.StepDone || len(st.Attempts) != 1 || st.Attempts[0].Cause != "accepted" {
		t.Fatalf("the step: %+v", st)
	}
	waitUntil(t, "the review dispatch", func() bool { return len(sp.dispatched()) == 2 })
	if got := sp.dispatched(); got[0] != "fix@1(resume=false)" || !strings.HasPrefix(got[1], "review@") {
		t.Fatalf("dispatches: %v (fix must not be re-dispatched)", got)
	}
	cancel()
	<-errCh
}

// An interjection while the step runs (no ask open): recorded as a
// message naming no ask, taken by the running turn as queued, delivered
// only once the harness hands it to the model (the started lifecycle the
// test feeds), and the step is never marked awaiting.
func TestRelayInterjectionIsQueuedThenDelivered(t *testing.T) {
	dir := t.TempDir()
	sp := &fakeSpawner{script: []fakeEntry{{hold: true, res: relayCompleted(), disposition: spawn.DispositionQueued}, done(`{}`)}}
	d, log, ow, _ := askEnv(t, dir, sp, nil, 10*time.Second)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	waitUntil(t, "the dispatch to start", func() bool { return len(sp.dispatched()) == 1 })
	run := sp.runs[0]
	go func() {
		// the harness hands the queued message to the model at the turn's
		// boundary: the user message carrying the marker
		waitUntil(t, "the message to be sent", func() bool { return len(run.messages()) == 1 })
		time.Sleep(50 * time.Millisecond)
		run.watch.feed(piUserMessage(run.messages()[0]))
	}()
	ack, err := d.OwnerMessage(ctx, "also run the e2e", "usr_root")
	if err != nil {
		t.Fatalf("OwnerMessage: %v", err)
	}
	if ack.Kind != capsule.AskKindMessage || ack.AskAt != "" || ack.Disposition != spawn.DispositionQueued || !ack.Delivered {
		t.Fatalf("the ack: %+v", ack)
	}
	if dv := log.byKind(EvDelivered); len(dv) != 1 || dv[0].body["disposition"] != spawn.DispositionQueued || dv[0].body["confirmed_by"] != confirmedUser || dv[0].body["kind"] != capsule.AskKindMessage {
		t.Fatalf("delivered: %+v", dv)
	}
	if len(log.byKind(EvAwaitingOwner)) != 0 {
		t.Fatal("an interjection parks nothing")
	}
	for _, w := range ow.calls {
		if w {
			t.Fatalf("the slot was released on an interjection: %v", ow.calls)
		}
	}
	if sent := run.messages(); len(sent) != 1 || !strings.HasPrefix(sent[0].Text, "Owner message from usr_root:\nalso run the e2e") {
		t.Fatalf("the run got: %+v", sent)
	}
	close(run.rel)
	waitUntil(t, "the step to complete", func() bool { return stepDone(log, "fix") })
	cancel()
	<-errCh
}

// A reply after the warm window: the process is gone and the step parked
// on its ask; the owner's message is recorded (reply) and announced, no
// live process takes it (disposition resume), the plan pass re-dispatches
// the step as a resume of its conversation whose first message carries
// the message alone (MessageResume), the new dispatch marks it delivered
// once its first turn starts — under the NEW spawn ordinal — and the
// envelope that dispatch delivers passes the spawn fence as spawn 2.
func TestRelayResumesTheSessionAfterTheWarmWindow(t *testing.T) {
	dir := t.TempDir()
	sp := &fakeSpawner{script: []fakeEntry{{hold: true}, {hold: true, res: relayCompleted()}, done(`{}`)}}
	d, log, ow, _ := askEnv(t, dir, sp, nil, 100*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	waitUntil(t, "the dispatch to start", func() bool { return len(sp.dispatched()) == 1 })
	ask := writeAsk(t, dir, "fix", 1, "lean or compatible?")
	waitUntil(t, "the warm window to end the process", func() bool { return sp.runs[0].terminated() })
	time.Sleep(50 * time.Millisecond)
	if got := sp.dispatched(); len(got) != 1 {
		t.Fatalf("re-dispatched before any reply: %v", got)
	}

	ack, err := d.OwnerMessage(ctx, "lean", "usr_root")
	if err != nil {
		t.Fatalf("OwnerMessage: %v", err)
	}
	if ack.Kind != capsule.AskKindReply || ack.AskAt != ask.At || ack.Delivered || ack.Disposition != dispositionResume {
		t.Fatalf("the ack: %+v", ack)
	}
	waitUntil(t, "the resume dispatch", func() bool { return len(sp.dispatched()) == 2 })
	sp2 := sp.spawns[1]
	if sp2.StepID != "fix" || sp2.SpawnN != 2 || !sp2.Resume || !sp2.MessageResume || len(sp2.OwnerMessages) != 1 || sp2.OwnerMessages[0].At != ack.At {
		t.Fatalf("the resume dispatch: step=%s spawn=%d resume=%v message_resume=%v messages=%+v", sp2.StepID, sp2.SpawnN, sp2.Resume, sp2.MessageResume, sp2.OwnerMessages)
	}
	if w, ok := ow.last(); !ok || w {
		t.Fatalf("the slot was not taken back for the resume: %v", ow.calls)
	}
	if n := len(log.byKind(EvDelivered)); n != 0 {
		t.Fatalf("delivered before the resumed session's first turn: %d", n)
	}
	// the first turn of the resumed session: the prompt was taken
	run := sp.runs[1]
	run.watch.feed(`{"type":"agent_start"}`)
	waitUntil(t, "the delivered event", func() bool { return len(log.byKind(EvDelivered)) == 1 })
	dv := log.byKind(EvDelivered)[0].body
	if dv["msg_at"] != ack.At || dv["spawn_n"] != int64(2) || dv["disposition"] != dispositionPrompt || dv["confirmed_by"] != confirmedTurn {
		t.Fatalf("delivered: %v", dv)
	}
	lines := mustAsks(t, dir)
	if last := lines[len(lines)-1]; last.Kind != capsule.AskKindDelivered || last.MsgAt != ack.At || last.Spawn != 2 {
		t.Fatalf("the delivered line: %+v", last)
	}
	if u := capsule.Undelivered(lines, "fix"); len(u) != 0 {
		t.Fatalf("still undelivered: %+v", u)
	}
	// the envelope of the resumed dispatch carries spawn 2: accepted
	close(run.rel)
	waitUntil(t, "the step to complete", func() bool { return stepDone(log, "fix") })
	exec := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
	if st := exec.Steps["fix"]; st.Status != capsule.StepDone || len(st.Attempts) != 1 || st.Attempts[0].Cause != "accepted" {
		t.Fatalf("the step (no death, no interruption archived): %+v", st)
	}
	if ev := log.byKind("step_done"); ev[0].body["spawn_n"] != int64(2) {
		t.Fatalf("the accepted result's dispatch: %v", ev[0].body)
	}
	cancel()
	<-errCh
}

// A delivery proof that lands in the same poll window as the result
// envelope: the running turn took a queued interjection, handed it to the
// model (its marker is in the stream, the watch's notify not yet taken, no
// ask tick due) and delivered its result. The dispatch's last look marks
// it delivered under that dispatch — left undelivered, the line would be
// replayed into the step's next dispatch as a duplicate (PR 6d review).
func TestRelayConfirmsAProofThatRacedTheResult(t *testing.T) {
	dir := t.TempDir()
	sp := &fakeSpawner{script: []fakeEntry{{hold: true, res: relayCompleted(), disposition: spawn.DispositionQueued}, done(`{}`)}}
	d, log, _, _ := askEnv(t, dir, sp, nil, 10*time.Second)
	d.cfg.AskPoll = time.Hour // no ask tick confirms anything here
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	waitUntil(t, "the dispatch to start", func() bool { return len(sp.dispatched()) == 1 })
	run := sp.runs[0]
	delivered := make(chan bool, 1)
	go func() {
		ack, _ := d.OwnerMessage(ctx, "also run the e2e", "usr_root")
		delivered <- ack.Delivered
	}()
	waitUntil(t, "the message to be sent", func() bool { return len(run.messages()) == 1 })
	// a barrier: the wait loop takes the next request only once relayTake
	// (and its own confirm) returned; a request for another step is dropped
	d.relay <- &relayRequest{line: capsule.AskLine{Step: "other"}, taken: make(chan relayTaken, 1), confirmed: make(chan struct{})}
	key := run.messages()[0].Key
	// the marker reached the stream; its notify is not consumed
	run.watch.mu.Lock()
	run.watch.markers[key] = true
	run.watch.mu.Unlock()
	close(run.rel)
	waitUntil(t, "the step to complete", func() bool { return stepDone(log, "fix") })
	if u := capsule.Undelivered(mustAsks(t, dir), "fix"); len(u) != 0 {
		t.Fatalf("the message whose proof raced the result stays undelivered (a replay would duplicate it): %+v", u)
	}
	dv := log.byKind(EvDelivered)
	if len(dv) != 1 || dv[0].body["msg_at"] != key || dv[0].body["spawn_n"] != int64(1) || dv[0].body["confirmed_by"] != confirmedUser {
		t.Fatalf("delivered: %+v", dv)
	}
	select {
	case ok := <-delivered:
		if !ok {
			t.Fatal("the caller was answered delivered=false")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("OwnerMessage never returned")
	}
	cancel()
	<-errCh
}

// A reply the live, awaiting process does not take (a refusal, no
// acknowledgement within relayAckBound): the line is recorded and closes
// the ask, so the warm wait ends at once — the idle process is torn down
// and the resume dispatch carries the reply now, not when the warm window
// runs out (an hour here; 15 minutes by default).
func TestRelaySendFailureWhileAwaitingResumesAtOnce(t *testing.T) {
	dir := t.TempDir()
	sp := &fakeSpawner{script: []fakeEntry{{hold: true, refuse: errors.New("harness wedged")}, {hold: true, res: relayCompleted()}, done(`{}`)}}
	d, log, _, _ := askEnv(t, dir, sp, nil, time.Hour)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 1)
	go func() { errCh <- d.Run(ctx) }()
	waitUntil(t, "the dispatch to start", func() bool { return len(sp.dispatched()) == 1 })
	ask := writeAsk(t, dir, "fix", 1, "lean or compatible?")
	waitUntil(t, "the awaiting_owner event", func() bool { return len(log.byKind(EvAwaitingOwner)) == 1 })

	ack, err := d.OwnerMessage(ctx, "lean", "usr_root")
	if err == nil || !strings.Contains(err.Error(), "harness wedged") {
		t.Fatalf("the refusal is not surfaced: %v", err)
	}
	if ack.Kind != capsule.AskKindReply || ack.AskAt != ask.At || ack.Delivered || ack.Disposition != dispositionResume {
		t.Fatalf("the ack: %+v", ack)
	}
	waitUntil(t, "the resume dispatch (not after the warm window)", func() bool { return len(sp.dispatched()) == 2 })
	if !sp.runs[0].terminated() {
		t.Fatal("the idle process that refused the reply was not torn down")
	}
	sp2 := sp.spawnAt(1)
	if sp2.StepID != "fix" || sp2.SpawnN != 2 || !sp2.Resume || !sp2.MessageResume || len(sp2.OwnerMessages) != 1 || sp2.OwnerMessages[0].At != ack.At {
		t.Fatalf("the resume dispatch: step=%s spawn=%d resume=%v message_resume=%v messages=%+v", sp2.StepID, sp2.SpawnN, sp2.Resume, sp2.MessageResume, sp2.OwnerMessages)
	}
	run := sp.runs[1]
	run.watch.feed(`{"type":"agent_start"}`)
	waitUntil(t, "the delivered event", func() bool { return len(log.byKind(EvDelivered)) == 1 })
	if dv := log.byKind(EvDelivered)[0].body; dv["msg_at"] != ack.At || dv["spawn_n"] != int64(2) || dv["confirmed_by"] != confirmedTurn {
		t.Fatalf("delivered: %v", dv)
	}
	close(run.rel)
	waitUntil(t, "the step to complete", func() bool { return stepDone(log, "fix") })
	cancel()
	<-errCh
}

// The two prompt forms: a resume of the conversation that ended awaiting
// the owner sends the framed messages alone — byte-for-byte what a live
// delivery would have sent, nothing else; every other dispatch carries
// them as the `# Owner messages` section of the full prompt, and a prompt
// without them has no such section.
func TestOwnerMessagesPromptForms(t *testing.T) {
	msgs := []capsule.AskLine{
		{At: "2026-11-10T21:00:00.000000001Z", Step: "fix", Spawn: 1, Kind: capsule.AskKindReply, AskAt: "2026-11-10T20:59:00Z", Text: "lean", By: "usr_root"},
		{At: "2026-11-10T21:00:05Z", Step: "fix", Spawn: 1, Kind: capsule.AskKindMessage, Text: "and keep the tests"},
	}
	short := assembleStepPrompt(promptParts{WIID: "wi", StepID: "fix", SpawnN: 2, CapsuleRoot: "/c", Definition: "do", OwnerMessages: msgs, ShortMessageResume: true})
	want := spawn.MessageText(ownerMessageText(msgs[0])) + "\n\n" + spawn.MessageText(ownerMessageText(msgs[1]))
	if short != want {
		t.Fatalf("the short form:\n%s\nwant:\n%s", short, want)
	}
	full := assembleStepPrompt(promptParts{WIID: "wi", StepID: "fix", SpawnN: 2, CapsuleRoot: "/c", Definition: "do", OwnerMessages: msgs})
	if !strings.Contains(full, "# Owner messages") || !strings.Contains(full, spawn.MessageMarker(msgs[0].At)) || !strings.Contains(full, spawn.MessageMarker(msgs[1].At)) || !strings.Contains(full, "# Step definition") {
		t.Fatalf("the full form lacks the section or the definition:\n%s", full)
	}
	if none := assembleStepPrompt(promptParts{WIID: "wi", StepID: "fix", SpawnN: 1, CapsuleRoot: "/c", Definition: "do"}); strings.Contains(none, "Owner messages") || strings.Contains(none, "[pf-msg:") {
		t.Fatal("a prompt without owner messages carries the section")
	}
}

// The refusals, nothing written: no step in progress (before any
// dispatch), empty words, words over the bound; a v1 question pending
// names its answer path.
func TestOwnerMessageRefusalsWriteNothing(t *testing.T) {
	dir := t.TempDir()
	sp := &fakeSpawner{}
	d, _, _, _ := askEnv(t, dir, sp, nil, time.Second)
	ctx := context.Background()
	for _, text := range []string{"", "   ", strings.Repeat("x", capsule.MaxOwnerMessageBytes+1), "hello"} {
		_, err := d.OwnerMessage(ctx, text, "usr_root")
		if err == nil {
			t.Fatalf("accepted %d bytes with no step agent", len(text))
		}
		// the words' own shape is ErrInvalidOwnerMessage (DAEMON-400), the
		// state refusal is not (DAEMON-409)
		if invalid := text != "hello"; errors.Is(err, ErrInvalidOwnerMessage) != invalid {
			t.Fatalf("%d bytes: ErrInvalidOwnerMessage=%v, want %v (%v)", len(text), errors.Is(err, ErrInvalidOwnerMessage), invalid, err)
		}
	}
	if lines := mustAsks(t, dir); len(lines) != 0 {
		t.Fatalf("a refusal wrote: %+v", lines)
	}
	// an in_progress step between dispatches: no agent to talk to
	d.mu.Lock()
	d.exec.WorkflowStatus = StatusRunning
	d.exec.CurrentStep = "fix"
	d.exec.Steps["fix"] = &capsule.Step{Status: capsule.StepInProgress}
	d.mu.Unlock()
	if _, err := d.OwnerMessage(ctx, "hello", "usr_root"); err == nil || !strings.Contains(err.Error(), "no step agent") {
		t.Fatalf("a step between dispatches: %v", err)
	}
	if lines := mustAsks(t, dir); len(lines) != 0 {
		t.Fatalf("a refusal wrote: %+v", lines)
	}
	d.mu.Lock()
	d.exec.Steps["fix"] = &capsule.Step{Status: capsule.StepInProgress, Dispatch: 1}
	d.exec.WorkflowStatus = StatusCompleted
	d.mu.Unlock()
	if _, err := d.OwnerMessage(ctx, "hello", "usr_root"); err == nil || !strings.Contains(err.Error(), "completed") {
		t.Fatalf("a terminal workflow: %v", err)
	}
	if lines := mustAsks(t, dir); len(lines) != 0 {
		t.Fatalf("a refusal wrote: %+v", lines)
	}
}

// relayCompleted is a minimal completed envelope the scripted run
// delivers (the spawn fence fields come from the dispatch).
func relayCompleted() *StepResult {
	return &StepResult{Handoff: handoffOf(stepcall.StatusDone, "")}
}

// eventBefore reports whether the first event of kind a was logged
// before the first of kind b.
func eventBefore(log *safeLog, a, b string) bool {
	log.mu.Lock()
	defer log.mu.Unlock()
	ia, ib := -1, -1
	for i, e := range log.entries {
		if e.kind == a && ia < 0 {
			ia = i
		}
		if e.kind == b && ib < 0 {
			ib = i
		}
	}
	return ia >= 0 && ib >= 0 && ia < ib
}

// stepDone reports whether the step's own step_done event was logged. A
// count of step_done events is no such wait: the lane's next step is
// dispatched and answered at once (the scripted review), so the count is
// 1 only for the instant between the two steps' events — a poll that
// sleeps past that instant sees 2 for good and waits out its deadline
// over a run that completed.
func stepDone(log *safeLog, step string) bool {
	for _, e := range log.byKind("step_done") {
		if e.body["step"] == step {
			return true
		}
	}
	return false
}
