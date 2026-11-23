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
		"From the second round on, a blocking finding is one of: a residual; a new defect on the fix surface; a `bug` anywhere in the change; a `project_rule` violation anywhere; and the two static facts reds — a unit that does not resolve or whose `commits` names a test-first problem, and a carries problem — when the hand-off gate's `facts_proof` result did not carry them",
		"when the gate's proof stood they are non-blocking",
		// the classing bullets name the same two facts and the same condition
		"The resolution and commits facts are static — the same ones build's close ran without the replay (`facts.py build`) — and they follow the hand-off gate's subset above: non-blocking when its proof stood, blocking `guarantee` when the gate was bypassed",
		"each `problems` entry is in the hand-off gate's subset above",
	} {
		if !strings.Contains(flat, strings.Join(strings.Fields(w), " ")) {
			t.Errorf("workflows/_shared/skills/review.md must state %q", w)
		}
	}
}
