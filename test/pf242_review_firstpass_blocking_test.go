package test

// carries: tools/review-firstpass, test/pf242_review_firstpass_blocking_test.go

// pf242_review_firstpass_blocking_test.go (polyforge#242, AC1): the blocking
// findings block tools/review-firstpass gains under --events — per blocker
// the class and the outcome, chained across review rounds by the
// `r<n>#<k>` reference and by the owner's asks.jsonl rulings, the suspected
// false-positive rate by class and by model, and the unparsable FAIL
// versions counted apart. Every expected number below is hand-derived from
// the ticket's fixture table (polyforge#242, 分析 2) and never read out of
// the tool.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func pf242Git(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git -C %s %s: %v\n%s", dir, strings.Join(args, " "), err, out)
	}
}

func pf242Write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pf242Stamp is one daemon-stamped review section: the round, the model and
// the verdict the blocking block reads beside the review.md versions.
func pf242Stamp(round int, model, status string) string {
	return fmt.Sprintf(`## review · 第 %d 轮 · 派发 %d · %s

| 项 | 值 |
|---|---|
| 步骤 | review |
| 轮次 | %d |
| 模型 | %s |
| 状态 | %s |
| 结束 | 2026-11-14T1%d:00:00Z |

### 正文（step 撰写）

`, round, round, status, round, model, status, round)
}

type pf242Cap struct {
	name     string
	versions []string // review.md versions, oldest first; empty = no review
	stamps   []string // handoff.md review sections
	asks     []string // runtime-state/asks.jsonl lines
	git      bool
}

// pf242Capsules writes the fixture capsules; with git it commits every
// review.md version on its own so the tool reads the version chain the way
// it reads a real capsule's.
func pf242Capsules(t *testing.T, caps []pf242Cap) string {
	t.Helper()
	dir := t.TempDir()
	for _, c := range caps {
		cap := filepath.Join(dir, c.name)
		pf242Write(t, filepath.Join(cap, "capsule.json"),
			fmt.Sprintf(`{"bundle_name":"develop","created_at":"2026-11-14T10:00:00Z","work_item_id":%q}`, c.name))
		if len(c.stamps) > 0 {
			pf242Write(t, filepath.Join(cap, "artifacts", "handoff.md"),
				"# handoff\n\n"+strings.Join(c.stamps, "\n---\n"))
		}
		if len(c.asks) > 0 {
			pf242Write(t, filepath.Join(cap, "runtime-state", "asks.jsonl"), strings.Join(c.asks, "\n")+"\n")
		}
		if c.git {
			pf242Git(t, cap, "init", "-q")
			for i, body := range c.versions {
				pf242Write(t, filepath.Join(cap, "artifacts", "review.md"), body)
				pf242Git(t, cap, "add", "-A")
				pf242Git(t, cap, "commit", "-q", "-m", fmt.Sprintf("review v%d", i+1))
			}
		} else if len(c.versions) > 0 {
			pf242Write(t, filepath.Join(cap, "artifacts", "review.md"), c.versions[len(c.versions)-1])
		}
	}
	return dir
}

func pf242Run(t *testing.T, caps string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("python3", append([]string{"-I", filepath.Join(v1Root(t), "tools", "review-firstpass"), caps}, args...)...)
	out, err := cmd.CombinedOutput()
	code := 0
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	} else if err != nil {
		t.Fatalf("review-firstpass: %v", err)
	}
	return string(out), code
}

func pf242Review(title, conclusion, findings string) string {
	return title + "\n\n" + conclusion + "\n\n" + findings + "\n"
}

