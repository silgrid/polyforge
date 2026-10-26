package runtime

// persisted_flags_test.go — the end_pending "chain push permanently
// rejected, stop pushing" verdict survives a daemon restart through the
// job record; records written before the field existed load it false.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

func TestPersistedFlagsAbsentInOldRecordsLoadFalse(t *testing.T) {
	var rec jobRecord
	if err := json.Unmarshal([]byte(`{"work_item_id":"wi_old","session_id":"s","state":"end_pending"}`), &rec); err != nil {
		t.Fatal(err)
	}
	j := jobFromRecord(&rec)
	if j.endPushGaveUp {
		t.Fatal("an old record loaded endPushGaveUp=true, want false")
	}
	// and the round trip keeps it
	j.endPushGaveUp = true
	if back := jobFromRecord(j.record()); !back.endPushGaveUp {
		t.Fatal("the record round trip dropped the flag")
	}
}

// A permanent chain push rejection on an end_pending close is recorded
// durably at once: after a daemon restart the blocked-close retry closes
// without pushing again.
func TestEndPushGaveUpSurvivesRestart(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	const wi = "wi_pf_gaveup"
	j := seedJob(r, wi)
	bare := initGitCapsule(t, j)
	pushes := filepath.Join(t.TempDir(), "pushes")
	hook := fmt.Sprintf("#!/bin/sh\necho x >> %q\necho 'aihub: commit is missing an integer Polyforge-Seq trailer' >&2\nexit 1\n", pushes)
	if err := os.WriteFile(filepath.Join(bare, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	hub.hold(wi, j.sessionID, api.StatusRunning)
	hub.end[wi] = fmt.Errorf("aihub unreachable (scripted)")
	j.mu.Lock()
	j.state = StateEndPending
	j.endNote = "boom"
	j.mu.Unlock()
	if endPendingAttempt(r, j) {
		t.Fatal("the close must stay pending while the hub refuses the end")
	}
	recs := loadRecords(t, r)
	if len(recs) != 1 || !recs[0].EndPushGaveUp {
		t.Fatalf("state.json after the permanent rejection = %+v, want end_push_gave_up", recs)
	}
	before := countLines(t, pushes)
	if before == 0 {
		t.Fatal("setup: the rejected push never reached the hook")
	}
	r.Close()

	// the next daemon start: the hub answers again
	hub.mu.Lock()
	delete(hub.end, wi)
	hub.mu.Unlock()
	r2 := New(r.cfg, hub, &scriptedSpawner{}, r.toOB, r.fromOB)
	r2.cfg.EndRetryBase = 10 * time.Millisecond
	r2.cfg.EndRetryMax = 20 * time.Millisecond
	t.Cleanup(r2.Close)
	if err := restartSettled(t, r2); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	waitFor(t, "the end_pending close to settle", func() bool {
		_, ok := r2.Info(wi)
		return !ok
	})
	waitRetryIdle(t, r2)
	if got := countLines(t, pushes); got != before {
		t.Fatalf("the retry after the restart pushed again (%d hook runs, was %d)", got, before)
	}
	if hub.endCount() != 1 || hub.lastEnd().Status != api.StatusBlocked {
		t.Fatalf("ends = %d (%+v), want the one blocked close", hub.endCount(), hub.lastEnd())
	}
}

func countLines(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(raw), "\n")
}
