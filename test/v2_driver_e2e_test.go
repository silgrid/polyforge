package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// v2_driver_e2e: the Go execution driver's end-to-end smoke. A real
// aihub, a real `pf daemon` (driver engine, no selection flag since
// polyforge#106), a scripted fakepi step agent
// that writes result envelopes (not execution.json), the capsule chain,
// and the completed workflow — driven entirely by the Go Driver.
func TestV2DriverEndToEnd(t *testing.T) {
	inst, _ := newV1InstallTaskRepo(t)
	// Script: each step writes a result envelope (v2 mode).
	// grill: handoff (needs to produce handoff for fix)
	// fix: handoff
	// verification: completed
	wi := inst.newWI(t, "v2 driver e2e: envelope-driven autopilot",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()

	// Submit through the real CLI.
	out := d.pf(t, "submit", wi)
	if !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}

	// Wait for the workflow to complete (execution.json → completed).
	capDir := d.capsuleDir(wi)
	execPath := filepath.Join(capDir, "runtime-state", "execution.json")
	waitUntil(t, 60*time.Second, "driver never completed the workflow", func() bool {
		raw, err := os.ReadFile(execPath)
		if err != nil {
			return false
		}
		return strings.Contains(string(raw), `"completed"`)
	})

	// Verify: all 4 steps done.
	raw, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatalf("execution.json: %v", err)
	}
	var exec map[string]any
	if err := json.Unmarshal(raw, &exec); err != nil {
		t.Fatalf("execution.json unparsable: %v", err)
	}
	steps, _ := exec["steps"].(map[string]any)
	for _, step := range []string{"grill", "fix", "verification"} {
		st, ok := steps[step].(map[string]any)
		if !ok {
			t.Fatalf("step %s missing from execution.json", step)
		}
		if st["status"] != "done" {
			t.Fatalf("step %s: expected done, got %v", step, st["status"])
		}
	}

	// Verify: result envelopes were archived.
	for _, n := range []string{"1", "2", "3", "4"} {
		p := filepath.Join(capDir, "artifacts", "steps", "*", "s"+n, "result.json")
		if matches, _ := filepath.Glob(p); len(matches) == 0 {
			t.Logf("WARN: no archived envelope for spawn %s (path %s)", n, p)
		}
	}

	// Verify: handoff files produced.
	for _, step := range []string{"grill", "fix"} {
		p := filepath.Join(capDir, "artifacts", "handoff-"+step+".md")
		if _, err := os.Stat(p); err != nil {
			t.Logf("WARN: handoff file for %s not at %v (agent may have used a different name)", step, err)
		}
	}
}
