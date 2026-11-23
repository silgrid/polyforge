package daemon

// autoanswer_test.go: the daemon's face of the automatic reply's reserved
// identity (answer.go; polyforge#239). `pf answer` may not claim the
// daemon's author: the refusal is DAEMON-400 and nothing is recorded,
// whether or not the work item exists.

import (
	"errors"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

func TestAutoAnswerReservedIdentityRefused(t *testing.T) {
	td := newTestDaemon(t, nil)
	_, err := td.client.Answer("wi_no_such_work_item", cli.AnswerPayload{
		AnswerText: "按推荐继续", AnsweredBy: capsule.AutoAnswerBy,
	})
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonBadInput {
		t.Fatalf("pf answer with the reserved author = %v, want DAEMON-400", err)
	}
}
