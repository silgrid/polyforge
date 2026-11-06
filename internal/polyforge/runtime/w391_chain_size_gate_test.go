package runtime

// at the runtime: an existing capsule gets the downloads
// rule of the capsule .gitignore on claim and on resume (no manual step),
// and a chain push the hub refuses for its blob limit ends in a blocked
// close whose note — the human decision — names the path and the
// unpushed range, as the push_failed event does.

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	aihubgit "github.com/silgrid/polyforge/internal/aihub/git"
	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
)

// w391OldGitignore is the capsule .gitignore before.
const w391OldGitignore = "/repos/\n/artifacts/release-*/\n/sync/\n"

func w391Git(t *testing.T, env []string, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env, cmd.Dir = env, dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return string(out)
}

func w391WriteFile(t *testing.T, root, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// w391AssertNoDownloads fails on any chain path under the downloads
// directory at the bare repo's main, and on a .gitignore that is not the
// current one.
func w391AssertNoDownloads(t *testing.T, bare string) {
	t.Helper()
	tree := w391Git(t, nil, "", "--git-dir", bare, "ls-tree", "-r", "--name-only", "main")
	for _, p := range strings.Split(strings.TrimSpace(tree), "\n") {
		if strings.HasPrefix(p, capsule.DirDownloads+"/") {
			t.Errorf("%s is in the chain", p)
		}
	}
	if gi := w391Git(t, nil, "", "--git-dir", bare, "show", "main:.gitignore"); !strings.Contains(gi, "/artifacts/downloads/\n") {
		t.Errorf("the chain's .gitignore lacks the downloads rule: %q", gi)
	}
}

// AC2, resume: a capsule whose chain carries the old .gitignore (and a
// file it let in under artifacts/downloads/) is recovered in place — the
// operator's resume — and its first commit after it keeps the downloads
// directory out of the chain.
func TestW391ResumeAppliesTheDownloadsRule(t *testing.T) {
	sp := &scriptedSpawner{}
	r, toOB, _ := newTestRuntime(t, newFakeHub(), sp)
	j := seedJob(r, "wi_w391_resume")
	bare := initGitCapsule(t, j)
	w391WriteFile(t, j.capsuleDir, ".gitignore", []byte(w391OldGitignore))
	w391WriteFile(t, j.capsuleDir, "artifacts/downloads/old.zip", []byte("committed before the rule\n"))
	if _, _, _, err := gitops.CommitPush(context.Background(), j.git, "before the upgrade"); err != nil {
		t.Fatalf("setup push: %v", err)
	}
	if tree := w391Git(t, nil, "", "--git-dir", bare, "ls-tree", "-r", "--name-only", "main"); !strings.Contains(tree, "artifacts/downloads/old.zip") {
		t.Fatalf("setup: the old rules let the download in: %s", tree)
	}
	w391WriteFile(t, j.capsuleDir, "artifacts/downloads/new.zip", []byte("downloaded while the agent ran\n"))

	j.mu.Lock()
	j.agent = &recordingHandle{}
	j.mu.Unlock()
	r.cfg.AgentStarter = func(ctx context.Context, in AgentStartInput) (AgentHandle, error) {
		return &recordingHandle{}, nil
	}
	r.recoverOp(context.Background(), j, causeOperatorResume, "")
	if msg := recvRT(t, toOB); msg.Kind != protocol.RTRecovered {
		t.Fatalf("the resume must recover, got %+v", msg)
	}
	w391AssertNoDownloads(t, bare)
	for _, p := range []string{"artifacts/downloads/old.zip", "artifacts/downloads/new.zip"} {
		if _, err := os.Stat(filepath.Join(j.capsuleDir, filepath.FromSlash(p))); err != nil {
			t.Errorf("%s must stay in the capsule: %v", p, err)
		}
	}
}

// AC2, claim: a work item whose chain carries the old .gitignore and a
// file under artifacts/downloads/ is claimed; the claim's capsule has
// the current .gitignore and its next commit leaves the downloads out.
func TestW391ClaimAppliesTheDownloadsRule(t *testing.T) {
	setGitIdentity(t)
	const wi = "wi_w391_claim"
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.WorkflowsDir = pristineBundle(t)

	// the chain a previous session published
	bare := filepath.Join(hub.capsuleRepoBase, wi+".git")
	seed := t.TempDir()
	w391Git(t, nil, "", "init", "-q", "-b", "main", seed)
	w391WriteFile(t, seed, ".gitignore", []byte(w391OldGitignore))
	w391WriteFile(t, seed, "artifacts/downloads/old.zip", []byte("committed before the rule\n"))
	w391WriteFile(t, seed, "artifacts/notes.md", []byte("kept\n"))
	w391Git(t, nil, seed, "add", "-A")
	w391Git(t, nil, seed, "commit", "-q", "-m", "an earlier session")
	w391Git(t, nil, "", "init", "--bare", "-q", "--initial-branch=main", bare)
	w391Git(t, nil, seed, "push", "-q", bare, "main:main")

	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop(context.Background(), wi, protocol.FinishWrapped) })
	j := r.jobOf(wi)
	if j == nil {
		t.Fatal("no live job")
	}
	w391WriteFile(t, j.capsuleDir, "artifacts/downloads/new.zip", []byte("a new download\n"))
	if _, _, _, err := gitops.CommitPush(context.Background(), j.git, "after the claim"); err != nil {
		t.Fatalf("CommitPush: %v", err)
	}
	w391AssertNoDownloads(t, bare)
	if got := w391Git(t, nil, "", "--git-dir", bare, "show", "main:artifacts/notes.md"); got != "kept\n" {
		t.Fatalf("the rest of the chain must stay: %q", got)
	}
}

