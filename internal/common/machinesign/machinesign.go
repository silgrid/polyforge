// Package machinesign is the one signing scheme of the cross-machine
// machine face: Ed25519 over
//
//	METHOD\nPATH\nsha256hex(body)\nTIMESTAMP\nMACHINE_ID
//
// with the three X-PF-* headers, a ±300 s clock window and a
// bounded anti-replay cache (512 live entries per bucket, 600 s TTL;
// aihub keys one bucket per machine id per signed-route lane —
// A2; no global eviction — every check sweeps
// every bucket's expired entries and drops the empty buckets, so memory
// follows live entries; an unexpired entry is never evicted). Both aihub
// (verify) and the daemon (sign) use this package, so the canonical
// string cannot drift.
//
// Note: PATH is the URL path only; query parameters are NOT covered by the
// signature (the inbox faces/wait parameters are unsigned today).
package machinesign

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Header names carried by every signed machine-face request.
const (
	HeaderMachineID = "X-PF-Machine-ID"
	HeaderTimestamp = "X-PF-Timestamp"
	HeaderSignature = "X-PF-Signature"
)

// Window is the accepted clock skew (seconds, either direction).
const Window = 300

// Per-bucket replay quota and the entry TTL. aihub keys a bucket by
// machine id AND signed-route lane (server/machineface.go,
// A2): the heartbeat, the inbox long-poll, the relay answer and the
// outgoing query each fill their own bucket, so traffic another member
// can induce (answers to its queries, inbox wakes) never shares the
// heartbeat's quota. A signature covers its method and path and a path
// is served by exactly one route of one lane, so a signature is only
// ever checked in one bucket — the per-lane duplicate check is exactly
// as strong as a per-machine one.
//
// Derivation ( D1, per lane since): a daemon
// beats ≤11 times per TTL and each signed inbox loop (the query loop, and
// on an interactive daemon the message loop) idles at one 25 s poll —
// ≤24 per TTL per loop — so a correctly behaving daemon cannot reach the
// heartbeat lane's quota and no other party can add an entry to it. The
// answer and inbox lanes grow by one entry per relayed query; aihub
// bounds relayed queries at enqueue so they stay under the quota too
// (server/machineinbox.go relayBudget). Only a machine sustaining over
// ~0.85 signed requests/second on one route class (512 live entries in a
// 600 s TTL) for a full TTL is refused (CORE-429) on that class only, and
// it recovers by itself: only its own oldest entries expire (≤600 s) and
// a refused request records nothing, so refusal cannot extend its own
// quota.
//
// Memory bound: per bucket at most ReplayQuota entries ≈300 B each
// ≈150 KiB for a saturating bucket (≈600 KiB for a machine id saturating
// all four aihub lanes); the total is 512 × (buckets holding entries
// younger than two TTLs) — Check prunes the caller's own bucket on every
// check and sweeps every bucket once per TTL, so an idle machine's expired
// entries are dropped by the first check after a TTL since the last sweep
// and its empty bucket goes with them; the bound counts buckets holding
// recent entries, not ids seen since startup.
// Ids are membership-gated (registration) and an entry is recorded only
// after the signature verifies. There is no global eviction and no global
// cap — that is exactly what "never evict an unexpired entry" costs.
const (
	ReplayQuota = 512
	ReplayTTL   = 600 * time.Second
)

// Canonical builds the signed string.
func Canonical(method, path string, body []byte, timestamp int64, machineID string) string {
	sum := sha256.Sum256(body)
	return method + "\n" + path + "\n" + hex.EncodeToString(sum[:]) + "\n" +
		strconv.FormatInt(timestamp, 10) + "\n" + machineID
}

// Sign answers the hex signature of the canonical string.
func Sign(key ed25519.PrivateKey, method, path string, body []byte, timestamp int64, machineID string) string {
	return hex.EncodeToString(ed25519.Sign(key, []byte(Canonical(method, path, body, timestamp, machineID))))
}

// VerifyTimestamp answers whether ts is inside the ±Window of now.
func VerifyTimestamp(now time.Time, timestamp int64) bool {
	delta := now.Unix() - timestamp
	if delta < 0 {
		delta = -delta
	}
	return delta <= Window
}

