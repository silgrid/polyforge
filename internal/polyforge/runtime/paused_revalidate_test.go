package runtime

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// pausedHeldRuntime seeds one retained paused record (an operator pause:
// hub paused, clean_pause set) through Restart, then runs the runtime's
// loop with a fast paused-record revalidation cadence.
func pausedHeldRuntime(t *testing.T, wi string) (*Runtime, *fakeHub, *jobRecord) {
	t.Helper()
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.SessionRevalidateEvery = 20 * time.Millisecond
	rec := matrixRecord(r, wi, 0, "")
	rec.State = string(StatePaused)
	rec.CleanPause = true
	rec.StopReason = "paused"
	hub.hold(wi, rec.SessionID, api.StatusPaused)
	hub.mu.Lock()
	hub.cleanPause[wi] = true
	hub.mu.Unlock()
	writeStateFile(t, r, []*jobRecord{rec})
	if err := os.MkdirAll(rec.CapsuleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if j := r.jobOf(wi); j == nil || stateOf(j) != StatePaused {
		t.Fatalf("the operator pause must leave a retained paused record")
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { _ = r.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return r, hub, rec
}

// TestRevalidatePausedReleasesCanceledRecord (polyforge#217): `pf cancel`
// goes straight to aihub, which cancels the paused work item and clears
// its session; the holder learns it at its next paused-record
// revalidation and drops the record — no restart, no hub transition.
func TestRevalidatePausedReleasesCanceledRecord(t *testing.T) {
	wi := "wi_w217cancel"
	r, hub, _ := pausedHeldRuntime(t, wi)
	hub.mu.Lock()
	hub.held[wi], hub.status[wi], hub.cleanPause[wi] = "", api.StatusCanceled, false
	hub.mu.Unlock()

	deadline := time.Now().Add(10 * time.Second)
	for r.jobOf(wi) != nil {
		if time.Now().After(deadline) {
			t.Fatal("the canceled work item's paused record was never released")
		}
		time.Sleep(10 * time.Millisecond)
	}
	settleOps(t, r, 10*time.Second)
	if recs := loadRecords(t, r); len(recs) != 0 {
		t.Fatalf("the released record must leave state.json, got %+v", recs)
	}
	if hub.endCount() != 0 || len(hub.attaches) != 0 || hub.claimCount() != 0 {
		t.Fatalf("the release must make no hub transition (ends=%d attaches=%d claims=%d)",
			hub.endCount(), len(hub.attaches), hub.claimCount())
	}
}

// TestRevalidatePausedKeepsMatchingRecord: while the session still holds
// the work item the revalidation changes nothing — the operator pause
// stands, and a clean_pause the hub has cleared (a resume started
// elsewhere) does not attach (the deferred attach stays with the
// operator's local `pf resume` or the next start).
func TestRevalidatePausedKeepsMatchingRecord(t *testing.T) {
	wi := "wi_w217keep"
	r, hub, _ := pausedHeldRuntime(t, wi)
	// waitVerifies waits until the paused-record revalidation has answered
	// n more verifies — proof the check ran, not merely time passing.
	waitVerifies := func(n int, phase string) {
		t.Helper()
		want := hub.verifyCount(wi) + n
		deadline := time.Now().Add(10 * time.Second)
		for hub.verifyCount(wi) < want {
			if time.Now().After(deadline) {
				t.Fatalf("no paused-record revalidation ran %s (verifies=%d, want >= %d)", phase, hub.verifyCount(wi), want)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	waitVerifies(3, "with clean_pause set")
	hub.mu.Lock()
	hub.cleanPause[wi] = false
	hub.mu.Unlock()
	waitVerifies(3, "with clean_pause cleared")
	if j := r.jobOf(wi); j == nil || stateOf(j) != StatePaused {
		t.Fatal("a paused record whose session still holds the work item must stay paused")
	}
	hub.mu.Lock()
	attaches := len(hub.attaches)
	hub.mu.Unlock()
	if attaches != 0 || hub.endCount() != 0 {
		t.Fatalf("the revalidation must not attach or end (attaches=%d ends=%d)", attaches, hub.endCount())
	}
}
