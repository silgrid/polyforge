package core

// lease_test.go: the generic lease base (polyforge#245, 245-lease).
// Every assertion pins the ticket's decided time semantics — server
// clock, 300 s TTL and 60 s waiter liveness (the >= boundary on both),
// FIFO by the server's first-seen sequence, one holder per (kind, key),
// a token minted per grant and checked on renew/release, and request-id
// idempotency across a store restart. The clock is injected: no test
// sleeps.

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// leaseBase is the fixed server time every case starts from.
var leaseBase = time.Date(2026, 11, 23, 12, 0, 0, 0, time.UTC)

// fakeClock is the injected server clock.
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// newLeaseEnv opens one store and manager at leaseBase.
func newLeaseEnv(t *testing.T) (*LeaseManager, *DB, *fakeClock) {
	t.Helper()
	return newLeaseEnvAt(t, t.TempDir())
}

func newLeaseEnvAt(t *testing.T, dir string) (*LeaseManager, *DB, *fakeClock) {
	t.Helper()
	db, err := Open(dir)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	clk := &fakeClock{t: leaseBase}
	m := NewLeaseManager(db)
	m.SetClock(clk.Now)
	return m, db, clk
}

// leaseReq is one caller's identity.
func leaseReq(id, key, machine, wi string) LeaseRequest {
	return LeaseRequest{Kind: "landing", Key: key, RequestID: id, MachineID: machine, WorkItemID: wi, OperatorID: "u1"}
}

// mustLeaseAcquire answers the grant or fails.
func mustLeaseAcquire(t *testing.T, m *LeaseManager, req LeaseRequest) *LeaseGrant {
	t.Helper()
	g, err := m.Acquire(req)
	if err != nil {
		t.Fatalf("acquire %s: %v", req.RequestID, err)
	}
	return g
}

// leaseWantErr asserts the typed refusal code and reason.
func leaseWantErr(t *testing.T, err error, code, reason string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want %s/%s refusal, got success", code, reason)
	}
	var ae *api.Error
	if !errors.As(err, &ae) {
		t.Fatalf("want *api.Error, got %T: %v", err, err)
	}
	if ae.Code != code {
		t.Fatalf("error code = %s, want %s (%v)", ae.Code, code, err)
	}
	var extra struct {
		Reason string `json:"reason"`
	}
	_ = json.Unmarshal(ae.Extra, &extra)
	if reason != "" && extra.Reason != reason {
		t.Fatalf("error reason = %q, want %q (%v)", extra.Reason, reason, err)
	}
}

var leaseTokenShape = regexp.MustCompile(`^[0-9a-f]{64}$`)

// TestLeaseFIFO pins the decided FIFO grant: one holder per key, the
// queue served by the server's first-seen order, and every grant made in
// the granted caller's own poll transaction.
func TestLeaseFIFO(t *testing.T) {
	m, _, clk := newLeaseEnv(t)
	const key = "github.com/o/app\nmain"

	a := mustLeaseAcquire(t, m, leaseReq("r-a", key, "m-a", "alpha#1"))
	if a.Status != LeaseStatusGranted || !leaseTokenShape.MatchString(a.Token) {
		t.Fatalf("first acquire = %+v, want granted with a 32-byte hex token", a)
	}
	if a.ExpiresAt != leaseBase.Add(300*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("expires_at = %q, want the server clock + 300 s", a.ExpiresAt)
	}
	b := mustLeaseAcquire(t, m, leaseReq("r-b", key, "m-b", "alpha#2"))
	if b.Status != LeaseStatusQueued || b.Position != 1 || b.Token != "" {
		t.Fatalf("second acquire = %+v, want queued position 1 with no token", b)
	}
	clk.Advance(time.Second)
	c := mustLeaseAcquire(t, m, leaseReq("r-c", key, "m-c", "alpha#3"))
	if c.Status != LeaseStatusQueued || c.Position != 2 {
		t.Fatalf("third acquire = %+v, want queued position 2", c)
	}
	// A release frees the key but grants nothing: B is granted on its own
	// next poll, C stays queued behind it.
	if _, err := m.Release(leaseReq("r-a", key, "m-a", "alpha#1"), a.Token); err != nil {
		t.Fatalf("release A: %v", err)
	}
	if got := mustLeaseAcquire(t, m, leaseReq("r-c", key, "m-c", "alpha#3")); got.Status != LeaseStatusQueued || got.Position != 2 {
		t.Fatalf("C polled before B's grant: %+v, want still second behind B", got)
	}
	bGrant := mustLeaseAcquire(t, m, leaseReq("r-b", key, "m-b", "alpha#2"))
	if bGrant.Status != LeaseStatusGranted || bGrant.Token == "" || bGrant.Token == a.Token {
		t.Fatalf("B poll = %+v, want granted with a fresh token", bGrant)
	}
	if got := mustLeaseAcquire(t, m, leaseReq("r-b", key, "m-b", "alpha#2")); got.Status != LeaseStatusHeld || got.Token != "" {
		t.Fatalf("B's idempotent retry = %+v, want held with no token", got)
	}
}

