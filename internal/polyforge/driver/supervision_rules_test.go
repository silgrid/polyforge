package driver

// supervision_rules_test.go: the contract 4 process supervision rules
// (design #483 §7), one test per row, and the backoff ledger they share.
//
//   - ended without a hand-off: the same session resumes; three in a row
//     block;
//   - model class (an error, a model that does not resolve, too slow,
//     hung): the next candidate at once; none left blocks, the reason
//     opening with NoUsableModelBlockPrefix;
//   - transient (a throttle, a corrupted upstream return): the same model
//     after 1s doubling to 3m, never counted;
//   - timeout (the step's total time, a hung tool call): the bound doubled,
//     the same session resumed once; the second blocks;
//   - any other exit (host class, harness silence, a crash): the same
//     session resumed once; the second in a row blocks.
//
// Every streak and owed wait is derived from the attempts ledger, so a
// driver that replaces a dead predecessor decides exactly the same.

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

// ewrEntry is a dispatch whose turn ended normally and whose process
// exited cleanly without a hand-off (ended_without_result).
var ewrEntry = fakeEntry{err: &ExitedError{Step: "s", ExitCode: 0, TurnOK: true}}

// TestModelErrorSwitchesAtOnce: a gateway 5xx (model_error) is model
// class and switches to the next candidate at once — a fresh
// conversation, no wait, the counter reset (§7 row 2).
func TestModelErrorSwitchesAtOnce(t *testing.T) {
	rec := &waitRecorder{}
	env := newChainEnv(t, func(c *Config) { c.Sleep = rec.sleep }, "s", "a", "b")
	env.sp.script = []fakeEntry{{hold: true}, done(`{"ok":true}`)}
	feedVerdicts(t, env, []string{gateway502})
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if models := env.models(); len(models) != 2 || models[0] != "a" || models[1] != "b" {
		t.Fatalf("dispatched %v, want [a b]: a model_error switches at once", models)
	}
	if sp := env.sp.spawnAt(1); !sp.Fresh || sp.Resume {
		t.Fatalf("the switched-to candidate must start fresh, got %+v", sp)
	}
	fails := failedBodies(env)
	if len(fails) != 1 || fails[0]["cause"] != FailModelError || fails[0]["disposition"] != "switch_model" ||
		fails[0]["from_model"] != "pi:a" || fails[0]["to_model"] != "pi:b" || fails[0]["failure_count"] != 0 {
		t.Fatalf("step_failed %v, want model_error / switch_model pi:a -> pi:b, count 0", fails)
	}
	if _, ok := fails[0]["backoff_ms"]; ok {
		t.Fatalf("a switch books no backoff: %v", fails[0])
	}
	if sw := env.log.byKind("candidate_switched"); len(sw) != 1 || sw[0].body["from"] != "pi:a" || sw[0].body["to"] != "pi:b" {
		t.Fatalf("candidate_switched events %+v, want one pi:a -> pi:b", sw)
	}
	if got := rec.got(); len(got) != 0 {
		t.Fatalf("waits %v, want none", got)
	}
}

