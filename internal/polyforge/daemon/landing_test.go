package daemon

// landing_test.go (polyforge#245 245-lane): the daemon's landing-lease
// manager. The private journal carries each request id before the first
// acquire; a start releases the journaled grants by request id (lookup
// answers the key) before any new request queues; a renewal failure voids
// the whole cycle and Check refuses; a token-less release of a request
// whose grant the server no longer knows drops the journal entry without a
// machine-wide delete. The fixture hub is a fake; the real server's lease
// routes are pinned by 245-lease's own tests.

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
)

type fakeLeaseHub struct {
	grants   map[string]*api.LeaseGrant
	queued   map[string]bool
	releases []api.LeaseRelease
	renews   int
	lookup   int
	renewErr error
	refuse   map[string]bool
}

func newFakeLeaseHub() *fakeLeaseHub {
	return &fakeLeaseHub{grants: map[string]*api.LeaseGrant{}, queued: map[string]bool{}, refuse: map[string]bool{}}
}

func (h *fakeLeaseHub) AcquireLease(_ string, req api.LeaseAcquire) (*api.LeaseGrant, error) {
	if h.refuse[req.Repo] {
		return nil, errors.New("the hub is unreachable")
	}
	if g := h.grants[req.RequestID]; g != nil {
		return &api.LeaseGrant{Kind: leaseKind, Key: g.Key, RequestID: req.RequestID, Status: "held"}, nil
	}
	if !h.queued[req.RequestID] {
		h.queued[req.RequestID] = true
		return &api.LeaseGrant{Kind: leaseKind, Key: "k/" + req.Repo, RequestID: req.RequestID, Status: "queued", Position: 1}, nil
	}
	g := &api.LeaseGrant{Kind: leaseKind, Key: "k/" + req.Repo, RequestID: req.RequestID, Status: "granted", Token: "tok-" + req.Repo}
	h.grants[req.RequestID] = g
	return g, nil
}

func (h *fakeLeaseHub) RenewLease(_ string, req api.LeaseRenew) (*api.LeaseGrant, error) {
	h.renews++
	if h.renewErr != nil {
		return nil, h.renewErr
	}
	return &api.LeaseGrant{Kind: leaseKind, Key: req.Key, RequestID: req.RequestID, Status: "granted"}, nil
}

func (h *fakeLeaseHub) ReleaseLease(_ string, req api.LeaseRelease) (*api.LeaseGrant, error) {
	h.releases = append(h.releases, req)
	delete(h.grants, req.RequestID)
	return &api.LeaseGrant{Kind: leaseKind, Key: req.Key, RequestID: req.RequestID, Status: "released"}, nil
}

func (h *fakeLeaseHub) LookupLease(requestID, _ string) (*api.LeaseGrant, error) {
	h.lookup++
	if g := h.grants[requestID]; g != nil {
		return g, nil
	}
	return nil, &api.Error{Code: "LEASE_NOT_QUEUED", Detail: "no such request"}
}

