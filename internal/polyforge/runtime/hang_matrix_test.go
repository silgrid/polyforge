package runtime

// hang_matrix_test.go — one hung call of each kind for work item A, and
// every door of another work item stays open: its Start, pause, resume
// and wrap return promptly, and the status reads (List, Info) within a
// second. Each row parks A's operation on exactly one call — a hub call
// at the fake hub's gate, a git transfer at a stalling remote, the agent
// start, the state write — and releases it at the end.

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// statusBound is how long a status read may take while A hangs, and
// doorBound how long another work item's start, pause, resume or wrap.
const (
	statusBound = time.Second
	doorBound   = 2 * time.Second
)

type matrixRig struct {
	r    *Runtime
	hub  *fakeHub
	sp   *scriptedSpawner
	toOB *supervise.Queue[protocol.RTMsg]
}

// awaitRT drains RT messages until kind arrives for wi (other work items'
// messages are skipped).
func awaitRT(q *supervise.Queue[protocol.RTMsg], kind protocol.RTMsgKind, wi string) error {
	deadline := time.After(promptly)
	for {
		if m, ok := q.Pop(); ok {
			if m.Kind == kind && m.WorkItem == wi {
				return nil
			}
			continue
		}
		select {
		case <-q.Notify():
		case <-deadline:
			return errors.New("timed out waiting for " + string(kind) + " of " + wi)
		}
	}
}

// statusResponsive reads the status faces within statusBound.
func (m *matrixRig) statusResponsive(t *testing.T, hung string) {
	t.Helper()
	within(t, statusBound, "List/Info while "+hung+" hangs", func() {
		_ = m.r.List()
		_, _ = m.r.Info(hung)
	})
}

// othersResponsive drives another work item through start, pause, resume
// and wrap while hung's operation is parked. live: the other work item is
// already running (the restart rows: one the restart revived).
func (m *matrixRig) othersResponsive(t *testing.T, hung, other string, live bool) {
	t.Helper()
	m.statusResponsive(t, hung)
	ctx := context.Background()
	if !live {
		within(t, doorBound, "Start of "+other, func() {
			if err := m.r.Start(ctx, other, StartOpts{}); err != nil {
				t.Errorf("Start(%s): %v", other, err)
				return
			}
			if err := awaitRT(m.toOB, protocol.RTStarted, other); err != nil {
				t.Error(err)
			}
		})
		t.Cleanup(func() { terminateJobAgent(m.r, other) })
	}
	within(t, doorBound, "pause of "+other, func() {
		if err := m.r.Stop(ctx, other, protocol.FinishPaused); err != nil {
			t.Errorf("pause %s: %v", other, err)
		}
	})
	m.hub.mu.Lock()
	m.hub.cleanPause[other] = false // the operator's pf resume PATCH
	m.hub.mu.Unlock()
	within(t, doorBound, "resume of "+other, func() {
		if out, err := m.r.ResumePaused(ctx, other); err != nil || out != ResumeAttached {
			t.Errorf("ResumePaused(%s) = %q, %v", other, out, err)
		}
	})
	within(t, doorBound, "wrap of "+other, func() {
		if err := m.r.Stop(ctx, other, protocol.FinishWrapped); err != nil {
			t.Errorf("wrap %s: %v", other, err)
		}
	})
	m.statusResponsive(t, hung)
}

// goErr runs fn on its own goroutine and answers its outcome.
func goErr(fn func() error) <-chan error {
	ch := make(chan error, 1)
	go func() { ch <- fn() }()
	return ch
}

