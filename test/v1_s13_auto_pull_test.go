package test

import (
	"strings"
	"testing"
	"time"
)

// v1_s13_auto_pull_test.go: auto-pull, driver/envelope edition. The daemon
// (booted with --auto) autonomously claims the
// project's ready queue in waves and runs every ticket to completion with
// no operator action — each member a driver-pinned autopilot workflow,
// completed and sealed by the terminal wiring. An explicit `pf submit`
// stays legal while auto is on, and `pf ops auto off` stops further
// pulls.
func TestV1S13AutoPull(t *testing.T) {
	inst := newV1Install(t)
	wiA := inst.newWI(t, "s13: auto-pull member A", envelopeAutopilot(), nil)
	wiB := inst.newWI(t, "s13: auto-pull member B", envelopeAutopilot(), nil)
	wiC := inst.newWI(t, "s13: auto-pull member C", envelopeAutopilot(), nil)

	d := newV1Daemon(t, inst, optAuto())
	d.start()

	if st := d.status(t); !st.Auto {
		t.Fatalf("--auto did not turn auto-pull on: %+v", st)
	}

	// The auto waves claim the ready queue on their own (wave period 5s)
	// and run every member to wrapped.
	inst.waitWIStatus(t, wiA, "wrapped", 120*time.Second)
	inst.waitWIStatus(t, wiB, "wrapped", 120*time.Second)
	inst.waitWIStatus(t, wiC, "wrapped", 120*time.Second)

	// Each member ran on this daemon as a driver workflow: the
	// execution.json face shows the completed autopilot chain, and the
	// auto-seal pushed the chain server-side.
	for _, wi := range []string{wiA, wiB, wiC} {
		waitWorkflowStatus(t, d, wi, "completed", 30*time.Second)
		x := readExecution(t, d.capsuleDir(wi))
		for _, step := range []string{"grill", "fix", "verification"} {
			if st := x.Steps[step]; st == nil || st.Status != "done" {
				t.Fatalf("%s: step %s not done: %+v", wi, step, st)
			}
		}
		if inst.bareCommitCount(t, wi) == 0 {
			t.Fatalf("%s never pushed its chain", wi)
		}
	}

	// A manual submit is legal while auto is on (no DAEMON-409).
	wiD := inst.newWI(t, "s13: manual submit while auto is on", envelopeAutopilot(), nil)
	if out := d.pf(t, "submit", wiD); strings.Contains(out, "DAEMON-409") {
		t.Fatalf("manual submit refused while auto is on: %q", out)
	}
	inst.waitWIStatus(t, wiD, "wrapped", 120*time.Second)

	// The queue drained to empty; auto switches off through the CLI.
	st := d.status(t)
	if !st.Auto || st.Queue.Pending != 0 || len(st.Jobs) != 0 {
		t.Fatalf("queue not drained / auto lost: %+v", st)
	}
	d.pf(t, "ops", "auto", "off")
	if st := d.status(t); st.Auto {
		t.Fatalf("auto still on after `pf ops auto off`: %+v", st)
	}
}
