package cli

import "testing"

// TestW521ConfigWorkspaceReportsSkip pins the read face carrying a step
// entry's `skip` (a skip-only entry included), which the Pi Web panel reads
// as the old value of a write so a rollback can restore the switch (r1#1).
func TestW521ConfigWorkspaceReportsSkip(t *testing.T) {
	doc := readWorkspaceDoc(t, newWfFixture(t, "steps:\n  alpha:\n    merge: { skip: true }\n    grill: { models: [claude:declared] }\n"))
	merge := stepRead(t, doc, "alpha", "merge")
	if merge["skip"] != true {
		t.Fatalf("a skip-only entry must report skip true: %v", merge)
	}
	if merge["declared"] != false {
		t.Fatalf("a skip-only entry declares no models chain: %v", merge)
	}
	grill := stepRead(t, doc, "alpha", "grill")
	if v, ok := grill["skip"]; ok && v != nil {
		t.Fatalf("an entry without skip reports none: %v", grill)
	}
}