func TestHangMatrixOtherWorkItemsStayResponsive(t *testing.T) {
	const a, b = "wi_hm_a", "wi_hm_b"
	ctx := context.Background()
	rows := []struct {
		name string
		// live: the other work item b is revived by the restart
		live, statusOnly bool
		// hang parks a's operation and answers its release; the matrix
		// waits for done (when non-nil) after the release
		hang func(t *testing.T, m *matrixRig) (release func(), done <-chan error)
	}{
		{"probe", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			g := m.hub.hangCall(t, "probe", a)
			done := goErr(func() error { return m.r.Start(ctx, a, StartOpts{}) })
			g.waitEntered(t, "the probe")
			return g.open, done
		}},
		{"journal-verify", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			m.hub.claimFaults = []claimFault{{err: errors.New("connection reset (scripted)"), afterCommit: true}}
			if err := m.r.Start(ctx, a, StartOpts{}); !isTransient(err) {
				t.Fatalf("lost-answer Start = %v", err)
			}
			g := m.hub.hangCall(t, "verify", a)
			done := goErr(func() error { return m.r.Start(ctx, a, StartOpts{}) })
			g.waitEntered(t, "the journal verify")
			return g.open, done
		}},
		{"claim", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			g := m.hub.hangCall(t, "claim", a)
			done := goErr(func() error { return m.r.Start(ctx, a, StartOpts{}) })
			g.waitEntered(t, "the claim")
			return g.open, done
		}},
		{"capsule-clone", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			base := m.r.cfg.GitRemoteBase
			m.r.cfg.GitRemoteBase = strings.TrimSuffix(hangingRemote(t), "/app.git")
			done := goErr(func() error { return m.r.Start(ctx, a, StartOpts{}) })
			waitClaiming(t, m.r, a)
			m.r.cfg.GitRemoteBase = base // the other work item clones from the hub
			return func() { _ = m.r.Stop(ctx, a, protocol.FinishWrapped) }, done
		}},
		{"task-repo-clone", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			m.hub.reposFor = map[string][]api.ProjectRepo{a: {{Name: "app", URL: hangingRemote(t)}}}
			done := goErr(func() error { return m.r.Start(ctx, a, StartOpts{}) })
			waitClaiming(t, m.r, a)
			return func() { _ = m.r.Stop(ctx, a, protocol.FinishWrapped) }, done
		}},
		{"seal-push", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			startRunning(t, m.r, a)
			release := stallPushes(t, m.r, a)
			done := goErr(func() error { return m.r.Stop(ctx, a, protocol.FinishWrapped) })
			waitBusy(t, m.r, a)
			return release, done
		}},
		{"pause-push", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			startRunning(t, m.r, a)
			release := stallPushes(t, m.r, a)
			done := goErr(func() error { return m.r.Stop(ctx, a, protocol.FinishPaused) })
			waitBusy(t, m.r, a)
			return release, done
		}},
		{"recovery-push", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			startRunning(t, m.r, a)
			release := stallPushes(t, m.r, a)
			ja := m.r.jobOf(a)
			done := goErr(func() error { m.r.recoverOp(ctx, ja, "agent exited", ""); return nil })
			waitBusy(t, m.r, a)
			return release, done
		}},
		{"close-session-end", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			startRunning(t, m.r, a)
			g := m.hub.hangCall(t, "end", a)
			done := goErr(func() error { return m.r.Stop(ctx, a, protocol.FinishWrapped) })
			g.waitEntered(t, "the close's session end")
			return g.open, done
		}},
		{"compensation-session-end", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			m.hub.reposFor = map[string][]api.ProjectRepo{a: {{Name: "app", URL: "/nonexistent/app.git"}}}
			g := m.hub.hangCall(t, "end", a)
			done := goErr(func() error { _ = m.r.Start(ctx, a, StartOpts{}); return nil })
			g.waitEntered(t, "the compensation's session end")
			return g.open, done
		}},
		{"end-pending-push", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			ja := endPendingJob(t, m, a)
			url, release := stallingRemote(t)
			ja.mu.Lock()
			ja.git.RemoteURL = url
			ja.mu.Unlock()
			waitBusy(t, m.r, a) // the retry's chain push
			return release, nil
		}},
		{"end-pending-session-end", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			m.r.cfg.EndRetryBase = 10 * time.Millisecond
			m.r.cfg.EndRetryMax = 20 * time.Millisecond
			m.hub.mu.Lock()
			m.hub.end[a] = errors.New("hub unreachable (scripted)")
			m.hub.mu.Unlock()
			startRunning(t, m.r, a)
			acceptPushOptions(t, m.r.cfg.GitRemoteBase+"/"+a+".git")
			ja := m.r.jobOf(a)
			underLock(m.r, a, func() { m.r.boundedFail(ctx, ja, "test: blocked") })
			if ji, _ := m.r.Info(a); ji.State != StateEndPending {
				t.Fatalf("job after the failed blocked close: %s, want end_pending", ji.State)
			}
			g := m.hub.hangCall(t, "end", a)
			m.hub.mu.Lock()
			delete(m.hub.end, a)
			m.hub.mu.Unlock()
			g.waitEntered(t, "the end_pending retry's session end")
			return g.open, nil
		}},
		{"task-wip-push", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			startRunning(t, m.r, a)
			entered := make(chan struct{})
			release := make(chan struct{})
			inner := pushTaskWIPFn
			pushTaskWIPFn = func(o gitops.Opts, keep, chainOK bool) {
				if o.WorkItemID == a {
					close(entered)
					<-release
				}
				inner(o, keep, chainOK)
			}
			t.Cleanup(func() { pushTaskWIPFn = inner })
			done := goErr(func() error { return m.r.Stop(ctx, a, protocol.FinishPaused) })
			<-entered
			return func() { close(release) }, done
		}},
		{"resume-verify", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			must(t, startLive(m.r, m.toOB, a))
			t.Cleanup(func() { terminateJobAgent(m.r, a) })
			must(t, pauseRetained(m.r, m.hub, m.toOB, a))
			g := m.hub.hangCall(t, "verify", a)
			done := goErr(func() error { _, err := m.r.ResumePaused(ctx, a); return err })
			g.waitEntered(t, "the resume's verify")
			return g.open, done
		}},
		{"resume-attach", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			must(t, startLive(m.r, m.toOB, a))
			t.Cleanup(func() { terminateJobAgent(m.r, a) })
			must(t, pauseRetained(m.r, m.hub, m.toOB, a))
			g := m.hub.hangCall(t, "attach", a)
			done := goErr(func() error { _, err := m.r.ResumePaused(ctx, a); return err })
			g.waitEntered(t, "the resume's attach")
			return g.open, done
		}},
		{"restart-verify", true, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			recB, _ := restartRecord(t, m.r, m.hub, b)
			recA, _ := restartRecord(t, m.r, m.hub, a)
			writeStateFile(t, m.r, []*jobRecord{recB, recA})
			g := m.hub.hangCall(t, "verify", a)
			restartReturning(t, m, b)
			g.waitEntered(t, "the restart verify")
			return g.open, nil
		}},
		{"restart-replay", true, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			recB, _ := restartRecord(t, m.r, m.hub, b)
			writeStateFile(t, m.r, []*jobRecord{recB})
			sess := fakeSession(a)
			if err := m.r.noteClaiming(a, sess, nil); err != nil {
				t.Fatal(err)
			}
			m.hub.hold(a, sess, api.StatusRunning)
			g := m.hub.hangCall(t, "claim", a)
			restartReturning(t, m, b)
			g.waitEntered(t, "the replayed claim")
			return g.open, nil
		}},
		{"restart-attach", true, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			recB, _ := restartRecord(t, m.r, m.hub, b)
			recA := pausedAttachRecord(t, m.r, m.hub, a)
			writeStateFile(t, m.r, []*jobRecord{recB, recA})
			g := m.hub.hangCall(t, "attach", a)
			restartReturning(t, m, b)
			g.waitEntered(t, "the restart attach")
			return g.open, nil
		}},
		{"verify-retry", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			m.r.cfg.EndRetryBase = 10 * time.Millisecond
			m.r.cfg.EndRetryMax = 20 * time.Millisecond
			recA, _ := restartRecord(t, m.r, m.hub, a)
			writeStateFile(t, m.r, []*jobRecord{recA})
			m.hub.lookupErr[a] = errLookupBoom
			if err := m.r.Restart(ctx); err != nil {
				t.Fatalf("Restart: %v", err)
			}
			g := m.hub.hangCall(t, "verify", a)
			m.hub.mu.Lock()
			delete(m.hub.lookupErr, a)
			m.hub.mu.Unlock()
			g.waitEntered(t, "the verify's retry")
			return g.open, nil
		}},
		{"agent-start", false, false, func(t *testing.T, m *matrixRig) (func(), <-chan error) {
			inner := m.r.cfg.AgentStarter
			entered := make(chan struct{})
			release := make(chan struct{})
			m.r.cfg.AgentStarter = func(sctx context.Context, in AgentStartInput) (AgentHandle, error) {
				if in.WI == a {
					close(entered)
					<-release
				}
				return inner(sctx, in)
			}
			done := goErr(func() error { return m.r.Start(ctx, a, StartOpts{}) })
			<-entered
			return func() { close(release) }, done
		}},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			setGitIdentity(t)
			hub := newFakeHub()
			sp := &scriptedSpawner{}
			r, toOB, _ := newTestRuntime(t, hub, sp)
			t.Cleanup(func() { r.Close(); terminateJobAgent(r, a); terminateJobAgent(r, b) })
			m := &matrixRig{r: r, hub: hub, sp: sp, toOB: toOB}
			release, done := row.hang(t, m)
			if !row.statusOnly {
				m.othersResponsive(t, a, b, row.live)
			}
			release()
			if done != nil {
				_ = recvErr(t, done, a+"'s operation")
			}
		})
	}
}

