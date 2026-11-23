package aihubclient

// landinglock.go: the daemon's client half of the generic lease base
// (polyforge#245, 245-lease). Four machine-signed calls — acquire/poll,
// renew, release and lookup-by-request-id — plus the daemon-private
// request journal: the request ids this daemon owns, so a restart can
// release its grant by request id without ever keeping the plaintext
// token (decided Q6/Q9). The journal holds no token and no credential;
// it is 0600 and written atomically.
//
// Reachability: daemon code only. A step or task agent holds neither an
// aihub key nor the machine key, so it cannot sign a lease call; the
// daemon is the one caller (a step reaches it through `pf step
// lock-check`, which is the next unit).

import (
	"errors"
	"sync"

	"github.com/silgrid/polyforge/internal/common/api"
)

// AcquireLease asks for (or polls) one lease. The answer's Token is the
// plaintext grant token, present only on the poll that granted it.
func (c *Client) AcquireLease(kind string, req api.LeaseAcquire) (*api.LeaseGrant, error) {
	return nil, errLeaseClientNotImplemented
}

// RenewLease extends the holder's lease with the grant token.
func (c *Client) RenewLease(kind string, req api.LeaseRenew) (*api.LeaseGrant, error) {
	return nil, errLeaseClientNotImplemented
}

// ReleaseLease releases the holder (with its token) or withdraws a queued
// request (no token, by request id).
func (c *Client) ReleaseLease(kind string, req api.LeaseRelease) (*api.LeaseGrant, error) {
	return nil, errLeaseClientNotImplemented
}

// LookupLease answers the grant or queue position one request id holds
// for this machine and ticket; the answer never carries the token.
func (c *Client) LookupLease(requestID, workItem string) (*api.LeaseGrant, error) {
	return nil, errLeaseClientNotImplemented
}

// LeaseJournalEntry is one acquisition cycle this daemon owns. Token is
// deliberately absent: a recovered daemon releases by request id.
type LeaseJournalEntry struct {
	RequestID string `json:"request_id"`
	Kind      string `json:"kind"`
	Key       string `json:"key"`
	WorkItem  string `json:"work_item"`
	// Cycle is the caller's non-secret cycle identifier (the daemon's
	// lock period id); it never carries the token.
	Cycle string `json:"cycle,omitempty"`
}

// LeaseJournal is the daemon-private request journal, one JSON file.
type LeaseJournal struct {
	path string
	mu   sync.Mutex
}

// NewLeaseJournal assembles the journal over one path.
func NewLeaseJournal(path string) *LeaseJournal {
	return &LeaseJournal{path: path}
}

// Add records one request id (idempotent: the same id replaces its entry).
func (j *LeaseJournal) Add(e LeaseJournalEntry) error {
	return errLeaseClientNotImplemented
}

// Remove drops one request id (a released cycle).
func (j *LeaseJournal) Remove(requestID string) error {
	return errLeaseClientNotImplemented
}

// Entries answers every recorded cycle.
func (j *LeaseJournal) Entries() ([]LeaseJournalEntry, error) {
	return nil, errLeaseClientNotImplemented
}

var errLeaseClientNotImplemented = errors.New("aihubclient: lease client not implemented")
