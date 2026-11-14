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
	"github.com/silgrid/polyforge/internal/common/spawn"
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
	// the transient backoff, never counted.
	FailUpstreamTransient = "upstream_transient"
	// FailExitHost: the driver's own I/O failed (ENOSPC, EACCES, ...) or
	// the harness reported a host error.
	FailExitHost = "exit_host_class"
	// FailExitOther: any other exit without a result (crash, operator
	// kill, envelope never written).
	FailExitOther = "exit_other"
	// The audit-only interruption causes a driver relaunch or a daemon
	// restart archives on the in-flight step (applyInterruptedLocked): not
	// dispatch outcomes, never counted failures.
	FailDriverRelaunch = "driver_relaunch"
	FailDaemonRestart  = "daemon_restart"
)

// WallEstimateFactor scales a step's declared budget.wall_minutes into its
// total-time bound T = max(3 x wall_minutes, global --step-timeout).
const WallEstimateFactor = 3

// The process supervision rules (workflow contract 4, design #483 §7,
// from the C0 death statistics): every dispatch that ended without a
// hand-off is one of five classes, and each class has one disposal.
//
//   - ended without a hand-off (ended_without_result: the turn ended and
//     the process exited cleanly without `pf step handoff`): the same
//     session resumes with the reminder; MaxEwrStreak in a row block;
//   - model class (model_error, exit_model_class, model_hung,
//     step_timeout_slow_model — an error, a model that does not resolve,
//     too slow, hung): the next candidate; none left blocks (the agent
//     cannot start: NoUsableModelBlockPrefix);
//   - transient (rate_limited, upstream_transient): the same model after
//     the transient backoff (TransientBackoffBase doubling to
//     TransientBackoffMax), never counted;
//   - timeout (step_timeout_long_task, tool_hung: the step's total time or
//     one tool call): the bound doubled and the same session resumed once;
//     the second timeout since the last reset blocks;
//   - any other exit (exit_other, exit_host_class, harness_silent, ...):
//     the same session resumed once; MaxOtherStreak in a row block.
//
// The streaks are read from the archived attempts (the ledger) within the
// streak era (streakEra: the owner's last continue opens a new one), so a
// crash-recovered driver decides exactly as its predecessor would have.
const (
	// MaxEwrStreak: the third consecutive dispatch that ended without a
	// hand-off blocks the step.
	MaxEwrStreak = 3
	// MaxOtherStreak: the second consecutive other-class exit blocks.
	MaxOtherStreak = 2
	// MaxTimeouts: the second timeout since the last reset blocks.
	MaxTimeouts = 2
)

// DefaultMaxStepFailures is the bound of consecutive dispatches that
// ended without a hand-off (MaxEwrStreak): Config.MaxStepFailures
// overrides it (0 = this default).
const DefaultMaxStepFailures = MaxEwrStreak

// NoUsableModelBlockPrefix opens the blocked reason of a step whose
// model-class death found no candidate left (or no candidate list): the
// agent cannot start on any model in effect. A message cannot wake such a
// park (WakeOf): the owner fixes the model list, then `pf resume`.
const NoUsableModelBlockPrefix = "no usable model: "

// The transient backoff (design #483 §7): the first throttled or
// corrupted-upstream dispatch is retried after TransientBackoffBase, each
// consecutive one (either cause) after twice the previous wait, never
// longer than TransientBackoffMax. There is no retry-count bound: the
// step's cumulative runtime T (which the waits count into) is the only
// limit.
const (
	TransientBackoffBase = 1 * time.Second
	TransientBackoffMax  = 3 * time.Minute
)

// The D7 names of the transient backoff (polyforge#147), kept for the
// callers that read them.
const (
	RateLimitBackoffBase = TransientBackoffBase
	RateLimitBackoffMax  = TransientBackoffMax
)

// transientCause reports the transient class: the same model after the
// backoff, never counted.
func transientCause(cause string) bool {
	return cause == FailRateLimited || cause == FailUpstreamTransient
}

// timeoutCause reports the timeout class: the bound doubled, one resume.
func timeoutCause(cause string) bool {
	return cause == FailStepTimeoutLongTask || cause == FailToolHung
}

// auditOnly reports the interruption entries a relaunch or a restart
// archives: no dispatch outcome, skipped by every streak.
func auditOnly(a capsule.Attempt) bool {
	return a.Cause == CauseInfraFailure && (a.FailureCause == FailDriverRelaunch || a.FailureCause == FailDaemonRestart)
}

// otherCause reports the other-exit class: every counted cause that is
// none of the named classes.
func otherCause(cause string) bool {
	return cause != FailEndedWithoutResult &&
		!IsModelClass(cause) && !transientCause(cause) && !timeoutCause(cause)
}

