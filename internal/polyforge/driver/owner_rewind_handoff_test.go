package driver

// owner_rewind_handoff_test.go: the rewind the owner's word orders, as the
// step hands it off (`rewind --to <step> --per-owner <at>`, workflow
// contract 4) — the only door to it since `pf rewind` was removed.
// Ported from the command's driver tests:
// the record and the event it lands, the budget it never spends, and the
// write's own guard on the target.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// The rewind lands [target, current] archived (the target with the rewind
// target cause naming the step it came from, the others superseded), one
// owner-class ledger record with who/when/from/to and the owner's words
// verbatim, the owner_rewind event, and the target resuming its previous
// conversation. The owner's message may be any length a message may be
// (capsule.MaxOwnerMessageBytes): the 8000-byte bound was `pf rewind
// --reason`'s and left with it.
func TestOwnerRewindHandoffLandsTheRecord(t *testing.T) {
	const at = "2026-11-11T09:30:00.000000001Z"
	words := "the plan skipped the migration — " + strings.Repeat("界", 3000) // over 8000 bytes
	d, sp, _, dir := handoffEnv(t, 2, spec("plan"), spec("build"), gateSpec("check", "build"))
	log := d.log.(*eventLog)
	ownerSaid(t, dir, "check", at, words)
	sp.script = []fakeEntry{
		done(`{}`), done(`{}`),
		handedOff(&stepcall.Handoff{Status: stepcall.StatusRewind, To: "plan", PerOwner: at, Reason: "the owner wants the plan redone"}, nil),
		done(`{}`), done(`{}`), done(`{}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := d.Snapshot()
	if x.WorkflowStatus != StatusCompleted {
		t.Fatalf("the lane runs again from plan and completes: %q", x.WorkflowStatus)
	}
	recs := x.RewindStats.Rewinds
	if len(recs) != 1 || x.RewindStats.Count != 1 {
		t.Fatalf("ledger %+v, want one record", x.RewindStats)
	}
	r := recs[0]
	if r.Gate != "check" || r.ToStep != "plan" || r.Class != capsule.RewindOwner || r.By != "owner" || r.At == "" ||
		!strings.Contains(r.Reason, words) || !strings.Contains(r.Reason, "the owner wants the plan redone") {
		t.Fatalf("ledger record %+v, want check→plan, owner class, by/at, the owner's words and the step's reading", r)
	}
	if AgentRewindCount(recs) != 0 || OwnerRewindCount(recs) != 1 {
		t.Fatalf("counts agent=%d owner=%d, want 0 and 1", AgentRewindCount(recs), OwnerRewindCount(recs))
	}
	archived := func(step, cause string) bool {
		for _, a := range x.Steps[step].Attempts {
			if a.Cause == cause && (cause != CauseGateFailRewind || a.RewindGate == "check") {
				return true
			}
		}
		return false
	}
	if !archived("plan", CauseGateFailRewind) || !archived("build", CauseSupersededByRewind) || !archived("check", CauseSupersededByRewind) {
		t.Fatalf("archives: plan %+v build %+v check %+v", x.Steps["plan"].Attempts, x.Steps["build"].Attempts, x.Steps["check"].Attempts)
	}
	if !log.has(EvOwnerRewind, "to", "plan") || !log.has(EvOwnerRewind, "from", "check") || !log.has(EvOwnerRewind, "by", "owner") {
		t.Fatalf("owner_rewind event missing who/from/to: %v", log.kinds())
	}
	if got := sp.dispatched(); len(got) != 6 || sp.spawnAt(3).StepID != "plan" || !sp.spawnAt(3).Resume {
		t.Fatalf("the rewound plan resumes its previous conversation: %v", got)
	}
}

// The owner's rewind spends no budget: with max_rewinds spent by a gate's
// fail it still lands, and the two counts stay apart.
func TestOwnerRewindHandoffSpendsNoBudget(t *testing.T) {
	d, sp, _, dir := handoffEnv(t, 1, spec("build"), gateSpec("check", "build"))
	var at string
	// the owner's message to check's second round (after the fail closed
	// the first): stamped now, so it falls in that round
	said := func() {
		at = time.Now().UTC().Format(time.RFC3339Nano)
		ownerSaid(t, dir, "check", at, "go back to build once more")
	}
	rewind := &stepcall.Handoff{Status: stepcall.StatusRewind, To: "build", Reason: "the owner asked for another build"}
	sp.script = []fakeEntry{
		done(`{}`),
		gateFAIL(),
		done(`{}`),
		{res: &StepResult{Handoff: rewind}, act: func() { said(); rewind.PerOwner = at }},
		done(`{}`), done(`{}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := d.Snapshot()
	recs := x.RewindStats.Rewinds
	if x.WorkflowStatus != StatusCompleted || len(recs) != 2 || recs[0].Class != capsule.RewindCharged || recs[1].Class != capsule.RewindOwner {
		t.Fatalf("the owner's rewind lands past the spent budget: %q %+v", x.WorkflowStatus, x.RewindStats)
	}
	if AgentRewindCount(recs) != 1 || OwnerRewindCount(recs) != 1 || MaxRewinds(d.workflow) != 1 {
		t.Fatalf("counts agent=%d owner=%d of max %d, want 1, 1 of 1", AgentRewindCount(recs), OwnerRewindCount(recs), MaxRewinds(d.workflow))
	}
}

// The write's own guard: only an agent step before the current one is a
// target, and a finished workflow is sent back nowhere.
func TestOwnerRewindWriteGuardsTheTarget(t *testing.T) {
	d, _, _, _ := testEnv(t, spec("plan"), spec("build"), gateSpec("review", "build"), spec("after"))
	mustInit(t, d)
	n := int64(1)
	for _, s := range []string{"plan", "build"} {
		mustStart(t, d, s, n)
		if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: s, SpawnN: n, Result: completedResult(s, n, `{}`)}); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
		n++
	}
	mustStart(t, d, "review", n)
	check := func(to string) error {
		d.mu.Lock()
		defer d.mu.Unlock()
		return d.checkOwnerRewindLocked(capsule.OwnerRewindRequest{To: to, Reason: "x", By: "owner"})
	}
	for _, to := range []string{"plan", "build"} {
		if err := check(to); err != nil {
			t.Fatalf("%s is an earlier agent step: %v", to, err)
		}
	}
	for _, to := range []string{"review", "after", "nope"} {
		if err := check(to); err == nil {
			t.Fatalf("%s is no target before review, yet the write takes it", to)
		}
	}
	if err := d.transition(TransitionRequest{Kind: TransTerminal, Status: StatusFailedGate, Reason: "x", SpawnN: d.currentSpawnN()}); err != nil {
		t.Fatalf("terminal: %v", err)
	}
	if err := check("plan"); err == nil || !strings.Contains(err.Error(), "ended") {
		t.Fatalf("a finished workflow is sent back nowhere: %v", err)
	}
}