// The state write is the one disk write every work item's change goes
// through: while it hangs the status faces still answer.
func TestHangMatrixStateWriteLeavesStatusResponsive(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	const a = "wi_hm_state"
	t.Cleanup(func() { r.Close(); terminateJobAgent(r, a) })
	startRunning(t, r, a)
	entered := make(chan struct{})
	release := make(chan struct{})
	inner := writeStateFn
	armed := true
	writeStateFn = func(path string, data []byte, perm os.FileMode) error {
		if armed {
			armed = false
			close(entered)
			<-release
		}
		return inner(path, data, perm)
	}
	t.Cleanup(func() { writeStateFn = inner })
	done := goErr(func() error { return r.Stop(context.Background(), a, protocol.FinishPaused) })
	<-entered
	m := &matrixRig{r: r, hub: hub}
	m.statusResponsive(t, a)
	close(release)
	_ = recvErr(t, done, "the pause")
}

// acceptPushOptions lets the fake hub's bare capsule repo take the push
// options the runtime's pushes carry.
func acceptPushOptions(t *testing.T, bare string) {
	t.Helper()
	if out, err := exec.Command("git", "--git-dir", bare, "config", "receive.advertisePushOptions", "true").CombinedOutput(); err != nil {
		t.Fatalf("git config: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "--git-dir", bare, "config", "receive.shallowUpdate", "true").CombinedOutput(); err != nil {
		t.Fatalf("git config: %v: %s", err, out)
	}
}

// endPendingJob runs a's bounded fail with its chain push and blocked
// close failing at once, leaving it end_pending with the retry lane
// running (fast backoff).
func endPendingJob(t *testing.T, m *matrixRig, a string) *job {
	t.Helper()
	m.r.cfg.EndRetryBase = 10 * time.Millisecond
	m.r.cfg.EndRetryMax = 20 * time.Millisecond
	startRunning(t, m.r, a)
	ja := m.r.jobOf(a)
	ja.mu.Lock()
	ja.git.RemoteURL = "http://127.0.0.1:1/refused.git" // fails at once
	ja.mu.Unlock()
	if err := os.WriteFile(ja.capsuleDir+"/dirty.txt", []byte("unflushed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.hub.mu.Lock()
	m.hub.end[a] = errors.New("hub unreachable (scripted)")
	m.hub.mu.Unlock()
	underLock(m.r, a, func() { m.r.boundedFail(context.Background(), ja, "test: blocked") })
	if ji, _ := m.r.Info(a); ji.State != StateEndPending {
		t.Fatalf("job after the failed blocked close: %s, want end_pending", ji.State)
	}
	m.hub.mu.Lock()
	delete(m.hub.end, a)
	m.hub.mu.Unlock()
	return ja
}

// restartReturning runs Restart — which never waits on a hub call — and
// waits for the live record to be revived by its actor.
func restartReturning(t *testing.T, m *matrixRig, live string) {
	t.Helper()
	within(t, promptly, "Restart with a hung hub call", func() {
		if err := m.r.Restart(context.Background()); err != nil {
			t.Errorf("Restart: %v", err)
		}
	})
	waitFor(t, live+" revived by the lane", func() bool {
		ji, ok := m.r.Info(live)
		return ok && ji.State == StateRunning && ji.Agent != nil
	})
}

// restartRecord writes a crash-mid-run record of wi (capsule included)
// the hub still holds as running, answering its live sleeper's pid.
func restartRecord(t *testing.T, r *Runtime, hub *fakeHub, wi string) (*jobRecord, int) {
	t.Helper()
	pid, startedAt := spawnSleeper(t, wi)
	rec := matrixRecord(r, wi, pid, startedAt)
	hub.hold(wi, rec.SessionID, api.StatusRunning)
	initGitCapsule(t, r.restoredJob(rec))
	return rec, pid
}

// pausedAttachRecord writes a record of wi the hub holds paused with
// clean_pause cleared: the restart attaches and resumes it.
func pausedAttachRecord(t *testing.T, r *Runtime, hub *fakeHub, wi string) *jobRecord {
	t.Helper()
	pid, startedAt := spawnSleeper(t, wi)
	rec := matrixRecord(r, wi, pid, startedAt)
	hub.hold(wi, rec.SessionID, api.StatusPaused)
	initGitCapsule(t, r.restoredJob(rec))
	writeTranscript(t, jobFromRecord(rec))
	return rec
}
