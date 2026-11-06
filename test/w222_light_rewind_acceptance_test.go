package test

// w222_light_rewind_acceptance_test.go: the scripted head of the vehicle
// lane (micro: fix, then the review gate) and the execution-ledger readers
// the review-gate scenarios share (w29, w67). Its own light-rewind
// scenarios drove only the retired lanes and are deleted (owner
// 2026-11-09: deprecated lanes retired, their tests are not maintained).

import (
	"strings"
	"testing"
	"time"
)

func w222Env(id string) map[string]any {
	return map[string]any{"do": "envelope", "id": id, "outcome": "completed"}
}

// w222MicroHead is the vehicle lane up to (excluding) its review: fix.
func w222MicroHead() []map[string]any {
	return []map[string]any{w222Env("fix")}
}

// w222RunPi runs one vehicle-lane work item on the pi fake until the
// workflow leaves running (or the bound expires) and answers its capsule dir.
func w222RunPi(t *testing.T, goal string, plan []map[string]any) string {
	t.Helper()
	inst, _ := newV1InstallTaskRepo(t)
	wi := newWIWithBundle(t, inst, goal, vehicleLane, plan)
	d := newV1Daemon(t, inst)
	d.start()
	if out := d.pf(t, "submit", wi); !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}
	capDir := d.capsuleDir(wi)
	w222WaitSettled(t, capDir)
	return capDir
}

// w222WaitSettled waits until the workflow is no longer running.
func w222WaitSettled(t *testing.T, capDir string) {
	t.Helper()
	waitExecution(t, capDir, 180*time.Second, "the workflow never settled", func(x *executionView) bool {
		return x.WorkflowStatus != "" && x.WorkflowStatus != "running"
	})
}

// w222Steps answers the spawn journal's entries of one step, in order.
func w222Steps(t *testing.T, capDir, step string) []map[string]any {
	var out []map[string]any
	for _, e := range w159SpawnJournal(t, capDir) {
		if e["step_id"] == step {
			out = append(out, e)
		}
	}
	return out
}

// w222Order answers the spawn journal's step ids in dispatch order.
func w222Order(t *testing.T, capDir string) []string {
	var out []string
	for _, e := range w159SpawnJournal(t, capDir) {
		s, _ := e["step_id"].(string)
		out = append(out, s)
	}
	return out
}
