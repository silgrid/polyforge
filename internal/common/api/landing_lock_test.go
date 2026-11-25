package api

import (
	"encoding/json"
	"testing"
)

// TestLeaseProjectionOldReader is the compatibility pin of the projection
// (polyforge#249 AC1): a reader struct that predates the field decodes a
// document carrying landing_lock and keeps every old field byte for byte,
// so an old CLI or panel never changes what it shows.
func TestLandingLockOldReader(t *testing.T) {
	doc := []byte(`{
		"id":"wi_abc","slug":"polyforge#9","seq":9,"project":"polyforge",
		"status":"running","priority":"normal","goal":"g","created_at":"2026-11-25T00:00:00Z",
		"landing_lock":{"state":"waiting","observed_at":"2026-11-25T00:00:00Z",
			"as_of":"2026-11-25T00:00:01Z","keys":[{"repo":"forge.example/o/r","branch":"epic/245","position":3,"since":"2026-11-25T00:00:00Z"}]}
	}`)
	var old struct {
		ID       string  `json:"id"`
		Slug     string  `json:"slug"`
		Status   string  `json:"status"`
		Priority string  `json:"priority"`
		Goal     *string `json:"goal"`
		Created  string  `json:"created_at"`
	}
	if err := json.Unmarshal(doc, &old); err != nil {
		t.Fatalf("old reader decode: %v", err)
	}
	if old.ID != "wi_abc" || old.Slug != "polyforge#9" || old.Status != "running" ||
		old.Priority != "normal" || old.Goal == nil || *old.Goal != "g" {
		t.Fatalf("old reader fields changed: %+v", old)
	}
	var wi WorkItem
	if err := json.Unmarshal(doc, &wi); err != nil {
		t.Fatalf("new reader decode: %v", err)
	}
	if wi.LeaseProjection == nil || wi.LeaseProjection.State != LeaseWaiting ||
		len(wi.LeaseProjection.Keys) != 1 || wi.LeaseProjection.Keys[0].Position != 3 {
		t.Fatalf("new reader lost the projection: %+v", wi.LeaseProjection)
	}
	if !wi.LeaseProjection.Waiting() {
		t.Fatalf("a live waiting projection must answer Waiting()")
	}
	out, err := json.Marshal(wi.LeaseProjection)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(out, &wire); err != nil {
		t.Fatalf("projection marshal: %v", err)
	}
	for _, forbidden := range []string{"token", "holder", "machine_id", "request_id", "operator_id"} {
		if _, ok := wire[forbidden]; ok {
			t.Fatalf("projection leaks %q: %s", forbidden, out)
		}
	}
}

// TestLeaseProjectionStates pins the state vocabulary and the 15 s window of Q2;
// a change to either is a protocol change, not an implementation detail.
func TestLandingLockStates(t *testing.T) {
	if LeaseWaiting != "waiting" || LeaseNone != "none" ||
		LeaseStale != "stale" || LeaseUnavailable != "unavailable" {
		t.Fatalf("state vocabulary changed")
	}
	if LeaseStaleAfter.Seconds() != 15 {
		t.Fatalf("stale window is %s, want 15s", LeaseStaleAfter)
	}
	if (&LeaseProjection{State: LeaseNone}).Waiting() {
		t.Fatalf("fresh-none answers no wait")
	}
	if (&LeaseProjection{State: LeaseStale, Keys: []LeaseProjectionKey{{Position: 2}}}).Waiting() != true {
		t.Fatalf("a stale snapshot still shows the last known wait")
	}
}
