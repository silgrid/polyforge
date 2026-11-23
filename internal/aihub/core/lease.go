package core

// lease.go: the generic server-side lease base (polyforge#245's lease
// unit, epic #245 · 245-lease). A lease is one holder per (kind, key)
// with a server-clock expiry, a FIFO queue of waiters, a token minted
// per grant and stored only as its SHA-256 digest, and an idempotent
// request id. The key and the holder are opaque strings the caller
// supplies; the landing lock (`kind` "landing") is the first user, and
// polyforge#246 reuses the same base under another kind.
//
// Time semantics: every expiry and liveness decision is the server's
// clock. The request bodies carry no client time. A waiter is evicted
// when it has not polled for the kind's waiter TTL (>=), and a lease
// expires when its TTL has passed (>=): the two boundaries agree.
//
// Reachability: the core layer only. The signed machine face
// (server/landinglock.go) is the one network caller; a step or task
// agent reaches it only through the daemon's machine key.

import (
	"database/sql"
	"errors"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// LeaseConfig is one lease kind's time envelope. The landing kind ships
// 300 s / 60 s (the ticket's decided values); polyforge#246 configures
// its own kind through NewLeaseManager + ConfigureKind.
type LeaseConfig struct {
	// TTL is how long a grant lasts without renewal.
	TTL time.Duration
	// WaiterTTL is how long a queued request may go unpolled before it
	// leaves the queue.
	WaiterTTL time.Duration
}

// DefaultLeaseKinds is the built-in kind table: the landing lock's
// decided 300 s lease and 60 s waiter liveness.
func DefaultLeaseKinds() map[string]LeaseConfig {
	return map[string]LeaseConfig{
		"landing": {TTL: 300 * time.Second, WaiterTTL: 60 * time.Second},
	}
}

// Lease statuses on the wire (the request's answer).
const (
	LeaseStatusGranted  = "granted"  // this answer carries the token
	LeaseStatusHeld     = "held"     // you already hold it; no token again
	LeaseStatusQueued   = "queued"   // waiters ahead of you (position >= 1)
	LeaseStatusReleased = "released" // release answered; the lease is free
)

// LeaseRequest is one caller's identity for a lease operation. Kind and
// Key name the lease; RequestID is the caller's idempotency key for one
// acquisition cycle; MachineID/WorkItemID/OperatorID are the holder the
// server authorizes and records.
type LeaseRequest struct {
	Kind       string
	Key        string
	RequestID  string
	MachineID  string
	WorkItemID string
	OperatorID string
}

// LeaseGrant is the wire answer of acquire/renew/release/lookup.
type LeaseGrant struct {
	Kind      string `json:"kind"`
	Key       string `json:"key"`
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	// Token is the plaintext grant token, answered exactly once: on the
	// poll that granted the lease. Every later answer carries none.
	Token string `json:"token,omitempty"`
	// Position is the 1-based rank among the waiters (head = 1) when
	// queued.
	Position int `json:"position,omitempty"`
	// HeldSince and ExpiresAt are the server-clock stamps of the grant.
	HeldSince string `json:"held_since,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	// QueuedMS is how long the request waited before this grant.
	QueuedMS int64 `json:"queued_ms,omitempty"`
}

// LeaseManager is the lease store over one Core database.
type LeaseManager struct {
	db    *DB
	now   func() time.Time
	kinds map[string]LeaseConfig
}

// NewLeaseManager assembles the manager with the built-in kinds.
func NewLeaseManager(db *DB) *LeaseManager {
	return &LeaseManager{db: db, now: time.Now, kinds: DefaultLeaseKinds()}
}

// SetClock injects the clock (tests drive every expiry decision; the
// server keeps time.Now).
func (m *LeaseManager) SetClock(now func() time.Time) {
	if now != nil {
		m.now = now
	}
}

// ConfigureKind installs one kind's time envelope. It is setup-time only
// (before the manager serves requests), the daemon and its tests
// included.
func (m *LeaseManager) ConfigureKind(kind string, cfg LeaseConfig) {
	m.kinds[kind] = cfg
}

// config answers one kind's envelope, or CORE-400 for a kind the server
// does not serve.
func (m *LeaseManager) config(kind string) (LeaseConfig, error) {
	cfg, ok := m.kinds[kind]
	if !ok || cfg.TTL <= 0 || cfg.WaiterTTL <= 0 {
		return LeaseConfig{}, Fail(api.ErrCoreBadInput, "LEASE_UNKNOWN_KIND", "lease kind %q is not configured", kind)
	}
	return cfg, nil
}

// Acquire asks for the lease and answers the request's state: granted
// (with the one-time token), held (the idempotent retry of a grant), or
// queued (with its position). The grant is made inside this request's own
// transaction, so a release never pushes a grant to another caller.
func (m *LeaseManager) Acquire(req LeaseRequest) (*LeaseGrant, error) {
	return nil, errors.New("lease: not implemented")
}

// Renew extends the holder's lease by the kind's TTL. A token that does
// not match the current holder is refused; a lease that already expired
// is lost (CORE-409) and frees the key.
func (m *LeaseManager) Renew(req LeaseRequest, token string) (*LeaseGrant, error) {
	return nil, errors.New("lease: not implemented")
}

// Release gives the lease back. A non-empty token releases the holder
// after the token check; an empty token releases by request id — the
// restart-recovery / withdraw path, authorized by the caller's machine
// and work item — and can never free another holder's lease.
func (m *LeaseManager) Release(req LeaseRequest, token string) (*LeaseGrant, error) {
	return nil, errors.New("lease: not implemented")
}

// Lookup answers the grant one request id holds for this machine and work
// item, or CORE-404 when it holds none. The plaintext token is never
// returned: a recovered daemon releases by request id.
func (m *LeaseManager) Lookup(requestID, machineID, workItemID string) (*LeaseGrant, error) {
	return nil, errors.New("lease: not implemented")
}

// leaseStmt is a small alias so the transaction helpers below read the
// same whether they run on the database or a transaction.
type leaseStmt interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}
