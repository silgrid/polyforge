package server

// machineinbox_test.go: the relay registry must broadcast, not wake one
// arbitrary long-poll. Every daemon holds a queries poll and an
// interactive multi-machine daemon also holds a messages poll, so a
// one-slot wake would hand a holder's query to a different machine's
// poll and leave the addressed holder asleep.

import (
	"sync"
	"testing"
	"time"
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
