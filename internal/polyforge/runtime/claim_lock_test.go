package runtime

// claim_lock_test.go — a start claims on its canonical id's actor: two
// starts of one work item under different spellings meet in one mailbox
// (never two claims), and a claim whose compensation has not landed is
// kept end_pending — refusing the next start until it lands, and ending
// blocked at a restart.

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// Starts of one work item by slug and by id, either first, with the first
// hung at its probe, its journal verify or its claim: the work item is
// claimed exactly once and exactly one start succeeds.
func TestSlugAndIDStartsClaimOnce(t *testing.T) {
	const a, slug = "wi_cl_a", "proj#41"
	for _, kind := range []string{"probe", "verify", "claim"} {
		for _, first := range []string{a, slug} {
			second := slug
			if first == slug {
				second = a
			}
			t.Run(kind+"/"+first+"-first", func(t *testing.T) {
				setGitIdentity(t)
				hub := newFakeHub()
				r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
				t.Cleanup(func() { r.Close(); terminateJobAgent(r, a) })
				hub.canonical = map[string]string{slug: a}
				ctx := context.Background()
				wantClaims := 1
				if kind == "verify" {
					hub.claimFaults = []claimFault{{err: errors.New("connection reset (scripted)"), afterCommit: true}}
					if err := r.Start(ctx, a, StartOpts{}); !isTransient(err) {
						t.Fatalf("lost-answer Start = %v, want the typed transient", err)
					}
					wantClaims = 2
				}
				var g *hubGate
				if kind == "probe" {
					g = hub.hangCallAs(t, "probe", first)
				} else {
					g = hub.hangCall(t, kind, a) // goes out under the canonical id
				}
				firstErr := make(chan error, 1)
				go func() { firstErr <- r.Start(ctx, first, StartOpts{}) }()
				g.waitEntered(t, kind+" of "+first)

				secondCh := make(chan error, 1)
				go func() { secondCh <- r.Start(ctx, second, StartOpts{}) }()
				if kind != "probe" {
					stillPending(t, "the second start", secondCh)
				}
				g.open()
				err1 := recvErr(t, firstErr, "the hung start")
				secondErr := recvErr(t, secondCh, "the second start")
				ok := 0
				for _, err := range []error{err1, secondErr} {
					if err == nil {
						ok++
					}
				}
				if ok != 1 {
					t.Fatalf("starts answered %v and %v, want exactly one success", err1, secondErr)
				}
				if n := claimsOf(hub, a); n != wantClaims {
					t.Fatalf("%s claimed %d times, want %d", a, n, wantClaims)
				}
				if ji, ok := r.Info(a); !ok || ji.State != StateRunning {
					t.Fatalf("%s = %s (ok=%v), want RUNNING", a, ji.State, ok)
				}
				if journaled(r, a) != nil || journaled(r, slug) != nil {
					t.Fatal("a settled start left a claiming-journal entry")
				}
			})
		}
	}
}

// The claim goes out (and is journaled) under the canonical id, whatever
// spelling Start was asked for.
func TestSlugStartClaimsUnderCanonicalID(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	const a, slug = "wi_cl_canon", "proj#42"
	t.Cleanup(func() { r.Close(); terminateJobAgent(r, a) })
	hub.canonical = map[string]string{slug: a}
	g := hub.hangCall(t, "claim", a)
	done := make(chan error, 1)
	go func() { done <- r.Start(context.Background(), slug, StartOpts{}) }()
	g.waitEntered(t, "the claim")
	if rec := journaled(r, a); rec == nil || rec.WorkItemID != a || journaled(r, slug) != nil {
		t.Fatalf("journal during the claim = %+v (slug key %+v), want the entry under %s", rec, journaled(r, slug), a)
	}
	g.open()
	if err := recvErr(t, done, "the start"); err != nil {
		t.Fatal(err)
	}
	hub.mu.Lock()
	got := hub.claims[0].WorkItemID
	hub.mu.Unlock()
	if got != a {
		t.Fatalf("SessionClaim went out for %q, want the canonical %q", got, a)
	}
	if id := r.Resolve(slug); id != a {
		t.Fatalf("Resolve(%s) = %q, want %s", slug, id, a)
	}
}

// A probe that answers no id claims nothing.
func TestStartRefusesProbeWithoutID(t *testing.T) {
	hub := &noIDHub{fakeHub: newFakeHub()}
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	if err := r.Start(context.Background(), "wi_cl_noid", StartOpts{}); err == nil {
		t.Fatal("a start whose probe answered no id succeeded")
	}
	if n := hub.claimCount(); n != 0 || len(r.loadClaiming()) != 0 {
		t.Fatalf("the refused start claimed %d time(s) / journaled %+v", n, r.loadClaiming())
	}
}

type noIDHub struct{ *fakeHub }

func (h *noIDHub) GetWorkItem(idOrSlug string) (*api.WorkItem, error) {
	w, err := h.fakeHub.GetWorkItem(idOrSlug)
	if w != nil {
		w.ID = ""
	}
	return w, err
}

