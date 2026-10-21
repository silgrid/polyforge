package driver

// supervision.go: the driver half of the driver <-> supervisor contract
// (v2-converge 2.2/2.3, package supervise). The driver announces every
// dispatch as facts on the per-WI supervise.Link; consumes the
// supervisor's verdicts behind the spawn-ordinal fence (only the CURRENT
// dispatch's verdict counts — at most one verdict and one failure record
// per dispatch); and classifies every dispatch that ended without a
// result, applying the death-cause table with the per-step failure
// counter in execution.json (the single authority). Nothing here touches
// the network or git.

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// Failure causes recorded on attempts (Attempt.FailureCause): the
// supervisor verdict causes plus the driver's own exit sub-classes.
// Which cause gets which disposal is decideFailureLocked's table.
const (
	FailHarnessSilent        = string(supervise.CauseHarnessSilent)
	FailModelError           = string(supervise.CauseModelError)
	FailEndedWithoutResult   = string(supervise.CauseEndedWithoutResult)
	FailModelHung            = string(supervise.CauseModelHung)
	FailToolHung             = string(supervise.CauseToolHung)
	FailStepTimeoutLongTask  = string(supervise.CauseStepTimeoutLongTask)
	FailStepTimeoutSlowModel = string(supervise.CauseStepTimeoutSlowModel)
	// FailRateLimited: the provider throttled the dispatch (polyforge#147
	// D7) — not model-class: the SAME model is retried after the backoff.
	FailRateLimited = string(supervise.CauseRateLimited)
	// FailExitModel: the process exited without a result and the last
	// signal was an error turn, or its exit text names a model problem
	// (unparseable model, 401/403/auth, quota, insufficient
	// permissions/balance, billing credit).
	FailExitModel = "exit_model_class"
	// FailUpstreamTransient (wi_seWTgHeKtaa8): the upstream's return was
	// corrupted or the transport dropped mid-flight, and the text names no
	// model, allowance or throttle problem. Retried on the SAME model under
	// the D7 backoff, bounded at UpstreamTransientRetryBound.
	FailUpstreamTransient = "upstream_transient"
	// FailExitHost: the driver's own I/O failed (ENOSPC, EACCES, ...) or
	// the harness reported a host error.
	FailExitHost = "exit_host_class"
	// FailExitOther: any other exit without a result (crash, operator
	// kill, envelope never written).
	FailExitOther = "exit_other"
	// FailResultInvalid: the step's result envelope stayed structurally
	// invalid through the in-process retry bound (maxEnvelopeRejects) — a
	// step-content failure that blocks the step.
	FailResultInvalid = "result_invalid"
	// The audit-only interruption causes a driver relaunch or a daemon
	// restart archives on the in-flight step (applyInterruptedLocked): not
	// dispatch outcomes, never counted failures.
	FailDriverRelaunch = "driver_relaunch"
	FailDaemonRestart  = "daemon_restart"
)

// WallEstimateFactor scales a step's declared budget.wall_minutes into its
// total-time bound T = max(3 x wall_minutes, global --step-timeout).
const WallEstimateFactor = 3

// DefaultMaxStepFailures is the per-step failure counter bound (v2-converge
// 2.3): the third counted failure blocks the step.
const DefaultMaxStepFailures = 3

// The D7 rate-limit backoff (polyforge#147): the first throttled
// dispatch is retried after RateLimitBackoffBase, each consecutive one
// after twice the previous wait, never longer than RateLimitBackoffMax.
// There is no retry-count bound: the step's cumulative runtime T (which
// the waits count into) is the only limit.
const (
	RateLimitBackoffBase = 1 * time.Second
	RateLimitBackoffMax  = 300 * time.Second
)

// UpstreamTransientRetryBound is the upstream-transient class's
// same-model retry bound: a third consecutive corrupted return is
// re-classified as a model-class death (applyStepFailedLocked) and
// switches candidates.
const UpstreamTransientRetryBound = 2

// rateLimitBackoff answers the wait before the (streak+1)-th throttled
// dispatch of a step: the base doubled once per already-recorded
// consecutive death of the class, capped.
func rateLimitBackoff(streak int) time.Duration {
	d := RateLimitBackoffBase
	for i := 0; i < streak && d < RateLimitBackoffMax; i++ {
		d *= 2
	}
	return min(d, RateLimitBackoffMax)
}

