package daemon

// close_during_claim_test.go — a wrap of a work item whose start is still
// on its claim's hub calls (no CLAIMING placeholder yet) queues behind the
// start and answers its final outcome: the claimed job is sealed once.

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

func TestCloseDuringClaimHubCallGetsTheFinalAnswer(t *testing.T) {
	td := newTestDaemon(t, nil)
	const wi = "wi_close_claiming"
	release := make(chan struct{})
	entered := make(chan struct{})
	td.hub.mu.Lock()
	td.hub.status[wi] = api.StatusQueued
	// the first claim's answer is lost; the requeued start verifies the
	// journaled claim, and that verify hangs
	td.hub.claimLost = 1
	td.hub.blockVerify = release
	td.hub.verifyEntered = entered
	td.hub.mu.Unlock()
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	mkBare(t, td.cfg.GitRemoteBase, wi)
	if _, err := td.client.ClaimWithModels(wi, "normal", nil); err != nil {
		t.Fatalf("claim: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(20 * time.Second):
		t.Fatal("the requeued start never reached its journal verify")
	}
	wrapped := make(chan error, 1)
	go func() {
		raw, err := td.client.Wrap(wi, "")
		if err == nil && !strings.Contains(string(raw), `"outcome":"sealed"`) {
			err = fmt.Errorf("wrap answer %s, want sealed", raw)
		}
		wrapped <- err
	}()
	// the wrap is not refused while the claim's verify hangs
	select {
	case err := <-wrapped:
		t.Fatalf("wrap answered while the claim's hub call hangs: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-wrapped:
		if err != nil {
			t.Fatalf("wrap queued behind the claim: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("the wrap never got its answer once the claim settled")
	}
	waitGone(t, td, wi)
	if e := td.hub.lastEnd(); td.hub.endCount() != 1 || e.Status != api.StatusWrapped {
		t.Fatalf("SessionEnds = %d (last %+v), want the one wrapped close", td.hub.endCount(), e)
	}
}
