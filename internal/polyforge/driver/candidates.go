// candidates.go: the v2 model chain as a PURE function. The candidate
// pointer is not a persisted field: it is recomputed from the step's
// archived attempts alone, so a crash-recovered driver derives the same
// ordinal as its predecessor without any process memory.
//
// The one advancement rule: the pointer is the number of archived
// infra_failure attempts whose FailureCause is model-class (IsModelClass)
// recorded after the most recent rewind archive (gate_fail_rewind,
// gate_fail_rewind_text_only, gate_fail_rewind_mechanical or
// superseded_by_rewind), capped at the last candidate. Host-class deaths
// (FailExitHost) block the step instead and never advance; every other
// cause is a same-model retry. The rule reads the death cause the driver
// itself assigned — never free text.
package driver

import "github.com/silgrid/polyforge/internal/common/capsule"

// IsModelClass reports whether a failure cause is a model-class death:
// the only causes that advance the candidate pointer.
func IsModelClass(cause string) bool {
	switch cause {
	case FailModelError, FailModelHung, FailStepTimeoutSlowModel, FailExitModel:
		return true
	}
	return false
}

// CandidatePointer answers the 0-based index into candidates of the
// step's current effective model, recomputed purely from the archived
// attempts: the count of model-class infra failures after the most
// recent rewind archive — gate_fail_rewind, gate_fail_rewind_text_only
// (the light rewind, polyforge#222: a light rewind never burns a
// candidate either), gate_fail_rewind_mechanical (the mechanical light
// rewind, polyforge#260: likewise) or superseded_by_rewind — (from the
// start when there is none), capped at len(candidates)-1. Advancing is
// bounded, never wrapping: exhausting the list is not a terminal
// condition (the failure counter owns terminality).
//
// An empty candidate list answers -1: no switchable chain exists and the
// harness adapter's default model stays in effect.
func CandidatePointer(attempts []capsule.Attempt, candidates []string) int {
	if len(candidates) == 0 {
		return -1
	}
	count := 0
	for _, a := range attempts {
		switch a.Cause {
		case CauseGateFailRewind, CauseGateFailRewindTextOnly, CauseGateFailRewindMechanical, CauseSupersededByRewind:
			count = 0 // counting restarts after the last rewind archive
		case CauseInfraFailure:
			if IsModelClass(a.FailureCause) {
				count++
			}
		}
	}
	return min(count, len(candidates)-1)
}
