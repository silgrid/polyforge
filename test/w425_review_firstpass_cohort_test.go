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
	"encoding/csv"
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
			map[string]string{"build/s1": w425Build("subagent", "subagent"), "review/s2": w425Review("pass")}, ""},
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

// w425ZhSection builds one zh-language hand-off section (the rows the
// metrics parser reads), the daemon's stamped shape.
func w425ZhSection(step string, round int, started, ended, dur, status string, events []string) string {
	b := fmt.Sprintf("---\n\n## %s · 第 %d 轮 · 派发 %d · %s\n\n", step, round, round, status)
	b += "| 项 | 值 |\n|---|---|\n"
	b += fmt.Sprintf("| 步骤 | %s |\n| 轮次 | %d |\n| 开始 | %s |\n| 结束 | %s |\n| 耗时 | %s |\n| 状态 | %s |\n", step, round, started, ended, dur, status)
	if len(events) > 0 {
		b += "\n**本轮事件**\n\n"
		for _, e := range events {
			b += "- " + e + "\n"
		}
	}
	b += "\n### 正文（step 撰写）\n\nx\n"
	return b
}

// w425EnSection is the EN-language twin.
func w425EnSection(step string, round int, started, ended, dur, status string, events []string) string {
	b := fmt.Sprintf("---\n\n## %s · Round %d · Dispatch %d · %s\n\n", step, round, round, status)
	b += "| Item | Value |\n|---|---|\n"
	b += fmt.Sprintf("| Step | %s |\n| Round | %d |\n| Started | %s |\n| Ended | %s |\n| Duration | %s |\n| Status | %s |\n", step, round, started, ended, dur, status)
	if len(events) > 0 {
		b += "\n**Events of this round**\n\n"
		for _, e := range events {
			b += "- " + e + "\n"
		}
	}
	b += "\n### Body (written by the step)\n\nx\n"
	return b
}

