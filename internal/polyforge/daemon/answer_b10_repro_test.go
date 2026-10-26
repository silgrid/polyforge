package daemon

// Regression test for wi_WLczIJMOLDhq item B10 (2026-10-13 audit): an
// answer that names NO question id is routed to pendingQuestionID,
// which walks the capsule's execution.json steps map and hands the
// FIRST pending question it happens to hit to the bridge — with two or
// more pending questions the answer goes to an ARBITRARY one (map
// iteration order), never to the one the operator meant, and never to
// a typed refusal.
//
// Expected behavior per the work item: without a question id the
// answer is accepted only when EXACTLY one pending question exists;
// with zero or multiple it must return a typed error that points at
// `--question` instead of guessing.
//
// Regression test: before the fix, with two pending questions the no-id
// answer was delivered to one of them.

import (
	"context"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

// A no-question-id answer with TWO pending questions on the capsule is
// refused typed (pointing at --question); it is never delivered to an
// arbitrary pending question.
func TestAnswerWithoutIDWithTwoPendingQuestionsIsRefused(t *testing.T) {
	aa := &answerAgent{answered: make(chan string, 1)}
	td := newTestDaemon(t, func(c *Config) {
		inner := fakeEngineStarter()
		c.AgentStarter = func(ctx context.Context, in runtime.AgentStartInput) (Agent, error) {
			h, err := inner(ctx, in)
			if err != nil {
				return nil, err
			}
			aa.Agent = h
			return aa, nil
		}
	})
	td.submitClaim(t, "wi_b10")
	td.waitMonitored(t, "wi_b10")

	// two open pending questions on the capsule (the multi-question
	// shape the work item reports).
	td.seedPendingQuestion(t, "wi_b10", "q_b10_first", "repro", "first open question")
	td.seedPendingQuestion(t, "wi_b10", "q_b10_second", "grill", "second open question")

	raw, err := td.client.Answer("wi_b10", cli.AnswerPayload{AnswerText: "an answer", AnsweredBy: "u_1"})
	if err == nil {
		t.Fatalf("REPORTED FAILURE (B10): answer with no question id and TWO pending questions was accepted: %s", raw)
	}
	select {
	case q := <-aa.answered:
		t.Fatalf("REPORTED FAILURE (B10): the no-id answer was delivered to pending question %q (arbitrary map-order pick) instead of a typed refusal", q)
	default:
	}
	if !strings.Contains(err.Error(), "--question") {
		t.Fatalf("the typed refusal must point the operator at --question, got: %v", err)
	}
}
