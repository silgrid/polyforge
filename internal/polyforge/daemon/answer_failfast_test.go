package daemon

// answer_failfast_test.go: pins the answer criterion's fail-fast posture
// on the daemon's answer route (polyforge#106) — the legacy no-bridge-
// handle relay fallback is deleted, so a request the driver branch
// cannot consume fails fast with a typed error and no relay executes.
// (The plan named this pin for the cli package; it lives here because
// the route and the in-process fakes it needs are daemon-side.)

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

func TestAnswerWithoutLiveBridgeHandleFailsFast(t *testing.T) {
	td := newTestDaemon(t, nil)
	payload := cli.AnswerPayload{
		QuestionID: "q_ff",
		AnswerText: "fail fast", AnsweredBy: "u_1",
	}

	// (1) Not monitored at all (an idle daemon): the observe fallthrough
	// shape — typed 404, never a success.
	if _, err := td.client.Answer("wi_ghost", payload); err == nil {
		t.Fatal("answer for an unmonitored work item was accepted")
	} else {
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Code != api.ErrDaemonNotFound {
			t.Fatalf("unmonitored answer error = %v, want DAEMON-404", err)
		}
	}

	// (2) Monitored, but the scripted engine carries no bridge Handle
	// (the between-derivations shape): typed 409 naming the missing
	// live driver — and no relay path executes anywhere.
	td.submitClaim(t, "wi_ff")
	td.waitMonitored(t, "wi_ff")
	raw, err := td.client.Answer("wi_ff", payload)
	if err == nil {
		t.Fatalf("answer with no live driver was accepted: %s", raw)
	}
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonConflict {
		t.Fatalf("no-driver answer error = %v, want DAEMON-409", err)
	}
	if !strings.Contains(ae.Detail, "no live driver") {
		t.Fatalf("the refusal must name the missing live driver: %q", ae.Detail)
	}
}

// answerAgent is a fake engine handle that carries the driver's Answer
// method, so the answer route takes its driver branch.
type answerAgent struct {
	Agent
	answered chan string
}

func (a *answerAgent) Answer(questionID, answer, answeredBy string) error {
	a.answered <- questionID
	return nil
}

// A driver-branch answer that names no question while the capsule carries
// no pending one is refused DAEMON-400 and never reaches the handle.
func TestAnswerWithNoPendingQuestionIsRefused(t *testing.T) {
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
	td.submitClaim(t, "wi_nq")
	td.waitMonitored(t, "wi_nq")
	_, err := td.client.Answer("wi_nq", cli.AnswerPayload{AnswerText: "x", AnsweredBy: "u_1"})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonBadInput {
		t.Fatalf("answer with no pending question = %v, want DAEMON-400", err)
	}
	select {
	case q := <-aa.answered:
		t.Fatalf("the handle must not be reached, got question %q", q)
	default:
	}
}
