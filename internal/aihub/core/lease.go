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
	"strings"
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
	cfg, err := m.config(req.Kind)
	if err != nil {
		return nil, err
	}
	if req.Key == "" || req.RequestID == "" || req.MachineID == "" || req.WorkItemID == "" {
		return nil, Fail(api.ErrCoreBadInput, "LEASE_BAD_REQUEST",
			"a lease request carries a key, a request id, a machine and a work item")
	}
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := m.now().UnixNano()
	cutoff := now - int64(cfg.WaiterTTL)

	// Evict the waiters that stopped polling: a waiter whose last poll is
	// exactly the TTL old is out (the same >= boundary as the lease).
	if _, err := tx.Exec(`DELETE FROM lease_waiters
		WHERE kind=?1 AND key=?2 AND last_poll_at_ns<=?3`, req.Kind, req.Key, cutoff); err != nil {
		return nil, err
	}

	holder, err := loadLeaseHolder(tx, req.Kind, req.Key)
	if err != nil {
		return nil, err
	}
	if holder != nil && holder.expiresAtNS <= now {
		// The lease expired: the key is free, the superseded holder's
		// renew/release answers lost from here on.
		if _, err := tx.Exec(`DELETE FROM leases WHERE kind=?1 AND key=?2`, req.Kind, req.Key); err != nil {
			return nil, err
		}
		holder = nil
	}
	if holder != nil {
		if holder.requestID == req.RequestID {
			if holder.machineID != req.MachineID || holder.workItem != req.WorkItemID {
				return nil, Fail(api.ErrCoreConflict, "LEASE_REQUEST_CONFLICT",
					"request id %s belongs to another machine and work item", req.RequestID)
			}
			if err := tx.Commit(); err != nil {
				return nil, err
			}
			return heldGrant(holder), nil
		}
		// A live holder: this request only joins (or keeps) its place in
		// the queue. The grant itself waits for the holder's expiry.
		waiter, err := ensureWaiter(tx, req, now)
		if err != nil {
			return nil, err
		}
		pos, err := waiterPosition(tx, req.Kind, req.Key, waiter.seq)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &LeaseGrant{Kind: req.Kind, Key: req.Key, RequestID: req.RequestID,
			Status: LeaseStatusQueued, Position: pos}, nil
	}

	// The key is free. A request id that already holds another key (or
	// the same one, after an expiry race) is the one cycle: answer it
	// instead of queueing it twice.
	granted, err := loadLeaseByRequest(tx, req.RequestID)
	if err != nil {
		return nil, err
	}
	if granted != nil {
		if granted.kind != req.Kind || granted.key != req.Key ||
			granted.machineID != req.MachineID || granted.workItem != req.WorkItemID {
			return nil, Fail(api.ErrCoreConflict, "LEASE_REQUEST_CONFLICT",
				"request id %s already names another lease request", req.RequestID)
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return heldGrant(granted), nil
	}

	waiter, err := ensureWaiter(tx, req, now)
	if err != nil {
		return nil, err
	}
	head, err := queueHead(tx, req.Kind, req.Key)
	if err != nil {
		return nil, err
	}
	if head != req.RequestID {
		pos, err := waiterPosition(tx, req.Kind, req.Key, waiter.seq)
		if err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return &LeaseGrant{Kind: req.Kind, Key: req.Key, RequestID: req.RequestID,
			Status: LeaseStatusQueued, Position: pos}, nil
	}

	token, err := NewSecret(32)
	if err != nil {
		return nil, err
	}
	expires := now + int64(cfg.TTL)
	if _, err := tx.Exec(`INSERT INTO leases(
		kind,key,request_id,machine_id,work_item,operator_id,token_hash,acquired_at_ns,expires_at_ns)
		VALUES (?1,?2,?3,?4,?5,?6,?7,?8,?9)`,
		req.Kind, req.Key, req.RequestID, req.MachineID, req.WorkItemID, req.OperatorID,
		Sha256Hex(token), now, expires); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(`DELETE FROM lease_waiters WHERE request_id=?1`, req.RequestID); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &LeaseGrant{Kind: req.Kind, Key: req.Key, RequestID: req.RequestID,
		Status: LeaseStatusGranted, Token: token,
		HeldSince: stamp(now), ExpiresAt: stamp(expires),
		QueuedMS: (now - waiter.requestedAtNS) / int64(time.Millisecond)}, nil
}

