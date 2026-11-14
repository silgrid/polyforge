package runtime

// w350_restart_disabled_test.go (D5, review s12#2): held work
// never starts behind an execution-disabled Cell. An operator-paused
// record whose hub clean_pause was cleared without the Cell's admission
// (pf resume while the Host was down, or from another machine) stays
// paused across a restart of the still-disabled Cell — no attach, no run;
// once the Cell reopens, the operator's pf resume continues it in place.

import (
	"context"
	"os"
	"sync/atomic"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
)

func TestW350DisabledCellRestartKeepsHeldPausedWorkPaused(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)
	var why atomic.Value
	why.Store("no model source")
	r.cfg.ExecutionDisabled = func() string { return why.Load().(string) }
	wi := "wi_w350held"
	rec := matrixRecord(r, wi, 0, "")
	rec.State = string(StatePaused)
	rec.CleanPause = true
	hub.hold(wi, rec.SessionID, api.StatusPaused) // clean_pause cleared at the hub
	writeStateFile(t, r, []*jobRecord{rec})
	if err := os.MkdirAll(rec.CapsuleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeTranscript(t, jobFromRecord(rec))

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	if n := len(hub.attaches); n != 0 {
		t.Fatalf("a disabled Cell's restart attached the held paused session %d time(s)", n)
	}
	if n := sp.callCount(); n != 0 {
		t.Fatalf("a disabled Cell's restart started the held work (%d derivation(s))", n)
	}
	if hub.status[wi] != api.StatusPaused {
		t.Fatalf("the hub status must stay paused, got %q", hub.status[wi])
	}
	if recs := loadRecords(t, r); len(recs) != 1 || recs[0].State != string(StatePaused) || recs[0].SessionID != rec.SessionID {
		t.Fatalf("the paused record must be retained as it was, got %+v", recs)
	}
	if _, err := r.ResumePaused(context.Background(), wi); err == nil {
		t.Fatal("a resume in the still-disabled Cell must be refused")
	}
	if n := len(hub.attaches); n != 0 {
		t.Fatalf("the refused resume attached the session %d time(s)", n)
	}

	// the Cell reopens: the operator's pf resume continues it in place
	why.Store("")
	out, err := r.ResumePaused(context.Background(), wi)
	if err != nil {
		t.Fatalf("the resume once the Cell reopened: %v", err)
	}
	settleOps(t, r, 20e9)
	if n := len(hub.attaches); n != 1 || out != ResumeAttached {
		t.Fatalf("the reopened Cell's resume must attach once, got %d attaches, outcome %q", n, out)
	}
	if j := r.jobOf(wi); j != nil {
		j.mu.Lock()
		h := j.agent
		j.mu.Unlock()
		if h != nil {
			_ = h.Terminate(2e9)
		}
	}
}
