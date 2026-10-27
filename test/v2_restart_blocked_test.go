package test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// wiRawWithEvents reads one work item with its event timeline as raw JSON
// text (the blocked reason rides the session_ended event's note).
func wiRawWithEvents(t *testing.T, inst *v1Install, wi string) string {
	t.Helper()
	var out json.RawMessage
	if err := inst.client.Do("GET", "/api/v1/work-items/"+wi+"?include=events", nil, &out); err != nil {
		t.Fatalf("get work item %s with events: %v", wi, err)
	}
	return string(out)
}

// TestV2RestartCapsuleMissingBlocksThenResumeFromCheckpoint: a running WI
// with a committed step loses its capsule directory while the daemon is
// down. The restarted daemon must start, recover every OTHER work item
// normally, and set ONLY this one to the ordinary aihub `blocked` status
// with a reason naming the missing capsule path (nothing stays parked
// locally). The human's `pf resume` re-claims it, restores the work from
// the capsule chain + the refs/pf-task carrier, and continues from the
// checkpoint: completed steps are not re-run.
func TestV2RestartCapsuleMissingBlocksThenResumeFromCheckpoint(t *testing.T) {
	inst, remote := newV1InstallTaskRepo(t)
	wi := inst.newWI(t, "restart: capsule missing -> blocked -> resume",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true,
				"name": "demo-src", "origin": remote.url},
			// the crash window: verification is in flight when the daemon dies
			{"do": "envelope", "id": "verification", "outcome": "completed", "ms": 120000},
			// the post-resume dispatch of verification
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	other := inst.newWI(t, "restart: bystander recovers normally",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "ms": 120000},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	t.Cleanup(func() { killOrphans(wi); killOrphans(other) })

	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	d.pf(t, "submit", other)
	waitStepStatus(t, d, wi, "verification", "in_progress", 90*time.Second)
	waitStepStatus(t, d, other, "fix", "in_progress", 90*time.Second)

	capA := d.capsuleDir(wi)
	repoA := filepath.Join(capA, "repos", "demo-src")
	codeHead := strings.TrimSpace(gitRun(t, repoA, "rev-parse", "HEAD"))
	if codeHead == remote.head {
		t.Fatalf("the fix step left no commit in the task repo (head %s)", codeHead)
	}
	// the checkpoint must be durable at the hub before the loss: the
	// capsule chain (execution.json) and the task-repo carrier ref
	waitUntil(t, 60*time.Second, "the carrier ref refs/pf-task/demo-src never reached the hub", func() bool {
		out, err := os.ReadFile(filepath.Join(inst.bareRepo(wi), "refs", "pf-task", "demo-src"))
		if err == nil {
			return strings.TrimSpace(string(out)) == codeHead
		}
		packed, perr := os.ReadFile(filepath.Join(inst.bareRepo(wi), "packed-refs"))
		return perr == nil && strings.Contains(string(packed), "refs/pf-task/demo-src")
	})
	// ...and the hub chain's execution.json must already record the fix
	// step done and verification's step_start (work published after the
	// last push is the only thing a capsule loss may cost; the step_start
	// reaches the hub by its own async push, so crashing on "fix done"
	// alone sometimes re-derives the dead ordinal and replays its 120s
	// action into the 60s model-error grace)
	waitUntil(t, 60*time.Second, "the hub chain never recorded the fix step done and verification running", func() bool {
		if !inst.bareHasPath(t, wi, "runtime-state/execution.json") {
			return false
		}
		var x executionView
		if json.Unmarshal([]byte(inst.bareFileAtMain(t, wi, "runtime-state/execution.json")), &x) != nil {
			return false
		}
		fix, ver := x.Steps["fix"], x.Steps["verification"]
		return fix != nil && fix.Status == "done" && ver != nil && ver.Status == "in_progress"
	})
	preMax := 0.0 // the highest ordinal dispatched before the loss
	for _, s := range spawnJournal(t, capA) {
		if n, _ := s["spawn_n"].(float64); n > preMax {
			preMax = n
		}
	}

	// the daemon dies; the operator's capsule loss happens while it is down
	d.crash()
	if err := os.RemoveAll(capA); err != nil {
		t.Fatal(err)
	}

	d2 := restartSameDaemon(t, d, "daemon2.log")

	// only the broken work item is blocked, with a reason naming the cause
	inst.waitWIStatus(t, wi, "blocked", 60*time.Second)
	raw := wiRawWithEvents(t, inst, wi)
	if !strings.Contains(raw, "capsule missing") || !strings.Contains(raw, capA) {
		t.Fatalf("the blocked reason does not name the missing capsule path %s: %s", capA, raw)
	}
	// nothing is left parked locally: no job record, no step process
	if st := watchSoft(d2, wi); st != nil && st.Job != nil {
		t.Fatalf("the blocked work item is still held locally: %+v", st.Job)
	}
	waitUntil(t, 20*time.Second, "the crashed daemon's step process of the blocked WI survived", func() bool {
		return len(orphanPIDs(wi)) == 0
	})
	if !contains(d2.eventKinds(t, wi), "capsule_missing") {
		t.Fatalf("pf watch events miss capsule_missing: %v", d2.eventKinds(t, wi))
	}

	// the bystander recovered normally and finishes
	inst.waitWIStatus(t, other, "wrapped", 180*time.Second)

	// the human resumes: re-claim (a fresh capsule from the chain), the
	// task repo restored from the carrier, the workflow continues
	out := d2.pf(t, "resume", wi)
	if !strings.Contains(out, "resumed") {
		t.Fatalf("resume answer: %q", out)
	}
	waitUntil(t, 90*time.Second, "the re-claimed capsule never restored the task repo", func() bool {
		st, err := os.Stat(filepath.Join(d2.capsuleDir(wi), "repos", "demo-src", ".git"))
		return err == nil && st.IsDir()
	})
	inst.waitWIStatus(t, wi, "wrapped", 180*time.Second)

	repoB := filepath.Join(d2.capsuleDir(wi), "repos", "demo-src")
	if head := strings.TrimSpace(gitRun(t, repoB, "rev-parse", "HEAD")); head != codeHead {
		t.Fatalf("task repo not restored from refs/pf-task: HEAD %s, want %s", head, codeHead)
	}
	if b, err := os.ReadFile(filepath.Join(repoB, "work.txt")); err != nil || !strings.Contains(string(b), wi) {
		t.Fatalf("the fix step's work.txt is missing after the resume: %q, %v", b, err)
	}
	// completed steps were NOT re-run: every dispatch after the loss —
	// an ordinal past the dead session's (the chain may lack the dead
	// dispatch's journal line, not its step_start: waited for above) — is
	// verification
	spawns := spawnJournal(t, d2.capsuleDir(wi))
	resumed := 0
	for _, s := range spawns {
		if n, _ := s["spawn_n"].(float64); n <= preMax {
			continue
		}
		resumed++
		if id, _ := s["step_id"].(string); id != "verification" {
			t.Fatalf("a completed step was re-run after the resume: %v (spawns %v)", s["step_id"], spawns)
		}
	}
	if resumed == 0 {
		t.Fatalf("the resume dispatched nothing past ordinal %v (spawns %v)", preMax, spawns)
	}
	x := readExecution(t, d2.capsuleDir(wi))
	for _, step := range []string{"grill", "fix", "verification"} {
		if st := x.Steps[step]; st == nil || st.Status != "done" {
			t.Fatalf("step %s: %+v, want done", step, st)
		}
	}
}

