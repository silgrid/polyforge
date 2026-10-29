package driver

// supervision_test.go: the driver half of the driver <-> supervisor
// contract (v2-converge 2.2/2.3/2.4): the death-cause table (switch
// candidate / same-model retry with a relaxed bound / blocked), the
// per-step failure counter and its resets, the retry shape, the spawn
// fence on verdicts (one verdict, one failure record per dispatch), the
// facts the driver sends, and the thresholds (global defaults, step
// overrides from workflow.md, relaxed values persisted).

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// safeLog is a goroutine-safe EventLogger.
type safeLog struct {
	mu      sync.Mutex
	entries []loggedEvent
}

func (l *safeLog) Append(wi, kind string, body any) error {
	b, _ := body.(map[string]any)
	l.mu.Lock()
	l.entries = append(l.entries, loggedEvent{wi: wi, kind: kind, body: b})
	l.mu.Unlock()
	return nil
}

func (l *safeLog) byKind(kind string) []loggedEvent {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []loggedEvent
	for _, e := range l.entries {
		if e.kind == kind {
			out = append(out, e)
		}
	}
	return out
}

// supEnv is a driver over a scripted spawner with a supervision link,
// per-step candidate chains and a safe event log.
type supEnv struct {
	d        *Driver
	sp       *fakeSpawner
	link     *supervise.Link
	log      *safeLog
	execPath string
}

func newSupEnv(t *testing.T, mod func(*Config), steps ...StepSpec) *supEnv {
	t.Helper()
	dir := t.TempDir()
	execPath := filepath.Join(dir, capsule.ExecutionJSON)
	sp := &fakeSpawner{}
	link := supervise.NewLink("wi_run")
	log := &safeLog{}
	cfg := Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: execPath,
		Workflow: &Workflow{Steps: steps, MaxTotal: 4, MaxPerStep: 2},
		Spawner:  sp,
		Log:      log,
		Link:     link,
	}
	if mod != nil {
		mod(&cfg)
	}
	d, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return &supEnv{d: d, sp: sp, link: link, log: log, execPath: execPath}
}

// newChainEnv is newSupEnv over one step id whose claim-time candidate
// list is models; a bare test id runs on pi (the test reads as the
// dispatched model ids — every candidate is `<harness>:<model>`,
// polyforge#175).
func newChainEnv(t *testing.T, mod func(*Config), id string, models ...string) *supEnv {
	t.Helper()
	list := []string{}
	for _, m := range models {
		if !strings.HasPrefix(m, "pi:") && !strings.HasPrefix(m, "claude:") {
			m = "pi:" + m
		}
		list = append(list, m)
	}
	return newSupEnv(t, func(c *Config) {
		if mod != nil {
			mod(c)
		}
		c.Compose = composeOf(map[string][]string{id: list})
	}, spec(id))
}

func exitErr(code int, lastErr string, turnErr bool) fakeEntry {
	return fakeEntry{err: &ExitedError{Step: "s", SpawnN: 1, ExitCode: code, LastError: lastErr, TurnError: turnErr}}
}

func (e *supEnv) models() []string {
	var out []string
	for i := range e.sp.dispatched() {
		out = append(out, e.sp.spawnAt(i).Model)
	}
	return out
}

func (e *supEnv) exec(t *testing.T) *capsule.Execution { return readExec(t, e.execPath) }

// --- exit classification -------------------------------------------------

func TestClassifyWaitFailure(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"last signal was an error turn", &ExitedError{ExitCode: 1, TurnError: true}, FailExitModel},
		{"unparseable model on stderr", &ExitedError{ExitCode: 1, LastError: `error: model "x/y" not found (unknown model)`}, FailExitModel},
		{"401 auth", &ExitedError{ExitCode: 1, LastError: "provider returned 401 Unauthorized"}, FailExitModel},
		{"invalid api key", &ExitedError{ExitCode: 1, LastError: "Invalid API key provided"}, FailExitModel},
		// polyforge#147 D7: only transient throttles are their own class —
		// the same model is retried after a backoff, the chain stays put;
		// allowance/permission problems stay model-class and switch.
		{"quota is model-class", &ExitedError{ExitCode: 1, LastError: "You exceeded your current quota"}, FailExitModel},
		{"403 insufficient permissions", &ExitedError{ExitCode: 1, LastError: "403 insufficient permissions"}, FailExitModel},
		{"429", &ExitedError{ExitCode: 1, LastError: "provider returned HTTP 429"}, FailRateLimited},
		{"overloaded outranks an error turn", &ExitedError{ExitCode: 1, TurnError: true, LastError: "upstream overloaded"}, FailRateLimited},
		{"harness-reported ENOSPC", &ExitedError{ExitCode: 1, LastError: "write: no space left on device"}, FailExitHost},
		{"harness-reported EACCES", &ExitedError{ExitCode: 1, LastError: "open /x: permission denied"}, FailExitHost},
		{"host outranks a model-looking error turn", &ExitedError{ExitCode: 1, TurnError: true, LastError: "ENOSPC: no space left"}, FailExitHost},
		{"startup death: prompt write EPIPE carrying a model stderr", errors.New(`runtime: initial prompt injection failed (process torn down): write |1: broken pipe; harness stderr: Error: model "x/y" not found`), FailExitModel},
		{"startup death: 401 on stderr", errors.New("runtime: initial prompt injection failed (process torn down): broken pipe; harness stderr: 401 Unauthorized"), FailExitModel},
		{"startup death with no explanation", errors.New("runtime: initial prompt injection failed (process torn down): broken pipe"), FailExitOther},
		{"stale model text cannot taint a clean ok-turn exit", &ExitedError{ExitCode: 0, TurnOK: true, LastError: "rate limit 429 (earlier, retried)"}, FailEndedWithoutResult},
		{"plain crash", &ExitedError{ExitCode: 101}, FailExitOther},
		{"operator kill", &ExitedError{ExitCode: -1, Signal: "killed"}, FailExitOther},
		{"driver's own ENOSPC", fmt.Errorf("staging: %w", syscall.ENOSPC), FailExitHost},
		{"driver's own EACCES", fmt.Errorf("staging: %w", os.ErrPermission), FailExitHost},
		{"driver's own other error", errors.New("something odd"), FailExitOther},
	}
	for _, tc := range cases {
		got, _, _ := classifyWaitFailure(tc.err)
		if got != tc.want {
			t.Errorf("%s: classified %q, want %q", tc.name, got, tc.want)
		}
	}
}