// A failed start whose compensation SessionEnd does not land keeps its
// session as an end_pending job (never a dangling running session): a
// Start of the work item meanwhile refuses without claiming (never a
// replay of the claim being ended); once the retry lands the close the
// job is gone and the work item starts afresh.
func TestClaimEndRetryRefusesStart(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.EndRetryBase = 20 * time.Millisecond
	r.cfg.EndRetryMax = 40 * time.Millisecond
	const bad = "wi_cl_ending"
	t.Cleanup(func() { r.Close(); terminateJobAgent(r, bad) })
	hub.reposFor = map[string][]api.ProjectRepo{bad: {{Name: "app", URL: "/nonexistent/app.git"}}}
	hub.mu.Lock()
	hub.end[bad] = errors.New("hub unreachable (scripted)")
	hub.mu.Unlock()
	if err := r.Start(context.Background(), bad, StartOpts{}); err == nil {
		t.Fatal("the start whose clone fails succeeded")
	}
	if journaled(r, bad) != nil {
		t.Fatal("the compensation kept the claiming-journal entry next to its end_pending record")
	}
	ji, ok := r.Info(bad)
	if !ok || ji.State != StateEndPending {
		t.Fatalf("after the failed compensation: %+v (ok=%v), want the end_pending job", ji, ok)
	}
	if recs := loadRecords(t, r); len(recs) != 1 || recs[0].State != string(StateEndPending) || recs[0].SessionID != ji.SessionID {
		t.Fatalf("state.json = %+v, want the end_pending record carrying the claim's session", recs)
	}
	claims := hub.claimCount()
	for i := 0; i < 3; i++ {
		if err := r.Start(context.Background(), bad, StartOpts{}); err == nil {
			t.Fatal("a Start during the compensation retry succeeded")
		}
		time.Sleep(30 * time.Millisecond)
	}
	if n := hub.claimCount() - claims; n != 0 {
		t.Fatalf("a start during the compensation retry claimed %d time(s)", n)
	}
	hub.mu.Lock()
	delete(hub.end, bad)
	hub.reposFor = nil
	hub.mu.Unlock()
	waitFor(t, "the compensation retry to land", func() bool { return r.jobOf(bad) == nil })
	if last := hub.lastEnd(); last.Status != api.StatusBlocked || last.SessionID != ji.SessionID {
		t.Fatalf("the retried close = %+v, want blocked under the claim's session", last)
	}
	hub.forceEnd(bad) // the owner's resume of the blocked work item
	if err := r.Start(context.Background(), bad, StartOpts{}); err != nil {
		t.Fatalf("Start after the compensation landed: %v", err)
	}
}

// At a restart an abandoned claim's end_pending record is ended blocked
// with its note — its claim is never replayed; a close that still fails
// keeps the record for the retry.
func TestRestartEndsEndingJournalEntry(t *testing.T) {
	for _, landed := range []bool{true, false} {
		name := "lands"
		if !landed {
			name = "keeps-failing"
		}
		t.Run(name, func(t *testing.T) {
			setGitIdentity(t)
			hub := newFakeHub()
			r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
			r.cfg.EndRetryBase = time.Hour // the retry stays out of the way
			t.Cleanup(r.Close)
			const x = "wi_cl_restart_ending"
			sess := fakeSession(x)
			writeStateFile(t, r, []*jobRecord{{WorkItemID: x, SessionID: sess, State: string(StateEndPending),
				EndNote: "clone failed (scripted)", EndPushGaveUp: true}})
			hub.hold(x, sess, api.StatusRunning)
			if !landed {
				hub.mu.Lock()
				hub.end[x] = errors.New("hub unreachable (scripted)")
				hub.mu.Unlock()
			}
			if err := restartSettled(t, r); err != nil {
				t.Fatalf("Restart: %v", err)
			}
			settleOps(t, r, 10*time.Second)
			if n := hub.claimCount(); n != 0 {
				t.Fatalf("the abandoned claim was replayed %d time(s)", n)
			}
			if landed {
				if r.jobOf(x) != nil {
					t.Fatal("the landed close kept the job")
				}
				if e := hub.lastEnd(); e.Status != api.StatusBlocked || e.SessionID != sess || e.Note != "clone failed (scripted)" {
					t.Fatalf("restart close = %+v, want blocked with the recorded note", e)
				}
				return
			}
			if recs := loadRecords(t, r); len(recs) != 1 || recs[0].State != string(StateEndPending) {
				t.Fatalf("the failed close dropped the record: %+v", recs)
			}
		})
	}
}

// A start the local admission refuses — the work item already running
// here — answers without a hub call.
func TestStartLocalRefusalMakesNoHubCall(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	const a = "wi_cl_local"
	t.Cleanup(func() { r.Close(); terminateJobAgent(r, a) })
	startRunning(t, r, a)
	g := hub.hangCall(t, "probe", a)
	within(t, promptly, "a Start of the running work item", func() {
		if err := r.Start(context.Background(), a, StartOpts{}); err == nil || isTransient(err) {
			t.Errorf("Start of a running work item = %v, want the already-active refusal", err)
		}
	})
	select {
	case <-g.entered:
		t.Fatal("a locally refused start probed the hub")
	default:
	}
}
