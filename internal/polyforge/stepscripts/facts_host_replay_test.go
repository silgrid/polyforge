package stepscripts

// facts_host_replay_test.go: `facts.py build --replay` on a repo whose
// HEAD carries no .polyforge/Dockerfile.verify replays on the host under
// the step's PATH — the rule pf-verify follows — so the test-first
// proof exists for such repos too: an honest unit is red at its test
// commit, a weak one (it passes before its implementation) is flagged, no
// container is started, and the container engine need not answer.

import (
	"errors"
	"strings"
	"testing"
)

func TestFactsBuildReplayOnTheHostWithoutAVerificationContainer(t *testing.T) {
	f := newCapsule(t, map[string]string{"a.txt": "a\n"})
	tc := f.change(t, map[string]string{"test/run.sh": "test -f impl.txt\n"})
	ic := f.change(t, map[string]string{"impl.txt": "done\n"})
	tc2 := f.change(t, map[string]string{"test/weak.sh": "echo WEAK-PASS\n"})
	ic2 := f.change(t, map[string]string{"notes.txt": "notes\n"})
	writeUnits(t, f, []factsUnitDecl{
		{ID: "u1", TestCommit: tc, ImplCommit: ic, Run: &factsUnitRun{Repo: "app", Cwd: ".", Argv: []string{"sh", "test/run.sh"}}},
		{ID: "u2", TestCommit: tc2, ImplCommit: ic2, Run: &factsUnitRun{Repo: "app", Argv: []string{"sh", "test/weak.sh"}}},
	})
	rt := newFakeRuntime(t)
	rt.unavailable = errors.New("no docker here: the host replay needs none")
	exit, res, errb := factsRun(t, f, rt, FactsBuild, "--replay", "--unit-timeout", "2m")
	if exit != 1 || res.Verdict != VerdictFail {
		t.Fatalf("exit %d (want 1: u2 is not red) %s\n%s", exit, res.Summary, errb)
	}
	var d buildDetails
	detailsOf(t, res, &d)
	if d.Replay == nil || len(d.Units) != 2 {
		t.Fatalf("details: %+v", d)
	}
	u1, u2 := d.Units[0], d.Units[1]
	if r := u1.Replay; r == nil || !r.Red || r.Exit != 1 || !r.Host || r.Container != "" || u1.ReplayError != "" {
		t.Fatalf("u1 replays red on the host: %+v (%s)", r, u1.ReplayError)
	}
	if r := u2.Replay; r == nil || r.Red || r.Exit != 0 || !r.Host {
		t.Fatalf("u2 passes before its implementation, on the host: %+v (%s)", r, u2.ReplayError)
	}
	if !strings.Contains(res.Summary, `unit "u2"`) || !strings.Contains(res.Summary, "passes before its implementation") || strings.Contains(res.Summary, `unit "u1"`) {
		t.Fatalf("the summary names u2 alone: %s", res.Summary)
	}
	if len(rt.starts) != 0 {
		t.Fatalf("no container for a repo without the Dockerfile: %d started", len(rt.starts))
	}
	if extra := replayWorktrees(t, f); len(extra) != 0 {
		t.Fatalf("worktrees left behind: %v", extra)
	}
	if f.head(t) != ic2 || gitT(t, f.repo(), "status", "--porcelain") != "" {
		t.Fatalf("the clone moved: HEAD %s status %q", f.head(t), gitT(t, f.repo(), "status", "--porcelain"))
	}
}
