package test

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// v1_s04: wrap, driver/envelope edition. The driver completes the
// workflow and the daemon's terminal wiring wraps it; the sealed
// capsule keeps its inventory repo (repos/<name>, a real git worktree the
// fix step's envelope action created with its own origin remote) and the
// daemon pushes nothing to that origin — delivery is the workflows'
// deliver step (polyforge#166), not a daemon bridge.
func TestV1S04Wrap(t *testing.T) {
	inst := newV1Install(t)
	// The inventory repo's origin: a real bare repo the test owns.
	originDir := shortTmp(t, "pfv1origin")
	origin := filepath.Join(originDir, "demo.git")
	gitRun(t, "", "init", "--bare", "-b", "main", origin)

	wi := inst.newWI(t, "s04: wrap the sealed capsule",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			// The fix step's envelope action lays the inventory repo (a
			// real worktree with its own origin) before reporting done.
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true,
				"name": "demo", "origin": origin},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)
	d := newV1Daemon(t, inst)
	d.start()
	d.pf(t, "submit", wi)
	inst.waitWIStatus(t, wi, "wrapped", 90*time.Second)

	// The driver-era completion face: every step done, envelopes archived.
	waitWorkflowStatus(t, d, wi, "completed", 30*time.Second)
	env := archivedEnvelope(t, d.capsuleDir(wi), "fix", 2)
	if env["outcome"] != "completed" {
		t.Fatalf("fix envelope wrong: %v", env["outcome"])
	}

	// The sealed worktree carries the inventory repo and its head.
	repoDir := filepath.Join(d.capsuleDir(wi), "repos", "demo")
	sealedHead := strings.TrimSpace(gitRun(t, repoDir, "rev-parse", "HEAD"))

	// The daemon never pushes a delivery (polyforge#166: delivery is the
	// workflows' deliver step, not a daemon bridge): the origin holds no
	// polyforge/<project>-<seq> branch after the wrap.
	if refs := strings.TrimSpace(gitRun(t, origin, "for-each-ref", "refs/heads/polyforge/")); refs != "" {
		t.Fatalf("origin unexpectedly holds a delivery branch: %s", refs)
	}
	if sealedHead == "" {
		t.Fatal("sealed worktree has no head")
	}
}
