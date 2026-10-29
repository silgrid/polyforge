package driver

// scope_test.go: unit coverage for the verification-tier vocabulary and
// its pure judgments. The e2e scenario suite
// (test/w19_verification_scope_acceptance_test.go) is the acceptance bar;
// these tests pin the parser/validator branches and the prompt render in
// isolation.

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/scopetier"
)

func TestScopeOptedInMarkerShapes(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    bool
	}{
		{"exact", "verification: scoped", true},
		{"surrounded by prose", "goal line\nverification: scoped\nrest", true},
		{"leading and trailing whitespace", "  \tverification: scoped \t ", true},
		{"crlf line ending", "a\r\nverification: scoped\r\nb", true},
		{"absent", "no marker here\nverification: full", false},
		{"wrong case", "Verification: Scoped", false},
		{"substring in prose", "run `verification: scoped` maybe", false},
		{"trailing colon prose", "note: verification: scoped is the marker", false},
		{"empty", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scopetier.OptedIn(tc.content); got != tc.want {
				t.Fatalf("scopetier.OptedIn(%q) = %v, want %v", tc.content, got, tc.want)
			}
		})
	}
}

func TestEffectiveTier(t *testing.T) {
	cases := []struct {
		optIn bool
		lane  string
		want  string
	}{
		{true, "feature-full", tierScoped},
		{true, "bug-fix", tierScoped},
		{true, "chore", tierScoped},
		{true, "autopilot", tierFull},
		{true, "release", tierFull},
		{true, "unknown", tierFull},
		{false, "feature-full", tierFull},
		{false, "autopilot", tierFull},
	}
	for _, tc := range cases {
		if got := effectiveTier(tc.optIn, tc.lane); got != tc.want {
			t.Errorf("effectiveTier(%v, %q) = %q, want %q", tc.optIn, tc.lane, got, tc.want)
		}
	}
}

func TestCheckVerificationScope(t *testing.T) {
	complete := map[string]any{
		"mode":           "scoped",
		"declared_paths": []any{"internal/a.go", "docs/b.md"},
		"rationale":      "narrow change",
		"fast_gates_ref": "CONTRIBUTING.md#scoped",
	}
	withPrior := map[string]any{
		"mode":           "scoped",
		"declared_paths": []any{"internal/result.go"},
		"rationale":      "increment",
		"fast_gates_ref": "CONTRIBUTING.md#scoped",
		"prior":          map[string]any{"tier": "scoped", "declared_paths": []any{"internal/scope.go"}},
	}
	cases := []struct {
		name    string
		scope   any
		tier    string
		wantErr string
	}{
		{"absent is full", nil, tierFull, ""},
		{"complete on scoped tier", complete, tierScoped, ""},
		{"complete with prior", withPrior, tierScoped, ""},
		{"claim without opt-in", complete, tierFull, "not opted in"},
		{"claim with no recorded tier", complete, "", "not opted in"},
		{"missing rationale", map[string]any{
			"mode": "scoped", "declared_paths": []any{"a"}, "fast_gates_ref": "x",
		}, tierScoped, "rationale"},
		{"missing declared paths", map[string]any{
			"mode": "scoped", "declared_paths": []any{}, "rationale": "r", "fast_gates_ref": "x",
		}, tierScoped, "declared_paths"},
		{"blank declared path", map[string]any{
			"mode": "scoped", "declared_paths": []any{"  "}, "rationale": "r", "fast_gates_ref": "x",
		}, tierScoped, "declared_paths"},
		{"wrong mode", map[string]any{
			"mode": "full", "declared_paths": []any{"a"}, "rationale": "r", "fast_gates_ref": "x",
		}, tierScoped, "mode"},
		{"non-object", "scoped", tierScoped, "must be a JSON object"},
		{"prior with wrong tier", map[string]any{
			"mode": "scoped", "declared_paths": []any{"a"}, "rationale": "r", "fast_gates_ref": "x",
			"prior": map[string]any{"tier": "full", "declared_paths": []any{"b"}},
		}, tierScoped, "prior.tier"},
		{"prior without paths", map[string]any{
			"mode": "scoped", "declared_paths": []any{"a"}, "rationale": "r", "fast_gates_ref": "x",
			"prior": map[string]any{"tier": "scoped", "declared_paths": []any{}},
		}, tierScoped, "prior.declared_paths"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := map[string]any{}
			if tc.scope != nil {
				out["scope"] = tc.scope
			}
			err := checkVerificationScope(out, tc.tier)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("checkVerificationScope = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("checkVerificationScope = %v, want error containing %q", err, tc.wantErr)
			}
		})
	}
}

