package daemon

// routes_test.go: full UDS round trips over a real unix socket, driven
// through the CLI's own DaemonClient so every response is asserted
// against the pinned CLI contract field by field (submit/pause/resume/
// wrap dual-path arbitration/ops + the top-level api.Error
// envelope).

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// TestSubmitAndStatus pins the claim round trip: SubmitResponse shape,
// the job going live, and every StatusResponse field.
func TestSubmitAndStatus(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_a")

	raw, err := td.client.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var st cli.StatusResponse
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("status undecodable: %v", err)
	}
	if st.MachineID != "m-test" || st.Auto || st.ShuttingDown {
		t.Fatalf("status head = %+v", st)
	}
	// #113: the status carries the mapped workspace SET.
	if len(st.Workspaces) != 1 || st.Workspaces[0] != td.cfg.Mapping.Workspaces()[0] {
		t.Fatalf("status workspaces = %v, want the mapped set %v", st.Workspaces, td.cfg.Mapping.Workspaces())
	}
	// #169: the workflows dir pf doctor judges yaml keys by.
	if st.WorkflowsDir != td.cfg.WorkflowsDir {
		t.Fatalf("status workflows_dir = %q, want %q", st.WorkflowsDir, td.cfg.WorkflowsDir)
	}
	if len(st.Jobs) != 1 {
		t.Fatalf("jobs = %+v, want 1", st.Jobs)
	}
	j := st.Jobs[0]
	if j.WorkItemID != "wi_a" || j.Status != "running" || j.AgentPID != 0 || j.SpawnN == 0 || j.SessionFingerprint == "" {
		t.Fatalf("status job = %+v", j)
	}
	if st.Queue.Running != 1 || st.Queue.MaxConcurrent != 4 || st.Queue.Pending != 0 {
		t.Fatalf("queue stats = %+v", st.Queue)
	}
}

