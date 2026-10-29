package driver

// review_coverage_test.go: the review-coverage merge
// guard. A PASS review records the task repos' HEADs (driver-written,
// never step-supplied); the automatic merge needs every delivered repo
// covered — a reviewed head with only git-reproducible merges between it
// and the delivered HEAD. A plain commit after the pass, or a missing
// record, blocks the merge and routes the delivery to the human;
// autopilot (no review step) is untouched; a re-verification that does
// not move HEAD keeps the pass covered.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// reviewCoverRepo lays one task repo (<tmp>/repos/demo, one commit on
// main, pushed to a bare origin) and answers its repos dir.
func reviewCoverRepo(t *testing.T) string {
	t.Helper()
	repos := filepath.Join(t.TempDir(), "repos")
	dir := filepath.Join(repos, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bare := filepath.Join(t.TempDir(), "origin.git")
	tgit(t, filepath.Dir(bare), "init", "-q", "--bare", "-b", "main", bare)
	tgit(t, dir, "init", "-q", "-b", "main")
	tgit(t, dir, "remote", "add", "origin", bare)
	dwrite(t, filepath.Join(dir, "base.txt"), "base\n")
	tgit(t, dir, "add", "-A")
	tgit(t, dir, "commit", "-q", "-m", "baseline")
	tgit(t, dir, "push", "-q", "origin", "main")
	tgit(t, dir, "fetch", "-q", "origin")
	tgit(t, dir, "remote", "set-head", "origin", "main")
	return repos
}

// TestReviewCoverageMergeGuard: mergeBlocker's review-coverage rule.
func TestReviewCoverageMergeGuard(t *testing.T) {
	r := deliverRepo{Name: "demo", Branch: "polyforge/polyforge-166",
		PRURL: "https://github.com/o/r/pull/1", Merged: ptrTrue(), MergeCommit: "abc", TestsPassed: ptrTrue()}

	t.Run("covered at the same head", func(t *testing.T) {
		repos := deliverCapsule(t)
		spec := deliverSpec(repos, true, 0) // reviewed == verified == HEAD
		if why := mergeBlocker(spec, r, "polyforge/polyforge-166", ev(goodPush)); why != "" {
			t.Fatalf("a review that covered HEAD must not block: %q", why)
		}
	})
	t.Run("a plain commit after the review pass blocks the merge", func(t *testing.T) {
		repos := deliverCapsule(t)
		spec := deliverSpec(repos, true, 0)
		dir := filepath.Join(repos, "demo")
		dwrite(t, filepath.Join(dir, "a.txt"), "landed after review\n")
		tgit(t, dir, "commit", "-q", "-am", "after review")
		spec.VerifiedHeads["demo"] = tgit(t, dir, "rev-parse", "HEAD") // verification re-passed on the new tree
		pushFeature(t, dir)
		why := mergeBlocker(spec, r, "polyforge/polyforge-166", ev(goodPush))
		low := strings.ToLower(why)
		if why == "" || !strings.Contains(low, "review") ||
			!(strings.Contains(low, "cover") || strings.Contains(low, "head")) ||
			!strings.Contains(low, "not a two-parent merge") {
			t.Fatalf("the blocker must name the review's missing coverage of HEAD, got %q", why)
		}
	})
	t.Run("only a reproducible default-branch merge after the pass is covered", func(t *testing.T) {
		repos := deliverCapsule(t)
		spec := deliverSpec(repos, true, 0)
		dir := filepath.Join(repos, "demo")
		// origin/main moves on; the deliver merge is a clean two-parent merge
		other := filepath.Join(t.TempDir(), "other")
		tgit(t, filepath.Dir(other), "clone", "-q", filepath.Join(filepath.Dir(filepath.Dir(repos)), "origin.git"), other)
		dwrite(t, filepath.Join(other, "up.txt"), "up\n")
		tgit(t, other, "add", "-A")
		tgit(t, other, "commit", "-q", "-m", "another PR")
		tgit(t, other, "push", "-q", "origin", "HEAD:main")
		tgit(t, dir, "fetch", "-q", "origin")
		tgit(t, dir, "merge", "-q", "--no-edit", "origin/main")
		spec.VerifiedHeads["demo"] = tgit(t, dir, "rev-parse", "HEAD")
		pushFeature(t, dir)
		evidence := append(ev(goodPush),
			CommandEvidence{Argv: []string{"sh", "-c", "cd repos/demo && git merge --no-edit origin/main"}, DurationMs: 9},
			CommandEvidence{Argv: []string{"sh", "-c", "cd repos/demo && make test"}, DurationMs: 9})
		if why := mergeBlocker(spec, r, "polyforge/polyforge-166", evidence); why != "" {
			t.Fatalf("a reproducible default-branch merge after the pass stays covered: %q", why)
		}
	})
	t.Run("a missing reviewed head routes to the human", func(t *testing.T) {
		repos := deliverCapsule(t)
		spec := deliverSpec(repos, true, 0)
		spec.ReviewedHeads = nil // a review that predates the record, an in-flight capsule
		why := mergeBlocker(spec, r, "polyforge/polyforge-166", ev(goodPush))
		if why == "" || !strings.Contains(why, "no review covers the delivered diff") {
			t.Fatalf("a missing reviewed head must block, got %q", why)
		}
	})
	t.Run("autopilot has no review to cover", func(t *testing.T) {
		repos := deliverCapsule(t)
		spec := deliverSpec(repos, true, 0)
		spec.HasReview = false
		spec.ReviewedHeads = nil
		if why := mergeBlocker(spec, r, "polyforge/polyforge-166", ev(goodPush)); why != "" {
			t.Fatalf("autopilot's merge must not need review coverage: %q", why)
		}
	})
}

// TestReviewedHeadsDriverWrittenOnReviewPass: the reviewed heads are
// driver-written at the review PASS settlement (the verifiedHeads
// pattern: git I/O outside the transition's lock) and a step-supplied
// value is never kept; a FAIL verdict records none.
func TestReviewedHeadsDriverWrittenOnReviewPass(t *testing.T) {
	d, _, _, execPath := testEnv(t, spec("build"), gateSpec("review", "build"))
	// one task repo with a commit on main
	dir := filepath.Join(d.wi.CapsuleDir, capsule.DirRepos, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tgit(t, dir, "init", "-q", "-b", "main")
	dwrite(t, filepath.Join(dir, "work.txt"), "w\n")
	tgit(t, dir, "add", "-A")
	tgit(t, dir, "commit", "-q", "-m", "work")
	head := tgit(t, dir, "rev-parse", "HEAD")

	mustInit(t, d)
	mustStart(t, d, "build", 1)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: 1, Result: completedResult("build", 1, `{}`)}); err != nil {
		t.Fatalf("result build: %v", err)
	}
	// a FAIL verdict records no reviewed heads (the pass does not exist)
	mustStart(t, d, "review", 2)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "review", SpawnN: 2,
		Result: &StepResult{StepID: "review", SpawnN: 2, Outcome: OutcomeGateFailed,
			Output: json.RawMessage(`{"verdict":"fail","fail_reason":"not yet"}`)},
	}); err != nil {
		t.Fatalf("review fail: %v", err)
	}
	if out := readExec(t, execPath).Steps["review"].Output; out["reviewed_heads"] != nil {
		t.Fatalf("a FAIL verdict must record no reviewed heads: %+v", out["reviewed_heads"])
	}
	// the PASS settlement writes the driver-read heads; the forged
	// step-supplied value is deleted
	mustStart(t, d, "build", 3)
	if err := d.transition(TransitionRequest{Kind: TransStepResult, StepID: "build", SpawnN: 3, Result: completedResult("build", 3, `{}`)}); err != nil {
		t.Fatalf("result build: %v", err)
	}
	mustStart(t, d, "review", 4)
	if err := d.transition(TransitionRequest{
		Kind: TransStepResult, StepID: "review", SpawnN: 4,
		Result: &StepResult{StepID: "review", SpawnN: 4, Outcome: OutcomeCompleted,
			Output: json.RawMessage(`{"verdict":"pass","reviewed_heads":{"demo":"forged-by-step"}}`)},
		ReviewedHeads: d.reviewedHeads("review", &StepResult{StepID: "review", SpawnN: 4, Outcome: OutcomeCompleted,
			Output: json.RawMessage(`{"verdict":"pass"}`)}),
	}); err != nil {
		t.Fatalf("review pass: %v", err)
	}
	out := readExec(t, execPath).Steps["review"].Output
	heads, _ := out[reviewedHeadsKey].(map[string]any)
	if heads["demo"] != head {
		t.Fatalf("the reviewed heads must be driver-written, never step-supplied: %+v", out[reviewedHeadsKey])
	}
}

