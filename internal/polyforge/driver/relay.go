package driver

// relay.go: the owner-message relay (workflow contract v2, design §2.2 and
// §5.2, orchestration O-9; PR 6d). The owner talks to the CURRENT step's
// agent at any time — `pf answer <wi> --text "<words>"` or the job card's
// input box, no question id — and the daemon carries the words into that
// agent's conversation without reading them (P0):
//
//  1. the message is appended to the capsule's runtime-state/asks.jsonl
//     first — `{at, step, spawn, kind: "reply", ask_at, text, by}` while
//     an ask of the current dispatch is open, `kind: "message"` otherwise
//     (a live interjection) — and the `owner_message` event is emitted;
//     the record is the authority, the delivery only follows it;
//  2. the dispatch's process is alive (idle after its ask, or mid-turn):
//     the message goes in through the harness's own message channel
//     (spawn.Messenger: claude's stream-json user message, pi's `prompt`
//     with `steer`), framed by ownerMessageText with the message's `at`
//     as its delivery key, so the harness embeds `[pf-msg:<at>]` in what
//     the model reads. A step that was awaiting the owner leaves that
//     state at acceptance: the warm window stops, the running slot is
//     taken back (Config.OwnerWait), the supervisor judges again
//     (StepOwnerWait false: the wait is declared an observation gap, so
//     neither silence nor T is charged for it);
//  3. the process is gone (the warm window ran out, a crash, a daemon
//     restart): the plan pass re-dispatches the step as a RESUME of its
//     conversation (claude `--resume`, pi `--session`) whose first message
//     is the undelivered owner messages rendered by the same framing —
//     a new spawn_n and identity, which the result envelope then carries
//     (lifecycle (iii)); a resume that degrades to a new conversation
//     carries them as a `# Owner messages` section of the full prompt;
//  4. a message counts as DELIVERED only when its marker is observed in
//     the harness's stream (deliveryWatch: claude — the `command_lifecycle`
//     `started` record of the message's uuid; pi — the user message
//     carrying the marker; a resume prompt — the dispatch's first turn
//     starting), never by the harness's "queued" acknowledgement: then
//     the record gets `{at, step, spawn, kind: "delivered", msg_at}` and
//     the `delivered` event. A restart replays exactly the reply and
//     message lines no delivered line names (capsule.Undelivered).
//
// The daemon never judges whether the owner's words answered the agent's
// question: a reply closes the ask line (the step is no longer waiting),
// and the agent either goes on or asks again (`pf ask`) in its next turn.
// It is the owner's only way to speak to a step: the v1 `needs_input`
// question's answer path (its question id, option selection and reserved
// `cancel`) left with workflow contract v2 change 19.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/polyforge/crashpoint"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// The relay's driver events.
const (
	// EvOwnerMessage: an owner message was recorded for the current step —
	// `{at, step, spawn_n, kind, ask_at?, by, text}` (the words verbatim).
	EvOwnerMessage = "owner_message"
	// EvDelivered: an owner message reached the step's conversation —
	// `{at, msg_at, step, spawn_n, kind, disposition, confirmed_by,
	// command_id?}`: `disposition` the harness's word at acceptance
	// (started / queued) or `prompt` when a resumed session's first
	// message carried it; `confirmed_by` what proved it (lifecycle_started
	// / user_message / turn_started).
	EvDelivered = "delivered"
)

// Delivery dispositions and confirmations the relay records beside the
// harness's own words (spawn.DispositionStarted / Queued).
const (
	dispositionPrompt = "prompt"

	confirmedLifecycle = "lifecycle_started"
	confirmedUser      = "user_message"
	confirmedTurn      = "turn_started"
)

// The relay's bounds: how long the harness may take to acknowledge a
// message (the acceptance; a running tool call delays claude's
// acknowledgement by nothing — it queues at once), how long a caller
// waits for a live dispatch to take its request (a dispatch busy with an
// earlier message's acknowledgement), and how long a caller waits for the
// delivery confirmation before answering "accepted, confirmation pending".
const (
	relayAckBound     = 15 * time.Second
	relayTakeBound    = 20 * time.Second
	relayConfirmBound = 3 * time.Second
)

// dispositionResume: no live process; the plan pass resumes the session.
const dispositionResume = "resume"

