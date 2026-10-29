package driver

// retry_pacing_test.go: the 5xx resume and the retry pacing.
//
//   - A model_error (an error-ended turn whose text names no throttle,
//     transient, allowance or auth problem — in practice a gateway 5xx) is
//     model-class but its FIRST death on a candidate resumes the same
//     candidate; only the second consecutive model-class death on it
//     rotates (ModelErrorRotationThreshold). It stays counted.
//   - Consecutive model_error / upstream_transient / ended_without_result
//     deaths on one candidate wait 1m, 3m, 9m (cap) before a re-dispatch of
//     that SAME candidate; a death whose decision switches candidates
//     dispatches the next one at once (the outage stays with its upstream)
//     and the next candidate's ladder starts again at 1m.
//   - Both are derived from the attempts ledger: a driver that replaces a
//     dead predecessor dispatches the same candidate and serves only the
//     remainder of the wait (RetryOwed).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// gateway502 is a gateway 5xx error turn as pi reports it: no throttle,
// transient, allowance or auth signature — verdictCause keeps model_error.
const gateway502 = `502: {"message":"Upstream access forbidden, please contact administrator","type":"upstream_error"}`

// waitRecorder is a Config.Sleep that records every wait; onCall, when
// set, runs inside the n-th (0-based) wait.
type waitRecorder struct {
	mu     sync.Mutex
	waits  []time.Duration
	onCall func(n int)
}

func (w *waitRecorder) sleep(_ context.Context, d time.Duration) {
	w.mu.Lock()
	n := len(w.waits)
	w.waits = append(w.waits, d)
	on := w.onCall
	w.mu.Unlock()
	if on != nil {
		on(n)
	}
}

func (w *waitRecorder) got() []time.Duration {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]time.Duration(nil), w.waits...)
}

// feedVerdicts answers each of the first len(errs) dispatches with the
// supervisor's rule-1 verdict over a live error turn (the dispatch is
// held until the verdict tears it down).
func feedVerdicts(t *testing.T, env *supEnv, errs []string) {
	t.Helper()
	go func() {
		for i, msg := range errs {
			waitForSpawns(env, i+1)
			sp := env.sp.spawnAt(i)
			env.link.Verdict(liveErrorVerdict(t, env.link, sp.StepID, sp.SpawnN, msg, 0))
		}
	}()
}

func failedBodies(env *supEnv) []map[string]any {
	var out []map[string]any
	for _, e := range env.log.byKind("step_failed") {
		out = append(out, e.body)
	}
	return out
}

// TestModelErrorFirstDeathResumesSameCandidate: one gateway 5xx on a
// candidate is a counted same-candidate retry that resumes the
// conversation after the first retry-pacing wait — no candidate burn.
func TestModelErrorFirstDeathResumesSameCandidate(t *testing.T) {
	rec := &waitRecorder{}
	env := newChainEnv(t, func(c *Config) { c.Sleep = rec.sleep }, "s", "a", "b")
	env.sp.script = []fakeEntry{{hold: true}, done(`{"ok":true}`)}
	feedVerdicts(t, env, []string{gateway502})
	before := time.Now()
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if models := env.models(); len(models) != 2 || models[0] != "a" || models[1] != "a" {
		t.Fatalf("dispatched %v, want [a a]: the first model_error stays on its candidate", models)
	}
	if sp := env.sp.spawnAt(1); !sp.Resume || sp.Fresh {
		t.Fatalf("the retry must resume the conversation, got resume=%v fresh=%v", sp.Resume, sp.Fresh)
	}
	fails := failedBodies(env)
	if len(fails) != 1 {
		t.Fatalf("step_failed events %v, want one", fails)
	}
	f := fails[0]
	if f["cause"] != FailModelError || f["disposition"] != "retry_same_model" || f["next_dispatch"] != "resume" {
		t.Fatalf("step_failed %v, want model_error / retry_same_model / resume", f)
	}
	// counted, not neutral: the 3-failure budget still sees it
	if f["failure_count"] != 1 {
		t.Fatalf("failure_count %v, want 1 (a model_error is counted)", f["failure_count"])
	}
	base := supervise.DefaultRetryBackoffBase
	if f["backoff_ms"] != base.Milliseconds() {
		t.Fatalf("backoff_ms %v, want %d", f["backoff_ms"], base.Milliseconds())
	}
	if got := rec.got(); len(got) != 1 || got[0] != base {
		t.Fatalf("waits %v, want [%v]", got, base)
	}
	if sw := env.log.byKind("candidate_switched"); len(sw) != 0 {
		t.Fatalf("the first model_error burned the candidate: %v", sw)
	}
	// the wait was declared to the supervisor (its stall clock starts at Until)
	var decl *supervise.StepRetryWait
	for _, f := range env.link.Facts.Drain() {
		if w, ok := f.(supervise.StepRetryWait); ok {
			decl = &w
		}
	}
	if decl == nil || decl.Step != "s" || decl.Cause != FailModelError ||
		decl.Until.Before(before.Add(base)) || decl.Until.After(time.Now().Add(base)) {
		t.Fatalf("step_retry_wait fact %+v, want step s / model_error / until now+%v", decl, base)
	}
	if got := env.exec(t).Steps["s"].BudgetConsumed.WallMs; got < base.Milliseconds() {
		t.Fatalf("step T %dms, want the %v wait booked in", got, base)
	}
}

