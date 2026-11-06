package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/spawn"
)

// classifyingStdin stands in for claude's stdin AND its stdout reader:
// the moment a message line is written, the records the harness answers
// it with are classified, all of them before sendClaudeMessage's wait
// first wakes — the waiter scheduled late, as on a loaded host.
type classifyingStdin struct {
	p       *proc
	answers func(uuid string) []string
}

func (w *classifyingStdin) Write(b []byte) (int, error) {
	var m struct {
		UUID string `json:"uuid"`
	}
	if err := json.Unmarshal(bytes.TrimSpace(b), &m); err != nil {
		return 0, err
	}
	for _, line := range w.answers(m.UUID) {
		w.p.classify(line)
	}
	return len(b), nil
}

func (w *classifyingStdin) Close() error { return nil }

// A claude message's disposition is read at its `queued` record's place
// in the stream, never at the moment the waiter wakes (the v0.2.9 release
// gate's red TestV26dClaudeReplyDeliveredToLiveProcess: an idle process
// recorded as queued). Idle, the message opens its own turn — queued,
// started, system/init — and a late waiter that read the live run state
// saw that new turn running: queued. Mid-turn, the message is absorbed —
// queued, started, then the turn's result — and a late waiter saw the
// turn over: started.
func TestClaudeDispositionFollowsTheStreamNotTheWakeUp(t *testing.T) {
	lifecycle := func(id, state string) string {
		return fmt.Sprintf(`{"type":"command_lifecycle","command_uuid":%q,"state":%q}`, id, state)
	}
	const (
		initRec   = `{"type":"system","subtype":"init","session_id":"s"}`
		resultRec = `{"type":"result","subtype":"success","is_error":false}`
	)
	cases := []struct {
		name    string
		before  []string
		answers func(id string) []string
		want    string
	}{
		{
			name:   "idle: the message opens its own turn",
			before: []string{initRec, resultRec},
			answers: func(id string) []string {
				return []string{lifecycle(id, "queued"), lifecycle(id, "started"), initRec}
			},
			want: spawn.DispositionStarted,
		},
		{
			name:   "mid-turn: the message is absorbed into the running turn",
			before: []string{initRec},
			answers: func(id string) []string {
				return []string{lifecycle(id, "queued"), lifecycle(id, "started"), resultRec}
			},
			want: spawn.DispositionQueued,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := &proc{pending: map[string]chan stdoutRecord{}, exited: make(chan struct{})}
			p.stdin = &classifyingStdin{p: p, answers: c.answers}
			for _, line := range c.before {
				p.classify(line)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			d, err := p.sendClaudeMessage(ctx, messageCommandID(1, 1), spawn.Message{Key: "2026-11-11T22:20:47.067586294Z", Text: "words"})
			if err != nil {
				t.Fatal(err)
			}
			if d.Disposition != c.want || d.CommandID != messageCommandID(1, 1) {
				t.Fatalf("delivery %+v, want disposition %s", d, c.want)
			}
		})
	}
}