// trailingCause counts the step's trailing consecutive attempts of one
// failure cause. The archived attempts are the ledger (a crash-recovered
// driver derives the same streak as its predecessor): any other outcome
// ends the streak, while the audit-only interruption entries are skipped —
// a restart resets neither the D7 backoff nor the upstream-transient bound.
func trailingCause(attempts []capsule.Attempt, cause string) int {
	n := 0
	for i := len(attempts) - 1; i >= 0; i-- {
		a := attempts[i]
		if a.Cause == CauseInfraFailure && (a.FailureCause == FailDriverRelaunch || a.FailureCause == FailDaemonRestart) {
			continue
		}
		if a.FailureCause != cause {
			break
		}
		n++
	}
	return n
}

// rateLimitStreak counts the step's trailing consecutive rate-limit deaths.
func rateLimitStreak(attempts []capsule.Attempt) int {
	return trailingCause(attempts, FailRateLimited)
}

// upstreamTransientStreak counts the step's trailing consecutive
// upstream-transient deaths: the retry bound's ledger.
func upstreamTransientStreak(attempts []capsule.Attempt) int {
	return trailingCause(attempts, FailUpstreamTransient)
}

// ErrDriverStalled is Run's error when the supervisor's driver_stalled
// verdict reached the loop: the recovery engine relaunches the driver
// (counted against the driver-crash bound).
var ErrDriverStalled = errors.New("driver: stalled between steps (supervisor verdict); relaunching")

// RunMeta is the optional description of a spawned step run; production
// runs (stepRun) implement MetaRun, scripted test runs need not.
type RunMeta struct {
	PID            int
	StartedAt      time.Time
	TranscriptPath string
	Harness        string
	HarnessSession string // the harness-native session id (claude's uuid; "" for pi)
	Granularity    supervise.Granularity
	Resumed        bool
}

// MetaRun is implemented by StepRuns that can describe themselves.
type MetaRun interface {
	Meta() RunMeta
}

// ExitedError is Wait's error when the step process exited without a
// usable result envelope: the raw material of the driver's own
// exited_without_result classification.
type ExitedError struct {
	Step      string
	SpawnN    int64
	ExitCode  int
	Signal    string
	LastError string // stderr tail / last reported error
	TurnError bool   // the last signal was "turn ended with an error"
	TurnOK    bool   // the last signal was a normally ended turn (no error)
	Detail    string // envelope defect, when one was left behind
}

// EnvelopeDefectError is a dispatch wait cut short (a supervisor verdict,
// shutdown) while the drop path held an UNUSABLE envelope: the step did
// write a result, and the defect must reach the failure record and the
// retry prompt instead of reading as "no envelope". It unwraps to the
// cancellation cause.
type EnvelopeDefectError struct {
	Defect error
	Cause  error
}

func (e *EnvelopeDefectError) Error() string {
	return fmt.Sprintf("%v (unusable result envelope: %v)", e.Cause, e.Defect)
}

func (e *EnvelopeDefectError) Unwrap() error { return e.Cause }

func (e *ExitedError) Error() string {
	msg := fmt.Sprintf("driver: step %s spawn %d exited without a result (code %d", e.Step, e.SpawnN, e.ExitCode)
	if e.Signal != "" {
		msg += ", " + e.Signal
	}
	msg += ")"
	for _, s := range []string{e.Detail, e.LastError} {
		if s != "" {
			msg += ": " + s
		}
	}
	return msg
}

// StepFailure is one failed dispatch handed to the TransStepFailed
// transition.
type StepFailure struct {
	Cause    string
	Detail   string
	ExitCode int
	WallMs   int64         // the dispatch's wall time, added to the step's T
	Backoff  time.Duration // filled by the decision (D7): the wait owed before the next dispatch, booked into T
}

// ---------------------------------------------------------------------------
// Exit classification (the driver's own judgement, 2.3 table).

var hostPatterns = regexp.MustCompile(`(?i)(no space left|enospc|disk quota|edquot|permission denied|eacces|read-only file system|erofs|too many open files|emfile|cannot allocate memory|out of memory|enomem|input/output error|executable file not found|no such file or directory.*(pi|harness)|权限不够|权限被拒绝|拒绝访问)`)

