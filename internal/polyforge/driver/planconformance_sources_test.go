package driver

// planconformance_sources_test.go: declared_extensions cites a review
// finding, a verification FAIL or a human deliver answer; a moot coupled
// obligation is withdrawn by a cited withdrawal or lapses when no write
// declaring it is in the diff; an uncited withdrawal withdraws nothing.

import (
	"reflect"
	"strings"
	"testing"
)

const sourcesPlan = `{"touched_files":[
	{"path":"a.go","intent":"write","coupled_obligations":[{"path":"docs/a.md"}]},
	{"path":"b.go","intent":"write","coupled_obligations":[{"path":"docs/b.md"}]}]}`

func TestVerificationAndDeliverCitationsCountAsDeclared(t *testing.T) {
	build := jsonMap(t, `{"declared_extensions":[
		{"path":"v.go","source":"verification","ref":"s7"},
		{"path":"h.go","source":"deliver","ref":"merge_or_back"},
		{"path":"bad.go","source":"verification","ref":"s7#1"}]}`)
	in := planConformanceInputs{plan: jsonMap(t, sourcesPlan), build: build,
		rewound: map[string]bool{verificationStepID: true, deliverStepID: true}}
	pc := foldPlanConformance(in, map[string][]string{"app": {"a.go", "b.go", "docs/a.md", "docs/b.md", "v.go", "h.go", "bad.go"}}, "")
	if want := []string{"bad.go"}; !reflect.DeepEqual(pc.Undeclared, want) {
		t.Fatalf("undeclared = %v, want %v (a verification or deliver citation declares its file)", pc.Undeclared, want)
	}
	s := renderPlanConformance(pc)
	for _, want := range []string{"`v.go` — cites the verification FAIL s7", "`h.go` — cites the human deliver answer to `merge_or_back`"} {
		if !strings.Contains(s, want) {
			t.Errorf("facts must render %q:\n%s", want, s)
		}
	}
	// without the source gate's rewind there is nothing to cite
	in.rewound = map[string]bool{reviewStepID: true}
	pc = foldPlanConformance(in, map[string][]string{"app": {"a.go", "b.go", "docs/a.md", "docs/b.md", "v.go"}}, "")
	if want := []string{"v.go"}; !reflect.DeepEqual(pc.Undeclared, want) {
		t.Fatalf("undeclared = %v, want %v", pc.Undeclared, want)
	}
}

func TestCitedWithdrawalClearsOnlyTheNamedObligation(t *testing.T) {
	build := jsonMap(t, `{"withdrawn_obligations":[{"path":"docs/a.md","source":"review","ref":"s4#2"}]}`)
	in := planConformanceInputs{plan: jsonMap(t, sourcesPlan), build: build, rewound: map[string]bool{reviewStepID: true}}
	pc := foldPlanConformance(in, map[string][]string{"app": {"a.go", "b.go"}}, "")
	if want := []string{"docs/b.md"}; !reflect.DeepEqual(pc.UnchangedObligations, want) {
		t.Fatalf("unchanged obligations = %v, want %v (only the cited one is withdrawn)", pc.UnchangedObligations, want)
	}
	if len(pc.Released) != 1 || !strings.Contains(pc.Released[0], "docs/a.md") || !strings.Contains(pc.Released[0], "review finding s4#2") {
		t.Fatalf("released = %v", pc.Released)
	}
}

func TestObligationLapsesWithoutItsParentWrite(t *testing.T) {
	in := planConformanceInputs{plan: jsonMap(t, sourcesPlan)}
	pc := foldPlanConformance(in, map[string][]string{"app": {"b.go"}}, "")
	if want := []string{"docs/b.md"}; !reflect.DeepEqual(pc.UnchangedObligations, want) {
		t.Fatalf("unchanged obligations = %v, want %v (a.go is not in the diff, so docs/a.md lapses)", pc.UnchangedObligations, want)
	}
	if len(pc.Released) != 1 || !strings.Contains(pc.Released[0], "`docs/a.md` — lapsed") {
		t.Fatalf("released = %v", pc.Released)
	}
}

func TestUncitedWithdrawalStillBlocks(t *testing.T) {
	build := jsonMap(t, `{"withdrawn_obligations":[{"path":"docs/a.md"},{"path":"docs/b.md","source":"review","ref":"s4#1"}]}`)
	in := planConformanceInputs{plan: jsonMap(t, sourcesPlan), build: build} // no review rewind: nothing to cite
	pc := foldPlanConformance(in, map[string][]string{"app": {"a.go", "b.go"}}, "")
	if want := []string{"docs/a.md", "docs/b.md"}; !reflect.DeepEqual(pc.UnchangedObligations, want) {
		t.Fatalf("unchanged obligations = %v, want %v (an uncited withdrawal withdraws nothing)", pc.UnchangedObligations, want)
	}
	if len(pc.Ignored) != 2 {
		t.Fatalf("both withdrawals are listed as not counted: %v", pc.Ignored)
	}
}
