package driver

// w51_added_rules_test.go (polyforge#51): the Host-configured additional
// failure-classification rules — where they sit in the ordered rows, what
// they may never do (pull an allowance/auth failure out of model class), the
// two paths they match (an exited process's text and a live error turn) and
// the one they never match (the driver's own error), and the classification
// source (builtin / added / fallback) recorded on the failure.

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

func w51Rules(t *testing.T, transient, model []string) *TextRules {
	t.Helper()
	r := &TextRules{}
	for _, p := range transient {
		re, err := regexp.Compile(p)
		if err != nil {
			t.Fatalf("transient pattern %q: %v", p, err)
		}
		r.Transient = append(r.Transient, re)
	}
	for _, p := range model {
		re, err := regexp.Compile(p)
		if err != nil {
			t.Fatalf("model pattern %q: %v", p, err)
		}
		r.Model = append(r.Model, re)
	}
	return r
}

// TestW51AddedTransientRuleMatchesBothPaths: an additional transient rule
// classifies its text upstream_transient on the exit path AND on the live
// error-turn path (Q7), and the source is "added".
func TestW51AddedTransientRuleMatchesBothPaths(t *testing.T) {
	rules := w51Rules(t, []string{`(?i)gateway hiccup`}, nil)
	cause, _, _, src, text := classifyWaitFailureWith(&ExitedError{Step: "s", ExitCode: 1, LastError: "gateway hiccup after 3s", TurnError: true}, rules)
	if cause != FailUpstreamTransient || src != ClassifiedByAdded {
		t.Fatalf("exit path: (%q, %q), want (%q, %q)", cause, src, FailUpstreamTransient, ClassifiedByAdded)
	}
	if text != "gateway hiccup after 3s" {
		t.Fatalf("the classified text must be carried: %q", text)
	}
	cause, src = verdictCauseWith(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: "gateway hiccup"}, rules)
	if cause != FailUpstreamTransient || src != ClassifiedByAdded {
		t.Fatalf("live path: (%q, %q), want (%q, %q)", cause, src, FailUpstreamTransient, ClassifiedByAdded)
	}
}

// TestW51AddedModelRuleSwitchesCandidates: an additional model-class rule
// classifies its text exit_model_class on both paths.
func TestW51AddedModelRuleSwitchesCandidates(t *testing.T) {
	rules := w51Rules(t, nil, []string{`(?i)weird model failure`})
	cause, _, _, src, _ := classifyWaitFailureWith(&ExitedError{Step: "s", ExitCode: 1, LastError: "weird model failure (code 5)", TurnError: true}, rules)
	if cause != FailExitModel || src != ClassifiedByAdded {
		t.Fatalf("exit path: (%q, %q), want (%q, %q)", cause, src, FailExitModel, ClassifiedByAdded)
	}
	cause, src = verdictCauseWith(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: "weird model failure"}, rules)
	if cause != FailExitModel || src != ClassifiedByAdded {
		t.Fatalf("live path: (%q, %q), want (%q, %q)", cause, src, FailExitModel, ClassifiedByAdded)
	}
}

// TestW51AllowanceBeatsAddedRules: the allowance/auth row is judged before
// any additional rule, and it now covers the bare 402 — so no added rule
// (however broad) can pull an allowance or auth failure out of model class
// (AC2's three samples), with or without rules in play.
func TestW51AllowanceBeatsAddedRules(t *testing.T) {
	samples := []string{
		"HTTP 402: 余额不足，请充值",
		"402 Payment Required",
		"provider returned 401 Unauthorized",
	}
	for _, text := range samples {
		if cause, _, _, src, _ := classifyWaitFailureWith(&ExitedError{Step: "s", ExitCode: 1, LastError: text, TurnError: true}, nil); cause != FailExitModel || src != ClassifiedByBuiltin {
			t.Errorf("no rules: %q classified (%q, %q), want (%q, %q) — the allowance row must cover the bare 402", text, cause, src, FailExitModel, ClassifiedByBuiltin)
		}
	}
	broad := w51Rules(t, []string{`(?s).`}, []string{`(?s).`}) // matches everything
	for _, text := range samples {
		cause, _, _, src, _ := classifyWaitFailureWith(&ExitedError{Step: "s", ExitCode: 1, LastError: text, TurnError: true}, broad)
		if cause != FailExitModel || src != ClassifiedByBuiltin {
			t.Errorf("broad rules: %q classified (%q, %q), want (%q, %q) — the allowance row is judged first", text, cause, src, FailExitModel, ClassifiedByBuiltin)
		}
		cause, src = verdictCauseWith(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: text}, broad)
		if cause != FailExitModel || src != ClassifiedByBuiltin {
			t.Errorf("broad rules, live: %q classified (%q, %q), want (%q, %q)", text, cause, src, FailExitModel, ClassifiedByBuiltin)
		}
	}
}

