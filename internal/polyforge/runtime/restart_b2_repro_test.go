package runtime

// Regression test for wi_WLczIJMOLDhq item B2 (2026-10-13 audit): a SEALING
// record that carries NO terminal stop reason is finished as WRAPPED by
// the restart's seal finisher (restartResume → stopOp, the
// `!isTerminalStopReason(reason)` default).
//
// Reported failure: the daemon crashed mid-seal before the close's
// verdict (or its note) was persisted; on restart the finisher guesses
// "wrapped" — reporting SUCCESS for a close nobody ever decided.
//
// Expected behavior per the work item: with no recorded reason the seal
// finishes BLOCKED (the human decides), with the recorded reason passed
// through verbatim when one IS present (the verbatim case is already
// pinned by TestRestartFinishesInterruptedSeal).
//
// Regression test: before the fix the no-reason seal finished wrapped.

import (
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

func TestRestartSealingWithoutRecordedReasonFinishesBlocked(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, hub, sp)
	wi := "wi_sealnoreason"

	// a job the previous daemon left mid-seal, crashed before ANY
	// terminal reason was persisted: state=sealing, stop_reason empty.
	j := seedJob(r, wi)
	initGitCapsule(t, j)
	pid, startedAt := spawnSleeper(t, wi)
	rec := matrixRecord(r, wi, pid, startedAt)
	rec.State = string(StateSealing)
	rec.StopReason = ""
	rec.CapsuleDir = j.capsuleDir
	rec.GitDir = j.git.GitDir
	rec.RemoteURL = j.git.RemoteURL
	rec.Transcript = j.transcript
	hub.hold(wi, rec.SessionID, api.StatusRunning)
	writeStateFile(t, r, []*jobRecord{rec})
	// drop the in-memory seed so Restart owns the job
	r.dropJob(j)

	if err := restartSettled(t, r); err != nil {
		t.Fatalf("Restart: %v", err)
	}
	waitDead(t, pid)
	msg := recvRT(t, toOB)
	if msg.Kind != protocol.RTFinished || msg.Reason != protocol.FinishBlocked {
		t.Fatalf("REPORTED FAILURE (B2): the interrupted seal with NO recorded reason must finish blocked (the human decides), "+
			"got RTFinished reason %q (kind %v) — the finisher guessed the verdict instead of leaving it to a human", msg.Reason, msg.Kind)
	}
	if hub.endCount() != 1 || hub.lastEnd().Status != protocol.FinishBlocked {
		t.Fatalf("REPORTED FAILURE (B2): session-end must carry the blocked verdict, got %+v (ends=%d)",
			hub.lastEnd(), hub.endCount())
	}
	// the B2 note (说明) must reach SessionEnd: the blocked verdict carries
	// WHY it was blocked — a seal finished by nobody's decision, for a
	// human to read on the hub.
	if note := hub.lastEnd().Note; !strings.Contains(note, "seal interrupted by a daemon crash") ||
		!strings.Contains(note, "finished blocked for a human to decide") {
		t.Fatalf("the blocked session-end must carry the cause-unknown note, got %q", note)
	}
	if recs := loadRecords(t, r); len(recs) != 0 {
		t.Fatalf("finished seal must drop the record, got %+v", recs)
	}
	if sp.callCount() != 0 {
		t.Fatal("finishing a seal must not revive the agent")
	}
	unseal(t, testWorkspace(r))
}
