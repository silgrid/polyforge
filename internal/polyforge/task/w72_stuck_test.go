package task

// w72_stuck_test.go (polyforge#72, u3 — AC9): the task lane's stuck
// detection. Every task (not just an analysis task) is judged by the step
// lane's silence and tool-hung rules — rule 0 (harness_silent), rule 2
// (model_hung), rule 3 (tool_hung) only, the task-appropriate subset
// (rulings 5/15); a judged-stuck candidate is killed and the manager
// switches to the next candidate inside the remaining budget; claude
// tasks carry --include-partial-messages so the silence bound is judged
// at token granularity, exactly like a step. The fake model below is
// the ticket's own proof shape: a harness that only starts and never
// outputs.

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/harness"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// TestW72ExecRunnerSilentModelIsJudgedStuck: a harness that starts and
// never outputs is killed at the silence bound and answers StuckError
// with the harness_silent cause — the fake-model proof AC9 names.
func TestW72ExecRunnerSilentModelIsJudgedStuck(t *testing.T) {
	r := fakePi(t, "cat >/dev/null\nsleep 30\n")
	spec := piSpec(t, 100000)
	spec.Limits = supervise.Defaults{Silence: 300 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	_, err := r.Run(ctx, spec, nil)
	var stuck *StuckError
	if !errors.As(err, &stuck) {
		t.Fatalf("a silent harness must answer a StuckError, got %v", err)
	}
	if stuck.Cause != supervise.CauseHarnessSilent {
		t.Fatalf("cause = %s, want harness_silent (rule 0: no work-class signal)", stuck.Cause)
	}
	if !strings.Contains(stuck.Evidence, "no work-class signal") {
		t.Errorf("evidence must say what fired: %q", stuck.Evidence)
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("the silence verdict must kill the process promptly (took %s)", time.Since(start))
	}
}

// TestW72ExecRunnerWaitingModelIsJudgedHung: rule 2 — a harness that
// began a model call and then went silent is model_hung.
func TestW72ExecRunnerWaitingModelIsJudgedHung(t *testing.T) {
	r := fakePi(t, "cat >/dev/null\n"+
		"echo '{\"type\":\"turn_start\"}'\n"+
		"sleep 30\n")
	spec := piSpec(t, 100000)
	spec.Limits = supervise.Defaults{Silence: 300 * time.Millisecond}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, err := r.Run(ctx, spec, nil)
	var stuck *StuckError
	if !errors.As(err, &stuck) || stuck.Cause != supervise.CauseModelHung {
		t.Fatalf("a waiting-then-silent harness must answer StuckError model_hung, got %v", err)
	}
}

// TestW72TaskClaudeArgvCarriesPartialMessages: the claude task argv adds
// --include-partial-messages (ruling 14), so token deltas reach the
// tracker and the silence bound is the token-level one.
func TestW72TaskClaudeArgvCarriesPartialMessages(t *testing.T) {
	r := &ExecRunner{}
	cc, _ := harness.ParseCandidate("claude:m")
	ca, err := r.innerArgv(cc, false)
	if err != nil {
		t.Fatal(err)
	}
	if !containsSeq(ca, "--include-partial-messages") {
		t.Fatalf("claude task argv must carry --include-partial-messages (ruling 14): %v", ca)
	}
}

// stuckThenDone answers StuckError on the first candidate and completes
// on the second — the rotation shape AC9 names.
type stuckThenDone struct {
	calls int
	specs []RunSpec
}

func (s *stuckThenDone) Run(_ context.Context, spec RunSpec, _ func(int64)) (RunResult, error) {
	s.specs = append(s.specs, spec)
	s.calls++
	if s.calls == 1 {
		return RunResult{Tokens: 3}, &StuckError{Cause: supervise.CauseHarnessSilent, Evidence: "no work-class signal within 300ms of dispatch"}
	}
	return RunResult{Report: "done on the second", Tokens: 5}, nil
}

func TestW72ManagerRotatesCandidateOnStuck(t *testing.T) {
	r := &stuckThenDone{}
	m, terms := newTestManager(t, r, []string{"pi:a/one", "pi:b/two"})
	tk, err := m.Spawn(Spec{Project: "p", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	done := waitState(t, m, tk.ID, StateCompleted)
	if done.Report != "done on the second" {
		t.Fatalf("the second candidate must complete the task: %+v", done)
	}
	if len(done.Attempts) != 1 || done.Attempts[0].Candidate != "pi:a/one" ||
		done.Attempts[0].Cause != string(supervise.CauseHarnessSilent) ||
		!strings.Contains(done.Attempts[0].Evidence, "no work-class signal") {
		t.Fatalf("the stuck attempt must be recorded on the task: %+v", done.Attempts)
	}
	if r.calls != 2 || len(r.specs) != 2 || r.specs[1].Candidate.String() != "pi:b/two" {
		t.Fatalf("the run must switch to the next candidate: calls=%d specs=%v", r.calls, r.specs)
	}
	if done.TokensConsumed != 8 {
		t.Fatalf("the stuck candidate's tokens stay spent: %d", done.TokensConsumed)
	}
	if terms() != 1 {
		t.Fatalf("OnTerminal calls = %d, want 1", terms())
	}
}

// TestW72StuckCauseVisibleInSummary: the summary — the decision feed's
// task text — names the stuck candidate and its cause.
func TestW72StuckCauseVisibleInSummary(t *testing.T) {
	tk := Task{ID: "task_0123456789abcdef", State: StateFailed,
		Attempts: []Attempt{{Candidate: "pi:a/one", Cause: string(supervise.CauseHarnessSilent), Evidence: "e"}}}
	s := tk.Summary()
	if !strings.Contains(s, "pi:a/one") || !strings.Contains(s, "harness_silent") {
		t.Fatalf("the summary must name the stuck candidate and cause: %q", s)
	}
}
