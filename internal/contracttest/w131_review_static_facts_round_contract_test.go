// carries: workflows/_shared/skills/review.md, workflows/_shared/skills/build.md, workflows/develop/steps/build.md

package test

// w131_review_static_facts_round_contract_test.go (polyforge#131, AC5):
// the review skill's round contract keeps the two static facts reds
// blocking in every round — a unit that does not resolve or whose
// commits name a test-first problem, and a carries problem — never
// downgraded as a hit outside the fix surface, the one exception an
// owner message that explicitly accepts the finding; the two facts are
// the same static ones build's close runs without the replay, so a red
// here is a red handed off on. Whitespace-flattened, so a rewrap keeps
// them pinned; the fixtures' behaviour is the Python suite's under
// workflows/_shared/scripts/tests/.

import (
	"strings"
	"testing"
)

func TestW131ReviewStaticFactsBlockEveryRound(t *testing.T) {
	flat := w68Flat(t, "workflows/_shared/skills/review.md")
	for _, w := range []string{
		// the round contract: the two static facts reds block every round
		"From the second round on, a blocking finding is one of: a residual; a new defect on the fix surface; a `bug` anywhere in the change; a `project_rule` violation anywhere; and — in every round, never downgraded as a hit outside the fix surface — the two static facts reds: a unit that does not resolve or whose `commits` names a test-first problem, and a carries problem.",
		"only an owner message that explicitly accepts the finding is an exception to them",
		// the classing bullets name the same two facts and the same exception
		"The resolution and commits facts are static — the same ones build's close ran without the replay (`facts.py build`) — and they block in every round, an owner message that explicitly accepts the finding excepted",
		"each `problems` entry is blocking `test_weakened`, in every round (the same conclusion build's close reached) — an owner message that explicitly accepts the finding excepted",
	} {
		if !strings.Contains(flat, strings.Join(strings.Fields(w), " ")) {
			t.Errorf("workflows/_shared/skills/review.md must state %q", w)
		}
	}
}