// landingCapsule writes a capsule whose fixed target branch is epic/7.
func landingCapsule(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "runtime-state"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "runtime-state", "target-branch.json"),
		[]byte(`{"target":"epic/7"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestIntervalLeaseRenewsAndReleases(t *testing.T) {
	hub := newFakeLeaseHub()
	journalPath := filepath.Join(t.TempDir(), leaseJournalFile)
	l := newLeaseManager(hub, journalPath)
	l.poll = time.Millisecond
	dir := landingCapsule(t)
	var positions []int
	hold, err := l.Hold(context.Background(), "wi_1", dir, []string{"o/app", "o/lib"}, func(pos int, _ time.Time) {
		positions = append(positions, pos)
	})
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	if len(positions) == 0 || positions[0] != 1 {
		t.Fatalf("queue positions %v, want the first position announced", positions)
	}
	entries, err := l.journal.Entries()
	if err != nil || len(entries) != 2 {
		t.Fatalf("journal entries %v (%v), want two request ids", entries, err)
	}
	if err := hold.Check(context.Background()); err != nil {
		t.Fatalf("Check: %v", err)
	}
	if hub.renews != 2 {
		t.Fatalf("renews %d, want one per repository", hub.renews)
	}
	hold.Release()
	if len(hub.releases) != 2 {
		t.Fatalf("releases %d, want one per repository", len(hub.releases))
	}
	for _, r := range hub.releases {
		if r.Token == "" {
			t.Fatalf("the holder released without its token: %+v", r)
		}
	}
	if entries, _ := l.journal.Entries(); len(entries) != 0 {
		t.Fatalf("journal after release %v, want empty", entries)
	}
}

func TestLandingCheckRefusesAfterLoss(t *testing.T) {
	hub := newFakeLeaseHub()
	l := newLeaseManager(hub, filepath.Join(t.TempDir(), leaseJournalFile))
	l.poll = time.Millisecond
	dir := landingCapsule(t)
	hold, err := l.Hold(context.Background(), "wi_1", dir, []string{"o/app"}, nil)
	if err != nil {
		t.Fatalf("Hold: %v", err)
	}
	defer hold.Release()
	hub.renewErr = errors.New("the token does not match the lease holder")
	if err := l.Check(context.Background(), "wi_1", "o/app"); err == nil {
		t.Fatal("Check passed after a renewal failure, want it refused")
	}
	// a late success never revives the void cycle
	hub.renewErr = nil
	if err := l.Check(context.Background(), "wi_1", "o/app"); err == nil {
		t.Fatal("Check passed on a cycle a renewal already voided")
	}
	if err := l.Check(context.Background(), "wi_1", "o/other"); err == nil {
		t.Fatal("Check passed for a repository the cycle does not hold")
	}
}

func TestLandingRecoveryReleasesBeforeNewCycles(t *testing.T) {
	hub := newFakeLeaseHub()
	journalPath := filepath.Join(t.TempDir(), leaseJournalFile)
	journal := aihubclient.NewLeaseJournal(journalPath)
	if err := journal.Add(aihubclient.LeaseJournalEntry{
		RequestID: "lr_old", Kind: leaseKind, WorkItem: "wi_1", Cycle: "lc_old",
	}); err != nil {
		t.Fatal(err)
	}
	// the server still holds the old grant, at its server-side key
	hub.grants["lr_old"] = &api.LeaseGrant{Kind: leaseKind, Key: "k/o/app", RequestID: "lr_old", Status: "held"}
	l := newLeaseManager(hub, journalPath)
	l.poll = time.Millisecond
	dir := landingCapsule(t)
	hold, err := l.Hold(context.Background(), "wi_1", dir, []string{"o/app"}, nil)
	if err != nil {
		t.Fatalf("Hold after recovery: %v", err)
	}
	defer hold.Release()
	if hub.lookup != 1 {
		t.Fatalf("lookups %d, want the journaled request looked up once", hub.lookup)
	}
	var recovered *api.LeaseRelease
	for i := range hub.releases {
		if hub.releases[i].RequestID == "lr_old" {
			recovered = &hub.releases[i]
		}
	}
	if recovered == nil || recovered.Token != "" || recovered.Key != "k/o/app" {
		t.Fatalf("recovery release %+v, want the old grant released by request id (no token)", recovered)
	}
	entries, _ := journal.Entries()
	for _, e := range entries {
		if e.RequestID == "lr_old" {
			t.Fatalf("the released request id stayed in the journal: %v", entries)
		}
	}
}

func TestLandingRecoveryBlocksOnUnknownState(t *testing.T) {
	hub := newFakeLeaseHub()
	journalPath := filepath.Join(t.TempDir(), leaseJournalFile)
	journal := aihubclient.NewLeaseJournal(journalPath)
	if err := journal.Add(aihubclient.LeaseJournalEntry{RequestID: "lr_lost", WorkItem: "wi_1"}); err != nil {
		t.Fatal(err)
	}
	// the hub cannot confirm: no Listen replaces the lookup's answer here —
	// a request with no grant answers LEASE_NOT_QUEUED (expired, safe)
	l := newLeaseManager(hub, journalPath)
	l.poll = time.Millisecond
	if _, err := l.Hold(context.Background(), "wi_1", landingCapsule(t), []string{"o/app"}, nil); err != nil {
		t.Fatalf("a request the server no longer knows must not block (it expired): %v", err)
	}
}

func TestIntervalLeaseRepoReleasesOne(t *testing.T) {
	hub := newFakeLeaseHub()
	l := newLeaseManager(hub, filepath.Join(t.TempDir(), leaseJournalFile))
	l.poll = time.Millisecond
	release, err := l.HoldRepo(context.Background(), "wi_1", landingCapsule(t), "https://github.com/o/app")
	if err != nil {
		t.Fatalf("HoldRepo: %v", err)
	}
	if err := l.Check(context.Background(), "wi_1", "https://github.com/o/app"); err != nil {
		t.Fatalf("Check: %v", err)
	}
	release()
	release() // idempotent
	if len(hub.releases) != 1 {
		t.Fatalf("releases %d, want one", len(hub.releases))
	}
	if entries, _ := l.journal.Entries(); len(entries) != 0 {
		t.Fatalf("journal %v, want empty", entries)
	}
}
