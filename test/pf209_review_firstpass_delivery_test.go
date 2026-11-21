// carries: tools/review-firstpass, test/pf209_review_firstpass_delivery_test.go

package test

// pf209_review_firstpass_delivery_test.go (polyforge#209, AC1–AC3): the
// delivery block tools/review-firstpass gains with --events — wall clock,
// p90, per-file delivery time, rework share, first pass, infra failures by
// model and model-class candidate switches with the 503 subcount — the
// cohort manifest's `arms` view, and the estimate-line parser. Every
// expected value below is hand-derived from the ticket's fixture tables
// (with the owner's plan-review correction: dv-b carries five opus
// step_started events, so opus 9 / all 30), never read out of the tool.

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type pf209Event struct {
	at   string
	kind string
	body map[string]interface{}
}

type pf209Spawn struct {
	started    string
	transcript string
}

type pf209Cap struct {
	name, lane, created, content string
	repos                        map[string][]int // repo -> files added by each head commit
	handoff                      func(sha map[string][]string) string
	events                       []pf209Event
	journal                      []pf209Spawn
	sessions                     map[string][]string // transcript basename -> JSONL lines
}

func pf209At(date, clock string) string {
	if len(clock) == 5 {
		clock += ":00"
	}
	return date + "T" + clock + "Z"
}

func pf209Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git -C %s %s: %v\n%s", dir, strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func pf209WriteFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// pf209Repos makes one real git repo per entry: a base commit carrying a
// README, then one commit per file count; answers base + heads by repo.
func pf209Repos(t *testing.T, cap string, repos map[string][]int) map[string][]string {
	t.Helper()
	out := map[string][]string{}
	for repo, commits := range repos {
		dir := filepath.Join(cap, "repos", repo)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		pf209Git(t, dir, "init", "-q")
		pf209WriteFile(t, filepath.Join(dir, "README"), "base\n")
		pf209Git(t, dir, "add", ".")
		pf209Git(t, dir, "commit", "-q", "-m", "base")
		sha := []string{pf209Git(t, dir, "rev-parse", "HEAD")}
		for c, n := range commits {
			for i := 0; i < n; i++ {
				pf209WriteFile(t, filepath.Join(dir, fmt.Sprintf("c%d_f%d.txt", c, i)), "x\n")
			}
			pf209Git(t, dir, "add", ".")
			if n == 0 {
				pf209Git(t, dir, "commit", "-q", "--allow-empty", "-m", fmt.Sprintf("head %d", c+1))
			} else {
				pf209Git(t, dir, "commit", "-q", "-m", fmt.Sprintf("head %d", c+1))
			}
			sha = append(sha, pf209Git(t, dir, "rev-parse", "HEAD"))
		}
		out[repo] = sha
	}
	return out
}

func pf209Section(step string, round int, started, ended, dur, status string, repos []pf209RepoChange) string {
	var b strings.Builder
	fmt.Fprintf(&b, "---\n\n## %s · 第 %d 轮 · 派发 %d · %s\n\n", step, round, round, status)
	b.WriteString("| 项 | 值 |\n|---|---|\n")
	fmt.Fprintf(&b, "| 步骤 | %s |\n| 轮次 | %d |\n| 开始 | %s |\n| 结束 | %s |\n| 耗时 | %s |\n| 状态 | %s |\n",
		step, round, started, ended, dur, status)
	b.WriteString("\n**各仓提交**\n\n")
	for _, r := range repos {
		stat := fmt.Sprintf("%d files", r.Files)
		if r.Files == 0 {
			stat = "无新提交"
		}
		fmt.Fprintf(&b, "- `%s`: `%s` → `%s`（%s）\n", r.Repo, r.Start, r.End, stat)
	}
	b.WriteString("\n### 正文（step 撰写）\n\nx\n")
	return b.String()
}

type pf209RepoChange struct {
	Repo       string
	Start, End string
	Files      int
}

func pf209WriteCapsule(t *testing.T, caps string, c pf209Cap) {
	t.Helper()
	dir := filepath.Join(caps, c.name)
	created := c.created
	if created == "" {
		created = "2026-11-10T00:00:00Z"
	}
	body, _ := json.Marshal(map[string]interface{}{
		"work_item_id": "wi_" + c.name, "bundle_name": c.lane, "created_at": created,
	})
	pf209WriteFile(t, filepath.Join(dir, "capsule.json"), string(body))
	if c.content != "" {
		pf209WriteFile(t, filepath.Join(dir, "runtime-state", "work-item.json"),
			fmt.Sprintf(`{"content":%q}`, c.content))
	}
	sha := pf209Repos(t, dir, c.repos)
	if c.handoff != nil {
		pf209WriteFile(t, filepath.Join(dir, "artifacts", "handoff.md"), "# 交接文档\n\n"+c.handoff(sha))
	}
	if len(c.journal) > 0 {
		var b strings.Builder
		for i, sp := range c.journal {
			line, _ := json.Marshal(map[string]interface{}{
				"at": sp.started, "step_id": "build", "spawn_n": i + 1,
				"started_at": sp.started, "transcript": sp.transcript,
			})
			b.WriteString(string(line) + "\n")
		}
		pf209WriteFile(t, filepath.Join(dir, "runtime-state", "spawn-journal.jsonl"), b.String())
	}
	for name, lines := range c.sessions {
		pf209WriteFile(t, filepath.Join(dir, "session", name), strings.Join(lines, "\n")+"\n")
	}
}

