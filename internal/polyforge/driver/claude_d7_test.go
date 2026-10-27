package driver

// claude_d7_test.go: polyforge#159 design §3 — Claude Code's structured
// failures, as raw stream-json lines, through the REAL Claude translator,
// the dispatch tracker, stepRun.exited and classifyWaitFailure, land in
// the D7 class the table promises. The classifier is unchanged: only the
// translator's canonical text decides.

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/spawn"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// exitedHandle is a finished process with a fixed exit.
type exitedHandle struct{ info spawn.ExitInfo }

func (h exitedHandle) PID() int                      { return 1 }
func (h exitedHandle) StartedAt() string             { return "" }
func (h exitedHandle) Alive() bool                   { return false }
func (h exitedHandle) Done() bool                    { return true }
func (h exitedHandle) Terminate(time.Duration) error { return nil }
func (h exitedHandle) ExitInfo() spawn.ExitInfo      { return h.info }

func cl(t *testing.T, rec map[string]any) []byte {
	t.Helper()
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestClaudeD7Table(t *testing.T) {
	init := map[string]any{"type": "system", "subtype": "init", "session_id": "s", "tools": []string{"Bash"}}
	apiErr := func(cat, text string) map[string]any {
		return map[string]any{"type": "assistant", "error": cat, "parent_tool_use_id": nil,
			"message": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}}}
	}
	result := func(status int, terminal, msg string) map[string]any {
		r := map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true, "result": msg}
		if status > 0 {
			r["api_error_status"] = status
		}
		if terminal != "" {
			r["terminal_reason"] = terminal
		}
		return r
	}
	cases := []struct {
		name  string
		lines []map[string]any
		code  int
		want  string
	}{
		{"429 rate limit: same-model backoff", []map[string]any{init, apiErr("rate_limit", "API Error: Request rejected"), result(429, "", "API Error: Request rejected")}, 1, FailRateLimited},
		{"529 overloaded: same-model backoff", []map[string]any{init, result(529, "", "Overloaded")}, 1, FailRateLimited},
		{"401 auth: switch", []map[string]any{init, apiErr("authentication_failed", "Invalid bearer token"), result(401, "", "Invalid bearer token")}, 1, FailExitModel},
		{"oauth org not allowed: switch", []map[string]any{init, apiErr("oauth_org_not_allowed", "org"), result(0, "", "org")}, 1, FailExitModel},
		{"402 billing: switch", []map[string]any{init, apiErr("billing_error", "Credit balance is too low"), result(0, "", "Credit balance is too low")}, 1, FailExitModel},
		{"model not found: switch", []map[string]any{init, apiErr("model_not_found", "There's an issue with the selected model"), result(404, "", "There's an issue with the selected model")}, 1, FailExitModel},
		{"5xx: bounded same-model retry", []map[string]any{init, apiErr("server_error", "Server error mid-response"), result(500, "", "Server error mid-response")}, 1, FailUpstreamTransient},
		{"no response from API: bounded same-model retry", []map[string]any{init, result(0, "", "No response from API")}, 1, FailUpstreamTransient},
		{"malformed tool use: bounded same-model retry", []map[string]any{init, result(0, "malformed_tool_use_exhausted", "tool use failed")}, 1, FailUpstreamTransient},
		{"prompt too long: error-turn fallback (switch)", []map[string]any{init, apiErr("invalid_request", "Prompt is too long"), result(400, "prompt_too_long", "Prompt is too long")}, 1, FailExitModel},
		{"unknown: error-turn fallback (switch)", []map[string]any{init, result(0, "", "something odd")}, 1, FailExitModel},
		{"success result, exit 0, no envelope: ended_without_result", []map[string]any{init, {"type": "result", "subtype": "success", "is_error": false, "result": "done"}}, 0, FailEndedWithoutResult},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			link := supervise.NewLink("wi_d7")
			tr := link.Tracker(1)
			tr.Begin(supervise.StepStarted{Step: "fix", SpawnN: 1, Harness: "claude", StartedAt: time.Now()})
			tl := supervise.NewClaude(tr)
			for _, rec := range c.lines {
				tl.Feed(cl(t, rec))
			}
			tl.Exit(supervise.ExitInfo{Code: c.code})
			run := &stepRun{sp: StepSpawn{StepID: "fix", SpawnN: 1}, handle: exitedHandle{spawn.ExitInfo{Code: c.code}}, sink: tr}
			cause, _, detail := classifyWaitFailure(run.exited(nil))
			if cause != c.want {
				t.Fatalf("cause %q, want %q (detail %s)", cause, c.want, detail)
			}
		})
	}
}

// An executable that does not exist is a host fault whatever the harness.
func TestClaudeMissingBinaryIsHost(t *testing.T) {
	cause, _, _ := classifyWaitFailure(errString(`driver: step fix spawn 1 derivation failed: runtime: harness start failed: exec: "claude": executable file not found in $PATH`))
	if cause != FailExitHost {
		t.Fatalf("cause %q", cause)
	}
}

type errString string

func (e errString) Error() string { return string(e) }
