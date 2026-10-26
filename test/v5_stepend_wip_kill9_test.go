package test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// TestV5StepEndWIPSurvivesKill9: the fix step commits its work and leaves
// more UNCOMMITTED in the task repo; the daemon is SIGKILLed while the
// next step runs, so no close point (seal, pause, bounded-fail, shutdown)
// ever snapshots it. The local capsule is lost too (the re-claim happens
// as if on another machine): the restart blocks the work item (capsule
// missing) and `pf resume` re-claims a fresh capsule. The WIP push at the
// fix step's end already carried the uncommitted work to refs/pf-wip, so
// the re-claimed task repo has it back — uncommitted, HEAD unchanged.
func TestV5StepEndWIPSurvivesKill9(t *testing.T) {
	inst, remote := newV1InstallTaskRepo(t)
	const authored = "package demo // left uncommitted by the fix step\n"
	verify := map[string]any{"do": "envelope", "id": "verification", "outcome": "completed", "ms": 120000}
	wi := inst.newWI(t, "v5: step-end WIP survives kill -9",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true,
				"name": "demo-src", "origin": remote.url,
				"files": map[string]string{
					"repos/demo-src/work.txt":        "edited after the commit\n",
					"repos/demo-src/e2e_wip_test.go": authored,
				}},
			// the next step stays in flight (before and after the re-claim)
			verify, verify, verify,
		}, nil)
	t.Cleanup(func() { killOrphans(wi) })

	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)

	// The verification step runs and the capsule chain on the hub already
	// records it (the re-claim resumes from the chain).
	waitUntil(t, 120*time.Second, "the hub's capsule chain never recorded the verification step running", func() bool {
		raw, err := exec.Command("git", "--git-dir", inst.bareRepo(wi), "show", "main:runtime-state/execution.json").Output()
		if err != nil {
			return false
		}
		var x executionView
		if json.Unmarshal(raw, &x) != nil {
			return false
		}
		fix, ver := x.Steps["fix"], x.Steps["verification"]
		return fix != nil && fix.Status == "done" && ver != nil && ver.Status == "in_progress"
	})
	capA := d.capsuleDir(wi)
	repoA := filepath.Join(capA, "repos", "demo-src")
	codeHead := strings.TrimSpace(gitRun(t, repoA, "rev-parse", "HEAD"))
	if codeHead == remote.head {
		t.Fatalf("the fix step left no commit (head %s)", codeHead)
	}
	if st := gitRun(t, repoA, "status", "--porcelain"); !strings.Contains(st, "e2e_wip_test.go") || !strings.Contains(st, "work.txt") {
		t.Fatalf("setup: the task repo is not dirty: %q", st)
	}

	// kill -9: the daemon and its step process die mid-step; the capsule
	// is lost with the machine.
	if err := d.cmd.Process.Signal(syscall.SIGKILL); err != nil {
		t.Fatalf("kill -9 the daemon: %v", err)
	}
	<-d.waitedExit
	d.cmd = nil
	killOrphans(wi)
	if err := os.RemoveAll(capA); err != nil {
		t.Fatal(err)
	}

	d2 := d.restarted()
	d2.start()
	inst.waitWIStatus(t, wi, "blocked", 90*time.Second)
	if out := d2.pf(t, "resume", wi); !strings.Contains(out, "resumed") {
		t.Fatalf("resume answer: %q", out)
	}
	repoB := filepath.Join(d2.capsuleDir(wi), "repos", "demo-src")
	waitUntil(t, 90*time.Second, "the re-claimed capsule never restored the fix step's uncommitted work", func() bool {
		b, err := os.ReadFile(filepath.Join(repoB, "e2e_wip_test.go"))
		return err == nil && string(b) == authored
	})
	if head := strings.TrimSpace(gitRun(t, repoB, "rev-parse", "HEAD")); head != codeHead {
		t.Fatalf("re-claimed HEAD = %s, want the code head %s", head, codeHead)
	}
	if b, err := os.ReadFile(filepath.Join(repoB, "work.txt")); err != nil || string(b) != "edited after the commit\n" {
		t.Fatalf("the uncommitted edit of work.txt is missing: %q, %v", b, err)
	}
	if st := gitRun(t, repoB, "status", "--porcelain"); !strings.Contains(st, "?? e2e_wip_test.go") || !strings.Contains(st, "M work.txt") {
		t.Fatalf("re-claimed status %q, want the uncommitted edit and the untracked file", st)
	}
}