// reviewDoneLog copies execution.json when the review step's step_done
// event is emitted: the file a kill -9 at that instant leaves behind.
type reviewDoneLog struct {
	src, dst string
	taken    bool
}

func (l *reviewDoneLog) Append(_, kind string, body any) error {
	if kind != "step_done" || l.taken {
		return nil
	}
	if b, _ := body.(map[string]any); b["step"] != reviewStepID {
		return nil
	}
	raw, err := os.ReadFile(l.src)
	if err != nil {
		return err
	}
	l.taken = true
	return os.WriteFile(l.dst, raw, 0o644)
}

// TestCrashAroundReviewPassKeepsReviewedHeads: the reviewed heads land in
// the review PASS's own settlement write — a kill -9 the instant the
// step_done event fires leaves the record durable, and the restarted
// lane merges on it.
func TestCrashAroundReviewPassKeepsReviewedHeads(t *testing.T) {
	e := newCrashEnv(t, spec("build"), gateSpec("review", "build"))
	dir := filepath.Join(e.dir, capsule.DirRepos, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tgit(t, dir, "init", "-q", "-b", "main")
	dwrite(t, filepath.Join(dir, "work.txt"), "w\n")
	tgit(t, dir, "add", "-A")
	tgit(t, dir, "commit", "-q", "-m", "work")
	head := tgit(t, dir, "rev-parse", "HEAD")
	crashed := filepath.Join(t.TempDir(), "execution.json")
	log := &reviewDoneLog{src: e.execPath, dst: crashed}
	sp := &fakeSpawner{script: []fakeEntry{
		done(`{}`),                 // build (spawn 1)
		done(`{"verdict":"pass"}`), // review (spawn 2): the PASS settlement
	}}
	if err := e.driver(sp, 0, log).Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !log.taken {
		t.Fatal("no step_done event fired")
	}
	atCrash := tryReadExec(crashed)
	if atCrash == nil {
		t.Fatal("the crash-instant execution.json is unreadable")
	}
	rev := atCrash.Steps["review"]
	if rev == nil || rev.Status != capsule.StepDone {
		t.Fatalf("the crash instant must hold the accepted review: %+v", rev)
	}
	heads, _ := rev.Output[reviewedHeadsKey].(map[string]any)
	if heads["demo"] != head {
		t.Fatalf("the crash instant must hold the driver-written reviewed heads: %+v", rev.Output)
	}
}