// TestModelErrorTwiceSwitchesCandidate: the second consecutive
// model-class death on the candidate rotates WITHOUT a wait (the wait
// rides out an upstream's outage; the next candidate is another one); the
// switched-to candidate starts a fresh conversation, and its own first
// model_error resumes it after the BASE wait — the ladder is per
// candidate, so a's streak does not carry over to b.
func TestModelErrorTwiceSwitchesCandidate(t *testing.T) {
	rec := &waitRecorder{}
	env := newChainEnv(t, func(c *Config) { c.Sleep = rec.sleep }, "s", "a", "b")
	env.sp.script = []fakeEntry{{hold: true}, {hold: true}, {hold: true}, done(`{"ok":true}`)}
	feedVerdicts(t, env, []string{gateway502, gateway502, gateway502})
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if models := env.models(); len(models) != 4 || models[0] != "a" || models[1] != "a" || models[2] != "b" || models[3] != "b" {
		t.Fatalf("dispatched %v, want [a a b b]", models)
	}
	if sp := env.sp.spawnAt(2); !sp.Fresh {
		t.Fatalf("the switched-to candidate must start fresh, got %+v", sp)
	}
	if sp := env.sp.spawnAt(3); !sp.Resume || sp.Fresh {
		t.Fatalf("b's first model_error must resume b, got %+v", sp)
	}
	fails := failedBodies(env)
	if len(fails) != 3 || fails[1]["disposition"] != "switch_model" || fails[1]["from_model"] != "pi:a" || fails[1]["to_model"] != "pi:b" {
		t.Fatalf("step_failed %v, want the second to switch pi:a -> pi:b", fails)
	}
	if fails[2]["disposition"] != "retry_same_model" || fails[2]["candidate"] != "pi:b" {
		t.Fatalf("step_failed %v, want b's first model_error to retry b", fails[2])
	}
	sw := env.log.byKind("candidate_switched")
	if len(sw) != 1 || sw[0].body["cause"] != FailModelError {
		t.Fatalf("candidate_switched %v, want one, cause model_error", sw)
	}
	base := supervise.DefaultRetryBackoffBase
	if got := rec.got(); len(got) != 2 || got[0] != base || got[1] != base {
		t.Fatalf("waits %v, want [1m 1m]: a's retry, no wait for the switch, b's first retry at the base again", got)
	}
	if b, ok := fails[1]["backoff_ms"]; ok {
		t.Fatalf("the switching step_failed books a backoff %v, want none", b)
	}
	if fails[2]["backoff_ms"] != base.Milliseconds() {
		t.Fatalf("b's first retry booked backoff_ms %v, want the base %d (not a 9m carried over from a)", fails[2]["backoff_ms"], base.Milliseconds())
	}
	decls := 0
	for _, f := range env.link.Facts.Drain() {
		if _, ok := f.(supervise.StepRetryWait); ok {
			decls++
		}
	}
	if decls != 2 {
		t.Fatalf("step_retry_wait facts %d, want 2 (the two same-candidate retries only)", decls)
	}
	// T carries the two 1m waits and the dispatches' own (millisecond)
	// walls, never a wait for the switch
	if got := env.exec(t).Steps["s"].BudgetConsumed.WallMs; got < 2*base.Milliseconds() || got >= 3*base.Milliseconds() {
		t.Fatalf("step T %dms, want the two 1m waits booked and no switch wait", got)
	}
}

// TestAllowanceErrorTurnStillSwitchesAtOnce: a live error turn whose
// text the exit path archives as exit_model_class — an allowance/auth
// problem (D7) or a model name that cannot resolve (F4) — is archived the
// same and keeps its immediate switch; only the unclassified error turn
// resumes first.
func TestAllowanceErrorTurnStillSwitchesAtOnce(t *testing.T) {
	for _, text := range []string{
		`401 {"type":"error","error":{"type":"authentication_error","message":"invalid x-api-key"}}`,
		`402 {"error":{"message":"Insufficient balance"}}`,
		`model "xyz" not found`,
		`404 {"error":{"message":"unknown model: prov/xyz"}}`,
	} {
		if got := verdictCause(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: text}); got != FailExitModel {
			t.Errorf("verdictCause(%q) = %q, want %q", text, got, FailExitModel)
		}
		if exit, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: text, TurnError: true}); exit != FailExitModel {
			t.Errorf("the exit path classified %q as %q: the live path must match it", text, exit)
		}
	}
	if got := verdictCause(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: gateway502}); got != FailModelError {
		t.Errorf("verdictCause(%q) = %q, want %q", gateway502, got, FailModelError)
	}
}

