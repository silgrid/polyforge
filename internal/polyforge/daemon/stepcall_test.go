package daemon

// stepcall_test.go (workflow contract 4 C1): `pf step` as
// the daemon serves it — every call recorded on the capsule's call ledger
// before it runs (a call that failed is still there; a ledger that cannot
// be written runs nothing), a caller that is no step refused with nothing
// recorded; `pf step handoff`'s six statuses each appending one section
// whose header the daemon stamps, and every on-the-spot refusal leaving
// handoff.md as it was; --per-owner checked against the record of this
// step's round and its message copied verbatim; the ledger, not the
// document, as the daemon's record; the hub reads proxied, the writes
// only recorded pending (no aihub write), outside the whitelist refused.

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// c4Body answers a hand-off body carrying the template's seven sections
// in the fixture's pinned language (zh-CN), plus the extra
// headings given.
func c4Body(extra ...string) string {
	var b strings.Builder
	for _, s := range append(append([]string{}, stepcall.BodySectionsOf(stepcall.LangZhCN)...), extra...) {
		b.WriteString("## " + s + "\n\n" + s + " 的内容。\n\n")
	}
	return b.String()
}

// c4Call posts one `pf step` call and answers status + body.
func c4Call(t *testing.T, td *testDaemon, verb string, args any) (int, string) {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"verb": verb, "args": args})
	if err != nil {
		t.Fatal(err)
	}
	return stepDo(t, td.sock, "POST", "/step/call", string(raw))
}

// c4Fixture is a live work item whose capsule carries a develop-shaped
// lane snapshot (c4Lane), an execution record (build's round started at
// the first commit, every other step's at the second) and one task repo
// with two commits (the materialization at the first); as(step, spawn)
// points every /step/* caller at that dispatch.
type c4Fixture struct {
	td     *testDaemon
	wi     string
	capDir string
	c1, c2 string
}

func (f *c4Fixture) as(step string, spawn int) {
	f.td.stepOriginOf = func(*http.Request) (stepJob, string) {
		return stepJob{wi: f.wi, step: step, attempt: spawn}, ""
	}
	f.journalLanguage(step, spawn, stepcall.LangZhCN)
}

// journalLanguage appends the dispatch (step, spawn)'s spawn-journal line
// recording the agent language it was told: the
// fixture's dispatches are pinned zh-CN, so their stamps are Chinese.
func (f *c4Fixture) journalLanguage(step string, spawn int, lang string) {
	path := filepath.Join(f.capDir, capsule.DirRuntimeState, capsule.SpawnJournalFile)
	line, _ := json.Marshal(capsule.SpawnJournalEntry{WorkItem: f.wi, StepID: step, SpawnN: spawn, Language: lang})
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		panic(err)
	}
	fh, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		panic(err)
	}
	defer fh.Close()
	if _, err := fh.Write(append(line, '\n')); err != nil {
		panic(err)
	}
}

