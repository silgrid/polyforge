package runtime

// missing_capsule_test.go pins the mid-flight no-silent-rebuild guard
// (#103): a recorded non-terminal work item whose capsule directory is
// absent under the active workspace is a typed hard failure — Restart
// blocks that WI alone at aihub (v2 §6; the daemon still starts), the resume
// entry refuses with *MissingCapsuleError, the recovery engine refuses
// with a loud blocked verdict — and NO code path rebuilds, re-clones or
// re-materializes the scene: the clone stays confined to the
// fresh-claim/re-claim lane (prepareCapsule).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

func assertNothingMaterialized(t *testing.T, r *Runtime, wi string, sp *scriptedSpawner) {
	t.Helper()
	if _, err := os.Stat(testCapsuleDir(r, wi)); !os.IsNotExist(err) {
		t.Errorf("capsule dir %s must NOT be created (no silent rebuild)", testCapsuleDir(r, wi))
	}
	if _, err := os.Stat(r.gitDirFor(wi)); !os.IsNotExist(err) {
		t.Errorf("external git dir %s must NOT be created (no clone from remote)", r.gitDirFor(wi))
	}
	if sp.callCount() != 0 {
		t.Errorf("no derivation may spawn against a missing capsule, got %d", sp.callCount())
	}
}

// assertBlocked asserts the v2 §6 posture: Restart returned nil, the WI was
// closed through the ordinary bounded-fail path (SessionEnd(blocked) under
// the kept session secret, a note naming the cause), the durable event was
// emitted, nothing is left parked locally and nothing was rebuilt.
func assertBlocked(t *testing.T, r *Runtime, hub *fakeHub, sp *scriptedSpawner, rec *jobRecord, kind, wantInNote string, events *[]string) {
	t.Helper()
	wi := rec.WorkItemID
	if hub.endCount() != 1 {
		t.Fatalf("exactly one SessionEnd(blocked) must fire, got %d", hub.endCount())
	}
	end := hub.lastEnd()
	if end.Status != api.StatusBlocked || end.SessionID != rec.SessionID || !strings.Contains(end.Note, wantInNote) {
		t.Fatalf("SessionEnd must be blocked under the stored secret with a note naming %q, got %+v", wantInNote, end)
	}
	if hub.status[wi] != api.StatusBlocked {
		t.Errorf("hub status = %q, want blocked", hub.status[wi])
	}
	found := false
	for _, k := range *events {
		found = found || k == kind
	}
	if !found {
		t.Errorf("durable event %q not emitted (got %v)", kind, *events)
	}
	if r.jobOf(wi) != nil || len(loadRecords(t, r)) != 0 {
		t.Fatalf("nothing may stay parked locally (job %v, records %+v)", r.jobOf(wi), loadRecords(t, r))
	}
	if len(hub.attaches) != 0 || hub.claimCount() != 0 {
		t.Fatalf("no attach / claim may fire (attaches=%d claims=%d)", len(hub.attaches), hub.claimCount())
	}
	assertNothingMaterialized(t, r, wi, sp)
}

func tapEvents(r *Runtime) *[]string {
	var kinds []string
	r.cfg.EventTap = func(w, kind string, body map[string]any) { kinds = append(kinds, kind) }
	return &kinds
}

// Restart with a mid-flight (verify-matches running) record whose capsule
// is absent at the active base: that WI alone is blocked (SessionEnd(blocked)
// naming the path, event written, record dropped, nothing rebuilt).
func TestRestartMissingMidflightCapsuleBlocks(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, hub, sp)
	events := tapEvents(r)
	wi := "wi_missing_running"
	rec := matrixRecord(r, wi, 0, "")
	hub.hold(wi, rec.SessionID, api.StatusRunning) // our session, still running
	writeStateFile(t, r, []*jobRecord{rec})
	// the capsule dir is deliberately NOT created.

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("a missing capsule must block the WI, not fail Restart: %v", err)
	}
	assertBlocked(t, r, hub, sp, rec, EventCapsuleMissing, "capsule missing at "+testCapsuleDir(r, wi), events)
	msg := recvRT(t, toOB)
	if msg.Kind != protocol.RTFinished || msg.Reason != protocol.FinishBlocked || msg.WorkItem != wi {
		t.Fatalf("expected RTFinished(blocked) so the slot is released, got %+v", msg)
	}
}

