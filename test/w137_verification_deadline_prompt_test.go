package test

// w137_verification_deadline_prompt_test.go: reproduction
// (AC3) — the verification step's prompt tells the agent to compute
// `pf verify-run --deadline` from its own shell timeout ("about 2 minutes
// under that timeout"). On pi there is no such number in the step
// environment: the agent invented 3480s / 55m / 72m / 78m on 2026-10-29
// and every one of those runs was killed by the daemon's 30m tool rule at
// exactly 30.0 minutes, with no result.json.
//
// Red on the pre-fix tree: workflows/_shared/steps/verification.md still
// carries the derivation instruction.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW137VerificationPromptDoesNotDeriveDeadlineFromShellTimeout(t *testing.T) {
	p := filepath.Join(v1Root(t), "workflows/_shared/steps/verification.md")
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, phrase := range []string{
		"under that timeout",
		"as long as your shell tool allows",
	} {
		if strings.Contains(text, phrase) {
			t.Errorf("workflows/_shared/steps/verification.md still tells the agent to derive --deadline from its own shell timeout (%q); a pi step has no shell-call maximum to derive it from", phrase)
		}
	}
	if !strings.Contains(text, "--deadline") {
		t.Errorf("workflows/_shared/steps/verification.md no longer mentions --deadline at all; the lane text must still say what to pass or that the runner bounds it")
	}
}
