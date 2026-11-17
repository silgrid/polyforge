package task

// carries: internal/polyforge/workspace/scratch_task.go, internal/polyforge/workspace/layout.go, internal/polyforge/task/manager.go, internal/polyforge/task/dispatch.go

// w121_task_scratch_test.go (polyforge#121, owner rulings Q3/Q4/Q10): the
// daemon-side delivery of the scratch directories — the manager creates
// them before the dispatch (a refusal fails the spawn), the runner's
// environment carries PF_SCRATCH_DIR / PF_SCRATCH_RAW_DIR, and every
// dispatched prompt — the ordinary dispatchedPrompt and a PromptFor
// (analysis) answer alike — ends with the # Scratch section naming the
// same absolute paths. Expected values are hand-built from the layout
// rule (raw/<UTC time>-<source id>-<unique suffix>, tasks/<task id>),
// never copied from the implementation.

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// w121Runner records every candidate's spec and prompt.
type w121Runner struct {
	specs   []RunSpec
	prompts []string
}

func (r *w121Runner) Run(_ context.Context, spec RunSpec, _ func(int64)) (RunResult, error) {
	r.specs = append(r.specs, spec)
	r.prompts = append(r.prompts, spec.Prompt)
	return RunResult{Report: "done", Tokens: 1}, nil
}

// TestW121TaskScratchDirs: a spawn creates the task's directories before
// the dispatch, the runner's spec carries them, the prompt ends with the
// # Scratch section naming the same absolute paths and the environment
// variable names — and the record keeps the prompt as given (no section,
// no paths).
func TestW121TaskScratchDirs(t *testing.T) {
	ws := t.TempDir()
	r := &w121Runner{}
	m := NewManager(Config{
		Store:  NewStore(t.TempDir()),
		Runner: r,
		Resolve: func(string) ([]string, string, error) {
			return []string{"pi:a/one"}, "steps.p.task.models", nil
		},
		WorkDir: func(string) (string, error) { return ws, nil },
	})
	t.Cleanup(m.Close)
	tk, err := m.Spawn(Spec{Project: "p", Prompt: "the filed prompt"})
	if err != nil {
		t.Fatal(err)
	}
	done := waitState(t, m, tk.ID, StateCompleted)
	if len(r.specs) != 1 {
		t.Fatalf("runs = %d, want 1", len(r.specs))
	}
	spec := r.specs[0]
	// the task directory: scratch/tasks/<task id>, created before the run
	taskDir := filepath.Join(ws, workspace.ScratchDir, workspace.ScratchTasksDir, tk.ID)
	if spec.ScratchTaskDir != taskDir {
		t.Fatalf("ScratchTaskDir = %q, want %q", spec.ScratchTaskDir, taskDir)
	}
	if st, err := os.Stat(taskDir); err != nil || !st.IsDir() {
		t.Fatalf("the task directory was not created: %v", err)
	}
	// the raw batch: scratch/raw/<UTC time>-<task id>-<suffix>, one directory
	rawEntries, err := os.ReadDir(filepath.Join(ws, workspace.ScratchDir, workspace.ScratchRawDir))
	if err != nil || len(rawEntries) != 1 {
		t.Fatalf("the raw batch was not created: %v (%d entries)", err, len(rawEntries))
	}
	batch := rawEntries[0].Name()
	pattern := `^\d{8}T\d{6}Z-` + regexp.QuoteMeta(tk.ID) + `-[0-9a-f]+$`
	if !regexp.MustCompile(pattern).MatchString(batch) {
		t.Fatalf("the raw batch %q must be <UTC time>-<task id>-<unique suffix>", batch)
	}
	rawDir := filepath.Join(ws, workspace.ScratchDir, workspace.ScratchRawDir, batch)
	if spec.ScratchRawDir != rawDir {
		t.Fatalf("ScratchRawDir = %q, want %q", spec.ScratchRawDir, rawDir)
	}
	// the prompt ends with the # Scratch section naming both paths and
	// the environment variables
	if !strings.HasPrefix(r.prompts[0], harness.TaskProhibitedActions(ws)) {
		t.Fatalf("the prompt must open with the prohibited-actions list:\n%s", r.prompts[0])
	}
	want := scratchNote(taskDir, rawDir)
	if !strings.HasSuffix(r.prompts[0], want) {
		t.Fatalf("the prompt must end with the # Scratch section:\n%s", r.prompts[0])
	}
	for _, s := range []string{"# Scratch", "PF_SCRATCH_DIR", "PF_SCRATCH_RAW_DIR", taskDir, rawDir} {
		if !strings.Contains(r.prompts[0], s) {
			t.Fatalf("the prompt lacks %q:\n%s", s, r.prompts[0])
		}
	}
	// the record keeps the prompt as given: no section, no paths
	if done.Prompt != "the filed prompt" || strings.Contains(done.Prompt, "# Scratch") {
		t.Fatalf("the record's prompt must stay as given: %q", done.Prompt)
	}
}