// ErrInvalidOwnerMessage marks an owner message refused for its own
// shape — empty words, or over capsule.MaxOwnerMessageBytes — as opposed
// to the state refusals (no step agent to talk to): the daemon answers the
// first DAEMON-400, the rest DAEMON-409.
var ErrInvalidOwnerMessage = errors.New("invalid owner message")

// ErrParked marks an owner message the live driver refused because its
// workflow is parked blocked: nothing was written, and the daemon takes the
// message through the park's wake (wake.go) instead.
var ErrParked = errors.New("the workflow is parked")

// ValidateOwnerMessage refuses an owner message for its own shape: empty
// words, or over capsule.MaxOwnerMessageBytes (ErrInvalidOwnerMessage).
func ValidateOwnerMessage(text string) error {
	if strings.TrimSpace(text) == "" {
		return fmt.Errorf("driver: %w: an owner message needs text", ErrInvalidOwnerMessage)
	}
	if len(text) > capsule.MaxOwnerMessageBytes {
		return fmt.Errorf("driver: %w: an owner message is at most %d bytes (got %d)", ErrInvalidOwnerMessage, capsule.MaxOwnerMessageBytes, len(text))
	}
	return nil
}

// OwnerMessageLine answers the asks.jsonl line of one owner message to
// dispatch n of step, stamped at, as the capsule's record reads now: a
// reply naming the ask when an ask of that dispatch is open, a message
// otherwise. by "" records "owner". Nothing is written.
func OwnerMessageLine(capsuleDir, step string, n int64, text, by, at string) (capsule.AskLine, error) {
	if by == "" {
		by = "owner"
	}
	lines, err := capsule.ReadAsks(capsuleDir)
	if err != nil {
		return capsule.AskLine{}, fmt.Errorf("driver: the ask record is unreadable: %w", err)
	}
	line := capsule.AskLine{At: at, Step: step, Spawn: n, Kind: capsule.AskKindMessage, Text: text, By: by}
	if open := capsule.OpenAsk(lines, step, n); open != nil {
		line.Kind, line.AskAt = capsule.AskKindReply, open.At
	}
	return line, nil
}

// RecordOwnerMessage appends OwnerMessageLine's line to the capsule's
// asks.jsonl. The record is the authority; every delivery follows it.
func RecordOwnerMessage(capsuleDir, step string, n int64, text, by, at string) (capsule.AskLine, error) {
	line, err := OwnerMessageLine(capsuleDir, step, n, text, by, at)
	if err != nil {
		return capsule.AskLine{}, err
	}
	if err := capsule.AppendAsk(capsuleDir, line); err != nil {
		return capsule.AskLine{}, fmt.Errorf("driver: the owner message was not recorded (nothing delivered): %w", err)
	}
	return line, nil
}

// OwnerMessageEvent is the body of the owner_message event of a recorded
// line (the words verbatim).
func OwnerMessageEvent(line capsule.AskLine) map[string]any {
	body := map[string]any{"at": line.At, "step": line.Step, "spawn_n": line.Spawn, "kind": line.Kind, "by": line.By, "text": line.Text}
	if line.AskAt != "" {
		body["ask_at"] = line.AskAt
	}
	return body
}

