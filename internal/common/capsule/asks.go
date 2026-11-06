package capsule

// asks.go: runtime-state/asks.jsonl — the record of "who said what"
// between a step's agent and the owner (workflow contract v2, design
// §2.2 and §5.2; PR 6c). The agent asks the owner with `pf ask
// "<question>"`, a LOCAL command that appends one line here and prints
// "now end your turn"; the daemon watches the file (and re-reads it at
// every turn end), and an ask with no reply keyed to it parks the step
// AWAITING THE OWNER: the question is shown, the running slot is released,
// nothing of the dispatch is judged, and a restart finds the open ask and
// parks the step again without a new dispatch. The owner's message (the
// relay, PR 6d) is appended here too, BEFORE it is delivered — a reply
// while an ask is open, a message otherwise — and its delivery is one
// more appended line (`delivered`, naming the message by its stamp), so
// a restart replays exactly the messages no delivered line names.
//
// The file is append-only and crash-safe: one JSON object per line,
// fsynced per append, never rewritten; a torn trailing line is skipped by
// the reader. Every line is keyed by its own `at` (RFC3339Nano, UTC) — a
// reply names the ask it answers by that stamp (`ask_at`), a delivered
// line names its message by it (`msg_at`), and the daemon's faces show an
// ask under that stamp as its id. The daemon reads the SHAPE of the file
// only (is there an ask of this step's current dispatch with no reply to
// it? which messages has no delivered line?); the question's and the
// owner's words are relayed verbatim and never interpreted here.

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// AsksJSONL is the capsule-relative path of the ask record.
const AsksJSONL = "runtime-state/asks.jsonl"

// The line kinds.
const (
	// AskKindAsk is the agent's question (`pf ask`).
	AskKindAsk = "ask"
	// AskKindReply is the owner's reply to one ask (`ask_at` names it).
	AskKindReply = "reply"
	// AskKindMessage is an owner message sent while no ask is open (a
	// live interjection, PR 6d); it closes nothing.
	AskKindMessage = "message"
	// AskKindDelivered records that one owner message (`msg_at`) reached
	// the step's conversation under dispatch `spawn` (PR 6d): the marker
	// the daemon embedded in the delivered text appeared in the harness's
	// stream. A reply or message with no delivered line is undelivered
	// and is replayed by the step's next dispatch.
	AskKindDelivered = "delivered"
)

// MaxAskBytes bounds one ask's text (`pf ask` refuses a longer one): the
// question is shown whole in the job card and the decision feed.
const MaxAskBytes = 8 * 1024

// MaxOwnerMessageBytes bounds one owner message's text (`pf answer`
// refuses a longer one): a message is one harness prompt, and a pasted
// log is the largest thing an owner sends.
const MaxOwnerMessageBytes = 64 * 1024

// AskLine is one line of asks.jsonl.
type AskLine struct {
	// At is the line's own stamp (RFC3339Nano, UTC): the key every other
	// line and every face refers to it by.
	At string `json:"at"`
	// Step and Spawn name the dispatch that asked (PF_STEP / PF_SPAWN of
	// the asking process): an ask is open only for THAT dispatch — a later
	// dispatch of the step never inherits it. On a reply or message the
	// relay wrote they name the step the owner addressed and the dispatch
	// current when the owner wrote (the message belongs to the STEP's
	// conversation: a later dispatch of the step delivers it when that one
	// could not); on a delivered line, the dispatch that took the message.
	Step  string `json:"step,omitempty"`
	Spawn int64  `json:"spawn,omitempty"`
	Kind  string `json:"kind"`
	// Text is the question (ask) or the owner's words (reply, message),
	// verbatim. Empty on a delivered line.
	Text string `json:"text,omitempty"`
	// AskAt is the `at` of the ask a reply answers.
	AskAt string `json:"ask_at,omitempty"`
	// MsgAt is the `at` of the reply or message a delivered line records.
	MsgAt string `json:"msg_at,omitempty"`
	// By is who wrote a reply or message (the owner's user id).
	By string `json:"by,omitempty"`
}

