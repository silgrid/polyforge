package runtime

// restart_fence_test.go — the restart's per-work-item session fence: a
// work item that was ended or taken over at the hub while the daemon was
// down is, after the restart, never (a) relaunched, (b) pushed under the
// dead session, or (c) sent a SessionEnd — whatever its record was doing
// when the daemon stopped. Each actor verifies before it acts.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

func TestRestartNeverActsOnAMismatchedSession(t *testing.T) {
	records := []struct {
		name string
		edit func(rec *jobRecord)
	}{
		{"running", func(rec *jobRecord) {}},
		{"recovering", func(rec *jobRecord) { rec.State = string(StateRecovering) }},
		{"sealing", func(rec *jobRecord) {
			rec.State, rec.StopReason = string(StateSealing), protocol.FinishWrapped
		}},
		{"end_pending", func(rec *jobRecord) {
			rec.State, rec.EndNote = string(StateEndPending), "bounded fail"
		}},
		{"paused-pending-close", func(rec *jobRecord) {
			rec.State, rec.StopReason, rec.CleanPause = string(StatePaused), protocol.FinishWrapped, true
		}},
		{"paused-pending-pause", func(rec *jobRecord) {
			rec.State, rec.StopReason, rec.CleanPause = string(StatePaused), protocol.FinishPaused, true
		}},
	}
	hubs := []struct {
		name string
		set  func(h *fakeHub, wi string)
	}{
		{"ended", func(h *fakeHub, wi string) { h.forceEnd(wi) }},
		{"taken-over", func(h *fakeHub, wi string) { h.hold(wi, fakeSession("another-daemon:"+wi), api.StatusRunning) }},
		{"wrapped", func(h *fakeHub, wi string) { h.hold(wi, "", api.StatusWrapped) }},
	}
	for _, rc := range records {
		for _, hc := range hubs {
			t.Run(rc.name+"/"+hc.name, func(t *testing.T) {
				hub := newFakeHub()
				sp := &scriptedSpawner{}
				r, _, _ := newTestRuntime(t, hub, sp)
				r.cfg.EndRetryBase = 10 * time.Millisecond
				r.cfg.EndRetryMax = 20 * time.Millisecond
				t.Cleanup(r.Close)
				const wi = "wi_fence"
				rec := matrixRecord(r, wi, 0, "")
				rc.edit(rec)
				bare := initGitCapsule(t, r.restoredJob(rec))
				rec.RemoteURL = bare
				pushes := filepath.Join(t.TempDir(), "pushes")
				hook := "#!/bin/sh\necho push >> " + pushes + "\n"
				if err := os.WriteFile(filepath.Join(bare, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
					t.Fatal(err)
				}
				// a dirty capsule: a recovery or a close would push it
				if err := os.WriteFile(filepath.Join(rec.CapsuleDir, "late.txt"), []byte("late work\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				writeStateFile(t, r, []*jobRecord{rec})
				hc.set(hub, wi)

				if err := restartSettled(t, r); err != nil {
					t.Fatalf("Restart: %v", err)
				}
				waitFor(t, "the mismatched record to be settled", func() bool { return r.jobOf(wi) == nil })
				settleOps(t, r, 10*time.Second)
				if n := sp.callCount(); n != 0 {
					t.Fatalf("(a) the restart relaunched %d agent(s) for a session the hub no longer holds", n)
				}
				if n := countLines(t, pushes); n != 0 {
					t.Fatalf("(b) the restart pushed %d time(s) under a session the hub no longer holds", n)
				}
				if n := hub.endCount(); n != 0 {
					t.Fatalf("(c) the restart sent %d SessionEnd(s) for a session the hub no longer holds: %+v", n, hub.lastEnd())
				}
				if recs := loadRecords(t, r); len(recs) != 0 {
					t.Fatalf("the mismatched record must be dropped, got %+v", recs)
				}
			})
		}
	}
}

// TestRefusedPushStopsTheWorkItem: the owner ruling — a push the git face
// refuses because the session no longer holds the work item stops it at
// once. The restart's verify still matched; the work item is taken over
// before the recovery's commit-dirty push, which the hub refuses (401):
// the recovery relaunches nothing, ends nothing, and releases the record.
func TestRefusedPushStopsTheWorkItem(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)
	t.Cleanup(r.Close)
	kinds := tapEvents(r)
	const wi = "wi_refused"
	rec := matrixRecord(r, wi, 0, "")
	initGitCapsule(t, r.restoredJob(rec))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hub.hold(wi, fakeSession("another-daemon:"+wi), api.StatusRunning) // taken over meanwhile
		w.Header().Set("WWW-Authenticate", `Basic realm="aihub"`)
		http.Error(w, "credential rejected: no live session", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	rec.RemoteURL = srv.URL + "/" + wi + ".git"
	writeStateFile(t, r, []*jobRecord{rec})
	hub.hold(wi, rec.SessionID, api.StatusRunning)

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	waitFor(t, "the refused work item to be released", func() bool { return r.jobOf(wi) == nil })
	settleOps(t, r, 10*time.Second)
	if n := sp.callCount(); n != 0 {
		t.Fatalf("the recovery relaunched %d agent(s) after the hub refused the session's push", n)
	}
	if n := hub.endCount(); n != 0 {
		t.Fatalf("the recovery sent %d SessionEnd(s) for a session the hub no longer holds", n)
	}
	if recs := loadRecords(t, r); len(recs) != 0 {
		t.Fatalf("the released record must be dropped, got %+v", recs)
	}
	if !slices.Contains(*kinds, "session_released") {
		t.Fatalf("events %v, want session_released", *kinds)
	}
}