// OwnerMessage relays one owner message to the current step's agent: the
// record line and the event first, then the delivery (the file header).
// Refused, with nothing written, when no step agent can be talked to: the
// workflow is terminal, or no step is in progress under a dispatch
// (between dispatches); a blocked workflow is refused as ErrParked (the
// daemon wakes it instead, wake.go). The text is the owner's, verbatim;
// empty or over capsule.MaxOwnerMessageBytes is refused as
// ErrInvalidOwnerMessage.
func (d *Driver) OwnerMessage(ctx context.Context, text, by string) (protocol.OwnerMessageAck, error) {
	if err := ValidateOwnerMessage(text); err != nil {
		return protocol.OwnerMessageAck{}, err
	}
	if d.wi.CapsuleDir == "" {
		return protocol.OwnerMessageAck{}, errors.New("driver: no capsule to record the owner message in")
	}
	d.mu.Lock()
	if d.failed != nil {
		err := d.failed
		d.mu.Unlock()
		return protocol.OwnerMessageAck{}, fmt.Errorf("driver: the driver failed; no step agent to talk to: %w", err)
	}
	status := d.exec.WorkflowStatus
	cur := d.exec.CurrentStep
	st := d.exec.Steps[cur]
	d.mu.Unlock()
	if status == StatusBlocked {
		return protocol.OwnerMessageAck{}, fmt.Errorf("driver: %w (step %q): the message wakes it through the park's door", ErrParked, cur)
	}
	if isTerminalStatus(status) {
		return protocol.OwnerMessageAck{}, fmt.Errorf("driver: the workflow is %s; no step agent to talk to", status)
	}
	if cur == "" || st == nil || st.Status != capsule.StepInProgress || st.Dispatch == 0 {
		return protocol.OwnerMessageAck{}, fmt.Errorf("driver: no step agent is running or waiting for the owner (step %q)", cur)
	}
	step, n := cur, st.Dispatch
	line, err := RecordOwnerMessage(d.wi.CapsuleDir, step, n, text, by, d.stamp())
	if err != nil {
		return protocol.OwnerMessageAck{}, err
	}
	d.emit(EvOwnerMessage, OwnerMessageEvent(line))
	crashpoint.At("driver.owner_message:" + step)
	res := protocol.OwnerMessageAck{Status: "accepted", At: line.At, Kind: line.Kind, AskAt: line.AskAt, Step: step, SpawnN: n}

	// the delivery: a live dispatch takes the request itself (its wait
	// loop owns the process); none alive — the warm window ran out, the
	// driver is between dispatches — and the plan pass resumes the session
	// with the message in its first prompt
	d.mu.Lock()
	live := d.liveStep == step && d.liveSpawn > 0
	d.mu.Unlock()
	if !live {
		res.Disposition = dispositionResume
		d.mu.Lock()
		d.wakeLoopLocked()
		d.mu.Unlock()
		return res, nil
	}
	req := &relayRequest{line: line, taken: make(chan relayTaken, 1), confirmed: make(chan struct{})}
	take := time.NewTimer(relayTakeBound)
	defer take.Stop()
	select {
	case d.relay <- req:
	case <-take.C:
		res.Disposition = dispositionResume // the dispatch did not take it: the next pass carries it
		return res, nil
	case <-ctx.Done():
		return res, ctx.Err()
	}
	var t relayTaken
	select {
	case t = <-req.taken:
	case <-take.C:
		res.Disposition = dispositionResume
		return res, nil
	case <-ctx.Done():
		return res, ctx.Err()
	}
	if t.err != nil {
		// recorded, not delivered: the step's next dispatch carries it
		res.Disposition = dispositionResume
		return res, fmt.Errorf("driver: the step process did not take the message (it is recorded; the next dispatch carries it): %w", t.err)
	}
	if t.dropped {
		res.Disposition = dispositionResume
		return res, nil
	}
	res.Disposition = t.delivery.Disposition
	confirm := time.NewTimer(relayConfirmBound)
	defer confirm.Stop()
	select {
	case <-req.confirmed:
		res.Delivered = true
	case <-confirm.C:
	case <-ctx.Done():
	}
	return res, nil
}

// relayRequest is one owner message handed to the live dispatch's wait
// loop (awaitDispatch takes it from Driver.relay).
type relayRequest struct {
	line capsule.AskLine
	// taken answers once: how the harness took the message, or that the
	// request was dropped (the line is not this dispatch's, or is
	// delivered already), or the error the harness refused it with.
	taken chan relayTaken
	// confirmed is closed once the marker was observed and the delivered
	// line is on the record.
	confirmed chan struct{}
}

type relayTaken struct {
	delivery spawn.Delivery
	dropped  bool
	err      error
}

func (r *relayRequest) answer(t relayTaken) {
	select {
	case r.taken <- t:
	default:
	}
}

// relayRun is the face of a StepRun whose process takes owner messages
// (the production stepRun): the harness's message channel, the dispatch's
// delivery watch over its stdout stream, and the owner messages the
// dispatch's first prompt carried.
type relayRun interface {
	SendMessage(ctx context.Context, m spawn.Message) (spawn.Delivery, error)
	deliveryWatch() *deliveryWatch
	carriedMessages() []capsule.AskLine
}

