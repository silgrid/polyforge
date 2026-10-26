package test

// w164_output_schema_record_test.go: polyforge#164 H3 end to end, in the
// v2.5.0 record-only mode. The shipped autopilot bundle runs with its
// step output schemas in place; the fake harness's placeholder outputs
// violate them, so the daemon's event log records output_schema_violation
// for the steps, and the work item still wraps (nothing is refused).

import (
	"strings"
	"testing"
	"time"
)

func TestW164OutputSchemaViolationsRecordedNotRefused(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "w164: placeholder outputs are recorded against the schemas", envelopeAutopilot(), nil)
	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)

	waitWorkflowStatus(t, d, wi, "completed", 120*time.Second)
	ev := waitForEvent(t, d, wi, "output_schema_violation", 30*time.Second)
	if !strings.Contains(string(ev.Body), "declared output schema") {
		t.Fatalf("the violation must carry the validation errors: %s", ev.Body)
	}
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
}