// pf242Fixtures: the ticket's capsules A–E. A#1 fixed, A#2 rebutted by
// build evidence, B#1 overturned by the owner's reply, C#1 one chain
// across two failure rounds then fixed, D#1 with no later round, E#1 a
// FAIL version whose item format cannot be parsed.
func pf242Fixtures(t *testing.T) (string, string) {
	t.Helper()
	caps := pf242Capsules(t, []pf242Cap{
		{name: "a-1", git: true,
			versions: []string{
				pf242Review("# polyforge#242 review · 第 1 轮", "**结论：FAIL** —— 2 条阻断、1 条非阻断。",
					"## 发现\n\n### #1 · blocking · bug — the first defect\n\nbody\n\n### #2 · blocking · test_weakened — the second defect\n\nbody\n\n### #3 · non-blocking · 其他\n\nbody\n"),
				pf242Review("# polyforge#242 review · 第 2 轮", "**结论：PASS** —— 0 条阻断。",
					"## 上一轮发现逐条核对\n\n- **r1#1 已修。** fixed\n\n- **r1#2：撤回（build 证据成立）。** rebutted\n"),
			},
			stamps: []string{pf242Stamp(1, "M1", "fail"), pf242Stamp(2, "M1", "done")}},
		{name: "b-1", git: true,
			versions: []string{
				pf242Review("# polyforge#242 review · 第 1 轮", "**结论：FAIL** —— 1 条阻断。",
					"## 发现\n\n### #1 blocking · ac_gap · AC2 — missing test\n\nbody\n"),
			},
			stamps: []string{pf242Stamp(1, "M2", "fail")},
			asks:   []string{`{"at":"2026-11-14T11:00:00Z","step":"build","kind":"reply","text":"r1#1 改判非阻断，放行"}`}},
		{name: "c-1", git: true,
			versions: []string{
				pf242Review("# polyforge#242 review · 第 1 轮", "**结论：FAIL** —— 1 条阻断。",
					"## 发现\n\n### #1 blocking · guarantee — the open guarantee\n\nbody\n"),
				pf242Review("# polyforge#242 review · 第 2 轮", "**结论：FAIL** —— 1 条阻断。",
					"## 发现\n\n### #1 blocking · guarantee（残留 r1#1）— still open\n\nbody\n"),
				pf242Review("# polyforge#242 review · 第 3 轮", "**结论：PASS** —— 0 条阻断。",
					"## 上一轮发现逐条核对\n\n- **r2#1 已修。**\n"),
			},
			stamps: []string{pf242Stamp(1, "M1", "fail"), pf242Stamp(2, "M1", "fail"), pf242Stamp(3, "M1", "done")}},
		{name: "d-1", git: true,
			versions: []string{
				pf242Review("# polyforge#242 review · 第 1 轮", "**结论：FAIL** —— 1 条阻断。",
					"## 发现\n\n### #1 · blocking · bug — no later round\n\nbody\n"),
			},
			stamps: []string{pf242Stamp(1, "M1", "fail")}},
		{name: "e-1", git: true,
			versions: []string{
				pf242Review("# polyforge#242 review · 第 1 轮", "**结论：FAIL** —— 1 条阻断。",
					"## 发现\n\n- 这个版本的条目写法解析不了\n"),
			},
			stamps: []string{pf242Stamp(1, "M1", "fail")}},
	})
	return caps, t.TempDir()
}

func pf242Has(t *testing.T, out string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !strings.Contains(out, w+"\n") {
			t.Fatalf("output lacks %q\n----\n%s", w, out)
		}
	}
}

func TestPF242ReviewFirstpassBlockingParse(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("python3 is a pf doctor prerequisite and runs this tool: %v", err)
	}
	caps, events := pf242Fixtures(t)
	out, code := pf242Run(t, caps, "--since", "2026-11-14", "--events", events)
	if code != 0 {
		t.Fatalf("review-firstpass exit %d\n%s", code, out)
	}
	pf242Has(t, out,
		"review history: 5 capsules, 8 review versions, 6 FAIL versions",
		"blocking items: 6 (chains 5, cross-round 1)",
		"outcomes: fixed 2, rebutted 1, owner-overturned 1, withdrawn-unknown 0, unknown 1",
		"decidable: 4; suspected false positives: 2/4 (50%)",
		"sensitivity (a PASS round without naming it counted fixed): 2/4 (50%)",
		"unparsable FAIL versions: 1",
		"by class:",
		"  bug: items 2, decidable 1, suspected 0/1 (0%)",
		"  ac_gap: items 1, decidable 1, suspected 1/1 (100%)",
		"  project_rule: items 0, decidable 0, suspected n/a",
		"  test_weakened: items 1, decidable 1, suspected 1/1 (100%)",
		"  guarantee: items 1, decidable 1, suspected 0/1 (0%)",
		"  unclassified: items 0, decidable 0, suspected n/a",
		"by model:",
		"  M1: items 4, decidable 3, suspected 1/3 (33%)",
		"  M2: items 1, decidable 1, suspected 1/1 (100%)",
		"unknown by reason:",
		"  no later round: 1",
		"  later rounds do not mention it: 0",
		"  PASS round without naming it: 0",
		"  owner words without an item number: 0",
		"  multiple candidates: 0",
		"  last mention still open: 0",
	)
	if strings.Contains(out, "suspected false positives: 2/5") {
		t.Fatalf("an undecidable blocker entered the denominator\n%s", out)
	}
	// without --events the report is byte-identical to the pre-change one:
	// no blocking block at all.
	plain, code := pf242Run(t, caps, "--since", "2026-11-14")
	if code != 0 {
		t.Fatalf("review-firstpass exit %d\n%s", code, plain)
	}
	if strings.Contains(plain, "== blocking findings") || strings.Contains(plain, "blocking items:") {
		t.Fatalf("the blocking block must be gated by --events\n%s", plain)
	}
}

func TestPF242ReviewFirstpassBlockingParseHandoffFallback(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("python3 is a pf doctor prerequisite and runs this tool: %v", err)
	}
	body := pf242Stamp(1, "M1", "fail") +
		"### 发现的问题和风险\n\n### #1 blocking · bug — from the stamped review section\n\nbody\n"
	caps := pf242Capsules(t, []pf242Cap{{name: "h-1", git: false, stamps: []string{body}}})
	events := t.TempDir()
	out, code := pf242Run(t, caps, "--since", "2026-11-14", "--events", events)
	if code != 0 {
		t.Fatalf("review-firstpass exit %d\n%s", code, out)
	}
	pf242Has(t, out,
		"review history: 1 capsules, 1 review versions, 1 FAIL versions",
		"blocking items: 1 (chains 1, cross-round 0)",
		"  no later round: 1",
	)
}
