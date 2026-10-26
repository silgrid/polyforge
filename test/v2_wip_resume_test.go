package test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestV2BlockedResumeKeepsUncommittedTaskWork (polyforge#167): the code
// step commits its work but leaves more UNCOMMITTED in the task repo (an
// edit of a tracked file and an untracked test file — the pi#102 shape
// that lost an e2e test three times). The next step crash-loops into the
// bounded-fail verdict (blocked). The blocked close snapshots the
// uncommitted work to refs/pf-wip/demo-src on the hub; `pf resume`
// re-claims a fresh capsule, restores the committed head from
// refs/pf-task/demo-src and checks the WIP tree out on top of it: the
// work is back in the working tree, uncommitted, HEAD unchanged.
func TestV2BlockedResumeKeepsUncommittedTaskWork(t *testing.T) {
	inst, remote := newV1InstallTaskRepo(t)
	const authored = "package demo // authored by the fix step, never committed\n"
	wi := inst.newWI(t, "wip: uncommitted task work survives blocked -> resume",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			// the code step: a commit (work.txt), then uncommitted work on
			// top — a tracked edit and an untracked file
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true,
				"name": "demo-src", "origin": remote.url,
				"files": map[string]string{
					"repos/demo-src/work.txt":        "edited after the commit\n",
					"repos/demo-src/e2e_wip_test.go": authored,
				}},
			// three rapid deaths: the bounded-fail verdict (blocked)
			{"do": "envelope", "id": "verification", "outcome": "completed", "crash": "before"},
			{"do": "envelope", "id": "verification", "outcome": "completed", "crash": "before"},
			{"do": "envelope", "id": "verification", "outcome": "completed", "crash": "before"},
			// the post-resume dispatch stays in flight while the test looks
			{"do": "envelope", "id": "verification", "outcome": "completed", "ms": 120000},
		}, nil)
	t.Cleanup(func() { killOrphans(wi) })

	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	inst.waitWIStatus(t, wi, "blocked", 180*time.Second)

	capA := d.capsuleDir(wi)
	repoA := filepath.Join(capA, "repos", "demo-src")
	codeHead := strings.TrimSpace(gitRun(t, repoA, "rev-parse", "HEAD"))
	if codeHead == remote.head {
		t.Fatalf("the fix step left no commit (head %s)", codeHead)
	}
	if st := gitRun(t, repoA, "status", "--porcelain"); !strings.Contains(st, "e2e_wip_test.go") || !strings.Contains(st, "work.txt") {
		t.Fatalf("setup: the dead session's task repo is not dirty: %q", st)
	}
	wip := strings.TrimSpace(gitRun(t, inst.bareRepo(wi), "rev-parse", "--verify", "-q", "refs/pf-wip/demo-src"))
	if parent := strings.TrimSpace(gitRun(t, inst.bareRepo(wi), "rev-parse", wip+"^")); parent != codeHead {
		t.Fatalf("WIP %s parent = %s, want the code head %s", wip, parent, codeHead)
	}

	if out := d.pf(t, "resume", wi); !strings.Contains(out, "resumed") {
		t.Fatalf("resume answer: %q", out)
	}
	repoB := filepath.Join(d.capsuleDir(wi), "repos", "demo-src")
	waitUntil(t, 90*time.Second, "the re-claimed capsule never restored the task repo's uncommitted work", func() bool {
		b, err := os.ReadFile(filepath.Join(repoB, "e2e_wip_test.go"))
		return err == nil && string(b) == authored
	})
	if head := strings.TrimSpace(gitRun(t, repoB, "rev-parse", "HEAD")); head != codeHead {
		t.Fatalf("re-claimed HEAD = %s, want the code head %s", head, codeHead)
	}
	if b, err := os.ReadFile(filepath.Join(repoB, "work.txt")); err != nil || string(b) != "edited after the commit\n" {
		t.Fatalf("the uncommitted edit of work.txt is missing: %q, %v", b, err)
	}
	if staged := strings.TrimSpace(gitRun(t, repoB, "diff", "--cached", "--name-only")); staged != "" {
		t.Fatalf("the restore staged %q (the work must come back uncommitted)", staged)
	}
	if st := gitRun(t, repoB, "status", "--porcelain"); !strings.Contains(st, "?? e2e_wip_test.go") || !strings.Contains(st, "M work.txt") {
		t.Fatalf("re-claimed status %q, want the uncommitted edit and the untracked file", st)
	}
	if contains(d.eventKinds(t, wi), "task_wip_not_applied") {
		t.Fatalf("a matching WIP raised task_wip_not_applied: %v", d.eventKinds(t, wi))
	}
}
