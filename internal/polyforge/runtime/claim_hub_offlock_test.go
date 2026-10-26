package runtime

// claim_hub_offlock_test.go — the hub calls of a claim (work-item probe,
// journal verify, SessionClaim) and of a live resume (verify, attach)
// occupy work item A's actor alone: one hung hub call for A never freezes
// the doors of another work item, and the work item it hangs for stays
// single-claimed — its competing doors queue and get the final answer.

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// promptly is the "the other work item's door did not wait on the hung
// call" bound: the hung call never returns until the test releases it.
const promptly = 10 * time.Second

func isTransient(err error) bool {
	var ae *api.Error
	return errors.As(err, &ae) && ae.Code == api.ErrDaemonHubUnavailable
}

func isConflictErr(err error) bool {
	var ae *api.Error
	return errors.As(err, &ae) && ae.Code == api.ErrDaemonConflict
}

// nextRT answers the next RT message (an error after a bound, never a
// t.Fatal: the helpers below also run on within's goroutine).
func nextRT(q *supervise.Queue[protocol.RTMsg]) (protocol.RTMsg, error) {
	deadline := time.After(5 * time.Second)
	for {
		if m, ok := q.Pop(); ok {
			return m, nil
		}
		select {
		case <-q.Notify():
		case <-deadline:
			return protocol.RTMsg{}, errors.New("timed out waiting for an RT message")
		}
	}
}

// startLive starts wi and consumes its RTStarted.
func startLive(r *Runtime, toOB *supervise.Queue[protocol.RTMsg], wi string) error {
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		return fmt.Errorf("Start(%s): %w", wi, err)
	}
	m, err := nextRT(toOB)
	if err != nil {
		return err
	}
	if m.Kind != protocol.RTStarted || m.WorkItem != wi {
		return fmt.Errorf("want RTStarted(%s), got %+v", wi, m)
	}
	return nil
}

