package runtime

// A release (supersede: the session is held elsewhere; notOurs: the hub
// no longer holds it for this daemon) whose old driver Run outlives the
// handle's bounded teardown must not quarantine (rename) the capsule or
// drop the record next to it: the Run may still be writing the scene, and
// a dropped record lets a re-claim start a second writer in it. The
// release parks, the operator gets a 409 that says so, and it finishes by
// itself once that Run exits.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// releaseParkRig is a closeParkRig that also records every rename (the
// quarantine's only mechanism) and the release events.
type releaseParkRig struct {
	*closeParkRig
	mu      sync.Mutex
	renames []string
	events  map[string]int
}

func newReleaseParkRig(t *testing.T, wi string) *releaseParkRig {
	t.Helper()
	k := &releaseParkRig{closeParkRig: newCloseParkRig(t, wi), events: map[string]int{}}
	orig := renameFn
	renameFn = func(from, to string) error {
		k.mu.Lock()
		k.renames = append(k.renames, from+" -> "+to)
		k.mu.Unlock()
		return orig(from, to)
	}
	t.Cleanup(func() { renameFn = orig })
	tap := k.r.cfg.EventTap
	k.r.cfg.EventTap = func(wi, kind string, body map[string]any) {
		tap(wi, kind, body)
		k.mu.Lock()
		k.events[kind]++
		k.mu.Unlock()
	}
	return k
}

func (k *releaseParkRig) renameCount() int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return len(k.renames)
}

func (k *releaseParkRig) event(kind string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.events[kind]
}

// assertParkedRelease: nothing renamed, the record still in the table,
// one close_parked(release) event, and the operator's doors answer the
// park.
func (k *releaseParkRig) assertParkedRelease(t *testing.T, settledEvent string) {
	t.Helper()
	time.Sleep(4 * cleanupBound)
	if n := k.renameCount(); n != 0 {
		t.Fatalf("%d rename(s) while the old Run is alive: %v", n, k.renames)
	}
	if !dirExists(k.j.capsuleDir) {
		t.Fatalf("the capsule %s moved while the old Run is alive", k.j.capsuleDir)
	}
	if k.r.jobOf(k.j.wi) != k.j {
		t.Fatal("the record was dropped while the old Run is alive (a re-claim could start a second writer)")
	}
	if got := k.event(settledEvent); got != 0 {
		t.Fatalf("%s emitted before the release could finish", settledEvent)
	}
	if ev := k.parkedEvents(); len(ev) != 1 || ev[0]["kind"] != "release" {
		t.Fatalf("the park must be named by one close_parked(kind=release) event, got %v", ev)
	}
	err := k.r.Stop(context.Background(), k.j.wi, protocol.FinishWrapped)
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "release of this work item is parked") {
		t.Fatalf("a close over the parked release must answer the DAEMON-409 naming it, got %v", err)
	}
}

// TestSupersedeParksOnLiveRunThenQuarantines: the observe lane's
// OBSuperseded on a work item whose driver Run ignores the teardown.
func TestSupersedeParksOnLiveRunThenQuarantines(t *testing.T) {
	k := newReleaseParkRig(t, "wi_release_park_superseded")

	k.r.handleOB(context.Background(), protocol.OBMsg{Kind: protocol.OBSuperseded, WorkItem: k.j.wi, SessionFP: k.j.sessionFingerprint})
	k.assertParkedRelease(t, "session_superseded")

	k.old.release()
	// the release's last act is its event (after the quarantine and the drop)
	waitFor(t, "the parked release to finish after the Run exited", func() bool { return k.event("session_superseded") > 0 })
	if k.r.jobOf(k.j.wi) != nil || dirExists(k.j.capsuleDir) || k.renameCount() == 0 {
		t.Fatalf("the finished release must drop the record and quarantine the capsule (renames %v)", k.renames)
	}
	if got := k.event("session_superseded"); got != 1 {
		t.Fatalf("the finished release must emit session_superseded once, got %d", got)
	}
}

// TestNotOursReleaseParksOnLiveRunThenQuarantines: a paused record's
// revalidation finds the work item held by another session.
func TestNotOursReleaseParksOnLiveRunThenQuarantines(t *testing.T) {
	k := newReleaseParkRig(t, "wi_release_park_released")
	k.j.mu.Lock()
	k.j.state, k.j.stopReason, k.j.cleanPause = StatePaused, protocol.FinishPaused, true
	k.j.mu.Unlock()
	k.hub.hold(k.j.wi, "another-machines-session", api.StatusRunning)

	underLock(k.r, k.j.wi, func() { k.r.revalidatePausedRecord(k.j.wi) })
	k.assertParkedRelease(t, "session_released")
	if _, err := k.r.ResumePaused(context.Background(), k.j.wi); err == nil || !strings.Contains(err.Error(), "release of this work item is parked") {
		t.Fatalf("a resume over the parked release must answer the park, got %v", err)
	}

	k.old.release()
	waitFor(t, "the parked release to finish after the Run exited", func() bool { return k.event("session_released") > 0 })
	if k.r.jobOf(k.j.wi) != nil || dirExists(k.j.capsuleDir) || k.renameCount() == 0 {
		t.Fatalf("the finished release must drop the record and quarantine the capsule (renames %v)", k.renames)
	}
	if got := k.event("session_released"); got != 1 {
		t.Fatalf("the finished release must emit session_released once, got %d", got)
	}
}

// TestNotOursDropParksOnLiveRun: the hub says the work item ended
// (canceled) — the record is dropped and the capsule kept, but not while
// the old Run lives.
func TestNotOursDropParksOnLiveRun(t *testing.T) {
	k := newReleaseParkRig(t, "wi_release_park_dropped")
	k.j.mu.Lock()
	k.j.state, k.j.stopReason, k.j.cleanPause = StatePaused, protocol.FinishPaused, true
	k.j.mu.Unlock()
	k.hub.mu.Lock()
	k.hub.held[k.j.wi] = ""
	k.hub.status[k.j.wi] = api.StatusCanceled
	k.hub.mu.Unlock()

	underLock(k.r, k.j.wi, func() { k.r.revalidatePausedRecord(k.j.wi) })
	k.assertParkedRelease(t, "session_released")

	k.old.release()
	waitFor(t, "the parked drop to finish after the Run exited", func() bool { return k.r.jobOf(k.j.wi) == nil })
	if !dirExists(k.j.capsuleDir) || k.renameCount() != 0 {
		t.Fatalf("an ended work item keeps its capsule in place (renames %v)", k.renames)
	}
}