// AppendAsk appends one line to the capsule's asks.jsonl (created on the
// first append, runtime-state/ with it) and fsyncs it. The line must
// carry its stamp and a kind.
func AppendAsk(capsuleDir string, l AskLine) error {
	if l.At == "" || l.Kind == "" {
		return fmt.Errorf("asks: a line needs its at and kind")
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return fmt.Errorf("asks: %w", err)
	}
	path := filepath.Join(capsuleDir, AsksJSONL)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("asks: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("asks: %w", err)
	}
	defer f.Close()
	// a torn trailing line (a crash mid-append) must not swallow this one:
	// the new line starts on its own line
	if st, err := f.Stat(); err == nil && st.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, st.Size()-1); err == nil && last[0] != '\n' {
			raw = append([]byte{'\n'}, raw...)
		}
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return fmt.Errorf("asks: %w", err)
	}
	if err := f.Sync(); err != nil {
		return fmt.Errorf("asks: %w", err)
	}
	return nil
}

// ReadAsks reads the capsule's asks.jsonl in order. A missing file is an
// empty record; a torn or malformed line (a crash mid-append) is skipped,
// never an error, so the record stays readable after any crash.
func ReadAsks(capsuleDir string) ([]AskLine, error) {
	f, err := os.Open(filepath.Join(capsuleDir, AsksJSONL))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("asks: %w", err)
	}
	defer f.Close()
	var out []AskLine
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 1024*1024)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var l AskLine
		if json.Unmarshal([]byte(line), &l) != nil || l.At == "" || l.Kind == "" {
			continue // torn or foreign: not a line of the record
		}
		out = append(out, l)
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("asks: %w", err)
	}
	return out, nil
}

// OpenAsk answers the open ask of one dispatch — the newest ask line of
// (step, spawn) that no reply line names by its at — or nil. A reply keyed
// to another stamp closes nothing; an ask of another dispatch (an earlier
// spawn of the same step, another step) is never this dispatch's.
func OpenAsk(lines []AskLine, step string, spawn int64) *AskLine {
	replied := map[string]bool{}
	for _, l := range lines {
		if l.Kind == AskKindReply && l.AskAt != "" {
			replied[l.AskAt] = true
		}
	}
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if l.Kind != AskKindAsk || l.Step != step || l.Spawn != spawn {
			continue
		}
		if replied[l.At] {
			return nil // the newest ask of the dispatch is answered
		}
		cp := l
		return &cp
	}
	return nil
}

// Undelivered answers, in order, the owner messages of one step — the
// reply and message lines the relay wrote naming step — that no delivered
// line names by their stamp: what the step's next dispatch carries in its
// prompt (PR 6d). A line naming no step (a v1 answer the driver mirrored
// as a reply) rides the v1 answer path and is never one of them.
func Undelivered(lines []AskLine, step string) []AskLine {
	delivered := map[string]bool{}
	for _, l := range lines {
		if l.Kind == AskKindDelivered && l.MsgAt != "" {
			delivered[l.MsgAt] = true
		}
	}
	var out []AskLine
	for _, l := range lines {
		if (l.Kind != AskKindReply && l.Kind != AskKindMessage) || l.Step == "" || l.Step != step || delivered[l.At] {
			continue
		}
		out = append(out, l)
	}
	return out
}

// OpenAskOf reads the capsule's record and answers the open ask of the
// execution's current dispatch: the current step is in_progress with a
// dispatch ordinal, and the record holds an open ask of exactly that step
// and ordinal. nil otherwise, and on any read error (the faces degrade to "no
// ask"; the driver re-reads at its next poll).
func OpenAskOf(capsuleDir string, ex *Execution) *AskLine {
	step, spawn, ok := CurrentDispatch(ex)
	if !ok {
		return nil
	}
	lines, err := ReadAsks(capsuleDir)
	if err != nil {
		return nil
	}
	return OpenAsk(lines, step, spawn)
}

// CurrentDispatch answers the execution's current step and dispatch
// ordinal when a step process may be asking: the workflow runs and the
// current step is in_progress under a dispatch ordinal.
func CurrentDispatch(ex *Execution) (step string, spawn int64, ok bool) {
	if ex == nil || ex.WorkflowStatus != WorkflowStatusRunning || ex.CurrentStep == "" {
		return "", 0, false
	}
	st := ex.Steps[ex.CurrentStep]
	if st == nil || st.Status != StepInProgress || st.Dispatch == 0 {
		return "", 0, false
	}
	return ex.CurrentStep, st.Dispatch, true
}