// pauseRetained pauses a live wi into a retained paused record whose
// clean_pause the operator already cleared (the resume door's input).
func pauseRetained(r *Runtime, hub *fakeHub, toOB *supervise.Queue[protocol.RTMsg], wi string) error {
	if err := r.Stop(context.Background(), wi, protocol.FinishPaused); err != nil {
		return fmt.Errorf("pause %s: %w", wi, err)
	}
	m, err := nextRT(toOB)
	if err != nil {
		return err
	}
	if m.Kind != protocol.RTFinished || m.Reason != protocol.FinishPaused {
		return fmt.Errorf("want RTFinished(paused), got %+v", m)
	}
	hub.mu.Lock()
	hub.cleanPause[wi] = false
	hub.mu.Unlock()
	return nil
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func attachCount(h *fakeHub, wi string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, a := range h.attaches {
		if a.WorkItemID == wi {
			n++
		}
	}
	return n
}

func claimsOf(h *fakeHub, wi string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, c := range h.claims {
		if h.canonicalID(c.WorkItemID) == wi {
			n++
		}
	}
	return n
}

// A hung probe, journal verify or SessionClaim of work item A leaves B
// fully operable — started, paused, resumed and wrapped while A hangs —
// and A itself stays single-claimed: a second Start of it (under either
// spelling) answers the typed transient, a Stop of it DAEMON-409; once the
// hub answers, A starts with exactly the one claim.
func TestOffLockHungClaimHubCallLeavesOthersResponsive(t *testing.T) {
	for _, kind := range []string{"probe", "verify", "claim"} {
		t.Run(kind, func(t *testing.T) {
			setGitIdentity(t)
			hub := newFakeHub()
			r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
			t.Cleanup(r.Close)
			const a, b = "wi_off_a", "wi_off_b"
			hub.canonical = map[string]string{"proj#9": a}
			ctx := context.Background()
			wantClaims := 1
			if kind == "verify" {
				// a journal entry whose claim answer was lost: the next
				// start verifies it first (claimIntent)
				hub.claimFaults = []claimFault{{err: errors.New("connection reset (scripted)"), afterCommit: true}}
				if err := r.Start(ctx, a, StartOpts{}); !isTransient(err) {
					t.Fatalf("lost-answer Start = %v, want the typed transient", err)
				}
				wantClaims = 2 // the lost one + its replay
			}
			g := hub.hangCall(t, kind, a)
			aErr := make(chan error, 1)
			go func() { aErr <- r.Start(ctx, a, StartOpts{}) }()
			g.waitEntered(t, kind+" of "+a)

			within(t, promptly, "Start of another work item", func() {
				if err := startLive(r, toOB, b); err != nil {
					t.Error(err)
				}
			})
			defer terminateJobAgent(r, b)
			within(t, promptly, "pause of another work item", func() {
				if err := pauseRetained(r, hub, toOB, b); err != nil {
					t.Error(err)
				}
			})
			within(t, promptly, "resume of another work item", func() {
				if out, err := r.ResumePaused(ctx, b); err != nil || out != ResumeAttached {
					t.Errorf("ResumePaused(%s) = %q, %v", b, out, err)
				}
			})
			within(t, promptly, "wrap of another work item", func() {
				if err := r.Stop(ctx, b, protocol.FinishWrapped); err != nil {
					t.Errorf("wrap %s: %v", b, err)
				}
			})

			// competing starts of the hung work item (its id, and its slug
			// once the probe answers) wait and claim nothing of their own
			second, slug := make(chan error, 1), make(chan error, 1)
			go func() { second <- r.Start(ctx, a, StartOpts{}) }()
			go func() { slug <- r.Start(ctx, "proj#9", StartOpts{}) }()
			stillPending(t, "the competing starts of the hung work item", aErr, second)
			if kind != "probe" {
				stillPending(t, "the slug start of the hung work item", slug)
			}

			g.open()
			nils := 0
			for _, ch := range []chan error{aErr, second, slug} {
				err := recvErr(t, ch, "a start of the hung work item")
				switch {
				case err == nil:
					nils++
				case !strings.Contains(err.Error(), "already active"):
					t.Fatalf("a competing start of %s = %v, want nil or already active", a, err)
				}
			}
			if nils != 1 {
				t.Fatalf("%d starts of %s succeeded, want exactly one", nils, a)
			}
			defer terminateJobAgent(r, a)
			if n := claimsOf(hub, a); n != wantClaims {
				t.Fatalf("%s claimed %d times, want %d", a, n, wantClaims)
			}
			if ji, ok := r.Info(a); !ok || ji.State != StateRunning {
				t.Fatalf("%s after the hub answered: state %s (ok=%v), want RUNNING", a, ji.State, ok)
			}
			if journaled(r, a) != nil {
				t.Fatal("the settled start left its claiming-journal entry")
			}
		})
	}
}

// A live resume whose session verify hangs occupies only its own work
// item: another work item closes promptly, while a second resume and a
// Start of the resuming one queue. Once the hub answers, the resume
// attaches exactly once and the queued doors get the final answers.
func TestOffLockResumeHungVerifyWhileAnotherCloses(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	const p, q = "wi_res_p", "wi_res_q"
	ctx := context.Background()
	must(t, startLive(r, toOB, p))
	must(t, pauseRetained(r, hub, toOB, p))
	must(t, startLive(r, toOB, q))
	defer terminateJobAgent(r, q)

	g := hub.hangCall(t, "verify", p)
	res := make(chan error, 1)
	go func() {
		out, err := r.ResumePaused(ctx, p)
		if err == nil && out != ResumeAttached {
			err = errors.New("resume outcome " + out)
		}
		res <- err
	}()
	g.waitEntered(t, "the resume's verify")

	within(t, promptly, "wrap of another work item", func() {
		if err := r.Stop(ctx, q, protocol.FinishWrapped); err != nil {
			t.Errorf("wrap %s: %v", q, err)
		}
	})
	again, start := make(chan error, 1), make(chan error, 1)
	go func() {
		out, err := r.ResumePaused(ctx, p)
		if err == nil && out != ResumeAlreadyLive {
			err = errors.New("second resume outcome " + out)
		}
		again <- err
	}()
	go func() { start <- r.Start(ctx, p, StartOpts{}) }()
	stillPending(t, "the doors of the resuming work item", res, again, start)
	g.open()
	if err := recvErr(t, res, "the hung resume"); err != nil {
		t.Fatalf("resume after the hub answered: %v", err)
	}
	if err := recvErr(t, again, "the queued second resume"); err != nil {
		t.Fatalf("the queued second resume: %v", err)
	}
	if err := recvErr(t, start, "the queued start"); err == nil {
		t.Fatal("a start of the resumed work item answered nil")
	}
	defer terminateJobAgent(r, p)
	if n := attachCount(hub, p); n != 1 {
		t.Fatalf("%s attached %d times, want 1", p, n)
	}
	if ji, _ := r.Info(p); ji.State != StateRunning {
		t.Fatalf("%s after the resume: %s, want RUNNING", p, ji.State)
	}
}

// A Stop during a resume's hung attach queues behind it (the attach
// decides the job first), and the attach goes out once.
func TestOffLockStopDuringHungAttach(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	const p = "wi_att_p"
	ctx := context.Background()
	must(t, startLive(r, toOB, p))
	must(t, pauseRetained(r, hub, toOB, p))

	g := hub.hangCall(t, "attach", p)
	res := make(chan error, 1)
	go func() { _, err := r.ResumePaused(ctx, p); res <- err }()
	g.waitEntered(t, "the resume's attach")
	stop := make(chan error, 1)
	go func() { stop <- r.Stop(ctx, p, protocol.FinishWrapped) }()
	stillPending(t, "the Stop during the attach", res, stop)
	g.open()
	if err := recvErr(t, res, "the hung resume"); err != nil {
		t.Fatalf("resume: %v", err)
	}
	defer terminateJobAgent(r, p)
	if err := recvErr(t, stop, "the queued Stop"); err != nil {
		t.Fatalf("the queued Stop after the resume settled: %v", err)
	}
	if n := attachCount(hub, p); n != 1 {
		t.Fatalf("attached %d times, want 1 (no double attach)", n)
	}
}

// A shutdown during each hung claim hub call is not held by it and the
// start defers once the hub answers: nothing is claimed after a probe,
// and the journal entry a verify or a claim was working on is kept for
// the next start.
func TestOffLockShutdownDuringHungClaimHubCall(t *testing.T) {
	for _, kind := range []string{"probe", "verify", "claim"} {
		t.Run(kind, func(t *testing.T) {
			setGitIdentity(t)
			hub := newFakeHub()
			r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
			t.Cleanup(r.Close)
			const a = "wi_off_sd"
			ctx := context.Background()
			if kind == "verify" {
				hub.claimFaults = []claimFault{{err: errors.New("connection reset (scripted)"), afterCommit: true}}
				if err := r.Start(ctx, a, StartOpts{}); !isTransient(err) {
					t.Fatalf("lost-answer Start = %v", err)
				}
			}
			g := hub.hangCall(t, kind, a)
			aErr := make(chan error, 1)
			go func() { aErr <- r.Start(ctx, a, StartOpts{}) }()
			g.waitEntered(t, kind+" of "+a)

			r.Close()
			g.open()
			if err := recvErr(t, aErr, "the hung Start"); !IsClaimDeferred(err) {
				t.Fatalf("Start after the shutdown latch = %v, want the deferral", err)
			}
			if r.jobOf(a) != nil {
				t.Fatal("a deferred start left a job behind")
			}
			rec := journaled(r, a)
			switch kind {
			case "probe":
				if rec != nil || hub.claimCount() != 0 {
					t.Fatalf("a probe-time deferral journaled/claimed: rec=%+v claims=%d", rec, hub.claimCount())
				}
			default:
				if rec == nil {
					t.Fatal("the deferral dropped the claiming-journal entry (the next start must replay it)")
				}
			}
			if n := hub.endCount(); n != 0 {
				t.Fatalf("a deferred start ended %d session(s) — a shutdown must never turn it blocked", n)
			}
		})
	}
}

// A shutdown latched during a resume's hung verify attaches nothing once
// the hub answers: the record stays paused for the next start.
func TestOffLockShutdownDuringHungResumeVerify(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, toOB, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	t.Cleanup(r.Close)
	const p = "wi_res_sd"
	ctx := context.Background()
	must(t, startLive(r, toOB, p))
	must(t, pauseRetained(r, hub, toOB, p))
	g := hub.hangCall(t, "verify", p)
	res := make(chan error, 1)
	go func() { _, err := r.ResumePaused(ctx, p); res <- err }()
	g.waitEntered(t, "the resume's verify")
	r.Close()
	g.open()
	err := recvErr(t, res, "the hung resume")
	var ae *api.Error
	if !errors.As(err, &ae) || ae.Code != api.ErrDaemonGone {
		t.Fatalf("resume after the shutdown latch = %v, want DAEMON-410", err)
	}
	if n := attachCount(hub, p); n != 0 {
		t.Fatalf("attached %d times behind the shutdown", n)
	}
	if ji, _ := r.Info(p); ji.State != StatePaused {
		t.Fatalf("record after the refused resume: %s, want PAUSED", ji.State)
	}
}