func TestScopeCredentialUnion(t *testing.T) {
	got := scopeCredentialUnion([]string{"a", "b"}, []string{"b", "c"})
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("union = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("union = %v, want %v (order preserved, deduped)", got, want)
		}
	}
}

func TestRewindToOf(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want string
	}{
		{`{"verdict":"fail","rewind_to":"verification"}`, "verification"},
		{`{"verdict":"fail","rewind_to":"  verification  "}`, "verification"},
		{`{"verdict":"fail"}`, ""},
		{`{"rewind_to":3}`, ""},
		{``, ""},
	} {
		if got := rewindToOf(json.RawMessage(tc.out)); got != tc.want {
			t.Errorf("rewindToOf(%q) = %q, want %q", tc.out, got, tc.want)
		}
	}
}

// TestValidateStepResultVerificationScope pins the accept/reject branches
// of the verification scope contract through the real validator.
func TestValidateStepResultVerificationScope(t *testing.T) {
	complete := map[string]any{
		"verdict": "pass",
		"checks":  []any{map[string]any{"name": "build", "status": "pass"}},
		"scope": map[string]any{
			"mode": "scoped", "declared_paths": []any{"internal/a.go"},
			"rationale": "r", "fast_gates_ref": "CONTRIBUTING.md#scoped",
		},
	}
	raw := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	spec := func(tier string) ResultSpec {
		return ResultSpec{WIID: "wi_x", StepID: verificationStepID, SpawnN: 1, ScopeTier: tier}
	}
	if err := ValidateStepResult(&StepResult{WIID: "wi_x", StepID: verificationStepID, SpawnN: 1, Outcome: OutcomeCompleted, Output: raw(complete)}, spec(tierScoped)); err != nil {
		t.Fatalf("complete scoped declaration rejected: %v", err)
	}
	if err := ValidateStepResult(&StepResult{WIID: "wi_x", StepID: verificationStepID, SpawnN: 1, Outcome: OutcomeCompleted, Output: raw(complete)}, spec(tierFull)); err == nil {
		t.Fatalf("scoped claim on a full tier was accepted")
	}
	full := map[string]any{"verdict": "pass", "checks": []any{map[string]any{"name": "build", "status": "pass"}}}
	if err := ValidateStepResult(&StepResult{WIID: "wi_x", StepID: verificationStepID, SpawnN: 1, Outcome: OutcomeCompleted, Output: raw(full)}, spec(tierFull)); err != nil {
		t.Fatalf("full result rejected: %v", err)
	}
}

