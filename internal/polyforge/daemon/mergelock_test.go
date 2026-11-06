package daemon

// mergelock_test.go: the per-repo merge lock — one holder per repo, the
// waiters served in arrival order, a waiter whose context ends leaves the
// queue without ever holding it, and other repos never wait.

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestMergeLocksSerializeOneRepo(t *testing.T) {
	l := newMergeLocks()
	ctx := context.Background()
	first, err := l.Lock(ctx, "o/app")
	if err != nil {
		t.Fatal(err)
	}
	// another repo never waits
	other, err := l.Lock(ctx, "o/lib")
	if err != nil {
		t.Fatal(err)
	}
	other()
	var mu sync.Mutex
	var order []int
	var wg sync.WaitGroup
	for i := 1; i <= 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			rel, err := l.Lock(ctx, "o/app")
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
			rel()
		}(i)
		// arrive in order
		deadline := time.Now().Add(5 * time.Second)
		for {
			l.mu.Lock()
			n := len(l.queues["o/app"])
			l.mu.Unlock()
			if n == i+1 || time.Now().After(deadline) {
				break
			}
			time.Sleep(time.Millisecond)
		}
	}
	mu.Lock()
	if len(order) != 0 {
		t.Fatalf("nobody holds the lock while the first does: %v", order)
	}
	mu.Unlock()
	first()
	first() // idempotent
	wg.Wait()
	if len(order) != 3 || order[0] != 1 || order[1] != 2 || order[2] != 3 {
		t.Fatalf("the waiters hold it in arrival order: %v", order)
	}
	if len(l.queues) != 0 {
		t.Fatalf("no queue left behind: %v", l.queues)
	}
}

func TestMergeLocksWaiterLeavesOnCancel(t *testing.T) {
	l := newMergeLocks()
	held, err := l.Lock(context.Background(), "o/app")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := l.Lock(ctx, "o/app")
		done <- err
	}()
	deadline := time.Now().Add(5 * time.Second)
	for {
		l.mu.Lock()
		n := len(l.queues["o/app"])
		l.mu.Unlock()
		if n == 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("a canceled waiter never holds the lock")
	}
	held()
	next, err := l.Lock(context.Background(), "o/app")
	if err != nil {
		t.Fatalf("the lock is free again: %v", err)
	}
	next()
	if len(l.queues) != 0 {
		t.Fatalf("no queue left behind: %v", l.queues)
	}
}