// TestV2RestartSnapshotMissingBlocksThenResumeRematerializes: a capsule whose
// claim-time bundle snapshot marker is gone at recovery is blocked (the
// recovery never re-pins or re-materializes silently); the human's
// `pf resume` re-claims, which materializes the bundle fresh at claim time
// (pinned digest verified), so the work item continues and wraps — it does
// not loop back into blocked.
func TestV2RestartSnapshotMissingBlocksThenResumeRematerializes(t *testing.T) {
	inst := newV1Install(t)
	wi := inst.newWI(t, "restart: snapshot missing -> blocked -> resume",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "ms": 120000},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	t.Cleanup(func() { killOrphans(wi) })
	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	waitStepStatus(t, d, wi, "fix", "in_progress", 90*time.Second)
	marker := filepath.Join(d.capsuleDir(wi), "sync", "materialized.json")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the claim-time snapshot marker is missing before the loss: %v", err)
	}
	d.crash()
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	d2 := restartSameDaemon(t, d, "daemon2.log")
	inst.waitWIStatus(t, wi, "blocked", 60*time.Second)
	if raw := wiRawWithEvents(t, inst, wi); !strings.Contains(raw, "materialized snapshot of bundle") {
		t.Fatalf("the blocked reason does not name the missing snapshot: %s", raw)
	}

	if out := d2.pf(t, "resume", wi); !strings.Contains(out, "resumed") {
		t.Fatalf("resume answer: %q", out)
	}
	inst.waitWIStatus(t, wi, "wrapped", 180*time.Second)
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("the re-claim did not re-materialize the bundle snapshot: %v", err)
	}
}
