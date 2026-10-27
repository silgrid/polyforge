package daemon

// Regression for polyforge#249, second lookup: a bare `pf answer <slug>`
// (no --question) resolves the one pending question from the capsule's
// execution.json. That lookup must address the canonical job too — a
// fix that canonicalized only the driver-handle lookup still refused
// this form with DAEMON-400 "no pending question found".

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

func TestAnswerBySlugWithoutQuestionIDResolvesThePendingOne(t *testing.T) {
	const (
		canonical = "wi_A0hzl6aqY3Eu"
		slug      = "polyforge#246"
		question  = "w246-repro-test-vs-yaml-quote"
	)
	aa := &answerAgent{answered: make(chan string, 1)}
	hub := newFakeHub()
	hub.canonical = map[string]string{slug: canonical}
	td := newTestDaemon(t, func(c *Config) {
		c.Hub = hub
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
	// claimed by id: the runtime alias table never learns the slug.
	td.submitClaim(t, canonical)
	td.waitMonitored(t, canonical)
	td.seedPendingQuestion(t, canonical, question, "repro", "pick A or B")

	raw, err := td.client.Answer(slug, cli.AnswerPayload{AnswerText: "A", AnsweredBy: "u_1"})
	if err != nil {
		t.Fatalf("bare answer by slug %q must resolve the single pending question: %v", slug, err)
	}
	var resp struct {
		QuestionID string `json:"question_id"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		t.Fatalf("decode answer response %s: %v", raw, err)
	}
	if resp.QuestionID != question {
		t.Fatalf("accepted question = %q, want %q", resp.QuestionID, question)
	}
	select {
	case q := <-aa.answered:
		if q != question {
			t.Fatalf("delivered question = %q, want %q", q, question)
		}
	default:
		t.Fatalf("the slug answer was accepted but never reached the driver's handle")
	}
}