// Renew extends the holder's lease by the kind's TTL. A token that does
// not match the current holder is refused; a lease that already expired
// is lost (CORE-409) and frees the key.
func (m *LeaseManager) Renew(req LeaseRequest, token string) (*LeaseGrant, error) {
	cfg, err := m.config(req.Kind)
	if err != nil {
		return nil, err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := m.now().UnixNano()
	holder, err := loadLeaseHolder(tx, req.Kind, req.Key)
	if err != nil {
		return nil, err
	}
	if holder == nil {
		return nil, Fail(api.ErrCoreConflict, "LEASE_LOST", "lease %s/%s is not held", req.Kind, req.Key)
	}
	if holder.expiresAtNS <= now {
		if _, err := tx.Exec(`DELETE FROM leases WHERE kind=?1 AND key=?2`, req.Kind, req.Key); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return nil, Fail(api.ErrCoreConflict, "LEASE_LOST", "lease %s/%s expired", req.Kind, req.Key)
	}
	if Sha256Hex(token) != holder.tokenHash {
		return nil, Fail(api.ErrAuthForbidden, "LEASE_TOKEN_MISMATCH",
			"the token does not match the lease holder")
	}
	if holder.requestID != req.RequestID || holder.machineID != req.MachineID ||
		holder.workItem != req.WorkItemID {
		return nil, Fail(api.ErrCoreConflict, "LEASE_REQUEST_CONFLICT",
			"the token belongs to another request")
	}
	expires := now + int64(cfg.TTL)
	if _, err := tx.Exec(`UPDATE leases SET expires_at_ns=?1 WHERE kind=?2 AND key=?3`,
		expires, req.Kind, req.Key); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	holder.expiresAtNS = expires
	return &LeaseGrant{Kind: holder.kind, Key: holder.key, RequestID: holder.requestID,
		Status: LeaseStatusGranted, HeldSince: stamp(holder.acquiredAtNS), ExpiresAt: stamp(expires)}, nil
}

// Release gives the lease back. A non-empty token releases the holder
// after the token check; an empty token releases by request id — the
// restart-recovery / withdraw path, authorized by the caller's machine
// and work item — and can never free another holder's lease.
func (m *LeaseManager) Release(req LeaseRequest, token string) (*LeaseGrant, error) {
	if _, err := m.config(req.Kind); err != nil {
		return nil, err
	}
	tx, err := m.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	now := m.now().UnixNano()

	released := &LeaseGrant{Kind: req.Kind, Key: req.Key, RequestID: req.RequestID, Status: LeaseStatusReleased}
	holder, err := loadLeaseHolder(tx, req.Kind, req.Key)
	if err != nil {
		return nil, err
	}
	waiter, err := loadLeaseWaiter(tx, req.RequestID)
	if err != nil {
		return nil, err
	}

	// Withdrawing a queued request needs no token: the request id, the
	// machine and the ticket together are the caller's proof.
	if waiter != nil && (holder == nil || holder.requestID != req.RequestID) {
		if waiter.kind != req.Kind || waiter.key != req.Key ||
			waiter.machineID != req.MachineID || waiter.workItem != req.WorkItemID {
			return nil, Fail(api.ErrAuthForbidden, "LEASE_TOKEN_MISMATCH",
				"the request id belongs to another machine and work item")
		}
		if _, err := tx.Exec(`DELETE FROM lease_waiters WHERE request_id=?1`, req.RequestID); err != nil {
			return nil, err
		}
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return released, nil
	}

	if holder != nil && holder.expiresAtNS <= now {
		if _, err := tx.Exec(`DELETE FROM leases WHERE kind=?1 AND key=?2`, req.Kind, req.Key); err != nil {
			return nil, err
		}
		holder = nil
	}
	if holder == nil {
		if err := tx.Commit(); err != nil {
			return nil, err
		}
		return released, nil
	}
	if token == "" {
		if holder.requestID != req.RequestID || holder.machineID != req.MachineID ||
			holder.workItem != req.WorkItemID {
			return nil, Fail(api.ErrAuthForbidden, "LEASE_TOKEN_REQUIRED",
				"releasing the holder requires its token")
		}
	} else {
		if Sha256Hex(token) != holder.tokenHash {
			return nil, Fail(api.ErrAuthForbidden, "LEASE_TOKEN_MISMATCH",
				"the token does not match the lease holder")
		}
		if holder.requestID != req.RequestID || holder.machineID != req.MachineID ||
			holder.workItem != req.WorkItemID {
			return nil, Fail(api.ErrCoreConflict, "LEASE_REQUEST_CONFLICT",
				"the token belongs to another request")
		}
	}
	if _, err := tx.Exec(`DELETE FROM leases WHERE kind=?1 AND key=?2`, req.Kind, req.Key); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return released, nil
}

// Lookup answers the grant one request id holds for this machine and work
// item, or CORE-404 when it holds none. The plaintext token is never
// returned: a recovered daemon releases by request id.
func (m *LeaseManager) Lookup(requestID, machineID, workItemID string) (*LeaseGrant, error) {
	holder, err := loadLeaseByRequest(m.db, requestID)
	if err != nil {
		return nil, err
	}
	if holder != nil {
		if holder.machineID != machineID || holder.workItem != workItemID {
			return nil, Fail(api.ErrCoreNotFound, "LEASE_NOT_QUEUED",
				"request %s holds no lease for this machine and work item", requestID)
		}
		if holder.expiresAtNS <= m.now().UnixNano() {
			return nil, Fail(api.ErrCoreConflict, "LEASE_LOST",
				"lease %s/%s expired", holder.kind, holder.key)
		}
		return heldGrant(holder), nil
	}
	waiter, err := loadLeaseWaiter(m.db, requestID)
	if err != nil {
		return nil, err
	}
	if waiter == nil || waiter.machineID != machineID || waiter.workItem != workItemID {
		return nil, Fail(api.ErrCoreNotFound, "LEASE_NOT_QUEUED",
			"request %s is not queued for this machine and work item", requestID)
	}
	pos, err := waiterPosition(m.db, waiter.kind, waiter.key, waiter.seq)
	if err != nil {
		return nil, err
	}
	return &LeaseGrant{Kind: waiter.kind, Key: waiter.key, RequestID: waiter.requestID,
		Status: LeaseStatusQueued, Position: pos}, nil
}

// leaseHolder is one row of the leases table.
type leaseHolder struct {
	kind         string
	key          string
	requestID    string
	machineID    string
	workItem     string
	operatorID   string
	tokenHash    string
	acquiredAtNS int64
	expiresAtNS  int64
}

// leaseWaiter is one row of the lease_waiters table.
type leaseWaiter struct {
	seq           int64
	requestID     string
	kind          string
	key           string
	machineID     string
	workItem      string
	operatorID    string
	requestedAtNS int64
	lastPollAtNS  int64
}

// loadLeaseHolder answers the (kind, key) holder, or nil when free.
func loadLeaseHolder(q leaseStmt, kind, key string) (*leaseHolder, error) {
	var h leaseHolder
	err := q.QueryRow(`SELECT kind,key,request_id,machine_id,work_item,operator_id,token_hash,acquired_at_ns,expires_at_ns
		FROM leases WHERE kind=?1 AND key=?2`, kind, key).
		Scan(&h.kind, &h.key, &h.requestID, &h.machineID, &h.workItem, &h.operatorID,
			&h.tokenHash, &h.acquiredAtNS, &h.expiresAtNS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// loadLeaseByRequest answers the grant one request id holds, or nil.
func loadLeaseByRequest(q leaseStmt, requestID string) (*leaseHolder, error) {
	var h leaseHolder
	err := q.QueryRow(`SELECT kind,key,request_id,machine_id,work_item,operator_id,token_hash,acquired_at_ns,expires_at_ns
		FROM leases WHERE request_id=?1`, requestID).
		Scan(&h.kind, &h.key, &h.requestID, &h.machineID, &h.workItem, &h.operatorID,
			&h.tokenHash, &h.acquiredAtNS, &h.expiresAtNS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &h, nil
}

// loadLeaseWaiter answers one queued request, or nil.
func loadLeaseWaiter(q leaseStmt, requestID string) (*leaseWaiter, error) {
	var w leaseWaiter
	err := q.QueryRow(`SELECT seq,request_id,kind,key,machine_id,work_item,operator_id,requested_at_ns,last_poll_at_ns
		FROM lease_waiters WHERE request_id=?1`, requestID).
		Scan(&w.seq, &w.requestID, &w.kind, &w.key, &w.machineID, &w.workItem,
			&w.operatorID, &w.requestedAtNS, &w.lastPollAtNS)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &w, nil
}

// ensureWaiter inserts the request's queue row or refreshes its poll
// stamp, answering the row as it now stands. A request id already used
// for another key, machine or ticket is refused.
func ensureWaiter(tx *sql.Tx, req LeaseRequest, now int64) (*leaseWaiter, error) {
	w, err := loadLeaseWaiter(tx, req.RequestID)
	if err != nil {
		return nil, err
	}
	if w != nil {
		if w.kind != req.Kind || w.key != req.Key || w.machineID != req.MachineID ||
			w.workItem != req.WorkItemID {
			return nil, Fail(api.ErrCoreConflict, "LEASE_REQUEST_CONFLICT",
				"request id %s already names another lease request", req.RequestID)
		}
		if _, err := tx.Exec(`UPDATE lease_waiters SET last_poll_at_ns=?1 WHERE request_id=?2`,
			now, req.RequestID); err != nil {
			return nil, err
		}
		w.lastPollAtNS = now
		return w, nil
	}
	res, err := tx.Exec(`INSERT INTO lease_waiters(
		request_id,kind,key,machine_id,work_item,operator_id,requested_at_ns,last_poll_at_ns)
		VALUES (?1,?2,?3,?4,?5,?6,?7,?7)`,
		req.RequestID, req.Kind, req.Key, req.MachineID, req.WorkItemID, req.OperatorID, now)
	if err != nil {
		return nil, err
	}
	seq, err := res.LastInsertId()
	if err != nil {
		return nil, err
	}
	return &leaseWaiter{seq: seq, requestID: req.RequestID, kind: req.Kind, key: req.Key,
		machineID: req.MachineID, workItem: req.WorkItemID, operatorID: req.OperatorID,
		requestedAtNS: now, lastPollAtNS: now}, nil
}

// queueHead answers the request id at the head of one key's queue ("" when
// empty).
func queueHead(q leaseStmt, kind, key string) (string, error) {
	var id string
	err := q.QueryRow(`SELECT request_id FROM lease_waiters WHERE kind=?1 AND key=?2
		ORDER BY seq LIMIT 1`, kind, key).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

// waiterPosition answers a queue row's 1-based rank.
func waiterPosition(q leaseStmt, kind, key string, seq int64) (int, error) {
	var n int
	err := q.QueryRow(`SELECT COUNT(*) FROM lease_waiters
		WHERE kind=?1 AND key=?2 AND seq<=?3`, kind, key, seq).Scan(&n)
	return n, err
}

// heldGrant is the answer of a request that already holds the lease: the
// state and the stamps, never the plaintext token.
func heldGrant(h *leaseHolder) *LeaseGrant {
	return &LeaseGrant{Kind: h.kind, Key: h.key, RequestID: h.requestID,
		Status: LeaseStatusHeld, HeldSince: stamp(h.acquiredAtNS), ExpiresAt: stamp(h.expiresAtNS)}
}

// stamp formats one server-clock nanosecond stamp.
func stamp(ns int64) string {
	return time.Unix(0, ns).UTC().Format(time.RFC3339Nano)
}

// leaseStmt is a small alias so the transaction helpers below read the
// same whether they run on the database or a transaction.
type leaseStmt interface {
	Exec(query string, args ...any) (sql.Result, error)
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// LandingSnapshot answers one ticket's read-only landing-lock projection.
func (m *LeaseManager) LandingSnapshot(workItemID string) (*api.LandingLock, error) {
	now := m.now().UnixNano()
	return &api.LandingLock{State: api.LandingLockNone, ObservedAt: stamp(now), AsOf: stamp(now)}, nil
}
