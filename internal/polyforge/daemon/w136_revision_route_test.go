package daemon

// w136_revision_route_test.go: the live-driver half of the owner revision
// door. The route must reach the running driver's single
// writer, not write execution.json beside it. The paused half is pinned in
// runtime/w136_revision_test.go; the human-only guard is pinned by the
// step-origin route matrix.

import (
	"context"
	"errors"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
)

type revisionAgent struct {
	Agent
	step        string
	corrections []capsule.BindingCorrection
	by          string
	revised     chan struct{}
}

func (a *revisionAgent) ReviseBinding(stepID string, corrections []capsule.BindingCorrection, by, reason string) error {
	a.step, a.corrections, a.by = stepID, corrections, by
	a.revised <- struct{}{}
	return nil
}

func TestReviseBindingRouteReachesTheLiveDriver(t *testing.T) {
	ra := &revisionAgent{revised: make(chan struct{}, 1)}
	td := newTestDaemon(t, func(c *Config) {
		inner := fakeEngineStarter()
		c.AgentStarter = func(ctx context.Context, in runtime.AgentStartInput) (Agent, error) {
			h, err := inner(ctx, in)
			if err != nil {
				return nil, err
			}
			ra.Agent = h
			return ra, nil
		}
	})
	td.submitClaim(t, "wi_rev_live")
	td.waitMonitored(t, "wi_rev_live")

	raw, err := td.client.ReviseBinding("wi_rev_live", cli.ReviseBindingPayload{
		StepID: "e2e-author",
		Corrections: []cli.BindingCorrectionPayload{
			{Index: 1, Command: "go test ./tests/e2e_cp37/ -count=1 -v"},
			{Index: 2, Command: "go test ./tests/e2e_cp37/ -run TestCapture -count=1 -v"},
		},
		By: "usr_owner", Reason: "w136 test",
	})
	if err != nil {
		t.Fatalf("revise-binding route: %v", err)
	}
	select {
	case <-ra.revised:
		if ra.step != "e2e-author" || len(ra.corrections) != 2 ||
			ra.corrections[0] != (capsule.BindingCorrection{Index: 1, Command: "go test ./tests/e2e_cp37/ -count=1 -v"}) ||
			ra.corrections[1] != (capsule.BindingCorrection{Index: 2, Command: "go test ./tests/e2e_cp37/ -run TestCapture -count=1 -v"}) ||
			ra.by != "usr_owner" {
			t.Fatalf("the driver got step=%q corrections=%+v by=%q, want the batch verbatim", ra.step, ra.corrections, ra.by)
		}
	default:
		t.Fatalf("the route answered %s without reaching the live driver", raw)
	}
}

func TestReviseBindingRouteRejectsAnIncompleteRequest(t *testing.T) {
	td := newTestDaemon(t, nil)
	_, err := td.client.ReviseBinding("wi_none", cli.ReviseBindingPayload{
		Corrections: []cli.BindingCorrectionPayload{{Index: 1, Command: "go test ./..."}}, By: "usr_owner",
	})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonBadInput {
		t.Fatalf("revise-binding without a step = %v, want DAEMON-400", err)
	}
}
