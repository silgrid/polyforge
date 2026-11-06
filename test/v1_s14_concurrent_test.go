package test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// v1_s14: two work items run concurrently on one driver-pinned daemon.
// Each keeps its own capsule, its own execution state and its own
// server-side session and chain; neither driver sees the other's
// identity, and both complete and close cleanly. The isolation pins:
// execution.json names its own work item and its own step table, the
// archived envelopes carry their own wi_id, the sealed chains never
// cross, the spawn journals carry distinct session handles (T8), and the
// hub's public session audit blocks name two DIFFERENT fingerprints
// while both run (T8 — session isolation is per-WI, m4).
func TestV1S14ConcurrentTwoWI(t *testing.T) {
	inst := newV1Install(t)
	wiA := inst.newWI(t, "s14: concurrent member A", envelopeMicro(), nil)
	wiB := inst.newWI(t, "s14: concurrent member B", envelopeMicro(), nil)

	d := newV1Daemon(t, inst)

	// The concurrency proof is a hold, not a race: each member's first
	// step (fix) parks inside a scripted command until the test releases
	// it, so neither driver can finish — and leave the window — before
	// the other has gone live. A free run overlaps for only ~0.2 s under
	// load (each claim snapshots the lane's _shared tree into its
	// capsule, so B's job starts ~0.8 s after A's ~0.95 s run began),
	// which a 100 ms status poll missed about a third of the time.
	// Serialized drivers fail it outright: while the first member is
	// held, the second never reaches its first step.
	hold := t.TempDir()
	release := filepath.Join(hold, "release")
	// Registered after newV1Daemon, so it runs before the daemon stops:
	// a red run frees its holders instead of leaving them to the bound.
	t.Cleanup(func() { _ = os.WriteFile(release, nil, 0o644) })
	inst.setPlan(t, wiA, s14HeldMicro(hold, wiA))
	inst.setPlan(t, wiB, s14HeldMicro(hold, wiB))
	heldA, heldB := filepath.Join(hold, wiA+".held"), filepath.Join(hold, wiB+".held")
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }

	d.start()
	d.pf(t, "submit", wiA)
	d.pf(t, "submit", wiB)

	// Both drivers are live at the same time (the concurrency cap is 4):
	// both step processes sit inside their first step at once. Neither
	// can leave it before the release below, so this observation cannot
	// be missed by the poll interval.
	jobState := func() string {
		var parts []string
		for _, j := range d.status(t).Jobs {
			parts = append(parts, j.WorkItemID+"="+j.Status)
		}
		return strings.Join(parts, " ")
	}
	waitDeadline(t, 30*time.Second, func() string {
		return fmt.Sprintf("the two drivers never ran concurrently: both first steps are held until release, yet A held=%v B held=%v (jobs: %s)",
			exists(heldA), exists(heldB), jobState())
	}, func() bool { return exists(heldA) && exists(heldB) })
	// While both are held, the status face shows two running jobs, each
	// with a spawned step process (the spawn journal is the step-process
	// ledger — the driver lane carries no agent pid on the status face).
	waitDeadline(t, 10*time.Second, func() string {
		return "both members are held mid-step, yet the status face does not show both jobs running (jobs: " + jobState() + ")"
	}, func() bool {
		var a, b bool
		for _, j := range d.status(t).Jobs {
			a = a || (j.WorkItemID == wiA && j.Status == "running")
			b = b || (j.WorkItemID == wiB && j.Status == "running")
		}
		return a && b
	})
	waitSpawnCount(t, d, wiA, 1, 30*time.Second)
	waitSpawnCount(t, d, wiB, 1, 30*time.Second)

	// T8: while both run, the hub names two DISTINCT session
	// fingerprints — per-WI session isolation on the public audit block.
	sessA := inst.sessionOf(t, wiA)
	sessB := inst.sessionOf(t, wiB)
	if sessA.Fingerprint == "" || sessB.Fingerprint == "" {
		t.Fatalf("a concurrent member carries no session: %+v %+v", sessA, sessB)
	}
	if sessA.Fingerprint == sessB.Fingerprint {
		t.Fatalf("the two concurrent work items share one session: %q", sessA.Fingerprint)
	}
	if err := os.WriteFile(release, nil, 0o644); err != nil {
		t.Fatal(err)
	}

	// Each watch snapshot shows its own step table, never the peer's.
	hasSteps := func(wi string) bool {
		x := d.watch(t, wi).Execution
		_, fix := x.Steps["fix"]
		_, review := x.Steps["review"]
		return fix && review
	}
	waitUntil(t, 30*time.Second, "A's steps never showed", func() bool { return hasSteps(wiA) })
	waitUntil(t, 30*time.Second, "B's steps never showed", func() bool { return hasSteps(wiB) })
	if s := d.watch(t, wiA); s.WorkItem != wiA {
		t.Fatalf("watch snapshot of A names %q", s.WorkItem)
	}
	if s := d.watch(t, wiB); len(s.Execution.Steps) != 2 {
		t.Fatalf("watch snapshot of B crossed streams: %+v", s.Execution.Steps)
	}

	// Both complete and close cleanly (the driver's completed exit is the
	// wrap signal).
	inst.waitWIStatus(t, wiA, "wrapped", 90*time.Second)
	inst.waitWIStatus(t, wiB, "wrapped", 90*time.Second)

	// The execution faces are independent: each workflow completed with
	// its own step table and its own archived envelopes, never the
	// peer's identity anywhere.
	for _, wi := range []string{wiA, wiB} {
		waitWorkflowStatus(t, d, wi, "completed", 30*time.Second)
		x := readExecution(t, d.capsuleDir(wi))
		if len(x.Steps) != 2 {
			t.Fatalf("%s: step table crossed streams: %v", wi, x.Steps)
		}
		if h := acceptedHandoff(t, d.capsuleDir(wi), "fix", 1); h.Handoff.Status != stepcall.StatusDone {
			t.Fatalf("%s: fix handed off %s, want done", wi, h.Handoff.Status)
		}
		doc, err := os.ReadFile(filepath.Join(d.capsuleDir(wi), "artifacts", "handoff.md"))
		if err != nil || !strings.Contains(string(doc), "`"+wi+"`") {
			t.Fatalf("%s: its handoff.md does not name it (%v)", wi, err)
		}
		exec := inst.bareFileAtMain(t, wi, "runtime-state/execution.json")
		if !strings.Contains(exec, wi) {
			t.Fatalf("chain of %s carries foreign state:\n%s", wi, exec)
		}
		if strings.Contains(exec, wiA) && wi != wiA {
			t.Fatalf("chain of %s carries A's state:\n%s", wi, exec)
		}
		if inst.bareCommitCount(t, wi) == 0 {
			t.Fatalf("%s sealed an empty chain", wi)
		}
	}

	// The two capsules' spawn journals carry distinct PUBLIC session
	// handles (v1.5: the v1 synthetic "session_id" value is deleted from
	// the journal — B3, the secret-class name must never land in the
	// capsule; the handle is the fingerprint).
	sa := spawnJournal(t, d.capsuleDir(wiA))
	sb := spawnJournal(t, d.capsuleDir(wiB))
	if len(sa) == 0 || len(sb) == 0 {
		t.Fatal("spawn journals missing")
	}
	if sa[0]["session_handle"] == sb[0]["session_handle"] {
		t.Fatalf("the two capsules share a session handle: %v", sa[0]["session_handle"])
	}
	if sa[0]["session_id"] != nil || sb[0]["session_id"] != nil {
		t.Fatal("the spawn journal still carries a session_id value (B3: the secret-class name must never ride the capsule)")
	}
}

// s14HeldMicro is envelopeMicro with the first step (fix) held: its
// scripted command marks <hold>/<wi>.held, then waits for <hold>/release.
// The wait is bounded (60 s) so a red run never leaves a waiter behind.
func s14HeldMicro(hold, wi string) []map[string]any {
	held := shellQuote(filepath.Join(hold, wi+".held"))
	release := shellQuote(filepath.Join(hold, "release"))
	plan := envelopeMicro()
	plan[0]["run"] = []string{"touch " + held + "; i=0; while [ ! -e " + release + " ] && [ $i -lt 1200 ]; do sleep 0.05; i=$((i+1)); done"}
	return plan
}
