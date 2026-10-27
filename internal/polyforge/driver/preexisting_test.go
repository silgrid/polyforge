package driver

// preexisting_test.go: the boundary of the pre-existing-failure lane
// (polyforge#164 S6, review B1). The tests that pin the change — the
// repro, the authored acceptance tests, the regression test — fail on
// the base commit by definition; classifying them pre_existing would
// pass an unfixed bug. Such a verification result is rejected in-process.

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// bug-fix: repro authored a test, fix names it as regression_test;
// verification tries to excuse the repro (and its test) as pre-existing.
func TestRunBugFixVerificationCannotExcuseTheRepro(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("repro"), spec("fix"), gateSpec("verification", "fix"))
	writeCapsuleFile(t, d.wi.CapsuleDir, "workflows/b/steps/verification.md", shippedStepDef(t, "bug-fix", "verification"))
	sp.script = []fakeEntry{
		done(`{"repro_path":"artifacts/repro.md","reproduces":true}`),
		done(`{"regression_test":"internal/x/nilmap_test.go"}`),
		verificationDone(`{"verdict":"pass","checks":[{"name":"build","status":"pass"},
			{"name":"repro","status":"pre_existing","detail":"fails on base abc1234 too"}]}`),
		verificationDone(`{"verdict":"pass","checks":[{"name":"unit","status":"pre_existing","detail":"nilmap_test.go fails on base abc1234"}],
			"pre_existing_failures":["internal/x/nilmap_test.go: TestNilMap"]}`),
		verificationDone(`{"verdict":"pass","checks":[{"name":"build","status":"pass"},{"name":"repro","status":"pass"}]}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := sp.dispatched(); len(got) != 5 {
		t.Fatalf("both excuses must be rejected in-process, got %v", got)
	}
	if fb := sp.spawnAt(3).PriorRejection; !strings.Contains(fb, `check "repro" is the repro`) {
		t.Fatalf("the repro excuse must be named: %q", fb)
	}
	if fb := sp.spawnAt(4).PriorRejection; !strings.Contains(fb, "internal/x/nilmap_test.go") || !strings.Contains(fb, "never pre_existing") {
		t.Fatalf("the regression-test excuse must be named: %q", fb)
	}
	if exec := readExec(t, execPath); exec.WorkflowStatus != StatusCompleted {
		t.Fatalf("the honest rewrite completes: %s", exec.WorkflowStatus)
	}
}

// feature-full: an authored acceptance test can never be pre-existing;
// a pre_existing check that names no base sha is rejected too.
func TestRunFeatureFullVerificationCannotExcuseAuthoredTests(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("e2e-author"), spec("code"), gateSpec("verification", "code"))
	writeCapsuleFile(t, d.wi.CapsuleDir, "workflows/b/steps/verification.md", shippedStepDef(t, "feature-full", "verification"))
	gitRepo(t, d.wi.CapsuleDir, "app", map[string]string{"test/accept_test.go": "accept\n"})
	sp.script = []fakeEntry{
		done(`{"scenarios_path":"artifacts/e2e-scenarios.md","scenario_titles":["A"],"test_files":["test/accept_test.go"]}`),
		done(`{}`),
		verificationDone(`{"verdict":"pass","checks":[{"name":"e2e","status":"pre_existing","detail":"fails on base abc1234"}],
			"pre_existing_failures":["test/accept_test.go: TestAccept"]}`),
		verificationDone(`{"verdict":"pass","checks":[{"name":"unit","status":"pre_existing","detail":"TestOld also fails on the base"}]}`),
		verificationDone(`{"verdict":"pass","checks":[{"name":"unit","status":"pre_existing","detail":"TestOld fails on base 0123abcd too"}],
			"pre_existing_failures":["internal/old/old_test.go: TestOld"]}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := sp.dispatched(); len(got) != 5 {
		t.Fatalf("two invalid classifications must be rejected, got %v", got)
	}
	if fb := sp.spawnAt(3).PriorRejection; !strings.Contains(fb, "test/accept_test.go") {
		t.Fatalf("the authored-test excuse must be named: %q", fb)
	}
	if fb := sp.spawnAt(4).PriorRejection; !strings.Contains(fb, "names no base commit sha") {
		t.Fatalf("a pre_existing check without the base sha must be rejected: %q", fb)
	}
	if exec := readExec(t, execPath); exec.WorkflowStatus != StatusCompleted {
		t.Fatalf("a genuine pre-existing failure with its base sha passes: %s", exec.WorkflowStatus)
	}
}

// bug-fix: once the repro reproduced the bug, a verification pass must
// show the repro re-run passing — a skipped (or missing) repro check
// under a pass is rejected.
func TestRunBugFixPassNeedsPassingRepro(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("repro"), spec("fix"), gateSpec("verification", "fix"))
	writeCapsuleFile(t, d.wi.CapsuleDir, "workflows/b/steps/verification.md", shippedStepDef(t, "bug-fix", "verification"))
	sp.script = []fakeEntry{
		done(`{"repro_path":"artifacts/repro.md","reproduces":true}`),
		done(`{"regression_test":"waived: no test surface"}`),
		verificationDone(`{"verdict":"pass","checks":[{"name":"build","status":"pass"},{"name":"repro","status":"skipped","detail":"slow"}]}`),
		verificationDone(`{"verdict":"pass","checks":[{"name":"build","status":"pass"},{"name":"original repro","status":"pass"}]}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := sp.dispatched(); len(got) != 4 {
		t.Fatalf("the skipped repro under a pass must be rejected once, got %v", got)
	}
	if fb := sp.spawnAt(3).PriorRejection; !strings.Contains(fb, `check named "repro" with status pass`) {
		t.Fatalf("feedback must ask for the passing repro check: %q", fb)
	}
	if exec := readExec(t, execPath); exec.WorkflowStatus != StatusCompleted {
		t.Fatalf("the pass with a passing repro completes: %s", exec.WorkflowStatus)
	}
}

// Matching is by path, never by substring: a protected file matches
// exactly, a protected directory as a component-boundary prefix; a
// "repro" check is "repro" as a word; regression_test entries reduce to
// their path token.
func TestPreExistingMatchingRules(t *testing.T) {
	rules := preExistingRulesOf(map[string]map[string]any{
		"e2e-author": {"test_files": []any{"test/a_test.go", "test/e2e/"}},
		"fix":        {"regression_test": "internal/x/nil_test.go (TestNilMap), ./internal/y/y_test.go:TestY"},
	})
	for _, p := range []string{"test/a_test.go", "test/e2e/login_test.go", "internal/x/nil_test.go", "internal/y/y_test.go"} {
		if protectedPath(p, rules.Protected) == "" {
			t.Errorf("%s must be protected (rules %v)", p, rules.Protected)
		}
	}
	for _, p := range []string{"test/a_test.go.bak", "xtest/a_test.go", "test/e2ex/a_test.go", "a_test.go", "internal/x/nil_test"} {
		if pr := protectedPath(p, rules.Protected); pr != "" {
			t.Errorf("%s must not match (matched %s)", p, pr)
		}
	}
	ok := `{"verdict":"pass","checks":[{"name":"TestReproducibleBuild","status":"pre_existing","detail":"fails on base 0123abcd; unrelated to test/a_test.go.bak"}],
		"pre_existing_failures":["internal/z/build_test.go: TestReproducibleBuild"]}`
	if err := checkPreExisting(json.RawMessage(ok), rules, true); err != nil {
		t.Fatalf("a genuine pre-existing failure must pass: %v", err)
	}
	for name, out := range map[string]string{
		"bare test name entry": `{"checks":[{"name":"unit","status":"pre_existing","detail":"base 0123abcd"}],"pre_existing_failures":["TestOld"]}`,
		"no entry at all":      `{"checks":[{"name":"unit","status":"pre_existing","detail":"base 0123abcd"}]}`,
		"protected dir entry":  `{"checks":[{"name":"e2e","status":"pre_existing","detail":"base 0123abcd"}],"pre_existing_failures":["test/e2e/login_test.go: TestLogin"]}`,
		"repro word":           `{"checks":[{"name":"repro-run","status":"pre_existing","detail":"base 0123abcd"}],"pre_existing_failures":["internal/z/z_test.go: TestZ"]}`,
	} {
		if err := checkPreExisting(json.RawMessage(out), rules, true); err == nil {
			t.Errorf("%s: must be rejected", name)
		}
	}
}
