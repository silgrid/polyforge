package server

// machineinbox_test.go: the relay registry must broadcast, not wake one
// arbitrary long-poll. Every daemon holds a queries poll and an
// interactive multi-machine daemon also holds a messages poll, so a
// one-slot wake would hand a holder's query to a different machine's
// poll and leave the addressed holder asleep.

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// TestQueryRegistryBroadcastWakesEveryWaiter registers three concurrent
// waiters on the current broadcast channel and enqueues one query: all
// three must wake (red on the 1-slot shared channel, where only one
// receiver is handed the token).
func TestQueryRegistryBroadcastWakesEveryWaiter(t *testing.T) {
	q := newQueryRegistry()
	const waiters = 3
	wake := q.wake
	ready := sync.WaitGroup{}
	ready.Add(waiters)
	woke := make(chan struct{}, waiters)
	for i := 0; i < waiters; i++ {
		go func() {
			ready.Done()
			select {
			case <-wake:
				woke <- struct{}{}
			case <-time.After(2 * time.Second):
			}
		}()
	}
	ready.Wait()
	time.Sleep(20 * time.Millisecond) // let every waiter block on the channel

	q.add(&pendingQuery{
		ID: "q-broadcast", Holder: "holder", Deadline: time.Now().Add(time.Second),
		Waiter: make(chan *queryResult, 1),
	})
	deadline := time.After(2 * time.Second)
	for n := 0; n < waiters; n++ {
		select {
		case <-woke:
		case <-deadline:
			t.Fatalf("only %d/%d long-polls woke on one enqueue", n, waiters)
		}
	}
}

// TestQueryRegistryAddressedHolderTakesOnlyItsQuery keeps the routing
// half: another machine's take sees nothing, the addressed holder gets it
// once.
func TestQueryRegistryAddressedHolderTakesOnlyItsQuery(t *testing.T) {
	q := newQueryRegistry()
	q.add(&pendingQuery{
		ID: "q-1", Holder: "holder", Kind: "progress", WorkItem: "wi_1",
		Deadline: time.Now().Add(time.Second), Waiter: make(chan *queryResult, 1),
	})
	if got := q.take("other"); len(got) != 0 {
		t.Fatalf("another machine took the query: %+v", got)
	}
	got := q.take("holder")
	if len(got) != 1 || got[0].ID != "q-1" {
		t.Fatalf("the addressed holder did not take its query: %+v", got)
	}
	if got := q.take("holder"); len(got) != 0 {
		t.Fatalf("a delivered query was handed out twice: %+v", got)
	}
}

// ---------------------------------------------------------------------------
// the expiry/answer race — first-wins delivery, never a
// blocking send. Deterministic (plan D6): the post-race state is forced
// directly, no timing race is required to expose the block.

// TestQueryAnswerAfterExpiryNoticeDoesNotBlock forces the post-race state:
// the registry still knows the query (deadline in the future) but its
// 1-slot waiter already holds the expiry notice an expiring take sent
// after the holder's get — exactly where the answer path used to block
// forever at p.Waiter <- res. The answer must return promptly with 200
// (the holder's own HTTP answer), and the waiter must still hold exactly
// the one result it already had (first wins).
func TestQueryAnswerAfterExpiryNoticeDoesNotBlock(t *testing.T) {
	e := newSrvEnv(t)
	waiter := make(chan *queryResult, 1)
	waiter <- &queryResult{Err: &api.Error{Code: api.ErrCoreUpstream, Detail: "holder did not answer within the deadline"}}
	e.srv.pending.add(&pendingQuery{
		ID: "q-race", Kind: "progress", WorkItem: "wi_1", Holder: "holder",
		Deadline: time.Now().Add(time.Minute), Waiter: waiter,
	})
	req := httptest.NewRequest("POST", "/api/v1/queries/q-race/answer",
		strings.NewReader(`{"holder_machine":"holder"}`))
	req.SetPathValue("qid", "q-race")
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		e.srv.hQueryAnswer(rec, req, &identity{}, machineAuth{MachineID: "holder"})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("hQueryAnswer blocked on a waiter that already holds the expiry notice")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("answer = %d %s, want 200 (the holder still gets its own answer)", rec.Code, rec.Body)
	}
	if len(waiter) != 1 {
		t.Fatalf("waiter holds %d results, want exactly the one it already had", len(waiter))
	}
	if res := <-waiter; res.Err == nil || res.Err.Code != api.ErrCoreUpstream {
		t.Fatalf("waiter holds %v, want the expiry notice (first wins)", res)
	}
}

// TestQueryRegistryExpiryIsFirstWinsAndPrompt walks the faithful sequence
// (plan D6 B): get before the deadline → deadline passes → take expires
// and delivers the notice after releasing q.mu → the query is gone; and
// take with a FULL waiter returns promptly — no send, blocking or not,
// may happen while q.mu is held.
func TestQueryRegistryExpiryIsFirstWinsAndPrompt(t *testing.T) {
	q := newQueryRegistry()
	waiter := make(chan *queryResult, 1)
	q.add(&pendingQuery{ID: "q-faith", Kind: "progress", Holder: "h",
		Deadline: time.Now().Add(30 * time.Millisecond), Waiter: waiter})
	if q.get("q-faith") == nil {
		t.Fatal("get before the deadline lost the query")
	}
	time.Sleep(50 * time.Millisecond) // the deadline passes
	if got := q.take("other"); len(got) != 0 {
		t.Fatalf("an expired query was handed out: %+v", got)
	}
	select {
	case res := <-waiter:
		if res.Err == nil || res.Err.Code != api.ErrCoreUpstream {
			t.Fatalf("waiter holds %v, want the expiry notice", res)
		}
	default:
		t.Fatal("take did not deliver the expiry notice")
	}
	if q.get("q-faith") != nil {
		t.Fatal("the expired query is still registered")
	}

	// take with a full waiter: prompt, and the original result stays.
	full := make(chan *queryResult, 1)
	full <- &queryResult{Err: &api.Error{Code: api.ErrCoreUpstream, Detail: "already here"}}
	q.add(&pendingQuery{ID: "q-full", Kind: "progress", Holder: "h",
		Deadline: time.Now().Add(-time.Second), Waiter: full})
	done := make(chan struct{})
	go func() { q.take("other"); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("take blocked on a full waiter (a send under q.mu)")
	}
	if len(full) != 1 {
		t.Fatalf("the full waiter holds %d results, want its original one (first wins)", len(full))
	}
}