// --- the death-cause table ----------------------------------------------

// failOnce drives one failed dispatch of the given cause on a step with
// the supplied chain, followed by one healthy dispatch, and returns the
// environment.
func failOnce(t *testing.T, cause string, models ...string) *supEnv {
	t.Helper()
	env := newChainEnv(t, nil, "s", models...)
	env.sp.script = []fakeEntry{failEntryFor(cause), done(`{"ok":true}`)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	return env
}

func failEntryFor(cause string) fakeEntry {
	switch cause {
	case FailExitModel:
		return exitErr(1, "model not found", false)
	case FailExitHost:
		return exitErr(1, "no space left on device", false)
	default:
		return exitErr(137, "", false)
	}
}

func TestDeathCauseTableSameModelRetry(t *testing.T) {
	// harness_silent / tool_hung / exit_other: same-model retry, counted
	for _, cause := range []string{FailExitOther} {
		env := failOnce(t, cause, "a", "b", "c")
		if m := env.models(); len(m) != 2 || m[0] != "a" || m[1] != "a" {
			t.Fatalf("%s: models %v, want the same model twice", cause, m)
		}
		x := env.exec(t)
		st := x.Steps["s"]
		if st.Supervision == nil || st.Supervision.FailureCount != 1 {
			t.Fatalf("%s: failure ledger %+v, want count 1", cause, st.Supervision)
		}
		var found bool
		for _, a := range st.Attempts {
			if a.Cause == CauseInfraFailure && a.FailureCause == cause {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: attempts %+v must record the cause sub-class", cause, st.Attempts)
		}
	}
}

func TestDeathCauseTableModelClassSwitchesCandidate(t *testing.T) {
	env := failOnce(t, FailExitModel, "a", "b", "c")
	if m := env.models(); len(m) != 2 || m[0] != "a" || m[1] != "b" {
		t.Fatalf("models %v, want a then b", m)
	}
	st := env.exec(t).Steps["s"]
	if st.Supervision == nil || st.Supervision.FailureCount != 0 {
		t.Fatalf("a switch to a new candidate resets the counter: %+v", st.Supervision)
	}
	if st.Attempts[0].FailureCause != FailExitModel {
		t.Fatalf("attempt %+v must carry the cause", st.Attempts[0])
	}
	if sw := env.log.byKind("candidate_switched"); len(sw) != 1 || sw[0].body["from"] != "pi:a" || sw[0].body["to"] != "pi:b" {
		t.Fatalf("candidate_switched events: %+v", sw)
	}
	// the switch starts a NEW conversation
	if !env.sp.spawnAt(1).Fresh || env.sp.spawnAt(1).Resume {
		t.Fatalf("a candidate switch dispatches fresh: %+v", env.sp.spawnAt(1))
	}
}

func TestDeathCauseTableHostClassBlocksWithoutSwitching(t *testing.T) {
	env := newChainEnv(t, nil, "s", "a", "b")
	env.sp.script = []fakeEntry{failEntryFor(FailExitHost)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := env.exec(t)
	if x.WorkflowStatus != StatusBlocked || !strings.Contains(x.BlockedReason, "host-class") {
		t.Fatalf("status %q reason %q, want blocked on a host-class fault", x.WorkflowStatus, x.BlockedReason)
	}
	if n := len(env.sp.dispatched()); n != 1 {
		t.Fatalf("a host fault must not redispatch (no model switch): %v", env.models())
	}
	if x.Steps["s"].Attempts[0].FailureCause != FailExitHost {
		t.Fatalf("attempt %+v must carry the host cause", x.Steps["s"].Attempts[0])
	}
	bl := env.log.byKind("blocked")
	if len(bl) != 1 || bl[0].body["report"] != true {
		t.Fatalf("blocked event must report the owner: %+v", bl)
	}
}

func TestFailureCounterBlocksAtThree(t *testing.T) {
	env := newSupEnv(t, nil, spec("s")) // no candidate chain: nothing to switch
	env.sp.script = []fakeEntry{failEntryFor(FailExitOther), failEntryFor(FailExitOther), failEntryFor(FailExitOther), done(`{}`)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if n := len(env.sp.dispatched()); n != 3 {
		t.Fatalf("the third counted failure blocks: dispatches %v", env.sp.dispatched())
	}
	x := env.exec(t)
	if x.WorkflowStatus != StatusBlocked || !strings.Contains(x.BlockedReason, "failed 3 times") {
		t.Fatalf("status %q reason %q", x.WorkflowStatus, x.BlockedReason)
	}
	if c := x.Steps["s"].Supervision.FailureCount; c != 3 {
		t.Fatalf("persisted counter %d, want 3", c)
	}
	// Retry shape: 1st retry resumes the transcript, 2nd+ is fresh.
	if sp := env.sp.spawnAt(1); !sp.Resume || sp.Fresh {
		t.Fatalf("1st retry must resume: %+v", sp)
	}
	if sp := env.sp.spawnAt(2); sp.Resume || !sp.Fresh {
		t.Fatalf("2nd retry must be fresh: %+v", sp)
	}
}

func TestHumanContinueResetsCounter(t *testing.T) {
	env := newSupEnv(t, nil, spec("s"))
	env.sp.script = []fakeEntry{failEntryFor(FailExitOther), failEntryFor(FailExitOther), failEntryFor(FailExitOther)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if env.exec(t).WorkflowStatus != StatusBlocked {
		t.Fatal("setup: the step must be blocked")
	}
	if err := env.d.ResumeCurrent(); err != nil {
		t.Fatalf("continue: %v", err)
	}
	x := env.exec(t)
	if x.WorkflowStatus != StatusRunning || x.BlockedReason != "" {
		t.Fatalf("continue must unblock and clear the reason: %q %q", x.WorkflowStatus, x.BlockedReason)
	}
	if c := x.Steps["s"].Supervision.FailureCount; c != 0 {
		t.Fatalf("a human continue resets the counter, got %d", c)
	}
}

func TestGateRewindIntoStepResetsLedger(t *testing.T) {
	env := newSupEnv(t, nil, spec("fix"), gateSpec("review", "fix"))
	env.sp.script = []fakeEntry{
		failEntryFor(FailExitOther), // fix fails once
		done(`{"summary":"fix"}`),
		gateFAIL(), // the gate FAILs: rewind into fix
		fakeEntry{hold: true},
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- env.d.Run(ctx) }()
	waitUntil(t, "the post-rewind fix dispatch", func() bool { return len(env.sp.dispatched()) >= 4 })
	cancel()
	<-runDone
	st := env.exec(t).Steps["fix"]
	if st.Supervision != nil {
		t.Fatalf("a gate rewind into the step resets its failure ledger: %+v", st.Supervision)
	}
}

func TestEnvelopeRejectionsAreNotCounted(t *testing.T) {
	// #107: in-process envelope retries have their own bound and never
	// feed the failure counter.
	env := newSupEnv(t, nil, spec("s"))
	// a result with a wrong spawn fence is rejected at validation
	rejected := fakeEntry{res: &StepResult{Outcome: OutcomeCompleted, SpawnN: 99, Output: []byte(`{}`)}}
	env.sp.script = []fakeEntry{rejected, done(`{}`)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	st := env.exec(t).Steps["s"]
	if st.Supervision != nil && st.Supervision.FailureCount != 0 {
		t.Fatalf("an envelope rejection was counted as a failure: %+v", st.Supervision)
	}
	for _, a := range st.Attempts {
		if a.Cause == CauseInfraFailure {
			t.Fatalf("an envelope rejection archived an infra failure: %+v", a)
		}
	}
}

func TestCandidatesExhaustedCountsAsSameModelRetry(t *testing.T) {
	env := newChainEnv(t, nil, "s", "a", "b")
	env.sp.script = []fakeEntry{
		failEntryFor(FailExitModel), // a -> b (switch, counter reset)
		failEntryFor(FailExitModel), // b is the end: same-model retry, counted 1
		failEntryFor(FailExitModel), // counted 2
		failEntryFor(FailExitModel), // counted 3: blocked
		done(`{}`),
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if m := env.models(); fmt.Sprint(m) != "[a b b b]" {
		t.Fatalf("models %v, want a, then b held at the end of the chain", m)
	}
	x := env.exec(t)
	if x.WorkflowStatus != StatusBlocked {
		t.Fatalf("status %q, want blocked after the counter bound on the last candidate", x.WorkflowStatus)
	}
	if c := x.Steps["s"].Supervision.FailureCount; c != 3 {
		t.Fatalf("counter %d, want 3", c)
	}
	if ex := env.log.byKind("candidates_exhausted"); len(ex) == 0 {
		t.Fatal("the decision event must say the candidates are exhausted")
	}
}

func TestPersistentEnvelopeRejectionReports(t *testing.T) {
	// persistent envelope rejection reports (the result contract is
	// broken, not the model) before the loop gives up
	env := newSupEnv(t, nil, spec("s"))
	var script []fakeEntry
	for i := 0; i < maxEnvelopeRejects+2; i++ {
		script = append(script, fakeEntry{res: &StepResult{Outcome: OutcomeCompleted, SpawnN: 99, Output: []byte(`{}`)}})
	}
	env.sp.script = script
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("a persistently rejected envelope blocks the step cleanly (no relaunch lane): %v", err)
	}
	if rep := env.log.byKind("schema_fault"); len(rep) != 1 {
		t.Fatalf("schema_fault report events: %+v", rep)
	}
	x := env.exec(t)
	if x.WorkflowStatus != StatusBlocked || !strings.Contains(x.BlockedReason, "result_invalid") {
		t.Fatalf("status %q reason %q, want blocked with result_invalid", x.WorkflowStatus, x.BlockedReason)
	}
	if x.RewindStats.Count != 0 {
		t.Fatalf("a result-invalid block must not fire a rewind: %+v", x.RewindStats)
	}
}

// --- relaxed bounds ---------------------------------------------------

func TestToolHungRelaxesYAndPersists(t *testing.T) {
	env := newSupEnv(t, func(c *Config) {
		c.Limits = supervise.Defaults{Tool: 10 * time.Minute}
	}, spec("s"))
	env.sp.script = []fakeEntry{fakeEntry{hold: true}, done(`{}`)}
	go func() {
		waitForSpawns(env, 1)
		env.link.Verdict(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseToolHung, Evidence: "bash 11m"})
	}()
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	sup := env.exec(t).Steps["s"].Supervision
	if sup.RelaxedToolMs != (20 * time.Minute).Milliseconds() {
		t.Fatalf("Y must double: %+v", sup)
	}
	if sup.FailureCount != 1 || env.models()[0] != env.models()[1] {
		t.Fatalf("tool_hung is a same-model retry, counted: %+v %v", sup, env.models())
	}
	// the second dispatch's step_started carries the relaxed Y
	var started []supervise.StepStarted
	for _, f := range env.link.Facts.Drain() {
		if s, ok := f.(supervise.StepStarted); ok {
			started = append(started, s)
		}
	}
	if len(started) != 2 || started[0].Thresholds.Tool != 10*time.Minute || started[1].Thresholds.Tool != 20*time.Minute {
		t.Fatalf("thresholds on step_started: %+v", started)
	}
}

func TestRelaxedBoundsSurviveRestart(t *testing.T) {
	// the persisted relaxed Y/T are re-sent on step_started after a
	// restart (a new Driver over the same execution.json), and the restart
	// re-dispatch is NOT a counted failure
	env := newSupEnv(t, nil, spec("s"))
	env.sp.script = []fakeEntry{{hold: true}, {hold: true}}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		waitForSpawns(env, 1)
		env.link.Verdict(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseStepTimeoutLongTask, Evidence: "x"})
	}()
	runDone := make(chan error, 1)
	go func() { runDone <- env.d.Run(ctx) }()
	waitUntil(t, "second dispatch", func() bool { return len(env.sp.dispatched()) >= 2 })
	cancel() // the daemon goes away mid-dispatch
	<-runDone

	// restart: a fresh driver over the same file
	sp2 := &fakeSpawner{script: []fakeEntry{done(`{}`)}}
	link2 := supervise.NewLink("wi_run")
	d2, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: filepath.Dir(env.execPath), BundleName: "b"},
		ExecPath: env.execPath, Workflow: &Workflow{Steps: []StepSpec{spec("s")}, MaxTotal: 4, MaxPerStep: 2},
		Spawner: sp2, Link: link2, SpawnN: env.d.SpawnN(),
		Limits: supervise.Defaults{StepTimeout: time.Hour},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := d2.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var th supervise.Thresholds
	for _, f := range link2.Facts.Drain() {
		if s, ok := f.(supervise.StepStarted); ok {
			th = s.Thresholds
		}
	}
	if th.StepTimeout != 3*time.Hour { // the first run's T (90m default) doubled
		t.Fatalf("after a restart step_started must carry the relaxed T (3h), got %+v", th)
	}
	st := readExec(t, env.execPath).Steps["s"]
	if st.Supervision.FailureCount != 1 {
		t.Fatalf("restart must not count: %+v", st.Supervision)
	}
}

func waitForSpawns(env *supEnv, n int) {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if len(env.sp.dispatched()) >= n {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func TestStepTimeoutAccumulatesAcrossDispatches(t *testing.T) {
	// T runs over the step's CUMULATIVE runtime: the second dispatch is
	// told how much of T earlier dispatches already used. The clock
	// advances 7 minutes on every reading, so each dispatch "takes" 7m.
	var mu sync.Mutex
	tick := 0
	env := newSupEnv(t, func(c *Config) {
		c.Now = func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			tick++
			return time.Unix(1_000_000, 0).Add(time.Duration(tick) * 7 * time.Minute)
		}
	}, spec("s"))
	env.sp.script = []fakeEntry{exitErr(1, "", false), done(`{}`)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var started []supervise.StepStarted
	for _, f := range env.link.Facts.Drain() {
		if s, ok := f.(supervise.StepStarted); ok {
			started = append(started, s)
		}
	}
	if len(started) != 2 {
		t.Fatalf("step_started facts: %d", len(started))
	}
	if started[0].Thresholds.PriorRuntime != 0 {
		t.Fatalf("first dispatch has no prior runtime: %v", started[0].Thresholds.PriorRuntime)
	}
	if prior := started[1].Thresholds.PriorRuntime; prior < 7*time.Minute {
		t.Fatalf("second dispatch prior runtime %v, want at least the first dispatch's 7m", prior)
	}
	if wall := env.exec(t).Steps["s"].BudgetConsumed.WallMs; wall < (7 * time.Minute).Milliseconds() {
		t.Fatalf("the finished dispatches' wall time must be booked: %dms", wall)
	}
}

// --- thresholds from workflow.md --------------------------------------

// TestStepOverridesFromWorkflowFrontmatter: budget.wall_minutes is the
// ONLY per-step threshold face left (polyforge#147 C5 retired the
// per-step silence_minutes / tool_minutes overrides — those keys are no
// longer read, and the global defaults hold for every step).
func TestStepOverridesFromWorkflowFrontmatter(t *testing.T) {
	dir := t.TempDir()
	bundle := filepath.Join(dir, "workflows", "b")
	if err := os.MkdirAll(bundle, 0o755); err != nil {
		t.Fatal(err)
	}
	md := "---\nname: b\nsteps:\n" +
		"  - { id: slow, budget: { wall_minutes: 45 }, silence_minutes: 20, tool_minutes: 60 }\n" + // the retired keys are ignored
		"  - { id: small, budget: { wall_minutes: 15 } }\n" +
		"  - { id: plain }\n---\n# b\n"
	if err := os.WriteFile(filepath.Join(bundle, "workflow.md"), []byte(md), 0o644); err != nil {
		t.Fatal(err)
	}
	wf, err := LoadWorkflow(bundle)
	if err != nil {
		t.Fatal(err)
	}
	slow := wf.step("slow")
	if slow.WallMinutes != 45 {
		t.Fatalf("overrides parsed as %+v", slow)
	}
	d, err := New(Config{
		WI:        WIInfo{ID: "wi", CapsuleDir: dir, BundleName: "b"},
		BundleDir: bundle, Workflow: wf, Spawner: &fakeSpawner{},
		Limits: supervise.Defaults{Tool: 30 * time.Minute, StepTimeout: 90 * time.Minute},
	})
	if err != nil {
		t.Fatal(err)
	}
	d.mu.Lock()
	th := d.effectiveThresholdsLocked("slow", supervise.GranularityToken)
	small := d.effectiveThresholdsLocked("small", supervise.GranularityToken)
	plain := d.effectiveThresholdsLocked("plain", supervise.GranularityToken)
	msg := d.effectiveThresholdsLocked("plain", supervise.GranularityMessage)
	d.mu.Unlock()
	// T = max(3 x wall_minutes, global default): wall_minutes is an estimate
	if small.StepTimeout != 90*time.Minute {
		t.Fatalf("a 15m estimate must not shrink T below the 90m global default: %+v", small)
	}
	if th.StepTimeout != 135*time.Minute || th.Silence != 10*time.Minute || th.Tool != 30*time.Minute {
		t.Fatalf("slow step thresholds (only the wall estimate is honored) %+v", th)
	}
	if plain.StepTimeout != 90*time.Minute || plain.Tool != 30*time.Minute || plain.Silence != 10*time.Minute {
		t.Fatalf("plain step must take the global defaults (X 10m token-level): %+v", plain)
	}
	if msg.Silence != 30*time.Minute {
		t.Fatalf("message-level harness defaults X to 30m: %+v", msg)
	}
}

// --- the fence ----------------------------------------------------------

func TestVerdictFenceStaleSpawnIgnoredOneFailurePerDispatch(t *testing.T) {
	env := newSupEnv(t, nil, spec("s"))
	env.sp.script = []fakeEntry{fakeEntry{hold: true}, done(`{}`)}
	go func() {
		waitForSpawns(env, 1)
		// a stale verdict (another dispatch) and a verdict for a
		// different step: both dropped by the fence
		env.link.Verdict(supervise.StepDead{Step: "s", SpawnN: 99, Cause: supervise.CauseModelHung})
		env.link.Verdict(supervise.StepDead{Step: "other", SpawnN: 1, Cause: supervise.CauseModelHung})
		time.Sleep(50 * time.Millisecond)
		// the real verdict, twice: the dispatch is "terminating" after the
		// first, the second is ignored and counted nowhere
		env.link.Verdict(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseHarnessSilent, Evidence: "e"})
		env.link.Verdict(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseToolHung, Evidence: "e2"})
	}()
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	st := env.exec(t).Steps["s"]
	var infra []capsule.Attempt
	for _, a := range st.Attempts {
		if a.Cause == CauseInfraFailure {
			infra = append(infra, a)
		}
	}
	if len(infra) != 1 || infra[0].FailureCause != FailHarnessSilent {
		t.Fatalf("one failure record per dispatch (the first verdict): %+v", infra)
	}
	if st.Supervision.FailureCount != 1 {
		t.Fatalf("counter %d, want 1", st.Supervision.FailureCount)
	}
	if !env.sp.runs[0].terminated() {
		t.Fatal("the verdict must kill the step process")
	}
	if n := len(env.log.byKind("step_dead")); n != 1 {
		t.Fatalf("step_dead consumed %d times, want once", n)
	}
	if n := len(env.log.byKind("verdict_stale")); n < 2 {
		t.Fatalf("stale verdicts must be reported discarded, got %d", n)
	}
}

func TestResultThatRacedAVerdictWins(t *testing.T) {
	// the envelope landed while the verdict was in flight: the dispatch
	// delivered; no failure is recorded
	env := newSupEnv(t, nil, spec("s"))
	env.sp.script = []fakeEntry{done(`{"ok":true}`)}
	env.link.Verdict(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseModelHung})
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	st := env.exec(t).Steps["s"]
	if st.Status != capsule.StepDone || st.Supervision != nil {
		t.Fatalf("a delivered result must stand: %+v", st)
	}
}

func TestDriverStalledVerdictEndsRunForRelaunch(t *testing.T) {
	env := newSupEnv(t, nil, spec("s"))
	env.sp.script = []fakeEntry{done(`{}`)}
	env.link.Verdict(supervise.DriverStalled{Duration: 11 * time.Minute, Phase: "between_steps"})
	err := env.d.Run(context.Background())
	if !errors.Is(err, ErrDriverStalled) {
		t.Fatalf("Run = %v, want ErrDriverStalled (the recovery engine relaunches)", err)
	}
	if n := len(env.sp.dispatched()); n != 0 {
		t.Fatalf("a stalled driver dispatches nothing: %v", env.models())
	}
}

func TestCancellationIsNotAFailureAndStaysUncounted(t *testing.T) {
	env := newChainEnv(t, nil, "s", "a", "b")
	env.sp.script = []fakeEntry{fakeEntry{hold: true}}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- env.d.Run(ctx) }()
	waitUntil(t, "dispatch", func() bool { return len(env.sp.dispatched()) == 1 })
	cancel()
	if err := <-runDone; !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v", err)
	}
	st := env.exec(t).Steps["s"]
	if st.Supervision != nil && st.Supervision.FailureCount != 0 {
		t.Fatalf("a cancellation (shutdown/relaunch) is not a failure: %+v", st.Supervision)
	}
	for _, a := range st.Attempts {
		if a.Cause == CauseInfraFailure {
			t.Fatalf("cancellation archived a failure: %+v", a)
		}
	}
}