// TestLeaseKeysIndependent pins that different keys never block each
// other: a held lease on K1 leaves K2 immediately grantable.
func TestLeaseKeysIndependent(t *testing.T) {
	m, _, _ := newLeaseEnv(t)
	k1 := mustLeaseAcquire(t, m, leaseReq("r1", "k1", "m1", "alpha#1"))
	k2 := mustLeaseAcquire(t, m, leaseReq("r2", "k2", "m2", "alpha#2"))
	if k1.Status != LeaseStatusGranted || k2.Status != LeaseStatusGranted {
		t.Fatalf("two keys: %+v / %+v, want both granted", k1, k2)
	}
	if k1.Token == k2.Token {
		t.Fatal("two grants share one token")
	}
}

// TestLeaseWaiterEviction pins the 60 s waiter liveness and its
// boundary: a waiter whose last poll is exactly 60 s old is out of the
// queue, and a later acquire with the same request id queues at the tail
// — the old sequence position never comes back.
func TestLeaseWaiterEviction(t *testing.T) {
	m, _, clk := newLeaseEnv(t)
	const key = "github.com/o/app\nmain"
	holder := mustLeaseAcquire(t, m, leaseReq("r-hold", key, "m0", "alpha#0"))
	_ = holder

	b := mustLeaseAcquire(t, m, leaseReq("r-b", key, "m-b", "alpha#1")) // t0
	if b.Status != LeaseStatusQueued || b.Position != 1 {
		t.Fatalf("B at t0 = %+v, want queued 1", b)
	}
	clk.Advance(59 * time.Second)
	c := mustLeaseAcquire(t, m, leaseReq("r-c", key, "m-c", "alpha#2")) // t0+59
	if c.Status != LeaseStatusQueued || c.Position != 2 {
		t.Fatalf("C at t0+59 = %+v, want queued 2 (B still live)", c)
	}
	if _, err := m.Lookup("r-b", "m-b", "alpha#1"); err != nil {
		t.Fatalf("B must still be queued at t0+59: %v", err)
	}
	clk.Advance(time.Second) // t0+60: B's last poll is exactly the TTL old
	c2 := mustLeaseAcquire(t, m, leaseReq("r-c", key, "m-c", "alpha#2"))
	if c2.Status != LeaseStatusQueued || c2.Position != 1 {
		t.Fatalf("C at t0+60 = %+v, want position 1 after B's eviction", c2)
	}
	leaseWantErr(t, func() error { _, err := m.Lookup("r-b", "m-b", "alpha#1"); return err }(), api.ErrCoreNotFound, "LEASE_NOT_QUEUED")
	// B comes back: a new queue row, tail position — never the old head.
	b2 := mustLeaseAcquire(t, m, leaseReq("r-b", key, "m-b", "alpha#1"))
	if b2.Status != LeaseStatusQueued || b2.Position != 2 {
		t.Fatalf("B re-queued = %+v, want queued 2 behind C", b2)
	}
}

// TestLeaseExpiryHandover pins the 300 s lease: without renewal the
// holder is out at exactly the TTL, the next waiter is granted on its own
// poll with a new token, and the old token can no longer touch the lease.
func TestLeaseExpiryHandover(t *testing.T) {
	m, _, clk := newLeaseEnv(t)
	const key = "github.com/o/app\nmain"
	a := mustLeaseAcquire(t, m, leaseReq("r-a", key, "m-a", "alpha#1"))
	clk.Advance(time.Second)
	if b := mustLeaseAcquire(t, m, leaseReq("r-b", key, "m-b", "alpha#2")); b.Status != LeaseStatusQueued {
		t.Fatalf("B = %+v, want queued", b)
	}
	clk.Advance(298 * time.Second) // t0+299: A's lease is still live
	if b := mustLeaseAcquire(t, m, leaseReq("r-b", key, "m-b", "alpha#2")); b.Status != LeaseStatusQueued || b.Position != 1 {
		t.Fatalf("B at t0+299 = %+v, want still queued", b)
	}
	clk.Advance(time.Second) // t0+300: A's lease is exactly the TTL old
	b := mustLeaseAcquire(t, m, leaseReq("r-b", key, "m-b", "alpha#2"))
	if b.Status != LeaseStatusGranted || !leaseTokenShape.MatchString(b.Token) || b.Token == a.Token {
		t.Fatalf("B at t0+300 = %+v, want granted with a new token", b)
	}
	if b.ExpiresAt != leaseBase.Add(600*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("B expires_at = %q, want the grant time + 300 s", b.ExpiresAt)
	}
}

