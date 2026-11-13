package daemon

// w350_exec_disabled_test.go (D5): a Cell whose execution
// plane is disabled starts nothing from any entry point — a task spawn, a
// resume of held work, a submit — refusing with the Cell's own reason,
// while queries keep serving and a job already running is not killed. The
// existing reopen rules hold: a [workspace] reload opens a no-workspace
// Cell; a missing model source needs a restart, and a task in between is
// refused.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// w350Reason is the JSON-safe head of a disabled reason (the reasons
// quote their remedy, which the wire answer escapes).
func w350Reason(why string) string {
	if i := strings.IndexByte(why, '"'); i >= 0 {
		return why[:i]
	}
	return why
}

func w350DisabledDaemon(t *testing.T, why string) *testDaemon {
	t.Helper()
	td := newTestDaemon(t, func(c *Config) {
		c.TaskRunner = blockRunner{}
		c.ExecutionDisabled = why
	})
	ws, err := td.cfg.Mapping.WorkspaceFor("proj")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ws, workspace.FileName), []byte(taskYaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return td
}

func TestW350DisabledCellRefusesSpawnResumeAndSubmit(t *testing.T) {
	td := w350DisabledDaemon(t, execDisabledNoModelSource)
	for i := 0; i < 2; i++ {
		code, body := stepDo(t, td.sock, "POST", "/task/spawn", `{"project":"proj","prompt":"x"}`)
		if code != 409 || !strings.Contains(body, "execution is disabled on this Cell: "+w350Reason(execDisabledNoModelSource)) {
			t.Fatalf("spawn %d in a disabled Cell = %d %s", i, code, body)
		}
	}
	if code, body := stepGet(t, td.sock, "/task/list"); code != 200 || !strings.Contains(body, `"tasks":[]`) {
		t.Fatalf("the refused spawns left a record: %d %s", code, body)
	}
	if s := td.tasks.Summarize(); s.Running != 0 {
		t.Fatalf("the refused spawns started %d task(s)", s.Running)
	}
	code, body := stepDo(t, td.sock, "POST", "/jobs/wi_held/resume-paused", `{}`)
	if code != 409 || !strings.Contains(body, w350Reason(execDisabledNoModelSource)) {
		t.Fatalf("resume-paused in a disabled Cell = %d %s", code, body)
	}
	if _, err := td.client.ClaimWithModels("wi_new", "normal", nil); err == nil || !strings.Contains(err.Error(), execDisabledNoModelSource) {
		t.Fatalf("a submit in a disabled Cell: %v", err)
	}
	if td.takeNext() != nil {
		t.Fatal("a disabled Cell took a queue entry")
	}
	if code, _ := stepGet(t, td.sock, "/status"); code != 200 {
		t.Fatalf("GET /status of a disabled Cell = %d", code)
	}
}

func TestW350DisabledStateKillsNoRunningJob(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_run")
	td.reloadMu.Lock()
	td.execDisabled = execDisabledNoModelSource
	td.reloadMu.Unlock()
	if _, err := td.client.ClaimWithModels("wi_next", "normal", nil); err == nil || !strings.Contains(err.Error(), execDisabledNoModelSource) {
		t.Fatalf("a submit after the Cell became disabled: %v", err)
	}
	time.Sleep(50 * time.Millisecond) // several dispatch ticks (10 ms) pass
	if _, live := td.rt.Info("wi_run"); !live {
		t.Fatal("the disabled state killed a job that was already running")
	}
}

func TestW350DisabledCellReopenRules(t *testing.T) {
	for _, tc := range []struct {
		why    string
		reopen bool
	}{
		{execDisabledNoWorkspace, true},
		{execDisabledNoModelSource, false},
	} {
		td := w350DisabledDaemon(t, tc.why)
		ws, err := td.cfg.Mapping.WorkspaceFor("proj")
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(t.TempDir(), "cli.toml")
		td.cfg.ConfigPath = path
		if err := os.WriteFile(path, []byte("[workspace.\""+ws+"\"]\nprojects = [\"proj\"]\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := td.reloadConfig(); err != nil {
			t.Fatal(err)
		}
		got := td.executionDisabled()
		if tc.reopen && got != "" {
			t.Fatalf("a [workspace] reload must open a no-workspace Cell, still %q", got)
		}
		if !tc.reopen {
			if got != tc.why {
				t.Fatalf("a reload must not hot-apply a model source: %q", got)
			}
			code, body := stepDo(t, td.sock, "POST", "/task/spawn", `{"project":"proj","prompt":"x"}`)
			if code != 409 || !strings.Contains(body, w350Reason(tc.why)) {
				t.Fatalf("a task between the reload and the restart = %d %s", code, body)
			}
		}
	}
}

// An owner message continues a run: in a disabled Cell it is refused
// DAEMON-409 naming the reason and never reaches the driver. (The reserved
// cancel answer the disabled state let through left with the v1 question,
// workflow contract v2 change 19; `pf cancel` cancels.)
func TestW350DisabledCellRefusesAnOwnerMessage(t *testing.T) {
	const wi = "wi_asked"
	aa := &answerAgent{messages: make(chan string, 1)}
	td := newAnswerDaemon(t, aa)
	td.submitClaim(t, wi)
	td.waitMonitored(t, wi)
	td.reloadMu.Lock()
	td.execDisabled = execDisabledNoModelSource
	td.reloadMu.Unlock()
	_, err := td.client.Answer(wi, cli.AnswerPayload{AnswerText: "yes", AnsweredBy: "u_1"})
	if err == nil || !strings.Contains(err.Error(), "DAEMON-409") || !strings.Contains(err.Error(), "execution is disabled on this Cell: "+execDisabledNoModelSource) {
		t.Fatalf("an owner message in a disabled Cell: %v", err)
	}
	select {
	case text := <-aa.messages:
		t.Fatalf("the refused message reached the driver (%s)", text)
	default:
	}
}
