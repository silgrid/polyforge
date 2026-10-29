package runtime

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
)

// TestStopOnClaimWithExhaustedReplayChainSaysSo: a restart's claim replay
// whose aihub stays unreachable past the retry window stops retrying; a
// close of that CLAIMING placeholder must then say the chain is exhausted
// and how to recover, not "retry later" as if something still retried it.
func TestStopOnClaimWithExhaustedReplayChainSaysSo(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.EndRetryBase = 5 * time.Millisecond
	r.cfg.EndRetryMax = 10 * time.Millisecond
	r.cfg.EndRetryWindow = 100 * time.Millisecond
	t.Cleanup(r.Close)
	var exhausted atomic.Int32
	r.cfg.EventTap = func(_ string, kind string, _ map[string]any) {
		if kind == EventRetryExhausted {
			exhausted.Add(1)
		}
	}
	const wi = "wi_claim_replay_exhausted"
	session := fakeSession(wi)
	if err := r.noteClaiming(wi, session, nil); err != nil {
		t.Fatal(err)
	}
	hub.mu.Lock()
	hub.lookupErr[wi] = errors.New("dial tcp: connection refused") // aihub stays unreachable
	hub.mu.Unlock()

	r.putJob(&job{wi: wi, sessionID: session, state: StateClaiming}) // the restart's placeholder
	// the left-over placeholder would hold the rig's settle check
	t.Cleanup(func() {
		if j := r.jobOf(wi); j != nil {
			r.dropJob(j)
		}
	})
	r.post(wi, func() { r.reconcile(wi) })
	waitFor(t, "the claim replay's retry chain to exhaust", func() bool { return exhausted.Load() > 0 })

	err := r.Stop(context.Background(), wi, protocol.FinishWrapped)
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonHubUnavailable {
		t.Fatalf("a close of the CLAIMING placeholder must answer DAEMON-503, got %v", err)
	}
	if !strings.Contains(ae.Detail, "retry chain is exhausted") || !strings.Contains(ae.Detail, "restart the daemon") ||
		strings.Contains(ae.Detail, "retry later") {
		t.Fatalf("the exhausted claim replay's answer must say nothing retries it and how to recover, got %q", ae.Detail)
	}
}
