package machinesign

import (
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"testing"
	"time"
)

func TestSignVerifyWindowAndReplay(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	now := time.Unix(1_700_000_000, 0)
	body := []byte(`{"x":1}`)
	sig := Sign(priv, "POST", "/api/v1/x", body, now.Unix(), "m1")
	cache := NewReplayCache()
	if !Verify(pub, now, "POST", "/api/v1/x", body, now.Unix(), "m1", sig, cache) {
		t.Fatal("valid signature refused")
	}
	if Verify(pub, now, "POST", "/api/v1/x", body, now.Unix(), "m1", sig, cache) {
		t.Fatal("replay accepted")
	}
	if Verify(pub, now, "POST", "/api/v1/x", body, now.Unix()-Window-1, "m1", sig, nil) {
		t.Fatal("stale timestamp accepted")
	}
	if Verify(pub, now, "POST", "/api/v1/x", body, now.Unix()+Window+1, "m1", sig, nil) {
		t.Fatal("future timestamp accepted")
	}
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	if Verify(otherPub, now, "POST", "/api/v1/x", body, now.Unix(), "m1", sig, nil) {
		t.Fatal("wrong key accepted")
	}
	if Verify(pub, now, "GET", "/api/v1/x", body, now.Unix(), "m1", sig, nil) {
		t.Fatal("method mismatch accepted")
	}
}

func TestReplayCachePrunesOnlyExpiredEntries(t *testing.T) {
	c := newReplayCache(4, time.Minute)
	now := time.Unix(1_700_000_000, 0)
	if c.Check(now, "m", "a") != ReplayFresh {
		t.Fatal("first signature refused")
	}
	// 'a' expires; 'b' stays live in the same bucket.
	if c.Check(now.Add(90*time.Second), "m", "b") != ReplayFresh {
		t.Fatal("second signature refused")
	}
	if got := c.Check(now.Add(61*time.Second), "m", "a"); got != ReplayFresh {
		t.Fatalf("expired entry not pruned (verdict %d), want ReplayFresh", got)
	}
	if got := c.Check(now.Add(90*time.Second), "m", "b"); got != ReplayDuplicate {
		t.Fatalf("an unexpired entry was evicted (verdict %d), want ReplayDuplicate", got)
	}
	// A bucket whose entries have all expired is dropped at the next
	// check of that machine: after the prune the bucket holds only the
	// newly recorded live entry (memory follows live entries).
	c3 := newReplayCache(4, time.Minute)
	c3.Check(now, "gone", "x")
	if got := c3.Check(now.Add(61*time.Second), "gone", "y"); got != ReplayFresh {
		t.Fatalf("a signature after the bucket fully expired = %d, want ReplayFresh", got)
	}
	c3.mu.Lock()
	live := len(c3.seen["gone"])
	c3.mu.Unlock()
	if live != 1 {
		t.Fatalf("the pruned bucket holds %d entries, want only the live one", live)
	}
}

func TestReplayCachePerMachineQuotaAndRecovery(t *testing.T) {
	c := newReplayCache(2, time.Minute)
	now := time.Unix(1_700_000_000, 0)
	if c.Check(now, "m1", "a") != ReplayFresh || c.Check(now, "m1", "b") != ReplayFresh {
		t.Fatal("first two signatures refused")
	}
	if got := c.Check(now, "m1", "c"); got != ReplayOverQuota {
		t.Fatalf("over-quota signature = %d, want ReplayOverQuota (never accepted, never recorded)", got)
	}
	// Isolation: another machine is unaffected.
	if got := c.Check(now, "m2", "z"); got != ReplayFresh {
		t.Fatalf("another machine suffered m1's quota: %d", got)
	}
	// A duplicate still answers ReplayDuplicate at quota (not CORE-429).
	if got := c.Check(now, "m1", "a"); got != ReplayDuplicate {
		t.Fatalf("replay at quota = %d, want ReplayDuplicate", got)
	}
	// Recovery: once the machine's own oldest entries pass the TTL, the
	// same machine is accepted again — only its own entries expired.
	if got := c.Check(now.Add(61*time.Second), "m1", "d"); got != ReplayFresh {
		t.Fatalf("quota did not recover after expiry: %d", got)
	}
	// A refused (over-quota) signature was never recorded: it stays fresh
	// once the bucket drained.
	if got := c.Check(now.Add(61*time.Second), "m1", "c"); got != ReplayFresh {
		t.Fatalf("a refused signature was recorded: %d", got)
	}
}