// TestObserveAnswerPending walks the watch snapshot end to end through the
// daemon socket: a driver-persisted pending question projected from
// execution.json, and the answer route's refusal.
func TestObserveAnswerPending(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_b")
	td.waitMonitored(t, "wi_b")

	snap, err := td.client.WatchSnapshot("wi_b", 0, false)
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	if snap.WorkItem != "wi_b" || !snap.Known || snap.Job == nil || snap.Job.Status != "running" {
		t.Fatalf("watch snapshot = %+v", snap)
	}
	if len(snap.Events) == 0 {
		t.Fatalf("watch snapshot missing events: %+v", snap)
	}

	// The driver parks on a question: pending_question lands in
	// execution.json and the snapshot projects it.
	td.seedPendingQuestion(t, "wi_b", "q_abc123", "spec", "which flavor?")
	deadline := time.Now().Add(5 * time.Second)
	for {
		snap, _ = td.client.WatchSnapshot("wi_b", 0, false)
		if (snap != nil && snap.Job != nil && snap.Job.Status == protocol.JobNeedsInput &&
			snap.Execution != nil && snap.Execution.PendingQuestion != nil) || time.Now().After(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if snap == nil || snap.Execution == nil || snap.Execution.PendingQuestion == nil ||
		snap.Execution.PendingQuestion.ID != "q_abc123" || snap.Job == nil || snap.Job.Status != protocol.JobNeedsInput {
		t.Fatalf("needs_input projection = %+v", snap)
	}

	// polyforge#106: the answer relay lane is deleted — the WI's engine
	// here is the scripted fake (no bridge Handle), so the answer
	// refuses fast with the typed no-bridge-handle 409 and NO relay
	// reaches the agent.
	araw, aerr := td.client.Answer("wi_b", cli.AnswerPayload{
		QuestionID: "q_abc123",
		AnswerText: "mint", AnsweredBy: "u_1",
	})
	if aerr == nil {
		t.Fatalf("answer with no live driver was accepted: %s", araw)
	}
	var ae *api.Error
	if !errors.As(aerr, &ae) || ae.Code != api.ErrDaemonConflict || !strings.Contains(ae.Detail, "no live driver") {
		t.Fatalf("answer error = %v, want the typed no-live-driver 409", aerr)
	}
}

// TestCloseArbitration is the load-bearing close arbitration: a CLI wrap
// holding the seal guard (its server-side end call parked) makes a
// concurrent wrap and a concurrent pause answer the typed DAEMON-409;
// releasing the park completes the first wrap as the one winner.
func TestCloseArbitration(t *testing.T) {
	td2 := newTestDaemon(t, nil)
	td2.hub.mu.Lock()
	td2.hub.blockEnd = make(chan struct{})
	td2.hub.mu.Unlock()
	td2.submitClaim(t, "wi_d")
	td2.waitMonitored(t, "wi_d")

	wrapDone := make(chan error, 1)
	go func() {
		_, err := td2.client.Wrap("wi_d", "wrap")
		wrapDone <- err
	}()
	waitState(t, td2, "wi_d", "sealing")
	// a concurrent close of the same work item queues behind the first
	// and gets the final answer once it settled: already terminal
	late := map[string]chan error{"wrap": make(chan error, 1), "pause": make(chan error, 1)}
	for name, call := range map[string]func() (json.RawMessage, error){
		"wrap":  func() (json.RawMessage, error) { return td2.client.Wrap("wi_d", "wrap") },
		"pause": func() (json.RawMessage, error) { return td2.client.Pause("wi_d") },
	} {
		go func() {
			raw, err := call()
			if err == nil && !strings.Contains(string(raw), `"outcome":"already_terminal"`) {
				err = fmt.Errorf("answer %s, want already_terminal", raw)
			}
			late[name] <- err
		}()
	}
	close(td2.hub.blockEnd)
	if err := <-wrapDone; err != nil {
		t.Fatalf("CLI wrap winner failed: %v", err)
	}
	for name, ch := range late {
		if err := <-ch; err != nil {
			t.Fatalf("concurrent %s during the CLI close: %v", name, err)
		}
	}
	waitGone(t, td2, "wi_d")
	if td2.hub.endCount() != 1 {
		t.Fatalf("end-generation calls = %d, want 1", td2.hub.endCount())
	}
}

// TestWrapAlreadyTerminalGraceful: a wrap of an already-sealed work item
// answers gracefully (the idempotent already-terminal shape), not 404.
// TestResumeBlockedOwnerMove (polyforge#116): the §6.4 owner face on a
// workflow parked blocked — the route drives the hub's resume-blocked
// door (blocked → queued) once and then starts the WI live (the
// claim/start path's owner move resumes the parked capsule). A live or
// queued duplicate is refused, the submit-family way.
func TestResumeBlockedOwnerMove(t *testing.T) {
	td := newTestDaemon(t, nil)
	const wi = "wi_parked"
	// The hub names the WI blocked with no session (the bounded close's
	// shape — M2).
	// The CLI has already landed the hub's resume-blocked door with the
	// human's credential: the WI is queued when the daemon route runs.
	td.hub.mu.Lock()
	td.hub.status[wi] = api.StatusQueued
	td.hub.mu.Unlock()
	mkBare(t, td.cfg.GitRemoteBase, wi)

	raw, err := td.client.ResumeBlocked(wi)
	if err != nil {
		t.Fatalf("resume-blocked: %v", err)
	}
	var rr struct {
		Resumed    bool   `json:"resumed"`
		WorkItemID string `json:"work_item_id"`
	}
	if err := json.Unmarshal(raw, &rr); err != nil {
		t.Fatalf("resume-blocked answer undecodable: %v", err)
	}
	if !rr.Resumed || rr.WorkItemID != wi {
		t.Fatalf("resume-blocked answer = %+v, want resumed for %s", rr, wi)
	}
	td.waitLive(t, wi)
	// The daemon must NOT call the hub's resume door with its machine key.
	td.hub.mu.Lock()
	if len(td.hub.resumeBlockeds) != 0 {
		t.Fatalf("the daemon route fired the hub resume door %v; the human's CLI owns that call", td.hub.resumeBlockeds)
	}
	td.hub.mu.Unlock()

	// A live work item refuses the owner's verdict (it owns its starts).
	if _, err := td.client.ResumeBlocked(wi); err == nil {
		t.Fatal("resume-blocked on a live work item must be refused")
	}
}

// TestParkedWICloseRefusal (polyforge#116): a close — wrap (success) or
// `--fail` — that finds no local job because the workflow is parked
// blocked answers the owner-move refusal (continue or abandon only);
// there is nothing to seal, and no hub door is fired.
func TestParkedWICloseRefusal(t *testing.T) {
	td := newTestDaemon(t, nil)
	const wi = "wi_parked"
	td.hub.mu.Lock()
	td.hub.status[wi] = api.StatusBlocked
	td.hub.mu.Unlock()

	// `--fail` on a parked workflow has no hub door and no local job —
	// the daemon refuses with the owner-move copy.
	if _, err := td.client.Wrap(wi, "fail"); err == nil || !strings.Contains(err.Error(), "pf resume") {
		t.Fatalf("daemon wrap --fail on a parked workflow must refuse with the owner-move copy, got %v", err)
	}
	td.hub.mu.Lock()
	if len(td.hub.resumeBlockeds) != 0 {
		t.Fatalf("the daemon fired a hub owner door %v with its machine key", td.hub.resumeBlockeds)
	}
	td.hub.mu.Unlock()

	// A wrap (success) of a STILL-parked workflow has nothing to seal —
	// the owner's moves are continue/abandon, not a runtime close.
	const wi2 = "wi_parked2"
	td.hub.mu.Lock()
	td.hub.status[wi2] = api.StatusBlocked
	td.hub.mu.Unlock()
	if _, err := td.client.Wrap(wi2, "wrap"); err == nil {
		t.Fatal("wrap (success) on a parked workflow must be refused")
	}
	// A pause of a parked workflow: same refusal — the owner's moves are
	// continue/abandon, not a runtime close.
	if _, err := td.client.Pause(wi2); err == nil {
		t.Fatal("pause on a parked workflow must be refused")
	}
}

func TestWrapAlreadyTerminalGraceful(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_e")
	td.waitMonitored(t, "wi_e")
	if _, err := td.client.Wrap("wi_e", "wrap"); err != nil {
		t.Fatalf("first wrap: %v", err)
	}
	waitGone(t, td, "wi_e")
	td.hub.mu.Lock()
	td.hub.status["wi_e"] = api.StatusWrapped
	td.hub.mu.Unlock()
	raw, err := td.client.Wrap("wi_e", "wrap")
	if err != nil {
		t.Fatalf("second wrap: %v", err)
	}
	var cr closeResponse
	if err := json.Unmarshal(raw, &cr); err != nil || cr.Outcome != "already_terminal" {
		t.Fatalf("second wrap answer = %s (%v)", raw, err)
	}
}

// TestPauseResume (Finding 2, v1.5 review): the operator pause RETAINS
// the job record — the hub hears SessionEnd(paused, clean_pause=true)
// and the local job transitions to the paused state instead of being
// dropped (the old contract destroyed the secret locally, so the M3
// attach could never fire). A repeat pause answers the graceful
// already-paused shape. The resume flow itself (clear clean_pause,
// holder daemon attaches on its next start) is pinned by the runtime
// restart tests and the s03 e2e.
func TestPauseResume(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_f")
	if _, err := td.client.Pause("wi_f"); err != nil {
		t.Fatalf("pause: %v", err)
	}
	waitState(t, td, "wi_f", "paused")
	if st := td.hub.lastEnd().Status; st != "paused" {
		t.Fatalf("pause end status = %q, want paused", st)
	}
	if !td.hub.lastEnd().CleanPause {
		t.Fatal("the operator pause must carry clean_pause=true")
	}
	// A repeat pause is the graceful idempotency answer, not a conflict.
	raw, err := td.client.Pause("wi_f")
	if err != nil {
		t.Fatalf("second pause: %v", err)
	}
	var cr closeResponse
	if err := json.Unmarshal(raw, &cr); err != nil || cr.Outcome != "already_paused" {
		t.Fatalf("second pause answer = %s (%v)", raw, err)
	}
}

// TestErrorEnvelope: unknown targets answer the top-level api.Error
// envelope (DAEMON-404), which the CLI client decodes as a typed error.
func TestErrorEnvelope(t *testing.T) {
	td := newTestDaemon(t, nil)
	// Typed 404 for an unknown work item (the envelope shape the CLI
	// decodes): no local job + aihub lookup fails.
	td.hub.mu.Lock()
	td.hub.status["wi_nope"] = "gone-broken"
	td.hub.mu.Unlock()
	if _, err := td.client.Wrap("wi_nope", "wrap"); err == nil {
		t.Fatal("wrap of unknown wi succeeded")
	} else {
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Code != api.ErrDaemonNotFound {
			t.Fatalf("wrap unknown error = %v, want DAEMON-404", err)
		}
	}
}

// TestOpsAutoAndMax: the auto switch and the max number over UDS; manual
// submits stay legal while auto is on; bad input and shutdown refusals.
func TestOpsAutoAndMax(t *testing.T) {
	td := newTestDaemon(t, nil)
	on, off, two := true, false, 2
	raw, err := td.client.OpsConfig(cli.OpsConfigRequest{Auto: &on, Max: &two})
	if err != nil {
		t.Fatalf("ops config: %v", err)
	}
	var r opsConfigResponse
	if err := json.Unmarshal(raw, &r); err != nil || !r.Auto || r.Max != 2 {
		t.Fatalf("ops config answer = %s (%v)", raw, err)
	}
	if !td.modes.Auto() || td.q.MaxConcurrent() != 2 {
		t.Fatalf("state not applied: auto=%v max=%d", td.modes.Auto(), td.q.MaxConcurrent())
	}
	// A manual submit is ALLOWED while auto is on (no DAEMON-409).
	if _, err := td.client.ClaimWithModels("wi_z", "normal", nil); err != nil {
		t.Fatalf("claim while auto on: %v", err)
	}
	if _, err := td.client.OpsConfig(cli.OpsConfigRequest{Auto: &off}); err != nil {
		t.Fatalf("auto off: %v", err)
	}
	if td.modes.Auto() {
		t.Fatal("auto still on")
	}
	// Bad input refuses with DAEMON-400.
	zero := 0
	for _, bad := range []cli.OpsConfigRequest{{}, {Max: &zero}} {
		_, err := td.client.OpsConfig(bad)
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Code != api.ErrDaemonBadInput {
			t.Fatalf("bad ops config %+v error = %v, want DAEMON-400", bad, err)
		}
	}
	// A submit and an auto switch during shutdown answer DAEMON-410.
	td.modes.SetShuttingDown()
	if _, err := td.client.ClaimWithModels("wi_y", "normal", nil); err == nil {
		t.Fatal("claim during shutdown succeeded")
	} else {
		var ae *api.Error
		if !errors.As(err, &ae) || ae.Code != api.ErrDaemonGone {
			t.Fatalf("shutdown claim error = %v, want DAEMON-410", err)
		}
	}
	if _, err := td.client.OpsConfig(cli.OpsConfigRequest{Auto: &on}); err == nil {
		t.Fatal("auto switch during shutdown succeeded")
	}
}

// TestSubmitDispatchBackfill: submits beyond the concurrency cap queue
// and backfill as slots open (archive/04-local-runtime.md §12).
func TestSubmitDispatchBackfill(t *testing.T) {
	td := newTestDaemon(t, func(c *Config) { c.MaxConcurrent = 1 })
	for _, wi := range []string{"wi_p", "wi_q", "wi_r"} {
		mkBare(t, td.cfg.GitRemoteBase, wi)
		if _, err := td.client.ClaimWithModels(wi, "normal", nil); err != nil {
			t.Fatalf("claim %s: %v", wi, err)
		}
	}
	// With max=1 exactly one job runs; the rest stay pending.
	td.waitLive(t, "wi_p")
	time.Sleep(100 * time.Millisecond)
	raw, _ := td.client.Status()
	var st cli.StatusResponse
	json.Unmarshal(raw, &st)
	if st.Queue.Running != 1 || st.Queue.Pending != 2 {
		t.Fatalf("queue after one slot = %+v, want 1 running / 2 pending", st.Queue)
	}
	// Wrapping the running job frees its slot; the next pending entry
	// backfills without any new submission.
	if _, err := td.client.Wrap("wi_p", "wrap"); err != nil {
		t.Fatalf("wrap wi_p: %v", err)
	}
	td.waitLive(t, "wi_q")
}

// ---------------------------------------------------------------------------
// helpers

func waitGone(t *testing.T, td *testDaemon, wi string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, err := td.client.Status()
		if err == nil {
			var st cli.StatusResponse
			if json.Unmarshal(raw, &st) == nil {
				found := false
				for _, j := range st.Jobs {
					if j.WorkItemID == wi {
						found = true
					}
				}
				if !found {
					return
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never left the job list", wi)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitState(t *testing.T, td *testDaemon, wi, state string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		raw, err := td.client.Status()
		if err == nil {
			var st cli.StatusResponse
			if json.Unmarshal(raw, &st) == nil {
				for _, j := range st.Jobs {
					if j.WorkItemID == wi && j.Status == state {
						return
					}
				}
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s never reached state %s", wi, state)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func strp(s string) *string { return &s }

// dialWorks reports whether the daemon socket still accepts connections.
func dialWorks(sock string) bool {
	c, err := net.DialTimeout("unix", sock, time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

func TestStatusShowsNeedsInput(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_askst")
	td.waitMonitored(t, "wi_askst")

	// before the question: plain running bookkeeping, no QUESTION cell
	st := td.statusOf(t)
	if j := statusJobOf(&st, "wi_askst"); j == nil || j.Status != "running" || j.NeedsInput {
		t.Fatalf("pre-question status = %+v", j)
	}

	// the driver parks on a human question (pending_question persisted)
	td.seedPendingQuestion(t, "wi_askst", "q_d6001", "spec", "pick one")

	// /status presents needs_input + the question id (the monitor's
	// projection is applied on the read, so a small poll window is legal)
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := td.statusOf(t)
		j := statusJobOf(&st, "wi_askst")
		if j != nil && j.NeedsInput && j.Status == protocol.JobNeedsInput && j.QuestionID == "q_d6001" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("needs_input never surfaced on /status: %+v", st.Jobs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// polyforge#176: /status names the in-flight dispatch's candidate, effort
// included (the ops status CANDIDATE column), from the monitor's activity.
func TestStatusShowsInFlightCandidate(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_candst")
	td.waitMonitored(t, "wi_candst")
	st0 := td.statusOf(t)
	if j := statusJobOf(&st0, "wi_candst"); j == nil || j.Candidate != "" {
		t.Fatalf("no dispatch announced yet: candidate %+v", j)
	}
	td.sup.Link("wi_candst").Fact(supervise.StepStarted{
		Step: "fix", SpawnN: 1, Model: "claude-opus-5-5", Effort: "high", Harness: "claude",
		StartedAt: time.Now(), Thresholds: supervise.Thresholds{Silence: time.Hour, Tool: time.Hour, StepTimeout: time.Hour},
	})
	deadline := time.Now().Add(5 * time.Second)
	for {
		st := td.statusOf(t)
		if j := statusJobOf(&st, "wi_candst"); j != nil && j.Candidate == "claude:claude-opus-5-5:high" {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("candidate never surfaced on /status: %+v", st.Jobs)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// statusOf reads GET /status once, decoded.
func (td *testDaemon) statusOf(t *testing.T) cli.StatusResponse {
	t.Helper()
	raw, err := td.client.Status()
	if err != nil {
		t.Fatalf("status: %v", err)
	}
	var st cli.StatusResponse
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("status undecodable: %v", err)
	}
	return st
}

// statusJobOf finds one jobs[] entry by work item id.
func statusJobOf(s *cli.StatusResponse, wi string) *cli.StatusJob {
	for i := range s.Jobs {
		if s.Jobs[i].WorkItemID == wi {
			return &s.Jobs[i]
		}
	}
	return nil
}

// TestResumeBlockedHubUnavailableQueuesClaim: a DAEMON-503 at the claim
// (the CLI already flipped the WI blocked -> queued) does not strand the
// WI: the route queues the claim in the persisted submit queue and answers
// success-deferred; the dispatcher claims it once the hub answers.
func TestResumeBlockedHubUnavailableQueuesClaim(t *testing.T) {
	td := newTestDaemon(t, nil)
	const wi = "wi_resume503"
	td.hub.mu.Lock()
	td.hub.status[wi] = api.StatusQueued
	td.hub.probeFails = 2 // the route's own Start probe fails (and possibly the first dispatch), then the hub answers
	td.hub.mu.Unlock()
	mkBare(t, td.cfg.GitRemoteBase, wi)

	raw, err := td.client.ResumeBlocked(wi)
	if err != nil {
		t.Fatalf("resume-blocked must answer success-deferred on DAEMON-503: %v", err)
	}
	var rr struct {
		Resumed    bool   `json:"resumed"`
		WorkItemID string `json:"work_item_id"`
		Deferred   bool   `json:"deferred"`
	}
	if err := json.Unmarshal(raw, &rr); err != nil {
		t.Fatal(err)
	}
	if !rr.Resumed || !rr.Deferred || rr.WorkItemID != wi {
		t.Fatalf("answer = %+v, want resumed+deferred", rr)
	}
	td.waitLive(t, wi)
}

// TestClaimAnswerLostIsRequeuedAndAdopted (repo review 2026-10-16 B-1):
// the hub commits the claim but its answer is lost in transit. The
// dispatcher requeues the submit (DAEMON-503 class) instead of dropping
// it, and the retry replays the SAME claim — the job runs on the session
// the hub already holds, never a second one.
func TestClaimAnswerLostIsRequeuedAndAdopted(t *testing.T) {
	td := newTestDaemon(t, nil)
	const wi = "wi_b1lost"
	td.hub.mu.Lock()
	td.hub.status[wi] = api.StatusQueued
	td.hub.claimLost = 1
	td.hub.mu.Unlock()
	td.submitClaim(t, wi) // waits until the requeued retry is live

	td.hub.mu.Lock()
	claims := append([]api.SessionClaimRequest(nil), td.hub.claims...)
	held := td.hub.held[wi]
	td.hub.mu.Unlock()
	if len(claims) != 2 {
		t.Fatalf("claims = %d, want 2 (the lost one + the replay)", len(claims))
	}
	if claims[1].SessionID != claims[0].SessionID {
		t.Fatalf("the requeued retry must replay the same claim: %+v vs %+v", claims[0], claims[1])
	}
	if ji, ok := td.rt.Info(wi); !ok || ji.SessionID != held {
		t.Fatalf("job session = %+v (ok=%v), want the hub-held session", ji, ok)
	}
}

// TestWrapSessionEndBlipRetriesToDone (repo review 2026-10-16 B-4): the
// wrap's SessionEnd hits a hub blip. The job is SEALING (a repeat wrap is
// the 409 "close in progress"), the runtime retries the close in the
// background, and once it lands the WI is done here: a later `pf wrap`
// answers already_terminal instead of "in progress" forever.
func TestWrapSessionEndBlipRetriesToDone(t *testing.T) {
	td := newTestDaemon(t, nil)
	const wi = "wi_b4wrap"
	td.submitClaim(t, wi)
	td.hub.mu.Lock()
	td.hub.endFails = 1
	td.hub.mu.Unlock()

	if _, err := td.client.Wrap(wi, ""); err == nil {
		t.Fatal("the wrap whose SessionEnd failed must surface the failure")
	}
	if ji, ok := td.rt.Info(wi); !ok || ji.State != runtime.StateSealing {
		t.Fatalf("job after the failed close = %+v (ok=%v), want SEALING", ji, ok)
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		if _, live := td.rt.Info(wi); !live {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the sealing job was never retried to done")
		}
		time.Sleep(50 * time.Millisecond)
	}
	raw, err := td.client.Wrap(wi, "")
	if err != nil {
		t.Fatalf("wrap after the retried close: %v", err)
	}
	var cr struct {
		Outcome string `json:"outcome"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(raw, &cr); err != nil {
		t.Fatal(err)
	}
	if cr.Outcome != "already_terminal" || cr.Status != api.StatusWrapped {
		t.Fatalf("wrap after the retried close = %+v, want already_terminal/wrapped", cr)
	}
}
