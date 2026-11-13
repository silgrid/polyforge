package daemon

// status_retry_test.go: the ops status face projects a pending retry
// backoff from the job's execution.json (the attempts ledger, read through
// driver.RetryOwed): seconds left and the death cause that owes the wait.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

// writeFailedStep writes an execution.json whose current step's newest
// n attempts are infra failures of cause, the newest interrupted at
// failedAt, with no dispatch since.
func writeFailedStep(t *testing.T, capDir, cause string, n int, failedAt time.Time) {
	t.Helper()
	started := failedAt.Add(-3 * time.Minute).UTC().Format(time.RFC3339Nano)
	var attempts []capsule.Attempt
	for i := range n {
		attempts = append(attempts, capsule.Attempt{
			Cause: driver.CauseInfraFailure, FailureCause: cause, AttemptSeq: i + 1,
			StartedAt: started, InterruptedAt: failedAt.UTC().Format(time.RFC3339Nano),
		})
	}
	ex := capsule.Execution{
		WorkflowStatus: capsule.WorkflowStatusRunning, CurrentStep: "fix",
		Steps: map[string]*capsule.Step{"fix": {
			Status: capsule.StepInProgress, StepStartedAt: started,
			Attempts: attempts,
		}},
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Version:   2,
	}
	blob, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(capDir, capsule.ExecutionJSON), blob, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestStatusJobsProjectPendingRetry(t *testing.T) {
	td := newTestDaemon(t, nil)
	td.submitClaim(t, "wi_retry")
	ji, ok := td.rt.Info("wi_retry")
	if !ok || ji.CapsuleDir == "" {
		t.Fatal("the claim must run to a live job with a capsule")
	}
	job := func() statusJob {
		jobs, _ := td.statusJobs()
		for _, j := range jobs {
			if j.WorkItemID == "wi_retry" {
				return j
			}
		}
		t.Fatalf("job wi_retry missing from %+v", jobs)
		return statusJob{}
	}

	// the seventh throttle in a row, 20s ago, owes 44s more of its 64s
	// wait (the transient backoff: 1s doubling, design #483 §7)
	writeFailedStep(t, ji.CapsuleDir, driver.FailRateLimited, 7, time.Now().Add(-20*time.Second))
	j := job()
	if j.RetryInSec == nil || *j.RetryInSec < 39 || *j.RetryInSec > 44 || j.RetryCause != driver.FailRateLimited {
		t.Fatalf("job %+v, want about 44s left on a rate_limited retry", j)
	}

	// the wait ran out: nothing shown
	writeFailedStep(t, ji.CapsuleDir, driver.FailRateLimited, 7, time.Now().Add(-2*time.Minute))
	if j := job(); j.RetryInSec != nil || j.RetryCause != "" {
		t.Fatalf("job %+v, want no pending retry once the wait ran out", j)
	}

	// a cause that owes no wait: nothing shown
	writeFailedStep(t, ji.CapsuleDir, driver.FailExitOther, 1, time.Now())
	if j := job(); j.RetryInSec != nil {
		t.Fatalf("job %+v, want no pending retry for exit_other", j)
	}
}