// TestW425ReviewFirstpassCohortTime pins the cohort time metrics (AC2,
// the owner's Q3/Q4 calibers): the rework share of proxy execution time
// (a rewind section's named steps' later rounds over all measured
// attempt time; counted and owner/uncounted rewinds split; infra-retry
// rounds listed apart), and the claim-to-merge duration (the merge
// step's end as the labeled proxy — the real merge moment is N/A
// offline), with the zero-denominator and not-merged honesty. Every
// expected value below is hand-computed from the fixture sections.
func TestW425ReviewFirstpassCohortTime(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("python3 is a pf doctor prerequisite and runs this tool: %v", err)
	}
	// t-1 (zh): attempts 600+120+480+60+300=1560s; after the counted
	// rewind naming build and review, build r2 (480s) and review r2
	// (60s) re-run → rework 540s (35%); build r2 carries an infra event
	// (rate_limited) → 1 infra-retry round (480s) listed apart; merged
	// at 10:45, claim 10:00 → proxy 45m.
	t1 := "# 交接文档\n\n"
	t1 += w425ZhSection("build", 1, "2026-11-02T10:01:00Z", "2026-11-02T10:11:00Z", "10m0s", "done", nil)
	t1 += w425ZhSection("review", 1, "2026-11-02T10:12:00Z", "2026-11-02T10:14:00Z", "2m0s", "fail", nil)
	t1 += "---\n\n## 事件 · 回退 · 2026-11-02T10:15:00Z\n\n- 触发：`review` 第 1 轮交接 `fail`\n- 原因：见该步该段正文\n- 要重做的步骤（按顺序）：`build` → `review`\n- 回退次数：1/3\n"
	t1 += w425ZhSection("build", 2, "2026-11-02T10:20:00Z", "2026-11-02T10:28:00Z", "8m0s", "done",
		[]string{"2026-11-02T10:21:00Z step_failed rate_limited (pi:example): 429"})
	t1 += w425ZhSection("review", 2, "2026-11-02T10:29:00Z", "2026-11-02T10:30:00Z", "1m0s", "done", nil)
	t1 += w425ZhSection("merge", 1, "2026-11-02T10:40:00Z", "2026-11-02T10:45:00Z", "5m0s", "done", nil)

	// t-3 (EN): attempts 60+30+30+30+20=170s; rewind #1 (counted) names
	// build and review → build r2, review r2; rewind #2 (owner, not
	// counted) names build → build r3; rework 30+30+20=80s (47%); never
	// merged.
	t3 := "# Hand-off document\n\n"
	t3 += w425EnSection("build", 1, "2026-11-04T10:01:00Z", "2026-11-04T10:02:00Z", "1m0s", "done", nil)
	t3 += w425EnSection("review", 1, "2026-11-04T10:03:00Z", "2026-11-04T10:03:30Z", "30s", "fail", nil)
	t3 += "---\n\n## Event · Rewind · 2026-11-04T10:04:00Z\n\n- Trigger: `review` Round 1 handed off `fail`\n- Reason: see that step's section body\n- Steps to redo (in order): `build` → `review`\n- Rewinds: 1/3\n"
	t3 += w425EnSection("build", 2, "2026-11-04T10:10:00Z", "2026-11-04T10:10:30Z", "30s", "done", nil)
	t3 += w425EnSection("review", 2, "2026-11-04T10:11:00Z", "2026-11-04T10:11:30Z", "30s", "done", nil)
	t3 += "---\n\n## Event · Rewind · 2026-11-04T10:12:00Z\n\n- Trigger: `review` Round 2 handed off `rewind → build`\n- Reason: see that step's section body\n- Steps to redo (in order): `build`\n- Rewinds: 1/3；本次依据 owner 的话，不计次数\n"
	t3 += w425EnSection("build", 3, "2026-11-04T10:20:00Z", "2026-11-04T10:20:20Z", "20s", "done", nil)

	caps := t.TempDir()
	w425CohortWrite(t, caps, w425CohortCap{"t-1", "develop", "2026-11-02", w425Plan(map[string]int{"u1": 2}),
		map[string]string{"build/s1": w425Build("main"), "review/s2": w425Review("pass")}, t1})
	// t-2: reviewed, no hand-off sections at all → zero measurable
	// attempts, not merged.
	w425CohortWrite(t, caps, w425CohortCap{"t-2", "develop", "2026-11-03", w425Plan(map[string]int{"u1": 1}),
		map[string]string{"review/s1": w425Review("pass")}, "# 交接文档\n"})
	w425CohortWrite(t, caps, w425CohortCap{"t-3", "develop", "2026-11-04", w425Plan(map[string]int{"u1": 1}),
		map[string]string{"review/s2": w425Review("pass")}, t3})

	manifest := filepath.Join(t.TempDir(), "cohort.json")
	man := `{"version": "v0.2.2", "control": ["t-1", "t-2", "t-3"], "split": []}`
	if err := os.WriteFile(manifest, []byte(man), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(t.TempDir(), "runs.csv")
	args := []string{"-I", filepath.Join(v1Root(t), "tools", "review-firstpass"), caps,
		"--cohort", manifest, "--csv", out}
	got, err := exec.Command("python3", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("review-firstpass --cohort: %v\n%s", err, got)
	}
	text := string(got)
	for _, want := range []string{
		// measurable 1560+0+170=1730s; rework 540+80=620s → 36%;
		// counted 1+1=2, owner/uncounted 1; infra 1 round (480s).
		"  proxy execution time: 1730s measurable, rework 620s (36%); rewinds counted 2, owner/uncounted 1; infra-retry rounds 1 (480s) listed apart",
		// merged 1 (claim 10:00 → merge ended 10:45 → 45m); the real
		// endpoint is N/A offline, and t-2/t-3 never merged.
		"  claim->merge: merge-step proxy median 45m (1 merged, 2 not merged); real endpoint N/A (3 runs with no offline record)",
		// the empty split group keeps the same honesty.
		"  proxy execution time: 0s measurable, rework N/A (0 measurable); rewinds counted 0, owner/uncounted 0; infra-retry rounds 0 (0s) listed apart",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the cohort time metrics must carry %q:\n%s", want, text)
		}
	}

	f, err := os.Open(out)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]map[string]string{}
	for _, r := range recs[1:] {
		m := map[string]string{}
		for i, k := range recs[0] {
			m[k] = r[i]
		}
		rows[m["capsule"]] = m
	}
	for name, want := range map[string]string{
		"t-1": "1560 540 480 1 0 2026-11-02T10:00:00Z 2026-11-02T10:45:00Z 1",
		"t-3": "170 80 0 1 1 2026-11-04T10:00:00Z  0",
		"t-2": "0 0 0 0 0 2026-11-03T10:00:00Z  0",
	} {
		r, ok := rows[name]
		if !ok {
			t.Fatalf("run %s missing from the CSV", name)
		}
		if g := strings.Join([]string{r["attempt_s"], r["rework_s"], r["infra_retry_s"], r["rewinds_counted"], r["rewinds_uncounted"], r["claim_at"], r["merge_done_at"], r["merged"]}, " "); g != want {
			t.Errorf("run %s: attempt rework infra counted uncounted claim merge merged = %q, want %q", name, g, want)
		}
	}

	// without --cohort the CSV schema stays the pre-cohort one.
	plain := filepath.Join(t.TempDir(), "plain.csv")
	if plainOut, err := exec.Command("python3", "-I", filepath.Join(v1Root(t), "tools", "review-firstpass"), caps, "--csv", plain).CombinedOutput(); err != nil {
		t.Fatalf("review-firstpass plain: %v\n%s", err, plainOut)
	}
	head, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(head), "attempt_s") {
		t.Error("without --cohort the CSV must keep the pre-cohort schema (no attempt_s column)")
	}
}
