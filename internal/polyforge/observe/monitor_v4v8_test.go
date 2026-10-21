package observe

// monitor_v4v8_test.go: the V8 gate-repair regression at the monitor
// level — the local-gitlink precheck refusal classifies as
// push_poisoned_local (never push_failed); its retries are bounded by the
// push backoff (backpressure_test.go).

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
)

func TestPushPoisonedLocalClassifiesItsOwnEvent(t *testing.T) {
	h := newFakeHandle("att_1")
	m, _ := newTestMonitor(t, h, func(c *monitorCfg) {
		c.push = func(ctx context.Context, o gitops.Opts, msg string) (string, bool, gitops.PushTimings, error) {
			return "", false, gitops.PushTimings{}, &api.Error{
				Code:   api.ErrGitGitlink,
				Detail: "LOCAL_GITLINK_DETECTED: staged gitlink (mode 160000) at poison — cleanup omitted",
			}
		}
	})
	m.writeExec(t, 1, "code")
	soon(t, 3*time.Second, "a poisoned-local classification event", func() bool {
		return len(eventsOf(m, EvPushPoisonedLocal)) >= 1
	})
	if n := len(eventsOf(m, EvPushFailed)); n != 0 {
		t.Fatalf("the local precheck refusal must never surface as push_failed, got %d", n)
	}
	// The event carries the not-an-M8-failure rationale for the reader.
	e := eventsOf(m, EvPushPoisonedLocal)[0]
	var body struct {
		Error  string `json:"error"`
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(e.Body, &body); err != nil {
		t.Fatalf("body: %v (%s)", err, e.Body)
	}
	if !containsAll(body.Error, "LOCAL_GITLINK_DETECTED") || !containsAll(body.Detail, "M8") {
		t.Fatalf("event must carry the error and the exclusion rationale: %+v", body)
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
