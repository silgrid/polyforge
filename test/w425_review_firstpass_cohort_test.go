package test

// carries: tools/review-firstpass, test/w425_review_firstpass_test.go, test/w425_review_firstpass_cohort_test.go

// w425_review_firstpass_cohort_test.go (polyforge#118, AC2): the metrics
// tool's --cohort view — an owner-confirmed cohort manifest groups the
// runs (unsplit control / split children / the epic aggregation listed
// apart), echoes the pinned #1 background baseline, keeps the plan-size
// table per group, and reports honestly. Review round 1 (#4) showed the
// fixtures mixed two protocols in one capsule: the current daemon stamps
// hand-off sections and writes no artifacts/steps/*/result.json, while
// the 2026-11-09 baseline cohort (the old capsules) carries result.json
// — so every fixture below is ONE real shape: the control capsules are
// old-protocol (result.json, the baseline cohort's history), the split
// capsules are current-protocol (units.json + stamped hand-off sections
// only) and their first-pass review reads the hand-off stamps. The
// rewind classification (#5) uses the daemon's own zh and EN texts —
// including the cumulative notes that follow a lane-uncounted rewind,
// which must not swallow a later counted one.

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
	results             map[string]string // "<step>/s<n>" → result.json (old protocol)
	units               string            // artifacts/build/units.json; "" = none
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
	created := c.created
	if !strings.Contains(created, "T") {
		created += "T10:00:00Z"
	}
	write("capsule.json", fmt.Sprintf(`{"bundle_name":%q,"created_at":%q}`, c.lane, created))
	if c.plan != "" {
		write("artifacts/plan.md", c.plan)
	}
	if c.units != "" {
		write("artifacts/build/units.json", c.units)
	}
	for k, v := range c.results {
		step, run, _ := strings.Cut(k, "/")
		write("artifacts/steps/"+step+"/"+run+"/result.json", v)
	}
	if c.handoff != "" {
		write("artifacts/handoff.md", c.handoff)
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

// w425ZhRewind builds one zh rewind event section with the daemon's own
// line texts (language.go zh labels).
func w425ZhRewind(redo, count string) string {
	return "---\n\n## 事件 · 回退 · 2026-11-05T10:00:00Z\n\n- 触发：`review` 第 1 轮交接 `fail`\n- 原因：见该步该段正文\n- 要重做的步骤（按顺序）：" + redo + "\n- 回退次数：" + count + "\n- 读法：x\n"
}

// w425EnRewind is the EN twin (language.go en labels).
func w425EnRewind(redo, count string) string {
	return "---\n\n## Event · Rewind · 2026-11-05T10:00:00Z\n\n- Trigger: `review` Round 1 handed off `fail`\n- Reason: see that step's section body\n- Steps to redo (in order): " + redo + "\n- Rewinds: " + count + "\n- How to read: x\n"
}

// TestW425ReviewFirstpassCohort pins the cohort view: grouped first-pass
// by plan size — the control capsules read the old result.json shape
// (the baseline cohort's history), the split children are current
// protocol and read the hand-off stamps — the baseline echo, the
// honesty lines (missing, window-filtered, quarantined), and the empty
// split group's N/A. Every expected value below is hand-computed from
// the fixture rows.
func TestW425ReviewFirstpassCohort(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("python3 is a pf doctor prerequisite and runs this tool: %v", err)
	}
	caps := t.TempDir()
	// the control group: the OLD protocol — the 2026-11-09 baseline
	// cohort's capsule shape (result.json; the current daemon writes
	// none). One PASS at <=7, one FAIL at 8-22.
	for _, c := range []w425CohortCap{
		{"ctl-1", "develop", "2026-11-02", w425Plan(map[string]int{"u1": 2, "u2": 1}), "",
			map[string]string{"build/s1": w425Build("main", "main"), "review/s2": w425Review("pass")}, ""},
		{"ctl-2", "develop", "2026-11-03", w425Plan(map[string]int{"u1": 5, "u2": 5}), "",
			map[string]string{"build/s1": w425Build("subagent", "subagent"), "review/s2": w425Review("fail")}, ""},
		// outside the manifest window: filtered, not missing
		{"ctl-3", "develop", "2026-10-01", w425Plan(map[string]int{"u1": 1}), "",
			map[string]string{"review/s1": w425Review("pass")}, ""},
		// a quarantined run the manifest names: excluded, listed
		{"q-8.quarantined.x1", "develop", "2026-11-05", w425Plan(map[string]int{"u1": 1}), "",
			map[string]string{"review/s1": w425Review("pass")}, ""},
	} {
		w425CohortWrite(t, caps, c)
	}
	// the split group: CURRENT protocol capsules — plan.md +
	// artifacts/build/units.json + stamped hand-off sections, no
	// result.json anywhere. kid-1: first review done → PASS, 3 written
	// files (<=7). kid-2: first review fail → FAIL, 12 written files
	// (8-22), rework after a counted rewind. ep-1: the epic, reviewed
	// with no plan → unknown.
	kid1 := "# 交接文档\n\n"
	kid1 += w425ZhSection("build", 1, "2026-11-04T10:00:00Z", "2026-11-04T10:05:00Z", "5m0s", "done", nil)
	kid1 += w425ZhSection("review", 1, "2026-11-04T10:06:00Z", "2026-11-04T10:07:00Z", "1m0s", "done", nil)
	kid2 := "# 交接文档\n\n"
	kid2 += w425ZhSection("build", 1, "2026-11-05T10:00:00Z", "2026-11-05T10:10:00Z", "10m0s", "done", nil)
	kid2 += w425ZhSection("review", 1, "2026-11-05T10:11:00Z", "2026-11-05T10:13:00Z", "2m0s", "fail", nil)
	kid2 += w425ZhRewind("`build` → `review`", "1/3")
	kid2 += w425ZhSection("build", 2, "2026-11-05T10:20:00Z", "2026-11-05T10:28:00Z", "8m0s", "done", nil)
	kid2 += w425ZhSection("review", 2, "2026-11-05T10:29:00Z", "2026-11-05T10:30:00Z", "1m0s", "done", nil)
	ep1 := "# 交接文档\n\n"
	ep1 += w425ZhSection("review", 1, "2026-11-06T10:00:00Z", "2026-11-06T10:02:00Z", "2m0s", "done", nil)
	for _, c := range []w425CohortCap{
		{"kid-1", "develop", "2026-11-04", w425Plan(map[string]int{"u1": 2, "u2": 1}),
			`[{"id":"u1"},{"id":"u2"}]`, nil, kid1},
		{"kid-2", "develop", "2026-11-05", w425Plan(map[string]int{"u1": 6, "u2": 6}),
			`[{"id":"u1"},{"id":"u2"}]`, nil, kid2},
		{"ep-1", "epic", "2026-11-06", "", "", nil, ep1},
	} {
		w425CohortWrite(t, caps, c)
	}
	manifest := filepath.Join(t.TempDir(), "cohort.json")
	man := `{
  "version": "v0.2.2",
  "window": {"since": "2026-11-01", "until": "2026-11-30"},
  "baseline": {"firstpass": {"<=7": "31/38", "8-22": "13/31", ">22": "4/34"}, "source": "the #1 background (rework baseline, 2026-11-09)"},
  "control": ["ctl-1", "ctl-2", "ctl-3", "q-8.quarantined.x1", "gone-9"],
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
		// control: 2 in-window old-protocol runs, both reviewed, 1 PASS
		// (<=7), 1 FAIL (8-22)
		"control (unsplit runs): 2 runs (2 reviewed), first-pass 1/2 (50%), plan sizes <=7 1/1, 8-22 0/1, >22 0/0, unknown 0",
		// the control capsules are old protocol: no stamps, nothing
		// measurable — N/A, never a 0 dressed as a rate
		"  proxy execution time: 0s measurable, rework N/A (0 measurable); rewinds counted 0, owner/uncounted 0; infra-retry rounds 0 (0s) listed apart",
		"  claim->merge: merge-step proxy median N/A (0 merged, 2 not merged); real endpoint N/A (2 runs with no offline record)",
		// split children, current protocol: first-pass reads the
		// stamps — kid-1 done, kid-2 fail
		"split children: 2 runs (2 reviewed), first-pass 1/2 (50%), plan sizes <=7 1/1, 8-22 0/1, >22 0/0, unknown 0",
		// kid-1: 300s+60s = 360s; kid-2: 600+120+480+60 = 1260s,
		// rework 540s (build r2 + review r2) → 540/1620 = 33%
		"  proxy execution time: 1620s measurable, rework 540s (33%); rewinds counted 1, owner/uncounted 0; infra-retry rounds 0 (0s) listed apart",
		"  claim->merge: merge-step proxy median N/A (0 merged, 2 not merged); real endpoint N/A (2 runs with no offline record)",
		// the epic is listed apart, never in the children's denominators
		"split epics (listed apart): 1 runs (1 reviewed), first-pass 1/1 (100%), plan sizes <=7 0/0, 8-22 0/0, >22 0/0, unknown 1",
		// the window filtered ctl-3 (it IS in the input); gone-9 is
		// not; q-8 is quarantined
		"outside the manifest window (filtered): ctl-3 (1)",
		"quarantined (counted, excluded): q-8.quarantined.x1 (1)",
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

// TestW425ReviewFirstpassCohortTime pins the cohort time metrics (AC2,
// the owner's Q3/Q4 calibers) and the rewind classification (#5): the
// rework share (a rewind section's named steps' later rounds over all
// stamped attempt time), counted and owner/uncounted rewinds told apart
// by the daemon's own zh and EN texts — a lane-uncounted rewind's
// cumulative note must not swallow a later counted one, and an EN
// owner rewind rests on the EN marker — infra-retry rounds listed
// apart, the claim-to-merge proxy with the real endpoint N/A, and the
// zero-denominator and not-merged honesty. Every expected value below
// is hand-computed from the fixture sections.
func TestW425ReviewFirstpassCohortTime(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("python3 is a pf doctor prerequisite and runs this tool: %v", err)
	}
	// t-1 (zh, current protocol): attempts 600+120+480+60+300=1560s;
	// after the counted rewind naming build and review, build r2 (480s)
	// and review r2 (60s) re-run → rework 540s (35%); build r2 carries
	// an infra event (rate_limited) → 1 infra-retry round (480s) listed
	// apart; merged at 10:45, claim 10:00 (RFC3339Nano, the real daemon
	// shape) → proxy 45m; first review fail.
	t1 := "# 交接文档\n\n"
	t1 += w425ZhSection("build", 1, "2026-11-02T10:01:00Z", "2026-11-02T10:11:00Z", "10m0s", "done", nil)
	t1 += w425ZhSection("review", 1, "2026-11-02T10:12:00Z", "2026-11-02T10:14:00Z", "2m0s", "fail", nil)
	t1 += w425ZhRewind("`build` → `review`", "1/3")
	t1 += w425ZhSection("build", 2, "2026-11-02T10:20:00Z", "2026-11-02T10:28:00Z", "8m0s", "done",
		[]string{"2026-11-02T10:21:00Z step_failed rate_limited (pi:example): 429"})
	t1 += w425ZhSection("review", 2, "2026-11-02T10:29:00Z", "2026-11-02T10:30:00Z", "1m0s", "done", nil)
	t1 += w425ZhSection("merge", 1, "2026-11-02T10:40:00Z", "2026-11-02T10:45:00Z", "5m0s", "done", nil)

	// t-3 (EN, current protocol): four rewinds — #1 counted; #2
	// lane-uncounted (the note starts the cumulative count); #3 an
	// owner rewind (the EN per-owner marker beside both notes); #4
	// counted again, CARRYING the cumulative notes — the case the
	// notes must not swallow. Attempts 60+30+30+30+20+20+40=230s;
	// rework: build r2 30 + review r2 30 (after #1) + verification r1 20
	// (after #2) + build r3 20 (after #3) + build r4 40 (after #4) =
	// 140s → 61%; counted 2, owner/uncounted 2; never merged.
	t3 := "# Hand-off document\n\n"
	t3 += w425EnSection("build", 1, "2026-11-04T10:01:00Z", "2026-11-04T10:02:00Z", "1m0s", "done", nil)
	t3 += w425EnSection("review", 1, "2026-11-04T10:03:00Z", "2026-11-04T10:03:30Z", "30s", "fail", nil)
	t3 += w425EnRewind("`build` → `review`", "1/3")
	t3 += w425EnSection("build", 2, "2026-11-04T10:10:00Z", "2026-11-04T10:10:30Z", "30s", "done", nil)
	t3 += w425EnSection("review", 2, "2026-11-04T10:11:00Z", "2026-11-04T10:11:30Z", "30s", "done", nil)
	t3 += w425EnRewind("`verification`", "1/3 (plus 1/2 lane-declared uncounted rewinds)")
	t3 += w425EnSection("verification", 1, "2026-11-04T10:12:00Z", "2026-11-04T10:12:20Z", "20s", "done", nil)
	t3 += w425EnRewind("`build`", "1/3 (plus 1 owner rewinds, not counted) (plus 1/2 lane-declared uncounted rewinds); this one rests on the owner's word and is not counted")
	t3 += w425EnSection("build", 3, "2026-11-04T10:20:00Z", "2026-11-04T10:20:20Z", "20s", "done", nil)
	t3 += w425EnRewind("`build`", "2/3 (plus 1 owner rewinds, not counted) (plus 1/2 lane-declared uncounted rewinds)")
	t3 += w425EnSection("build", 4, "2026-11-04T10:30:00Z", "2026-11-04T10:30:40Z", "40s", "done", nil)

	// t-4 (zh, current protocol): the zh cumulative-note case — #1
	// counted; #2 lane-uncounted（另有不计数类回退 1/2 次）; #3 counted
	// again, carrying the same note (the count did not move) — the zh
	// misclassification the round-1 review found. Attempts
	// 100+50+80+40+30=300s; rework 80+40 (after #1) + 30 (after #3) =
	// 150s → 50%; counted 2, owner/uncounted 1; never merged.
	t4 := "# 交接文档\n\n"
	t4 += w425ZhSection("build", 1, "2026-11-05T10:01:00Z", "2026-11-05T10:02:40Z", "1m40s", "done", nil)
	t4 += w425ZhSection("review", 1, "2026-11-05T10:03:00Z", "2026-11-05T10:03:50Z", "50s", "fail", nil)
	t4 += w425ZhRewind("`build` → `review`", "1/3")
	t4 += w425ZhSection("build", 2, "2026-11-05T10:10:00Z", "2026-11-05T10:11:20Z", "1m20s", "done", nil)
	t4 += w425ZhSection("review", 2, "2026-11-05T10:12:00Z", "2026-11-05T10:12:40Z", "40s", "done", nil)
	t4 += w425ZhRewind("`sync`", "1/3（另有不计数类回退 1/2 次）")
	t4 += w425ZhRewind("`build`", "2/3（另有不计数类回退 1/2 次）")
	t4 += w425ZhSection("build", 3, "2026-11-05T10:20:00Z", "2026-11-05T10:20:30Z", "30s", "done", nil)

	caps := t.TempDir()
	w425CohortWrite(t, caps, w425CohortCap{"t-1", "develop", "2026-11-02T10:00:00.123456789Z", w425Plan(map[string]int{"u1": 2}),
		`[{"id":"u1"}]`, nil, t1})
	// t-2: OLD protocol (the baseline cohort's shape), reviewed, no
	// stamps → zero measurable attempts, not merged.
	w425CohortWrite(t, caps, w425CohortCap{"t-2", "develop", "2026-11-03", w425Plan(map[string]int{"u1": 1}),
		"", map[string]string{"review/s1": w425Review("pass")}, ""})
	w425CohortWrite(t, caps, w425CohortCap{"t-3", "develop", "2026-11-04", w425Plan(map[string]int{"u1": 1}),
		`[{"id":"u1"}]`, nil, t3})
	w425CohortWrite(t, caps, w425CohortCap{"t-4", "develop", "2026-11-05", w425Plan(map[string]int{"u1": 1}),
		`[{"id":"u1"}]`, nil, t4})

	manifest := filepath.Join(t.TempDir(), "cohort.json")
	man := `{"version": "v0.2.2", "control": ["t-1", "t-2", "t-3", "t-4"], "split": []}`
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
		// measurable 1560+0+230+300=2090s; rework 540+0+140+150=830s →
		// 40%; counted 1+0+2+2=5, owner/uncounted 0+0+2+1=3; infra 1
		// round (480s).
		"  proxy execution time: 2090s measurable, rework 830s (40%); rewinds counted 5, owner/uncounted 3; infra-retry rounds 1 (480s) listed apart",
		// merged 1 (claim 10:00.123 → merge ended 10:45 → 45m); the
		// real endpoint is N/A offline; t-2/t-3/t-4 never merged.
		"  claim->merge: merge-step proxy median 45m (1 merged, 3 not merged); real endpoint N/A (4 runs with no offline record)",
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
		// attempt rework infra counted uncounted claim merge merged —
		// and the current protocol reads the first review from the
		// stamps (t-1's first review failed)
		"t-1": "FAIL 1560 540 480 1 0 2026-11-02T10:00:00.123456789Z 2026-11-02T10:45:00Z 1",
		"t-3": "FAIL 230 140 0 2 2 2026-11-04T10:00:00Z  0",
		"t-4": "FAIL 300 150 0 2 1 2026-11-05T10:00:00Z  0",
		"t-2": "PASS 0 0 0 0 0 2026-11-03T10:00:00Z  0",
	} {
		r, ok := rows[name]
		if !ok {
			t.Fatalf("run %s missing from the CSV", name)
		}
		if g := strings.Join([]string{r["first_review"], r["attempt_s"], r["rework_s"], r["infra_retry_s"], r["rewinds_counted"], r["rewinds_uncounted"], r["claim_at"], r["merge_done_at"], r["merged"]}, " "); g != want {
			t.Errorf("run %s: first_review attempt rework infra counted uncounted claim merge merged = %q, want %q", name, g, want)
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
