package driver

// claude_live_turn_parity_test.go: a claude step's error turn is a LIVE
// turn (workflow contract v2 §5.2, PR 6a: the interactive process idles
// on after its error `result`, so the dispatch dies by the supervisor's
// rule-1 verdict, never by an exit). The D7 table (supervise/claude.go)
// promises a class per structured failure; it must hold on the live path
// (verdictCause) exactly as on the exit path (classifyWaitFailure) for
// every structural row — the one that broke before this test: the
// canonical "claude-code model not found (HTTP 404)" text switched the
// candidate at once on exit but resumed the same unresolvable model
// live, costing a second dispatch (and the chain scenarios their
// spawn-indexed plans).

import (
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

func TestClaudeErrorTurnClassifiesTheSameLiveOrExited(t *testing.T) {
	for _, tc := range []struct {
		name     string
		category string
		status   int
		terminal string
		message  string
		want     string
	}{
		{"429 rate limit", "rate_limit", 429, "", "API Error: Request rejected", FailRateLimited},
		{"529 overloaded", "", 529, "", "Overloaded", FailRateLimited},
		{"401 authentication", "authentication_failed", 401, "", "Failed to authenticate. API Error: 401 Invalid API key", FailExitModel},
		{"402 billing", "billing_error", 402, "", "Your credit balance is too low", FailExitModel},
		{"404 model not found", "model_not_found", 404, "", "There's an issue with the selected model (claude-opus-5-5). It may not exist or you may not have access to it.", FailExitModel},
		{"404 by status alone", "", 404, "", "Not found", FailExitModel},
		{"500 upstream", "server_error", 500, "", "Server error mid-response", FailUpstreamTransient},
	} {
		text := supervise.ClaudeCanonicalError(tc.category, tc.status, tc.terminal, tc.message)
		live := verdictCause(supervise.StepDead{Step: "fix", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: text})
		exit, _, _ := classifyWaitFailure(&ExitedError{Step: "fix", SpawnN: 1, ExitCode: 1, LastError: text, TurnError: true})
		if exit != tc.want {
			t.Errorf("%s: the exit path classified %q as %q, want %q", tc.name, text, exit, tc.want)
		}
		if live != tc.want {
			t.Errorf("%s: the live path classified %q as %q, want %q (the exit path's class)", tc.name, text, live, tc.want)
		}
	}
	// the outage shapes stay model_error live (the first death resumes
	// the same candidate): the widened matcher must not take them
	for _, text := range []string{
		"claude-code error: model endpoint not found, retrying",
		"claude-code upstream connection dropped (HTTP 5xx): the model server returned unknown status",
	} {
		if got := verdictCause(supervise.StepDead{Step: "fix", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: text}); got == FailExitModel {
			t.Errorf("verdictCause(%q) = %q: an outage-shaped text must not switch at once", text, got)
		}
	}
}
