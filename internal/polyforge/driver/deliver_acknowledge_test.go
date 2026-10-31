package driver

// deliver_acknowledge_test.go: the deliver merge rule's no-review carve-out
// (a lane without a review step needs no review head — verification's
// verified heads stand in) and the already-merged acknowledge path (a
// merged delivery outside the merge rule is accepted merged:true once a
// human answered in the round, with no second question). Both pin
// existing driver behaviour the contract text now states.

import (
	"strings"
	"testing"
)

func TestNoReviewDeliveryNeedsNoReviewHead(t *testing.T) {
	repos := deliverCapsule(t)
	noReview := deliverSpec(repos, false, 0)
	noReview.HasReview = false
	noReview.ReviewedHeads = nil
	if err := ValidateStepResult(deliverRes(OutcomeCompleted, mergedOut, ev(goodPush)), noReview); err != nil {
		t.Fatalf("a lane without review merges on verified_heads alone: %v", err)
	}
	reviewed := deliverSpec(repos, true, 0)
	reviewed.ReviewedHeads = nil
	if err := ValidateStepResult(deliverRes(OutcomeCompleted, mergedOut, ev(goodPush)), reviewed); err == nil || !strings.Contains(err.Error(), "no review covers") {
		t.Fatalf("a reviewed lane still needs a review head: %v", err)
	}
}

func TestAcknowledgedMergedDeliveryCompletes(t *testing.T) {
	repos := deliverCapsule(t)
	spec := deliverSpec(repos, true, 0)
	spec.VerifiedHeads = map[string]string{"demo": "0000000000000000000000000000000000000000"} // HEAD is not the verified commit
	if err := ValidateStepResult(deliverRes(OutcomeCompleted, mergedOut, ev(goodPush)), spec); err == nil {
		t.Fatal("an already-merged delivery whose HEAD is not verified needs the human's acknowledge")
	}
	spec.HumanAnswers = 1 // the round's acknowledge answer
	res := deliverRes(OutcomeCompleted, mergedOut, ev(goodPush))
	if err := ValidateStepResult(res, spec); err != nil || res.Outcome != OutcomeCompleted {
		t.Fatalf("after acknowledge the delivery completes merged:true without asking again: %v %s", err, res.Outcome)
	}
}
