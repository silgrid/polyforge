package runtime

// claim_lost_answer_test.go — repo review 2026-10-16 B-1: a SessionClaim
// whose answer is lost (transport error, timeout, hub 5xx) may have been
// committed by the hub. The claiming journal entry — the only copy of the
// session secret — must survive it, and the retry must replay the SAME
// claim so a committed session is adopted instead of orphaned. Only the
// hub's typed 4xx refusal drops the entry.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

var errTransport = errors.New(`Post "http://hub/api/v1/work-items/x/claim": context deadline exceeded`)

func isHubUnavailable(err error) bool {
	var ae *api.Error
	return errors.As(err, &ae) && ae.Code == api.ErrDaemonHubUnavailable
}

func TestClaimAnswerLostAfterCommitKeepsJournalAndRetryAdopts(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	hub.claimFaults = []claimFault{{err: errTransport, afterCommit: true}}
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	const wi = "wi_b1lost"

	err := r.Start(context.Background(), wi, StartOpts{})
	if !isHubUnavailable(err) {
		t.Fatalf("a lost claim answer must answer the typed transient (requeue), got %v", err)
	}
	rec := r.loadClaiming()[wi]
	if rec == nil {
		t.Fatal("the claiming journal entry (the only copy of the secret) was dropped on a transport error")
	}
	if hub.held[wi] != rec.SessionID {
		t.Fatalf("test premise: the hub committed session %q, journal has %q", hub.held[wi], rec.SessionID)
	}

	// The dispatcher's retry: the same claim is replayed and adopted.
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("retry Start: %v", err)
	}
	t.Cleanup(func() { terminateJobAgent(r, wi) })
	if n := hub.claimCount(); n != 2 {
		t.Fatalf("claims = %d, want 2", n)
	}
	first, second := hub.claims[0], hub.claims[1]
	if second.SessionID != first.SessionID {
		t.Fatalf("the retry must replay the same claim: first %+v, second %+v", first, second)
	}
	if ji, ok := r.Info(wi); !ok || ji.SessionID != first.SessionID {
		t.Fatalf("the retry must adopt the committed session %q, job = %+v (ok=%v)", first.SessionID, ji, ok)
	}
	if items := r.loadClaiming(); len(items) != 0 {
		t.Fatalf("the settled start must clear the journal, got %+v", items)
	}
	if hub.endCount() != 0 {
		t.Fatalf("no session may be ended on the adopt path, got %d ends", hub.endCount())
	}
}

// A lost answer whose claim never landed: the retry finds the journaled
// session not holding the WI, drops the entry and claims fresh.
func TestClaimLostBeforeCommitRetryClaimsFresh(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	hub.claimFaults = []claimFault{{err: &api.Error{Code: "HTTP-502", Detail: "bad gateway"}}}
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	const wi = "wi_b1before"

	if err := r.Start(context.Background(), wi, StartOpts{}); !isHubUnavailable(err) {
		t.Fatalf("a hub 5xx on the claim must answer the typed transient, got %v", err)
	}
	if r.loadClaiming()[wi] == nil {
		t.Fatal("a 5xx claim answer must keep the journal entry")
	}
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("retry Start: %v", err)
	}
	t.Cleanup(func() { terminateJobAgent(r, wi) })
	if hub.claims[1].SessionID == hub.claims[0].SessionID {
		t.Fatal("a claim that never landed must be retried with a fresh session, not replayed")
	}
	if ji, ok := r.Info(wi); !ok || ji.SessionID != hub.claims[1].SessionID {
		t.Fatalf("job = %+v (ok=%v), want the fresh session", ji, ok)
	}
	if items := r.loadClaiming(); len(items) != 0 {
		t.Fatalf("journal must be settled, got %+v", items)
	}
}

// The hub's typed refusal: the claim did not open — the entry is cleared
// and the refusal surfaces as is (no requeue).
func TestClaimTypedRefusalClearsJournal(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	refusal := &api.Error{Code: api.ErrCoreConflict, Detail: "NOT_CLAIMABLE"}
	hub.claimFaults = []claimFault{{err: refusal}}
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})

	err := r.Start(context.Background(), "wi_b1refused", StartOpts{})
	if err == nil || isHubUnavailable(err) || !errors.Is(err, refusal) {
		t.Fatalf("a typed 4xx refusal must surface as is, got %v", err)
	}
	if items := r.loadClaiming(); len(items) != 0 {
		t.Fatalf("a typed refusal must clear the journal, got %+v", items)
	}
}

// The restart replay: a claim replay whose answer is lost keeps the
// entry, and the work item's actor retries until it adopts the journaled
// session with the same key.
func TestJournalReplayTransportErrorKeepsEntry(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.EndRetryBase = 10 * time.Millisecond
	r.cfg.EndRetryMax = 20 * time.Millisecond
	const wi = "wi_b1replay"
	session := fakeSession(wi)
	if err := r.noteClaiming(wi, session, nil); err != nil {
		t.Fatal(err)
	}
	hub.hold(wi, session, api.StatusRunning)
	hub.claimFaults = []claimFault{{err: errTransport}}

	replayClaims(t, r)
	t.Cleanup(func() { terminateJobAgent(r, wi) })
	if ji, ok := r.Info(wi); !ok || ji.SessionID != session {
		t.Fatalf("the retried replay must adopt the journaled session, job = %+v (ok=%v)", ji, ok)
	}
	hub.mu.Lock()
	claims := append([]api.SessionClaimRequest(nil), hub.claims...)
	hub.mu.Unlock()
	if len(claims) != 2 || claims[0].SessionID != session || claims[1].SessionID != session {
		t.Fatalf("claims = %+v, want the lost one and its replay, both under the journaled session", claims)
	}
	if items := r.loadClaiming(); len(items) != 0 {
		t.Fatalf("journal must be settled, got %+v", items)
	}
}

// The claim body carries exactly the work item, the session secret (the
// replay key, D4) and the machine: aihub reads nothing else, so nothing
// else is minted or sent.
func TestClaimBodyCarriesOnlyTheReplayKey(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	const wi = "wi_claimbody"
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { terminateJobAgent(r, wi) })
	hub.mu.Lock()
	req := hub.claims[0]
	hub.mu.Unlock()
	blob, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal(blob, &body); err != nil {
		t.Fatal(err)
	}
	if len(body) != 3 || body["work_item_id"] != wi || body["session_id"] == "" || body["machine_id"] == "" {
		t.Fatalf("claim body = %v, want exactly work_item_id, session_id and machine_id", body)
	}
}
