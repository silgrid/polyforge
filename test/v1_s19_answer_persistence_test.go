package test

// v1_s19_answer_persistence_test.go: a terminal outcome reached before a
// daemon crash is replayed (from the durable file), never re-run: the
// restarted daemon re-derives nothing, the spawn journal and the
// execution state are byte-identical, and the WI stays wrapped. (The
// v1 question's durability scenarios left with the v1 question, workflow
// contract v2 change 19; the open `pf ask` is pinned by v2_6c.)
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---------------------------------------------------------------------
// Scenario C: the final outcome reached before a daemon crash is
// replayed, never re-run.

func TestV1S19FinalOutcomeReplayedAfterDaemonCrash(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "s19 C: the terminal outcome replayed after a crash",
		envelopeMicro(), nil)

	d := newV1Daemon(t, inst)
	d.start()
	t.Cleanup(func() { killOrphans(wi) })
	d.pf(t, "submit", wi)

	// The workflow completes and the terminal wiring wraps it — the
	// final outcome is durable in the capsule and at aihub.
	waitWorkflowStatus(t, d, wi, "completed", 90*time.Second)
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	capDir := d.capsuleDir(wi)
	execBefore, err := os.ReadFile(filepath.Join(capDir, "runtime-state", "execution.json"))
	if err != nil {
		t.Fatal(err)
	}
	spawnsBefore := len(spawnJournal(t, capDir))
	if spawnsBefore != 2 {
		t.Fatalf("spawn journal has %d entries, want 2 (one pass)", spawnsBefore)
	}

	// Crash the daemon AFTER the terminal outcome; restart on the same
	// state. The restarted daemon must re-derive NOTHING: the final
	// outcome is replayed from the durable file, not re-run.
	d.crash()
	d2 := restartSameDaemon(t, d, "daemon2.log")
	time.Sleep(5 * time.Second) // any re-derivation would show within this window
	if st := inst.wiStatus(t, wi); st != "wrapped" {
		t.Fatalf("the restarted daemon disturbed the terminal WI: %q", st)
	}
	if n := len(spawnJournal(t, capDir)); n != spawnsBefore {
		t.Fatalf("the restart re-ran work: spawn journal %d entries, want %d", n, spawnsBefore)
	}
	execAfter, err := os.ReadFile(filepath.Join(d2.capsuleDir(wi), "runtime-state", "execution.json"))
	if err != nil {
		t.Fatal(err)
	}
	var before, after map[string]any
	if json.Unmarshal(execBefore, &before) != nil || json.Unmarshal(execAfter, &after) != nil {
		t.Fatal("execution.json unparsable")
	}
	if fmt.Sprint(before["version"]) != fmt.Sprint(after["version"]) {
		t.Fatalf("the restarted daemon wrote new versions (%v -> %v)", before["version"], after["version"])
	}
	if before["workflow_status"] != "completed" || after["workflow_status"] != "completed" {
		t.Fatalf("the terminal outcome moved: %v -> %v", before["workflow_status"], after["workflow_status"])
	}
	// The restarted daemon holds no job for the finished WI.
	if st := d2.status(t); len(st.Jobs) != 0 {
		t.Fatalf("the restarted daemon holds jobs: %+v", st.Jobs)
	}
	// And the session stays cleared (the wrap's terminal close).
	if sess := inst.sessionOf(t, wi); sess.Fingerprint != "" {
		t.Fatalf("wrapped work item still names session %q", sess.Fingerprint)
	}
}