// TestOutageShapedModelTextsStayModelError: live error turns that mention
// the model but describe an outage, not a name that does not resolve,
// stay model_error (resume first) however the gateway wraps them.
func TestOutageShapedModelTextsStayModelError(t *testing.T) {
	for _, text := range []string{
		`400: {"message":"The model request failed due to an upstream error.","type":"invalid_request_error"}`,
		"400: The model request failed due to an upstream error.",
		`503 {"error":{"message":"The model is temporarily unavailable, please try again"}}`,
		`500 {"error":{"message":"model inference failed: invalid response from backend"}}`,
		gateway502,
		// a resolution word that does not sit next to the model's name
		"The model encountered an unknown error, please try again later",
		"500: model server returned unknown status",
		"model endpoint not found, retrying",
		"the model you requested was not found in the cache, retrying",
	} {
		if got := verdictCause(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: text}); got != FailModelError {
			t.Errorf("verdictCause(%q) = %q, want %q (an outage resumes first)", text, got, FailModelError)
		}
	}
	// the adjacent name-resolution forms switch at once
	for _, text := range []string{
		`model "xyz" not found`,
		"model xyz not found",
		"model x: not found",
		"unknown model xyz",
		`404 {"error":{"message":"unknown model: prov/xyz"}}`,
		"The model `gpt-x` does not exist or you do not have access to it.",
		`{"error":{"code":"model_not_found"}}`,
		"no such model: prov/xyz",
		"unsupported model prov/xyz",
	} {
		if got := verdictCause(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseModelError, TurnError: text}); got != FailExitModel {
			t.Errorf("verdictCause(%q) = %q, want %q (an unresolvable model name switches at once)", text, got, FailExitModel)
		}
	}
}

// TestUnresolvableModelErrorTurnSwitchesWithoutWait: a live error turn
// naming a model that cannot resolve switches to the next candidate at
// once — no resume of the unresolvable model, no retry wait.
func TestUnresolvableModelErrorTurnSwitchesWithoutWait(t *testing.T) {
	rec := &waitRecorder{}
	env := newChainEnv(t, func(c *Config) { c.Sleep = rec.sleep }, "s", "a", "b")
	env.sp.script = []fakeEntry{{hold: true}, done(`{"ok":true}`)}
	feedVerdicts(t, env, []string{`model "xyz" not found`})
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if models := env.models(); len(models) != 2 || models[0] != "a" || models[1] != "b" {
		t.Fatalf("dispatched %v, want [a b]: an unresolvable model switches at once", models)
	}
	fails := failedBodies(env)
	if len(fails) != 1 || fails[0]["cause"] != FailExitModel || fails[0]["disposition"] != "switch_model" {
		t.Fatalf("step_failed %v, want exit_model_class / switch_model", fails)
	}
	if got := rec.got(); len(got) != 0 {
		t.Fatalf("waits %v, want none", got)
	}
}

// TestRetryPacingLadderAcrossCauses: the pacing counts any mix of the
// paced causes on the step — EWR, upstream transient, model_error wait
// 1m, 3m, 9m — and a model_error after an EWR is a first model-class
// death again (the EWR broke the model-class streak).
func TestRetryPacingLadderAcrossCauses(t *testing.T) {
	rec := &waitRecorder{}
	env := newChainEnv(t, func(c *Config) { c.Sleep = rec.sleep }, "s", "a", "b")
	ewr := fakeEntry{err: &ExitedError{Step: "s", ExitCode: 0, TurnOK: true}}
	transient := exitErr(1, "Upstream tool call has incomplete or invalid JSON arguments", true)
	env.sp.script = []fakeEntry{ewr, transient, {hold: true}, done(`{"ok":true}`)}
	go func() {
		waitForSpawns(env, 3)
		sp := env.sp.spawnAt(2)
		env.link.Verdict(liveErrorVerdict(t, env.link, sp.StepID, sp.SpawnN, gateway502, 0))
	}()
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	base := supervise.DefaultRetryBackoffBase
	want := []time.Duration{base, 3 * base, 9 * base}
	if got := rec.got(); len(got) != len(want) || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("waits %v, want %v", got, want)
	}
	if models := env.models(); len(models) != 4 || models[3] != "a" {
		t.Fatalf("dispatched %v, want every retry on a", models)
	}
}