// textClasses is the ordered failure-text classification: the first
// matching row names the cause. Chinese provider texts are matched
// literally ((?i) and \b mean nothing to CJK): each alternative is a
// multi-character phrase (never a bare 额/限), a gap is bounded to the
// same clause, and a permission phrase counts only next to its subject
// (模型/接口/API/服务) — a bare 权限不足 is as likely a local file error.
var textClasses = []struct {
	re    *regexp.Regexp
	cause string
	// exitOnly: matched only in an exited process's text, never in the
	// driver's own failure ("read transcript: unexpected EOF" is a local
	// I/O error, not a provider stream drop).
	exitOnly bool
}{
	// allowance/auth (model-class, D7): the account or key cannot use this
	// candidate — a different one may work, so these switch IMMEDIATELY,
	// even when the text also carries a throttle or transport signature
	// ("429 ... exceeded your current quota", "insufficient balance ...
	// connection reset").
	{regexp.MustCompile(`(?i)(\b401\b|\b403\b|unauthori[sz]ed|authentication|invalid api key|incorrect api key|api key|quota|insufficient|billing|credit|` +
		`余额不足|欠费|(额度|配额)[^，。；,.;\n]{0,6}(用尽|用完|耗尽|不足|超出|超限)|(超出|超过)了?(额度|配额)|` +
		`账户[^，。；,.;\n]{0,10}(停用|禁用|冻结)|鉴权失败|认证失败|无效的?(密钥|令牌)|(密钥|令牌)无效|` +
		`(无权限|没有权限|权限不足)[^，。；,.;\n]{0,6}(模型|接口|API|服务)|(模型|接口|API|服务)[^，。；,.;\n]{0,6}(无权限|没有权限|权限不足))`),
		FailExitModel, false},
	// rate limit (D7): ONLY transient throttling. Another model would be
	// throttled just the same, so these never advance the candidate
	// pointer. Allowance problems are the row above.
	{regexp.MustCompile(`(?i)(\b429\b|rate.?limit|too many requests|overloaded|` +
		`并发[^，。；,.;\n]{0,6}(过高|过大|过多|超限|超出|超过)|请求(过于|太)?频繁|请求频率|请求过多|并发超额|限流|速率限制|繁忙|负载过高)`),
		FailRateLimited, false},
	// upstream transient: ONLY a corrupted upstream return or a drop of the
	// upstream stream — malformed/incomplete/truncated tool-call arguments
	// or JSON, an empty (0-token) reply, a reset/refused/closed/EOF upstream
	// connection or an upstream stream that ended before completion
	// (polyforge#202), or a gateway reply naming an upstream error AND
	// inviting a retry ("400: {"message":"The model request failed due to
	// an upstream error. Please retry.",...}", polyforge#227 — keyed on the
	// conjunction, never on the 4xx status or invalid_request_error). The
	// stream-drop verbs need an upstream/provider/stream/connection
	// subject: a bare "the model ended the turn" stays model-class. A bare
	// "invalid ..." (tool name, request, argument), a plain 4xx, an
	// upstream error without a retry invitation (or the reverse), a
	// context-length error or a local I/O error is NOT here.
	{regexp.MustCompile(`(?i)(` +
		`tool\W{0,3}calls?\W.{0,60}\b(incomplete|truncated|malformed|unterminated)\b|` +
		`\b(incomplete|truncated|malformed|unterminated)\W{1,3}(json|arguments|tool\W{0,3}calls?)\b|` +
		`\bempty\W(response|reply|completion)\b|\b(0|zero)\W?(output\W)?tokens?\b|` +
		`\b(upstream|provider|stream|connection)\W.{0,30}\b(reset|refused|dropped|closed|aborted|ended|eof)\b|` +
		`\bupstream\W.{0,40}\bplease\W+retry\b)`),
		FailUpstreamTransient, true},
	// model name (model-class, F4): a pinned model that cannot resolve must
	// advance the candidate chain. After the throttle and transient rows:
	// its loose "model ... invalid" shape would otherwise swallow "the
	// model returned an invalid tool call (incomplete JSON arguments)".
	{regexp.MustCompile(`(?i)(model\W.{0,60}(not found|unknown|unresolved|cannot be resolved|could not be resolved|not resolve|invalid|unavailable|not available|unsupported|does not exist)|(unknown|invalid|unparse?able|unresolved|unsupported|no such) model|no models? (found|available|match))`),
		FailExitModel, false},
}