// TestLeaseStaleToken pins the token check: a superseded token neither
// renews nor releases, the new holder is untouched, and a lease that
// already expired answers lost and frees the key.
func TestLeaseStaleToken(t *testing.T) {
	m, _, clk := newLeaseEnv(t)
	const key = "github.com/o/app\nmain"
	a := mustLeaseAcquire(t, m, leaseReq("r-a", key, "m-a", "alpha#1"))
	if b := mustLeaseAcquire(t, m, leaseReq("r-b", key, "m-b", "alpha#2")); b.Status != LeaseStatusQueued {
		t.Fatalf("B = %+v, want queued", b)
	}
	clk.Advance(300 * time.Second)
	b := mustLeaseAcquire(t, m, leaseReq("r-b", key, "m-b", "alpha#2"))
	if b.Status != LeaseStatusGranted {
		t.Fatalf("B = %+v, want granted", b)
	}
	leaseWantErr(t, func() error { _, err := m.Renew(leaseReq("r-a", key, "m-a", "alpha#1"), a.Token); return err }(),
		api.ErrAuthForbidden, "LEASE_TOKEN_MISMATCH")
	leaseWantErr(t, func() error { _, err := m.Release(leaseReq("r-a", key, "m-a", "alpha#1"), a.Token); return err }(),
		api.ErrAuthForbidden, "LEASE_TOKEN_MISMATCH")
	if got, err := m.Renew(leaseReq("r-b", key, "m-b", "alpha#2"), b.Token); err != nil || got.Status != LeaseStatusGranted {
		t.Fatalf("new holder renew = %+v, %v", got, err)
	}
	leaseWantErr(t, func() error { _, err := m.Release(leaseReq("r-b", key, "m-b", "alpha#2"), a.Token); return err }(),
		api.ErrAuthForbidden, "LEASE_TOKEN_MISMATCH")
	if got, err := m.Release(leaseReq("r-b", key, "m-b", "alpha#2"), b.Token); err != nil || got.Status != LeaseStatusReleased {
		t.Fatalf("release B = %+v, %v", got, err)
	}
	// No holder: the old token's renew is lost, its release is the
	// idempotent no-change success, and the key is free for the next poll.
	leaseWantErr(t, func() error { _, err := m.Renew(leaseReq("r-a", key, "m-a", "alpha#1"), a.Token); return err }(),
		api.ErrCoreConflict, "LEASE_LOST")
	if got, err := m.Release(leaseReq("r-a", key, "m-a", "alpha#1"), a.Token); err != nil || got.Status != LeaseStatusReleased {
		t.Fatalf("idempotent release = %+v, %v", got, err)
	}
	c := mustLeaseAcquire(t, m, leaseReq("r-c", key, "m-c", "alpha#3"))
	if c.Status != LeaseStatusGranted {
		t.Fatalf("free key acquire = %+v, want granted", c)
	}
}

// TestLeaseRequestIDIdempotent pins request-id idempotency: the retry of
// an acquire answers the same state (held after a grant, no second token),
// lookup answers only the same machine and work item, and a different
// request id for the same machine and ticket is a new queue entry.
func TestLeaseRequestIDIdempotent(t *testing.T) {
	m, _, _ := newLeaseEnv(t)
	const key = "github.com/o/app\nmain"
	first := mustLeaseAcquire(t, m, leaseReq("r1", key, "m-a", "alpha#1"))
	if first.Status != LeaseStatusGranted {
		t.Fatalf("first = %+v, want granted", first)
	}
	retry := mustLeaseAcquire(t, m, leaseReq("r1", key, "m-a", "alpha#1"))
	if retry.Status != LeaseStatusHeld || retry.Token != "" || retry.ExpiresAt != first.ExpiresAt {
		t.Fatalf("retry = %+v, want held with the original grant's stamps and no token", retry)
	}
	if held, err := m.Lookup("r1", "m-a", "alpha#1"); err != nil || held.Status != LeaseStatusHeld {
		t.Fatalf("lookup of the grant = %+v, %v", held, err)
	}
	leaseWantErr(t, func() error { _, err := m.Lookup("r1", "m-other", "alpha#1"); return err }(),
		api.ErrCoreNotFound, "LEASE_NOT_QUEUED")
	leaseWantErr(t, func() error { _, err := m.Acquire(leaseReq("r1", key, "m-other", "alpha#1")); return err }(),
		api.ErrCoreConflict, "LEASE_REQUEST_CONFLICT")
	other := mustLeaseAcquire(t, m, leaseReq("r2", key, "m-a", "alpha#1"))
	if other.Status != LeaseStatusQueued || other.Position != 1 {
		t.Fatalf("different request id = %+v, want a new queue entry", other)
	}
}

