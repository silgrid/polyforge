package driver

// landing_lock_events_test.go: the merge_lock_* event contract
// (polyforge#245 epic 3/3, Q4/Q15). Every event names the work item, a
// non-secret cycle, each key's repo/branch/position and the server queue
// facts; a live cycle emits queued once per key (an immediate grant included
// as a 0 ms pair); a renewal loss is renew_failed then one lost; a re-queued
// cycle counts up. No raw error or token text reaches the stream.

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func eventsOfKind(l *eventLog, kind string) []map[string]any {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []map[string]any
	for _, e := range l.entries {
		if e.kind == kind {
			out = append(out, e.body)
		}
	}
	return out
}

func TestLandingLockEventContract(t *testing.T) {
	d, sp, log, fl := landingEnv(t, "sync", "publish",
		spec("build"), spec("sync"), spec("merge"), spec("publish"), spec("after"))
	fl.queued = true
	fl.keys = []LandingKey{
		{Repo: "github.com/o/app", Branch: "epic/245", Position: 3, QueuedMS: 1200},
		{Repo: "github.com/o/lib", Branch: "epic/245", Position: 1, QueuedMS: 0},
	}
	lose := done("")
	lose.act = func() { fl.lose(errors.New("renewing github.com/o/app: HTTP 503 token=TOKEN_SENTINEL")) }
	sp.script = []fakeEntry{done(""), done(""), lose, done(""), done(""), done(""), done("")}
	if err := d.Run(t.Context()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	granted := eventsOfKind(log, EvLeaseGranted)
	if len(granted) != 2 {
		t.Fatalf("granted events = %d, want one per cycle", len(granted))
	}
	for i, body := range granted {
		if body["cycle_id"] == "" || body["cycle_id"] == nil {
			t.Fatalf("granted %d has no non-secret cycle: %v", i, body)
		}
		if body["reason"] != "granted" {
			t.Fatalf("granted %d reason = %v", i, body["reason"])
		}
		if got := body["requeue_count"]; got != i {
			t.Fatalf("granted %d requeue_count = %v, want %d", i, got, i)
		}
		keys, ok := body["keys"].([]map[string]any)
		if !ok || len(keys) != 2 {
			t.Fatalf("granted %d keys = %v", i, body["keys"])
		}
		if keys[0]["repo"] != "github.com/o/app" || keys[0]["branch"] != "epic/245" ||
			keys[0]["position"] != 3 || keys[0]["queued_ms"] != int64(1200) {
			t.Fatalf("granted %d first key = %v", i, keys[0])
		}
		if keys[1]["repo"] != "github.com/o/lib" || keys[1]["queued_ms"] != int64(0) {
			t.Fatalf("granted %d second key = %v", i, keys[1])
		}
	}

	queued := eventsOfKind(log, EvLeaseQueued)
	byCycle := map[string]map[string]bool{}
	for _, body := range queued {
		cycle, _ := body["cycle_id"].(string)
		if cycle == "" || body["reason"] != "waiting" || body["step"] != "sync" {
			t.Fatalf("queued body = %v", body)
		}
		keys, ok := body["keys"].([]map[string]any)
		if !ok || len(keys) != 1 {
			t.Fatalf("queued body keys = %v", body["keys"])
		}
		if byCycle[cycle] == nil {
			byCycle[cycle] = map[string]bool{}
		}
		key := keys[0]["repo"].(string)
		if byCycle[cycle][key] {
			t.Fatalf("cycle %s emitted queued twice for %s", cycle, key)
		}
		byCycle[cycle][key] = true
	}
	for cycle, keys := range byCycle {
		if !keys["github.com/o/app"] || !keys["github.com/o/lib"] {
			t.Fatalf("cycle %s queued keys = %v", cycle, keys)
		}
	}
	if len(byCycle) != 2 {
		t.Fatalf("cycles with queued = %d, want the two holds", len(byCycle))
	}

	renew := eventsOfKind(log, EvLeaseRenewFailed)
	if len(renew) != 1 || renew[0]["reason"] != "renew_failed" || renew[0]["cycle_id"] == nil {
		t.Fatalf("renew_failed events = %v", renew)
	}
	lost := eventsOfKind(log, EvLeaseLost)
	if len(lost) != 1 || lost[0]["reason"] != "lost" {
		t.Fatalf("lost events = %v", lost)
	}

	raw, _ := json.Marshal(log.entries)
	if strings.Contains(string(raw), "TOKEN_SENTINEL") || strings.Contains(string(raw), "error") ||
		strings.Contains(string(raw), "token") {
		t.Fatalf("an event leaked the raw error or a token: %s", raw)
	}
}
