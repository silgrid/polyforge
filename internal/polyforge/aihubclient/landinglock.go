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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/silgrid/polyforge/internal/common/api"
)

// AcquireLease asks for (or polls) one lease. The answer's Token is the
// plaintext grant token, present only on the poll that granted it.
func (c *Client) AcquireLease(kind string, req api.LeaseAcquire) (*api.LeaseGrant, error) {
	req.Nonce = leaseNonce()
	return c.leasePost(kind, "acquire", req)
}

// RenewLease extends the holder's lease with the grant token.
func (c *Client) RenewLease(kind string, req api.LeaseRenew) (*api.LeaseGrant, error) {
	req.Nonce = leaseNonce()
	return c.leasePost(kind, "renew", req)
}

// ReleaseLease releases the holder (with its token) or withdraws a queued
// request (no token, by request id).
func (c *Client) ReleaseLease(kind string, req api.LeaseRelease) (*api.LeaseGrant, error) {
	req.Nonce = leaseNonce()
	return c.leasePost(kind, "release", req)
}

// LookupLease answers the grant or queue position one request id holds
// for this machine and ticket; the answer never carries the token.
func (c *Client) LookupLease(requestID, workItem string) (*api.LeaseGrant, error) {
	var out api.LeaseGrant
	path := "/api/v1/leases/requests/" + url.PathEscape(requestID) + "?work_item=" + url.QueryEscape(workItem)
	if err := c.SignedDo("GET", path, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// leaseNonce answers one call's random nonce: every poll and every
// idempotent retry signs distinct bytes, so the anti-replay cache never
// mistakes a legitimate poll for a captured request.
func leaseNonce() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return ""
	}
	return hex.EncodeToString(b[:])
}

// leasePost is the one signed POST body of the three lease verbs.
func (c *Client) leasePost(kind, verb string, body any) (*api.LeaseGrant, error) {
	var out api.LeaseGrant
	path := "/api/v1/leases/" + url.PathEscape(kind) + "/" + verb
	if err := c.SignedDo("POST", path, body, &out); err != nil {
		return nil, err
	}
	return &out, nil
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
	if strings.TrimSpace(e.RequestID) == "" {
		return errors.New("aihubclient: lease journal entry carries no request id")
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	entries, err := j.read()
	if err != nil {
		return err
	}
	replaced := false
	for i := range entries {
		if entries[i].RequestID == e.RequestID {
			entries[i] = e
			replaced = true
			break
		}
	}
	if !replaced {
		entries = append(entries, e)
	}
	return j.write(entries)
}

// Remove drops one request id (a released cycle); an unknown id is a
// no-op.
func (j *LeaseJournal) Remove(requestID string) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	entries, err := j.read()
	if err != nil {
		return err
	}
	kept := entries[:0]
	for _, e := range entries {
		if e.RequestID != requestID {
			kept = append(kept, e)
		}
	}
	return j.write(kept)
}

// Entries answers every recorded cycle, in insertion order.
func (j *LeaseJournal) Entries() ([]LeaseJournalEntry, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.read()
}

// read answers the journal file (an absent or empty file is no entry).
func (j *LeaseJournal) read() ([]LeaseJournalEntry, error) {
	raw, err := os.ReadFile(j.path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("aihubclient: read lease journal: %w", err)
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return nil, nil
	}
	var entries []LeaseJournalEntry
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("aihubclient: lease journal %s is not JSON: %w", j.path, err)
	}
	return entries, nil
}

// write replaces the journal atomically: a 0600 temporary file in the same
// directory, fsynced, renamed over the old one. A crash leaves either the
// old or the new file, never a half one.
func (j *LeaseJournal) write(entries []LeaseJournalEntry) error {
	if entries == nil {
		entries = []LeaseJournalEntry{}
	}
	raw, err := json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	dir := filepath.Dir(j.path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("aihubclient: lease journal dir: %w", err)
	}
	tmp, err := os.CreateTemp(dir, ".lease-journal-*")
	if err != nil {
		return fmt.Errorf("aihubclient: lease journal temp: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, j.path)
}