// ownerMessageText frames one recorded owner message for delivery: a
// neutral first line naming who speaks and, for a reply, the ask it
// answers by its stamp, then the owner's words verbatim; spawn.MessageText
// appends the delivery marker `[pf-msg:<at>]` on its own trailing line.
// The first line never starts with `/`: pi reads a prompt starting with
// `/` as an extension command (`agent-session.js`: an unknown one is
// refused on the steer path), and claude's `--disable-slash-commands`
// aside, an owner's `/foo` must reach the model as words.
func ownerMessageText(l capsule.AskLine) spawn.Message {
	head := "Owner message"
	if l.Kind == capsule.AskKindReply {
		head = "Owner reply to your question (ask " + l.AskAt + ")"
	}
	if l.By != "" {
		head += " from " + l.By
	}
	return spawn.Message{Key: l.At, Text: head + ":\n" + l.Text}
}

// renderOwnerMessages renders the undelivered owner messages a resumed
// session's first message carries (the short form: exactly what a live
// delivery would have sent, one framed message after another).
func renderOwnerMessages(lines []capsule.AskLine) string {
	var parts []string
	for _, l := range lines {
		parts = append(parts, spawn.MessageText(ownerMessageText(l)))
	}
	return strings.Join(parts, "\n\n")
}

// ownerMessagesSection renders the `# Owner messages` section of a full
// step prompt that carries undelivered owner messages (a dispatch that
// does not resume the conversation they were written to).
func ownerMessagesSection(lines []capsule.AskLine) string {
	if len(lines) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\n# Owner messages\n\n")
	b.WriteString("The owner wrote these to this step's agent while the step ran, in order; a reply names the question it answers by that question's stamp. ")
	b.WriteString("Each is verbatim; the bracketed `[pf-msg:…]` line after a message is the daemon's delivery marker, not the owner's words.\n\n")
	b.WriteString(renderOwnerMessages(lines))
	b.WriteString("\n")
	return b.String()
}

// undeliveredMessages answers the current step's owner messages no
// delivered line names (capsule.Undelivered): what the step's next
// dispatch carries. Unreadable record: none (the next read tries again).
func (d *Driver) undeliveredMessages(stepID string) []capsule.AskLine {
	if d.wi.CapsuleDir == "" {
		return nil
	}
	lines, err := capsule.ReadAsks(d.wi.CapsuleDir)
	if err != nil {
		return nil
	}
	return capsule.Undelivered(lines, stepID)
}

// deliveredMessage reports whether a delivered line names the message.
func (d *Driver) deliveredMessage(msgAt string) bool {
	lines, err := capsule.ReadAsks(d.wi.CapsuleDir)
	if err != nil {
		return false
	}
	for _, l := range lines {
		if l.Kind == capsule.AskKindDelivered && l.MsgAt == msgAt {
			return true
		}
	}
	return false
}

// markDelivered records that the message reached dispatch n's
// conversation: the delivered line (the record first), then the
// `delivered` event. A failed append marks nothing — the message is
// replayed by the next dispatch, which is the safe side.
func (d *Driver) markDelivered(line capsule.AskLine, n int64, disposition, confirmedBy, commandID string) bool {
	if err := capsule.AppendAsk(d.wi.CapsuleDir, capsule.AskLine{
		At: d.stamp(), Step: line.Step, Spawn: n, Kind: capsule.AskKindDelivered, MsgAt: line.At,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "driver: %s: delivered line not appended (the message is replayed by the next dispatch): %v\n", d.wi.ID, err)
		return false
	}
	body := map[string]any{
		"at": line.At, "msg_at": line.At, "step": line.Step, "spawn_n": n, "kind": line.Kind,
		"disposition": disposition, "confirmed_by": confirmedBy,
	}
	if commandID != "" {
		body["command_id"] = commandID
	}
	d.emit(EvDelivered, body)
	return true
}

// noteMessageResume records that dispatch n of the step ended awaiting
// the owner with its process gone: the step's next dispatch, when it
// resumes that conversation, sends only the owner messages (the
// conversation already holds the prompt and the question).
func (d *Driver) noteMessageResume(stepID string) {
	d.mu.Lock()
	d.messageResume = stepID
	d.mu.Unlock()
}