// TestReviewScopeRecord: reading the review step's scope
// declaration back — absent, complete, and the malformed shapes that
// answer present-but-unprovable (fail-closed at the merge rule).
func TestReviewScopeRecord(t *testing.T) {
	done := func(out map[string]any) *capsule.Step {
		return &capsule.Step{Status: capsule.StepDone, Output: out}
	}
	if rec, present := reviewScopeRecord(nil); present || rec != nil {
		t.Fatalf("no review step: rec=%+v present=%v", rec, present)
	}
	if rec, present := reviewScopeRecord(done(map[string]any{"verdict": "pass"})); present || rec != nil {
		t.Fatalf("an absent scope must answer not-present: rec=%+v present=%v", rec, present)
	}
	good := done(map[string]any{"scope": map[string]any{"mode": "scoped", "declared_paths": []any{"a.go", "b.go"}}})
	rec, present := reviewScopeRecord(good)
	if !present || rec == nil || rec.Mode != "scoped" || len(rec.DeclaredPaths) != 2 {
		t.Fatalf("a complete record must parse: rec=%+v present=%v", rec, present)
	}
	for name, out := range map[string]any{
		"non-object":      "scoped",
		"no mode":         map[string]any{"declared_paths": []any{"a.go"}},
		"no paths":        map[string]any{"mode": "scoped"},
		"empty paths":     map[string]any{"mode": "scoped", "declared_paths": []any{}},
		"non-string path": map[string]any{"mode": "scoped", "declared_paths": []any{1}},
		"non-list paths":  map[string]any{"mode": "scoped", "declared_paths": "a.go"},
	} {
		rec, present := reviewScopeRecord(done(map[string]any{"scope": out}))
		if !present || rec != nil {
			t.Fatalf("%s: a malformed record must answer present-but-unprovable, got rec=%+v present=%v", name, rec, present)
		}
	}
	if rec, present := reviewScopeRecord(&capsule.Step{Status: capsule.StepInProgress, Output: map[string]any{"scope": map[string]any{"mode": "scoped", "declared_paths": []any{"a.go"}}}}); present || rec != nil {
		t.Fatalf("a not-done review step records nothing: rec=%+v present=%v", rec, present)
	}
}