// TestRetryPacingSchedule pins the ladder and its causes.
func TestRetryPacingSchedule(t *testing.T) {
	base := time.Minute
	for streak, want := range []time.Duration{time.Minute, 3 * time.Minute, 9 * time.Minute, 9 * time.Minute, 9 * time.Minute} {
		if got := retryPacedBackoff(streak, base); got != want {
			t.Errorf("streak %d: %v, want %v", streak, got, want)
		}
	}
	if got := retryPacedBackoff(5, 10*time.Millisecond); got != 90*time.Millisecond {
		t.Errorf("a shortened base keeps the shape: %v, want 90ms", got)
	}
	if got := retryPacedBackoff(0, 0); got != supervise.DefaultRetryBackoffBase {
		t.Errorf("an unset base reads the default: %v", got)
	}
	a := func(c string) capsule.Attempt { return capsule.Attempt{Cause: CauseInfraFailure, FailureCause: c} }
	for _, tc := range []struct {
		name  string
		prior []capsule.Attempt
		cause string
		want  time.Duration
	}{
		{"first model_error", nil, FailModelError, base},
		{"first EWR", nil, FailEndedWithoutResult, base},
		{"first transient", nil, FailUpstreamTransient, base},
		{"mixed streak", []capsule.Attempt{a(FailEndedWithoutResult), a(FailUpstreamTransient)}, FailModelError, 9 * base},
		{"another candidate's streak does not carry over",
			[]capsule.Attempt{onModel(a(FailModelError), "pi:a"), onModel(a(FailModelError), "pi:a")}, FailModelError, base},
		{"audit-only entries are skipped",
			[]capsule.Attempt{a(FailModelError), a(FailDaemonRestart), a(FailDriverRelaunch)}, FailModelError, 3 * base},
		{"another cause ends the streak", []capsule.Attempt{a(FailModelError), a(FailExitOther)}, FailModelError, base},
		{"a throttle ends the streak", []capsule.Attempt{a(FailModelError), a(FailRateLimited)}, FailUpstreamTransient, base},
		{"an accepted result ends the streak", []capsule.Attempt{a(FailModelError), {Cause: "accepted"}}, FailModelError, base},
		{"a throttle keeps the D7 ladder", []capsule.Attempt{a(FailRateLimited)}, FailRateLimited, 2 * RateLimitBackoffBase},
		{"daemon_restart owes nothing", nil, FailDaemonRestart, 0},
		{"driver_relaunch owes nothing", nil, FailDriverRelaunch, 0},
		{"a model-class exit owes nothing", nil, FailExitModel, 0},
		{"exit_other owes nothing", nil, FailExitOther, 0},
	} {
		if got := decidedBackoff(tc.prior, tc.cause, "", time.Time{}, base); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestRetryOwedFromLedger: what is left of a decided wait is read from
// execution.json alone.
func TestRetryOwedFromLedger(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC)
	started := t0.Add(-5 * time.Minute).Format(time.RFC3339Nano)
	fail := func(c string) capsule.Attempt {
		return capsule.Attempt{Cause: CauseInfraFailure, FailureCause: c, StartedAt: started, InterruptedAt: t0.Format(time.RFC3339Nano)}
	}
	audit := func(c string) capsule.Attempt {
		return capsule.Attempt{Cause: CauseInfraFailure, FailureCause: c, StartedAt: started, InterruptedAt: t0.Add(5 * time.Second).Format(time.RFC3339Nano)}
	}
	exec := func(status string, mut func(*capsule.Step), attempts ...capsule.Attempt) *capsule.Execution {
		st := &capsule.Step{Status: capsule.StepInProgress, StepStartedAt: started, Attempts: attempts}
		if mut != nil {
			mut(st)
		}
		return &capsule.Execution{WorkflowStatus: status, CurrentStep: "s", Steps: map[string]*capsule.Step{"s": st}}
	}
	base := time.Minute
	for _, tc := range []struct {
		name      string
		x         *capsule.Execution
		now       time.Time
		wantWait  time.Duration
		wantCause string
	}{
		{"first model_error, 20s in", exec(StatusRunning, nil, fail(FailModelError)), t0.Add(20 * time.Second), 40 * time.Second, FailModelError},
		{"second paced death: the 3m wait", exec(StatusRunning, nil, fail(FailEndedWithoutResult), fail(FailUpstreamTransient)), t0.Add(time.Minute), 2 * time.Minute, FailUpstreamTransient},
		{"the cap", exec(StatusRunning, nil, fail(FailModelError), fail(FailModelError), fail(FailModelError), fail(FailModelError)), t0, 9 * time.Minute, FailModelError},
		{"a restart during the wait archived nothing new", exec(StatusRunning, nil, fail(FailModelError), audit(FailDaemonRestart)), t0.Add(30 * time.Second), 30 * time.Second, FailModelError},
		{"a throttle's D7 wait", exec(StatusRunning, nil, fail(FailRateLimited)), t0, RateLimitBackoffBase, FailRateLimited},
		{"the wait is over", exec(StatusRunning, nil, fail(FailModelError)), t0.Add(2 * time.Minute), 0, ""},
		{"a dispatch started since", exec(StatusRunning, func(st *capsule.Step) { st.StepStartedAt = t0.Add(time.Minute).Format(time.RFC3339Nano) }, fail(FailModelError)), t0.Add(10 * time.Second), 0, ""},
		{"a blocked workflow owes nothing", exec(StatusBlocked, nil, fail(FailModelError)), t0, 0, ""},
		{"an open question owes nothing", exec(StatusRunning, func(st *capsule.Step) { st.PendingQuestion = &capsule.PendingQuestion{ID: "q"} }, fail(FailModelError)), t0, 0, ""},
		{"a cause without a backoff", exec(StatusRunning, nil, fail(FailExitOther)), t0, 0, ""},
		{"an accepted attempt is newest", exec(StatusRunning, nil, fail(FailModelError), capsule.Attempt{Cause: "accepted", StartedAt: started}), t0, 0, ""},
		{"no attempts", exec(StatusRunning, nil), t0, 0, ""},
		{"a clock behind the failure never waits longer than the backoff", exec(StatusRunning, nil, fail(FailModelError)), t0.Add(-time.Hour), time.Minute, FailModelError},
		// the decision kept the candidate (anchor = the model the death
		// ran): the wait is owed; it switched (the anchor moved): the next
		// dispatch is another candidate and owes nothing
		{"same candidate: owed", exec(StatusRunning, func(st *capsule.Step) { st.Candidate = "pi:a" },
			onModel(fail(FailModelError), "pi:a")), t0.Add(20 * time.Second), 40 * time.Second, FailModelError},
		{"a decided switch owes nothing",
			exec(StatusRunning, func(st *capsule.Step) { st.Candidate = "pi:b" },
				onModel(fail(FailModelError), "pi:a"), onModel(fail(FailModelError), "pi:a")), t0.Add(20 * time.Second), 0, ""},
		{"an EWR rotation owes nothing",
			exec(StatusRunning, func(st *capsule.Step) { st.Candidate = "pi:b" },
				onModel(fail(FailEndedWithoutResult), "pi:a"), onModel(fail(FailEndedWithoutResult), "pi:a")), t0, 0, ""},
		{"the new candidate's first paced death waits the base again",
			exec(StatusRunning, func(st *capsule.Step) { st.Candidate = "pi:b" },
				onModel(fail(FailModelError), "pi:a"), onModel(fail(FailModelError), "pi:a"), onModel(fail(FailModelError), "pi:b")),
			t0.Add(20 * time.Second), 40 * time.Second, FailModelError},
		{"its second paced death climbs b's own ladder",
			exec(StatusRunning, func(st *capsule.Step) { st.Candidate = "pi:b" },
				onModel(fail(FailModelError), "pi:a"), onModel(fail(FailModelError), "pi:a"),
				onModel(fail(FailEndedWithoutResult), "pi:b"), onModel(fail(FailUpstreamTransient), "pi:b")),
			t0.Add(20 * time.Second), 3*time.Minute - 20*time.Second, FailUpstreamTransient},
		{"the owner continued after the death: a new era owes nothing",
			exec(StatusRunning, func(st *capsule.Step) {
				st.Supervision = &capsule.StepSupervision{ResumedAt: t0.Add(10 * time.Second).Format(time.RFC3339Nano)}
			}, fail(FailModelError)), t0.Add(20 * time.Second), 0, ""},
		{"a continue before the death leaves its wait owed",
			exec(StatusRunning, func(st *capsule.Step) {
				st.Supervision = &capsule.StepSupervision{ResumedAt: t0.Add(-10 * time.Minute).Format(time.RFC3339Nano)}
			}, fail(FailModelError), fail(FailModelError)), t0.Add(20 * time.Second), 3*time.Minute - 20*time.Second, FailModelError},
		{"the ladder restarts after a continue",
			exec(StatusRunning, func(st *capsule.Step) {
				// between the old death (t0-9m) and the new dispatch (t0-5m)
				st.Supervision = &capsule.StepSupervision{ResumedAt: t0.Add(-6 * time.Minute).Format(time.RFC3339Nano)}
			}, capsule.Attempt{Cause: CauseInfraFailure, FailureCause: FailModelError, StartedAt: t0.Add(-9 * time.Minute).Format(time.RFC3339Nano)},
				fail(FailModelError)), t0.Add(20 * time.Second), 40 * time.Second, FailModelError},
		{"a switch then a restart archived nothing new: still nothing owed",
			exec(StatusRunning, func(st *capsule.Step) { st.Candidate = "pi:b" },
				onModel(fail(FailModelError), "pi:a"), onModel(fail(FailModelError), "pi:a"), onModel(audit(FailDaemonRestart), "pi:b")), t0.Add(20 * time.Second), 0, ""},
	} {
		wait, cause := RetryOwed(tc.x, tc.now, base)
		if wait != tc.wantWait || cause != tc.wantCause {
			t.Errorf("%s: RetryOwed = (%v, %q), want (%v, %q)", tc.name, wait, cause, tc.wantWait, tc.wantCause)
		}
	}
}

// onModel stamps the candidate an archived attempt ran.
func onModel(a capsule.Attempt, cand string) capsule.Attempt {
	a.Candidate = cand
	return a
}

// restartEnv is a successor driver over an existing capsule (its
// execution.json and the predecessor's spawnN dispatches): the daemon
// restarted, the clock reads now.
func restartEnv(t *testing.T, dir string, spawnN int64, now time.Time, rec *waitRecorder, script ...fakeEntry) (*Driver, *fakeSpawner, *safeLog, *supervise.Link) {
	t.Helper()
	sp := &fakeSpawner{script: script}
	log := &safeLog{}
	link := supervise.NewLink("wi_run")
	d, err := New(Config{
		WI:          WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath:    filepath.Join(dir, capsule.ExecutionJSON),
		Workflow:    &Workflow{Steps: []StepSpec{spec("s")}, MaxTotal: 4, MaxPerStep: 2},
		Spawner:     sp,
		Log:         log,
		Link:        link,
		Compose:     composeOf(map[string][]string{"s": {"pi:a", "pi:b"}}),
		Interrupted: &InterruptedDispatch{Detail: FailDaemonRestart},
		SpawnN:      spawnN,
		Now:         func() time.Time { return now },
		Sleep:       rec.sleep,
	})
	if err != nil {
		t.Fatalf("New (restart): %v", err)
	}
	return d, sp, log, link
}

// diesDuringWait runs a driver whose dispatches all die by a gateway 5xx
// until the context ends inside the last death's backoff (the daemon died
// mid-wait): no step_start may land after the failure write. It answers
// the capsule dir and the dispatch count.
func diesDuringWait(t *testing.T, t0 time.Time, deaths int) (string, int64) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rec := &waitRecorder{onCall: func(n int) {
		if n == deaths-1 {
			cancel()
		}
	}}
	env := newChainEnv(t, func(c *Config) {
		c.Sleep = rec.sleep
		c.Now = func() time.Time { return t0 }
	}, "s", "a", "b")
	errs := make([]string, deaths)
	for i := range errs {
		env.sp.script = append(env.sp.script, fakeEntry{hold: true})
		errs[i] = gateway502
	}
	feedVerdicts(t, env, errs)
	if err := env.d.Run(ctx); err == nil {
		t.Fatalf("Run must end on the context cancelled during the backoff")
	}
	st := env.exec(t).Steps["s"]
	last := st.Attempts[len(st.Attempts)-1]
	if st.Dispatch != 0 || last.FailureCause != FailModelError || last.StartedAt != st.StepStartedAt {
		t.Fatalf("a step_start landed after the interrupted backoff: dispatch=%d last=%+v started=%s", st.Dispatch, last, st.StepStartedAt)
	}
	return env.d.wi.CapsuleDir, int64(deaths)
}