// takeMessageResume answers and clears the note for the step about to
// be dispatched — or, with no note (a daemon restart, the re-claim of a
// parked work item: the note lives in this driver only), the same fact
// derived from the capsule's records: the step's newest conversation ended
// with its agent waiting for the owner and the owner's words not yet in
// it (endedAwaitingOwner, wake.go).
func (d *Driver) takeMessageResume(stepID string) bool {
	d.mu.Lock()
	ok := d.messageResume == stepID
	d.messageResume = ""
	d.mu.Unlock()
	if ok {
		return true
	}
	return endedAwaitingOwner(d.wi.CapsuleDir, stepID, d.undeliveredMessages(stepID))
}

// setLive records (or clears, n = 0) the dispatch whose wait loop takes
// relay requests.
func (d *Driver) setLive(stepID string, n int64) {
	d.mu.Lock()
	d.liveStep, d.liveSpawn = stepID, n
	d.mu.Unlock()
}

// ---------------------------------------------------------------------------
// The dispatch's relay state (owned by awaitDispatch).

// pendingDelivery is a message the harness accepted whose marker is not
// yet observed.
type pendingDelivery struct {
	line     capsule.AskLine
	delivery spawn.Delivery
	req      *relayRequest // nil for a prompt-carried message
}

// relayState is one dispatch's relay bookkeeping: the run's message
// channel and watch, the messages its prompt carried (confirmed by the
// first turn), and the live deliveries awaiting their marker.
type relayState struct {
	run     relayRun
	stepID  string
	n       int64
	carried []capsule.AskLine
	pending []pendingDelivery
}

// newRelayState opens the dispatch's relay over run (nil run face: no
// messages, the prompt-carried ones included, are confirmed here).
func (d *Driver) newRelayState(run StepRun, stepID string, n int64) *relayState {
	rl := &relayState{stepID: stepID, n: n}
	if rr, ok := run.(relayRun); ok {
		rl.run = rr
		rl.carried = rr.carriedMessages()
	}
	d.setLive(stepID, n)
	return rl
}

// notify answers the watch's change channel (nil without a watch).
func (rl *relayState) notify() <-chan struct{} {
	if rl.run == nil {
		return nil
	}
	if w := rl.run.deliveryWatch(); w != nil {
		return w.notify
	}
	return nil
}

// carries reports whether the dispatch's prompt carried the message.
func (rl *relayState) carries(msgAt string) bool {
	for _, l := range rl.carried {
		if l.At == msgAt {
			return true
		}
	}
	return false
}

// take delivers one relay request into the live process. A request that
// is not this step's, that the prompt already carried, or that a
// delivered line already names is dropped (answered dropped). A step
// awaiting the owner leaves that state when the harness accepts the
// message: the warm window stops (leaveAwaiting), the slot is taken back
// and the supervisor judges again. It answers false when the live
// process did not take the message (no message channel, a refusal, no
// acknowledgement within relayAckBound): the line is recorded and the
// step's next dispatch carries it — awaitDispatch ends an awaiting
// dispatch's warm wait at once, so that dispatch comes now.
func (d *Driver) relayTake(ctx context.Context, rl *relayState, req *relayRequest, awaiting bool, leaveAwaiting func()) bool {
	line := req.line
	if line.Step != rl.stepID || rl.carries(line.At) || d.deliveredMessage(line.At) {
		req.answer(relayTaken{dropped: true})
		return true
	}
	if rl.run == nil {
		req.answer(relayTaken{err: errors.New("the step process takes no later message")})
		return false
	}
	sctx, cancel := context.WithTimeout(ctx, relayAckBound)
	dl, err := rl.run.SendMessage(sctx, ownerMessageText(line))
	cancel()
	if err != nil {
		req.answer(relayTaken{err: err})
		return false
	}
	if awaiting {
		leaveAwaiting()
		d.ownerWait(false)
		d.fact(supervise.StepOwnerWait{Step: rl.stepID, SpawnN: rl.n, Waiting: false})
		d.mu.Lock()
		d.parked = nil // the ask is replied: the dispatch is unsettled again
		d.mu.Unlock()
	}
	rl.pending = append(rl.pending, pendingDelivery{line: line, delivery: dl, req: req})
	req.answer(relayTaken{delivery: dl})
	d.relayConfirm(rl)
	return true
}