// TestW121TaskScratchPromptForPath: a PromptFor (analysis) answer gets the
// same # Scratch section appended — the re-read path is covered where it
// joins the manager, not only in dispatchedPrompt.
func TestW121TaskScratchPromptForPath(t *testing.T) {
	ws := t.TempDir()
	r := &w121Runner{}
	m := NewManager(Config{
		Store:  NewStore(t.TempDir()),
		Runner: r,
		Resolve: func(string) ([]string, string, error) {
			return []string{"pi:a/one"}, "steps.p.task.models", nil
		},
		WorkDir: func(string) (string, error) { return ws, nil },
		PromptFor: func(Task) string {
			return "# 分析任务\nthe re-read answer"
		},
	})
	t.Cleanup(m.Close)
	tk, err := m.Spawn(Spec{Project: "p", Prompt: "the filed prompt", AnalysisOf: "wi_121"})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, tk.ID, StateCompleted)
	p := r.prompts[0]
	if !strings.Contains(p, "# 分析任务") || !strings.Contains(p, "the re-read answer") {
		t.Fatalf("the candidate must run the hook's answer:\n%s", p)
	}
	taskDir := filepath.Join(ws, workspace.ScratchDir, workspace.ScratchTasksDir, tk.ID)
	if !strings.Contains(p, "# Scratch") {
		t.Fatalf("the PromptFor path must carry the # Scratch section too:\n%s", p)
	}
	if !strings.HasSuffix(p, scratchNote(taskDir, r.specs[0].ScratchRawDir)) {
		t.Fatalf("the PromptFor path must carry the same # Scratch section:\n%s", p)
	}
}

// TestW121TaskScratchRefusal: a workspace whose scratch/ cannot carry the
// categories (a file in its place) refuses the spawn — no task is
// recorded — and TaskScratch refuses an id that is not one safe path
// segment.
func TestW121TaskScratchRefusal(t *testing.T) {
	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, workspace.ScratchDir), []byte("a file"), 0o644); err != nil {
		t.Fatal(err)
	}
	r := &w121Runner{}
	m := NewManager(Config{
		Store:  NewStore(t.TempDir()),
		Runner: r,
		Resolve: func(string) ([]string, string, error) {
			return []string{"pi:a/one"}, "steps.p.task.models", nil
		},
		WorkDir: func(string) (string, error) { return ws, nil },
	})
	t.Cleanup(m.Close)
	if _, err := m.Spawn(Spec{Project: "p", Prompt: "hi"}); err == nil || !strings.Contains(err.Error(), "scratch") {
		t.Fatalf("a spawn over a broken scratch must be refused, got %v", err)
	}
	if len(r.specs) != 0 {
		t.Fatalf("a refused spawn must not run: %d run(s)", len(r.specs))
	}
	for _, bad := range []string{"", ".", "..", "a/b", "a\\b", "/abs"} {
		if _, _, err := workspace.TaskScratch(t.TempDir(), bad, time.Now()); err == nil {
			t.Fatalf("TaskScratch accepted the unsafe id %q", bad)
		}
	}
}

// TestW121TaskScratchEnv: the harness's environment carries the daemon's
// determined values — PF_SCRATCH_DIR and PF_SCRATCH_RAW_DIR, each exactly
// once (the inherited environment is stripped of every PF_* name before
// they are set).
func TestW121TaskScratchEnv(t *testing.T) {
	r := &ExecRunner{PiBin: "/bin/true", DaemonID: "m-w121", SocketPath: "/nonexistent.sock"}
	spec := RunSpec{
		Candidate:      harness.Candidate{Harness: harness.Pi, Model: "m"},
		WorkDir:        t.TempDir(),
		TaskID:         "task_0123456789abcdef",
		ScratchTaskDir: "/ws/scratch/tasks/task_0123456789abcdef",
		ScratchRawDir:  "/ws/scratch/raw/20261118T000000Z-task_0123456789abcdef-1a2b3c4d",
	}
	cmd, err := r.command(context.Background(), spec)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		workspace.EnvScratchDir + "=" + spec.ScratchTaskDir,
		workspace.EnvScratchRawDir + "=" + spec.ScratchRawDir,
	} {
		n := 0
		for _, kv := range cmd.Env {
			if kv == want {
				n++
			}
		}
		if n != 1 {
			t.Fatalf("the child env must carry %q exactly once (got %d):\n%v", want, n, cmd.Env)
		}
	}
	// a run without scratch directories (none wired) sets neither
	cmd, err = r.command(context.Background(), RunSpec{Candidate: spec.Candidate, WorkDir: t.TempDir(), TaskID: spec.TaskID})
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, workspace.EnvScratchDir+"=") || strings.HasPrefix(kv, workspace.EnvScratchRawDir+"=") {
			t.Fatalf("an unset scratch directory must not reach the child env: %q", kv)
		}
	}
}

// TestW121TaskScratchReadOnlyNone: a read-only errand (the message
// analyzer) runs without scratch directories — no dirs created in the
// workspace, none in the spec, no # Scratch section: its invariant is
// that not one path of the workspace changes.
func TestW121TaskScratchReadOnlyNone(t *testing.T) {
	ws := t.TempDir()
	r := &w121Runner{}
	m := NewManager(Config{
		Store:  NewStore(t.TempDir()),
		Runner: r,
		Resolve: func(string) ([]string, string, error) {
			return []string{"pi:a/one"}, "steps.p.task.models", nil
		},
		WorkDir: func(string) (string, error) { return ws, nil },
	})
	t.Cleanup(m.Close)
	tk, err := m.Spawn(Spec{Project: "p", Prompt: "read only", ReadOnlyTools: true})
	if err != nil {
		t.Fatal(err)
	}
	waitState(t, m, tk.ID, StateCompleted)
	if r.specs[0].ScratchTaskDir != "" || r.specs[0].ScratchRawDir != "" {
		t.Fatalf("a read-only errand carries no scratch directories: %+v", r.specs[0])
	}
	if strings.Contains(r.prompts[0], "# Scratch") {
		t.Fatalf("a read-only errand's prompt carries no # Scratch section:\n%s", r.prompts[0])
	}
	if _, err := os.Stat(filepath.Join(ws, workspace.ScratchDir)); !os.IsNotExist(err) {
		t.Fatalf("a read-only errand created scratch/ in the workspace: %v", err)
	}
}