// classifyText answers the first textClasses cause matching text ("" for
// none); exited selects the exit-only rows too.
func classifyText(text string, exited bool) string {
	for _, c := range textClasses {
		if (exited || !c.exitOnly) && c.re.MatchString(text) {
			return c.cause
		}
	}
	return ""
}

// classifyWaitFailure maps a Wait error (not a cancellation) onto a
// failure cause.
func classifyWaitFailure(err error) (cause string, exitCode int, detail string) {
	var ee *ExitedError
	if errors.As(err, &ee) {
		text := strings.TrimSpace(ee.LastError + " " + ee.Detail)
		switch {
		case hostPatterns.MatchString(text):
			cause = FailExitHost
		case ee.TurnOK && ee.ExitCode == 0 && ee.Signal == "":
			// the turn ended normally and the process then exited CLEANLY
			// with no result: the agent stopped without delivering (a kill
			// or crash after an ok turn is still exit_other). Evaluated
			// before any text match: leftover stderr must never turn a
			// clean stop into a model fault that burns a candidate.
			cause = FailEndedWithoutResult
		default:
			cause = classifyText(text, true)
		}
		switch {
		case cause != "":
		case ee.TurnError:
			cause = FailExitModel
		default:
			cause = FailExitOther
		}
		return cause, ee.ExitCode, ee.Error()
	}
	// The driver's own failure (staging an envelope, reading the drop,
	// a harness that died at startup — its error carries the stderr, and a
	// model-class text there advances the chain, F4).
	if isHostError(err) {
		return FailExitHost, -1, err.Error()
	}
	if cause = classifyText(err.Error(), false); cause == "" {
		cause = FailExitOther
	}
	return cause, -1, err.Error()
}

// verdictCause answers the failure cause a supervisor verdict records. A
// model_error verdict is an error-ended turn of a harness that stayed
// ALIVE (pi --mode rpc idles after a provider error): its text gets the
// exit path's classification (polyforge#162), so a throttle or an
// upstream transient is a backoff retry here too; any other text keeps
// model_error (model-class, like the exit path's error-turn fallback).
func verdictCause(v supervise.StepDead) string {
	if v.Cause != supervise.CauseModelError || strings.TrimSpace(v.TurnError) == "" {
		return string(v.Cause)
	}
	cause, _, _ := classifyWaitFailure(&ExitedError{Step: v.Step, SpawnN: v.SpawnN, ExitCode: -1, LastError: v.TurnError, TurnError: true})
	switch cause {
	case FailRateLimited, FailUpstreamTransient:
		return cause
	}
	return string(v.Cause)
}

// isHostError reports whether err is the driver's own host fault.
func isHostError(err error) bool {
	if err == nil {
		return false
	}
	for _, errno := range []syscall.Errno{syscall.ENOSPC, syscall.EACCES, syscall.EROFS, syscall.EDQUOT, syscall.EMFILE, syscall.ENOMEM, syscall.EIO} {
		if errors.Is(err, errno) {
			return true
		}
	}
	if errors.Is(err, fs.ErrPermission) {
		return true
	}
	return hostPatterns.MatchString(err.Error())
}

// ---------------------------------------------------------------------------
// Thresholds.

// stepBases answers the tool and total-time bases of one step: the
// global defaults, the wall base raised around the step's budget
// estimate (an ESTIMATE, not a hard limit: never below the default).
func (d *Driver) stepBases(stepID string) (tool, wall time.Duration) {
	def := d.cfg.Limits.Fill()
	tool, wall = def.Tool, def.StepTimeout
	if spec := d.workflow.step(stepID); spec != nil && spec.WallMinutes > 0 {
		wall = max(wall, time.Duration(spec.WallMinutes)*time.Minute*WallEstimateFactor)
	}
	return tool, wall
}