// TestScopeConsistent: the verification pass must have
// used exactly the scope review declared — plain equality, the union with
// an incremental prior included; order is free, duplicates and any wider
// or narrower set are not.
func TestScopeConsistent(t *testing.T) {
	ver := func(paths ...string) *scopeObject {
		return &scopeObject{Mode: "scoped", DeclaredPaths: paths}
	}
	rev := func(paths ...string) *reviewScope {
		return &reviewScope{Mode: "scoped", DeclaredPaths: paths}
	}
	cases := []struct {
		name string
		ver  *scopeObject
		rev  *reviewScope
		want bool
	}{
		{"equal", ver("a.go", "b.go"), rev("b.go", "a.go"), true},
		{"review wider", ver("a.go"), rev("a.go", "b.go"), false},
		{"review narrower", ver("a.go", "b.go"), rev("a.go"), false},
		{"disjoint", ver("a.go"), rev("b.go"), false},
		{"duplicates differ", ver("a.go", "a.go"), rev("a.go"), false},
		{"no verification scope", nil, rev("a.go"), false},
		{"no review scope", ver("a.go"), nil, false},
		{"prior union matches", &scopeObject{Mode: "scoped", DeclaredPaths: []string{"result.go"},
			Prior: &scopePrior{Tier: "scoped", DeclaredPaths: []string{"scope.go"}}}, rev("scope.go", "result.go"), true},
		{"prior union mismatch", &scopeObject{Mode: "scoped", DeclaredPaths: []string{"result.go"},
			Prior: &scopePrior{Tier: "scoped", DeclaredPaths: []string{"scope.go"}}}, rev("result.go"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scopeConsistent(tc.ver, tc.rev); got != tc.want {
				t.Fatalf("scopeConsistent = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestValidateStepResultReviewScopeAdequate pins the review obligation:
// a completed review of a work item that opted into the scoped tier must
// answer; a fail review (re-bound gate_failed) owes nothing. The trigger
// is the work item's opt-in review runs before
// verification, so it can never key on verification's own output.
func TestValidateStepResultReviewScopeAdequate(t *testing.T) {
	raw := func(v any) json.RawMessage { b, _ := json.Marshal(v); return b }
	spec := ResultSpec{WIID: "wi_x", StepID: reviewStepID, SpawnN: 1, ScopedWorkItem: true, Gate: true}
	res := func(out map[string]any) *StepResult {
		return &StepResult{WIID: "wi_x", StepID: reviewStepID, SpawnN: 1, Outcome: OutcomeCompleted, Output: raw(out)}
	}
	if err := ValidateStepResult(res(map[string]any{"verdict": "pass", scopeAdequateKey: true}), spec); err != nil {
		t.Fatalf("scoped review with scope_adequate rejected: %v", err)
	}
	if err := ValidateStepResult(res(map[string]any{"verdict": "pass"}), spec); err == nil {
		t.Fatalf("scoped review without scope_adequate was accepted")
	}
	// a fail verdict re-binds to gate_failed before the obligation is
	// judged: no scope_adequate is owed on the failing round.
	fail := map[string]any{"verdict": "fail", "fail_reason": "broken",
		"findings": []any{map[string]any{"severity": "blocking", "detail": "d"}}}
	if err := ValidateStepResult(res(fail), spec); err != nil {
		t.Fatalf("failing scoped review rejected: %v", err)
	}
}

// TestAssembleStepPromptRendersTier pins the verification-tier prompt
// section: scoped and full differ, and no other step renders one.
func TestAssembleStepPromptRendersTier(t *testing.T) {
	scoped := assembleStepPrompt(promptParts{WIID: "wi", StepID: verificationStepID, SpawnN: 1, ScopeTier: tierScoped, Definition: "body"})
	if !strings.Contains(scoped, "# Verification tier") || !strings.Contains(scoped, "SCOPED") {
		t.Fatalf("scoped verification prompt lacks the scoped tier section:\n%s", scoped)
	}
	full := assembleStepPrompt(promptParts{WIID: "wi", StepID: verificationStepID, SpawnN: 1, ScopeTier: tierFull, Definition: "body"})
	if !strings.Contains(full, "# Verification tier") || !strings.Contains(full, "FULL") {
		t.Fatalf("full verification prompt lacks the full tier section:\n%s", full)
	}
	other := assembleStepPrompt(promptParts{WIID: "wi", StepID: "code", SpawnN: 1, ScopeTier: tierScoped, Definition: "body"})
	if strings.Contains(other, "# Verification tier") {
		t.Fatalf("non-verification step rendered a tier section:\n%s", other)
	}
}

// TestW80AssembleRendersScopedRulesOnScopedOnly: the
// shared scoped-tier material rides the verification tier section of a
// SCOPED dispatch exactly once; a full dispatch and any other step never
// render it, and an absent material leaves the scoped one-liner alone.
func TestW80AssembleRendersScopedRulesOnScopedOnly(t *testing.T) {
	const rules = "W80 SCOPED RULES MARKER"
	scoped := assembleStepPrompt(promptParts{WIID: "wi", StepID: verificationStepID, SpawnN: 1, ScopeTier: tierScoped, ScopedRules: rules, Definition: "body"})
	if strings.Count(scoped, rules) != 1 {
		t.Fatalf("scoped verification prompt carries the scoped rules %d times, want 1:\n%s", strings.Count(scoped, rules), scoped)
	}
	if strings.Index(scoped, rules) < strings.Index(scoped, "# Verification tier") {
		t.Fatalf("scoped rules render outside the verification tier section:\n%s", scoped)
	}
	full := assembleStepPrompt(promptParts{WIID: "wi", StepID: verificationStepID, SpawnN: 1, ScopeTier: tierFull, ScopedRules: rules, Definition: "body"})
	if strings.Contains(full, rules) {
		t.Fatalf("full verification prompt rendered the scoped rules:\n%s", full)
	}
	other := assembleStepPrompt(promptParts{WIID: "wi", StepID: "code", SpawnN: 1, ScopeTier: tierScoped, ScopedRules: rules, Definition: "body"})
	if strings.Contains(other, rules) {
		t.Fatalf("non-verification step rendered the scoped rules:\n%s", other)
	}
	bare := assembleStepPrompt(promptParts{WIID: "wi", StepID: verificationStepID, SpawnN: 1, ScopeTier: tierScoped, Definition: "body"})
	if !strings.Contains(bare, "SCOPED") {
		t.Fatalf("scoped prompt without the material lost the tier one-liner:\n%s", bare)
	}
}