func pf209WriteEvents(t *testing.T, eventsDir, capsule string, evs []pf209Event) {
	t.Helper()
	var b strings.Builder
	for i, e := range evs {
		raw, err := json.Marshal(e.body)
		if err != nil {
			t.Fatal(err)
		}
		line, _ := json.Marshal(map[string]interface{}{
			"at": e.at, "kind": e.kind, "work_item": "wi_" + capsule, "seq": i + 1,
			"body": json.RawMessage(raw),
		})
		b.WriteString(string(line) + "\n")
	}
	pf209WriteFile(t, filepath.Join(eventsDir, "wi-wi_"+capsule+".jsonl"), b.String())
}

func pf209Ev(at, kind string, body map[string]interface{}) pf209Event {
	if body == nil {
		body = map[string]interface{}{}
	}
	return pf209Event{at: at, kind: kind, body: body}
}

func pf209Step(at, kind, step, model string) pf209Event {
	b := map[string]interface{}{"step": step}
	if model != "" {
		b["model"] = model
	}
	return pf209Ev(at, kind, b)
}

func pf209Session(messages ...string) []string { return messages }

func pf209Assistant(at, id string, output int) string {
	return fmt.Sprintf(`{"type":"message","timestamp":%q,"message":{"id":%q,"role":"assistant","content":"x","usage":{"input":10,"output":%d,"cacheRead":0,"cacheWrite":0}}}`, at, id, output)
}

func pf209User(at string) string {
	return fmt.Sprintf(`{"type":"message","timestamp":%q,"message":{"role":"user","content":"go"}}`, at)
}

func pf209ToolResult(at string) string {
	return fmt.Sprintf(`{"type":"message","timestamp":%q,"message":{"role":"toolResult","content":"ok"}}`, at)
}

