package runtime

// cancel_intent_test.go (review s8#1): an owner's reserved cancel answer
// is completed by every settled blocked close — including reconcile's
// settle of a close whose answer was lost — and a capsule without the
// intent (the owner resumed it, which drops the intent) cancels nothing
// at its next blocked close.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// cancelDoorSpy records every cancel-door call the runtime makes.
type cancelDoorSpy struct {
	mu    sync.Mutex
	calls []capsule.CancelIntent
}

func (s *cancelDoorSpy) door(_ string, intent capsule.CancelIntent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, intent)
}

func (s *cancelDoorSpy) got() []capsule.CancelIntent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]capsule.CancelIntent(nil), s.calls...)
}

func writeCapsuleExec(t *testing.T, capDir string, exec capsule.Execution) {
	t.Helper()
	raw, err := json.Marshal(exec)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(capDir, capsule.ExecutionJSON)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// The lost-answer close: the end_pending record's close landed at the hub
// (the work item is blocked, the session no longer holds it) but its
// answer never came back. Reconcile settles it — and the owner's cancel
// answer completes there too.
func TestLostAnswerEndPendingSettleCancels(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	spy := &cancelDoorSpy{}
	r.cfg.CancelRequested = spy.door
	j := seedJob(r, "wi_cancel_lost")
	intent := capsule.CancelIntent{QuestionID: "q1", Answer: "cancel", AnsweredBy: "owner"}
	writeCapsuleExec(t, j.capsuleDir, capsule.Execution{WorkflowStatus: "blocked", CancelRequested: &intent})
	j.mu.Lock()
	j.state = StateEndPending
	j.mu.Unlock()
	hub.hold(j.wi, "", api.StatusBlocked) // the close landed server-side

	underLock(r, j.wi, func() { _, _ = r.reconcileRecord(j, causeOperatorAttach) })
	if r.jobOf(j.wi) != nil {
		t.Fatal("the lost-answer close settles the end_pending record")
	}
	if got := spy.got(); len(got) != 1 || got[0] != intent {
		t.Fatalf("the settled lost-answer close must call the cancel door once with the intent, got %+v", got)
	}
}

// A capsule the owner resumed carries no intent (the driver's resume drops
// it): its next blocked close, for any reason, cancels nothing.
func TestBlockedCloseWithoutIntentCancelsNothing(t *testing.T) {
	for _, tc := range []struct {
		name   string
		intent *capsule.CancelIntent
		want   int
	}{
		{"intent", &capsule.CancelIntent{QuestionID: "q1", Answer: "cancel"}, 1},
		{"resumed", nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			hub := newFakeHub()
			r, _, _ := endPendingRuntime(t, hub)
			spy := &cancelDoorSpy{}
			r.cfg.CancelRequested = spy.door
			j := r.jobOf("wi_d2_end")
			writeCapsuleExec(t, j.capsuleDir, capsule.Execution{WorkflowStatus: "blocked", CancelRequested: tc.intent})
			underLock(r, j.wi, func() { r.boundedFail(context.Background(), j, "incomplete") })
			if got := spy.got(); len(got) != tc.want {
				t.Fatalf("cancel door calls = %d (%+v), want %d", len(got), got, tc.want)
			}
		})
	}
}
