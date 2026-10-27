// Package machinesign is the one signing scheme of the cross-machine
// machine face (workbench#11): Ed25519 over
//
//	METHOD\nPATH\nsha256hex(body)\nTIMESTAMP\nMACHINE_ID
//
// with the three X-PF-* headers, a ±300 s clock window and a bounded
// anti-replay cache (4096 entries, 600 s TTL). Both aihub (verify) and the
// daemon (sign) use this package, so the canonical string cannot drift.
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

// Replay capacity and TTL.
const (
	ReplayCapacity = 4096
	ReplayTTL      = 600 * time.Second
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
	if replay != nil && !replay.Check(now, machineID, signatureHex) {
		return false
	}
	return true
}

// ReplayCache is a bounded in-memory anti-replay set. Check records the
// signature and answers false on a verbatim repeat.
type ReplayCache struct {
	mu   sync.Mutex
	seen map[string]time.Time
	cap  int
	ttl  time.Duration
}

// NewReplayCache answers the plan-pinned cache (4096 entries / 600 s).
func NewReplayCache() *ReplayCache {
	return newReplayCache(ReplayCapacity, ReplayTTL)
}

func newReplayCache(capacity int, ttl time.Duration) *ReplayCache {
	if capacity <= 0 {
		capacity = ReplayCapacity
	}
	if ttl <= 0 {
		ttl = ReplayTTL
	}
	return &ReplayCache{seen: map[string]time.Time{}, cap: capacity, ttl: ttl}
}

// Check answers true when the signature had not been seen (and records
// it), false on a repeat. Expired entries are pruned first; when the cache
// is at capacity the oldest entry is evicted (bounded memory).
func (c *ReplayCache) Check(now time.Time, machineID, signatureHex string) bool {
	key := machineID + "\n" + signatureHex
	c.mu.Lock()
	defer c.mu.Unlock()
	cutoff := now.Add(-c.ttl)
	oldestKey, oldestAt := "", now
	for k, at := range c.seen {
		if at.Before(cutoff) {
			delete(c.seen, k)
			continue
		}
		if oldestKey == "" || at.Before(oldestAt) {
			oldestKey, oldestAt = k, at
		}
	}
	if _, ok := c.seen[key]; ok {
		return false
	}
	if len(c.seen) >= c.cap && oldestKey != "" {
		delete(c.seen, oldestKey)
	}
	c.seen[key] = now
	return true
}

// Describe is a small diagnostic used by tests and error details.
func Describe(method, path, machineID string) string {
	return fmt.Sprintf("%s %s machine=%s", method, path, machineID)
}
