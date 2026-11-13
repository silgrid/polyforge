package test

// w425_review_firstpass_test.go (AC5): tools/review-firstpass
// re-runs the rework baseline's measure over a capsules directory — the
// first review's verdict per run, the plan's ex-ante write set (develop:
// the initial units only), the <=7 / 8-22 / >22 buckets — and groups it by
// who implemented the units of the build the first review judged.

import (
	"encoding/csv"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type w425Cap struct {
	name, lane, created string
	plan                string            // artifacts/plan.md; "" = none
	results             map[string]string // "<step>/s<n>" → result.json
}

func w425Plan(units map[string]int) string {
	var b strings.Builder
	b.WriteString("# plan\n\n## Units\n\n")
	for _, id := range []string{"u1", "u2", "u3"} {
		n, ok := units[id]
		if !ok {
			continue
		}
		fmt.Fprintf(&b, "### %s — unit\n\n- Touched files: ", id)
		for i := 0; i < n; i++ {
			fmt.Fprintf(&b, "pkg/%s/f%d.go (write), ", id, i)
		}
		b.WriteString("pkg/shared/read.go (read)\n\n")
	}
	return b.String()
}

func w425Build(by ...string) string {
	var units []string
	for i, who := range by {
		u := fmt.Sprintf(`{"id":"u%d"`, i+1)
		if who != "" {
			u += fmt.Sprintf(`,"implemented_by":%q`, who)
		}
		units = append(units, u+"}")
	}
	return `{"outcome":"completed","output":{"units":[` + strings.Join(units, ",") + `]}}`
}

func w425Review(verdict string) string {
	return `{"outcome":"completed","output":{"verdict":"` + verdict + `"}}`
}

func TestW425ReviewFirstpassMeasure(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("python3 is a pf doctor prerequisite and runs this tool: %v", err)
	}
	caps := t.TempDir()
	for _, c := range []w425Cap{
		// 3 written files, built by the session itself, first review PASS
		{"a-1", "develop", "2026-11-01", w425Plan(map[string]int{"u1": 2, "u2": 1}),
			map[string]string{"build/s1": w425Build("main", "main"), "review/s2": w425Review("pass")}},
		// 10 written files, built by sub-agents; first review FAIL, then PASS
		{"b-2", "develop", "2026-11-02", w425Plan(map[string]int{"u1": 5, "u2": 5}),
			map[string]string{"build/s1": w425Build("subagent", "subagent"), "review/s2": w425Review("fail"),
				"build/s3": w425Build("main", "main"), "review/s4": w425Review("pass")}},
		// 25 written files; the first build result is a grill stop (no
		// units: every plan unit counts); the judged build mixes both
		{"c-3", "develop", "2026-11-03", w425Plan(map[string]int{"u1": 12, "u2": 13}),
			map[string]string{"build/s1": `{"outcome":"needs_input"}`, "build/s2": w425Build("main", "subagent"), "review/s3": w425Review("pass")}},
		// a lane whose build reports no implementer
		{"d-4", "chore", "2026-11-04", w425Plan(map[string]int{"u1": 2}),
			map[string]string{"code/s2": `{"outcome":"completed","output":{}}`, "review/s3": w425Review("fail")}},
		// develop's ex-ante size: u2 was added by a rework, the initial
		// build listed only u1 (4 files, not 4+9)
		{"g-7", "develop", "2026-11-05", w425Plan(map[string]int{"u1": 4, "u2": 9}),
			map[string]string{"build/s1": w425Build("subagent"), "review/s2": w425Review("pass")}},
		// reviewed but no plan: out of the size table
		{"h-8", "develop", "2026-11-05", "", map[string]string{"review/s1": w425Review("pass")}},
		// never reviewed: counts nowhere
		{"f-6", "develop", "2026-11-05", w425Plan(map[string]int{"u1": 1}), map[string]string{"build/s1": w425Build("main")}},
		// quarantined: skipped
		{"e-5.quarantined.x1", "develop", "2026-11-05", w425Plan(map[string]int{"u1": 1}),
			map[string]string{"build/s1": w425Build("main"), "review/s2": w425Review("pass")}},
		// before --since
		{"i-9", "develop", "2026-10-01", w425Plan(map[string]int{"u1": 1}),
			map[string]string{"build/s1": w425Build("main"), "review/s2": w425Review("pass")}},
	} {
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
	}
	out := filepath.Join(t.TempDir(), "runs.csv")
	cmd := exec.Command("python3", "-I", filepath.Join(v1Root(t), "tools", "review-firstpass"), caps, "--since", "2026-10-30", "--csv", out)
	got, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("review-firstpass: %v\n%s", err, got)
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
		"a-1": "PASS 3 <=7 main 1",
		"b-2": "FAIL 10 8-22 subagent 2",
		"c-3": "PASS 25 >22 mixed 1",
		"d-4": "FAIL 2 <=7 unrecorded 1",
		"g-7": "PASS 4 <=7 subagent 1",
		"h-8": "PASS   unrecorded 1",
		"f-6": "    0",
	} {
		r, ok := rows[name]
		if !ok {
			t.Fatalf("run %s missing from the CSV (%v)", name, rows)
		}
		if g := strings.Join([]string{r["first_review"], r["written_files"], r["bucket"], r["implementer"], r["review_runs"]}, " "); g != want {
			t.Errorf("run %s: first_review written bucket implementer review_runs = %q, want %q", name, g, want)
		}
	}
	for _, gone := range []string{"e-5.quarantined.x1", "i-9"} {
		if _, ok := rows[gone]; ok {
			t.Errorf("run %s must be outside the run set", gone)
		}
	}
	text := string(got)
	for _, w := range []string{
		"runs=7 quarantined(skipped)=1 reviewed=6 sized(plan-declared write set)=5",
		"<=7                         3         2/3    67%        1",
		"8-22                        1         0/1     0%        2",
		">22                         1         1/1   100%        1",
		"subagent                    2         1/2    50%      1.5",
	} {
		if !strings.Contains(text, w) {
			t.Errorf("the report must carry %q:\n%s", w, text)
		}
	}
}
