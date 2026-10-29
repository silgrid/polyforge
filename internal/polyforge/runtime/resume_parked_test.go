package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// TestResumePausedAnswersParkedWhileTheOldRunLives: an operator resume
// whose recovery parks on the previous driver Run (it outlived its bounded
// teardown) leaves the work item RECOVERING, still in the table. The door
// must answer that park — not "closed", which tells the operator the work
// item ended — and the recovery continues by itself once that Run exits.
func TestResumePausedAnswersParkedWhileTheOldRunLives(t *testing.T) {
	origBound := cleanupBound
	cleanupBound = 50 * time.Millisecond
	t.Cleanup(func() { cleanupBound = origBound })

	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	const wi = "wi_resume_parked"
	j := seedJob(r, wi)
	old := newParkedRunHandle()
	t.Cleanup(old.release)
	j.mu.Lock()
	j.state, j.stopReason, j.cleanPause = StatePaused, protocol.FinishPaused, false
	j.agent, j.agentPID = old, 0 // a retained record still naming a live driver Run
	j.mu.Unlock()
	hub.hold(wi, j.sessionID, api.StatusPaused)
	hub.mu.Lock()
	hub.cleanPause[wi] = false // the operator's pf resume PATCH
	hub.mu.Unlock()

	out, err := r.ResumePaused(context.Background(), wi)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if out != ResumeParked {
		t.Fatalf("resume over a recovery parked on the live Run answered %q, want %q", out, ResumeParked)
	}
	if st, _ := j.snapshot(); st != StateRecovering || r.jobOf(wi) != j {
		t.Fatalf("the parked work item must stay recovering in the table, got %q (present=%v)", st, r.jobOf(wi) != nil)
	}

	old.release()
	waitFor(t, "the parked recovery to continue once the old Run exited", func() bool {
		st, _ := j.snapshot()
		return st == StateRunning
	})
}