// effectiveThresholdsLocked answers the thresholds a dispatch of stepID
// runs under: the bases raised to any relaxed value persisted in
// execution.json, plus the step runtime already spent. T is CUMULATIVE
// over the whole step (polyforge#147 D6): a candidate switch or a human
// continue does not reopen the window — only a rewind's fresh start
// zeroes it.
func (d *Driver) effectiveThresholdsLocked(stepID string, gran supervise.Granularity) supervise.Thresholds {
	tool, wall := d.stepBases(stepID)
	th := supervise.Thresholds{Silence: d.cfg.Limits.Fill().SilenceFor(gran), Tool: tool, StepTimeout: wall}
	if st := d.exec.Steps[stepID]; st != nil {
		if sup := st.Supervision; sup != nil {
			th.Tool = max(th.Tool, time.Duration(sup.RelaxedToolMs)*time.Millisecond)
			th.StepTimeout = max(th.StepTimeout, time.Duration(sup.RelaxedWallMs)*time.Millisecond)
		}
		th.PriorRuntime = time.Duration(max(st.BudgetConsumed.WallMs, 0)) * time.Millisecond
	}
	return th
}

func (d *Driver) maxStepFailures() int {
	if d.cfg.MaxStepFailures > 0 {
		return d.cfg.MaxStepFailures
	}
	return DefaultMaxStepFailures
}

// ---------------------------------------------------------------------------
// Facts out.

func (d *Driver) fact(f supervise.Fact) {
	if d.cfg.Link != nil {
		d.cfg.Link.Fact(f)
	}
}

// announceStart sends step_started for a freshly spawned dispatch.
func (d *Driver) announceStart(sp *StepSpawn, run StepRun, startedAt time.Time) {
	if d.cfg.Link == nil {
		return
	}
	meta := RunMeta{Harness: harness.Of(sp.Harness), Granularity: supervise.GranularityToken, StartedAt: startedAt, Resumed: sp.Resume}
	if mr, ok := run.(MetaRun); ok {
		m := mr.Meta()
		if m.Harness != "" {
			meta.Harness = m.Harness
		}
		if m.Granularity != "" {
			meta.Granularity = m.Granularity
		}
		if !m.StartedAt.IsZero() {
			meta.StartedAt = m.StartedAt
		}
		meta.PID, meta.TranscriptPath, meta.Resumed = m.PID, m.TranscriptPath, m.Resumed
		meta.HarnessSession = m.HarnessSession
	}
	d.mu.Lock()
	th := d.effectiveThresholdsLocked(sp.StepID, meta.Granularity)
	d.mu.Unlock()
	d.cfg.Link.Fact(supervise.StepStarted{
		Step: sp.StepID, SpawnN: sp.SpawnN, Model: sp.Model, Effort: sp.Effort, Harness: meta.Harness,
		PID: meta.PID, TranscriptPath: meta.TranscriptPath, StartedAt: meta.StartedAt,
		Thresholds: th, Resumed: meta.Resumed, HarnessSession: meta.HarnessSession,
	})
}

// announceEnd sends step_ended.
func (d *Driver) announceEnd(stepID string, n int64, outcome supervise.Outcome, exitCode int, lastError string) {
	d.fact(supervise.StepEnded{Step: stepID, SpawnN: n, Outcome: outcome, ExitCode: exitCode, LastError: lastError})
}

// ---------------------------------------------------------------------------
// Dispatch wait: result | exit | verdict, first one wins.

type waitResult struct {
	res *StepResult
	err error
}

// awaitDispatch waits for the dispatch's outcome while consuming the
// supervisor's verdicts behind the fence. It returns the result, the
// wait error, and — only when a verdict ended the dispatch — the
// verdict. A result that raced a verdict wins (the dispatch delivered).
func (d *Driver) awaitDispatch(ctx context.Context, run StepRun, stepID string, n int64) (*StepResult, error, *supervise.StepDead) {
	wctx, wcancel := context.WithCancel(ctx)
	defer wcancel()
	ch := make(chan waitResult, 1)
	go func() {
		res, err := run.Wait(wctx)
		ch <- waitResult{res, err}
	}()
	var notify <-chan struct{}
	if d.cfg.Link != nil {
		notify = d.cfg.Link.Verdicts.Notify()
	}
	for {
		select {
		case r := <-ch:
			return r.res, r.err, nil
		case <-notify:
			for _, v := range d.cfg.Link.Verdicts.Drain() {
				dead, ok := v.(supervise.StepDead)
				if !ok {
					continue // a driver_stalled verdict for a driver that is demonstrably running: stale
				}
				if dead.SpawnN != n || dead.Step != stepID {
					d.emit("verdict_stale", map[string]any{"step": dead.Step, "spawn_n": dead.SpawnN, "current_spawn_n": n, "cause": dead.Cause})
					continue // the fence: only the current dispatch's verdict counts
				}
				// Terminating: kill the process, then ignore its exit and any
				// further verdict of this dispatch.
				wcancel()
				_ = run.Terminate(teardownBound)
				r := <-ch
				if r.res != nil && r.err == nil {
					return r.res, nil, nil // the envelope landed first: the dispatch delivered
				}
				var ed *EnvelopeDefectError
				if errors.As(r.err, &ed) {
					// the step wrote an envelope the light checks refused: name
					// it in the failure record, not just "no result"
					dead.Evidence = strings.TrimSpace(dead.Evidence + "; unusable result envelope: " + ed.Defect.Error())
				}
				return nil, r.err, &dead
			}
		}
	}
}

