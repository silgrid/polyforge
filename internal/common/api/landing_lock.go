package api

// landing_lock.go: the read-only landing-lock projection (polyforge#245
// epic 3/3, 245-observe). A ticket that waits for the server landing lock
// exposes only its own queue state: a state word, the source observation
// times and one entry per lock key it has actually queued on. The holder,
// the request id, the machine and the token never cross this boundary
// (Q11); the projection is derived from the lease tables on every read and
// is never a second authority for the lock.
//
// States: waiting (a fresh snapshot with at least one live queue entry),
// none (a fresh snapshot that proves this ticket queues on no key), stale
// (a cached snapshot whose source is older than LeaseStaleAfter) and
// unavailable (no evidence at all). A reader that predates this field
// ignores it: every field is optional on the wire.

import "time"

// Landing-lock projection states.
const (
	LeaseWaiting     = "waiting"
	LeaseNone        = "none"
	LeaseStale       = "stale"
	LeaseUnavailable = "unavailable"
)

// LeaseStaleAfter is the ticket's decided freshness window (Q2): three
// 5 s daemon poll cycles. A cached projection older than it is stale.
const LeaseStaleAfter = 15 * time.Second

// LeaseProjection is one ticket's read-only landing-lock projection. ObservedAt
// is when the server observed the queue; AsOf is the server's answer time.
// A consumer derives the source age from their difference plus its own
// monotonic elapsed time after receipt — it never subtracts two machines'
// wall clocks. AgeMS is that consumer-computed age when the snapshot was
// served (set on the wire only by a caching reader).
type LeaseProjection struct {
	State      string               `json:"state"`
	ObservedAt string               `json:"observed_at,omitempty"`
	AsOf       string               `json:"as_of,omitempty"`
	AgeMS      int64                `json:"age_ms,omitempty"`
	Keys       []LeaseProjectionKey `json:"keys,omitempty"`
}

// LeaseProjectionKey is one lock key this ticket queues on: the normalized
// repository identity, the target branch and the 1-based position among the
// live waiters (the head is 1; the holder is not a position). Since is the
// server's first-seen stamp of the queue entry.
type LeaseProjectionKey struct {
	Repo     string `json:"repo"`
	Branch   string `json:"branch"`
	Position int    `json:"position"`
	Since    string `json:"since,omitempty"`
}

// Waiting answers whether the projection shows a live queue entry.
func (l *LeaseProjection) Waiting() bool {
	return l != nil && len(l.Keys) > 0 && (l.State == LeaseWaiting || l.State == LeaseStale)
}
