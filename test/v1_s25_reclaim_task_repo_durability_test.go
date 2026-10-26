package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// v1_s25: bounded-fail → unblock → re-claim loses the task-repo work
// (polyforge#108, observed on #107). The capsule git chain deliberately
// carries EXECUTION state only — repos/ is gitignored and the task
// repo's own origin is the task repo remote — so the code step's commits
// exist nowhere durable. A fresh re-claim (the unblock path: the old
// capsule is quarantined, the capsule chain is re-cloned, the task repo
// is re-cloned from its origin) resumes the workflow with a tree that
// contains NONE of the code step's implementation while execution.json
// (restored from the chain) still records the code step done.
//
// The scenario is the acceptance shape of the work item, inverted into
// the failing oracle: drive a real code commit, drive a real
// bounded-fail verdict (the storm guard: the verification step dies
// without an envelope, three rapid recoveries), unblock through the
// real CLI, re-submit through the real CLI — then the re-claimed
// capsule's repos/demo-src must stand at the SAME commit the dead
// session left. Today it does not: the worktree is a bare re-clone of
// the origin (no work.txt, HEAD == origin main), and the work survives
// only inside the quarantined capsule.
func TestV1S25ReclaimTaskRepoNotDurable(t *testing.T) {
	inst, remote := newV1InstallTaskRepo(t)
	wi := inst.newWI(t, "s25: bounded-fail -> unblock -> re-claim loses the task-repo work",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			// The code step: a real commit on top of the claim-time clone
			// of demo-src (makeRepo commits work.txt locally; nothing is
			// pushed to the task repo's origin — the deliver step is the
			// only push lane, and this run never delivers).
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true,
				"name": "demo-src", "origin": remote.url},
			// The verification step dies BEFORE its envelope write, three
			// dispatches running: three rapid recovery re-entries inside
			// the stability window trip the storm guard — the bounded-fail
			// verdict (SessionEnd(blocked), WI blocked, the owner holds
			// the next move).
			{"do": "envelope", "id": "verification", "outcome": "completed", "crash": "before"},
			{"do": "envelope", "id": "verification", "outcome": "completed", "crash": "before"},
			{"do": "envelope", "id": "verification", "outcome": "completed", "crash": "before"},
		}, nil)
	t.Cleanup(func() { killOrphans(wi) })

	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)

	// The dead session's scene: the crash loop burned the recovery
	// budget and the WI landed in the blocked (bounded-fail) state.
	inst.waitWIStatus(t, wi, "blocked", 180*time.Second)

	// The code work exists in the dead session's capsule: a real commit
	// on top of the claim-time clone, recorded in execution.json.
	capA := d.capsuleDir(wi)
	repoA := filepath.Join(capA, "repos", "demo-src")
	codeHead := strings.TrimSpace(gitRun(t, repoA, "rev-parse", "HEAD"))
	if codeHead == "" || codeHead == remote.head {
		t.Fatalf("the code step left no commit: codeHead %q, origin main %q", codeHead, remote.head)
	}
	if b, err := os.ReadFile(filepath.Join(repoA, "work.txt")); err != nil || !strings.Contains(string(b), wi) {
		t.Fatalf("the code step's work.txt is missing from the dead session's capsule: %q, %v", b, err)
	}
	x := readExecution(t, capA)
	if st := x.Steps["fix"]; st == nil || st.Status != "done" {
		t.Fatalf("dead-session execution.json: fix step %+v, want done", st)
	}
	if st := x.Steps["verification"]; st == nil || st.Status != "in_progress" {
		t.Fatalf("dead-session execution.json: verification step %+v, want in_progress (the bounded-fail mid-flight shape)", st)
	}
	preSpawns := len(spawnJournal(t, capA))
	t.Logf("dead session: code head %s (origin main %s), fix done, verification in_progress, %d dispatches",
		codeHead, remote.head, preSpawns)

	// The capsule chain (the recovery commit-dirty pushes) carries the
	// execution state the re-claim will restore.
	if !inst.bareHasPath(t, wi, "runtime-state/execution.json") {
		t.Fatal("the capsule chain does not carry execution.json — the re-claim could not even restore the execution state")
	}

	// The human's half of the contract: continue the bounded-fail verdict
	// (`pf resume` = the blocked door plus the synchronous re-claim;
	// v2-converge §7).
	out := d.pf(t, "resume", wi)
	if !strings.Contains(out, "resumed") {
		t.Fatalf("resume answer: %q", out)
	}

	// The re-claim quarantined the dead session's capsule and
	// re-materialized a fresh one at the same path, with a FRESH clone
	// of the task repo from its origin.
	waitUntil(t, 30*time.Second, "the dead session's capsule was never quarantined", func() bool {
		ents, err := os.ReadDir(filepath.Dir(capA))
		if err != nil {
			return false
		}
		for _, e := range ents {
			if strings.HasPrefix(e.Name(), wiPathName(t, inst, wi)+".quarantined.") {
				return true
			}
		}
		return false
	})
	waitUntil(t, 90*time.Second, "the re-claimed capsule never re-materialized the task repo", func() bool {
		st, err := os.Stat(filepath.Join(d.capsuleDir(wi), "repos", "demo-src", ".git"))
		return err == nil && st.IsDir()
	})
	// The resumed workflow re-dispatched the verification step on the
	// re-claimed tree (the journal rides the chain; the new dispatch
	// grows past the restored ordinals).
	waitSpawnCount(t, d, wi, preSpawns+1, 120*time.Second)

	// The oracle: the re-claimed task repo must stand at the commit the
	// dead session left (the acceptance criterion of polyforge#108).
	repoB := filepath.Join(d.capsuleDir(wi), "repos", "demo-src")
	reclaimedHead := strings.TrimSpace(gitRun(t, repoB, "rev-parse", "HEAD"))
	workB, workErr := os.ReadFile(filepath.Join(repoB, "work.txt"))
	x2 := readExecution(t, d.capsuleDir(wi))
	fixAfter := x2.Steps["fix"]

	// Observed state, for the record (the repro document quotes these).
	t.Logf("re-claimed session: repos/demo-src HEAD %s (origin main %s, dead session left %s); work.txt: %q (%v); execution.json fix step: %s",
		reclaimedHead, remote.head, codeHead, workB, workErr, fixAfter.Status)

	if reclaimedHead != codeHead {
		t.Fatalf("task-repo state is not durable across the bounded-fail re-claim: the re-claimed repos/demo-src is at %s, want the dead session's code head %s (origin main is %s; work.txt present: %v) — while execution.json (restored from the capsule chain) still records the code step %s",
			reclaimedHead, codeHead, remote.head, workErr == nil, fixAfter.Status)
	}
	if workErr != nil {
		t.Fatalf("the re-claimed task repo lost the code step's work.txt: %v", workErr)
	}
}