// Same guard on the paused/unclean attach branch: the block fires BEFORE
// the SessionAttach flips the hub.
func TestRestartMissingPausedCapsuleBlocksBeforeAttach(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)
	events := tapEvents(r)
	wi := "wi_missing_paused"
	rec := matrixRecord(r, wi, 0, "")
	rec.State = string(StatePaused)
	rec.CleanPause = false
	hub.hold(wi, rec.SessionID, api.StatusPaused)
	writeStateFile(t, r, []*jobRecord{rec})

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	assertBlocked(t, r, hub, sp, rec, EventCapsuleMissing, "capsule missing at", events)
}

// Same guard on the NEW-3 pending-close replay branch: the close must not
// be "replayed" against a capsule that is not there.
func TestRestartMissingPendingCloseCapsuleBlocks(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)
	events := tapEvents(r)
	wi := "wi_missing_pclose"
	rec := matrixRecord(r, wi, 0, "")
	rec.State = string(StatePaused)
	rec.CleanPause = true
	rec.StopReason = protocol.FinishWrapped // the pending terminal close
	hub.hold(wi, rec.SessionID, api.StatusPaused)
	writeStateFile(t, r, []*jobRecord{rec})

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	assertBlocked(t, r, hub, sp, rec, EventCapsuleMissing, "capsule missing at", events)
}

// An unmapped mid-flight project blocks that WI alone with a note naming
// the project; the (existing) capsule is left where it is.
func TestRestartUnmappedProjectBlocks(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)
	events := tapEvents(r)
	wi := "wi_unmapped"
	rec := matrixRecord(r, wi, 0, "")
	rec.Project = "ghost" // not in the mapping
	hub.hold(wi, rec.SessionID, api.StatusRunning)
	writeStateFile(t, r, []*jobRecord{rec})

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("an unmapped project must block the WI, not fail Restart: %v", err)
	}
	assertBlocked(t, r, hub, sp, rec, EventProjectUnmapped, `project "ghost"`, events)
}

// A SessionEnd the hub does not acknowledge keeps the record in the durable
// end_pending lane (the ordinary blocked-close retry) until it lands.
func TestRestartMissingCapsuleBlockedCloseRetries(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)
	r.cfg.EndRetryBase = 10 * time.Millisecond
	r.cfg.EndRetryMax = 20 * time.Millisecond
	t.Cleanup(r.Close)
	wi := "wi_missing_retry"
	rec := matrixRecord(r, wi, 0, "")
	hub.hold(wi, rec.SessionID, api.StatusRunning)
	hub.mu.Lock()
	hub.end[wi] = fmt.Errorf("aihub unreachable (scripted)")
	hub.mu.Unlock()
	writeStateFile(t, r, []*jobRecord{rec})

	if err := restartSettled(t, r); err != nil {
		t.Fatal(err)
	}
	recs := loadRecords(t, r)
	if len(recs) != 1 || recs[0].State != string(StateEndPending) || recs[0].SessionID != rec.SessionID ||
		!strings.Contains(recs[0].EndNote, "capsule missing at") {
		t.Fatalf("durable record must be end_pending with the secret and the reason, got %+v", recs)
	}
	hub.mu.Lock()
	delete(hub.end, wi)
	hub.mu.Unlock()
	waitFor(t, "the retry to land the blocked close", func() bool { return r.jobOf(wi) == nil })
	if hub.lastEnd().Status != api.StatusBlocked {
		t.Fatalf("retry must send blocked, got %+v", hub.lastEnd())
	}
	waitFor(t, "state.json to drop the record", func() bool { return len(loadRecords(t, r)) == 0 })
}

