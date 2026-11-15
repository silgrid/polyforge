package test

// carries: workflows/_shared/test_quality.md, workflows/_shared/steps/review.md, workflows/develop/steps/build.md, workflows/micro/steps/fix.md, workflows/develop/README.md, workflows/micro/README.md, workflows/develop/fragments/review-judge.md, docs/concepts.md, docs/contributing.md, docs/design/polyforge-vnext/reference.md, test/test_quality_fragment_contract_test.go

// test_quality_fragment_contract_test.go: text contract for the shared
// test-quality bar (polyforge#28; adapted from spec-superflow's
// skills/build-executor/writing-good-tests.md, MIT, Copyright (c) 2026
// MageByte). What counts as a test — the observable behaviour it
// exercises, expected values derived independently of the implementation,
// the reasonable production-code change it would catch; a test that only
// asserts source text exists is not a behaviour test; a first red says
// why — lives once in workflows/_shared/test_quality.md, and the steps
// that write or judge tests include it: develop's build, micro's fix and
// the shared review step. The placeholder render inserts a fragment
// verbatim and never re-scans it, so the include lives in the step
// definition files themselves (never in a vars_files fragment or a skill
// body), and each including step renders the fragment's operative lines
// exactly once.

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/workflow"
)

// testQualityLines are the fragment's operative lines (compared
// whitespace-flattened, so a re-wrapped paragraph keeps matching); each
// must render exactly once per including step.
var testQualityLines = []string{
	"## What counts as a test",
	"Adapted from spec-superflow's `skills/build-executor/writing-good-tests.md`",
	"MIT — Copyright (c) 2026 MageByte",
	"A test that counts pins an observable behaviour, and its author can say three things about it:",
	"**The behaviour it observes.**",
	"**Expected values derived independently.**",
	"**The change it catches.**",
	"**Text existence is not behaviour.**",
	"unless the acceptance is itself that text or document, where the text is the behaviour",
	"**A first red says why.**",
	"a red from any other cause — a missing symbol, a typo, a compile error — is not the red that was meant",
	"Every unit of the plan carries the three statements above as its test-quality record, in its `## Units` entry",
}

var testQualityPlaceholder = regexp.MustCompile(`\{\{[^}]*\}\}`)

// testQualityFlat collapses every whitespace run to one space, so a
// re-wrapped paragraph keeps matching.
func testQualityFlat(s string) string { return strings.Join(strings.Fields(s), " ") }

// testQualityFragment reads the shared fragment itself.
func testQualityFragment(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(v1Root(t), "workflows", "_shared", "test_quality.md"))
	if err != nil {
		t.Fatalf("the test_quality fragment: %v", err)
	}
	return string(raw)
}

// testQualityOnce asserts one rendered definition carries every
// operative line exactly once and no unresolved placeholder.
func testQualityOnce(t *testing.T, rel, def string) {
	t.Helper()
	if m := testQualityPlaceholder.FindString(def); m != "" {
		t.Errorf("%s renders with an unresolved placeholder %s", rel, m)
	}
	flat := testQualityFlat(def)
	for _, line := range testQualityLines {
		if n := strings.Count(flat, line); n != 1 {
			t.Errorf("%s renders the test-quality line %q %d times, want exactly 1", rel, line, n)
		}
	}
}

func TestTestQualityFragmentReachesItsSteps(t *testing.T) {
	frag := strings.TrimSpace(testQualityFragment(t))
	flat := testQualityFlat(frag)
	for _, line := range testQualityLines {
		if !strings.Contains(flat, line) {
			t.Errorf("the test_quality fragment lacks its operative line %q", line)
		}
	}
	// the authoring steps include the fragment once and render it once
	// (they declare no vars, so the raw render is the shipped one)
	for _, bs := range [][2]string{{"develop", "build"}, {"micro", "fix"}} {
		bundle, step := bs[0], bs[1]
		rel := "workflows/" + bundle + "/steps/" + step + ".md"
		raw := w149CarrierRead(t, rel)
		if n := strings.Count(raw, "{{test_quality}}"); n != 1 {
			t.Errorf("%s includes {{test_quality}} %d times, want exactly 1", rel, n)
		}
		if strings.Contains(raw, frag) {
			t.Errorf("%s carries a second copy of the test_quality fragment", rel)
		}
		out, err := workflow.RenderDefinition(filepath.Join(v1Root(t), "workflows", bundle), step, nil)
		if err != nil {
			t.Fatalf("render %s: %v", rel, err)
		}
		testQualityOnce(t, rel, string(out))
	}
	// the shared review step renders with the lane's own vars (its
	// intro/judge fragments arrive as vars_files values)
	lane, errs := workflow.Load(filepath.Join(v1Root(t), "workflows", "develop"))
	if len(errs) > 0 || lane == nil {
		t.Fatalf("the develop lane does not load: %v", errs)
	}
	s := lane.Step("review")
	if s == nil {
		t.Fatal("the develop lane declares no review step")
	}
	rel := "workflows/_shared/steps/review.md"
	if n := strings.Count(w149CarrierRead(t, rel), "{{test_quality}}"); n != 1 {
		t.Errorf("%s includes {{test_quality}} %d times, want exactly 1", rel, n)
	}
	out, err := workflow.RenderDefinition(filepath.Join(v1Root(t), "workflows", "develop"), "review", s.Vars)
	if err != nil {
		t.Fatalf("render %s: %v", rel, err)
	}
	testQualityOnce(t, rel, string(out))
}