// classStreak counts the step's trailing consecutive deaths whose cause
// in(cause) reports, within one streak era (attempts started before era
// end it): the audit-only interruption entries and — unless in counts
// them — the transient deaths are skipped; any other outcome ends it.
func classStreak(attempts []capsule.Attempt, era time.Time, in func(string) bool) int {
	n := 0
	for i := len(attempts) - 1; i >= 0; i-- {
		a := attempts[i]
		if auditOnly(a) {
			continue
		}
		if a.Cause != CauseInfraFailure || beforeEra(a, era) {
			break
		}
		if in(a.FailureCause) {
			n++
			continue
		}
		if transientCause(a.FailureCause) {
			continue
		}
		break
	}
	return n
}

// transientBackoff answers the wait before the (streak+1)-th consecutive
// transient re-dispatch of a step.
func transientBackoff(streak int) time.Duration {
	d := TransientBackoffBase
	for i := 0; i < streak && d < TransientBackoffMax; i++ {
		d *= 2
	}
	return min(d, TransientBackoffMax)
}

// transientStreak counts the step's trailing consecutive transient deaths
// (either cause; the audit-only entries skipped): the backoff ladder's
// position. It reads no era: the owner's continue clears an owed wait
// (RetryOwed), not the ladder of a throttle that persists.
func transientStreak(attempts []capsule.Attempt) int {
	n := 0
	for i := len(attempts) - 1; i >= 0; i-- {
		a := attempts[i]
		if auditOnly(a) {
			continue
		}
		if a.Cause != CauseInfraFailure || !transientCause(a.FailureCause) {
			break
		}
		n++
	}
	return n
}

// decidedBackoff answers the wait a death of cause owes before the step's
// next dispatch, given the step's archived attempts BEFORE that death:
// the transient backoff for a transient cause, none for anything else.
// The failure decision books it; RetryOwed re-derives it from the archive,
// so both read one rule.
func decidedBackoff(prior []capsule.Attempt, cause string) time.Duration {
	if transientCause(cause) {
		return transientBackoff(transientStreak(prior))
	}
	return 0
}

// RetryOwed answers the part of a decided backoff that is still owed
// before the current step's next dispatch, and the death cause that owes
// it, derived from execution.json alone (the attempts ledger): the
// workflow runs, its current step is in_progress with no open question,
// the step's newest archived attempt — the audit-only interruption entries
// skipped — is an infra failure of a backoff-owing cause, and no dispatch
// started since (the step's start stamp is still the one that attempt
// archived), and that attempt's decision kept the candidate (the
// persisted anchor Step.Candidate still names the model it ran). The wait
// counts from the failure's interrupted_at, so a restarted driver honours
// what is left of it and the status face can show it; zero when nothing
// is owed. A decision that blocked the step owes nothing while the
// workflow stays blocked, and the owner's continue (Supervision.ResumedAt)
// opens a new streak era: a death that started before it owes nothing.
func RetryOwed(exec *capsule.Execution, now time.Time) (time.Duration, string) {
	if exec == nil || exec.WorkflowStatus != StatusRunning {
		return 0, ""
	}
	st := exec.Steps[exec.CurrentStep]
	if st == nil || st.Status != capsule.StepInProgress {
		return 0, ""
	}
	j := len(st.Attempts) - 1
	for j >= 0 && auditOnly(st.Attempts[j]) {
		j--
	}
	if j < 0 {
		return 0, ""
	}
	a := st.Attempts[j]
	era := streakEra(st)
	if a.Cause != CauseInfraFailure || a.StartedAt != st.StepStartedAt || a.Candidate != st.Candidate || beforeEra(a, era) {
		return 0, ""
	}
	backoff := decidedBackoff(st.Attempts[:j], a.FailureCause)
	if backoff <= 0 {
		return 0, ""
	}
	at, err := time.Parse(time.RFC3339Nano, a.InterruptedAt)
	if err != nil {
		return 0, ""
	}
	wait := at.Add(backoff).Sub(now)
	if wait <= 0 {
		return 0, ""
	}
	return min(wait, backoff), a.FailureCause
}

// rateLimitBackoff is the transient backoff under its D7 name.
func rateLimitBackoff(streak int) time.Duration { return transientBackoff(streak) }

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
// hand-off: the raw material of the driver's own
// exited_without_result classification.
type ExitedError struct {
	Step      string
	SpawnN    int64
	ExitCode  int
	Signal    string
	LastError string // stderr tail / last reported error
	TurnError bool   // the last signal was "turn ended with an error"
	TurnOK    bool   // the last signal was a normally ended turn (no error)
	// StderrIncomplete: the stderr tail was read before its copy caught
	// up with the exit (spawn.ExitInfo), so LastError may lack the
	// process's last words. It is recorded in Error() — the archived
	// failure detail — and never classified.
	StderrIncomplete bool
	// StdoutIncomplete: the exit was read before the stdout reader caught
	// up with it (spawn.ExitInfo), so the process's last records may not
	// have reached the dispatch. Recorded in Error(), never classified.
	StdoutIncomplete bool
}