// pf209Fixture builds the six develop capsules and their event files the
// ticket's AC1 table describes (dates 2026-11-10; times are the table's).
func pf209Fixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	caps, events := filepath.Join(root, "caps"), filepath.Join(root, "events")
	if err := os.MkdirAll(events, 0o755); err != nil {
		t.Fatal(err)
	}
	const d = "2026-11-10"

	// dv-a: 3 files, one build round, first review done; 34m wall,
	// 0 owner wait; output 1200 tokens / 40 generated seconds = 30.0.
	aEvents := []pf209Event{
		pf209Ev(pf209At(d, "10:00"), "job_started", nil),
		pf209Step(pf209At(d, "10:00"), "step_started", "build", "glm"),
		pf209Step(pf209At(d, "10:20"), "step_done", "build", ""),
		pf209Step(pf209At(d, "10:20"), "step_started", "review", "opus"),
		pf209Step(pf209At(d, "10:25"), "step_done", "review", ""),
		pf209Step(pf209At(d, "10:25"), "step_started", "sync", "flash"),
		pf209Step(pf209At(d, "10:26"), "step_done", "sync", ""),
		pf209Step(pf209At(d, "10:26"), "step_started", "verification", "flash"),
		pf209Step(pf209At(d, "10:30"), "step_done", "verification", ""),
		pf209Step(pf209At(d, "10:30"), "step_started", "publish", "flash"),
		pf209Step(pf209At(d, "10:32"), "step_done", "publish", ""),
		pf209Step(pf209At(d, "10:32"), "step_started", "merge", "flash"),
		pf209Step(pf209At(d, "10:34"), "step_done", "merge", ""),
		pf209Ev(pf209At(d, "10:34"), "terminal", map[string]interface{}{"status": "completed"}),
	}
	pf209WriteCapsule(t, caps, pf209Cap{
		name: "dv-a", lane: "develop", created: d + "T10:00:00Z", repos: map[string][]int{"polyforge": {3}},
		content: "## 分析\n\n### 1. 改动范围\n\n估算：polyforge 新增 1 个文件、修改 2 个文件、单元 2 个\n",
		handoff: func(sha map[string][]string) string {
			return pf209Section("build", 1, pf209At(d, "10:00"), pf209At(d, "10:20"), "20m0s", "done",
				[]pf209RepoChange{{"polyforge", sha["polyforge"][0], sha["polyforge"][1], 3}}) +
				pf209Section("review", 1, pf209At(d, "10:20"), pf209At(d, "10:25"), "5m0s", "done", nil)
		},
		journal: []pf209Spawn{{pf209At(d, "10:00"), filepath.Join(root, "session-a.jsonl")}},
		sessions: map[string][]string{"session-a.jsonl": pf209Session(
			pf209User(pf209At(d, "10:00:10")),
			pf209Assistant(pf209At(d, "10:00:30"), "a1", 400),
			pf209ToolResult(pf209At(d, "10:00:40")),
			pf209Assistant(pf209At(d, "10:01:00"), "a2", 800))},
	})
	pf209WriteEvents(t, events, "dv-a", aEvents)

	// dv-a2: 6 files, first review done; 60m wall; 600/30 = 20.0 tok/s.
	a2Events := []pf209Event{
		pf209Ev(pf209At(d, "08:00"), "job_started", nil),
		pf209Step(pf209At(d, "08:00"), "step_started", "build", "glm"),
		pf209Step(pf209At(d, "08:40"), "step_done", "build", ""),
		pf209Step(pf209At(d, "08:40"), "step_started", "review", "opus"),
		pf209Step(pf209At(d, "08:45"), "step_done", "review", ""),
		pf209Step(pf209At(d, "08:45"), "step_started", "sync", "flash"),
		pf209Step(pf209At(d, "08:46"), "step_done", "sync", ""),
		pf209Step(pf209At(d, "08:46"), "step_started", "verification", "flash"),
		pf209Step(pf209At(d, "08:52"), "step_done", "verification", ""),
		pf209Step(pf209At(d, "08:52"), "step_started", "publish", "flash"),
		pf209Step(pf209At(d, "08:55"), "step_done", "publish", ""),
		pf209Step(pf209At(d, "08:55"), "step_started", "merge", "flash"),
		pf209Step(pf209At(d, "09:00"), "step_done", "merge", ""),
		pf209Ev(pf209At(d, "09:00"), "terminal", map[string]interface{}{"status": "completed"}),
	}
	pf209WriteCapsule(t, caps, pf209Cap{
		name: "dv-a2", lane: "develop", created: d + "T08:00:00Z", repos: map[string][]int{"polyforge": {6}},
		content: "## 分析\n\n### 1. 改动范围\n\n估算：polyforge 新增 0 个文件、修改 2 个文件、单元 1 个\n",
		handoff: func(sha map[string][]string) string {
			return pf209Section("build", 1, pf209At(d, "08:00"), pf209At(d, "08:40"), "40m0s", "done",
				[]pf209RepoChange{{"polyforge", sha["polyforge"][0], sha["polyforge"][1], 6}}) +
				pf209Section("review", 1, pf209At(d, "08:40"), pf209At(d, "08:45"), "5m0s", "done", nil)
		},
		journal: []pf209Spawn{{pf209At(d, "08:00"), filepath.Join(root, "session-a2.jsonl")}},
		sessions: map[string][]string{"session-a2.jsonl": pf209Session(
			pf209User(pf209At(d, "08:00:00")),
			pf209Assistant(pf209At(d, "08:00:30"), "b1", 600))},
	})
	pf209WriteEvents(t, events, "dv-a2", a2Events)

	// dv-b: two build rounds; 8 then 10 files (head2 vs base); the glm
	// 503 switches to opus (1 model-class switch, 503 text), then the
	// owner asks with a 30m wait; 140m wall / 110m effective; rework
	// 20+5 = 25m; output 1500/60 = 25.0.
	bEvents := []pf209Event{
		pf209Ev(pf209At(d, "09:00"), "job_started", nil),
		pf209Step(pf209At(d, "09:00"), "step_started", "build", "glm"),
		pf209Ev(pf209At(d, "09:10"), "step_failed", map[string]interface{}{
			"step": "build", "cause": "model_error", "error_text": "503 Service temporarily unavailable"}),
		pf209Ev(pf209At(d, "09:10"), "candidate_switched", map[string]interface{}{
			"step": "build", "from": "glm", "to": "opus", "cause": "model_error"}),
		pf209Step(pf209At(d, "09:10"), "step_started", "build", "opus"),
		pf209Ev(pf209At(d, "09:20"), "awaiting_owner", map[string]interface{}{
			"step": "build", "question_id": "q1", "ask_at": pf209At(d, "09:20")}),
		pf209Ev(pf209At(d, "09:50"), "owner_message", map[string]interface{}{
			"step": "build", "ask_at": pf209At(d, "09:20"), "text": "ok"}),
		pf209Step(pf209At(d, "09:50"), "step_started", "build", "opus"),
		pf209Step(pf209At(d, "10:30"), "step_done", "build", ""),
		pf209Step(pf209At(d, "10:30"), "step_started", "review", "opus"),
		pf209Step(pf209At(d, "10:40"), "gate_verdict", "review", ""),
		pf209Ev(pf209At(d, "10:40"), "rewind", map[string]interface{}{"gate": "review", "target": "build"}),
		pf209Step(pf209At(d, "10:40"), "step_started", "build", "opus"),
		pf209Step(pf209At(d, "11:00"), "step_done", "build", ""),
		pf209Step(pf209At(d, "11:00"), "step_started", "review", "opus"),
		pf209Step(pf209At(d, "11:05"), "step_done", "review", ""),
		pf209Step(pf209At(d, "11:05"), "step_started", "sync", "flash"),
		pf209Step(pf209At(d, "11:06"), "step_done", "sync", ""),
		pf209Step(pf209At(d, "11:06"), "step_started", "verification", "flash"),
		pf209Step(pf209At(d, "11:14"), "step_done", "verification", ""),
		pf209Step(pf209At(d, "11:14"), "step_started", "publish", "flash"),
		pf209Step(pf209At(d, "11:16"), "step_done", "publish", ""),
		pf209Step(pf209At(d, "11:16"), "step_started", "merge", "flash"),
		pf209Step(pf209At(d, "11:20"), "step_done", "merge", ""),
		pf209Ev(pf209At(d, "11:20"), "terminal", map[string]interface{}{"status": "completed"}),
	}
	pf209WriteCapsule(t, caps, pf209Cap{
		name: "dv-b", lane: "develop", created: d + "T09:00:00Z",
		repos:   map[string][]int{"polyforge": {8, 2}},
		content: "## 分析\n\n### 1. 改动范围\n\n估算：polyforge 新增 2 个文件、修改 3 个文件、单元 2 个\n估算：i-have-adhd 新增 0 个文件、修改 0 个文件、单元 0 个\n",
		handoff: func(sha map[string][]string) string {
			p := sha["polyforge"]
			return pf209Section("build", 1, pf209At(d, "09:00"), pf209At(d, "10:30"), "80m0s", "done",
				[]pf209RepoChange{{"polyforge", p[0], p[1], 8}}) +
				pf209Section("review", 1, pf209At(d, "10:30"), pf209At(d, "10:40"), "10m0s", "fail", nil) +
				pf209Section("build", 2, pf209At(d, "10:40"), pf209At(d, "11:00"), "20m0s", "done",
					[]pf209RepoChange{{"polyforge", p[1], p[2], 10}}) +
				pf209Section("review", 2, pf209At(d, "11:00"), pf209At(d, "11:05"), "5m0s", "done", nil) +
				pf209Section("sync", 1, pf209At(d, "11:05"), pf209At(d, "11:06"), "1m0s", "done",
					[]pf209RepoChange{{"polyforge", p[2], p[2], 0}})
		},
		journal: []pf209Spawn{
			{pf209At(d, "09:00"), filepath.Join(root, "session-b1.jsonl")},
			{pf209At(d, "09:10"), filepath.Join(root, "session-b-missing.jsonl")},
			{pf209At(d, "09:50"), filepath.Join(root, "session-b2.jsonl")},
			{pf209At(d, "10:40"), filepath.Join(root, "session-b3.jsonl")},
		},
		sessions: map[string][]string{
			"session-b1.jsonl": pf209Session(
				pf209User(pf209At(d, "09:00:00")),
				pf209Assistant(pf209At(d, "09:00:30"), "c1", 900)),
			"session-b2.jsonl": pf209Session(
				pf209User(pf209At(d, "09:50:00")),
				pf209Assistant(pf209At(d, "09:50:30"), "c2", 600)),
			// the 10:40 build spawn's window has no assistant record: that
			// dispatch is unknown, dv-b still measures 1500/60 = 25.0.
			"session-b3.jsonl": pf209Session(pf209User(pf209At(d, "10:40:00"))),
		},
	})
	pf209WriteEvents(t, events, "dv-b", bEvents)

	// dv-c: 20 + 5 files across two repos, 119m build wall from three
	// build dispatches; a rate_limited death and a 401 exit_model_class
	// switch (model-class switch without the 503 text); no session.
	cEvents := []pf209Event{
		pf209Ev(pf209At(d, "08:00"), "job_started", nil),
		pf209Step(pf209At(d, "08:00"), "step_started", "build", "glm"),
		pf209Ev(pf209At(d, "08:30"), "step_failed", map[string]interface{}{
			"step": "build", "cause": "rate_limited"}),
		pf209Step(pf209At(d, "08:31"), "step_started", "build", "glm"),
		pf209Ev(pf209At(d, "09:00"), "step_failed", map[string]interface{}{
			"step": "build", "cause": "exit_model_class", "error_text": "401 Unauthorized"}),
		pf209Ev(pf209At(d, "09:00"), "candidate_switched", map[string]interface{}{
			"step": "build", "from": "glm", "to": "opus", "cause": "exit_model_class"}),
		pf209Step(pf209At(d, "09:00"), "step_started", "build", "opus"),
		pf209Step(pf209At(d, "10:00"), "step_done", "build", ""),
		pf209Step(pf209At(d, "10:00"), "step_started", "review", "opus"),
		pf209Step(pf209At(d, "10:10"), "step_done", "review", ""),
		pf209Step(pf209At(d, "10:10"), "step_started", "sync", "flash"),
		pf209Step(pf209At(d, "10:12"), "step_done", "sync", ""),
		pf209Step(pf209At(d, "10:12"), "step_started", "verification", "flash"),
		pf209Step(pf209At(d, "10:20"), "step_done", "verification", ""),
		pf209Step(pf209At(d, "10:20"), "step_started", "publish", "flash"),
		pf209Step(pf209At(d, "10:22"), "step_done", "publish", ""),
		pf209Step(pf209At(d, "10:22"), "step_started", "merge", "flash"),
		pf209Step(pf209At(d, "10:30"), "step_done", "merge", ""),
		pf209Ev(pf209At(d, "10:30"), "terminal", map[string]interface{}{"status": "completed"}),
	}
	pf209WriteCapsule(t, caps, pf209Cap{
		name: "dv-c", lane: "develop", created: d + "T08:00:00Z",
		repos:   map[string][]int{"polyforge": {20}, "other": {5}},
		content: "## 分析\n\n### 1. 改动范围\n\n估算：polyforge 新增约 10 个文件\n",
		handoff: func(sha map[string][]string) string {
			return pf209Section("build", 1, pf209At(d, "08:00"), pf209At(d, "10:00"), "119m0s", "done",
				[]pf209RepoChange{
					{"polyforge", sha["polyforge"][0], sha["polyforge"][1], 20},
					{"other", sha["other"][0], sha["other"][1], 5},
				}) +
				pf209Section("review", 1, pf209At(d, "10:00"), pf209At(d, "10:10"), "10m0s", "done", nil)
		},
	})
	pf209WriteEvents(t, events, "dv-c", cEvents)

	// dv-d: no event file (events missing), 4 files, first review done.
	pf209WriteCapsule(t, caps, pf209Cap{
		name: "dv-d", lane: "develop", created: d + "T07:00:00Z",
		repos:   map[string][]int{"polyforge": {4}},
		content: "## 背景\n\n没有分析估算行。\n",
		handoff: func(sha map[string][]string) string {
			return pf209Section("build", 1, pf209At(d, "07:00"), pf209At(d, "07:30"), "30m0s", "done",
				[]pf209RepoChange{{"polyforge", sha["polyforge"][0], sha["polyforge"][1], 4}}) +
				pf209Section("review", 1, pf209At(d, "07:30"), pf209At(d, "07:40"), "10m0s", "done", nil)
		},
	})

	// dv-f: the "answer then NO redispatch, runs to step_done" shape
	// (owner ruling 5); size 0 (its own line), no work-item.json (estimate
	// unknown: counted and named), no session (throughput unknown). The
	// build dispatch is 06:00 -> 07:00 (60m) minus the 06:10 -> 06:30 owner
	// wait = 40m; wall 75m, effective 55m.
	fEvents := []pf209Event{
		pf209Ev(pf209At(d, "06:00"), "job_started", nil),
		pf209Step(pf209At(d, "06:00"), "step_started", "build", "glm"),
		pf209Ev(pf209At(d, "06:10"), "awaiting_owner", map[string]interface{}{
			"step": "build", "question_id": "qf", "ask_at": pf209At(d, "06:10")}),
		pf209Ev(pf209At(d, "06:30"), "owner_message", map[string]interface{}{
			"step": "build", "ask_at": pf209At(d, "06:10"), "text": "ok"}),
		pf209Step(pf209At(d, "07:00"), "step_done", "build", ""),
		pf209Step(pf209At(d, "07:00"), "step_started", "review", "opus"),
		pf209Step(pf209At(d, "07:05"), "step_done", "review", ""),
		pf209Step(pf209At(d, "07:05"), "step_started", "sync", "flash"),
		pf209Step(pf209At(d, "07:06"), "step_done", "sync", ""),
		pf209Step(pf209At(d, "07:06"), "step_started", "verification", "flash"),
		pf209Step(pf209At(d, "07:10"), "step_done", "verification", ""),
		pf209Step(pf209At(d, "07:10"), "step_started", "publish", "flash"),
		pf209Step(pf209At(d, "07:12"), "step_done", "publish", ""),
		pf209Step(pf209At(d, "07:12"), "step_started", "merge", "flash"),
		pf209Step(pf209At(d, "07:15"), "step_done", "merge", ""),
		pf209Ev(pf209At(d, "07:15"), "terminal", map[string]interface{}{"status": "completed"}),
	}
	pf209WriteCapsule(t, caps, pf209Cap{
		name: "dv-f", lane: "develop", created: d + "T06:00:00Z",
		repos: map[string][]int{"polyforge": {0}},
		handoff: func(sha map[string][]string) string {
			return pf209Section("build", 1, pf209At(d, "06:00"), pf209At(d, "07:00"), "40m0s", "done",
				[]pf209RepoChange{{"polyforge", sha["polyforge"][0], sha["polyforge"][1], 0}}) +
				pf209Section("review", 1, pf209At(d, "07:00"), pf209At(d, "07:05"), "5m0s", "done", nil)
		},
	})
	pf209WriteEvents(t, events, "dv-f", fEvents)

	// dv-e: events but no completed terminal — excluded and named.
	pf209WriteCapsule(t, caps, pf209Cap{
		name: "dv-e", lane: "develop", created: d + "T07:00:00Z",
		repos: map[string][]int{"polyforge": {1}},
	})
	pf209WriteEvents(t, events, "dv-e", []pf209Event{
		pf209Ev(pf209At(d, "07:00"), "job_started", nil),
		pf209Step(pf209At(d, "07:00"), "step_started", "build", "glm"),
	})
	return caps, events
}

