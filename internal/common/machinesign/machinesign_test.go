package machinesign

import (
	"crypto/ed25519"
	"crypto/rand"
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

func TestReplayCacheBoundedAndEvicts(t *testing.T) {
	c := newReplayCache(2, time.Minute)
	now := time.Unix(1_700_000_000, 0)
	if !c.Check(now, "m", "a") || !c.Check(now, "m", "b") {
		t.Fatal("first two signatures refused")
	}
	if !c.Check(now, "m", "c") {
		t.Fatal("eviction did not make room")
	}
	if !c.Check(now.Add(2*time.Minute), "m", "c") {
		t.Fatal("expired entry was not pruned before the repeat check")
	}
}