// TestRestartHonoursOwedRetryAndResumes: the daemon dies 20s into the
// 1m wait after a first model_error; the successor serves the remaining
// 40s (declared to its supervisor), then resumes the SAME candidate.
func TestRestartHonoursOwedRetryAndResumes(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC)
	dir, n := diesDuringWait(t, t0, 1)
	rec := &waitRecorder{}
	now := t0.Add(20 * time.Second)
	d, sp, log, link := restartEnv(t, dir, n, now, rec, done(`{"ok":true}`))
	if wait, cause := RetryOwed(d.Snapshot(), now, 0); wait != 40*time.Second || cause != FailModelError {
		t.Fatalf("RetryOwed after the restart = (%v, %q), want (40s, model_error)", wait, cause)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run (restart): %v", err)
	}
	if got := rec.got(); len(got) != 1 || got[0] != 40*time.Second {
		t.Fatalf("the successor waited %v, want the remaining [40s]", got)
	}
	if ev := log.byKind("retry_wait"); len(ev) != 1 || ev[0].body["cause"] != FailModelError || ev[0].body["wait_ms"] != int64(40000) {
		t.Fatalf("retry_wait events %v, want one naming model_error and 40000ms", ev)
	}
	var decl *supervise.StepRetryWait
	for _, f := range link.Facts.Drain() {
		if w, ok := f.(supervise.StepRetryWait); ok {
			decl = &w
		}
	}
	if decl == nil || !decl.Until.Equal(now.Add(40*time.Second)) {
		t.Fatalf("step_retry_wait fact %+v, want until %v", decl, now.Add(40*time.Second))
	}
	if n := len(sp.dispatched()); n != 1 {
		t.Fatalf("dispatches after the restart %d, want 1", n)
	}
	if s := sp.spawnAt(0); s.Model != "a" || !s.Resume || s.Fresh {
		t.Fatalf("the successor dispatched %+v, want a resume of a", s)
	}
}