func pf209Run(t *testing.T, caps string, args ...string) (string, int) {
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

func pf209CSV(t *testing.T, path string) map[string]map[string]string {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	recs, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) == 0 {
		t.Fatal("empty CSV")
	}
	rows := map[string]map[string]string{}
	for _, r := range recs[1:] {
		m := map[string]string{}
		for i, k := range recs[0] {
			m[k] = r[i]
		}
		rows[m["capsule"]] = m
	}
	return rows
}

// TestPF209DeliveryTiers pins AC1: the delivery block's hand-derived
// values on the ticket's fixture, its CSV columns, and the counterexamples
// (owner wait deducted, only 503 text counted as 503, an absent session is
// unknown, the size is taken before sync).
func TestPF209DeliveryTiers(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("python3 is a pf doctor prerequisite and runs this tool: %v", err)
	}
	caps, events := pf209Fixture(t)
	csvPath := filepath.Join(t.TempDir(), "runs.csv")
	text, code := pf209Run(t, caps, "--events", events, "--csv", csvPath)
	if code != 0 {
		t.Fatalf("review-firstpass --events: exit %d\n%s", code, text)
	}
	for _, want := range []string{
		"== delivery metrics (events: events) ==",
		"runs: develop 7, completed 5, events missing 1, not completed (excluded): dv-e (1)",
		"<=7: n 3 (wall known 2), wall median 47.0m p90 60.0m, per-file median 10.7m pooled 10.4m, rework 0.0% (0.0m of 94.0m), first-pass 3/3",
		"8-22: n 1 (wall known 1), wall median 140.0m p90 140.0m, per-file median 11.0m pooled 11.0m, rework 22.7% (25.0m of 110.0m), first-pass 0/1",
		">22: n 1 (wall known 1), wall median 150.0m p90 150.0m, per-file median 6.0m pooled 6.0m, rework 0.0% (0.0m of 150.0m), first-pass 1/1",
		"size 0: n 1 (wall known 1), wall median 75.0m p90 75.0m, per-file unknown, rework 0.0% (0.0m of 55.0m), first-pass 1/1",
		"total: n 6 (wall known 5), wall median 75.0m p90 150.0m, per-file median 10.5m pooled 8.0m, rework 6.1% (25.0m of 409.0m), first-pass 5/6",
		"dispatches by model: flash 20 dispatches, 0 failed (0.0%); glm 6 dispatches, 3 failed (50.0%); opus 10 dispatches, 0 failed (0.0%)",
		"all 36 dispatches, 3 failed (8.3%)",
		"candidate switches (model-class): 2, of which 503 text: 1",
		"estimate vs approved: 3 with estimate, ratio median 2.00 min 1.00 max 3.00; no estimate 2: dv-c, dv-d; estimate unknown 1: dv-f; approved unknown 0",
		"dv-b: size 10 (8-22), wall 140.0m, owner wait 30.0m, build wall 80.0m, build rounds 2, first review FAIL, review rejections 1, verification rejections 0, rework 25.0m, failed dispatches 1, output 25.0 tok/s, estimate 5 (ratio 2.00)",
		"dv-a: size 3 (<=7), wall 34.0m, owner wait 0.0m, build wall 20.0m, build rounds 1, first review PASS, review rejections 0, verification rejections 0, rework 0.0m, failed dispatches 0, output 30.0 tok/s, estimate 3 (ratio 1.00)",
		"dv-c: size 25 (>22), wall 150.0m, owner wait 0.0m, build wall 119.0m, build rounds 1, first review PASS, review rejections 0, verification rejections 0, rework 0.0m, failed dispatches 2, output unknown tok/s, estimate none",
		"dv-d: size 4 (<=7), wall unknown, owner wait unknown, build wall unknown, build rounds 1, first review PASS, review rejections unknown, verification rejections unknown, rework unknown, failed dispatches unknown, output unknown tok/s, estimate none",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the delivery block must carry %q:\n%s", want, text)
		}
	}
	// The counterexamples: subtracting nothing would print b's 14.0m
	// per-file and 17.9% rework; counting the 401 as 503 would print
	// `503 text: 2`; taking the size after sync would print 8.
	for _, bad := range []string{"per-file median 14.0m", "rework 17.9%", "of which 503 text: 2"} {
		if strings.Contains(text, bad) {
			t.Errorf("the delivery block must not carry %q:\n%s", bad, text)
		}
	}
	if strings.Contains(text, "dv-b: size 8 ") {
		t.Errorf("dv-b's approved size is the last build before sync (10), not the first round's 8:\n%s", text)
	}
	if !strings.Contains(text, "dv-f: size 0 (0), wall 75.0m, owner wait 20.0m, build wall 40.0m, build rounds 1, first review PASS, review rejections 0, verification rejections 0, rework 0.0m, failed dispatches 0, output unknown tok/s, estimate unknown") {
		t.Errorf("dv-f (answer, no redispatch, then step_done; size 0; estimate unknown) is missing:\n%s", text)
	}
	// The no-redispatch shape: ending the dispatch at awaiting_owner
	// would leave dv-f's build wall at 10.0m instead of 40.0m.
	if strings.Contains(text, "dv-f: size 0 (0), wall 75.0m, owner wait 20.0m, build wall 10.0m") {
		t.Errorf("an answered dispatch that never re-dispatches must end at step_done, not at awaiting_owner:\n%s", text)
	}
	if strings.Contains(text, "dv-c: size 25 (>22), wall 150.0m, owner wait 0.0m, build wall 119.0m, build rounds 1, first review PASS, review rejections 0, verification rejections 0, rework 0.0m, failed dispatches 2, output 0.0 tok/s") {
		t.Errorf("a run with no session record must print output unknown, never a number:\n%s", text)
	}

	rows := pf209CSV(t, csvPath)
	for name, want := range map[string]string{
		"dv-b": "140.0 30.0 110.0 25.0 10 2 1 0 1 80.0 5 2.00",
		"dv-c": "150.0 0.0 150.0 0.0 25 1 0 0 2 unknown none unknown",
		"dv-d": "unknown unknown unknown unknown 4 1 PASS unknown unknown none unknown",
		"dv-f": "75.0 20.0 55.0 0.0 0 1 PASS unknown 0 unknown unknown",
	} {
		r, ok := rows[name]
		if !ok {
			t.Fatalf("run %s missing from the CSV (%v)", name, rows)
		}
		var got string
		if name == "dv-b" {
			got = strings.Join([]string{r["wall_min"], r["owner_wait_min"], r["effective_min"], r["rework_min"],
				r["approved_files"], r["build_rounds"], r["review_rejections"], r["verification_rejections"],
				r["failed_dispatches"], r["build_wall_min"], r["estimate_files"], r["estimate_ratio"]}, " ")
		} else {
			got = strings.Join([]string{r["wall_min"], r["owner_wait_min"], r["effective_min"], r["rework_min"],
				r["approved_files"], r["build_rounds"], r["first_review"], r["out_tok_s"],
				r["failed_dispatches"], r["estimate_files"], r["estimate_ratio"]}, " ")
			if name == "dv-c" {
				got = strings.Join([]string{r["wall_min"], r["owner_wait_min"], r["effective_min"], r["rework_min"],
					r["approved_files"], r["build_rounds"], r["review_rejections"], r["verification_rejections"],
					r["failed_dispatches"], r["out_tok_s"], r["estimate_files"], r["estimate_ratio"]}, " ")
			}
		}
		if got != want {
			t.Errorf("run %s CSV = %q, want %q", name, got, want)
		}
	}

	// regression: without --events the report is the pre-delivery one.
	plain, code := pf209Run(t, caps)
	if code != 0 {
		t.Fatalf("review-firstpass plain: exit %d\n%s", code, plain)
	}
	if strings.Contains(plain, "delivery metrics") {
		t.Errorf("without --events the report must not grow the delivery block:\n%s", plain)
	}
}

