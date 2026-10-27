package runtime

// stepwip_test: the task-repo WIP push of the driver's step-end hook and
// of the shutdown (SaveTaskWIP).

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
)

// A kill -9 right after a step end loses none of the step's uncommitted
// task-repo work on a re-claim: the step-end hook already pushed it to
// refs/pf-wip, and the re-claim of a fresh capsule restores it.
func TestStepEndWIPSurvivesACrashAndReclaim(t *testing.T) {
	g := newWIPRig(t, "wi_wip_stepend")
	g.dirty(t)
	g.r.stepEnded(g.wi)(context.Background())

	wip, ok := g.hubRef(t, gitops.TaskWIPRefName("app"))
	if !ok {
		t.Fatal("the step end left no refs/pf-wip/app for a dirty task repo")
	}
	if parent, _ := g.hubRef(t, wip+"^"); parent != g.workHead {
		t.Fatalf("WIP parent = %q, want the work head %q", parent, g.workHead)
	}

	// kill -9 and a re-claim elsewhere: no close point runs, the job and
	// the local capsule are gone, the hub frees the work item.
	terminateJobAgent(g.r, g.wi)
	g.r.dropJob(g.r.jobOf(g.wi))
	if err := os.RemoveAll(testCapsuleDir(g.r, g.wi)); err != nil {
		t.Fatal(err)
	}
	g.hub.forceEnd(g.wi)

	if err := g.r.Start(context.Background(), g.wi, StartOpts{}); err != nil {
		t.Fatalf("re-claim Start: %v", err)
	}
	t.Cleanup(func() { terminateJobAgent(g.r, g.wi) })
	repoB := filepath.Join(testCapsuleDir(g.r, g.wi), capsule.DirRepos, "app")
	if got := carrierGit(t, repoB, "rev-parse", "HEAD"); got != g.workHead {
		t.Fatalf("re-claimed HEAD = %q, want %q", got, g.workHead)
	}
	if b, err := os.ReadFile(filepath.Join(repoB, "work.txt")); err != nil || string(b) != "committed work\nplus uncommitted edit\n" {
		t.Fatalf("the uncommitted edit is missing: %q, %v", b, err)
	}
	if b, err := os.ReadFile(filepath.Join(repoB, "e2e_test.go")); err != nil || !strings.Contains(string(b), "never committed") {
		t.Fatalf("the untracked file is missing: %q, %v", b, err)
	}
}

// Every derivation the runtime starts carries the step-end hook.
func TestAgentStartCarriesTheStepEndHook(t *testing.T) {
	g := newWIPRig(t, "wi_wip_hook")
	terminateJobAgent(g.r, g.wi)
	g.r.dropJob(g.r.jobOf(g.wi))
	g.hub.forceEnd(g.wi)
	inner := g.r.cfg.AgentStarter
	var got func(context.Context)
	g.r.cfg.AgentStarter = func(ctx context.Context, in AgentStartInput) (AgentHandle, error) {
		got = in.StepEnded
		return inner(ctx, in)
	}
	if err := g.r.Start(context.Background(), g.wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { terminateJobAgent(g.r, g.wi) })
	if got == nil {
		t.Fatal("the agent start got no StepEnded hook")
	}
}

// A hung WIP push holds SaveTaskWIP no longer than its context, and the
// push itself is cut at the same deadline.
func TestSaveTaskWIPReturnsAtItsDeadline(t *testing.T) {
	g := newWIPRig(t, "wi_wip_bound")
	t.Cleanup(func() { terminateJobAgent(g.r, g.wi) })
	entered := make(chan gitops.Opts, 1)
	release := make(chan struct{})
	inner := pushTaskWIPFn
	pushTaskWIPFn = func(o gitops.Opts, keep, chainOK bool) {
		entered <- o
		<-release
	}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	want, _ := ctx.Deadline()
	t0 := time.Now()
	g.r.SaveTaskWIP(ctx, g.wi)
	took := time.Since(t0)
	o := <-entered
	close(release)
	pushTaskWIPFn = inner
	if took > 5*time.Second {
		t.Fatalf("SaveTaskWIP took %s on a hung push, want its 300ms context", took)
	}
	if got, _ := o.Ctx.Deadline(); !got.Equal(want) {
		t.Fatalf("push deadline = %v, want the context's %v", got, want)
	}
}
