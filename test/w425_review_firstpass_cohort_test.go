package test

// carries: tools/review-firstpass, test/w425_review_firstpass_test.go, test/w425_review_firstpass_cohort_test.go

// w425_review_firstpass_cohort_test.go (polyforge#118, AC2): the metrics
// tool's --cohort view — an owner-confirmed cohort manifest groups the
// runs (unsplit control / split children / the epic aggregation listed
// apart), echoes the pinned #1 background baseline, keeps the plan-size
// table per group (a reviewed run with no parseable plan counts
// `unknown`), and reports honestly: a capsule named by the manifest but
// absent from the input is counted and listed, a window only filters,
// and an empty split group prints N/A — never a fabricated comparison.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// w425CohortCap is one fixture capsule of the cohort tests.
type w425CohortCap struct {
	name, lane, created string
	plan                string            // artifacts/plan.md; "" = none
	results             map[string]string // "<step>/s<n>" → result.json
	handoff             string            // artifacts/handoff.md; "" = none
}

// w425CohortWrite lays one fixture capsule into caps.
func w425CohortWrite(t *testing.T, caps string, c w425CohortCap) {
	t.Helper()
	dir := filepath.Join(caps, c.name)
	write := func(rel, body string) {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("capsule.json", fmt.Sprintf(`{"bundle_name":%q,"created_at":"%sT10:00:00Z"}`, c.lane, c.created))
	if c.plan != "" {
		write("artifacts/plan.md", c.plan)
	}
	for k, v := range c.results {
		step, run, _ := strings.Cut(k, "/")
		write("artifacts/steps/"+step+"/"+run+"/result.json", v)
	}
	if c.handoff != "" {
		write("artifacts/handoff.md", c.handoff)
	}
}

// TestW425ReviewFirstpassCohort pins the cohort view: grouped first-pass
// by plan size, the baseline echo, missing and filtered capsules counted
// and listed, and the honest N/A of a split group with no samples. Every
// expected value below is hand-computed from the fixture rows.
func TestW425ReviewFirstpassCohort(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("python3 is a pf doctor prerequisite and runs this tool: %v", err)
	}
	caps := t.TempDir()
	for _, c := range []w425CohortCap{
		// control: one PASS at <=7, one FAIL at 8-22
		{"ctl-1", "develop", "2026-11-02", w425Plan(map[string]int{"u1": 2, "u2": 1}),
			map[string]string{"build/s1": w425Build("main", "main"), "review/s2": w425Review("pass")}, ""},
		{"ctl-2", "develop", "2026-11-03", w425Plan(map[string]int{"u1": 5, "u2": 5}),
			map[string]string{"build/s1": w425Build("subagent", "subagent"), "review/s2": w425Review("fail")}, ""},
		// outside the manifest window: filtered, not missing
		{"ctl-3", "develop", "2026-10-01", w425Plan(map[string]int{"u1": 1}),
			map[string]string{"review/s1": w425Review("pass")}, ""},
		// split: two children (one <=7 PASS, one 8-22 PASS) and the epic
		// (reviewed, no plan → unknown)
		{"kid-1", "develop", "2026-11-04", w425Plan(map[string]int{"u1": 2, "u2": 1}),
			map[string]string{"build/s1": w425Build("main"), "review/s2": w425Review("pass")}, ""},
		{"kid-2", "develop", "2026-11-05", w425Plan(map[string]int{"u1": 6, "u2": 6}),
			map[string]string{"build/s1": w425Build("subagent"), "review/s2": w425Review("pass")}, ""},
		{"ep-1", "epic", "2026-11-06", "",
			map[string]string{"review/s1": w425Review("pass")}, ""},
	} {
		w425CohortWrite(t, caps, c)
	}
	manifest := filepath.Join(t.TempDir(), "cohort.json")
	man := `{
  "version": "v0.2.2",
  "window": {"since": "2026-11-01", "until": "2026-11-30"},
  "baseline": {"firstpass": {"<=7": "31/38", "8-22": "13/31", ">22": "4/34"}, "source": "the #1 background (rework baseline, 2026-11-09)"},
  "control": ["ctl-1", "ctl-2", "ctl-3", "gone-9"],
  "split": [{"origin": "wi_o", "epic": "ep-1", "children": ["kid-1", "kid-2"]}]
}`
	if err := os.WriteFile(manifest, []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	run := func(manifestPath string) string {
		t.Helper()
		args := []string{"-I", filepath.Join(v1Root(t), "tools", "review-firstpass"), caps, "--since", "2026-10-30"}
		if manifestPath != "" {
			args = append(args, "--cohort", manifestPath)
		}
		out, err := exec.Command("python3", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("review-firstpass --cohort: %v\n%s", err, out)
		}
		return string(out)
	}
	text := run(manifest)
	for _, want := range []string{
		"== cohort comparison (manifest: cohort.json, version: v0.2.2, window: 2026-11-01..2026-11-30) ==",
		"baseline (pinned #1 background, source: the #1 background (rework baseline, 2026-11-09)): first-pass <=7 31/38, 8-22 13/31, >22 4/34",
		// control: 2 in-window runs, both reviewed, 1 PASS (<=7), 1 FAIL (8-22)
		"control (unsplit runs): 2 runs (2 reviewed), first-pass 1/2 (50%), plan sizes <=7 1/1, 8-22 0/1, >22 0/0, unknown 0",
		// split children: both PASS; kid-1 is <=7, kid-2 is 8-22
		"split children: 2 runs (2 reviewed), first-pass 2/2 (100%), plan sizes <=7 1/1, 8-22 1/1, >22 0/0, unknown 0",
		// the epic is listed apart, never in the children's denominators
		"split epics (listed apart): 1 runs (1 reviewed), first-pass 1/1 (100%), plan sizes <=7 0/0, 8-22 0/0, >22 0/0, unknown 1",
		// the window filtered ctl-3 (it IS in the input); gone-9 is not
		"outside the manifest window (filtered): ctl-3 (1)",
		"missing from the input (counted, excluded): gone-9 (1)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the cohort report must carry %q:\n%s", want, text)
		}
	}

	// no split samples yet: the split group says so, never a fabricated
	// number (the owner's Q5 ruling).
	empty := filepath.Join(t.TempDir(), "cohort-empty.json")
	if err := os.WriteFile(empty, []byte(`{"version": "v0.2.2", "control": [], "split": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	text = run(empty)
	for _, want := range []string{
		"== cohort comparison (manifest: cohort-empty.json, version: v0.2.2, window: none) ==",
		"control (unsplit runs): 0 runs (0 reviewed), first-pass N/A (0 samples), plan sizes <=7 0/0, 8-22 0/0, >22 0/0, unknown 0",
		"split children: 0 runs (0 reviewed), first-pass N/A (0 samples - no epic run has completed yet), plan sizes <=7 0/0, 8-22 0/0, >22 0/0, unknown 0",
		"split epics (listed apart): 0 runs (0 reviewed), first-pass N/A (0 samples), plan sizes <=7 0/0, 8-22 0/0, >22 0/0, unknown 0",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the empty cohort report must carry %q:\n%s", want, text)
		}
	}

	// without --cohort the report is unchanged: no cohort section appears.
	if plain := run(""); strings.Contains(plain, "cohort comparison") {
		t.Errorf("without --cohort the report must not grow a cohort section:\n%s", plain)
	}
}
