package cli

// doctor_light_fallbacks_test.go: the light verification fallbacks row
// (U3; the scoped-gates row carried it until its check moved
// into the workflow).

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeVerifyRun installs one capsule verify run's result.json
// (<ws>/capsules/<capsule>/runtime-state/verify/<n>/result.json).
func writeVerifyRun(t *testing.T, ws, capsule, n, body string) {
	t.Helper()
	p := filepath.Join(ws, "capsules", capsule, "runtime-state", "verify", n, "result.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// The row counts the latest verify run of every live capsule whose light
// request ran full, and names the reasons by category: an earlier run, a
// quarantined capsule, a full request, a light run and an unparsable
// result.json are not counted; with none, the row says so. Always pass.
func TestLightFallbacksCountsTheLatestRunsThatRanFull(t *testing.T) {
	ws := t.TempDir()
	goList := "repos/app: go list failed: `GOWORK=off go list -e -test -f <record format> ./...` exited 1: go: updates to go.mod needed; falling back to the full tier"
	stackConf := "repos/app: --light: changed path go.mod is the go analyzer's own config file, which it cannot scope; falling back to the full tier"
	run := func(requested, effective string, reasons ...string) string {
		raw := `{"mechanical_verdict":"pass","tier":{"requested":"` + requested + `","effective":"` + effective + `"`
		if len(reasons) > 0 {
			raw += `,"reasons":["` + strings.Join(reasons, `","`) + `"]`
		}
		return raw + "}}\n"
	}
	// a: run 9 is earlier than run 10 (numeric, not lexical order).
	writeVerifyRun(t, ws, "a", "9", run("light", "full", "repos/app: carries: changed path lib/x.js is covered by no carries: declaration; falling back to the full tier"))
	writeVerifyRun(t, ws, "a", "10", run("light", "full", goList, stackConf))
	writeVerifyRun(t, ws, "b", "1", run("light", "light"))
	writeVerifyRun(t, ws, "c.quarantined.x", "1", run("light", "full", "repos/app: conf rejects an empty suite_glob; falling back to the full tier: suite_glob="))
	writeVerifyRun(t, ws, "d", "1", run("full", "full", "repos/app: not a git work tree; falling back to the full tier"))
	writeVerifyRun(t, ws, "e", "1", "{not json\n")

	status, detail := lightFallbacksCheck(&doctorRun{present: []string{ws}})
	if want := "the latest light verification of 1 capsule(s) ran full — reasons: go package graph ×1, stack config file changed ×1"; status != "pass" || detail != want {
		t.Fatalf("row %s %q, want pass %q", status, detail, want)
	}
	// No light run ran full: the row says so.
	writeVerifyRun(t, ws, "a", "11", run("light", "light"))
	if status, detail = lightFallbacksCheck(&doctorRun{present: []string{ws}}); status != "pass" || strings.Contains(detail, "ran full —") {
		t.Fatalf("a note without any fallback: %s %q", status, detail)
	}
	if status, _ = lightFallbacksCheck(&doctorRun{}); status != "skip" {
		t.Fatalf("no workspace: %s", status)
	}
	registered := false
	for _, c := range doctorChecks {
		registered = registered || c.name == docChLightFallbacks
	}
	if !registered {
		t.Fatal("the row is not in the doctor's ladder")
	}
}

// Every fallback reason the engine and the verify run write maps to its
// category (texts copied from the engine's diag lines, workflows/_shared/scripts/verify-scope, and verifyrun).
func TestLightFallbackKinds(t *testing.T) {
	for _, c := range []struct{ reason, kind string }{
		{"repos/app: go list failed: `GOWORK=off go list -e -test -f <record format> ./...` exited 1: go: updates to go.mod needed; falling back to the full tier", "go package graph"},
		{"repos/app: go list failed: `GOWORK=off go list -e -test -f <record format> ./...` reports a package error: pattern all:dist: contains no embeddable files; falling back to the full tier", "go package graph"},
		{"repos/app: unexpected go list -test output shape; falling back to the full tier", "go package graph"},
		{"repos/app: --light: changed path go.mod is the go analyzer's own config file, which it cannot scope; falling back to the full tier", "stack config file changed"},
		{"repos/app: --light: changed path package.json is the npm analyzer's own config file, which it cannot scope; falling back to the full tier", "stack config file changed"},
		{"repos/app: carries: suite e2e/flow.mjs declares no carries: header in its opening comment block; falling back to the full tier", "carries"},
		{"repos/app: carries: changed path lib/x.js is covered by no carries: declaration; falling back to the full tier", "carries"},
		{"repos/app: carries: e2e_map names e2e/gone.mjs, which is not a discovered suite; falling back to the full tier", "carries"},
		{"repos/app: conf rejects an empty suite_glob; falling back to the full tier: suite_glob=", "conf"},
		{"repos/app: conf rejects a second e2e_runner; falling back to the full tier: e2e_runner=npm test", "conf"},
		{"repos/app: no supported stack manifest (go.mod, package.json, pyproject.toml, requirements.txt or Cargo.toml); falling back to the full tier with the conf's commands only", "no analyzer"},
		{"repos/app: several stack manifests (go.mod package.json) are present without a stack= pin; falling back to the full tier with no commands (the conf declares none)", "no analyzer"},
		{"repos/app: cannot read package.json scripts; falling back to the full tier with the conf's commands only", "no analyzer"},
		{"repos/app: python: no test imports the changed module app/x.py (directly or through another module); its affected tests cannot be resolved, falling back to the full tier", "analyzer could not resolve"},
		{"repos/app: rust: the crate root src/lib.rs changed; its affected tests cannot be narrowed, falling back to the full tier", "analyzer could not resolve"},
		{"repos/app: not a git work tree; falling back to the full tier", "git"},
		{"repos/app: cannot resolve the engine root's place in the git work tree; falling back to the full tier", "git"},
		{"repos/app: cannot resolve the base (merge-base HEAD origin/main); falling back to the full tier", "git"},
		{"repos/app: cannot diff abc123..HEAD; falling back to the full tier", "git"},
		{"repos/app: changed path ../x is outside the engine root (sub of the git work tree); falling back to the full tier", "git"},
		{"repos/app: engine-failed: the engine exited 2: boom", "unplanned"},
		{"repos/app: empty: the engine planned no command (stack \"\", conf none)", "unplanned"},
		{"no touched repo has a plan", "unplanned"},
		{"repos/app: the engine answered mode full with no fallback diagnostic (a forced-full path or a full-only declaration)", "unexplained"},
		{"repos/app: something the engine says one day", "other"},
		{"", "other"},
	} {
		if got := lightFallbackKind(c.reason); got != c.kind {
			t.Errorf("lightFallbackKind(%q) = %q, want %q", c.reason, got, c.kind)
		}
	}
}