// relayConfirm marks every pending delivery whose marker the watch has
// observed, and the prompt-carried messages once the dispatch's first
// turn started.
func (d *Driver) relayConfirm(rl *relayState) {
	if rl.run == nil {
		return
	}
	w := rl.run.deliveryWatch()
	if w == nil {
		return
	}
	if len(rl.carried) > 0 && w.turnStarted() {
		for _, l := range rl.carried {
			d.markDelivered(l, rl.n, dispositionPrompt, confirmedTurn, "")
		}
		rl.carried = nil
	}
	kept := rl.pending[:0]
	for _, p := range rl.pending {
		how, ok := w.confirmed(p.line.At, p.delivery.CommandID)
		if !ok {
			kept = append(kept, p)
			continue
		}
		if d.markDelivered(p.line, rl.n, p.delivery.Disposition, how, p.delivery.CommandID) && p.req != nil {
			close(p.req.confirmed)
		}
	}
	rl.pending = kept
}

// ---------------------------------------------------------------------------
// The delivery watch: the proof of delivery, read from the harness's
// own stdout stream.

// deliveryWatch follows one dispatch's stdout records for the delivery
// proofs (fed by the spawn adapter's record tap): claude's
// `command_lifecycle` `started` records (keyed by the message uuid the
// Messenger minted), pi's user `message_start` records (the marker inside
// the message text), and the dispatch's first turn start (claude's
// `system`/`init`, pi's `agent_start`) — the proof a resumed session's
// first message, which carries undelivered owner messages, was taken.
type deliveryWatch struct {
	harness string
	mu      sync.Mutex
	turn    bool
	started map[string]bool // claude: command uuids handed to the model
	markers map[string]bool // pi: delivery keys seen in a user message
	notify  chan struct{}
}

func newDeliveryWatch(h string) *deliveryWatch {
	return &deliveryWatch{harness: h, started: map[string]bool{}, markers: map[string]bool{}, notify: make(chan struct{}, 1)}
}

// feed reads one stdout line (the record tap; cheap, never blocking).
func (w *deliveryWatch) feed(line string) {
	var rec struct {
		Type        string `json:"type"`
		Subtype     string `json:"subtype"`
		CommandUUID string `json:"command_uuid"`
		State       string `json:"state"`
		Message     struct {
			Role    string          `json:"role"`
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	if json.Unmarshal([]byte(line), &rec) != nil || rec.Type == "" {
		return
	}
	changed := false
	w.mu.Lock()
	switch rec.Type {
	case "command_lifecycle":
		if rec.State == spawn.DispositionStarted && rec.CommandUUID != "" && !w.started[rec.CommandUUID] {
			w.started[rec.CommandUUID] = true
			changed = true
		}
	case "system":
		if rec.Subtype == "init" && !w.turn {
			w.turn, changed = true, true
		}
	case "agent_start":
		if !w.turn {
			w.turn, changed = true, true
		}
	case "message_start":
		if rec.Message.Role == "user" {
			for _, k := range markerKeys(string(rec.Message.Content)) {
				if !w.markers[k] {
					w.markers[k], changed = true, true
				}
			}
		}
	}
	w.mu.Unlock()
	if changed {
		select {
		case w.notify <- struct{}{}:
		default:
		}
	}
}

// markerKeys answers every delivery key whose marker `[pf-msg:<key>]`
// appears in text (a user message may carry several).
func markerKeys(text string) []string {
	const open = "[pf-msg:"
	var keys []string
	for {
		i := strings.Index(text, open)
		if i < 0 {
			return keys
		}
		rest := text[i+len(open):]
		j := strings.IndexByte(rest, ']')
		if j < 0 {
			return keys
		}
		if k := rest[:j]; k != "" {
			keys = append(keys, k)
		}
		text = rest[j+1:]
	}
}

// confirmed answers whether the message keyed by key — sent as harness
// command commandID — was observed: pi by its marker in a user message,
// claude by the `started` lifecycle of its uuid (the marker rides the
// native session record, not the stream). The proof's name comes with
// the answer.
func (w *deliveryWatch) confirmed(key, commandID string) (string, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.markers[key] {
		return confirmedUser, true
	}
	if w.harness == harness.Claude && commandID != "" && w.started[commandID] {
		return confirmedLifecycle, true
	}
	return "", false
}

// turnStarted reports whether the dispatch's first turn began.
func (w *deliveryWatch) turnStarted() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.turn
}