// TestLeaseRestartNoDoubleHolder pins the store restart: the holder
// survives it, a later waiter still waits for the expiry, and the
// (kind, key) primary key refuses a second holder row outright.
func TestLeaseRestartNoDoubleHolder(t *testing.T) {
	dir := t.TempDir()
	const key = "github.com/o/app\nmain"
	m1, db1, _ := newLeaseEnvAt(t, dir)
	a := mustLeaseAcquire(t, m1, leaseReq("r-a", key, "m-a", "alpha#1"))
	if a.Status != LeaseStatusGranted {
		t.Fatalf("A = %+v, want granted", a)
	}
	if _, err := db1.Exec(`INSERT INTO leases(kind,key,request_id,machine_id,work_item,operator_id,token_hash,acquired_at_ns,expires_at_ns)
		VALUES ('landing',?1,'r-dup','m-x','alpha#9','u1','h',0,0)`, key); err == nil {
		t.Fatal("a second holder row for one (kind, key) was accepted")
	}
	if err := db1.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	m2, _, clk := newLeaseEnvAt(t, dir)
	clk.Advance(time.Second)
	if b := mustLeaseAcquire(t, m2, leaseReq("r-b", key, "m-b", "alpha#2")); b.Status != LeaseStatusQueued {
		t.Fatalf("B after restart = %+v, want queued behind the surviving holder", b)
	}
	clk.Advance(299 * time.Second) // t0+300
	if b := mustLeaseAcquire(t, m2, leaseReq("r-b", key, "m-b", "alpha#2")); b.Status != LeaseStatusGranted {
		t.Fatalf("B after the TTL = %+v, want granted", b)
	}
}

// TestLeaseConcurrentAcquire pins AC1's "simultaneous" half: several
// independent callers start on one barrier and race for one key. Exactly
// one is granted, every other one is queued, no call errors, and the queue
// positions are a distinct 1..n-1 — a read-then-write split (no single
// compare-and-write transaction) turns the extra callers into errors or a
// second grant.
func TestLeaseConcurrentAcquire(t *testing.T) {
	m, _, _ := newLeaseEnv(t)
	const key = "github.com/o/app\nmain"
	const n = 8
	start := make(chan struct{})
	results := make([]*LeaseGrant, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			results[i], errs[i] = m.Acquire(leaseReq(
				fmt.Sprintf("r-%d", i), key, fmt.Sprintf("m-%d", i), fmt.Sprintf("alpha#%d", i)))
		}(i)
	}
	close(start)
	wg.Wait()

	granted, queued := 0, 0
	positions := map[int]bool{}
	for i, g := range results {
		if errs[i] != nil {
			t.Fatalf("caller %d errored: %v", i, errs[i])
		}
		switch g.Status {
		case LeaseStatusGranted:
			granted++
			if !leaseTokenShape.MatchString(g.Token) {
				t.Fatalf("grant %d carries no token: %+v", i, g)
			}
		case LeaseStatusQueued:
			queued++
			positions[g.Position] = true
		default:
			t.Fatalf("caller %d = %+v, want granted or queued", i, g)
		}
	}
	if granted != 1 || queued != n-1 {
		t.Fatalf("grants=%d queues=%d, want exactly 1 granted and %d queued", granted, queued, n-1)
	}
	for p := 1; p < n; p++ {
		if !positions[p] {
			t.Fatalf("queue positions = %v, want the distinct set 1..%d", positions, n-1)
		}
	}
}

// TestLeaseLookupExpired pins the recovered daemon's read: a grant whose
// TTL passed is not reported held. Before a new acquire takes the key, the
// lookup answers CORE-409 LEASE_LOST; after that acquire the request id is
// no longer a lease request at all (CORE-404).
func TestLeaseLookupExpired(t *testing.T) {
	m, _, clk := newLeaseEnv(t)
	const key = "github.com/o/app\nmain"
	if a := mustLeaseAcquire(t, m, leaseReq("r-a", key, "m-a", "alpha#1")); a.Status != LeaseStatusGranted {
		t.Fatalf("A = %+v, want granted", a)
	}
	clk.Advance(300 * time.Second)
	leaseWantErr(t, func() error { _, err := m.Lookup("r-a", "m-a", "alpha#1"); return err }(),
		api.ErrCoreConflict, "LEASE_LOST")
	b := mustLeaseAcquire(t, m, leaseReq("r-b", key, "m-b", "alpha#2"))
	if b.Status != LeaseStatusGranted {
		t.Fatalf("B = %+v, want granted after the expiry", b)
	}
	leaseWantErr(t, func() error { _, err := m.Lookup("r-a", "m-a", "alpha#1"); return err }(),
		api.ErrCoreNotFound, "LEASE_NOT_QUEUED")
}