// takeStalled drains the verdict queue between dispatches: a
// driver_stalled verdict that reaches the loop ends it (the recovery
// engine relaunches); stale step verdicts are discarded.
func (d *Driver) takeStalled() *supervise.DriverStalled {
	if d.cfg.Link == nil {
		return nil
	}
	var stalled *supervise.DriverStalled
	for _, v := range d.cfg.Link.Verdicts.Drain() {
		switch vv := v.(type) {
		case supervise.DriverStalled:
			c := vv
			stalled = &c
		case supervise.StepDead:
			d.emit("verdict_stale", map[string]any{"step": vv.Step, "spawn_n": vv.SpawnN, "cause": vv.Cause, "note": "no dispatch in flight"})
		}
	}
	return stalled
}

// ---------------------------------------------------------------------------
// Failure decision (applied atomically inside the TransStepFailed write).

// failureDecision is the outcome of the death-cause table for one failed
// dispatch.
type failureDecision struct {
	Cause       string
	Switch      bool
	Exhausted   bool // switch wanted, candidate chain spent
	NoChain     bool // switch wanted, no candidate chain at all
	From, To    string
	Block       bool
	BlockReason string
	Report      bool
	Count       int
	RelaxToolMs int64
	RelaxWallMs int64
	Fresh       bool
	// Backoff is the wait the driver owes before the next dispatch of
	// this step (D7: a throttle-like disposal only ever backs off).
	Backoff time.Duration
}

func (dec failureDecision) disposition() string {
	switch {
	case dec.Block:
		return "blocked"
	case dec.Switch:
		return "switch_model"
	default:
		return "retry_same_model"
	}
}

