// preexisting.go: the boundary of verification's pre-existing-failure
// lane (polyforge#164 S6).
//
// A test that fails the same way on the base commit is reported as
// status pre_existing and does not fail the gate. That lane must never
// excuse the tests that pin THIS change: the repro of the bug being
// fixed fails on the base by definition (an unfixed bug would pass as
// "pre-existing"), and so do the authored acceptance tests and the
// regression test. The driver therefore rejects — through the ordinary
// envelope-rejection retry lane — a completed result that classifies a
// repro check as pre_existing, a pre_existing_failures entry naming a
// protected test, a pre_existing check that names no base commit or is
// not backed by a well-formed `<repo-relative path>: <test>`
// pre_existing_failures entry, and (bug-fix)
// a verification pass that does not show the reproduced bug's repro
// check passing.
package driver

import (
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"strings"
)

// statusPreExisting is the verification check status of a failure that
// also fails on the base commit.
const statusPreExisting = "pre_existing"

// baseSHAShape finds a commit sha (abbreviated or full) in a detail.
var baseSHAShape = regexp.MustCompile(`\b[0-9a-f]{7,40}\b`)

// reproName matches a check named for the repro: "repro" as a word
// (repro, repro-run, "original repro"), never TestReproducibleBuild.
var reproName = regexp.MustCompile(`(?i)\brepro\b`)

// reproCheck is the exact check name the bug-fix lane records its repro
// re-run under; only a passing check of this name shows the repro passed.
const reproCheck = "repro"

// preExistingEntry is the documented pre_existing_failures entry form:
// `<repo-relative test file>: <test name>`.
var preExistingEntry = regexp.MustCompile(`^(\S+): (\S.*)$`)

// PreExistingRules is what the pre-existing boundary judges against,
// folded from the done steps' outputs.
type PreExistingRules struct {
	// Protected are repo-relative test paths (files, or directories
	// matched as exact path prefixes) that pin this change.
	Protected []string
	// ReproReproduced: bug-fix's repro reported reproduces: true, so a
	// passing verification must show a passing repro check.
	ReproReproduced bool
}

// cleanRel normalizes one repo-relative path ("" when it is not one).
func cleanRel(p string) string {
	p = strings.TrimSpace(p)
	if p == "" {
		return ""
	}
	c := path.Clean(p)
	if path.IsAbs(c) || c == "." || c == ".." || strings.HasPrefix(c, "../") {
		return ""
	}
	return c
}

// regressionPaths parses fix's regression_test: comma-separated entries,
// each reduced to its path token (a trailing "(TestFoo)" or a test name
// after whitespace is dropped); a `waived: ...` value names none.
func regressionPaths(rt string) []string {
	if strings.HasPrefix(strings.ToLower(strings.TrimSpace(rt)), "waived") {
		return nil
	}
	var out []string
	for _, item := range strings.Split(rt, ",") {
		fields := strings.Fields(item)
		if len(fields) == 0 {
			continue
		}
		tok := fields[0]
		if i := strings.IndexAny(tok, "(:"); i >= 0 {
			tok = tok[:i]
		}
		if c := cleanRel(tok); c != "" {
			out = append(out, c)
		}
	}
	return out
}

// preExistingRulesOf folds the rules from the done steps' outputs: every
// declared test_files path (e2e-author, repro), the regression_test
// paths (bug-fix fix) and whether the repro reproduced the bug.
func preExistingRulesOf(outputs map[string]map[string]any) PreExistingRules {
	var r PreExistingRules
	seen := map[string]bool{}
	add := func(p string) {
		if c := cleanRel(p); c != "" && !seen[c] {
			seen[c] = true
			r.Protected = append(r.Protected, c)
		}
	}
	for id, o := range outputs {
		if list, ok := o["test_files"].([]any); ok {
			for _, v := range list {
				if s, ok := v.(string); ok {
					add(s)
				}
			}
		}
		if rt, ok := o["regression_test"].(string); ok {
			for _, p := range regressionPaths(rt) {
				add(p)
			}
		}
		if id == "repro" && o["reproduces"] == true {
			r.ReproReproduced = true
		}
	}
	return r
}

// protectedPath answers the protected entry p matches: the exact path,
// or a protected directory on a path-component boundary ("" = none).
func protectedPath(p string, protected []string) string {
	c := cleanRel(p)
	if c == "" {
		return ""
	}
	for _, pr := range protected {
		if c == pr || strings.HasPrefix(c, pr+"/") {
			return pr
		}
	}
	return ""
}

// checkPreExisting judges one completed output against the boundary.
// verification marks the step whose pass must show the repro (bug-fix).
func checkPreExisting(output json.RawMessage, rules PreExistingRules, verification bool) error {
	if len(output) == 0 {
		return nil
	}
	var probe struct {
		Verdict any `json:"verdict"`
		Checks  []struct {
			Name   string `json:"name"`
			Status string `json:"status"`
			Detail string `json:"detail"`
		} `json:"checks"`
		PreExisting []any `json:"pre_existing_failures"`
	}
	if json.Unmarshal(output, &probe) != nil {
		return nil
	}
	var problems []string
	preChecks := 0
	reproPassed := false
	for _, c := range probe.Checks {
		// the pass rule wants THE repro check, exactly: a check that merely
		// mentions the word (`app:repro-tools`, "original repro") is not it
		if strings.TrimSpace(c.Name) == reproCheck && c.Status == "pass" {
			reproPassed = true
		}
		if c.Status != statusPreExisting {
			continue
		}
		preChecks++
		switch {
		case reproName.MatchString(c.Name):
			problems = append(problems, fmt.Sprintf("check %q is the repro of the bug being fixed — it fails on the base by definition, so it can never be pre_existing", c.Name))
		case !baseSHAShape.MatchString(c.Detail):
			problems = append(problems, fmt.Sprintf("check %q is pre_existing but its detail names no base commit sha", c.Name))
		}
	}
	entries := 0
	for _, v := range probe.PreExisting {
		s, _ := v.(string)
		m := preExistingEntry.FindStringSubmatch(strings.TrimSpace(s))
		if m == nil || cleanRel(m[1]) == "" {
			problems = append(problems, fmt.Sprintf("pre_existing_failures entry %q is not of the form `<repo-relative test file>: <test name>`", s))
			continue
		}
		entries++
		if pr := protectedPath(m[1], rules.Protected); pr != "" {
			problems = append(problems, fmt.Sprintf("pre_existing_failures entry %q is the protected test %s", s, pr))
		}
	}
	if preChecks > 0 && entries == 0 {
		problems = append(problems, "a pre_existing check must be backed by at least one pre_existing_failures entry `<repo-relative test file>: <test name>`")
	}
	if verdict, _ := probe.Verdict.(string); verification && rules.ReproReproduced && strings.EqualFold(strings.TrimSpace(verdict), "pass") && !reproPassed {
		problems = append(problems, "the repro reproduced the bug, so a pass needs the repro re-run recorded as a check named \"repro\" with status pass (not skipped, not missing)")
	}
	if len(problems) == 0 {
		return nil
	}
	return fmt.Errorf("an invalid pre_existing classification: %s — the repro, the authored acceptance tests (test_files) and the regression test pin this change and fail on the base by design; report them as pass or fail, never pre_existing",
		strings.Join(problems, "; "))
}