// TestModelClassWithNoCandidateLeftBlocks: a model-class death on the
// last candidate — or on a step with no candidate list — blocks at once
// (no same-model retry), the reason opening with NoUsableModelBlockPrefix
// and naming the owner's way on (§7 row 2: "没有候选 → blocked"; the
// agent cannot start, so no message wakes it — WakeOf).
func TestModelClassWithNoCandidateLeftBlocks(t *testing.T) {
	for _, tc := range []struct {
		name    string
		env     func(t *testing.T, rec *waitRecorder) *supEnv
		noChain bool
	}{
		{"the list's end", func(t *testing.T, rec *waitRecorder) *supEnv {
			return newChainEnv(t, func(c *Config) { c.Sleep = rec.sleep }, "s", "a")
		}, false},
		{"no list", func(t *testing.T, rec *waitRecorder) *supEnv {
			return newSupEnv(t, func(c *Config) { c.Sleep = rec.sleep }, spec("s"))
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &waitRecorder{}
			env := tc.env(t, rec)
			env.sp.script = []fakeEntry{{hold: true}, done(`{"ok":true}`)}
			feedVerdicts(t, env, []string{gateway502})
			if err := env.d.Run(context.Background()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			x := env.exec(t)
			if x.WorkflowStatus != StatusBlocked || !strings.HasPrefix(x.BlockedReason, NoUsableModelBlockPrefix) ||
				!strings.Contains(x.BlockedReason, "pf resume") {
				t.Fatalf("status %q (%q), want blocked naming no usable model and pf resume", x.WorkflowStatus, x.BlockedReason)
			}
			if n := len(env.sp.dispatched()); n != 1 {
				t.Fatalf("dispatches %d, want 1: no same-model retry at the chain end", n)
			}
			fails := failedBodies(env)
			if len(fails) != 1 || fails[0]["disposition"] != "blocked" {
				t.Fatalf("step_failed %v, want one blocked decision", fails)
			}
			if _, ok := fails[0]["no_candidate_chain"]; ok != tc.noChain {
				t.Fatalf("step_failed %v: no_candidate_chain present=%v, want %v", fails[0], ok, tc.noChain)
			}
			if got := rec.got(); len(got) != 0 {
				t.Fatalf("waits %v, want none", got)
			}
		})
	}
}

// TestEndedWithoutHandoffResumesThenBlocksAtThree: a dispatch that ended
// without a hand-off resumes the SAME session (the reminder rides the
// prompt), never switches candidates and waits nothing; the third in a
// row blocks (§7 row 1).
func TestEndedWithoutHandoffResumesThenBlocksAtThree(t *testing.T) {
	rec := &waitRecorder{}
	env := newChainEnv(t, func(c *Config) { c.Sleep = rec.sleep }, "s", "a", "b")
	env.sp.script = []fakeEntry{ewrEntry, ewrEntry, ewrEntry, done(`{"ok":true}`)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if models := env.models(); len(models) != 3 || models[1] != "a" || models[2] != "a" {
		t.Fatalf("dispatched %v, want [a a a]: an EWR never switches", models)
	}
	if env.sp.spawnAt(0).PriorEndedWithoutResult {
		t.Fatalf("only a retry carries the reminder: %+v", env.sp.spawnAt(0))
	}
	for i := 1; i < 3; i++ {
		if sp := env.sp.spawnAt(i); !sp.Resume || sp.Fresh || !sp.PriorEndedWithoutResult {
			t.Fatalf("retry %d must resume the session with the reminder: %+v", i, sp)
		}
	}
	x := env.exec(t)
	if x.WorkflowStatus != StatusBlocked || !strings.Contains(x.BlockedReason, "3 times in a row") ||
		!strings.Contains(x.BlockedReason, `"pi:a"`) || !strings.Contains(x.BlockedReason, "pf step handoff") {
		t.Fatalf("status %q (%q), want blocked at the third naming the model and the hand-off", x.WorkflowStatus, x.BlockedReason)
	}
	if got := rec.got(); len(got) != 0 {
		t.Fatalf("waits %v, want none", got)
	}
	if sw := env.log.byKind("candidate_switched"); len(sw) != 0 {
		t.Fatalf("an EWR switched candidates: %v", sw)
	}
}

// TestTransientRetriesSameModelAfterBackoffUncounted: five consecutive
// throttles keep the ORIGINAL model (the chain has a successor and is not
// touched), resume the same session after the doubled backoff (1s, 2s, 4s,
// 8s, 16s), are never counted toward a block (five is past every other
// class's bound) and book their waits into T (§7 row 3).
func TestTransientRetriesSameModelAfterBackoffUncounted(t *testing.T) {
	rec := &waitRecorder{}
	env := newChainEnv(t, func(c *Config) { c.Sleep = rec.sleep }, "s", "a", "b", "c")
	throttle := exitErr(1, "provider request failed: HTTP 429 Too Many Requests (rate limit)", false)
	env.sp.script = []fakeEntry{throttle, throttle, throttle, throttle, throttle, done(`{"ok":true}`)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := env.exec(t)
	if x.WorkflowStatus != StatusCompleted {
		t.Fatalf("the throttled step must survive its retries: status %q (%q)", x.WorkflowStatus, x.BlockedReason)
	}
	models := env.models()
	if len(models) != 6 {
		t.Fatalf("dispatches %v, want six (five throttled + the delivery)", models)
	}
	for i, m := range models {
		if m != "a" {
			t.Fatalf("dispatch %d ran on %q — a throttle switched models (%v)", i, m, models)
		}
		if sp := env.sp.spawnAt(i); i > 0 && (!sp.Resume || sp.Fresh) {
			t.Fatalf("retry %d must resume the session: %+v", i, sp)
		}
	}
	st := x.Steps["s"]
	throttles := 0
	for _, a := range st.Attempts {
		if a.Cause != CauseInfraFailure {
			continue
		}
		if a.FailureCause != FailRateLimited {
			t.Fatalf("attempt %+v, want the rate-limit cause", a)
		}
		throttles++
	}
	if throttles != 5 {
		t.Fatalf("archived throttles %d, want five (%+v)", throttles, st.Attempts)
	}
	if st.Supervision != nil && (st.Supervision.FailureCount != 0 || st.Supervision.RelaxedWallMs != 0) {
		t.Fatalf("a throttle moves no counter and relaxes no bound: %+v", st.Supervision)
	}
	want := []time.Duration{1 * time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second}
	got := rec.got()
	if len(got) != len(want) {
		t.Fatalf("backoff waits %v, want %v", got, want)
	}
	var sum time.Duration
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("backoff %d is %v, want %v", i, got[i], want[i])
		}
		sum += want[i]
	}
	if wall := st.BudgetConsumed.WallMs; wall < sum.Milliseconds() {
		t.Fatalf("step T %dms, want the backoff waits (>= %v) booked in", wall, sum)
	}
	fails := failedBodies(env)
	if len(fails) != 5 || fails[0]["cause"] != FailRateLimited || fails[0]["disposition"] != "retry_same_model" ||
		fails[0]["backoff_ms"] != TransientBackoffBase.Milliseconds() {
		t.Fatalf("step_failed %v, want rate_limited / retry_same_model with the first backoff", fails)
	}
	if _, ok := fails[0]["to_model"]; ok {
		t.Fatalf("a throttle names no switch: %v", fails[0])
	}
}

// TestTimeoutDoublesResumesOnceThenBlocks: a hung tool call doubles Y and
// resumes the same session; the next timeout — a step-total one here, the
// class is shared — doubles T and blocks (§7 row 4).
func TestTimeoutDoublesResumesOnceThenBlocks(t *testing.T) {
	env := newSupEnv(t, func(c *Config) {
		c.Limits = supervise.Defaults{Tool: 10 * time.Minute, StepTimeout: time.Hour}
	}, spec("s"))
	env.sp.script = []fakeEntry{{hold: true}, {hold: true}, done(`{}`)}
	go func() {
		waitForSpawns(env, 1)
		env.link.Verdict(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseToolHung, Evidence: "bash 11m"})
		waitForSpawns(env, 2)
		env.link.Verdict(supervise.StepDead{Step: "s", SpawnN: 2, Cause: supervise.CauseStepTimeoutLongTask, Evidence: "T"})
	}()
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if sp := env.sp.spawnAt(1); !sp.Resume || sp.Fresh {
		t.Fatalf("the first timeout must resume the session: %+v", sp)
	}
	x := env.exec(t)
	if x.WorkflowStatus != StatusBlocked || !strings.Contains(x.BlockedReason, "timed out again") {
		t.Fatalf("status %q (%q), want blocked at the second timeout", x.WorkflowStatus, x.BlockedReason)
	}
	sup := x.Steps["s"].Supervision
	if sup.RelaxedToolMs != (20*time.Minute).Milliseconds() || sup.RelaxedWallMs != (2*time.Hour).Milliseconds() || sup.LongTaskTimeouts != 2 {
		t.Fatalf("supervision %+v, want Y and T doubled and two timeouts on the ledger", sup)
	}
	if n := len(env.sp.dispatched()); n != 2 {
		t.Fatalf("dispatches %d, want 2", n)
	}
	// the resumed dispatch runs under the doubled Y
	var started []supervise.StepStarted
	for _, f := range env.link.Facts.Drain() {
		if s, ok := f.(supervise.StepStarted); ok {
			started = append(started, s)
		}
	}
	if len(started) != 2 || started[0].Thresholds.Tool != 10*time.Minute || started[1].Thresholds.Tool != 20*time.Minute {
		t.Fatalf("thresholds on step_started %+v, want Y 10m then the doubled 20m", started)
	}
}