// TestRestartAfterSecondModelErrorSwitchesAtOnce: the ledger the second
// consecutive model_error leaves (the switch to b persisted in the anchor,
// a fresh conversation ordered), the daemon dead before the next
// step_start: the successor dispatches b at once — the 3m the paced
// streak would give a same-candidate retry is not resurrected.
func TestRestartAfterSecondModelErrorSwitchesAtOnce(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	started := func(d time.Duration) string { return t0.Add(d).Format(time.RFC3339Nano) }
	death := func(start, at time.Duration, seq int) capsule.Attempt {
		return capsule.Attempt{Cause: CauseInfraFailure, FailureCause: FailModelError, AttemptSeq: seq,
			StartedAt: started(start), InterruptedAt: started(at), Candidate: "pi:a"}
	}
	x := &capsule.Execution{
		CapsuleID: "cap_wi_run", WorkItemID: "wi_run", WorkflowStatus: StatusRunning, CurrentStep: "s", Version: 7,
		Steps: map[string]*capsule.Step{"s": {
			Status: capsule.StepInProgress, Candidate: "pi:b", StepStartedAt: started(-2 * time.Minute),
			Attempts:    []capsule.Attempt{death(-5*time.Minute, -4*time.Minute, 1), death(-2*time.Minute, 0, 2)},
			Supervision: &capsule.StepSupervision{NextDispatch: capsule.NextDispatchFresh},
		}},
	}
	writeExecFixture(t, filepath.Join(dir, capsule.ExecutionJSON), x)
	now := t0.Add(20 * time.Second)
	if wait, cause := RetryOwed(x, now, 0); wait != 0 {
		t.Fatalf("RetryOwed after a decided switch = (%v, %q), want 0", wait, cause)
	}
	rec := &waitRecorder{}
	d, sp, log, _ := restartEnv(t, dir, 2, now, rec, done(`{"ok":true}`))
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run (restart): %v", err)
	}
	if got := rec.got(); len(got) != 0 {
		t.Fatalf("the successor waited %v, want none: the next dispatch is a switch", got)
	}
	if ev := log.byKind("retry_wait"); len(ev) != 0 {
		t.Fatalf("retry_wait events %v, want none", ev)
	}
	if s := sp.spawnAt(0); s.Model != "b" || !s.Fresh {
		t.Fatalf("the successor dispatched %+v, want a fresh dispatch of b", s)
	}
}

