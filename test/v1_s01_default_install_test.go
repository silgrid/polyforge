package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// v1_s01: default-install smoke, driver/envelope edition. A real aihub,
// a real `pf daemon` (the driver is the engine; polyforge#106 removed
// the selection flag), a real `pf submit`, a scripted
// envelope-action plan that walks the autopilot workflow (grill → fix →
// verification), and the completed workflow's faces: every
// step done in execution.json, every result envelope archived under
// artifacts/steps/<step>/s<n>/, the task-repo clone, the capsule git
// chain visible on the server side, the terminal wrapped state (the
// daemon's v2 terminal wiring translates the driver's completed exit
// into the wrap close — the recovery cause IS the completion signal),
// and the sealed chain carrying execution.json plus the session
// transcripts — plus the no-orphan invariant over the driver's step
// processes.
func TestV1S01DefaultInstallSmoke(t *testing.T) {
	inst, remote := newV1InstallTaskRepo(t)
	wi := inst.newWI(t, "s01: smoke the default install end to end",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()

	// Interactive submit through the real CLI.
	out := d.pf(t, "submit", wi)
	if !strings.Contains(out, "accepted") {
		t.Fatalf("submit answer: %q", out)
	}

	// The job runs: a live driver on a materialized capsule (the driver
	// lane carries no agent pid on the status face — the live job row is
	// the running probe).
	waitUntil(t, 30*time.Second, "the job never started running", func() bool {
		for _, j := range d.status(t).Jobs {
			if j.WorkItemID == wi && j.Status == "running" {
				return true
			}
		}
		return false
	})
	capDir := d.capsuleDir(wi)
	rawID, err := os.ReadFile(filepath.Join(capDir, "capsule.json"))
	if err != nil {
		t.Fatalf("capsule.json: %v", err)
	}
	var idnMap map[string]any
	if err := json.Unmarshal(rawID, &idnMap); err != nil {
		t.Fatalf("capsule.json unparsable: %v", err)
	}
	// v1.5 ASSERTION CHANGE: the v1 assertion (Generation==1, AttemptID!="")
	// pinned the deleted lineage; the fossil round removed even the zero
	// fields from the frozen Identity shape, so the correct pin is the
	// KEYS' ABSENCE — the runtime no longer writes generation/attempt_id
	// into capsule.json at all.
	for _, gone := range []string{"generation", "attempt_id"} {
		if _, ok := idnMap[gone]; ok {
			t.Fatalf("capsule.json still carries the deleted v1 lineage key %q: %v", gone, idnMap)
		}
	}
	if idnMap["work_item_id"] != wi || idnMap["bundle_name"] != "autopilot" {
		t.Fatalf("capsule identity wrong: %v", idnMap)
	}
	// T1 session trail, first half: the claim set the hub's session row
	// — the public audit block names this machine and a fingerprint,
	// never a hash and never the secret.
	sess := inst.sessionOf(t, wi)
	if sess.Fingerprint == "" || sess.Machine != "m-e2e" {
		t.Fatalf("running work item carries no session block: %+v", sess)
	}
	// The claim's task-repo inventory was cloned before the driver's
	// first step process spawned: repos/demo-src is a real clone of the
	// local fake remote, its own .git, the remote's content, and the
	// claim-time HEAD recorded as the capsule baseline.
	repoDir := filepath.Join(capDir, "repos", "demo-src")
	if _, err := os.Stat(filepath.Join(repoDir, ".git")); err != nil {
		t.Fatalf("task repo not cloned into the capsule: %v", err)
	}
	if b, err := os.ReadFile(filepath.Join(repoDir, "hello.txt")); err != nil || string(b) != "hello\n" {
		t.Fatalf("cloned task repo content wrong: %q, %v", b, err)
	}
	repos, _ := idnMap["repos"].(map[string]any)
	if got, _ := repos["demo-src"].(string); got != remote.head {
		t.Fatalf("capsule baseline for demo-src = %q, want remote HEAD %q", got, remote.head)
	}
	// The task repo stays outside the capsule's own git history.
	if inst.bareHasPath(t, wi, "repos") {
		t.Fatal("capsule chain must not carry repos/ (gitignored, own .git)")
	}
	// The materialized bundle rides the capsule.
	if _, err := os.Stat(filepath.Join(capDir, "workflows", "autopilot", "workflow.md")); err != nil {
		t.Fatalf("materialized bundle missing: %v", err)
	}

	// The driver walks the workflow: every step done on the
	// execution.json face, with the budget_consumed and Version faces
	// present (the driver is execution.json's single writer).
	waitWorkflowStatus(t, d, wi, "completed", 60*time.Second)
	x := readExecution(t, capDir)
	if x.Version < 4 {
		t.Fatalf("execution.json version %d — the workflow-version progression face is absent", x.Version)
	}
	if x.BudgetConsumed == nil {
		t.Fatal("execution.json carries no budget_consumed face")
	}
	for _, step := range []string{"grill", "fix", "verification"} {
		st := x.Steps[step]
		if st == nil || st.Status != "done" {
			t.Fatalf("step %s: %+v, want done", step, st)
		}
	}
	// The envelope face: every step's collected result envelope is
	// archived in the R14 staging location, in spawn order.
	for i, step := range []string{"grill", "fix", "verification"} {
		env := archivedEnvelope(t, capDir, step, i+1)
		if env["step_id"] != step || env["outcome"] != "completed" {
			t.Fatalf("archived envelope of %s wrong: %v", step, env)
		}
		if int(env["spawn_n"].(float64)) != i+1 {
			t.Fatalf("archived envelope of %s carries spawn_n %v", step, env["spawn_n"])
		}
	}
	// The handoff chain: grill and fix declare handoff — their envelopes
	// carry verified handoff refs, and the handoff files exist.
	for _, step := range []string{"grill", "fix"} {
		if _, err := os.Stat(filepath.Join(capDir, "artifacts", "handoff-"+step+".md")); err != nil {
			t.Fatalf("handoff file of %s missing: %v", step, err)
		}
	}

	// The capsule chain is visible on the SERVER side: the observe
	// lane's async push landed the driver's execution.json versions —
	// one commit per landed wave, so the mid-run chain already has
	// history.
	inst.waitBareCommits(t, wi, 2, 40*time.Second)

	// The terminal close: the daemon's v2 terminal wiring wraps the WI
	// when the driver exits with a completed workflow (the recovery
	// cause is the completion signal) — the CLI's close on the already
	// sealed work item is the graceful already-terminal answer.
	inst.waitWIStatus(t, wi, "wrapped", 60*time.Second)
	// T1 session trail, second half: the wrap CLEARED the session row —
	// a terminal work item names no holder (m4).
	if sess2 := inst.sessionOf(t, wi); sess2.Fingerprint != "" {
		t.Fatalf("wrapped work item still names session %q", sess2.Fingerprint)
	}

	// The sealed chain: several commits, the execution state committed,
	// the final push done.
	if n := inst.bareCommitCount(t, wi); n < 2 {
		t.Fatalf("sealed chain too short: %d commits", n)
	}
	waitUntil(t, 30*time.Second, "sealed chain never carried runtime-state/execution.json", func() bool {
		return inst.bareHasPath(t, wi, "runtime-state/execution.json")
	})
	transcript := inst.bareFileAtMain(t, wi, "runtime-state/execution.json")
	if !strings.Contains(transcript, wi) || !strings.Contains(transcript, "work_item_id") {
		t.Fatalf("execution.json at main is not this work item's: %s", transcript)
	}
	// The harness transcript rides the chain (v1 ruling C1 (archive/v1-architecture-spec.md)).
	hasTranscript := false
	out3 := gitRun(t, inst.bareRepo(wi), "ls-tree", "--name-only", "-r", "main", "session")
	for _, l := range strings.Split(out3, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), "session/session-") {
			hasTranscript = true
		}
	}
	if !hasTranscript {
		t.Fatalf("sealed chain misses the harness transcript:\n%s", out3)
	}

	// The daemon holds no job for a sealed work item.
	if st := d.status(t); len(st.Jobs) != 0 {
		t.Fatalf("daemon still holds jobs after wrap: %+v", st.Jobs)
	}

	// No orphaned step process survives the close (PF_WI_ID marker scan
	// — the driver terminates every step process after collecting its
	// envelope; the sealed close must leave none).
	waitUntil(t, 10*time.Second, "step processes survived the wrap", func() bool {
		return len(orphanPIDs(wi)) == 0
	})

	// The events file tells the whole story in order: the job lifecycle,
	// the C3 pushes, and exactly ONE recovery — the driver's completed
	// exit, which the v2 terminal wiring translates into the wrap (the
	// completion signal, not a death).
	kinds := d.eventKinds(t, wi)
	for _, want := range []string{"job_started", "published", "job_stopped"} {
		if !contains(kinds, want) {
			t.Fatalf("events %v miss %q", kinds, want)
		}
	}
	if n := countOf(kinds, "needs_recovery"); n != 1 {
		t.Fatalf("needs_recovery fired %d times, want exactly 1 (the completion signal): %v", n, kinds)
	}
}