// --- facts --------------------------------------------------------------

func TestFactsOutOnTheContract(t *testing.T) {
	env := newChainEnv(t, nil, "grill", "m1")
	env.sp.script = []fakeEntry{asks("q_1", "ok?"), done(`{}`)}
	ctx := context.Background()
	runDone := make(chan error, 1)
	go func() { runDone <- env.d.Run(ctx) }()
	waitUntil(t, "the open question", func() bool {
		x := tryReadExec(env.execPath)
		return x != nil && x.Steps["grill"].PendingQuestion != nil
	})
	// Answer only once the step_waiting_human FACT is out. The parked
	// question's transition (execution.json's PendingQuestion, the
	// needs_input event) lands BEFORE the loop re-plans and announces the
	// wait, so answering on those signals raced the driver goroutine:
	// the test goroutine's step_answered could precede step_waiting_human,
	// or (answered before the re-plan saw the question) suppress it.
	var facts []supervise.Fact
	waitUntil(t, "step_waiting_human", func() bool {
		facts = append(facts, env.link.Facts.Drain()...)
		for _, f := range facts {
			if _, ok := f.(supervise.StepWaitingHuman); ok {
				return true
			}
		}
		return false
	})
	if err := env.d.Answer("q_1", "yes", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := <-runDone; err != nil {
		t.Fatal(err)
	}
	facts = append(facts, env.link.Facts.Drain()...)
	var kinds []string
	var versions []int64
	for _, f := range facts {
		switch v := f.(type) {
		case supervise.StepStarted:
			kinds = append(kinds, fmt.Sprintf("started#%d(%s,%s)", v.SpawnN, v.Step, v.Model))
			if v.Harness != "pi" || v.Thresholds.Silence <= 0 || v.StartedAt.IsZero() {
				t.Fatalf("step_started fields: %+v", v)
			}
		case supervise.StepEnded:
			kinds = append(kinds, fmt.Sprintf("ended#%d(%s)", v.SpawnN, v.Outcome))
		case supervise.StepWaitingHuman:
			kinds = append(kinds, "waiting("+v.QuestionID+")")
		case supervise.StepAnswered:
			kinds = append(kinds, "answered("+v.QuestionID+")")
		case supervise.ExecCommitted:
			versions = append(versions, v.Version)
		}
	}
	want := "[started#1(grill,m1) ended#1(result) waiting(q_1) answered(q_1) started#2(grill,m1) ended#2(result)]"
	if fmt.Sprint(kinds) != want {
		t.Fatalf("facts %v\nwant  %s", kinds, want)
	}
	for i := 1; i < len(versions); i++ {
		if versions[i] <= versions[i-1] {
			t.Fatalf("exec_committed versions must increase: %v", versions)
		}
	}
	if len(versions) == 0 {
		t.Fatal("every committed transition must announce its Version (the push trigger)")
	}
}

// --- event append failures are degradations ---------------------------

type failingLog struct{}

func (failingLog) Append(wi, kind string, body any) error { return errors.New("disk full") }

func TestEventAppendFailureNeverBreaksTheRun(t *testing.T) {
	env := newSupEnv(t, func(c *Config) { c.Log = failingLog{} }, spec("s"))
	env.sp.script = []fakeEntry{exitErr(1, "", false), done(`{}`)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("a failing event log must not break the run: %v", err)
	}
	if env.exec(t).WorkflowStatus != StatusCompleted {
		t.Fatal("execution.json stays the authority")
	}
}

// TestCandidateSwitchKeepsTCumulative: T accumulates across the WHOLE
// step (polyforge#147 D6) — a candidate switch does NOT reopen the
// window. The successor runs with the predecessor's spent time carried
// over; what keeps it alive is the relaxation a timeout-class disposal
// applied, not a rebased clock.
func TestCandidateSwitchKeepsTCumulative(t *testing.T) {
	var mu sync.Mutex
	tick := 0
	env := newChainEnv(t, func(c *Config) {
		c.Now = func() time.Time {
			mu.Lock()
			defer mu.Unlock()
			tick++
			return time.Unix(1_000_000, 0).Add(time.Duration(tick) * 40 * time.Minute)
		}
		c.Limits = supervise.Defaults{StepTimeout: time.Hour}
	}, "s", "a", "b")
	env.sp.script = []fakeEntry{exitErr(1, "model not found", false), done(`{}`)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	var started []supervise.StepStarted
	for _, f := range env.link.Facts.Drain() {
		if s, ok := f.(supervise.StepStarted); ok {
			started = append(started, s)
		}
	}
	if len(started) != 2 {
		t.Fatalf("step_started facts: %d", len(started))
	}
	if prior := started[1].Thresholds.PriorRuntime; prior != 40*time.Minute {
		t.Fatalf("T is cumulative across the switch, prior runtime %v want 40m", prior)
	}
	// accounting keeps every booked millisecond and nothing rebases it
	if st := env.exec(t).Steps["s"]; st.BudgetConsumed.WallMs < (40 * time.Minute).Milliseconds() {
		t.Fatalf("booked wall time: %+v", st.BudgetConsumed)
	}
}

// TestHumanContinueKeepsTCumulative: the human "continue" resets the
// failure ledger but NOT the step's cumulative runtime (D6).
func TestHumanContinueKeepsTCumulative(t *testing.T) {
	env := newSupEnv(t, nil, spec("s"))
	env.sp.script = []fakeEntry{failEntryFor(FailExitOther), failEntryFor(FailExitOther), failEntryFor(FailExitOther)}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := env.d.ResumeCurrent(); err != nil {
		t.Fatalf("continue: %v", err)
	}
	st := env.exec(t).Steps["s"]
	if st.Supervision.FailureCount != 0 || st.Supervision.LongTaskTimeouts != 0 {
		t.Fatalf("a human continue resets the failure ledger: %+v", st.Supervision)
	}
	// Nothing rebases the step's cumulative runtime: the only zeroing
	// of T is a rewind's fresh start (D6).
}

// --- ended_without_result (rule 1, ok-ended turn) ---------------------------

func TestEndedWithoutResultVerdictRetriesSameModelWithFeedback(t *testing.T) {
	env := newChainEnv(t, nil, "s", "a", "b")
	env.sp.script = []fakeEntry{{hold: true}, done(`{"ok":true}`)}
	go func() {
		waitForSpawns(env, 1)
		env.link.Verdict(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseEndedWithoutResult, Evidence: "turn ended ok"})
	}()
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if m := env.models(); len(m) != 2 || m[0] != "a" || m[1] != "a" {
		t.Fatalf("ended_without_result is a same-model retry, got %v", m)
	}
	st := env.exec(t).Steps["s"]
	if st.Supervision == nil || st.Supervision.FailureCount != 1 {
		t.Fatalf("counted failure: %+v", st.Supervision)
	}
	if a := st.Attempts[0]; a.FailureCause != FailEndedWithoutResult {
		t.Fatalf("attempt %+v must carry the cause", a)
	}
	if env.sp.spawnAt(0).PriorEndedWithoutResult || !env.sp.spawnAt(1).PriorEndedWithoutResult {
		t.Fatalf("only the retry tells the agent its previous turn ended without a result: %+v / %+v",
			env.sp.spawnAt(0), env.sp.spawnAt(1))
	}
}

func TestClassifyEndedOKExitIsEndedWithoutResult(t *testing.T) {
	if got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 0, TurnOK: true}); got != FailEndedWithoutResult {
		t.Fatalf("clean exit after an ok-ended turn: %q", got)
	}
	if got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: -1, Signal: "killed", TurnOK: true}); got != FailExitOther {
		t.Fatalf("a kill after an ok turn stays exit_other: %q", got)
	}
	if got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 101, TurnOK: true}); got != FailExitOther {
		t.Fatalf("a crash after an ok turn stays exit_other: %q", got)
	}
	if got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, TurnError: true}); got != FailExitModel {
		t.Fatalf("an error-ended turn stays model-class: %q", got)
	}
}

