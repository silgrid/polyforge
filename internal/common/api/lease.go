package api

// lease.go: the wire contract of the generic lease base (polyforge#245,
// 245-lease). The three signed machine routes —
// POST /api/v1/leases/{kind}/acquire|renew|release and
// GET /api/v1/leases/requests/{rid} — carry these bodies and answer
// LeaseGrant. `work_item` is the ticket whose current session the
// signing machine must hold; the key is the caller's opaque lease key
// (the landing lock passes the normalized repository identity plus the
// target branch).

// LeaseAcquire is one acquire/poll request. A retry with the same
// request_id is idempotent; a new request_id for the same machine and
// ticket is a new queue entry.
type LeaseAcquire struct {
	// Repo and Branch are the landing kind's inputs: the repository (its
	// declared URL or a declared name) and the target branch. The server
	// normalizes them into the lease key, so two daemons spelling one
	// repository differently share one lock. Kinds that carry their own
	// opaque key (polyforge#246) set Key instead.
	Repo      string `json:"repo,omitempty"`
	Branch    string `json:"branch,omitempty"`
	Key       string `json:"key,omitempty"`
	RequestID string `json:"request_id"`
	WorkItem  string `json:"work_item"`
	// Nonce makes every call's signed bytes distinct, so a poll or an
	// immediate idempotent retry is never refused as a replay; the
	// server ignores its value.
	Nonce string `json:"nonce,omitempty"`
}

// LeaseRenew extends the holder's lease. The token is the plaintext one
// the granting poll answered; the server never stores it.
type LeaseRenew struct {
	Key       string `json:"key"`
	RequestID string `json:"request_id"`
	WorkItem  string `json:"work_item"`
	Token     string `json:"token"`
	Nonce     string `json:"nonce,omitempty"`
}

// LeaseRelease gives the lease or the queued request back. An empty token
// is the release-by-request-id path (a restarted daemon, or a waiter
// withdrawing); it is limited to the same machine, ticket and request id.
type LeaseRelease struct {
	Key       string `json:"key"`
	RequestID string `json:"request_id"`
	WorkItem  string `json:"work_item"`
	Token     string `json:"token,omitempty"`
	Nonce     string `json:"nonce,omitempty"`
}

// LeaseGrant is the answer of acquire/renew/release/lookup. Token is
// present exactly once, on the poll that granted the lease.
type LeaseGrant struct {
	Kind      string `json:"kind"`
	Key       string `json:"key"`
	RequestID string `json:"request_id"`
	Status    string `json:"status"`
	Token     string `json:"token,omitempty"`
	Position  int    `json:"position,omitempty"`
	HeldSince string `json:"held_since,omitempty"`
	ExpiresAt string `json:"expires_at,omitempty"`
	QueuedMS  int64  `json:"queued_ms,omitempty"`
}