// decideFailureLocked applies the 2.3 death-cause table to the step's
// current state:
//
//   - result_invalid, host class: block (reported to the owner);
//   - rate limit, upstream transient (in bound): the SAME model after the
//     D7 backoff, the transcript resumed, the failure counter untouched —
//     a throttle is not the step failing (and the chain must not be
//     spent by someone else's quota);
//   - model class: switch to the next candidate (a fresh ledger), or —
//     chain spent or absent — a counted same-model retry;
//   - everything else: a counted same-model retry. The counter bound
//     blocks; the 1st retry resumes the transcript, the 2nd+ starts fresh.
//
// Timeout-class deaths additionally relax their bound x2 and persist it
// (D6): the next dispatch — another candidate or a human continue
// included — runs under the doubled bound with the cumulative runtime
// carried over, never judged timed out at its own startup. A second
// long-task timeout blocks (after doubling T once more, so a human
// continue gets a fresh doubled window).
func (d *Driver) decideFailureLocked(stepID string, st *capsule.Step, f *StepFailure) failureDecision {
	dec := failureDecision{Cause: f.Cause}
	var sup capsule.StepSupervision
	if st.Supervision != nil {
		sup = *st.Supervision
	}
	throttled := false
	switch {
	case f.Cause == FailResultInvalid:
		dec.Block, dec.Report = true, true
		dec.BlockReason = fmt.Sprintf("step %s: result_invalid: the result envelope stayed invalid through the in-process retry bound (%d): %s",
			stepID, maxEnvelopeRejects, f.Detail)
	case f.Cause == FailExitHost:
		dec.Block, dec.Report = true, true
		dec.BlockReason = fmt.Sprintf("host-class fault (%s): the machine, not the model, is broken: %s", f.Cause, f.Detail)
	case f.Cause == FailRateLimited:
		throttled = true
		dec.Backoff = rateLimitBackoff(rateLimitStreak(st.Attempts))
	case f.Cause == FailUpstreamTransient:
		// past its bound applyStepFailedLocked re-classified the record
		// as model-class, so only in-bound retries reach this branch
		throttled = true
		dec.Backoff = rateLimitBackoff(upstreamTransientStreak(st.Attempts))
	case IsModelClass(f.Cause):
		cands := d.effectiveCandidates(stepID)
		ptr := CandidatePointer(st.Attempts, cands)
		switch {
		case len(cands) == 0:
			dec.NoChain = true
		case ptr < len(cands)-1:
			dec.Switch = true
			dec.From, dec.To = cands[ptr], cands[ptr+1]
		default:
			// the pointer is already at the end (CandidatePointer caps
			// there): still model-class on the record, but a same-model retry
			dec.Exhausted = true
		}
	}
	tool, wall := d.stepBases(stepID)
	switch f.Cause {
	case FailToolHung:
		dec.RelaxToolMs = doubledMs(tool, sup.RelaxedToolMs)
	case FailStepTimeoutSlowModel:
		dec.RelaxWallMs = doubledMs(wall, sup.RelaxedWallMs)
	case FailStepTimeoutLongTask:
		dec.RelaxWallMs = doubledMs(wall, sup.RelaxedWallMs)
		if sup.LongTaskTimeouts >= 1 && !dec.Block {
			dec.Block = true
			dec.BlockReason = "second step timeout (step_timeout_long_task) after T was already relaxed: " + f.Detail
		}
	}
	switch {
	case dec.Switch:
		dec.Count, dec.Fresh = 0, true
	case throttled:
		dec.Count, dec.Fresh = sup.FailureCount, false
	default:
		dec.Count = sup.FailureCount + 1
		if dec.Count >= d.maxStepFailures() && !dec.Block {
			dec.Block = true
			dec.BlockReason = fmt.Sprintf("step failed %d times without a result (failure counter bound %d); last cause %s: %s",
				dec.Count, d.maxStepFailures(), f.Cause, f.Detail)
		}
		dec.Fresh = dec.Count >= 2
	}
	return dec
}

// doubledMs answers a relaxed bound after one more relaxation: the
// current bound (the persisted relaxed value, or the base when it was
// never relaxed) doubled, in milliseconds.
func doubledMs(base time.Duration, relaxedMs int64) int64 {
	return (2 * max(base, time.Duration(relaxedMs)*time.Millisecond)).Milliseconds()
}

// applyStepFailedLocked records one failed dispatch: the attempt (with
// its cause sub-class), the failure counter, the relaxed thresholds, the
// next dispatch's shape, the dispatch's wall time plus the backoff it
// owes (both count into the step's cumulative T) and — when the decision
// blocks — the blocked workflow status.
func (d *Driver) applyStepFailedLocked(req TransitionRequest, info *appliedInfo) error {
	f := req.Failure
	if f == nil {
		return fmt.Errorf("driver: step_failed without a failure record")
	}
	if err := d.requireRunningStep(req.StepID); err != nil {
		return err
	}
	st := d.exec.Steps[req.StepID]
	// An upstream transient past its retry bound is archived as a
	// model-class death: the archive is the bound's ledger, so a
	// crash-recovered driver reads the same remaining retries.
	if f.Cause == FailUpstreamTransient && upstreamTransientStreak(st.Attempts) >= UpstreamTransientRetryBound {
		f.Cause = FailExitModel
	}
	dec := d.decideFailureLocked(req.StepID, st, f)
	st.Dispatch = 0
	st.Attempts = append(st.Attempts, capsule.Attempt{
		Cause:         CauseInfraFailure,
		FailureCause:  f.Cause,
		AttemptSeq:    nextAttemptSeq(st),
		StartedAt:     st.StepStartedAt,
		InterruptedAt: d.stamp(),
		Detail:        f.Detail,
	})
	if st.Supervision == nil {
		st.Supervision = &capsule.StepSupervision{}
	}
	sup := st.Supervision
	sup.FailureCount = dec.Count
	if f.Cause == FailStepTimeoutLongTask {
		sup.LongTaskTimeouts++
	}
	sup.RelaxedToolMs = max(sup.RelaxedToolMs, dec.RelaxToolMs)
	sup.RelaxedWallMs = max(sup.RelaxedWallMs, dec.RelaxWallMs)
	sup.NextDispatch = ""
	if dec.Fresh {
		sup.NextDispatch = capsule.NextDispatchFresh
	}
	if wall := f.WallMs + dec.Backoff.Milliseconds(); wall > 0 {
		d.addWallLocked(st, wall)
	}
	f.Backoff = dec.Backoff
	if dec.Block {
		d.exec.WorkflowStatus = StatusBlocked
		d.exec.BlockedReason = dec.BlockReason
	}
	info.failure = &dec
	return nil
}

