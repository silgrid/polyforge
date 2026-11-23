package driver

// autoanswer.go: the daemon's standing authorization for a low-star
// question card (polyforge#239, owner ruling 2026-11-23): when every row
// of a STRICT four-column question table is graded ★ or ★★, nobody
// answered it before T0+timeout, and no protected stop owns the step, the
// daemon appends one reply of its own — a reserved identity, never the
// owner's — adopting each row's recommended option verbatim.
//
// This file owns the mechanical half: the card parser (only the one
// unambiguous shape the template teaches), the reply text (the real
// elapsed wait, never the configured threshold), and the decision of
// whether a step is a protected stop. The commit, the delivery and the
// decision-feed projection live beside their own paths (ask.go, relay.go,
// decisions.go).

import (
	"strings"
	"time"
)

// autoAnswerAuthorizationDate is the date of the owner ruling that
// authorizes the automatic reply (polyforge#239); it is written into the
// reply so the audit trail names the decision it rests on.
const autoAnswerAuthorizationDate = "2026-11-23"

// autoAnswerCardHeader is the one header the template teaches; the card
// must carry it cell for cell.
var autoAnswerCardHeader = []string{"问题", "推荐选项", "理由", "重点关注程度"}

// parseAutoAnswerCard answers the recommended option of every row of text's
// one strict four-column question table, or ok=false when the card is not
// exactly that shape (ok is all-or-nothing: one unsafe or ambiguous row
// refuses the whole card).
func parseAutoAnswerCard(text string) (recommendations []string, ok bool) {
	return nil, false
}

// autoAnswerReplyText renders the reply the daemon appends for a card whose
// recommendations were adopted after elapsed: the fixed authorization
// sentence (with the REAL wait, never the configured threshold) and one
// numbered line per recommendation, in card order.
func autoAnswerReplyText(recommendations []string, elapsed time.Duration) string {
	return ""
}

// autoAnswerProtectedStop reports whether stepID is a protected stop — a
// human gate the owner's ruling 2026-11-23 keeps human-only, or a step of
// a lane this build cannot verify. content is the work item's verbatim
// content (runtime-state/work-item.json).
func autoAnswerProtectedStop(lane, stepID, content string) bool {
	return true
}

// autoAnswerConfirmPlan reports whether the ticket carries the
// `confirm_plan: true` marker (a build stop before the first test).
func autoAnswerConfirmPlan(content string) bool {
	return strings.Contains(content, "confirm_plan: true")
}