// TestW51AddedRulesSitBeforeTheBuiltInRows: an added rule is judged after
// the allowance row and before every other built-in row — here an added
// model rule wins over the built-in rate-limit row.
func TestW51AddedRulesSitBeforeTheBuiltInRows(t *testing.T) {
	rules := w51Rules(t, nil, []string{`(?i)\blimit\b`})
	cause, _, _, src, _ := classifyWaitFailureWith(&ExitedError{Step: "s", ExitCode: 1, LastError: "rate limit exceeded, retry later", TurnError: true}, rules)
	if cause != FailExitModel || src != ClassifiedByAdded {
		t.Fatalf("added rules must sit before the built-in rate-limit row: (%q, %q)", cause, src)
	}
}

// TestW51AddedRulesNeverMatchTheDriversOwnError: the driver's own failure
// (a spawn or staging error) is never re-classified by an additional rule
// (Q7) — only the built-in rows read it, as before.
func TestW51AddedRulesNeverMatchTheDriversOwnError(t *testing.T) {
	rules := w51Rules(t, []string{`(?i)hiccup`}, []string{`(?i)hiccup`})
	cause, _, _, src, _ := classifyWaitFailureWith(errors.New("driver: step s spawn 1 failed: hiccup while staging the envelope"), rules)
	if cause == FailUpstreamTransient || cause == FailExitModel || src != "" {
		t.Fatalf("the driver's own error must not match an added rule: (%q, %q)", cause, src)
	}
	if cause != FailExitOther {
		t.Fatalf("an unmatched driver error stays exit_other: %q", cause)
	}
}

// TestW51ClassificationSources: builtin (a built-in row), added (a user
// pattern) and fallback (no rule matched) are reported as the source.
func TestW51ClassificationSources(t *testing.T) {
	cause, _, _, src, _ := classifyWaitFailureWith(&ExitedError{Step: "s", ExitCode: 1, LastError: "429 Too Many Requests", TurnError: true}, nil)
	if cause != FailRateLimited || src != ClassifiedByBuiltin {
		t.Fatalf("builtin: (%q, %q)", cause, src)
	}
	cause, _, _, src, _ = classifyWaitFailureWith(&ExitedError{Step: "s", ExitCode: 1, LastError: "a gateway tantrum nothing describes", TurnError: true}, nil)
	if cause != FailExitModel || src != ClassifiedByFallback {
		t.Fatalf("exit fallback: (%q, %q), want (%q, %q)", cause, src, FailExitModel, ClassifiedByFallback)
	}
	cause, src = verdictCauseWith(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: "a gateway tantrum nothing describes"}, nil)
	if cause != string(supervise.CauseModelError) || src != ClassifiedByFallback {
		t.Fatalf("live fallback: (%q, %q), want (%q, %q)", cause, src, supervise.CauseModelError, ClassifiedByFallback)
	}
	cause, src = verdictCauseWith(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: `model "x/y" not found`}, nil)
	if cause != FailExitModel || src != ClassifiedByBuiltin {
		t.Fatalf("live builtin promotion: (%q, %q)", cause, src)
	}
}

// TestW51StepFailedRecordsTheClassification: the step_failed event carries
// which rule judged the failure (classified_by) and the classified error
// text — here an added transient rule on a live error turn, and a fallback
// on an exited process.
func TestW51StepFailedRecordsTheClassification(t *testing.T) {
	env := newChainEnv(t, func(c *Config) {
		c.TextRulesFunc = func() TextRules {
			return TextRules{
				Transient: []*regexp.Regexp{regexp.MustCompile(`(?i)gateway hiccup`)},
			}
		}
	}, "s", "m1", "m2")
	env.sp.script = []fakeEntry{exitErr(1, "gateway hiccup, try again", true), done(`{"ok":true}`)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ev := env.log.byKind("step_failed")
	if len(ev) != 1 {
		t.Fatalf("step_failed events %d, want 1", len(ev))
	}
	body := ev[0].body
	if body["cause"] != FailUpstreamTransient {
		t.Fatalf("cause %v, want %q (the added rule owns the live error turn)", body["cause"], FailUpstreamTransient)
	}
	if body["classified_by"] != ClassifiedByAdded {
		t.Fatalf("classified_by %v, want %q", body["classified_by"], ClassifiedByAdded)
	}
	if body["error_text"] != "gateway hiccup, try again" {
		t.Fatalf("error_text %v, want the turn's error text", body["error_text"])
	}

	// a fallback: an exited error turn no row names
	env2 := newChainEnv(t, nil, "s", "m1", "m2")
	env2.sp.script = []fakeEntry{exitErr(1, "a gateway tantrum nothing describes", true), done(`{"ok":true}`)}
	if err := env2.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	ev = env2.log.byKind("step_failed")
	if len(ev) != 1 {
		t.Fatalf("step_failed events %d, want 1", len(ev))
	}
	if ev[0].body["classified_by"] != ClassifiedByFallback {
		t.Fatalf("classified_by %v, want %q (no rule matched)", ev[0].body["classified_by"], ClassifiedByFallback)
	}
	if ev[0].body["error_text"] != "a gateway tantrum nothing describes" {
		t.Fatalf("error_text %v, want the raw text", ev[0].body["error_text"])
	}
}
