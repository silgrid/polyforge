package daemon

// cancel_answer_test.go: the reserved cancel answer is authorized by the
// hub's cancel gate — the daemon probes the cancel door (dry_run) with
// its own credential before relaying anything; a refusal leaves the
// question pending and never reaches the driver, and the self-asserted
// answered_by never authorizes. An ordinary answer probes nothing.

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

func TestCancelAnswerIsAuthorizedByTheHub(t *testing.T) {
	const wi, question = "wi_cancelans", "continue_or_stop"
	aa := &answerAgent{answered: make(chan string, 2)}
	hub := newFakeHub()
	var mu sync.Mutex
	var probes []api.CancelRequest
	allow := false
	hub.memDo = func(method, path string, body, out any) error {
		if path != cancelDoorPath(wi) {
			return &api.Error{Code: api.ErrDaemonInternal, Detail: "unexpected " + path}
		}
		mu.Lock()
		defer mu.Unlock()
		req, _ := body.(api.CancelRequest)
		probes = append(probes, req)
		if !allow {
			return &api.Error{Code: api.ErrAuthForbidden, Detail: "cancel requires reporter, maintainer or admin"}
		}
		return nil
	}
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
	td.submitClaim(t, wi)
	td.waitMonitored(t, wi)
	td.seedPendingQuestion(t, wi, question, "grill", "continue or stop?")

	// unauthorized — even when answered_by claims an admin — is refused
	if _, err := td.client.Answer(wi, cli.AnswerPayload{QuestionID: question, AnswerText: " CANCEL ", AnsweredBy: "admin"}); err == nil {
		t.Fatal("an unauthorized cancel answer must be refused")
	}
	select {
	case q := <-aa.answered:
		t.Fatalf("a refused cancel answer must never reach the driver (got %s)", q)
	case <-time.After(200 * time.Millisecond):
	}
	mu.Lock()
	if len(probes) != 1 || !probes[0].DryRun || probes[0].QuestionID != question || probes[0].Reason != " CANCEL " {
		t.Fatalf("the daemon probes the hub's cancel door (dry_run) with the verbatim answer: %+v", probes)
	}
	allow = true
	mu.Unlock()

	// authorized: probed, then relayed
	if _, err := td.client.Answer(wi, cli.AnswerPayload{QuestionID: question, AnswerText: "cancel", AnsweredBy: "u_1"}); err != nil {
		t.Fatalf("an authorized cancel answer is relayed: %v", err)
	}
	select {
	case <-aa.answered:
	case <-time.After(5 * time.Second):
		t.Fatal("the authorized cancel answer never reached the driver")
	}
	// an ordinary answer probes nothing
	if _, err := td.client.Answer(wi, cli.AnswerPayload{QuestionID: question, AnswerText: "cancel the flag, keep the rest", AnsweredBy: "u_1"}); err != nil {
		t.Fatalf("an ordinary answer: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(probes) != 2 {
		t.Fatalf("only the reserved answer is probed, got %d probes", len(probes))
	}
}
