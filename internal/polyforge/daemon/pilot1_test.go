package daemon

// pilot1_test.go: the pilot's F2 route-level regressions.
// A ticket submitted by its "<project>#<seq>" slug runs entirely under
// the CANONICAL id (queue keys, runtime job keys, capsule paths), and
// every later route call by EITHER spelling — status, observe, pause —
// finds the same job.

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestSlugLifecycleEndToEnd(t *testing.T) {
	hub := newFakeHub()
	hub.canonical = map[string]string{"proj#7": "wi_canon7"}
	td := newTestDaemon(t, func(c *Config) {
		c.Hub = hub
	})
	const canonical = "wi_canon7"
	mkBare(t, td.cfg.GitRemoteBase, canonical)

	// submit BY SLUG: accepted, and the answer names the canonical id —
	// the queue and the runtime key everything by it.
	raw, err := td.client.ClaimWithModels("proj#7", "normal", nil)
	if err != nil {
		t.Fatalf("claim by slug: %v", err)
	}
	var sr struct {
		Accepted   bool   `json:"accepted"`
		WorkItemID string `json:"work_item_id"`
	}
	if err := json.Unmarshal(raw, &sr); err != nil {
		t.Fatalf("claim answer undecodable: %v (%s)", err, raw)
	}
	if !sr.Accepted || sr.WorkItemID != canonical {
		t.Fatalf("claim answer = %+v, want accepted for %s", sr, canonical)
	}
	td.waitLive(t, canonical)
	// the runtime paths are canonical (no "#" anywhere near a path).
	ji, ok := td.rt.Info(canonical)
	if !ok {
		t.Fatal("no canonical job")
	}
	if strings.Contains(ji.CapsuleDir, "#") || strings.Contains(ji.Git.RemoteURL, "#") {
		t.Fatalf("canonical paths must not carry the slug separator: %+v", ji)
	}

	// the watch snapshot BY SLUG resolves the same work item (the
	// daemon's resolver: alias first, aihub otherwise).
	td.waitMonitored(t, canonical)
	if snap, err := td.client.WatchSnapshot("proj#7", 0, false); err != nil || snap.WorkItem != canonical || !snap.Known {
		t.Fatalf("watch by slug: %+v %v", snap, err)
	}

	// pause BY SLUG closes the canonical job: the pilot's exact flow
	// (pause after submit) must keep working end to end. Since the v1.5
	// review Finding 2 the pause RETAINS the record (state paused) —
	// the slug spelling must reach the canonical job for that too.
	if _, err := td.client.Pause("proj#7"); err != nil {
		t.Fatalf("pause by slug: %v", err)
	}
	waitState(t, td, canonical, "paused")
	if st := hub.lastEnd().Status; st != "paused" {
		t.Fatalf("pause end status = %q, want paused", st)
	}
	// and the end-generation call named the canonical id.
	hub.mu.Lock()
	lastWI := ""
	if len(hub.ends) > 0 {
		lastWI = hub.ends[len(hub.ends)-1].WI
	}
	hub.mu.Unlock()
	if lastWI != canonical {
		t.Fatalf("end-generation named %q, want the canonical %q", lastWI, canonical)
	}
}