// Verify checks the signature, the timestamp window and the replay cache.
// It answers (accepted, error). A replay is a valid signature seen before
// (machine + signature hex); nothing takes effect on a refused signature.
// A non-nil replay is checked in the bucket named by the machine id
// itself; aihub passes nil and records each signature in its machine ×
// lane bucket only after authorization (server/machineface.go).
func Verify(publicKey ed25519.PublicKey, now time.Time, method, path string, body []byte, timestamp int64, machineID, signatureHex string, replay *ReplayCache) bool {
	if !VerifyTimestamp(now, timestamp) {
		return false
	}
	sig, err := hex.DecodeString(strings.TrimSpace(signatureHex))
	if err != nil || len(sig) != ed25519.SignatureSize {
		return false
	}
	if !ed25519.Verify(publicKey, []byte(Canonical(method, path, body, timestamp, machineID)), sig) {
		return false
	}
	if replay != nil && replay.Check(now, machineID, signatureHex) != ReplayFresh {
		return false
	}
	return true
}

// ReplayVerdict is Check's answer.
type ReplayVerdict int

const (
	ReplayFresh     ReplayVerdict = iota // not seen before; now recorded
	ReplayDuplicate                      // verbatim repeat; not re-recorded
	ReplayOverQuota                      // the bucket holds its quota; not recorded
)

// ReplayCache is the in-memory anti-replay set: one bucket per key (aihub:
// machine id × signed-route lane), so one bucket's flood can never flush
// another bucket's entry.
type ReplayCache struct {
	mu    sync.Mutex
	seen  map[string]map[string]time.Time
	quota int
	ttl   time.Duration
	// lastSweep is when every machine's bucket was last pruned; the next
	// all-bucket sweep runs once a TTL has passed since.
	lastSweep time.Time
}

// NewReplayCache answers the plan-pinned cache (512 live entries per
// bucket / 600 s).
func NewReplayCache() *ReplayCache {
	return newReplayCache(ReplayQuota, ReplayTTL)
}

func newReplayCache(quota int, ttl time.Duration) *ReplayCache {
	if quota <= 0 {
		quota = ReplayQuota
	}
	if ttl <= 0 {
		ttl = ReplayTTL
	}
	return &ReplayCache{seen: map[string]map[string]time.Time{}, quota: quota, ttl: ttl}
}

// Check answers the signature's verdict in bucket (one machine id × route
// lane) and records it when fresh. Only entries older than the TTL are
// pruned — an unexpired entry is NEVER evicted, whatever else the cache
// holds. Every check prunes the caller's own bucket (bounded by its quota);
// the other buckets are swept at most once per TTL, amortized inside Check,
// so a request never pays for every bucket: an idle machine still cannot
// pin its expired entries in memory until it signs again or aihub restarts,
// and a bucket that becomes empty is dropped, so memory follows live
// entries (the bound is 512 × buckets holding entries younger than two
// TTLs, not buckets recorded since startup). A repeat answers
// ReplayDuplicate; a bucket at its quota answers ReplayOverQuota for its own
// signatures only (the verdict is per bucket, and an over-quota answer
// records nothing).
func (c *ReplayCache) Check(now time.Time, bucket, signatureHex string) ReplayVerdict {
	c.mu.Lock()
	defer c.mu.Unlock()
	cutoff := now.Add(-c.ttl)
	if !now.Before(c.lastSweep.Add(c.ttl)) {
		for id := range c.seen {
			c.pruneLocked(id, cutoff)
		}
		c.lastSweep = now
	} else {
		c.pruneLocked(bucket, cutoff)
	}
	entries := c.seen[bucket]
	if _, ok := entries[signatureHex]; ok {
		return ReplayDuplicate
	}
	if len(entries) >= c.quota {
		return ReplayOverQuota
	}
	if entries == nil {
		entries = map[string]time.Time{}
		c.seen[bucket] = entries
	}
	entries[signatureHex] = now
	return ReplayFresh
}

// pruneLocked drops one bucket's expired entries, and the bucket once
// empty. The caller holds c.mu.
func (c *ReplayCache) pruneLocked(bucket string, cutoff time.Time) {
	entries, ok := c.seen[bucket]
	if !ok {
		return
	}
	for sig, at := range entries {
		if at.Before(cutoff) {
			delete(entries, sig)
		}
	}
	if len(entries) == 0 {
		delete(c.seen, bucket)
	}
}

// Describe is a small diagnostic used by tests and error details.
func Describe(method, path, machineID string) string {
	return fmt.Sprintf("%s %s machine=%s", method, path, machineID)
}
