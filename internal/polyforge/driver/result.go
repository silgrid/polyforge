// result.go: the result a dispatch delivers — its accepted `pf step
// handoff`, read off the capsule's call ledger (handoff.go, ledgerHandoff)
// — as the shape the transitions route (handoffShape): the outcome, the
// gate's fail verdict, the blocker of a blocked hand-off and the intent of
// a hand-off citing the owner. Workflow contract 4 removed the result
// envelope and its validation: the daemon checks a hand-off when the step
// calls (daemon/stephandoff.go) and records only the one it accepted.
package driver

import (
	"encoding/json"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// Outcome is the routed outcome of a hand-off (handoffShape).
type Outcome string

const (
	OutcomeCompleted  Outcome = "completed"
	OutcomeGateFailed Outcome = "gate_failed"
	OutcomeIncomplete Outcome = "incomplete"
)

// StepResult is one dispatch's accepted hand-off as the transitions route
// it: the identity and fence of the dispatch, the hand-off as the call
// ledger records it (Handoff, HandoffCall: the call's seq) and the routed
// shape handoffShape gives it (Outcome, Output, Blocker, Intent).
type StepResult struct {
	WIID    string
	StepID  string
	SpawnN  int64 // fencing: only the current spawn's results count
	Outcome Outcome
	// Output is a gate fail's verdict ({verdict: fail, fail_reason}), the
	// text a rewind target is told; "{}" for every other routed outcome.
	Output json.RawMessage
	// Blocker (incomplete only) is the obstacle that parks the workflow:
	// {reason, evidence, required_action}, recorded in the blocked reason.
	Blocker map[string]any
	// Intent is the owner's move a hand-off citing the owner carries
	// (cancel, waive, retry or rewind; intent.go).
	Intent      *StepIntent
	SubmittedAt time.Time
	// Handoff is the dispatch's accepted `pf step handoff` as the call
	// ledger records it (workflow contract 4, handoff.go) and HandoffCall
	// that call's seq: the one result a dispatch delivers.
	Handoff     *stepcall.Handoff
	HandoffCall int64
}