// AC3, the human decision: the final chain push of a wrap is refused by
// the production hook for a blob already in the unpushed range (a step's
// own commit); the close degrades to blocked and its note names the path,
// the commit and the range, and so does the push_failed event.
func TestW391SizeRefusalNamesPathAndRangeInTheBlockedClose(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	const wi = "wi_w391_seal"
	j := seedJob(r, wi)
	initGitCapsule(t, j)
	mgr, err := aihubgit.NewManager(filepath.Join(t.TempDir(), "hubdata"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := mgr.EnsureBare(wi); err != nil {
		t.Fatal(err)
	}
	j.git.RemoteURL = mgr.RepoDir(wi)
	if _, _, _, err := gitops.CommitPush(context.Background(), j.git, "published"); err != nil {
		t.Fatalf("setup push: %v", err)
	}
	pushed := strings.TrimSpace(w391Git(t, nil, "", "--git-dir", j.git.RemoteURL, "rev-parse", "main"))
	w391WriteFile(t, j.capsuleDir, "artifacts/ci/trace.zip", bytes.Repeat([]byte{4}, capsule.MaxBlobBytes+1))
	w391Git(t, j.git.Env(), j.capsuleDir, "add", "artifacts/ci/trace.zip")
	w391Git(t, j.git.Env(), j.capsuleDir, "commit", "-q", "-m", "a step's own commit")
	offender := strings.TrimSpace(w391Git(t, nil, "", "--git-dir", j.git.GitDir, "rev-parse", "HEAD"))

	var mu sync.Mutex
	var failed []map[string]any
	r.cfg.EventTap = func(_ string, kind string, body map[string]any) {
		if kind == "push_failed" {
			mu.Lock()
			failed = append(failed, body)
			mu.Unlock()
		}
	}
	hub.hold(wi, j.sessionID, api.StatusRunning)
	if err := r.Stop(context.Background(), wi, protocol.FinishWrapped); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	end := hub.lastEnd()
	if end.Status != api.StatusBlocked {
		t.Fatalf("the refused wrap must close blocked, got %+v", end)
	}
	head := strings.TrimSpace(w391Git(t, nil, "", "--git-dir", j.git.GitDir, "rev-parse", "HEAD"))
	rng := pushed + ".." + head
	for _, want := range []string{"artifacts/ci/trace.zip", rng, offender} {
		if !strings.Contains(end.Note, want) {
			t.Errorf("the blocked close's note must name %q: %q", want, end.Note)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(failed) != 1 || failed[0]["range"] != rng || !strings.Contains(fmt.Sprint(failed[0]["oversize"]), "artifacts/ci/trace.zip") {
		t.Fatalf("push_failed events = %v, want one naming the range %s and the path", failed, rng)
	}
}
