package core

// cancel_note_test.go: an owner's cancel answer reaches the hub's
// existing blocked→canceled door with its verbatim words: the reason and
// the answered question ride the work_item_canceled event, the actor is
// the authenticated caller, and a plain cancel keeps its empty body.

import (
	"encoding/json"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

func lastEventOf(t *testing.T, env *testEnv, wiID, typ string) Event {
	t.Helper()
	evs, err := env.WIs.Events(wiID)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(evs) - 1; i >= 0; i-- {
		if evs[i].Type == typ {
			return evs[i]
		}
	}
	t.Fatalf("no %s event on %s", typ, wiID)
	return Event{}
}

func TestCancelCarriesTheOwnersVerbatimWords(t *testing.T) {
	env := newEnv(t)
	id := mustCreateExecWI(t, env, "cancel with a note", nil, "")
	const words = "  Cancel  "
	wi, err := env.WIs.Cancel(id, env.UserID, api.CancelRequest{Reason: words, QuestionID: "continue_or_stop"})
	if err != nil || wi.Status != api.StatusCanceled {
		t.Fatalf("cancel: %v %+v", err, wi)
	}
	ev := lastEventOf(t, env, id, "work_item_canceled")
	var body map[string]string
	if err := json.Unmarshal(ev.Payload, &body); err != nil {
		t.Fatal(err)
	}
	if body["reason"] != words || body["question_id"] != "continue_or_stop" || ev.Actor != env.UserID {
		t.Fatalf("the event must carry the verbatim words, the question and the caller: actor %q body %v", ev.Actor, body)
	}
	plain := mustCreateExecWI(t, env, "plain cancel", nil, "")
	if _, err := env.WIs.Cancel(plain, env.UserID, api.CancelRequest{}); err != nil {
		t.Fatal(err)
	}
	if ev := lastEventOf(t, env, plain, "work_item_canceled"); string(ev.Payload) != "{}" {
		t.Fatalf("a plain cancel keeps its empty body, got %s", ev.Payload)
	}
}