func pf209Manifest(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "cohort.json")
	pf209WriteFile(t, p, body)
	return p
}

// TestPF209DeliveryArms pins AC2: an `arms` manifest prints the same
// metrics per trial arm (alphabetical arm order, manifest row order),
// keeps the missing names in the honesty line, refuses a malformed arms
// value with the named message, and without --events keeps the size and
// first review while the event summary lines say n/a.
func TestPF209DeliveryArms(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("python3 is a pf doctor prerequisite and runs this tool: %v", err)
	}
	caps, events := pf209Fixture(t)
	manifest := pf209Manifest(t, `{"version":"t","arms":{"flash":["dv-a","dv-b","dv-e","gone-x"],"glm":["dv-c","dv-a2","dv-d"]}}`)
	text, code := pf209Run(t, caps, "--events", events, "--cohort", manifest)
	if code != 0 {
		t.Fatalf("review-firstpass --cohort --events: exit %d\n%s", code, text)
	}
	for _, want := range []string{
		"== arm flash ==",
		"runs: develop 3, completed 2, events missing 0, not completed (excluded): dv-e (1)",
		"<=7: n 1 (wall known 1), wall median 34.0m p90 34.0m, per-file median 11.3m pooled 11.3m, rework 0.0% (0.0m of 34.0m), first-pass 1/1",
		">22: n 0",
		"total: n 2 (wall known 2), wall median 87.0m p90 140.0m, per-file median 11.2m pooled 11.1m, rework 17.4% (25.0m of 144.0m), first-pass 1/2",
		"dispatches by model: flash 8 dispatches, 0 failed (0.0%); glm 2 dispatches, 1 failed (50.0%); opus 6 dispatches, 0 failed (0.0%)",
		"all 16 dispatches, 1 failed (6.2%)",
		"candidate switches (model-class): 1, of which 503 text: 1",
		"dv-b: size 10 (8-22), wall 140.0m, owner wait 30.0m, build wall 80.0m, build rounds 2, first review FAIL, review rejections 1, verification rejections 0, rework 25.0m, failed dispatches 1, output 25.0 tok/s, estimate 5 (ratio 2.00)",
		"dv-a: size 3 (<=7), wall 34.0m, owner wait 0.0m, build wall 20.0m, build rounds 1, first review PASS, review rejections 0, verification rejections 0, rework 0.0m, failed dispatches 0, output 30.0 tok/s, estimate 3 (ratio 1.00)",
		"missing from the input (counted, excluded): gone-x (1)",
		"== arm glm ==",
		"runs: develop 3, completed 2, events missing 1",
		"<=7: n 2 (wall known 1), wall median 60.0m p90 60.0m, per-file median 10.0m pooled 10.0m, rework 0.0% (0.0m of 60.0m), first-pass 2/2",
		"total: n 3 (wall known 2), wall median 105.0m p90 150.0m, per-file median 8.0m pooled 6.8m, rework 0.0% (0.0m of 210.0m), first-pass 3/3",
		"dispatches by model: flash 8 dispatches, 0 failed (0.0%); glm 3 dispatches, 2 failed (66.7%); opus 3 dispatches, 0 failed (0.0%)",
		"candidate switches (model-class): 1, of which 503 text: 0",
		"dv-c: size 25 (>22), wall 150.0m, owner wait 0.0m, build wall 119.0m, build rounds 1, first review PASS, review rejections 0, verification rejections 0, rework 0.0m, failed dispatches 2, output unknown tok/s, estimate none",
		"dv-d: size 4 (<=7), wall unknown, owner wait unknown, build wall unknown, build rounds 1, first review PASS, review rejections unknown, verification rejections unknown, rework unknown, failed dispatches unknown, output unknown tok/s, estimate none",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the arm block must carry %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "== arm flash ==") > strings.Index(text, "== arm glm ==") {
		t.Errorf("the arms are printed in alphabetical order:\n%s", text)
	}

	// malformed arms: the named message, exit 2.
	bad := pf209Manifest(t, `{"arms":{"flash":"dv-a"}}`)
	text, code = pf209Run(t, caps, "--cohort", bad)
	if code != 2 || !strings.Contains(text, "the cohort manifest’s arms must be an object of lists of capsule dir names") {
		t.Errorf("a malformed arms value: exit %d, output %q; want exit 2 with the named message", code, text)
	}

	// arms without --events: size and first review stay, event fields are
	// unknown, the three event summary lines say n/a.
	noev, code := pf209Run(t, caps, "--cohort", manifest)
	if code != 0 {
		t.Fatalf("arms without --events: exit %d\n%s", code, noev)
	}
	for _, want := range []string{
		"== arm flash ==",
		"n/a (no events dir)",
		"dv-a: size 3 (<=7), wall unknown, owner wait unknown, build wall unknown, build rounds 1, first review PASS",
	} {
		if !strings.Contains(noev, want) {
			t.Errorf("arms without --events must carry %q:\n%s", want, noev)
		}
	}
}

