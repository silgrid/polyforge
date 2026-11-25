package daemon

// autoanswer_lane_test.go: the lane-verifiability predicate the driver's
// automatic reply consults (polyforge#239). It enumerates the lanes the
// daemon's workflows/ tree ships and refuses every other name, so a custom
// lane's stops keep waiting for the owner.

import "testing"

func TestShippedLaneVerifier(t *testing.T) {
	v := shippedLaneVerifier("../../../workflows")
	for _, name := range []string{"develop", "epic", "integrate", "micro", "release"} {
		if !v(name) {
			t.Errorf("%s is a shipped lane but the verifier refused it", name)
		}
	}
	if v("custom-lane") || v("") {
		t.Fatal("the verifier accepted a lane the workflows/ tree does not ship")
	}
}