func TestRetryPromptNamesEndedWithoutResult(t *testing.T) {
	p := promptParts{PriorEndedWithoutResult: true}
	out := assembleStepPrompt(p)
	if !strings.Contains(out, "ended without submitting a result") {
		t.Fatalf("retry prompt must tell the agent: %q", out)
	}
	// the EWR notice names the one delivery channel, never
	// the stale "envelope file is written" shape.
	if !strings.Contains(out, "pf result submit") || strings.Contains(out, "envelope file is written") {
		t.Fatalf("the EWR notice must name `pf result submit` and drop the file wording: %q", out)
	}
}

func TestStartupDeathWithModelStderrAdvancesCandidate(t *testing.T) {
	env := newChainEnv(t, nil, "s", "a", "b")
	env.sp.script = []fakeEntry{
		{spawnErr: errors.New(`runtime: initial prompt injection failed (process torn down): write |1: broken pipe; harness stderr: Error: model "a" not found`)},
		done(`{"ok":true}`),
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if m := env.models(); len(m) != 2 || m[0] != "a" || m[1] != "b" {
		t.Fatalf("models %v, want a then b (a startup model fault advances the candidate)", m)
	}
}

// repo review 2026-10-16 B-12: a pi step that wrote a MALFORMED envelope
// and stayed alive is judged ended_without_result by the supervisor; the
// defect must reach step_dead's evidence and the retry prompt, which must
// not claim the agent wrote nothing.
func TestEndedWithoutResultCarriesTheEnvelopeDefect(t *testing.T) {
	env := newChainEnv(t, nil, "s", "a", "b")
	defect := errors.New(`driver: envelope outcome "done" is not in the closed set`)
	env.sp.script = []fakeEntry{{hold: true, cancelErr: &EnvelopeDefectError{Defect: defect, Cause: context.Canceled}}, done(`{"ok":true}`)}
	go func() {
		waitForSpawns(env, 1)
		env.link.Verdict(supervise.StepDead{Step: "s", SpawnN: 1, Cause: supervise.CauseEndedWithoutResult, Evidence: "turn ended ok"})
	}()
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	dead := env.log.byKind("step_dead")
	if len(dead) != 1 || !strings.Contains(fmt.Sprint(dead[0].body["evidence"]), "closed set") {
		t.Fatalf("step_dead evidence must name the defect: %+v", dead)
	}
	retry := env.sp.spawnAt(1)
	if !strings.Contains(retry.PriorRejection, "closed set") || retry.PriorEndedWithoutResult {
		t.Fatalf("the retry must name the defect, not claim no envelope: %+v", retry)
	}
}

// B-12, the stepRun half: a Wait cut short while the drop path holds an
// unusable envelope reports the defect (and still unwraps to ctx's error).
func TestStepRunWaitCancelSurfacesEnvelopeDefect(t *testing.T) {
	ad, _, capDir := spawnEnv(t)
	writeCapsuleFile(t, capDir, "artifacts/fix-summary.md", "s")
	run, err := ad.SpawnStep(context.Background(), StepSpawn{StepID: "verification", SpawnN: 9})
	if err != nil {
		t.Fatalf("SpawnStep: %v", err)
	}
	writeCapsuleFile(t, capDir, "runtime-state/pending-results/9.json", `{"outcome":"done"}`)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, werr := run.Wait(ctx)
	var ed *EnvelopeDefectError
	if !errors.As(werr, &ed) || !errors.Is(werr, context.Canceled) {
		t.Fatalf("canceled Wait over a defective drop: %v", werr)
	}
}

// ---------------------------------------------------------------------------
// The capsule tree hash on the failure archive.

// The step_failed archive records the capsule tree hash failDispatch
// computed before the transition took the lock.
func TestStepFailureArchivesTreeHash(t *testing.T) {
	old := treeHashFn
	t.Cleanup(func() { treeHashFn = old })
	treeHashFn = func(string) (string, error) { return "hash-from-probe", nil }

	d, sp, execPath := runEnv(t, spec("s"))
	sp.script = []fakeEntry{{err: &ExitedError{Step: "s", ExitCode: 0, TurnOK: true}}, done(`{}`)}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	st := readExec(t, execPath).Steps["s"]
	if len(st.Attempts) == 0 || st.Attempts[0].TreeHash != "hash-from-probe" {
		t.Fatalf("the step_failed archive must carry the computed tree hash: %+v", st.Attempts)
	}
}

// A hash error records none and still lands the failure archive.
func TestStepFailureTreeHashErrorRecordsNone(t *testing.T) {
	old := treeHashFn
	t.Cleanup(func() { treeHashFn = old })
	treeHashFn = func(string) (string, error) { return "", errors.New("hashing unavailable") }

	d, sp, execPath := runEnv(t, spec("s"))
	sp.script = []fakeEntry{{err: &ExitedError{Step: "s", ExitCode: 0, TurnOK: true}}, done(`{}`)}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	st := readExec(t, execPath).Steps["s"]
	if len(st.Attempts) == 0 || st.Attempts[0].TreeHash != "" {
		t.Fatalf("a hash error must record no tree hash and keep today's behavior: %+v", st.Attempts)
	}
}

// The hash reads the filesystem, so it must run with the driver's
// transition lock free (AGENTS.md: no I/O under the job-table lock). The
// probe only succeeds when it can take d.mu itself.
func TestTreeHashRunsOffTheDriverLock(t *testing.T) {
	d, sp, execPath := runEnv(t, spec("s"))
	old := treeHashFn
	t.Cleanup(func() { treeHashFn = old })
	sawLockFree := false
	treeHashFn = func(string) (string, error) {
		if d.mu.TryLock() {
			sawLockFree = true
			d.mu.Unlock()
		}
		return "lock-probe-hash", nil
	}
	sp.script = []fakeEntry{{err: &ExitedError{Step: "s", ExitCode: 0, TurnOK: true}}, done(`{}`)}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !sawLockFree {
		t.Fatalf("the tree hash probe ran while the driver lock was held (I/O under the lock)")
	}
	st := readExec(t, execPath).Steps["s"]
	if len(st.Attempts) == 0 || st.Attempts[0].TreeHash != "lock-probe-hash" {
		t.Fatalf("the archive must carry the probe hash: %+v", st.Attempts)
	}
}
