// carries: test/v1_inproc_harness_test.go, internal/polyforge/runtime/stepagentdir.go

package test

// polyforge84_inproc_step_agent_test.go (polyforge#84 fix build, verification
// r1 red no-untracked-test-files): every in-process scenario built by
// newInprocRig must keep the daemon-owned step agent directory under the
// rig's temp data dir, never in the checkout.
//
// The rig builds runtime.DefaultSpawner from a Config it keeps by pointer and
// then hands a COPY to runtime.New, whose fill() derives HarnessRoot from
// DataDir. With the rig's Config carrying no HarnessRoot, the spawner's
// StepAgentDir() degenerates to the relative "step-agent", and the test
// binary's cwd (test/) is written into — six untracked files after the bar.
// This guard starts one rig scenario and fails when the checkout gains the
// directory.

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestInprocRigKeepsStepAgentOutOfTheCheckout(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	leaked := filepath.Join(cwd, "step-agent")
	if _, err := os.Stat(leaked); err == nil {
		t.Fatalf("%s exists before the rig starts: the checkout must be clean for this guard", leaked)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", leaked, err)
	}

	inst := newV1Install(t)
	wi := inst.newWI(t, "s84: the inproc rig keeps step-agent out of the checkout",
		[]map[string]any{{"do": "envelope", "id": "fix", "outcome": "completed", "ms": 1000}}, nil)
	rig := newInprocRig(t, inst)
	t.Cleanup(func() { killOrphans(wi) })
	rig.start(wi)

	// Info answers only after the claim's spawn returned, so the step agent
	// directory (written before the process is born) would already exist.
	waitUntil(t, 60*time.Second, "the engine never started", func() bool {
		ji, ok := rig.rt.Info(wi)
		return ok && ji.Agent != nil
	})

	if _, err := os.Stat(leaked); err == nil {
		t.Fatalf("the inproc rig wrote the daemon-owned step agent dir into the checkout: %s", leaked)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", leaked, err)
	}
}