func (e *ExitedError) Error() string {
	msg := fmt.Sprintf("driver: step %s spawn %d exited without a result (code %d", e.Step, e.SpawnN, e.ExitCode)
	if e.Signal != "" {
		msg += ", " + e.Signal
	}
	msg += ")"
	if e.LastError != "" {
		msg += ": " + e.LastError
	}
	if e.StderrIncomplete {
		msg += " [" + spawn.StderrIncompleteNote + "]"
	}
	if e.StdoutIncomplete {
		msg += " [" + spawn.StdoutIncompleteNote + "]"
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
	// TreeHash is the capsule tree hash as of this death,
	// computed by failDispatch BEFORE the transition takes the driver's
	// lock and archived on the attempt in the same atomic write. Empty
	// when hashing failed (the mismatch/no-hash path = today's behavior).
	TreeHash string
}

// ---------------------------------------------------------------------------
// Exit classification (the driver's own judgement, 2.3 table).

var hostPatterns = regexp.MustCompile(`(?i)(no space left|enospc|disk quota|edquot|permission denied|eacces|read-only file system|erofs|too many open files|emfile|cannot allocate memory|out of memory|enomem|input/output error|executable file not found|no such file or directory.*(pi|harness)|权限不够|权限被拒绝|拒绝访问)`)

// allowanceText is the allowance/auth row of textClasses (model-class,
// D7): the account or key cannot use this candidate — a different one may
// work, so these switch IMMEDIATELY, even when the text also carries a
// throttle or transport signature ("429 ... exceeded your current quota",
// "insufficient balance ... connection reset"). verdictCause promotes a
// live error turn naming it to exit_model_class.
var allowanceText = regexp.MustCompile(`(?i)(\b401\b|\b403\b|unauthori[sz]ed|authentication|invalid api key|incorrect api key|api key|quota|insufficient|billing|credit|` +
	`余额不足|欠费|(额度|配额)[^，。；,.;\n]{0,6}(用尽|用完|耗尽|不足|超出|超限)|(超出|超过)了?(额度|配额)|` +
	`账户[^，。；,.;\n]{0,10}(停用|禁用|冻结)|鉴权失败|认证失败|无效的?(密钥|令牌)|(密钥|令牌)无效|` +
	`(无权限|没有权限|权限不足)[^，。；,.;\n]{0,6}(模型|接口|API|服务)|(模型|接口|API|服务)[^，。；,.;\n]{0,6}(无权限|没有权限|权限不足))`)

// The name-resolution forms of a live error turn: the pinned model does
// not exist for the provider. verdictCause promotes such a turn to
// exit_model_class (resuming the same unresolvable model cannot help).
// Only ADJACENT forms count — "unknown/unresolved/unsupported/no such
// model", the model_not_found code, the Claude translator's canonical
// "claude-code model not found" (supervise.ClaudeCanonicalError renders
// it only from the structured model_not_found category or an HTTP 404 —
// a claude step's error turn is a LIVE turn, the process idles on, so
// the D7 row "model not found → switch" must hold here as on the exit
// path), or a resolution phrase directly after
// the model's name or id ("model "x" not found", "model x: not found",
// "The model `x` does not exist"). A resolution word anywhere else in a
// sentence that mentions the model is outage-shaped ("The model
// encountered an unknown error", "model server returned unknown status",
// "model endpoint not found, retrying") and stays model_error, as do the
// model-name row's looser shapes ("model ... unavailable", "model ...
// invalid response").
var (
	unresolvedModelAdjacent = regexp.MustCompile(`(?i)\b(unknown|unresolved|unsupported|no such) model\b|\bmodel_not_found\b|\bclaude-code model not found\b`)
	unresolvedModelNamed    = regexp.MustCompile("(?i)\\bmodel\\W+(\"[^\"]{1,128}\"|'[^']{1,128}'|`[^`]{1,128}`|[^\\s\"'`,;:]{1,128})\\W{0,3}(not found|cannot be resolved|could not be resolved|does not exist)\\b")
)

// modelInfraNouns are the words that, right after "model", name the
// serving infrastructure rather than a model id: "model endpoint not
// found" is an outage, "model xyz not found" a name that does not resolve.
var modelInfraNouns = map[string]bool{
	"server": true, "servers": true, "endpoint": true, "endpoints": true, "service": true, "backend": true,
	"provider": true, "gateway": true, "host": true, "instance": true, "instances": true, "worker": true,
	"workers": true, "api": true, "route": true, "upstream": true, "node": true, "pool": true,
	"replica": true, "replicas": true, "container": true, "process": true, "deployment": true,
}

// unresolvedModel reports whether a live error turn names a model that
// does not resolve (the adjacent forms above).
func unresolvedModel(text string) bool {
	if unresolvedModelAdjacent.MatchString(text) {
		return true
	}
	for _, m := range unresolvedModelNamed.FindAllStringSubmatch(text, -1) {
		if !modelInfraNouns[strings.ToLower(m[1])] {
			return true
		}
	}
	return false
}

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
	{allowanceText, FailExitModel, false},
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
	// "invalid" is a whole word: a gateway body's invalid_request_error
	// type names no model problem.
	{regexp.MustCompile(`(?i)(model\W.{0,60}(not found|unknown|unresolved|cannot be resolved|could not be resolved|not resolve|invalid\b|unavailable|not available|unsupported|does not exist)|(unknown|invalid|unparse?able|unresolved|unsupported|no such) model|no models? (found|available|match))`),
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
		text := strings.TrimSpace(ee.LastError)
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
// upstream transient is a backoff retry here too, and an allowance/auth
// text (allowanceText) or a model name that does not resolve
// (unresolvedModel) is archived exit_model_class, as on an exited
// process — it switches the candidate at once. Any other text — a gateway
// 5xx, a "model ... temporarily unavailable" or "invalid response" outage,
// an unnamed provider error — keeps model_error: model-class, but its
// first death on a candidate resumes the same candidate
// (ModelErrorRotationThreshold). The split lands in the archived cause,
// so the pointer never re-reads the text.
func verdictCause(v supervise.StepDead) string {
	if v.Cause != supervise.CauseModelError || strings.TrimSpace(v.TurnError) == "" {
		return string(v.Cause)
	}
	cause, _, _ := classifyWaitFailure(&ExitedError{Step: v.Step, SpawnN: v.SpawnN, ExitCode: -1, LastError: v.TurnError, TurnError: true})
	switch cause {
	case FailRateLimited, FailUpstreamTransient:
		return cause
	}
	if allowanceText.MatchString(v.TurnError) || unresolvedModel(v.TurnError) {
		return FailExitModel
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
	def := d.limits()
	tool, wall = def.Tool, def.StepTimeout
	if spec := d.workflow.step(stepID); spec != nil {
		if spec.WallMinutes > 0 {
			wall = max(wall, time.Duration(spec.WallMinutes)*time.Minute*WallEstimateFactor)
		}
		if w := spec.toolWindow(); w > 0 {
			tool = w // the step's own window (budget.tool_minutes) replaces the global one
		}
	}
	return tool, wall
}

// limits answers the current global supervision defaults (a live reload's
// LimitsFunc when the daemon bound one).
func (d *Driver) limits() supervise.Defaults {
	if d.cfg.LimitsFunc != nil {
		return d.cfg.LimitsFunc().Fill()
	}
	return d.cfg.Limits.Fill()
}

// effectiveThresholdsLocked answers the thresholds a dispatch of stepID
// runs under: the bases raised to any relaxed value persisted in
// execution.json, plus the step runtime already spent. T is CUMULATIVE
// over the whole step (polyforge#147 D6): a candidate switch or a human
// continue does not reopen the window — only a rewind's fresh start
// zeroes it.
func (d *Driver) effectiveThresholdsLocked(stepID string, gran supervise.Granularity) supervise.Thresholds {
	tool, wall := d.stepBases(stepID)
	th := supervise.Thresholds{Silence: d.limits().SilenceFor(gran), Tool: tool, StepTimeout: wall}
	// The step's own floor: its wall estimate and any persisted relaxed
	// bound, WITHOUT the current global T. The supervisor replays
	// T = max(live step_timeout, floor) every tick, so a live reload of
	// step_timeout reaches an in-flight dispatch.
	floor := stepTimeoutFloor(d.workflow.step(stepID))
	if st := d.exec.Steps[stepID]; st != nil {
		if sup := st.Supervision; sup != nil {
			th.Tool = max(th.Tool, time.Duration(sup.RelaxedToolMs)*time.Millisecond)
			th.StepTimeout = max(th.StepTimeout, time.Duration(sup.RelaxedWallMs)*time.Millisecond)
			floor = max(floor, time.Duration(sup.RelaxedWallMs)*time.Millisecond)
		}
		th.PriorRuntime = time.Duration(max(st.BudgetConsumed.WallMs+d.unbooked[stepID], 0)) * time.Millisecond
	}
	th.StepTimeoutFloor = floor
	return th
}

// toolWindow answers the step's declared tool window (budget.tool_minutes;
// 0 = none declared).
func (s *StepSpec) toolWindow() time.Duration {
	if s == nil || s.ToolMinutes <= 0 {
		return 0
	}
	return time.Duration(s.ToolMinutes) * time.Minute
}

// stepTimeoutFloor answers a step's own part of T (everything but the
// global step_timeout): 3 x its budget.wall_minutes estimate.
func stepTimeoutFloor(spec *StepSpec) time.Duration {
	if spec == nil || spec.WallMinutes <= 0 {
		return 0
	}
	return time.Duration(spec.WallMinutes) * time.Minute * WallEstimateFactor
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

// announceStart sends step_started for a freshly spawned dispatch and
// answers the start instant it announced (zero without a link) — the
// stamp declaredPause measures the supervisor's shift against.
func (d *Driver) announceStart(sp *StepSpawn, run StepRun, startedAt time.Time) time.Time {
	if d.cfg.Link == nil {
		return time.Time{}
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
	return meta.StartedAt
}

// declaredPause answers how much of dispatch n's wall the supervisor
// declared unobserved (R4). The monitor forgives its own
// observation gap — a host or daemon freeze — by moving every stamp of
// the dispatch's tracker at or before the pause forward by the gap
// (Tracker.ObserveTick), the dispatch's start included, so
// the distance from the start the driver announced to the tracker's start
// now is exactly the sum of the gaps declared during this dispatch. It
// must be read before step_ended (the monitor drops the tracker on it). No
// link, or a tracker no supervisor attached step_started to, declares
// nothing. The tracker is in-memory state behind its own mutex: no I/O,
// and d.mu is not held.
func (d *Driver) declaredPause(n int64, announced time.Time) time.Duration {
	if d.cfg.Link == nil || announced.IsZero() {
		return 0
	}
	snap := d.cfg.Link.Tracker(n).Snapshot(announced)
	if !snap.Begun {
		return 0
	}
	return max(snap.StartedAt.Sub(announced), 0)
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

// verdictFinalLook is the optional StepRun face the verdict path uses to
// read its drop one last time after the verdict cancelled Wait. An envelope
// already written when the verdict arrived is a delivery that raced the
// verdict and wins; a StepRun without this face offers only
// its Wait outcome.
type verdictFinalLook interface {
	finalLook() (*StepResult, error)
}

// awaitDispatch waits for the dispatch's outcome while consuming the
// supervisor's verdicts behind the fence and watching the capsule's ask
// record (ask.go). It returns the result, the wait error, and — only when
// a verdict ended the dispatch — the verdict. A result that raced a
// verdict wins (the dispatch delivered). A dispatch whose agent asked the
// owner (`pf ask`: an open ask of this dispatch in asks.jsonl, re-read
// every Config.AskPoll) is marked awaiting the owner at once — the event,
// the supervisor's StepOwnerWait, the slot release — and its process is
// kept for the warm window; when that runs out (or the idle process
// exits by itself) the wait error is an *awaitingOwnerEnd: no death, the
// step stays awaiting. A verdict that raced the ask is discarded. The
// loop is also the dispatch's owner-message relay (relay.go): it takes
// each request off Driver.relay and writes it into the live process —
// a step awaiting the owner leaves that state at the harness's
// acceptance (the warm window stops, the slot is taken back, the
// supervisor judges again) — and it marks a message delivered once its
// marker shows in the stream (the watch's notify), the prompt-carried
// messages once the first turn starts.
func (d *Driver) awaitDispatch(ctx context.Context, run StepRun, stepID string, n int64) (*StepResult, error, *supervise.StepDead) {
	defer d.relayWaitCalls(stepID, n)()
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
	askTick := time.NewTicker(d.askPoll())
	defer askTick.Stop()
	var (
		awaiting *awaitingOwnerEnd
		warm     <-chan time.Time
		warmT    *time.Timer
	)
	defer func() {
		if warmT != nil {
			warmT.Stop()
		}
	}()
	rl := d.newRelayState(run, stepID, n)
	defer d.setLive("", 0)
	watchNotify := rl.notify()
	// leaveAwaiting ends the awaiting-owner state in place: the message
	// was accepted by the live process, so the warm window is off and
	// the dispatch is running again.
	leaveAwaiting := func() {
		if warmT != nil {
			warmT.Stop()
		}
		warmT, warm, awaiting = nil, nil, nil
	}
	d.relayConfirm(rl) // a prompt-carried message whose turn started already
	// cut ends the wait and tears the process down — unless an envelope
	// landed first: a delivery wins any race. The verdict cut Wait short,
	// so one last look at the drop is taken: an envelope already on disk is
	// complete (the daemon writes it atomically), and without this look the
	// window between Wait's last collect and the cut swallowed the delivery.
	cut := func() (*StepResult, error) {
		wcancel()
		r := <-ch
		if r.res != nil && r.err == nil {
			return r.res, nil // the envelope landed first: the dispatch delivered
		}
		if fl, ok := run.(verdictFinalLook); ok {
			res, ferr := fl.finalLook()
			if res != nil {
				return res, nil // finalLook archived and consumed it, and tore the process down
			}
			if ferr != nil {
				r.err = ferr
			}
		}
		// Nothing on disk: the process still has to go.
		_ = run.Terminate(teardownBound)
		return nil, r.err
	}
	// endWarm ends the awaiting dispatch: the idle process is ended
	// (stdin closed, then the signal — the teardown an accepted envelope
	// gets) and the step stays awaiting the owner; the owner's message
	// resumes the session (relay.go)
	endWarm := func() (*StepResult, error, *supervise.StepDead) {
		if res, _ := cut(); res != nil {
			return res, nil, nil
		}
		return nil, awaiting, nil
	}
	for {
		select {
		case r := <-ch:
			// a delivery proof that landed in the same poll window as the
			// result is marked now, or the line would be replayed into the
			// step's next dispatch as a duplicate
			d.relayConfirm(rl)
			if awaiting != nil && r.res == nil && ctx.Err() == nil {
				// the process ended by itself while the owner is asked: the
				// question stands, nothing died (a cancellation keeps its
				// own path)
				return nil, awaiting, nil
			}
			return r.res, r.err, nil
		case req := <-d.relay:
			if !d.relayTake(ctx, rl, req, awaiting != nil, leaveAwaiting) && awaiting != nil {
				// the reply is recorded but the idle process did not take
				// it: the warm window ends now, so the resume delivers it
				// at once instead of when the window runs out
				return endWarm()
			}
		case <-watchNotify:
			d.relayConfirm(rl)
		case <-askTick.C:
			d.relayConfirm(rl)
			if awaiting != nil {
				continue
			}
			ask := d.openAsk(stepID, n)
			if ask == nil {
				continue
			}
			awaiting = &awaitingOwnerEnd{Ask: *ask, Since: d.now()}
			d.markAwaitingOwner(stepID, n, ask)
			if d.stepDeclaresResources(stepID) {
				// contract 2 (Q9 ③): a dispatch holding declared
				// resources keeps no warm window — the idle process ends
				// now and the loop gives its admission and locks back;
				// the owner's message resumes the session through a
				// dispatch that takes them again before it is delivered
				return endWarm()
			}
			warmT = time.NewTimer(d.askWarmWindow())
			warm = warmT.C
		case <-warm:
			// the warm window is over (endWarm)
			return endWarm()
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
				if awaiting != nil {
					// a verdict that raced the ask: nothing is judged while
					// the owner is asked
					d.emit("verdict_stale", map[string]any{"step": dead.Step, "spawn_n": dead.SpawnN, "current_spawn_n": n, "cause": dead.Cause, "note": "awaiting owner"})
					continue
				}
				// Terminating: cancel the wait first so the process can be torn
				// down, then ignore its exit and any further verdict of this
				// dispatch.
				res, werr := cut()
				if res != nil {
					return res, nil, nil
				}
				return nil, werr, &dead
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
// Wait calls: counted and surfaced, never
// judged.

// EvWaitCall records one wait call of a dispatch: a bash tool call whose
// command starts with `sleep N`, N over supervise.WaitCallFloor (form
// "sleep", seconds = N), or that runs the shell's `wait` builtin for over
// the floor of measured wall time (form "wait", seconds = that time). It
// is news only — no rule reads it, no verdict changes and nothing is
// killed for it. The body names the call (form, seconds, argv excerpt,
// tool call id, at) and the dispatch's running totals (wait_calls,
// wait_seconds).
const EvWaitCall = "wait_call"

// relayWaitCalls records one wait_call event per wait call dispatch n's
// tracker folds, while the dispatch runs. The returned stop ends the relay
// and then takes what is left, so a call folded just before the dispatch
// ended is recorded too, and each call exactly once (the tracker hands a
// call out once, and the two takers never overlap). Before that last take
// it folds the wait-form calls still running — a dispatch killed while
// blocked in `wait` never ends that call — with the time they ran. The appends are I/O:
// they run on the relay goroutine — never on the harness stdout reader,
// never under d.mu. Without a link nothing is relayed.
func (d *Driver) relayWaitCalls(stepID string, n int64) (stop func()) {
	if d.cfg.Link == nil {
		return func() {}
	}
	tr := d.cfg.Link.Tracker(n)
	record := func() {
		for _, w := range tr.TakeWaitCalls() {
			d.emit(EvWaitCall, map[string]any{
				"step": stepID, "spawn_n": n,
				"form": w.Form, "seconds": w.Seconds, "argv": w.Argv, "tool_call_id": w.ToolCallID,
				"at":         w.At.UTC().Format(time.RFC3339Nano),
				"wait_calls": w.WaitCalls, "wait_seconds": w.WaitSeconds,
			})
		}
	}
	quit, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-tr.WaitCallNotify():
				record()
			case <-quit:
				return
			}
		}
	}()
	return func() {
		close(quit)
		<-done
		tr.FinishOpenWaitCalls()
		record()
	}
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

// decideFailureLocked applies the contract 4 supervision rules (design
// #483 §7; the classes are listed at MaxEwrStreak) to the step's current
// state:
//
//   - transient (rate limit, upstream transient): the SAME model after the
//     transient backoff, the transcript resumed, the counter untouched;
//   - model class: switch to the next candidate after the persisted
//     Step.Candidate anchor (a fresh conversation, the counter reset); no
//     candidate left, or no list at all, blocks — the reason opens with
//     NoUsableModelBlockPrefix (the agent cannot start: no message wakes it);
//   - ended_without_result: the same session resumed (the reminder rides
//     its prompt); the MaxEwrStreak-th in a row blocks;
//   - timeout (step total time, a hung tool call): the bound doubled and
//     persisted (D6), the same session resumed; the MaxTimeouts-th timeout
//     since the last reset blocks;
//   - any other exit (host class, harness silence, a crash ...): the same
//     session resumed; the MaxOtherStreak-th in a row blocks.
//
// A slow-model timeout is model class and relaxes T x2 as well, so the
// next candidate runs under the doubled bound with the cumulative runtime
// carried over. A decision that switches or blocks owes no wait.
func (d *Driver) decideFailureLocked(stepID string, st *capsule.Step, f *StepFailure) failureDecision {
	dec := failureDecision{Cause: f.Cause}
	var sup capsule.StepSupervision
	if st.Supervision != nil {
		sup = *st.Supervision
	}
	era := streakEra(st)
	tool, wall := d.stepBases(stepID)
	switch f.Cause {
	case FailToolHung:
		dec.RelaxToolMs = doubledMs(tool, sup.RelaxedToolMs)
	case FailStepTimeoutSlowModel, FailStepTimeoutLongTask:
		dec.RelaxWallMs = doubledMs(wall, sup.RelaxedWallMs)
	}
	switch {
	case transientCause(f.Cause):
		dec.Count = sup.FailureCount
		dec.Backoff = decidedBackoff(st.Attempts, f.Cause)
	case IsModelClass(f.Cause):
		cands := d.effectiveCandidates(stepID)
		// model selection reads no streak era: a switch a chain-end or
		// no-chain death still owes survives the owner's continue
		ptr := CandidatePointer(st.Candidate, st.Attempts, cands)
		switch {
		case len(cands) > 0 && ptr < len(cands)-1:
			dec.Switch, dec.Fresh = true, true
			dec.From, dec.To = cands[ptr], cands[ptr+1]
		default:
			dec.Block, dec.Report = true, true
			dec.NoChain = len(cands) == 0
			dec.Exhausted = !dec.NoChain
			dec.Count = sup.FailureCount + 1
			what := "the step's candidate list is spent"
			if dec.NoChain {
				what = "the step has no candidate list to switch along"
			}
			dec.BlockReason = fmt.Sprintf("%sstep %s: a model-class death (%s) on %q and %s: %s — edit .polyforge.yaml (steps.%s.models) to add or change a model, then `pf resume` (or `pf cancel`)",
				NoUsableModelBlockPrefix, stepID, f.Cause, st.Candidate, what, f.Detail, stepID)
		}
	case f.Cause == FailEndedWithoutResult:
		dec.Count = classStreak(st.Attempts, era, func(c string) bool { return c == FailEndedWithoutResult }) + 1
		if dec.Count >= d.maxStepFailures() {
			dec.Block = true
			dec.BlockReason = fmt.Sprintf("step %s ended without `pf step handoff` %d times in a row on model %q: %s",
				stepID, dec.Count, st.Candidate, f.Detail)
		}
	case timeoutCause(f.Cause):
		dec.Count = sup.LongTaskTimeouts + 1
		if dec.Count >= MaxTimeouts {
			dec.Block = true
			dec.BlockReason = fmt.Sprintf("step %s timed out again (%s) after its bound was already doubled once: %s", stepID, f.Cause, f.Detail)
		}
	default:
		dec.Count = classStreak(st.Attempts, era, otherCause) + 1
		if dec.Count >= MaxOtherStreak {
			dec.Block, dec.Report = true, true
			dec.BlockReason = fmt.Sprintf("step %s exited again without a hand-off (%s) after one resume: %s", stepID, f.Cause, f.Detail)
		}
	}
	if dec.Block || dec.Switch {
		// a block owes no re-dispatch; a switch leaves the outage behind
		// with its candidate: nothing to wait for or book (RetryOwed reads
		// the same switch off the moved anchor)
		dec.Backoff = 0
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
	// Capture the dying dispatch's candidate BEFORE the decision below may
	// persist the successor: the step_failed body self-describes the
	// dispatch that failed, not the one the next dispatch will run (the
	// identity rule of the model-health profile joins on step_started).
	info.candidate = st.Candidate
	// S3: a step rotates the candidate on a timeout at most
	// once per timeout ledger (reset with LongTaskTimeouts: a rewind, the
	// gate's re-entry, the owner's continue). The supervisor's rule-4 share
	// test makes a second slow-model blame unlikely but not impossible — a
	// successor whose tick overshoot exceeds its predecessor's teardown can
	// reach half of the doubled T — so a later one is archived as the long
	// task it is, deliberately (the upstream-transient pattern above).
	if f.Cause == FailStepTimeoutSlowModel && st.Supervision != nil && st.Supervision.SlowModelTimeouts >= 1 {
		f.Cause = FailStepTimeoutLongTask
		f.Detail += " (archived as step_timeout_long_task: this step already rotated the candidate on a slow-model timeout since its timeout ledger was last reset)"
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
		Candidate:     st.Candidate,
		TreeHash:      f.TreeHash,
	})
	if dec.Switch {
		// the decided switch is persisted with its archive entry: the anchor
		// of every later pointer computation, so an owner edit of the list
		// between this death and the next dispatch cannot double the advance
		st.Candidate = dec.To
	}
	if st.Supervision == nil {
		st.Supervision = &capsule.StepSupervision{}
	}
	sup := st.Supervision
	sup.FailureCount = dec.Count
	if timeoutCause(f.Cause) {
		// the timeout class's ledger since the last reset: a long task
		// and a hung tool call alike (MaxTimeouts)
		sup.LongTaskTimeouts++
	}
	if f.Cause == FailStepTimeoutSlowModel {
		sup.SlowModelTimeouts++
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

// addUnbookedUntil adds one finished dispatch's wall time, measured from
// since to until (the envelope's delivery stamp for a collected envelope).
func (d *Driver) addUnbookedUntil(stepID string, since, until time.Time) {
	ms := max(until.Sub(since).Milliseconds(), 0)
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
// applies the death-cause table, then the backoff the decision imposed
// (already booked into T). until is the instant the dispatch's process was
// known gone — the step-end cleanup that runs after it is not dispatch
// runtime, so the booked wall stops there. stop reports that Run must
// return err: the write failed, the decision parked the workflow (err
// nil — Run exits cleanly, the owner holds the next move), or the context
// ended during the backoff (no step_start may land then: the successor
// serves the rest of the wait from the attempts ledger, RetryOwed).
func (d *Driver) failDispatch(ctx context.Context, stepID string, n int64, since, until time.Time, cause string, exitCode int, detail string, dead *supervise.StepDead) (stop bool, err error) {
	d.addUnbookedUntil(stepID, since, until)
	f := &StepFailure{Cause: cause, Detail: detail, ExitCode: exitCode, WallMs: d.takeUnbooked(stepID)}
	// The tree hash is read BEFORE the transition takes the driver's lock
	// (the VerifiedHeads/LightDiffBase pattern: I/O never under d.mu); a
	// hash error leaves the field empty — the mismatch/no-hash path is
	// exactly today's behavior.
	if h, herr := treeHashFn(d.wi.CapsuleDir); herr == nil {
		f.TreeHash = h
	} else {
		fmt.Fprintf(os.Stderr, "driver: %s: step %s tree hash unavailable (recording none): %v\n", d.wi.ID, stepID, herr)
	}
	last := detail[:min(len(detail), 500)]
	d.announceEnd(stepID, n, supervise.OutcomeExitedWithoutResult, exitCode, last)
	if err := d.transition(TransitionRequest{Kind: TransStepFailed, StepID: stepID, SpawnN: n, Failure: f}); err != nil {
		return true, fmt.Errorf("driver: step %s dispatch %d failure record failed: %w", stepID, n, err)
	}
	if dead != nil {
		// The supervisor verdict record, after the failure write: a reader
		// that observes step_dead also observes the step_failed that names
		// the refined cause and the dying candidate.
		d.emit("step_dead", map[string]any{
			"step": stepID, "spawn_n": n, "cause": dead.Cause, "evidence": dead.Evidence,
		})
	}
	d.mu.Lock()
	stop = d.exec.WorkflowStatus == StatusBlocked
	d.mu.Unlock()
	if !stop {
		d.waitRetry(ctx, stepID, f.Cause, f.Backoff)
		if cerr := ctx.Err(); cerr != nil && f.Backoff > 0 {
			return true, fmt.Errorf("driver: step %s retry backoff (%s) interrupted: %w", stepID, f.Cause, cerr)
		}
	}
	return stop, nil
}

// waitRetry waits a decided backoff before the step's next dispatch,
// declared to the supervisor first (step_retry_wait) so the wait does not
// run its between-steps stall clock. Cut short by ctx.
func (d *Driver) waitRetry(ctx context.Context, stepID, cause string, wait time.Duration) {
	if wait <= 0 {
		return
	}
	d.fact(supervise.StepRetryWait{Step: stepID, Cause: cause, Until: d.now().Add(wait)})
	d.sleep(ctx, wait)
}

// awaitOwedRetry serves what is left of a backoff the attempts ledger
// still owes the current step (RetryOwed) before this Run's first
// dispatch — the restart and relaunch path; the owner's continue opened a
// new streak era, which owes nothing. A cancellation during the wait ends
// Run.
func (d *Driver) awaitOwedRetry(ctx context.Context) error {
	d.mu.Lock()
	wait, cause := RetryOwed(d.exec, d.now())
	stepID := d.exec.CurrentStep
	d.mu.Unlock()
	if wait <= 0 {
		return nil
	}
	d.emit("retry_wait", map[string]any{"step": stepID, "cause": cause, "wait_ms": wait.Milliseconds(), "resumed": true})
	d.waitRetry(ctx, stepID, cause, wait)
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("driver: step %s retry backoff (%s) interrupted: %w", stepID, cause, err)
	}
	return nil
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
	timerSleep(ctx, wait)
}

// timerSleep is the real wait behind Driver.sleep: a timer cut short by
// ctx. The package's TestMain replaces it, so no test that leaves
// Config.Sleep unset waits a real retry backoff.
var timerSleep = func(ctx context.Context, wait time.Duration) {
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