// writeExecFixture persists an execution.json fixture.
func writeExecFixture(t *testing.T, path string, x *capsule.Execution) {
	t.Helper()
	blob, err := json.MarshalIndent(x, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, blob, 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRestartLongAfterTheWaitDoesNotWait: a successor that starts after
// the decided wait ran out dispatches at once.
func TestRestartLongAfterTheWaitDoesNotWait(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC)
	dir, n := diesDuringWait(t, t0, 1)
	rec := &waitRecorder{}
	d, sp, _, _ := restartEnv(t, dir, n, t0.Add(5*time.Minute), rec, done(`{"ok":true}`))
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run (restart): %v", err)
	}
	if got := rec.got(); len(got) != 0 {
		t.Fatalf("waits %v, want none (the wait already ran out)", got)
	}
	if s := sp.spawnAt(0); s.Model != "a" || !s.Resume {
		t.Fatalf("dispatched %+v, want a resume of a", s)
	}
}

// TestOwnerResumeWaivesTheOwedRetry: a model_error that hits the failure
// bound blocks with no wait booked; the owner's continue opens a new
// streak era (Supervision.ResumedAt), so the ledger owes the blocking
// death's pacing window to nobody — not the resumed driver, and not a
// driver that replaces it after a restart.
func TestOwnerResumeWaivesTheOwedRetry(t *testing.T) {
	rec := &waitRecorder{}
	env := newChainEnv(t, func(c *Config) {
		c.Sleep = rec.sleep
		c.MaxStepFailures = 1
	}, "s", "a", "b")
	env.sp.script = []fakeEntry{{hold: true}, done(`{"ok":true}`)}
	feedVerdicts(t, env, []string{gateway502})
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := env.exec(t)
	if x.WorkflowStatus != StatusBlocked {
		t.Fatalf("status %q, want blocked at the failure bound", x.WorkflowStatus)
	}
	if f := failedBodies(env); len(f) != 1 || f[0]["backoff_ms"] != nil {
		t.Fatalf("step_failed %v: a blocking decision owes no wait", f)
	}
	if wait, _ := RetryOwed(x, time.Now(), 0); wait != 0 {
		t.Fatalf("a blocked step shows a pending retry %v", wait)
	}
	if err := env.d.ResumeCurrent(); err != nil {
		t.Fatalf("ResumeCurrent: %v", err)
	}
	after := env.exec(t)
	if sup := after.Steps["s"].Supervision; sup == nil || sup.ResumedAt == "" {
		t.Fatalf("the owner's continue must persist its stamp: %+v", sup)
	}
	if wait, _ := RetryOwed(after, time.Now(), 0); wait != 0 {
		t.Fatalf("the ledger still owes %v after the owner's continue", wait)
	}
	// a restart between the continue and the dispatch: the successor
	// reads the same era off the file and does not wait either
	rrec := &waitRecorder{}
	d2, sp2, _, _ := restartEnv(t, env.d.wi.CapsuleDir, 1, time.Now(), rrec, done(`{"ok":true}`))
	if err := d2.Run(context.Background()); err != nil {
		t.Fatalf("Run after restart: %v", err)
	}
	if got := rrec.got(); len(got) != 0 {
		t.Fatalf("the successor after the owner's continue waited %v, want none", got)
	}
	if n := len(sp2.dispatched()); n != 1 {
		t.Fatalf("successor dispatches %d, want 1", n)
	}
	if got := rec.got(); len(got) != 0 {
		t.Fatalf("waits %v, want none", got)
	}
}

