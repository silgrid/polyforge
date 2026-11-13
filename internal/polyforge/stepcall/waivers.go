package stepcall

// waivers.go: the owner waivers of a work item's run, read from the ledger
// (workflow contract 4, design #483 §5.3,). A waiver is an
// accepted `pf step handoff --status waive --per-owner <at>` of a waivable
// gate: the daemon checked, when it accepted the hand-off, that the cited
// owner message is on the ask record. The wrap writes every one into the
// work item's attrs.waivers — the owner's words verbatim, the agent's
// reading, the times — for every lane; a later lane reads them there.
// The daemon writes the record, never a step.

import (
	"fmt"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// Waiver is one owner waiver as attrs.waivers carries it.
type Waiver struct {
	// Gate is the waived gate, Round its round and SpawnN the dispatch
	// that handed the waiver off.
	Gate   string `json:"gate"`
	Round  int64  `json:"round,omitempty"`
	SpawnN int64  `json:"spawn_n,omitempty"`
	// OwnerWords is the cited owner message verbatim, OwnerBy who wrote
	// it and OwnerMessageAt its stamp on the ask record.
	OwnerWords     string `json:"owner_words"`
	OwnerBy        string `json:"owner_by,omitempty"`
	OwnerMessageAt string `json:"owner_message_at"`
	// AgentReason is the agent's one-line reading (`--reason`).
	AgentReason string `json:"agent_reason,omitempty"`
	// WaivedAt is when the daemon accepted the hand-off.
	WaivedAt string `json:"waived_at"`
}

// Waivers answers every waiver of the run in hand-off order: each
// accepted `waive` hand-off on the ledger with the owner message it cites
// from the ask record. A cited message the record does not hold is an
// error — the waiver exists, its words cannot be carried, and the caller
// must not wrap without them.
func Waivers(lines []Line, asks []capsule.AskLine) ([]Waiver, error) {
	byAt := map[string]capsule.AskLine{}
	for _, a := range asks {
		if a.Kind == capsule.AskKindReply || a.Kind == capsule.AskKindMessage {
			byAt[a.At] = a
		}
	}
	var out []Waiver
	for _, l := range lines {
		if !accepted(l, VerbHandoff) || l.Handoff == nil || l.Handoff.Status != StatusWaive {
			continue
		}
		msg, ok := byAt[l.Handoff.PerOwner]
		if !ok {
			return nil, fmt.Errorf("the waiver of %s (dispatch %d) cites owner message %q, which the ask record does not hold", l.Step, l.Spawn, l.Handoff.PerOwner)
		}
		out = append(out, Waiver{
			Gate: l.Step, Round: l.Round, SpawnN: l.Spawn,
			OwnerWords: msg.Text, OwnerBy: msg.By, OwnerMessageAt: msg.At,
			AgentReason: l.Handoff.Reason, WaivedAt: l.At,
		})
	}
	return out, nil
}
