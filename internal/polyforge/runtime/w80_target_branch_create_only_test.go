package runtime

// w80_target_branch_create_only_test.go (polyforge#80 u7, owner ruling
// 2026-11-16T17:05:35Z over review r1#6): the daemon's creation of the
// fixed target branch on the origin is a CREATE-ONLY push — the remote
// already carrying the branch rejects it whatever the tips' relation,
// so a claim whose clone predates the branch never fast-forwards the
// shared epic/N past what the first claim created, and the claim falls
// back to the remote's copy (the ticket's 「被拒（已存在）就用远端已有
// 的」， held strictly).
//
// Expected values are the ticket's own words over a real git remote: a
// clone at a tip DESCENDANT of the remote's existing branch must not
// move it — a plain non-forced push would fast-forward it and leave the
// first claim's clone behind (the r1#6 window), which is what this test
// pins away.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// TestW80TargetBranchCreateOnlyNeverFastForwards: the clone stands on a
// tip (M2) that descends from the tip the origin's branch (M1) carries —
// the push is rejected because the branch EXISTS, and the clone falls
// back to the remote's M1, which stays where the other claim left it.
func TestW80TargetBranchCreateOnlyNeverFastForwards(t *testing.T) {
	r, j := w80RemoteHub(t, "wi_w80co", 12)
	src := t.TempDir()
	m1 := w079GitRepo(t, src, "readme.txt", "first\n")
	if err := os.WriteFile(filepath.Join(src, "second.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	carrierGit(t, src, "add", "-A")
	carrierGit(t, src, "commit", "-m", "the clone's tip, a descendant of M1")
	m2 := carrierGit(t, src, "rev-parse", "main")
	j.repos = []api.ProjectRepo{{Name: "app", URL: "file://" + src}}

	// park the push: the origin gains the branch at the ANCESTOR tip M1
	// only once the clone is about to push its descendant tip.
	orig := pushTargetBranchFn
	arrived := make(chan struct{}, 1)
	release := make(chan struct{})
	var once sync.Once
	unpark := func() { once.Do(func() { close(release) }) }
	t.Cleanup(func() { pushTargetBranchFn = orig; unpark() })
	pushTargetBranchFn = func(ctx context.Context, dir, name string) error {
		arrived <- struct{}{}
		<-release
		return orig(ctx, dir, name)
	}
	done := make(chan error, 1)
	go func() { done <- r.materialize(context.Background(), j) }()
	select {
	case <-arrived:
	case <-time.After(30 * time.Second):
		t.Fatal("the claim never reached the branch push (30s)")
	}
	carrierGit(t, src, "push", "-q", "origin", m1+":refs/heads/epic/12")
	unpark()
	if err := <-done; err != nil {
		t.Fatalf("materialize %s: %v", j.wi, err)
	}
	repoDir := filepath.Join(j.capsuleDir, capsule.DirRepos, "app")
	if head := carrierGit(t, repoDir, "rev-parse", "HEAD"); head != m1 {
		t.Fatalf("the clone stands on %q, want the remote's existing tip %q (the create-only push was not rejected)", head, m1)
	}
	if b := carrierGit(t, repoDir, "symbolic-ref", "--short", "HEAD"); b != "epic/12" {
		t.Fatalf("HEAD names %q, want epic/12", b)
	}
	if out := strings.TrimSpace(carrierGit(t, repoDir, "ls-remote", "--heads", "origin")); out != m1+"\trefs/heads/epic/12\n"+m2+"\trefs/heads/main" {
		t.Fatalf("the origin's epic/12 must stay at the other claim's tip %s (main is %s), got %q", m1, m2, out)
	}
}