// TestOwnerContinueStartsFreshStreaks: the owner's continue resets the
// ledger-derived streaks as it resets the failure counter. No candidate
// chain, three consecutive model_errors (1m, 3m, then the bound blocks),
// the owner continues, one more 502: it is the FIRST model-class death of
// the new era — a resumed same-model retry, counted once, after the base
// wait — not the fourth of a streak that would have waited 9m.
func TestOwnerContinueStartsFreshStreaks(t *testing.T) {
	rec := &waitRecorder{}
	env := newSupEnv(t, func(c *Config) { c.Sleep = rec.sleep }, spec("s"))
	env.sp.script = []fakeEntry{{hold: true}, {hold: true}, {hold: true}, {hold: true}, done(`{"ok":true}`)}
	feedVerdicts(t, env, []string{gateway502, gateway502, gateway502, gateway502})
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if x := env.exec(t); x.WorkflowStatus != StatusBlocked {
		t.Fatalf("status %q, want blocked by the third counted model_error", x.WorkflowStatus)
	}
	base := supervise.DefaultRetryBackoffBase
	if got := rec.got(); len(got) != 2 || got[0] != base || got[1] != 3*base {
		t.Fatalf("waits before the block %v, want [1m 3m]", got)
	}
	if err := env.d.ResumeCurrent(); err != nil {
		t.Fatalf("ResumeCurrent: %v", err)
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run after resume: %v", err)
	}
	if got := rec.got(); len(got) != 3 || got[2] != base {
		t.Fatalf("waits %v, want the post-continue 502 to wait the base (1m), not 9m", got)
	}
	fails := failedBodies(env)
	if len(fails) != 4 {
		t.Fatalf("step_failed %v, want four", fails)
	}
	f := fails[3]
	if f["disposition"] != "retry_same_model" || f["next_dispatch"] != "resume" || f["failure_count"] != 1 ||
		f["backoff_ms"] != base.Milliseconds() {
		t.Fatalf("post-continue step_failed %v, want a resumed, once-counted retry after the base wait", f)
	}
	if _, ok := f["no_candidate_chain"]; ok {
		t.Fatalf("post-continue step_failed %v: a first model-class death wants no rotation", f)
	}
	if n := len(env.sp.dispatched()); n != 5 {
		t.Fatalf("dispatches %d, want 5", n)
	}
}

// listEditEnv is a supervised driver over one step whose dispatch-time
// candidate list is read from *list (an owner edit between dispatches
// changes it).
func listEditEnv(t *testing.T, rec *waitRecorder, list *[]string) *supEnv {
	t.Helper()
	return newSupEnv(t, func(c *Config) {
		c.Sleep = rec.sleep
		c.Compose = func() (map[string][]string, error) { return map[string][]string{"s": *list}, nil }
	}, spec("s"))
}

// TestListEditThenResumeSwitchesAfterABlock pins the owner's escape hatch
// the block reason names: three deaths on the only candidate block the
// step; the owner adds a candidate and continues — the next dispatch runs
// the new candidate. The continue starts the death streaks afresh, but the
// advance the chain-end death still owes is model selection and survives
// it (CandidatePointer reads the answer era, not streakEra).
func TestListEditThenResumeSwitchesAfterABlock(t *testing.T) {
	ewr := fakeEntry{err: &ExitedError{Step: "s", ExitCode: 0, TurnOK: true}}
	for _, tc := range []struct {
		name   string
		script []fakeEntry
		errs   []string
		cause  string
	}{
		{"three EWRs", []fakeEntry{ewr, ewr, ewr, done(`{"ok":true}`)}, nil, FailEndedWithoutResult},
		{"three live 502s", []fakeEntry{{hold: true}, {hold: true}, {hold: true}, done(`{"ok":true}`)},
			[]string{gateway502, gateway502, gateway502}, FailModelError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &waitRecorder{}
			list := []string{"pi:a"}
			env := listEditEnv(t, rec, &list)
			env.sp.script = tc.script
			if tc.errs != nil {
				feedVerdicts(t, env, tc.errs)
			}
			if err := env.d.Run(context.Background()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			x := env.exec(t)
			if x.WorkflowStatus != StatusBlocked || !strings.Contains(x.BlockedReason, "failed 3 times") {
				t.Fatalf("status %q (%q), want blocked at the failure bound", x.WorkflowStatus, x.BlockedReason)
			}
			list = []string{"pi:a", "pi:b"}               // the owner edits the list ...
			if err := env.d.ResumeCurrent(); err != nil { // ... and continues
				t.Fatalf("ResumeCurrent: %v", err)
			}
			if err := env.d.Run(context.Background()); err != nil {
				t.Fatalf("Run after resume: %v", err)
			}
			if models := env.models(); len(models) != 4 || models[3] != "b" {
				t.Fatalf("dispatched %v, want [a a a b]: the edited list's next candidate runs at once", models)
			}
			sw := env.log.byKind("candidate_switched")
			if len(sw) != 1 || sw[0].body["to"] != "pi:b" || sw[0].body["cause"] != tc.cause {
				t.Fatalf("candidate_switched %v, want one to pi:b naming %s", sw, tc.cause)
			}
			if x := env.exec(t); x.WorkflowStatus != StatusCompleted {
				t.Fatalf("status %q, want completed on b", x.WorkflowStatus)
			}
		})
	}
}