func TestBuildPlanUnitsCarryTheTestQualityRecord(t *testing.T) {
	// the plan spec: every ## Units entry carries the record the bar names
	build := w68Flat(t, "workflows/develop/steps/build.md")
	// polyforge#49 (AC2): the record's three items are the rule's three
	// (test_quality.md:10-18) — the behaviour, the expected values derived
	// independently of the implementation, the change it catches; why the
	// red is expected stays the bar's own first-red duty, not a recorded
	// fourth item.
	const record = "its test-quality record (the behaviour its test observes, how its expected values are derived independently of the implementation, the change it catches — the bar below)"
	if !strings.Contains(build, record) {
		t.Errorf("the build step's plan spec must require %q", record)
	}
	// the lane README describes the same record where it describes the plan
	readme := w68Flat(t, "workflows/develop/README.md")
	const readmeRecord = "each `## Units` entry recording the behaviour its test observes, how its expected values are derived independently of the implementation and the change it catches (`workflows/_shared/test_quality.md`)"
	if !strings.Contains(readme, readmeRecord) {
		t.Errorf("the develop README must state %q", readmeRecord)
	}
	// and the record's home is the fragment itself, not a second copy
	frag := testQualityFlat(testQualityFragment(t))
	const recordSentence = "Every unit of the plan carries the three statements above as its test-quality record, in its `## Units` entry: the behaviour its test observes, how its expected values are derived independently of the implementation, the change it catches"
	if !strings.Contains(frag, recordSentence) {
		t.Error("the test_quality fragment no longer names the per-unit record duty with the rule's own three items (the behaviour, the independently derived expected values, the change it catches)")
	}
}

func TestReviewBlocksOnTestQualityViolations(t *testing.T) {
	// the shared review's test_weakened class names the missing or
	// contradicted record (polyforge#28: review reads the same bar)
	review := w68Flat(t, "workflows/_shared/steps/review.md")
	if !strings.Contains(review, "a unit that misses or contradicts its test-quality record") {
		t.Error("the shared review's test_weakened class must name the missing-or-contradicted test-quality record")
	}
	// the develop judgment carries the three blocking conditions against
	// the same fragment: no record, text existence only, a red that is
	// not the recorded one
	judge := w68Flat(t, "workflows/develop/fragments/review-judge.md")
	for _, want := range []string{
		"The shared test-quality bar (`workflows/_shared/test_quality.md`, included in this step's definition)",
		"blocking `test_weakened` too — a unit whose plan entry carries no test-quality record",
		"a test that only asserts that some source text exists (unless the acceptance is itself that text or document)",
		"a unit whose replayed red fails for another reason than its record states",
	} {
		if !strings.Contains(judge, want) {
			t.Errorf("the develop review's judgment must state %q", want)
		}
	}
	// the documents that describe review's blocking classes say it too
	for rel, want := range map[string]string{
		"docs/concepts.md":            "and so are a unit without its test-quality record",
		"docs/contributing.md":        "a unit without its test-quality record or whose replayed red contradicts the recorded reason",
		"workflows/develop/README.md": "A unit that misses its test-quality record",
	} {
		if !strings.Contains(w68Flat(t, rel), want) {
			t.Errorf("%s must state %q", rel, want)
		}
	}
}

// asyncSettleLines are the operative lines of the bar's settle duty
// (polyforge#33): a state that settles asynchronously is asserted only
// after a bounded poll reaches the expected state; one read right after
// the action is a race and test_weakened; the fix is never a relaxed
// assertion or an "at least". Each lives in the fragment and renders
// exactly once in every step that includes it — a second copy written
// into a step definition or a review fragment counts twice.
var asyncSettleLines = []string{
	"Three duties follow, for every test a step writes:",
	"**Await the settled state before asserting it.**",
	"A state that settles asynchronously — the effect of a UI action, a streamed update, a background task — is asserted only after a bounded poll has reached the expected state the test names.",
	"Reading it once right after the action and asserting on that read is a race with the settling, and is `test_weakened`.",
	"Such a test is fixed by polling, never by relaxing the assertion or weakening an exact expectation to \"at least\".",
}

func TestAsyncSettleDutyReachesItsSteps(t *testing.T) {
	flat := testQualityFlat(testQualityFragment(t))
	for _, line := range asyncSettleLines {
		if !strings.Contains(flat, line) {
			t.Errorf("the test_quality fragment lacks the settle duty's line %q", line)
		}
	}
	renders := map[string]string{}
	for _, bs := range [][2]string{{"develop", "build"}, {"micro", "fix"}} {
		out, err := workflow.RenderDefinition(filepath.Join(v1Root(t), "workflows", bs[0]), bs[1], nil)
		if err != nil {
			t.Fatalf("render %s/%s: %v", bs[0], bs[1], err)
		}
		renders["workflows/"+bs[0]+"/steps/"+bs[1]+".md"] = string(out)
	}
	lane, errs := workflow.Load(filepath.Join(v1Root(t), "workflows", "develop"))
	if len(errs) > 0 || lane == nil {
		t.Fatalf("the develop lane does not load: %v", errs)
	}
	s := lane.Step("review")
	if s == nil {
		t.Fatal("the develop lane declares no review step")
	}
	out, err := workflow.RenderDefinition(filepath.Join(v1Root(t), "workflows", "develop"), "review", s.Vars)
	if err != nil {
		t.Fatalf("render review: %v", err)
	}
	renders["workflows/_shared/steps/review.md"] = string(out)
	for rel, def := range renders {
		f := testQualityFlat(def)
		for _, line := range asyncSettleLines {
			if n := strings.Count(f, line); n != 1 {
				t.Errorf("%s renders the settle duty's line %q %d times, want exactly 1", rel, line, n)
			}
		}
	}
}
