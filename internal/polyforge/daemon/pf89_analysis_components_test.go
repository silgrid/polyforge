package daemon

// pf89_analysis_components_test.go (polyforge#89, AC3): the analysis
// prompt carries the component division. The daemon — the one side that
// knows the lane's shared scripts and the mapped clones — runs the
// workflow's components.py over the ticket's repositories (its frozen
// repo_inventory, else the project's every repository) and attaches the
// tool's line report between the template and the ticket; a missing
// clone, an unusable tool, a failing run or a timeout answers one
// fallback line (the template's defaults apply) and never blocks the
// dispatch.

import (
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// pf89Root answers the repository root (this package sits three levels
// below it).
func pf89Root(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// pf89CopyTree copies one directory tree (the shared scripts: the tool
// and the pfstep package it imports) into the test's lane directory.
func pf89CopyTree(t *testing.T, src, dst string) {
	t.Helper()
	err := filepath.WalkDir(src, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if e.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !e.Type().IsRegular() {
			return nil
		}
		in, err := os.Open(p)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
	if err != nil {
		t.Fatalf("copy %s: %v", src, err)
	}
}

// pf89World is w72World with a hook over the fake hub's scripted maps.
func pf89World(t *testing.T, hub func(*fakeHub)) (*testDaemon, *w72Hub, *w72Runner) {
	t.Helper()
	wf := t.TempDir()
	workflowtest.WriteLane(t, wf, "develop", "steps:\n  - { id: build }\n")
	inner := newFakeHub()
	if hub != nil {
		hub(inner)
	}
	hubw := &w72Hub{fakeHub: inner, contents: map[string]*string{}}
	runner := newW72Runner()
	td := newTestDaemon(t, func(c *Config) {
		c.Hub = hubw
		c.WorkflowsDir = wf
		c.TaskRunner = runner
	})
	pf89WriteWorkspace(t, td)
	return td, hubw, runner
}

// pf89SharedScripts copies the workflow's real shared scripts into the
// test's lane directory — the tool the daemon finds at
// <WorkflowsDir>/_shared/scripts/components.py.
func pf89SharedScripts(t *testing.T, td *testDaemon) {
	t.Helper()
	dst := filepath.Join(td.cfg.WorkflowsDir, "_shared", "scripts")
	if err := os.MkdirAll(dst, 0o755); err != nil {
		t.Fatal(err)
	}
	pf89CopyTree(t, filepath.Join(pf89Root(t), "workflows", "_shared", "scripts"), dst)
}

// pf89WriteWorkspace writes the project's task-model declaration into
// the mapped workspace (w72World's tail).
func pf89WriteWorkspace(t *testing.T, td *testDaemon) {
	t.Helper()
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, workspace.FileName),
		[]byte("steps:\n  proj:\n    task: { models: [pi:pf89/m] }\n"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// pf89Clone seeds one git clone at <workspace>/.repo/<name> holding a
// conf that declares a component and a threshold.
func pf89Clone(t *testing.T, td *testDaemon, name string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	d := filepath.Join(ws, ".repo", name)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = d
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %s in %s: %v\n%s", strings.Join(args, " "), d, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	conf := filepath.Join(d, ".polyforge", "verify-scope.conf")
	if err := os.MkdirAll(filepath.Dir(conf), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(conf, []byte("component=core|src\nepic_units=9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(d, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "src", "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "-A")
	run("commit", "-q", "-m", "fixture")
}

// pf89Prompt enqueues the seeded ticket and answers the spawned task's
// prompt.
func pf89Prompt(t *testing.T, td *testDaemon, hub *w72Hub, runner *w72Runner, id string) string {
	t.Helper()
	w72Ticket(t, hub, id, "## 背景\n\nthe ticket body\n\n## Acceptance\n\n- AC1 x — carried by build\n")
	res := w72Enqueue(t, td, id)
	if res.State != "scheduled" || !strings.HasPrefix(res.TaskID, "task_") {
		t.Fatalf("the analysis must dispatch with the division attached or fallen back: %+v", res)
	}
	spec, ok := runner.specOf(res.TaskID)
	if !ok {
		t.Fatal("the runner never saw the analysis task")
	}
	return spec.Prompt
}

// TestPF89AnalysisPromptCarriesTheComponentSection: with the tool in the
// lane's shared scripts and a clone under the mapped workspace, the
// prompt carries the tool's report between the template and the ticket,
// the template's epic section pointing at the attached division and its
// defaults, and a second repository without a clone named by the one
// fallback line.
func TestPF89AnalysisPromptCarriesTheComponentSection(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	td, hub, runner := pf89World(t, func(h *fakeHub) {
		h.repoInventory = map[string][]string{"wi_pf89_a": {"repoA", "repoB"}}
	})
	pf89SharedScripts(t, td)
	pf89Clone(t, td, "repoA")
	prompt := pf89Prompt(t, td, hub, runner, "wi_pf89_a")
	// the attached division, between the template and the ticket
	iIntro := strings.Index(prompt, "各仓库组件划分与生效阈值")
	iSec := strings.Index(prompt, "component core  [src]  declared")
	iBody := strings.Index(prompt, "# 工单")
	for _, c := range []struct{ what, s string }{
		{"the section intro", "各仓库组件划分与生效阈值"},
		{"the declared component", "component core  [src]  declared"},
		{"the declared threshold with its source", "epic_units 9 (declared)"},
		{"the template's fallback sentence", "没有附带这一节时"},
		{"the missing clone's fallback line", "仓库 repoB 在工作区没有 clone"},
		{"the ticket heading", "# 工单"},
	} {
		if !strings.Contains(prompt, c.s) {
			t.Fatalf("the analysis prompt must carry %s: %q", c.what, c.s)
		}
	}
	iTpl := strings.Index(prompt, "升级 epic 建议")
	if iTpl < 0 || iIntro < iTpl || iSec < iIntro || iBody < iSec {
		t.Fatalf("the prompt's order must be template, division, ticket (template %d, intro %d, section %d, ticket %d)",
			iTpl, iIntro, iSec, iBody)
	}
	if !strings.Contains(prompt, "the ticket body") {
		t.Fatal("the ticket body must stay in the prompt")
	}
}

// TestPF89MissingCloneWritesTheFallbackLine: no clone at all answers the
// one fallback line per missing repository and the dispatch proceeds.
func TestPF89MissingCloneWritesTheFallbackLine(t *testing.T) {
	td, hub, runner := pf89World(t, func(h *fakeHub) {
		h.repoInventory = map[string][]string{"wi_pf89_b": {"repoA"}}
	})
	prompt := pf89Prompt(t, td, hub, runner, "wi_pf89_b")
	if !strings.Contains(prompt, "仓库 repoA 在工作区没有 clone——按默认阈值判断") {
		t.Fatalf("a missing clone answers the one fallback line:\n%s", prompt)
	}
	if !strings.Contains(prompt, "# 工单") || !strings.Contains(prompt, "the ticket body") {
		t.Fatal("the fallback must not eat the ticket")
	}
}

// TestPF89UnusableToolWritesTheFallbackLine: a lane directory without
// the shared scripts answers the tool-unavailable fallback and the
// dispatch proceeds (the w72 world's shape).
func TestPF89UnusableToolWritesTheFallbackLine(t *testing.T) {
	td, hub, runner := pf89World(t, func(h *fakeHub) {
		h.repoInventory = map[string][]string{"wi_pf89_c": {"repoA"}}
	})
	pf89Clone(t, td, "repoA")
	prompt := pf89Prompt(t, td, hub, runner, "wi_pf89_c")
	if !strings.Contains(prompt, "组件划分未附带（工具不可用）——按默认阈值判断") {
		t.Fatalf("a missing tool answers the one fallback line:\n%s", prompt)
	}
}

// TestPF89FailingToolWritesTheFallbackLine: a tool that exits non-zero
// answers the tool-failed fallback and the dispatch proceeds.
func TestPF89FailingToolWritesTheFallbackLine(t *testing.T) {
	td, hub, runner := pf89World(t, func(h *fakeHub) {
		h.repoInventory = map[string][]string{"wi_pf89_d": {"repoA"}}
	})
	pf89SharedScripts(t, td)
	pf89Clone(t, td, "repoA")
	tool := filepath.Join(td.cfg.WorkflowsDir, "_shared", "scripts", "components.py")
	if err := os.WriteFile(tool, []byte("import sys\nsys.exit(3)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	prompt := pf89Prompt(t, td, hub, runner, "wi_pf89_d")
	if !strings.Contains(prompt, "组件划分未附带（工具失败）——按默认阈值判断") {
		t.Fatalf("a failing tool answers the one fallback line:\n%s", prompt)
	}
}

// TestPF89StuckToolTimesOutAndWritesTheFallbackLine: a tool that never
// answers is killed at the bounded timeout and the dispatch proceeds
// with the timeout fallback — the tool runs inside the dispatch walk's
// lock and must never hold it.
func TestPF89StuckToolTimesOutAndWritesTheFallbackLine(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed")
	}
	old := analysisComponentsTimeout
	analysisComponentsTimeout = 300 * time.Millisecond
	t.Cleanup(func() { analysisComponentsTimeout = old })
	td, hub, runner := pf89World(t, func(h *fakeHub) {
		h.repoInventory = map[string][]string{"wi_pf89_e": {"repoA"}}
	})
	pf89SharedScripts(t, td)
	pf89Clone(t, td, "repoA")
	tool := filepath.Join(td.cfg.WorkflowsDir, "_shared", "scripts", "components.py")
	if err := os.WriteFile(tool, []byte("import time\ntime.sleep(600)\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	prompt := pf89Prompt(t, td, hub, runner, "wi_pf89_e")
	if d := time.Since(started); d > 10*time.Second {
		t.Fatalf("the stuck tool must not hold the dispatch: %v", d)
	}
	if !strings.Contains(prompt, "组件划分未附带（工具超时）——按默认阈值判断") {
		t.Fatalf("a stuck tool answers the timeout fallback:\n%s", prompt)
	}
}

// TestPF89NoRepositoryNamesNoSection: a ticket whose inventory and
// project repos read answers nothing names no repository and the prompt
// carries no section — the template's defaults apply on their own.
func TestPF89NoRepositoryNamesNoSection(t *testing.T) {
	td, hub, runner := pf89World(t, nil)
	prompt := pf89Prompt(t, td, hub, runner, "wi_pf89_f")
	if strings.Contains(prompt, "组件划分") {
		t.Fatalf("no repository named answers no section:\n%s", prompt)
	}
	if !strings.Contains(prompt, "升级 epic 建议") || !strings.Contains(prompt, "the ticket body") {
		t.Fatal("the template and the ticket stay in the prompt")
	}
}
