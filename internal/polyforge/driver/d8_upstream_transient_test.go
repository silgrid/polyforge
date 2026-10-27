package driver

// d8_upstream_transient_test.go: the upstream-transient class of
// wi_seWTgHeKtaa8.
// An upstream's OCCASIONAL return corruption — truncated tool-call
// arguments, an empty response, a dropped connection — is NOT a
// model-class death: the same model is retried under the D7 backoff,
// bounded at UpstreamTransientRetryBound; past the bound the attempt
// archives as a model-class death and switches candidates. Allowance /
// auth / model-name texts keep the immediate switch (the existing D7
// tests pin those); pure throttle texts keep their own class.

import (
	"context"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// TestD8TransientTextsClassifyUpstreamTransient: the transient
// signatures (also on an error turn) classify as the upstream-transient
// cause, which never advances the candidate chain; the controls keep
// their classes.
func TestD8TransientTextsClassifyUpstreamTransient(t *testing.T) {
	for _, text := range []string{
		"Upstream tool call has incomplete or invalid JSON arguments",
		"tool call arguments truncated mid-stream",
		"empty response from the provider",
		"connection reset by peer while streaming",
		"upstream dropped the connection",
		"stream closed: unexpected EOF",
		"the model returned an invalid tool call (incomplete JSON arguments)",
		"provider returned an empty response (0 output tokens)",
		"Upstream response stream ended before completion",
	} {
		for _, turnErr := range []bool{false, true} {
			got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: text, TurnError: turnErr})
			if got != FailUpstreamTransient {
				t.Errorf("%q (turn error %v) classified %q, want %q", text, turnErr, got, FailUpstreamTransient)
			}
		}
		// the non-exit path is the driver's own failure: never transient
		if got, _, _ := classifyWaitFailure(errWithText("read transcript: " + text)); got == FailUpstreamTransient {
			t.Errorf("driver-local %q classified %q, want not transient", text, got)
		}
	}
	// controls: the other classes keep their texts.
	if got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: "429 Too Many Requests"}); got != FailRateLimited {
		t.Errorf("a throttle must stay a throttle: %q", got)
	}
	if got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: "insufficient quota"}); got != FailExitModel {
		t.Errorf("an allowance problem must stay model-class: %q", got)
	}
	if got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: `model "x/y" not found`}); got != FailExitModel {
		t.Errorf("a model-name problem must stay model-class: %q", got)
	}
	// a bare model "ended" has no upstream/stream subject: never transient.
	if got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: "the model ended the turn", TurnError: true}); got == FailUpstreamTransient {
		t.Errorf("a model ending its own turn must not be an upstream transient: %q", got)
	}
	if IsModelClass(FailUpstreamTransient) {
		t.Fatalf("an upstream transient must never advance the candidate pointer")
	}
}

// errWithText builds a plain error carrying the text (the startup-path
// classification input).
func errWithText(text string) error {
	return &textError{text}
}

type textError struct{ s string }

func (e *textError) Error() string { return e.s }

// TestD8TransientStreakResetsOnAnotherCause: the bound's ledger counts
// only the TRAILING consecutive transients.
func TestD8TransientStreakResetsOnAnotherCause(t *testing.T) {
	att := func(cause string) capsule.Attempt {
		return capsule.Attempt{Cause: CauseInfraFailure, FailureCause: cause}
	}
	cases := []struct {
		name string
		in   []capsule.Attempt
		want int
	}{
		{"none", nil, 0},
		{"one", []capsule.Attempt{att(FailUpstreamTransient)}, 1},
		{"two", []capsule.Attempt{att(FailUpstreamTransient), att(FailUpstreamTransient)}, 2},
		{"interrupted", []capsule.Attempt{att(FailUpstreamTransient), att(FailExitOther), att(FailUpstreamTransient)}, 1},
		{"trailing other", []capsule.Attempt{att(FailUpstreamTransient), att(FailExitModel)}, 0},
	}
	for _, tc := range cases {
		if got := upstreamTransientStreak(tc.in); got != tc.want {
			t.Errorf("%s: streak %d, want %d", tc.name, got, tc.want)
		}
	}
}

