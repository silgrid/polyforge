package api

// landing_lock.go: the read-only landing-lock projection (polyforge#245
// epic 3/3, 245-observe). Stub of the wire contract; the served projection
// is derived from the lease tables on every read (Q11).

import "time"

// Landing-lock projection states.
const (
	LandingLockWaiting     = "waiting"
	LandingLockNone        = "none"
	LandingLockStale       = "stale"
	LandingLockUnavailable = "unavailable"
)

// LandingLockStaleAfter is the ticket's decided freshness window (Q2).
const LandingLockStaleAfter = 15 * time.Second

// LandingLock is one ticket's read-only landing-lock projection.
type LandingLock struct {
	State      string           `json:"state"`
	ObservedAt string           `json:"observed_at,omitempty"`
	AsOf       string           `json:"as_of,omitempty"`
	AgeMS      int64            `json:"age_ms,omitempty"`
	Keys       []LandingLockKey `json:"keys,omitempty"`
}

// LandingLockKey is one lock key this ticket queues on.
type LandingLockKey struct {
	Repo     string `json:"repo"`
	Branch   string `json:"branch"`
	Position int    `json:"position"`
	Since    string `json:"since,omitempty"`
}

// Waiting answers whether the projection shows a live queue entry.
func (l *LandingLock) Waiting() bool {
	return false
}