// TestOtherExitResumesOnceThenBlocks: any other exit — a host-class fault
// first, then a crash — resumes the same session once; the second in a
// row blocks (§7 row 5: host class is no special case any more).
func TestOtherExitResumesOnceThenBlocks(t *testing.T) {
	env := newChainEnv(t, nil, "s", "a", "b")
	env.sp.script = []fakeEntry{failEntryFor(FailExitHost), failEntryFor(FailExitOther), done(`{}`)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if models := env.models(); len(models) != 2 || models[1] != "a" {
		t.Fatalf("dispatched %v, want [a a]", models)
	}
	if sp := env.sp.spawnAt(1); !sp.Resume || sp.Fresh {
		t.Fatalf("the first other exit must resume the session: %+v", sp)
	}
	x := env.exec(t)
	if x.WorkflowStatus != StatusBlocked || !strings.Contains(x.BlockedReason, "after one resume") {
		t.Fatalf("status %q (%q), want blocked at the second other exit", x.WorkflowStatus, x.BlockedReason)
	}
	if bl := env.log.byKind("blocked"); len(bl) != 1 || bl[0].body["report"] != true {
		t.Fatalf("blocked event must report the owner: %+v", bl)
	}
	causes := []string{}
	for _, a := range x.Steps["s"].Attempts {
		causes = append(causes, a.FailureCause)
	}
	if len(causes) != 2 || causes[0] != FailExitHost || causes[1] != FailExitOther {
		t.Fatalf("archived causes %v, want [exit_host_class exit_other] (the cause names modelhealth reads stay)", causes)
	}
}

// TestDecisionClassesDoNotMix: a streak is one class's — an EWR between
// two other exits ends the other-exit streak, so neither blocks.
func TestDecisionClassesDoNotMix(t *testing.T) {
	env := newChainEnv(t, nil, "s", "a")
	env.sp.script = []fakeEntry{failEntryFor(FailExitOther), ewrEntry, failEntryFor(FailExitOther), done(`{}`)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if x := env.exec(t); x.WorkflowStatus != StatusCompleted {
		t.Fatalf("status %q (%q), want completed", x.WorkflowStatus, x.BlockedReason)
	}
}

// TestAllowanceErrorTurnStillSwitchesAtOnce: a live error turn whose
// text the exit path archives as exit_model_class — an allowance/auth
// problem (D7) or a model name that cannot resolve (F4) — is archived the
// same and keeps its immediate switch.
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
// stay model_error however the gateway wraps them (model class either way
// since contract 4; the archived cause keeps the distinction modelhealth
// reads).
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
			t.Errorf("verdictCause(%q) = %q, want %q (an outage)", text, got, FailModelError)
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

// TestDecidedBackoffSchedule pins which deaths owe a wait: the transient
// ones alone, on one shared ladder.
func TestDecidedBackoffSchedule(t *testing.T) {
	a := func(c string) capsule.Attempt { return capsule.Attempt{Cause: CauseInfraFailure, FailureCause: c} }
	for _, tc := range []struct {
		name  string
		prior []capsule.Attempt
		cause string
		want  time.Duration
	}{
		{"first throttle", nil, FailRateLimited, time.Second},
		{"first transient", nil, FailUpstreamTransient, time.Second},
		{"a mixed transient streak", []capsule.Attempt{a(FailRateLimited), a(FailUpstreamTransient)}, FailRateLimited, 4 * time.Second},
		{"audit-only entries are skipped", []capsule.Attempt{a(FailRateLimited), a(FailDaemonRestart), a(FailDriverRelaunch)}, FailUpstreamTransient, 2 * time.Second},
		{"another cause ends the streak", []capsule.Attempt{a(FailRateLimited), a(FailExitOther)}, FailRateLimited, time.Second},
		{"model_error owes nothing", nil, FailModelError, 0},
		{"an EWR owes nothing", []capsule.Attempt{a(FailEndedWithoutResult)}, FailEndedWithoutResult, 0},
		{"a timeout owes nothing", nil, FailToolHung, 0},
		{"daemon_restart owes nothing", nil, FailDaemonRestart, 0},
		{"a model-class exit owes nothing", nil, FailExitModel, 0},
		{"exit_other owes nothing", nil, FailExitOther, 0},
	} {
		if got := decidedBackoff(tc.prior, tc.cause); got != tc.want {
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
	ms := time.Millisecond
	for _, tc := range []struct {
		name      string
		x         *capsule.Execution
		now       time.Time
		wantWait  time.Duration
		wantCause string
	}{
		{"first transient, 200ms in", exec(StatusRunning, nil, fail(FailUpstreamTransient)), t0.Add(200 * ms), 800 * ms, FailUpstreamTransient},
		{"the third of a mixed streak: the 4s wait", exec(StatusRunning, nil, fail(FailRateLimited), fail(FailUpstreamTransient), fail(FailRateLimited)), t0.Add(time.Second), 3 * time.Second, FailRateLimited},
		{"the cap", exec(StatusRunning, nil, fail(FailRateLimited), fail(FailRateLimited), fail(FailRateLimited), fail(FailRateLimited),
			fail(FailRateLimited), fail(FailRateLimited), fail(FailRateLimited), fail(FailRateLimited), fail(FailRateLimited), fail(FailRateLimited)), t0, 3 * time.Minute, FailRateLimited},
		{"a restart during the wait archived nothing new", exec(StatusRunning, nil, fail(FailRateLimited), audit(FailDaemonRestart)), t0.Add(300 * ms), 700 * ms, FailRateLimited},
		{"the wait is over", exec(StatusRunning, nil, fail(FailRateLimited)), t0.Add(2 * time.Second), 0, ""},
		{"a dispatch started since", exec(StatusRunning, func(st *capsule.Step) { st.StepStartedAt = t0.Add(time.Minute).Format(time.RFC3339Nano) }, fail(FailRateLimited)), t0.Add(10 * ms), 0, ""},
		{"a blocked workflow owes nothing", exec(StatusBlocked, nil, fail(FailRateLimited)), t0, 0, ""},
		{"a cause without a backoff", exec(StatusRunning, nil, fail(FailModelError)), t0, 0, ""},
		{"an EWR owes nothing", exec(StatusRunning, nil, fail(FailEndedWithoutResult)), t0, 0, ""},
		{"an accepted attempt is newest", exec(StatusRunning, nil, fail(FailRateLimited), capsule.Attempt{Cause: "accepted", StartedAt: started}), t0, 0, ""},
		{"no attempts", exec(StatusRunning, nil), t0, 0, ""},
		{"a clock behind the failure never waits longer than the backoff", exec(StatusRunning, nil, fail(FailRateLimited)), t0.Add(-time.Hour), time.Second, FailRateLimited},
		{"same candidate: owed", exec(StatusRunning, func(st *capsule.Step) { st.Candidate = "pi:a" },
			onModel(fail(FailRateLimited), "pi:a")), t0.Add(200 * ms), 800 * ms, FailRateLimited},
		{"the anchor moved (an owner list edit): nothing owed",
			exec(StatusRunning, func(st *capsule.Step) { st.Candidate = "pi:b" }, onModel(fail(FailRateLimited), "pi:a")), t0, 0, ""},
		{"the owner continued after the death: a new era owes nothing",
			exec(StatusRunning, func(st *capsule.Step) {
				st.Supervision = &capsule.StepSupervision{ResumedAt: t0.Add(10 * ms).Format(time.RFC3339Nano)}
			}, fail(FailRateLimited)), t0.Add(20 * ms), 0, ""},
		{"a continue before the death leaves its wait owed",
			exec(StatusRunning, func(st *capsule.Step) {
				st.Supervision = &capsule.StepSupervision{ResumedAt: t0.Add(-10 * time.Minute).Format(time.RFC3339Nano)}
			}, fail(FailRateLimited), fail(FailRateLimited)), t0.Add(200 * ms), 1800 * ms, FailRateLimited},
	} {
		wait, cause := RetryOwed(tc.x, tc.now)
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
		Workflow:    testLane(4, []StepSpec{spec("s")}...),
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

// diesDuringWait runs a driver whose dispatches all die by a throttle
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
	for range deaths {
		env.sp.script = append(env.sp.script, exitErr(1, "429 Too Many Requests", false))
	}
	if err := env.d.Run(ctx); err == nil {
		t.Fatalf("Run must end on the context cancelled during the backoff")
	}
	st := env.exec(t).Steps["s"]
	last := st.Attempts[len(st.Attempts)-1]
	if st.Dispatch != 0 || last.FailureCause != FailRateLimited || last.StartedAt != st.StepStartedAt {
		t.Fatalf("a step_start landed after the interrupted backoff: dispatch=%d last=%+v started=%s", st.Dispatch, last, st.StepStartedAt)
	}
	return env.d.wi.CapsuleDir, int64(deaths)
}

// TestRestartHonoursOwedRetryAndResumes: the daemon dies 300ms into the
// 1s wait after a first throttle; the successor serves the remaining
// 700ms (declared to its supervisor), then resumes the SAME candidate.
func TestRestartHonoursOwedRetryAndResumes(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC)
	dir, n := diesDuringWait(t, t0, 1)
	rec := &waitRecorder{}
	now := t0.Add(300 * time.Millisecond)
	left := 700 * time.Millisecond
	d, sp, log, link := restartEnv(t, dir, n, now, rec, done(`{"ok":true}`))
	if wait, cause := RetryOwed(d.Snapshot(), now); wait != left || cause != FailRateLimited {
		t.Fatalf("RetryOwed after the restart = (%v, %q), want (700ms, rate_limited)", wait, cause)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run (restart): %v", err)
	}
	if got := rec.got(); len(got) != 1 || got[0] != left {
		t.Fatalf("the successor waited %v, want the remaining [700ms]", got)
	}
	if ev := log.byKind("retry_wait"); len(ev) != 1 || ev[0].body["cause"] != FailRateLimited || ev[0].body["wait_ms"] != int64(700) {
		t.Fatalf("retry_wait events %v, want one naming rate_limited and 700ms", ev)
	}
	var decl *supervise.StepRetryWait
	for _, f := range link.Facts.Drain() {
		if w, ok := f.(supervise.StepRetryWait); ok {
			decl = &w
		}
	}
	if decl == nil || !decl.Until.Equal(now.Add(left)) {
		t.Fatalf("step_retry_wait fact %+v, want until %v", decl, now.Add(left))
	}
	if n := len(sp.dispatched()); n != 1 {
		t.Fatalf("dispatches after the restart %d, want 1", n)
	}
	if s := sp.spawnAt(0); s.Model != "a" || !s.Resume || s.Fresh {
		t.Fatalf("the successor dispatched %+v, want a resume of a", s)
	}
}

// TestRestartAfterModelErrorSwitchesAtOnce: the ledger a model_error
// leaves (the switch to b persisted in the anchor, a fresh conversation
// ordered), the daemon dead before the next step_start: the successor
// dispatches b at once.
func TestRestartAfterModelErrorSwitchesAtOnce(t *testing.T) {
	t0 := time.Date(2026, 10, 26, 4, 0, 0, 0, time.UTC)
	dir := t.TempDir()
	started := func(d time.Duration) string { return t0.Add(d).Format(time.RFC3339Nano) }
	x := &capsule.Execution{
		CapsuleID: "cap_wi_run", WorkItemID: "wi_run", WorkflowStatus: StatusRunning, CurrentStep: "s", Version: 7,
		Steps: map[string]*capsule.Step{"s": {
			Status: capsule.StepInProgress, Candidate: "pi:b", StepStartedAt: started(-2 * time.Minute),
			Attempts: []capsule.Attempt{{Cause: CauseInfraFailure, FailureCause: FailModelError, AttemptSeq: 1,
				StartedAt: started(-2 * time.Minute), InterruptedAt: started(0), Candidate: "pi:a"}},
			Supervision: &capsule.StepSupervision{NextDispatch: capsule.NextDispatchFresh},
		}},
	}
	writeExecFixture(t, filepath.Join(dir, capsule.ExecutionJSON), x)
	now := t0.Add(20 * time.Second)
	if wait, cause := RetryOwed(x, now); wait != 0 {
		t.Fatalf("RetryOwed after a decided switch = (%v, %q), want 0", wait, cause)
	}
	rec := &waitRecorder{}
	d, sp, log, _ := restartEnv(t, dir, 1, now, rec, done(`{"ok":true}`))
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

// TestOwnerResumeWaivesTheOwedRetry: a throttle owes its wait; the
// owner's continue (here after a block by the next death) opens a new
// streak era (Supervision.ResumedAt), so the ledger owes nothing — not to
// the resumed driver, and not to a driver that replaces it after a
// restart.
func TestOwnerResumeWaivesTheOwedRetry(t *testing.T) {
	rec := &waitRecorder{}
	env := newChainEnv(t, func(c *Config) { c.Sleep = rec.sleep }, "s", "a")
	env.sp.script = []fakeEntry{exitErr(1, "429 Too Many Requests", false), {hold: true}, done(`{"ok":true}`)}
	go func() {
		waitForSpawns(env, 2)
		sp := env.sp.spawnAt(1)
		env.link.Verdict(liveErrorVerdict(t, env.link, sp.StepID, sp.SpawnN, gateway502, 0))
	}()
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := env.exec(t)
	if x.WorkflowStatus != StatusBlocked {
		t.Fatalf("status %q, want blocked (no candidate left)", x.WorkflowStatus)
	}
	if wait, _ := RetryOwed(x, time.Now()); wait != 0 {
		t.Fatalf("a blocked step shows a pending retry %v", wait)
	}
	if err := env.d.ResumeCurrent(); err != nil {
		t.Fatalf("ResumeCurrent: %v", err)
	}
	after := env.exec(t)
	if sup := after.Steps["s"].Supervision; sup == nil || sup.ResumedAt == "" {
		t.Fatalf("the owner's continue must persist its stamp: %+v", sup)
	}
	if wait, _ := RetryOwed(after, time.Now()); wait != 0 {
		t.Fatalf("the ledger still owes %v after the owner's continue", wait)
	}
	rrec := &waitRecorder{}
	d2, sp2, _, _ := restartEnv(t, env.d.wi.CapsuleDir, 2, time.Now(), rrec, done(`{"ok":true}`))
	if err := d2.Run(context.Background()); err != nil {
		t.Fatalf("Run after restart: %v", err)
	}
	if got := rrec.got(); len(got) != 0 {
		t.Fatalf("the successor after the owner's continue waited %v, want none", got)
	}
	if n := len(sp2.dispatched()); n != 1 {
		t.Fatalf("successor dispatches %d, want 1", n)
	}
}

// TestOwnerContinueStartsFreshStreaks: the owner's continue resets the
// ledger-derived streaks. Three EWRs block; the owner continues; one more
// EWR is the FIRST of the new era — resumed, counted once, no block.
func TestOwnerContinueStartsFreshStreaks(t *testing.T) {
	env := newSupEnv(t, nil, spec("s"))
	env.sp.script = []fakeEntry{ewrEntry, ewrEntry, ewrEntry, ewrEntry, done(`{"ok":true}`)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if x := env.exec(t); x.WorkflowStatus != StatusBlocked {
		t.Fatalf("status %q, want blocked by the third EWR", x.WorkflowStatus)
	}
	if err := env.d.ResumeCurrent(); err != nil {
		t.Fatalf("ResumeCurrent: %v", err)
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run after resume: %v", err)
	}
	fails := failedBodies(env)
	if len(fails) != 4 {
		t.Fatalf("step_failed %v, want four", fails)
	}
	if f := fails[3]; f["disposition"] != "retry_same_model" || f["next_dispatch"] != "resume" || f["failure_count"] != 1 {
		t.Fatalf("post-continue step_failed %v, want a resumed, once-counted retry", f)
	}
	if x := env.exec(t); x.WorkflowStatus != StatusCompleted {
		t.Fatalf("status %q, want completed", x.WorkflowStatus)
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
// the no-usable-model block names: a model-class death on the only
// candidate blocks; the owner adds a candidate and continues — the next
// dispatch runs the new candidate (the advance the chain-end death still
// owes is model selection and survives the continue: CandidatePointer
// reads no streak era).
func TestListEditThenResumeSwitchesAfterABlock(t *testing.T) {
	rec := &waitRecorder{}
	list := []string{"pi:a"}
	env := listEditEnv(t, rec, &list)
	env.sp.script = []fakeEntry{{hold: true}, done(`{"ok":true}`)}
	feedVerdicts(t, env, []string{gateway502})
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := env.exec(t)
	if x.WorkflowStatus != StatusBlocked || !strings.HasPrefix(x.BlockedReason, NoUsableModelBlockPrefix) {
		t.Fatalf("status %q (%q), want blocked on no usable model", x.WorkflowStatus, x.BlockedReason)
	}
	list = []string{"pi:a", "pi:b"}               // the owner edits the list ...
	if err := env.d.ResumeCurrent(); err != nil { // ... and continues
		t.Fatalf("ResumeCurrent: %v", err)
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run after resume: %v", err)
	}
	if models := env.models(); len(models) != 2 || models[1] != "b" {
		t.Fatalf("dispatched %v, want [a b]: the edited list's next candidate runs at once", models)
	}
	if x := env.exec(t); x.WorkflowStatus != StatusCompleted {
		t.Fatalf("status %q, want completed on b", x.WorkflowStatus)
	}
}
