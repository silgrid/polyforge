package daemon

// wake_terminal_scoped_test.go — the scoped decision wake
// must stay scoped when the job's terminal close DROPS it (stopOp moves
// the job to DONE and dropJob removes it — Info answers false from then
// on). Both scope facts (the WI's origin chat id and its
// project-mapping workspace) come from the job record, so they must
// survive the drop: a scope that died with the job would read an empty
// eligible set and fall back to the owner-wide broadcast — the
// terminal decision (completed / failed / blocked) reaching every
// subscribed chat of the owner daemon-wide, the pi#122 blast radius
// again. The review's rewind scenario: the origin unsubscribed (or
// there is none), the job reaches a terminal state and is dropped, the
// cross-workspace and untagged owner-routed subscribers print nothing,
// and the eligible same-workspace chat still gets the decision.
//
// The scene helpers are the acceptance e2e's (same package, the frozen
// wake_target_handover_e2e_test.go): a real daemon over a
// two-workspace mapping, real chats, the real resident watcher.

import (
	"strings"
	"testing"
	"time"
)

func TestTerminalDecisionStaysScopedAfterTheJobDrop(t *testing.T) {
	for _, originChat := range []bool{true, false} {
		s := gmi14Setup(t, originChat)
		// the full pi#122 audience subscribed through owner routing:
		// the second wsA chat (the eligible one), a wsA chat that mutes
		// the WI itself, the wsB chat and the untagged chat.
		for _, chat := range []string{s.a2, s.a3, s.b1, s.untagged} {
			gmi14Own(t, s.td, chat)
		}
		gmi14Mute(t, s.td, s.a3, s.wi) // its explicit wish: nothing, ever
		if originChat {
			gmi14Mute(t, s.td, s.origin, s.wi)
		}
		// the terminal close: the job is sealed, moved to DONE and
		// dropped — the decision it carries is completed (wrapped).
		if _, err := s.td.client.Wrap(s.wi, "wrap"); err != nil {
			t.Fatalf("wrap %s: %v", s.wi, err)
		}
		waitGone(t, s.td, s.wi)
		if _, held := s.td.rt.Info(s.wi); held {
			t.Fatalf("the wrapped close must drop the job (%s)", s.wi)
		}
		// the eligible same-workspace chat still gets the decision
		if out := gmi14RunChat(t, s.td, s.a2, 5*time.Second); !strings.Contains(out, "completed") {
			t.Fatalf("the WI's workspace's live subscribed chat must still get the terminal decision: %q", out)
		}
		for _, c := range []struct{ chat, why string }{
			{s.b1, "a cross-workspace chat"},
			{s.untagged, "an untagged chat"},
			{s.a3, "a chat that muted the WI itself"},
		} {
			if out := gmi14RunChat(t, s.td, c.chat, 400*time.Millisecond); strings.Contains(out, "[polyforge]") {
				t.Fatalf("REPORTED pi#122 BLAST RADIUS: the terminal decision reached %s after the drop: %q", c.why, out)
			}
		}
	}
}
