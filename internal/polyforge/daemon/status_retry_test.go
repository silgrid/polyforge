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
// attempt is an infra failure of cause, interrupted at failedAt, with no
// dispatch since.
func writeFailedStep(t *testing.T, capDir, cause string, failedAt time.Time) {
	t.Helper()
	started := failedAt.Add(-3 * time.Minute).UTC().Format(time.RFC3339Nano)
	ex := capsule.Execution{
		WorkflowStatus: capsule.WorkflowStatusRunning, CurrentStep: "fix",
		Steps: map[string]*capsule.Step{"fix": {
			Status: capsule.StepInProgress, StepStartedAt: started,
			Attempts: []capsule.Attempt{{
				Cause: driver.CauseInfraFailure, FailureCause: cause, AttemptSeq: 1,
				StartedAt: started, InterruptedAt: failedAt.UTC().Format(time.RFC3339Nano),
			}},
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

	// a model_error 20s ago owes 40s more of its 1m wait
	writeFailedStep(t, ji.CapsuleDir, driver.FailModelError, time.Now().Add(-20*time.Second))
	j := job()
	if j.RetryInSec == nil || *j.RetryInSec < 35 || *j.RetryInSec > 40 || j.RetryCause != driver.FailModelError {
		t.Fatalf("job %+v, want about 40s left on a model_error retry", j)
	}

	// the wait ran out: nothing shown
	writeFailedStep(t, ji.CapsuleDir, driver.FailModelError, time.Now().Add(-2*time.Minute))
	if j := job(); j.RetryInSec != nil || j.RetryCause != "" {
		t.Fatalf("job %+v, want no pending retry once the wait ran out", j)
	}

	// a cause that owes no wait: nothing shown
	writeFailedStep(t, ji.CapsuleDir, driver.FailExitOther, time.Now())
	if j := job(); j.RetryInSec != nil {
		t.Fatalf("job %+v, want no pending retry for exit_other", j)
	}
}