// ---------------------------------------------------------------------------
// Failure handling + wall accounting.

// emit appends one driver event (best-effort news: a failing log is a
// stderr line, never a correctness dependency — execution.json stays the
// authority).
func (d *Driver) emit(kind string, body map[string]any) {
	if d.log == nil {
		return
	}
	if err := d.log.Append(d.wi.ID, kind, body); err != nil {
		fmt.Fprintf(os.Stderr, "driver: %s: event %s append failed (degraded, state unaffected): %v\n", d.wi.ID, kind, err)
	}
}

// addUnbooked adds one finished dispatch's wall time to the step's
// not-yet-persisted runtime.
func (d *Driver) addUnbooked(stepID string, since time.Time) {
	ms := max(d.now().Sub(since).Milliseconds(), 0)
	d.mu.Lock()
	d.unbooked[stepID] += ms
	d.mu.Unlock()
}

// takeUnbooked returns and clears the step's pending wall time.
func (d *Driver) takeUnbooked(stepID string) int64 {
	d.mu.Lock()
	defer d.mu.Unlock()
	ms := d.unbooked[stepID]
	delete(d.unbooked, stepID)
	return ms
}

// failDispatch records one failed dispatch (the dispatch's single failure
// record): the step_ended fact, then the TransStepFailed write that
// applies the death-cause table, then the D7 backoff the decision imposed
// (already booked into T). stop reports that Run must return err: the
// write failed, or the decision parked the workflow (err nil — Run exits
// cleanly, the owner holds the next move).
func (d *Driver) failDispatch(ctx context.Context, stepID string, n int64, since time.Time, cause string, exitCode int, detail string) (stop bool, err error) {
	d.addUnbooked(stepID, since)
	f := &StepFailure{Cause: cause, Detail: detail, ExitCode: exitCode, WallMs: d.takeUnbooked(stepID)}
	last := detail[:min(len(detail), 500)]
	d.announceEnd(stepID, n, supervise.OutcomeExitedWithoutResult, exitCode, last)
	if err := d.transition(TransitionRequest{Kind: TransStepFailed, StepID: stepID, SpawnN: n, Failure: f}); err != nil {
		return true, fmt.Errorf("driver: step %s dispatch %d failure record failed: %w", stepID, n, err)
	}
	d.mu.Lock()
	stop = d.exec.WorkflowStatus == StatusBlocked
	d.mu.Unlock()
	if !stop {
		d.sleep(ctx, f.Backoff)
	}
	return stop, nil
}

// sleep waits for d (tests inject Config.Sleep), cut short by ctx.
func (d *Driver) sleep(ctx context.Context, wait time.Duration) {
	if wait <= 0 {
		return
	}
	if d.cfg.Sleep != nil {
		d.cfg.Sleep(ctx, wait)
		return
	}
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}

// announceWaiting sends step_waiting_human for the current step's open
// question.
func (d *Driver) announceWaiting() {
	d.mu.Lock()
	step, n := d.exec.CurrentStep, d.spawnN
	qid, question := "", ""
	if st := d.exec.Steps[step]; st != nil && st.PendingQuestion != nil {
		qid, question = st.PendingQuestion.ID, st.PendingQuestion.Question
	}
	announced := d.waitingAnnounced == qid
	d.waitingAnnounced = qid
	// a driver that finds a question already parked (a restart) tells the
	// log once per life, like a fresh park does at its transition
	reannounce := d.inputEventFor != qid
	d.inputEventFor = qid
	d.mu.Unlock()
	if reannounce {
		d.emit("needs_input", map[string]any{"step": step, "question_id": qid, "question": question, "resumed": true})
	}
	if announced || d.cfg.Link == nil {
		return
	}
	d.cfg.Link.Fact(supervise.StepWaitingHuman{Step: step, SpawnN: n, QuestionID: qid})
}