// TestReplayCacheSweepDropsIdleExpiredBuckets pins the memory bound's
// "machine ids holding LIVE entries" half (code review): the
// sweep covers every bucket, not only the caller's. An idle machine's
// fully expired bucket must be gone once any other machine checks — on
// the pre-sweep code it survived until that machine signed again or the
// aihub restarted, so memory followed machine ids recorded since startup.
// An unexpired entry of a third machine must survive the same sweep.
func TestReplayCacheSweepDropsIdleExpiredBuckets(t *testing.T) {
	c := newReplayCache(4, time.Minute)
	now := time.Unix(1_700_000_000, 0)
	if c.Check(now, "m-idle", "old") != ReplayFresh {
		t.Fatal("the idle machine's first signature was refused")
	}
	// m-live signs inside the TTL, so its entry is still live when the
	// sweep runs below.
	if c.Check(now.Add(30*time.Second), "m-live", "live") != ReplayFresh {
		t.Fatal("the live machine's signature was refused")
	}
	// Only the idle machine's entry passes the TTL; then another machine
	// checks — the sweep must drop the expired entry and the empty bucket,
	// and must keep the live machine's unexpired entry.
	if got := c.Check(now.Add(61*time.Second), "m-active", "fresh"); got != ReplayFresh {
		t.Fatalf("the active machine's signature = %d, want ReplayFresh", got)
	}
	c.mu.Lock()
	_, idleKept := c.seen["m-idle"]
	liveEntries := len(c.seen["m-live"])
	c.mu.Unlock()
	if idleKept {
		t.Fatal("the idle machine's fully expired bucket survived another machine's Check")
	}
	if liveEntries != 1 {
		t.Fatalf("the sweep evicted an unexpired entry: m-live holds %d entries, want 1", liveEntries)
	}
}

// TestReplayCacheFloodCannotEvictCapturedSignature pins
// acceptance 1: >4096 fresh verified entries from other machine ids (each
// within its own quota) can never flush a captured signature — the old
// global evict-oldest cache did exactly that.
func TestReplayCacheFloodCannotEvictCapturedSignature(t *testing.T) {
	c := NewReplayCache() // the production shape: 512 per machine id / 600 s
	now := time.Now()
	if c.Check(now, "captured", "the-captured-signature") != ReplayFresh {
		t.Fatal("captured signature refused on first sight")
	}
	const machines, perMachine = 9, 500 // 4500 live entries, past the old global 4096
	for m := 0; m < machines; m++ {
		id := fmt.Sprintf("flood-%d", m)
		for i := 0; i < perMachine; i++ {
			if got := c.Check(now, id, fmt.Sprintf("sig-%d-%d", m, i)); got != ReplayFresh {
				t.Fatalf("flood entry %s/%d = %d, want ReplayFresh", id, i, got)
			}
		}
	}
	if got := c.Check(now, "captured", "the-captured-signature"); got != ReplayDuplicate {
		t.Fatalf("the flood evicted the captured signature (verdict %d), want ReplayDuplicate", got)
	}
}

// TestReplayCacheSweepsOtherBucketsOncePerTTL pins the amortized sweep
// a check prunes only the caller's bucket while the last
// all-bucket sweep is younger than the TTL — an idle machine's expired
// bucket is not walked on every request — and the next check past that
// window sweeps every bucket once. Unexpired entries survive both.
func TestReplayCacheSweepsOtherBucketsOncePerTTL(t *testing.T) {
	c := newReplayCache(4, time.Minute)
	now := time.Unix(1_700_000_000, 0)
	c.Check(now, "m-a", "sweep-0") // the first check sweeps (none yet)
	c.Check(now.Add(10*time.Second), "m-idle", "old")
	c.Check(now.Add(50*time.Second), "m-live", "live")
	swept := func() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.lastSweep }
	has := func(id string) bool { c.mu.Lock(); defer c.mu.Unlock(); _, ok := c.seen[id]; return ok }
	if !swept().Equal(now) {
		t.Fatalf("lastSweep = %v, want the first check's %v (no sweep inside the window)", swept(), now)
	}
	// 65s: past the window opened at 0s — this check sweeps every bucket:
	// m-a's entry (0s) expired, m-idle's (10s) and m-live's (50s) are live.
	if c.Check(now.Add(65*time.Second), "m-b", "x") != ReplayFresh {
		t.Fatal("m-b refused")
	}
	if !swept().Equal(now.Add(65 * time.Second)) {
		t.Fatalf("the check past the window must sweep once: lastSweep %v", swept())
	}
	if has("m-a") {
		t.Fatal("the sweep kept m-a's fully expired bucket")
	}
	if !has("m-idle") || !has("m-live") {
		t.Fatal("the sweep evicted an unexpired entry")
	}
	// inside the new window: m-idle's entry expires at 70s, but a check by
	// another machine at 80s prunes only its own bucket
	c.Check(now.Add(80*time.Second), "m-b", "y")
	if !has("m-idle") || !swept().Equal(now.Add(65*time.Second)) {
		t.Fatal("a check inside the window swept another machine's bucket")
	}
	// the idle machine's own check prunes its own bucket at once
	if c.Check(now.Add(81*time.Second), "m-idle", "old") != ReplayFresh {
		t.Fatal("m-idle's expired signature was not pruned from its own bucket")
	}
	// past the next window every expired bucket is gone
	c.Check(now.Add(126*time.Second), "m-b", "z")
	if has("m-live") || !swept().Equal(now.Add(126*time.Second)) {
		t.Fatalf("the next window's sweep kept m-live's expired bucket (lastSweep %v)", swept())
	}
}