// TestPF209EstimateParse pins AC3 on polyforge#213's shared sample file:
// each row is read inside `## 分析` -> `### 1.`, ok rows give N+M and
// reject rows `none`; the sample file is the only sample set. The extra
// cases carry the ticket's e4–e8 table under the owner's per-repository
// ruling (a malformed line voids its own repository only).
func TestPF209EstimateParse(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Fatalf("python3 is a pf doctor prerequisite and runs this tool: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(v1Root(t), "internal", "contracttest", "testdata", "analysis_estimate_lines.tsv"))
	if err != nil {
		t.Fatalf("polyforge#213's shared estimate-line samples must exist: %v", err)
	}
	caps := t.TempDir()
	want := map[string]string{}
	i := 0
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		cols := strings.Split(line, "\t")
		if len(cols) != 3 {
			t.Fatalf("sample row %q must have 3 columns", line)
		}
		i++
		name := fmt.Sprintf("est%02d", i)
		content := "## 分析\n\n### 1. 改动范围\n\n" + cols[0] + "\n"
		pf209WriteCapsule(t, caps, pf209Cap{name: name, lane: "develop", created: "2026-11-10T00:00:00Z", content: content})
		if cols[1] == "ok" {
			// N is the first capture, M the second — N+M is the estimate.
			parts := strings.Split(cols[2], ",")
			if len(parts) != 3 {
				t.Fatalf("sample %q: third column must be N,M,K", line)
			}
			var a, b int
			fmt.Sscanf(parts[0], "%d", &a)
			fmt.Sscanf(parts[1], "%d", &b)
			want[name] = fmt.Sprint(a + b)
		} else {
			want[name] = "none"
		}
	}
	// e4: a valid line outside `## 分析` is ignored.
	pf209WriteCapsule(t, caps, pf209Cap{name: "e4", lane: "develop",
		content: "## 背景\n\n估算：polyforge 新增 3 个文件、修改 4 个文件、单元 2 个\n\n## 分析\n\n### 1. 改动范围\n\n无\n"})
	want["e4"] = "none"
	// e5: two valid lines for one repository are ambiguous.
	pf209WriteCapsule(t, caps, pf209Cap{name: "e5", lane: "develop",
		content: "## 分析\n\n### 1. 改动范围\n\n估算：polyforge 新增 1 个文件、修改 1 个文件、单元 1 个\n估算：polyforge 新增 2 个文件、修改 2 个文件、单元 2 个\n"})
	want["e5"] = "none"
	// e6: the malformed polyforge line voids polyforge only; other's
	// valid line still counts (the owner's per-repository ruling).
	pf209WriteCapsule(t, caps, pf209Cap{name: "e6", lane: "develop",
		content: "## 分析\n\n### 1. 改动范围\n\n估算：polyforge 新增 1 个文件、修改 2 个文件、单元 1 个\n估算：other 新增 3 个文件、修改 4 个文件、单元 1 个\n估算：polyforge 大约 10 个\n"})
	want["e6"] = "7"
	// e7: no work-item.json → unknown.
	pf209WriteCapsule(t, caps, pf209Cap{name: "e7", lane: "develop"})
	want["e7"] = "unknown"
	// e8: a zero estimate is 0, with no ratio.
	pf209WriteCapsule(t, caps, pf209Cap{name: "e8", lane: "develop",
		content: "## 分析\n\n### 1. 改动范围\n\n估算：polyforge 新增 0 个文件、修改 0 个文件、单元 0 个\n"})
	want["e8"] = "0"
	// e9: a valid line under `### 2.` is outside the judged subsection.
	pf209WriteCapsule(t, caps, pf209Cap{name: "e9", lane: "develop",
		content: "## 分析\n\n### 1. 改动范围\n\n无\n\n### 2. 测试\n\n估算：polyforge 新增 5 个文件、修改 5 个文件、单元 1 个\n"})
	want["e9"] = "none"

	events := filepath.Join(t.TempDir(), "events")
	if err := os.MkdirAll(events, 0o755); err != nil {
		t.Fatal(err)
	}
	csvPath := filepath.Join(t.TempDir(), "runs.csv")
	text, code := pf209Run(t, caps, "--events", events, "--csv", csvPath)
	if code != 0 {
		t.Fatalf("review-firstpass estimate fixtures: exit %d\n%s", code, text)
	}
	rows := pf209CSV(t, csvPath)
	for name, w := range want {
		r, ok := rows[name]
		if !ok {
			t.Fatalf("estimate fixture %s missing from the CSV", name)
		}
		if r["estimate_files"] != w {
			t.Errorf("%s: estimate_files=%q, want %q", name, r["estimate_files"], w)
		}
	}
	if rows["e8"]["estimate_ratio"] != "unknown" {
		t.Errorf("a zero estimate has no ratio, got %q", rows["e8"]["estimate_ratio"])
	}
}
