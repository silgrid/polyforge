// candidates.go: the v2 model chain as a PURE function. The candidate
// pointer is not a persisted field of its own: it is recomputed from the
// step's persisted Step.Candidate (the model of its current/next dispatch:
// written at step_start and at the failure that decides a switch), the
// archived attempts and the LIVE candidate list of the dispatch
// (polyforge#33: composed at every dispatch, never snapshotted), so a
// crash-recovered driver derives the same model as its predecessor without
// process memory.
//
// The advancement rule (anchor + pending): the anchor is the persisted
// Step.Candidate, located BY IDENTITY in the live list: found → its index,
// empty (the era reset after a rewind of a gate or of a step between the
// target and the gate; a resuming rewind target keeps its anchor)
// or removed from the list → the list head. Pending is the
// one advancement the newest non-neutral attempt still owes — and it owes
// it only when that attempt ran the anchor model (an attempt of another
// model means the switch it implied is already persisted in the anchor):
// a model-class death owes one step (workflow contract 4, design #483 §7:
// every model-class death switches at once). The pointer is anchor+pending
// capped at the last candidate, so a chain end keeps its end model across
// blocked/resume, and an owner edit that appends a candidate lets the same
// formula switch to it. Every other cause keeps the model. The rule reads
// the death cause the driver itself assigned — never free text.
package driver

import (
	"slices"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// IsModelClass reports whether a failure cause is a model-class death
// (an error, a model that does not resolve, too slow, hung): the only
// causes that advance the candidate pointer from its Step.Candidate
// anchor.
func IsModelClass(cause string) bool {
	switch cause {
	case FailModelError, FailModelHung, FailStepTimeoutSlowModel, FailExitModel:
		return true
	}
	return false
}

// neutralCause reports the infra causes that neither count toward nor
// break a model-class streak: the uncounted ones (throttles and
// audit-only interruptions).
func neutralCause(c string) bool {
	switch c {
	case FailRateLimited, FailUpstreamTransient, FailDriverRelaunch, FailDaemonRestart:
		return true
	}
	return false
}

// streakEra answers the start of the step's current streak era for a NEW
// death: the owner's last continue of the blocked step
// (Supervision.ResumedAt; zero = none). An attempt that started before it
// belongs to an earlier era, so the failure decision's streaks — the EWR
// and model-class rotations, the retry pacing — and RetryOwed do not reach
// across the owner's continue. Model selection (CandidatePointer /
// pendingAdvance) reads no era: the advance a chain-end or no-chain death
// still owes is exactly what the owner's "edit the list, then pf resume"
// collects.
func streakEra(st *capsule.Step) time.Time {
	var era time.Time
	if st.Supervision != nil {
		if t, err := time.Parse(time.RFC3339Nano, st.Supervision.ResumedAt); err == nil {
			era = t
		}
	}
	return era
}

// beforeEra reports whether an attempt started before the streak era.
func beforeEra(a capsule.Attempt, era time.Time) bool {
	t, err := time.Parse(time.RFC3339Nano, a.StartedAt)
	return err == nil && t.Before(era)
}

// ModelClassStreak counts the step's trailing model-class deaths on one
// candidate within one streak era (attempts started at or after era,
// streakEra: the owner's last continue; zero = every attempt): neutral
// causes are skipped, every other attempt (an accepted result, a rewind, a
// counted failure of another class, a different candidate, an earlier
// era) ends the streak.
func ModelClassStreak(attempts []capsule.Attempt, candidate string, era time.Time) int {
	n := 0
	for i := len(attempts) - 1; i >= 0; i-- {
		a := attempts[i]
		if a.Cause != CauseInfraFailure {
			break
		}
		if neutralCause(a.FailureCause) {
			continue
		}
		if !IsModelClass(a.FailureCause) || a.Candidate != candidate || beforeEra(a, era) {
			break
		}
		n++
	}
	return n
}

// pendingAdvance answers the one advancement the step's newest
// non-neutral attempt still owes against the anchor (the persisted
// Step.Candidate): 0 or 1. Trailing neutral attempts (an audit-only
// daemon_restart/driver_relaunch audit entries, a throttle) are skipped.
// The advance is owed only while the anchor still is the model that
// attempt ran: a death that decided a switch persisted it into the anchor,
// so what stays owed is a chain-end or no-chain death followed by an owner
// list edit that adds a later candidate. An attempt that ran a model other than
// the anchor owes nothing: the switch it implied is already the anchor.
func pendingAdvance(attempts []capsule.Attempt, anchor string) (int, string) {
	j := len(attempts) - 1
	for j >= 0 && attempts[j].Cause == CauseInfraFailure && neutralCause(attempts[j].FailureCause) {
		j--
	}
	if j < 0 {
		return 0, ""
	}
	a := attempts[j]
	if a.Cause != CauseInfraFailure || a.Candidate != anchor {
		return 0, ""
	}
	if IsModelClass(a.FailureCause) {
		return 1, a.FailureCause
	}
	return 0, ""
}

// CandidatePointer answers the 0-based index into candidates of the
// step's current effective model (anchor + pending, see the file header),
// recomputed from the persisted anchor (Step.Candidate), the archived
// attempts (their streaks, across every era) and the live list alone. An empty list answers -1: no switchable chain exists and the
// harness adapter's default model stays in effect.
func CandidatePointer(anchor string, attempts []capsule.Attempt, candidates []string) int {
	if len(candidates) == 0 {
		return -1
	}
	if anchor == "" {
		return 0
	}
	idx := slices.Index(candidates, anchor)
	if idx < 0 {
		return 0 // the model left the list: the new list head
	}
	n, _ := pendingAdvance(attempts, anchor)
	return min(idx+n, len(candidates)-1)
}
