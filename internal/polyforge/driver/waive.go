package driver

// waive.go: a gate's FAIL the owner waived (workflow contract 4: the gate
// hands off `waive --per-owner <at>` on a `waivable: true` gate; intent.go
// routes it here). No rewind, no charge: the gate is accepted on the
// waiver and the workflow continues with the next step — except the
// workflow's last agent step (the delivery), which cannot be skipped and
// runs again in place. The owner's words and the agent's reading reach the
// wrap from the call ledger and the ask record (stepcall.Waivers), never
// from execution.json.

import (
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// waivedAcceptDetail marks the accepted attempt acceptWaivedGateLocked
// archives: a gate whose newest accepted attempt carries it stands done on
// the owner's waiver, not on a pass.
const waivedAcceptDetail = "the owner waived the FAIL verdict's findings"

// applyWaiveLocked lands the owner's waiver of the gate's FAIL verdict
// (out is the verdict): the gate done on the waiver, or re-run in place
// when it is the workflow's last agent step. replyAt and by name the owner
// message the hand-off cites.
func (d *Driver) applyWaiveLocked(gate string, st *capsule.Step, out map[string]any, replyAt, by string, info *appliedInfo) {
	if d.workflow.lastAgentStep(gate) {
		d.closeGateRoundLocked(gate, out, "the owner waived the FAIL verdict's findings; "+gate+" re-runs")
	} else {
		d.acceptWaivedGateLocked(st)
	}
	info.extra = append(info.extra, appliedEvent{"owner_waived", map[string]any{
		"spawn_n": d.spawnN, "step": gate, "gate": gate, "reply_at": replyAt, "by": by,
	}})
}

// acceptWaivedGateLocked accepts a gate whose FAIL the owner waived: done,
// with the accepted attempt marked as the waiver's.
func (d *Driver) acceptWaivedGateLocked(st *capsule.Step) {
	st.Status = capsule.StepDone
	st.Attempts = append(st.Attempts, capsule.Attempt{
		Cause:      "accepted",
		AttemptSeq: nextAttemptSeq(st),
		StartedAt:  st.StepStartedAt,
		ArchivedAt: d.stamp(),
		Detail:     waivedAcceptDetail,
	})
}

// stampAfter answers a after b for two RFC3339Nano stamps (the
// lexicographic order is not the time order once trailing zeros are
// trimmed); an unparsable stamp falls back to the string order.
func stampAfter(a, b string) bool {
	ta, ea := time.Parse(time.RFC3339Nano, a)
	tb, eb := time.Parse(time.RFC3339Nano, b)
	if ea != nil || eb != nil {
		return a > b
	}
	return ta.After(tb)
}
