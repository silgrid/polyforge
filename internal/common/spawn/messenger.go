package spawn

import (
	"context"
	"strings"
)

// messenger.go: the owner-message face of a resident step process
// (workflow contract v2, design §2.2 and §5.2). One harness process per
// step dispatch lives across many turns: the initial prompt opens the
// first turn, and every later owner message reaches the SAME process
// over the channel the prompt took. Today's lanes never send a later
// message — the owner-message relay (PR 6d) is the first caller; PR 6b
// only gives the process the capability.

// Message is one owner message relayed into a resident step process.
type Message struct {
	// Key is the delivery key the daemon tracks the message by (§2.2: the
	// owner message's `at`). It is embedded in the delivered text as
	// MessageMarker(Key), so a transcript reader confirms delivery by the
	// marker's presence: two messages with the same text never mix, and a
	// harness's "queued" acknowledgement — which does not guarantee the
	// message stays queued — is never mistaken for delivery. "" embeds no
	// marker.
	Key string
	// Text is the owner's message, verbatim.
	Text string
}

// Delivery reports how the harness took a message it accepted.
type Delivery struct {
	// CommandID is the harness command id the message rode (its response
	// record repeated it).
	CommandID string
	// Disposition is the harness's own word for what it did with the
	// message: DispositionStarted, DispositionQueued or DispositionHandled.
	Disposition string
}

const (
	// DispositionStarted: the process was idle; the message opened a new
	// turn.
	DispositionStarted = "started"
	// DispositionQueued: a turn was running; the message is queued and
	// handed to the model after the current assistant turn finishes its
	// tool calls, before the next model call (a steer). The harness does
	// not guarantee it stays queued — the transcript marker is the proof
	// of delivery, never this word.
	DispositionQueued = "queued"
	// DispositionHandled: an input handler of the harness consumed the
	// message; no turn ran for it.
	DispositionHandled = "handled"
)

// Messenger is the optional face of a Handle whose process stays
// resident across turns and takes later messages.
type Messenger interface {
	// SendMessage writes m to the process and waits, bounded by ctx, for
	// the harness to accept or refuse it. An error means the message did
	// NOT reach the harness (command channel closed, process gone, the
	// harness refused the command, ctx done); a nil error means the
	// harness accepted it with the returned Delivery.
	SendMessage(ctx context.Context, m Message) (Delivery, error)
}

// TurnReporter is the optional face of a Handle that follows the
// harness's turn boundaries from its own stdout stream.
type TurnReporter interface {
	// TurnRunning reports whether the harness is inside a run: a prompt
	// was accepted and the harness has not settled since (pi: between
	// agent_start and agent_settled). A message sent while it is true is
	// queued, not started.
	TurnRunning() bool
}

// MessageMarker is the marker MessageText embeds for key: what a
// transcript reader greps for to confirm delivery.
func MessageMarker(key string) string { return "[pf-msg:" + key + "]" }

// MessageText renders the text delivered for m: its Text, then the
// marker alone on a trailing line (no marker without a Key).
func MessageText(m Message) string {
	if m.Key == "" {
		return m.Text
	}
	return strings.TrimRight(m.Text, "\n") + "\n\n" + MessageMarker(m.Key)
}