// The resume guard refuses with the typed error when the capsule vanishes
// after start.
func TestRestartResumeMissingCapsuleTypedError(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, _, _ := newTestRuntime(t, hub, sp)
	wi := "wi_missing_resume"
	rec := matrixRecord(r, wi, 0, "")
	r.mu.Lock()
	r.jobs[wi] = jobFromRecord(rec)
	r.mu.Unlock()

	var err error
	underLock(r, rec.WorkItemID, func() { err = r.requireMidflightCapsule(rec.WorkItemID, rec.Project, rec.CapsuleDir) })
	var miss *MissingCapsuleError
	if !errors.As(err, &miss) {
		t.Fatalf("want *MissingCapsuleError, got %T: %v", err, err)
	}
	if miss.Dir != testCapsuleDir(r, wi) {
		t.Errorf("typed error dir = %q, want %q", miss.Dir, testCapsuleDir(r, wi))
	}
	assertNothingMaterialized(t, r, wi, sp)
}

// The recovery engine refuses a vanished-after-start capsule with the
// loud blocked verdict (surfaced to the operator) and no rebuild.
func TestRecoverCoreMissingCapsuleBoundedFail(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, hub, sp)
	j := seedJob(r, "wi_missing_recover")
	if err := os.RemoveAll(j.capsuleDir); err != nil {
		t.Fatal(err)
	}

	var tapped []map[string]any
	r.cfg.EventTap = func(w, kind string, body map[string]any) {
		if kind == "missing_midflight_capsule" {
			tapped = append(tapped, body)
		}
	}

	r.recoverOp(context.Background(), j, "daemon_restart", "daemon restart (crash mid-run)")

	msg := recvRT(t, toOB)
	if msg.Kind != protocol.RTFinished || msg.Reason != protocol.FinishBlocked {
		t.Fatalf("expected RTFinished(blocked), got %+v", msg)
	}
	if !strings.Contains(msg.Detail, j.wi) || !strings.Contains(msg.Detail, j.capsuleDir) {
		t.Errorf("blocked detail must name the WI and the missing path, got %q", msg.Detail)
	}
	if len(tapped) != 1 {
		t.Fatalf("expected exactly one missing_midflight_capsule event, got %d", len(tapped))
	}
	if hub.status[j.wi] != api.StatusBlocked {
		t.Errorf("hub status = %q, want blocked (the surfaced verdict)", hub.status[j.wi])
	}
	assertNothingMaterialized(t, r, j.wi, sp)
}

// One bad WI never takes the others down (v2 §6): the daemon starts, the
// good WI recovers, only the bad one is blocked.
func TestRestartBlocksOnlyBadWI(t *testing.T) {
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, hub, sp)
	events := tapEvents(r)

	bad := matrixRecord(r, "wi_bad", 0, "")
	good := matrixRecord(r, "wi_good", 0, "")
	hub.hold(bad.WorkItemID, bad.SessionID, api.StatusRunning)
	hub.hold(good.WorkItemID, good.SessionID, api.StatusRunning)
	if err := os.MkdirAll(good.CapsuleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeStateFile(t, r, []*jobRecord{bad, good})

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart must not fail for one bad WI: %v", err)
	}
	if n := sp.callCount(); n != 1 {
		t.Fatalf("exactly the good WI recovers (1 derivation), got %d", n)
	}
	var kinds []string
	for i := 0; i < 2; i++ {
		msg := recvRT(t, toOB)
		kinds = append(kinds, fmt.Sprintf("%v:%s", msg.Kind, msg.WorkItem))
	}
	want := []string{fmt.Sprintf("%v:%s", protocol.RTRecovered, good.WorkItemID), fmt.Sprintf("%v:%s", protocol.RTFinished, bad.WorkItemID)}
	for _, w := range want {
		if !slices.Contains(kinds, w) {
			t.Fatalf("expected RT messages %v, got %v", want, kinds)
		}
	}
	if hub.status[bad.WorkItemID] != api.StatusBlocked || hub.status[good.WorkItemID] != api.StatusRunning {
		t.Fatalf("only the bad WI is blocked: bad=%q good=%q", hub.status[bad.WorkItemID], hub.status[good.WorkItemID])
	}
	if r.jobOf(bad.WorkItemID) != nil || r.jobOf(good.WorkItemID) == nil {
		t.Fatal("the bad job must be released and the good one live")
	}
	_ = events
}