// TestD8TransientArchiveLedgerAndSwitch: three consecutive transients —
// the first two archive upstream_transient and retry the same model
// (counter untouched, backoff imposed, booked into T), the third
// archives exit_model_class and switches. The archive alone answers the
// remaining bound.
func TestD8TransientArchiveLedgerAndSwitch(t *testing.T) {
	var slept []time.Duration
	env := newChainEnv(t, func(c *Config) {
		c.Sleep = func(_ context.Context, d time.Duration) { slept = append(slept, d) }
	}, "s", "a", "b")
	text := "Upstream tool call has incomplete or invalid JSON arguments"
	env.sp.script = []fakeEntry{
		exitErr(1, text, true),
		exitErr(1, text, true),
		exitErr(1, text, true),
		done(`{"ok":true}`),
	}
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := env.exec(t)
	if x.WorkflowStatus != StatusCompleted {
		t.Fatalf("the transient-bounded step must deliver: status %q (%q)", x.WorkflowStatus, x.BlockedReason)
	}
	st := x.Steps["s"]
	var causes []string
	for _, a := range st.Attempts {
		if a.Cause == CauseInfraFailure {
			causes = append(causes, a.FailureCause)
		}
	}
	want := []string{FailUpstreamTransient, FailUpstreamTransient, FailExitModel}
	if len(causes) != len(want) {
		t.Fatalf("archived failure causes %v, want %v", causes, want)
	}
	for i := range want {
		if causes[i] != want[i] {
			t.Fatalf("archived failure causes %v, want %v", causes, want)
		}
	}
	// the bound-crossing attempt is a model-class SWITCH: the blocking
	// counter starts the new candidate's fresh ledger (Count=0, the
	// existing switch rule), and the two in-bound retries never moved it.
	if st.Supervision != nil && st.Supervision.FailureCount != 0 {
		t.Fatalf("the in-bound retries must not move the blocking counter: %+v", st.Supervision)
	}
	// the two in-bound retries waited the D7 ladder; the switch did not.
	if len(slept) != 2 || slept[0] != RateLimitBackoffBase || slept[1] != 2*RateLimitBackoffBase {
		t.Fatalf("backoff waits %v, want the D7 ladder [1s 2s] for the two retries only", slept)
	}
}

// TestD8ClassificationPrecedence pins the text-class order: allowance/
// auth (switch) > throttle (same model) > upstream transient (same
// model, bounded) > model name (switch) — and keeps the transient class
// narrow (bare "invalid ...", context length and local I/O errors stay
// out of it).
func TestD8ClassificationPrecedence(t *testing.T) {
	for _, tc := range []struct {
		text string
		want string
	}{
		{"the model returned an invalid tool call (incomplete JSON arguments)", FailUpstreamTransient},
		{"insufficient balance on the account; connection reset by peer", FailExitModel},
		{"invalid model name foo", FailExitModel},
		{"401 Unauthorized", FailExitModel},
		{"429 Too Many Requests: connection reset", FailRateLimited},
		{"429 You exceeded your current quota", FailExitModel},
		// Chinese provider texts (no \b / (?i) for CJK): throttle → rate limit
		{"当前并发数过高，请稍后重试", FailRateLimited},
		{"并发 超限", FailRateLimited},
		{"请求过于频繁，请稍后再试", FailRateLimited},
		{"请求频率超过限制", FailRateLimited},
		{"触发限流", FailRateLimited},
		{"超过速率限制", FailRateLimited},
		{"服务繁忙，请稍后重试", FailRateLimited},
		{"系统繁忙", FailRateLimited},
		{"当前负载过高", FailRateLimited},
		{"请求过多，请稍后重试", FailRateLimited},
		{"并发超额", FailRateLimited},
		// Chinese balance/quota/auth texts → model class (switch)
		{"账户余额不足", FailExitModel},
		{"您的账户已欠费", FailExitModel},
		{"额度已用尽", FailExitModel},
		{"配额不足", FailExitModel},
		{"已超出额度", FailExitModel},
		{"账户已被停用", FailExitModel},
		{"鉴权失败", FailExitModel},
		{"认证失败：无效的 API Key", FailExitModel},
		{"API Key 无效", FailExitModel},
		{"无权限访问该模型", FailExitModel},
		{"该模型无权限", FailExitModel},
		{"API 权限不足", FailExitModel},
		// a bare permission phrase is a local error, not allowance; the
		// Chinese EACCES phrasings stay host-class
		{"无法打开文件: 权限不足", FailExitOther},
		{"open /tmp/x: 权限不够", FailExitHost},
		{"写入失败：权限被拒绝", FailExitHost},
		{"拒绝访问", FailExitHost},
		// allowance wins over throttle when both appear
		{"余额不足；请求过于频繁", FailExitModel},
		// Chinese negatives: no bare single-character match
		{"参数错误：消息格式不正确", FailExitOther},
		{"限制条件不满足", FailExitOther},
		// negatives: not upstream transient
		{"Invalid tool name: foo", FailExitOther},
		{"400 invalid request: invalid argument", FailExitOther},
		{"context length exceeded", FailExitOther},
		{"read transcript: unexpected EOF", FailExitOther},
	} {
		got, _, _ := classifyWaitFailure(&ExitedError{ExitCode: 1, LastError: tc.text})
		if got != tc.want {
			t.Errorf("%q classified %q, want %q", tc.text, got, tc.want)
		}
	}
	// the driver's own (non-exit) failures never classify transient
	for _, text := range []string{
		"read transcript: unexpected EOF",
		"stage envelope: connection closed",
		"read drop: incomplete JSON",
	} {
		if got, _, _ := classifyWaitFailure(errWithText(text)); got != FailExitOther {
			t.Errorf("driver-local %q classified %q, want %q", text, got, FailExitOther)
		}
	}
}