// handoffDoc reads handoff.md ("" when absent).
func (f *c4Fixture) handoffDoc(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(stepcall.HandoffPath(f.capDir))
	if os.IsNotExist(err) {
		return ""
	}
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// ledger reads the call ledger.
func (f *c4Fixture) ledger(t *testing.T) []stepcall.Line {
	t.Helper()
	lines, err := stepcall.Read(f.capDir)
	if err != nil {
		t.Fatal(err)
	}
	return lines
}

// writeExec writes the execution record's steps.
func (f *c4Fixture) writeExec(t *testing.T, steps map[string]*capsule.Step) {
	t.Helper()
	raw, err := json.Marshal(capsule.Execution{WorkItemID: f.wi, WorkflowStatus: capsule.WorkflowStatusRunning, Steps: steps})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.capDir, capsule.ExecutionJSON), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// ownerSays appends one owner message to step (kind message) at the stamp.
func (f *c4Fixture) ownerSays(t *testing.T, step, at, text string) {
	t.Helper()
	if err := capsule.AppendAsk(f.capDir, capsule.AskLine{At: at, Step: step, Spawn: 1, Kind: capsule.AskKindMessage, Text: text, By: "owner"}); err != nil {
		t.Fatal(err)
	}
}

func c4Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newC4Fixture(t *testing.T, wi string) *c4Fixture {
	t.Helper()
	td := newTestDaemon(t, nil)
	td.submitClaim(t, wi)
	ji, _ := td.rt.Info(wi)
	f := &c4Fixture{td: td, wi: wi, capDir: ji.CapsuleDir}
	// a develop-shaped lane snapshot, as a claim materializes one
	writeC4Lane(t, filepath.Join(f.capDir, capsule.DirWorkflows))
	repo := filepath.Join(f.capDir, capsule.DirRepos, "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	c4Git(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c4Git(t, repo, "add", ".")
	c4Git(t, repo, "commit", "-q", "-m", "one")
	f.c1 = c4Git(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "b.txt"), []byte("two\nthree\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c4Git(t, repo, "add", ".")
	c4Git(t, repo, "commit", "-q", "-m", "two")
	f.c2 = c4Git(t, repo, "rev-parse", "HEAD")
	idn := capsule.Identity{WorkItemID: wi, Project: "proj", BundleName: "develop", Repos: map[string]string{"app": f.c1}}
	raw, _ := json.Marshal(idn)
	if err := os.WriteFile(filepath.Join(f.capDir, capsule.CapsuleJSON), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	start := time.Now().Add(-90 * time.Second).UTC().Format(time.RFC3339Nano)
	steps := map[string]*capsule.Step{}
	for _, s := range []string{"build", "review", "sync", "verification"} {
		heads := map[string]string{"app": f.c2}
		if s == "build" {
			heads = map[string]string{"app": f.c1}
		}
		steps[s] = &capsule.Step{Status: capsule.StepInProgress, Round: 1, Candidate: "pi:test/model-" + s, StepStartedAt: start,
			StartHeads: &capsule.StartHeads{Round: 1, Heads: heads}}
	}
	f.writeExec(t, steps)
	return f
}

// c4Lane is the fixture lane the step-call tests run on: develop's step
// order and gate shape (review a gate rewinding to build, verification a
// waivable one) under workflow contract 4, merge declaring the landing
// record its round registers before done.
const c4Lane = `max_rewinds: 3
steps:
  - id: build
  - id: review
    gate: true
    on_fail: { target: build }
  - id: sync
  - id: verification
    gate: true
    waivable: true
    on_fail: { target: build }
  - id: publish
  - id: merge
    resources: { locks: [merge] }
    wrap_attrs: [integration]
`

// writeC4Lane writes the fixture lane "develop" (c4Lane, a definition per
// step) under workflowsDir.
func writeC4Lane(t *testing.T, workflowsDir string) {
	t.Helper()
	workflowtest.WriteLane(t, workflowsDir, "develop", c4Lane)
}

// copyTree copies the regular files under src to dst.
func copyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(target, raw, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStepCallOutsideAStepIsRefusedAndUnrecorded(t *testing.T) {
	f := newC4Fixture(t, "wi_c4a")
	f.td.stepOriginOf = nil // the real resolver: this test process is no step
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 403 || !strings.Contains(body, "serves step agents only") {
		t.Fatalf("a non-step call = %d %s", code, body)
	}
	if lines := f.ledger(t); len(lines) != 0 || f.handoffDoc(t) != "" {
		t.Fatalf("a refused non-step call left a record: %+v / %q", lines, f.handoffDoc(t))
	}
}

// TestStepCallRecordedBeforeItRuns: a call whose execution fails is on
// the ledger (its call line, then its failed result naming it); a ledger
// that cannot be written runs nothing.
func TestStepCallRecordedBeforeItRuns(t *testing.T) {
	f := newC4Fixture(t, "wi_c4b")
	f.as("build", 1)
	var mu sync.Mutex
	hubCalls := 0
	f.td.hub.mu.Lock()
	f.td.hub.memDo = func(method, path string, body, out any) error {
		mu.Lock()
		hubCalls++
		mu.Unlock()
		return &os.PathError{Op: "dial", Path: "aihub", Err: os.ErrDeadlineExceeded}
	}
	f.td.hub.mu.Unlock()
	if code, body := c4Call(t, f.td, "recall", map[string]any{"q": "sqlite busy"}); code < 500 {
		t.Fatalf("a recall the hub fails = %d %s", code, body)
	}
	lines := f.ledger(t)
	if len(lines) != 2 || lines[0].Kind != stepcall.KindCall || lines[0].Verb != "recall" || lines[0].Step != "build" || lines[0].Spawn != 1 ||
		!strings.Contains(string(lines[0].Args), "sqlite busy") {
		t.Fatalf("the call line: %+v", lines)
	}
	if r := lines[1]; r.Kind != stepcall.KindResult || r.Call != lines[0].Seq || r.Outcome != stepcall.OutcomeFailed || r.Error == "" {
		t.Fatalf("the failed call's result line: %+v", r)
	}
	// a refused call is recorded too (refused)
	if code, _ := c4Call(t, f.td, "no-such-verb", map[string]any{}); code != 400 {
		t.Fatalf("an unknown verb = %d", code)
	}
	if lines := f.ledger(t); len(lines) != 4 || lines[2].Verb != "no-such-verb" || lines[3].Outcome != stepcall.OutcomeRefused || lines[3].Code != "DAEMON-400" {
		t.Fatalf("the refused call's lines: %+v", lines[2:])
	}
	// the ledger cannot be written: nothing runs (the hub is not called)
	if err := os.Rename(stepcall.Path(f.capDir), stepcall.Path(f.capDir)+".bak"); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(stepcall.Path(f.capDir), 0o755); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	before := hubCalls
	mu.Unlock()
	if code, body := c4Call(t, f.td, "recall", map[string]any{"q": "x"}); code != 500 || !strings.Contains(body, "was not run") {
		t.Fatalf("an unrecordable call = %d %s", code, body)
	}
	mu.Lock()
	defer mu.Unlock()
	if hubCalls != before {
		t.Fatalf("an unrecorded call reached aihub (%d → %d)", before, hubCalls)
	}
}

// TestStepHandoffEachStatusAppendsAStampedSection: each of the six
// statuses, legal, appends one section whose header the daemon stamped —
// step, round, spawn, times, model, status, the repos' start and end
// commits with their diff stat, the evidence, the owner messages — and the
// ledger records the hand-off.
func TestStepHandoffEachStatusAppendsAStampedSection(t *testing.T) {
	f := newC4Fixture(t, "wi_c4c")
	if err := os.WriteFile(filepath.Join(f.capDir, capsule.DirArtifacts, "test-result.json"), []byte(`{}`), 0o644); err != nil {
		t.Fatal(err)
	}
	f.ownerSays(t, "verification", "2026-11-11T10:00:00.000000001Z", "这次 FAIL 不算，\n直接放行。")
	f.ownerSays(t, "build", "2026-11-11T10:00:00.000000002Z", "不做了。")
	type call struct {
		step  string
		spawn int
		args  map[string]any
		want  []string
	}
	for i, c := range []call{
		{"build", 1, map[string]any{"status": "done", "body": c4Body(), "evidence": []string{"artifacts/test-result.json"}},
			[]string{"## build · 第 1 轮 · 派发 1 · done", "| 模型 | pi:test/model-build |", "| 证据 | `artifacts/test-result.json` |",
				"`app`: `" + f.c1 + "` → `" + f.c2 + "`（1 file changed, 2 insertions(+)）", "| 耗时 | 1m", "不做了。"}},
		{"sync", 2, map[string]any{"status": "blocked", "body": c4Body(), "reason": "the merge needs the owner"},
			// the start is the previous hand-off's end: no new commit since
			[]string{"## sync · 第 1 轮 · 派发 2 · blocked", "| 原因 | the merge needs the owner |", "- （无）", "`app`: `" + f.c2 + "` → `" + f.c2 + "`（无新提交）"}},
		{"review", 3, map[string]any{"status": "fail", "body": c4Body()},
			[]string{"## review · 第 1 轮 · 派发 3 · fail"}},
		{"verification", 4, map[string]any{"status": "waive", "body": c4Body(), "per_owner": "2026-11-11T10:00:00.000000001Z"},
			[]string{"## verification · 第 1 轮 · 派发 4 · waive", "**`--per-owner` 引用的 owner 消息**", "~~~\n这次 FAIL 不算，\n直接放行。\n~~~"}},
		{"build", 5, map[string]any{"status": "cancel", "body": c4Body(stepcall.ReentrySectionOf(stepcall.LangZhCN)), "per_owner": "2026-11-11T10:00:00.000000002Z"},
			[]string{"## build · 第 1 轮 · 派发 5 · cancel", "~~~\n不做了。\n~~~"}},
		{"verification", 6, map[string]any{"status": "rewind", "to": "build", "body": c4Body(stepcall.ReentrySectionOf(stepcall.LangZhCN)), "per_owner": "2026-11-11T10:00:00.000000001Z"},
			[]string{"## verification · 第 1 轮 · 派发 6 · rewind → build"}},
	} {
		f.as(c.step, c.spawn)
		before := f.handoffDoc(t)
		code, body := c4Call(t, f.td, "handoff", c.args)
		if code != 200 || !strings.Contains(body, `"status":"`+c.args["status"].(string)+`"`) {
			t.Fatalf("%d %s: %d %s", i, c.args["status"], code, body)
		}
		doc := f.handoffDoc(t)
		if !strings.HasPrefix(doc, before) {
			t.Fatalf("%s: the document was rewritten, not appended to", c.args["status"])
		}
		section := strings.TrimPrefix(doc, before)
		for _, w := range append(c.want, "| 步骤 | "+c.step+" |", "| 轮次 | 1 |", "| 开始 | ", "| 结束 | ", "由 daemon 盖章", "### 正文（step 撰写）", "## 给下游与未决") {
			if !strings.Contains(section, w) {
				t.Fatalf("%s: the section lacks %q:\n%s", c.args["status"], w, section)
			}
		}
		rec := stepcall.HandoffOf(f.ledger(t), c.step, int64(c.spawn))
		if rec == nil || rec.Handoff.Status != c.args["status"] || rec.Handoff.Heads["app"] != f.c2 {
			t.Fatalf("%s: the ledger's record: %+v", c.args["status"], rec)
		}
	}
	if n := strings.Count(f.handoffDoc(t), "由 daemon 盖章"); n != 6 {
		t.Fatalf("six hand-offs, %d sections", n)
	}
}

// TestStepHandoffRefusalsLeaveTheDocument: every on-the-spot refusal exits
// non-2xx and leaves handoff.md byte for byte.
func TestStepHandoffRefusalsLeaveTheDocument(t *testing.T) {
	f := newC4Fixture(t, "wi_c4d")
	f.as("build", 1)
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 200 {
		t.Fatalf("the first hand-off = %d %s", code, body)
	}
	long := c4Body() + strings.Repeat("字", stepcall.MaxBodyChars)
	missing := strings.Replace(c4Body(), "## 验证", "## 检查", 1)
	for _, c := range []struct {
		name, step string
		spawn      int
		args       map[string]any
		code       int
		want       string
	}{
		{"second call of the dispatch", "build", 1, map[string]any{"status": "done", "body": c4Body()}, 409, "hands off once"},
		{"fail from a non-gate", "sync", 2, map[string]any{"status": "fail", "body": c4Body()}, 400, "is no gate"},
		{"waive from a non-waivable gate", "review", 2, map[string]any{"status": "waive", "body": c4Body(), "per_owner": "x"}, 400, "not one"},
		{"a section missing", "sync", 2, map[string]any{"status": "done", "body": missing}, 400, "验证"},
		{"a re-run without 回应上一轮", "build", 2, map[string]any{"status": "done", "body": c4Body()}, 400, stepcall.ReentrySectionOf(stepcall.LangZhCN)},
		{"a body over 3000 characters", "sync", 2, map[string]any{"status": "done", "body": long}, 400, "over the 3000 bound"},
		{"evidence that does not exist", "sync", 2, map[string]any{"status": "done", "body": c4Body(), "evidence": []string{"artifacts/nope.json"}}, 400, "no such file"},
		{"evidence outside the capsule", "sync", 2, map[string]any{"status": "done", "body": c4Body(), "evidence": []string{"/etc/hostname"}}, 400, ""},
		{"an unknown status", "sync", 2, map[string]any{"status": "pass", "body": c4Body()}, 400, "none of"},
		{"rewind without --to", "sync", 2, map[string]any{"status": "rewind", "body": c4Body(), "per_owner": "x"}, 400, "needs --to"},
		{"rewind to a later step", "build", 2, map[string]any{"status": "rewind", "to": "merge", "body": c4Body(stepcall.ReentrySectionOf(stepcall.LangZhCN)), "per_owner": "x"}, 400, "at or before"},
	} {
		f.as(c.step, c.spawn)
		before := f.handoffDoc(t)
		code, body := c4Call(t, f.td, "handoff", c.args)
		if code != c.code || !strings.Contains(body, c.want) {
			t.Fatalf("%s: %d %s, want %d %q", c.name, code, body, c.code, c.want)
		}
		if f.handoffDoc(t) != before {
			t.Fatalf("%s: the refused hand-off changed handoff.md", c.name)
		}
	}
	if n := len(stepcall.StepHandoffs(f.ledger(t), "sync")); n != 0 {
		t.Fatalf("a refused hand-off was recorded as accepted: %d", n)
	}
}

// TestStepHandoffPerOwnerNamesAMessageOfThisRound: cancel, waive and
// rewind each need --per-owner, and it must name an owner message of this
// step in its current round (another step's, an earlier round's, an ask's
// or no line at all is refused); a real one passes and is copied
// verbatim. The daemon reads no word of it.
func TestStepHandoffPerOwnerNamesAMessageOfThisRound(t *testing.T) {
	f := newC4Fixture(t, "wi_c4e")
	steps := map[string]*capsule.Step{
		"build": {Status: capsule.StepInProgress, Round: 2, StepStartedAt: "2026-11-11T09:00:00Z",
			Attempts: []capsule.Attempt{{Cause: "gate_fail_rewind", ArchivedAt: "2026-11-11T09:00:00Z"}, {Cause: "infra_failure", FailureCause: "model_error", Candidate: "pi:old", ArchivedAt: "2026-11-11T09:30:00Z"}}},
		"review":       {Status: capsule.StepInProgress, Round: 1},
		"verification": {Status: capsule.StepInProgress, Round: 1},
	}
	f.writeExec(t, steps)
	f.ownerSays(t, "build", "2026-11-11T08:00:00Z", "上一轮的话")
	f.ownerSays(t, "review", "2026-11-11T09:10:00Z", "退回 build 重做。")
	if err := capsule.AppendAsk(f.capDir, capsule.AskLine{At: "2026-11-11T09:20:00Z", Step: "build", Spawn: 1, Kind: capsule.AskKindAsk, Text: "要取消吗？"}); err != nil {
		t.Fatal(err)
	}
	f.ownerSays(t, "build", "2026-11-11T09:40:00Z", "可以取消。\n  原样照抄这一行。")
	f.ownerSays(t, "verification", "2026-11-11T09:50:00Z", "这次 FAIL 不算。")
	for _, c := range []struct {
		status, step, to string
		spawn            int
		real, other      string
		extra            []string // more refused stamps (an earlier round's, the step's own ask)
		want             []string
	}{
		{"cancel", "build", "", 3, "2026-11-11T09:40:00Z", "2026-11-11T09:10:00Z", []string{"2026-11-11T08:00:00Z", "2026-11-11T09:20:00Z"},
			[]string{"~~~\n可以取消。\n  原样照抄这一行。\n~~~", "## build · 第 2 轮 · 派发 3 · cancel", "infra_failure/model_error (pi:old)"}},
		{"waive", "verification", "", 4, "2026-11-11T09:50:00Z", "2026-11-11T09:40:00Z", nil,
			[]string{"~~~\n这次 FAIL 不算。\n~~~", "## verification · 第 1 轮 · 派发 4 · waive"}},
		{"rewind", "review", "build", 5, "2026-11-11T09:10:00Z", "2026-11-11T09:50:00Z", nil,
			[]string{"~~~\n退回 build 重做。\n~~~", "## review · 第 1 轮 · 派发 5 · rewind → build"}},
	} {
		f.as(c.step, c.spawn)
		args := func(at string) map[string]any {
			a := map[string]any{"status": c.status, "body": c4Body(), "per_owner": at}
			if c.to != "" {
				a["to"] = c.to
			}
			return a
		}
		before := f.handoffDoc(t)
		if code, body := c4Call(t, f.td, "handoff", args("")); code != 400 || !strings.Contains(body, "names the owner message it rests on") {
			t.Fatalf("%s without --per-owner: %d %s", c.status, code, body)
		}
		for _, at := range append([]string{"2026-11-11T11:11:11Z", c.other}, c.extra...) {
			if code, body := c4Call(t, f.td, "handoff", args(at)); code != 400 || !strings.Contains(body, "names no owner message") {
				t.Fatalf("%s --per-owner %s: %d %s", c.status, at, code, body)
			}
		}
		if f.handoffDoc(t) != before {
			t.Fatalf("%s: a refused --per-owner wrote the document", c.status)
		}
		if code, body := c4Call(t, f.td, "handoff", args(c.real)); code != 200 {
			t.Fatalf("%s with the round's real message = %d %s", c.status, code, body)
		}
		section := strings.TrimPrefix(f.handoffDoc(t), before)
		for _, w := range c.want {
			if !strings.Contains(section, w) {
				t.Fatalf("%s: the section lacks %q:\n%s", c.status, w, section)
			}
		}
		if rec := stepcall.HandoffOf(f.ledger(t), c.step, int64(c.spawn)); rec == nil || rec.Handoff.Status != c.status || rec.Handoff.PerOwner != c.real {
			t.Fatalf("%s: the ledger's record: %+v", c.status, rec)
		}
	}
	if doc := f.handoffDoc(t); strings.Contains(doc, "上一轮的话") {
		t.Fatalf("an earlier round's message was stamped:\n%s", doc)
	}
}

// TestStepCallsOfADispatchAreSerialized: concurrent checking calls of one
// dispatch see each other's results — of N parallel hand-offs exactly one
// is accepted (one section, one ok result line), and N parallel files of
// one key record one pending write.
func TestStepCallsOfADispatchAreSerialized(t *testing.T) {
	f := newC4Fixture(t, "wi_c4i")
	f.as("build", 1)
	f.td.stepHubAllow = func(stepJob) []string { return stepcall.HubVerbs }
	const n = 8
	parallel := func(verb string, args map[string]any) []int {
		var wg sync.WaitGroup
		codes := make([]int, n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				codes[i], _ = c4Call(t, f.td, verb, args)
			}(i)
		}
		wg.Wait()
		return codes
	}
	content := "修复。\n\n## Acceptance\n- 绿\n"
	for _, code := range parallel("hub.file", map[string]any{"key": "K1", "lane": "develop", "goal": "g", "content": content}) {
		if code != 200 {
			t.Fatalf("a same-key file = %d", code)
		}
	}
	if ops := stepcall.PendingOps(f.ledger(t), "build", 1); len(ops) != 1 {
		t.Fatalf("%d parallel same-key files recorded %d pending writes: %+v", n, len(ops), ops)
	}
	ok := 0
	for _, code := range parallel("handoff", map[string]any{"status": "done", "body": c4Body()}) {
		switch code {
		case 200:
			ok++
		case 409:
		default:
			t.Fatalf("a parallel hand-off = %d", code)
		}
	}
	accepted := 0
	for _, l := range f.ledger(t) {
		if l.Kind == stepcall.KindResult && l.Verb == stepcall.VerbHandoff && l.Outcome == stepcall.OutcomeOK {
			accepted++
		}
	}
	if sections := strings.Count(f.handoffDoc(t), "由 daemon 盖章"); ok != 1 || accepted != 1 || sections != 1 {
		t.Fatalf("%d parallel hand-offs: %d answered ok, %d ok result lines, %d sections — want one each", n, ok, accepted, sections)
	}
	for _, code := range parallel("hub.file", map[string]any{"key": "K2", "lane": "develop", "goal": "late", "content": content}) {
		if code != 409 {
			t.Fatalf("a write after the hand-off = %d", code)
		}
	}
}

// TestStepHandoffLedgerIsTheRecord: a step that rewrites handoff.md
// changes nothing the daemon recorded — the status, and the once-only
// check that reads it.
func TestStepHandoffLedgerIsTheRecord(t *testing.T) {
	f := newC4Fixture(t, "wi_c4f")
	f.as("review", 1)
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 200 {
		t.Fatalf("hand-off = %d %s", code, body)
	}
	doc := strings.ReplaceAll(f.handoffDoc(t), "done", "fail")
	if err := os.WriteFile(stepcall.HandoffPath(f.capDir), []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	if rec := stepcall.HandoffOf(f.ledger(t), "review", 1); rec == nil || rec.Handoff.Status != "done" {
		t.Fatalf("the recorded status after the edit: %+v", rec)
	}
	if err := os.WriteFile(stepcall.HandoffPath(f.capDir), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "fail", "body": c4Body()}); code != 409 || !strings.Contains(body, "already handed off done") {
		t.Fatalf("a second hand-off after the document was emptied = %d %s", code, body)
	}
}

// TestStepHubReadsProxyWritesStayPending: get and list are read through
// aihub, scoped to the step's project (each --attr passed on as aihub's
// attr=<path>:<value>); every write verb (batch included) is only
// recorded pending — aihub sees no write — in declaration order; a
// key:<K> reference must name a file recorded before it; a verb outside
// the step's whitelist is refused, and none is allowed without one; an
// attrs write's key must be on the step's hub.attrs.
func TestStepHubReadsProxyWritesStayPending(t *testing.T) {
	f := newC4Fixture(t, "wi_c4g")
	f.as("build", 1)
	var mu sync.Mutex
	var calls []string
	f.td.hub.mu.Lock()
	f.td.hub.memDo = func(method, path string, body, out any) error {
		mu.Lock()
		calls = append(calls, method+" "+path)
		mu.Unlock()
		switch {
		case path == "/api/v1/work-items/wi_own":
			return json.Unmarshal([]byte(`{"id":"wi_own","project":"proj","status":"wrapped"}`), out)
		case path == "/api/v1/work-items/wi_other":
			return json.Unmarshal([]byte(`{"id":"wi_other","project":"other"}`), out)
		case strings.HasPrefix(path, "/api/v1/work-items?"):
			return json.Unmarshal([]byte(`{"items":[{"id":"wi_own"}]}`), out)
		}
		return json.Unmarshal([]byte(`{}`), out)
	}
	f.td.hub.mu.Unlock()
	if code, body := c4Call(t, f.td, "hub.get", map[string]any{"wi": "wi_own"}); code != 403 || !strings.Contains(body, "whitelist: none") {
		t.Fatalf("hub without a whitelist = %d %s", code, body)
	}
	f.td.stepHubAllow = func(stepJob) []string { return stepcall.HubVerbs }
	f.td.stepHubAttrs = func(stepJob) []string { return []string{"integration", "note"} }
	if code, body := c4Call(t, f.td, "hub.get", map[string]any{"wi": "wi_own"}); code != 200 || !strings.Contains(body, `"status":"wrapped"`) {
		t.Fatalf("get = %d %s", code, body)
	}
	if code, _ := c4Call(t, f.td, "hub.get", map[string]any{"wi": "wi_other"}); code != 404 {
		t.Fatalf("get of another project's = %d", code)
	}
	if code, body := c4Call(t, f.td, "hub.list", map[string]any{"status": "wrapped", "attrs": []string{"integration.state=landed", "integration.batch=p#9"}, "limit": 5}); code != 200 || !strings.Contains(body, "wi_own") {
		t.Fatalf("list = %d %s", code, body)
	}
	mu.Lock()
	last := calls[len(calls)-1]
	mu.Unlock()
	for _, w := range []string{"project=proj", "status=wrapped", "attr=integration.state%3Alanded", "attr=integration.batch%3Ap%239", "limit=5"} {
		if !strings.Contains(last, w) {
			t.Fatalf("list forwarded %s, lacking %s", last, w)
		}
	}
	if code, _ := c4Call(t, f.td, "hub.list", map[string]any{"project": "other"}); code != 403 {
		t.Fatalf("list of another project = %d", code)
	}
	if code, body := c4Call(t, f.td, "hub.list", map[string]any{"attrs": []string{"integration.state"}}); code != 400 || !strings.Contains(body, "is not \\u003cpath\\u003e=\\u003cvalue\\u003e") {
		t.Fatalf("an attrs filter without a value = %d %s", code, body)
	}
	if code, body := c4Call(t, f.td, "hub.list", map[string]any{"integration": "landed"}); code != 400 || !strings.Contains(body, "unknown field") {
		t.Fatalf("the removed integration filter = %d %s", code, body)
	}
	content := "修复。\n\n## Acceptance\n- 绿\n"
	f.ownerSays(t, "build", "2026-11-11T10:00:00Z", "owner cleared")
	for _, c := range []struct {
		verb string
		args any
	}{
		{"hub.file", map[string]any{"key": "K1", "lane": "develop", "goal": "fix it", "content": content, "labels": []string{"fix"}}},
		{"hub.attrs", map[string]any{"target": "wi_own", "name": "integration", "value": map[string]any{"state": "landed", "fix_tickets": []any{"key:K1"}}, "if": map[string]any{"integration.state": "landed"}}},
		{"hub.attrs", map[string]any{"target": "key:K1", "name": "note", "value": "owner cleared", "per_owner": "2026-11-11T10:00:00Z"}},
		{"hub.wrap-attrs", map[string]any{"name": "landing", "value": map[string]any{"state": "landed"}}},
		{"hub.batch", map[string]any{"ops": []map[string]any{
			{"verb": "file", "key": "K2", "lane": "develop", "goal": "g2", "content": content},
			{"verb": "file", "key": "K1", "lane": "develop", "goal": "again", "content": content}, // the same ticket
			{"verb": "attrs", "target": "key:K2", "name": "integration", "value": map[string]any{"state": "merged"}},
		}}},
	} {
		if code, body := c4Call(t, f.td, c.verb, c.args); code != 200 || !strings.Contains(body, "recorded pending") {
			t.Fatalf("%s = %d %s", c.verb, code, body)
		}
	}
	mu.Lock()
	for _, c := range calls {
		if !strings.HasPrefix(c, "GET ") {
			t.Fatalf("a pending write reached aihub: %s", c)
		}
	}
	mu.Unlock()
	ops := stepcall.PendingOps(f.ledger(t), "build", 1)
	var got []string
	for _, op := range ops {
		got = append(got, op.Verb+":"+op.Key+op.Target)
	}
	if strings.Join(got, " ") != "file:K1 attrs:wi_own attrs:key:K1 wrap-attrs: file:K2 attrs:key:K2" {
		t.Fatalf("the pending writes, in order: %v", got)
	}
	if ops[0].Project != "proj" || ops[2].OwnerMessage == nil || ops[2].OwnerMessage.Words != "owner cleared" || ops[1].OwnerMessage != nil || !strings.Contains(ops[0].ID, ".1") {
		t.Fatalf("the daemon-set fields: %+v", ops)
	}
	// refusals record nothing pending
	n := len(ops)
	for _, c := range []struct {
		verb string
		args any
		code int
	}{
		{"hub.attrs", map[string]any{"target": "key:K9", "name": "integration", "value": map[string]any{"state": "merged"}}, 400},
		{"hub.attrs", map[string]any{"target": "wi_own", "name": "integration", "value": map[string]any{"fix_tickets": []any{"key:K9"}}}, 400},
		{"hub.attrs", map[string]any{"target": "wi_own", "name": "integration_batch", "value": 1}, 403},
		{"hub.attrs", map[string]any{"target": "wi_own", "name": "integration", "value": 1, "if": map[string]any{"Integration.State": "landed"}}, 400},
		{"hub.move", map[string]any{"target": "wi_own"}, 400},
		{"hub.file", map[string]any{"lane": "develop", "goal": "no key", "content": content}, 400},
		{"hub.batch", map[string]any{"ops": []map[string]any{{"verb": "file", "key": "K3", "lane": "develop", "goal": "g", "content": content}, {"verb": "get", "target": "x"}}}, 400},
	} {
		if code, body := c4Call(t, f.td, c.verb, c.args); code != c.code {
			t.Fatalf("%s %v = %d %s", c.verb, c.args, code, body)
		}
	}
	f.td.stepHubAllow = func(stepJob) []string { return []string{stepcall.HubGet, stepcall.HubBatch, stepcall.HubFile} }
	if code, _ := c4Call(t, f.td, "hub.attrs", map[string]any{"target": "wi_own", "name": "integration", "value": map[string]any{"state": "merged"}}); code != 403 {
		t.Fatalf("a write outside the whitelist = %d", code)
	}
	if code, body := c4Call(t, f.td, "hub.batch", map[string]any{"ops": []map[string]any{{"verb": "attrs", "target": "wi_own", "name": "integration", "value": map[string]any{"state": "merged"}}}}); code != 403 || !strings.Contains(body, "op 1") && !strings.Contains(body, "attrs refused") {
		t.Fatalf("a batch op outside the whitelist = %d %s", code, body)
	}
	if got := len(stepcall.PendingOps(f.ledger(t), "build", 1)); got != n {
		t.Fatalf("refused writes were recorded pending: %d → %d", n, got)
	}
	// after the dispatch handed off, nothing more is recorded
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 200 {
		t.Fatalf("hand-off = %d %s", code, body)
	}
	if code, _ := c4Call(t, f.td, "hub.file", map[string]any{"key": "K4", "lane": "develop", "goal": "late", "content": content}); code != 409 {
		t.Fatalf("a write after the hand-off = %d", code)
	}
}

// TestStepAskThroughTheCall: `pf step ask` appends the question under the
// dispatch the daemon resolved; a second one while it is open is refused.
func TestStepAskThroughTheCall(t *testing.T) {
	f := newC4Fixture(t, "wi_c4h")
	f.as("review", 4)
	if code, body := c4Call(t, f.td, "ask", map[string]any{"text": "  哪个方案？ "}); code != 200 || !strings.Contains(body, `"step":"review"`) {
		t.Fatalf("ask = %d %s", code, body)
	}
	lines, _ := capsule.ReadAsks(f.capDir)
	if open := capsule.OpenAsk(lines, "review", 4); open == nil || open.Text != "哪个方案？" {
		t.Fatalf("the open ask: %+v", lines)
	}
	if code, _ := c4Call(t, f.td, "ask", map[string]any{"text": "再问一次"}); code != 409 {
		t.Fatalf("a second open ask = %d", code)
	}
}

// TestStepHubSinkFilesOncePerKey: the executor's daemon binding files a
// ticket idempotently by its key's body line — a ticket already carrying
// the line (a write whose answer, or exec line, was lost) is answered
// without a second create; a new one is created carrying the line.
func TestStepHubSinkFilesOncePerKey(t *testing.T) {
	var mu sync.Mutex
	var posts []string
	filed := `[{"id":"wi_fix1","slug":"proj#7","content":"x\n\nintegration-ticket-key: K1\n"}]`
	hub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "POST" {
			var req struct {
				Content string `json:"content"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			posts = append(posts, req.Content)
			_, _ = w.Write([]byte(`{"id":"wi_new","slug":"proj#8"}`))
			return
		}
		_, _ = w.Write([]byte(`{"items":` + filed + `}`))
	}))
	t.Cleanup(hub.Close)
	td := newTestDaemon(t, func(c *Config) { c.Server, c.APIKey = hub.URL, "k"; c.Hub = aihubclient.New(hub.URL, "k") })
	sink := stepHubSink{td.Daemon}
	op := stepcall.Op{Verb: stepcall.HubFile, Key: "K1", Project: "proj", Lane: "develop", Goal: "g", Content: "x"}
	if ref, err := sink.File(context.Background(), op); err != nil || ref != "proj#7" {
		t.Fatalf("the filed ticket = %q, %v", ref, err)
	}
	op.Key = "K2"
	if ref, err := sink.File(context.Background(), op); err != nil || ref != "proj#8" {
		t.Fatalf("a new ticket = %q, %v", ref, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(posts) != 1 || !strings.Contains(posts[0], "integration-ticket-key: K2") {
		t.Fatalf("creates: %q", posts)
	}
}
