package observe

import (
	"encoding/json"
	"testing"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// TestRTFinishedRecordsItsActualReason (code review): every
// runtime release — a close whose final reason degraded (a wrap whose push
// failed ends blocked), a bounded-fail block, a pause, a supersede — leaves
// one job_finished event naming the reason the runtime actually ended with,
// so the daemon's decision feed records what happened, not what was asked.
func TestRTFinishedRecordsItsActualReason(t *testing.T) {
	o, err := New(Config{RT: supervise.NewQueue[protocol.RTMsg](), OB: supervise.NewQueue[protocol.OBMsg](), DataDir: t.TempDir(),
		Agent: func(string) (Handle, WIInfo, bool) { return nil, WIInfo{}, false }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.log.Close() })
	reasons := []string{protocol.FinishWrapped, protocol.FinishFailed, protocol.FinishBlocked, protocol.FinishPaused, protocol.FinishSuperseded}
	for _, r := range reasons {
		o.onRT(protocol.RTMsg{Kind: protocol.RTFinished, WorkItem: "wi_" + r, Reason: r})
	}
	for _, r := range reasons {
		es := o.log.SinceAfter("wi_"+r, 0)
		if len(es) != 1 || es[0].Kind != EvJobFinished {
			t.Fatalf("%s: events = %+v", r, es)
		}
		var body struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(es[0].Body, &body) != nil || body.Reason != r {
			t.Fatalf("%s: body = %s", r, es[0].Body)
		}
	}
}

// TestRTFinishedUnconfirmedIsMarked: a local seal the hub never confirmed
// (finishSealLocally) carries its requested reason marked unconfirmed.
func TestRTFinishedUnconfirmedIsMarked(t *testing.T) {
	o, err := New(Config{RT: supervise.NewQueue[protocol.RTMsg](), OB: supervise.NewQueue[protocol.OBMsg](), DataDir: t.TempDir(),
		Agent: func(string) (Handle, WIInfo, bool) { return nil, WIInfo{}, false }})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.log.Close() })
	o.onRT(protocol.RTMsg{Kind: protocol.RTFinished, WorkItem: "wi_u", Reason: protocol.FinishWrapped, Unconfirmed: true})
	es := o.log.SinceAfter("wi_u", 0)
	if len(es) != 1 || string(es[0].Body) != `{"reason":"wrapped","unconfirmed":true}` {
		t.Fatalf("events = %+v", es)
	}
}
