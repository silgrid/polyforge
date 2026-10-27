package git

// Direct unit tests for Janitor.GCOnce() (fix memo wi_Xah1Y0REajOb): the
// maintenance pass must actually pack loose objects in a REAL bare repo
// (built through Manager.EnsureBare / Manager.RepoDir), be idempotent
// (a second run is a nil-returning, log-silent no-op that keeps the repo
// fsck-clean), and treat a missing git root as a silent nil no-op.
//
// The git binary is required (the janitor shells out to `git gc`), so the
// same LookPath skip guard the git-chain tests in git_test.go use is
// applied here.

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// requireGitBin skips the test when git or the shell is unavailable
// (mirrors the guard in newGitEnv).
func requireGitBin(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git binary not found: %v", err)
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash not found: %v", err)
	}
}

// gitStdin runs git in repo with input fed on stdin; fails the test on
// error. (runGit/mustGit in git_test.go have no stdin support.)
func gitStdin(t *testing.T, repo, input string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = repo
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

// looseObjects lists the loose objects under <repo>/objects/XX/.
func looseObjects(t *testing.T, repo string) []string {
	t.Helper()
	objs, err := filepath.Glob(filepath.Join(repo, "objects", "[0-9a-f][0-9a-f]", "*"))
	if err != nil {
		t.Fatalf("glob loose objects: %v", err)
	}
	return objs
}

// packFiles lists the pack files under <repo>/objects/pack/.
func packFiles(t *testing.T, repo string) []string {
	t.Helper()
	packs, err := filepath.Glob(filepath.Join(repo, "objects", "pack", "*.pack"))
	if err != nil {
		t.Fatalf("glob pack files: %v", err)
	}
	return packs
}

// captureLog swaps the standard logger's output for the duration of fn
// and returns what was written (GCOnce reports per-repo failures through
// log.Printf).
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)
	fn()
	return buf.String()
}

// pickSeventeenSeeds returns two distinct file contents whose blob object
// ids start with "17". Why it matters: git's `gc --auto` heuristic
// estimates the loose-object total by sampling ONLY the objects/17
// fan-out bucket and fires when that bucket's count exceeds
// ceil(gc.auto/256) — with gc.auto 1 that means at least TWO loose
// objects in the 17 bucket, or the auto-gc silently does nothing.
// Computing the ids locally (a git blob id is sha1("blob <size>\0" +
// content)) keeps the fixture deterministic instead of spawning hundreds
// of hash-object probes hunting for 17-prefixed content.
func pickSeventeenSeeds() []string {
	found := make([]string, 0, 2)
	for i := 0; len(found) < 2; i++ {
		content := fmt.Sprintf("janitor gc seed %d\n", i)
		h := sha1.New()
		io.WriteString(h, fmt.Sprintf("blob %d\x00", len(content)))
		io.WriteString(h, content)
		if fmt.Sprintf("%x", h.Sum(nil)[0]) == "17" { // first object-id byte = 0x17
			found = append(found, content)
		}
	}
	return found
}

// seedReachableLooseObjects writes a small fully-reachable history (blobs
// → tree → commit, pointed at by refs/heads/main) into the bare repo using
// plumbing only, so every object in the repo is loose and gc --auto has
// real work to do. It returns the commit id for later history checks.
//
// The gc fixture config is test infrastructure on this test repo only:
// gc.auto 1 makes `git gc --auto` (what GCOnce runs) actually fire on a
// handful of loose objects, and gc.autoDetach false keeps it in the
// foreground so the pass's effect is observable without racing a
// detached gc process.
func seedReachableLooseObjects(t *testing.T, repo string) string {
	t.Helper()
	mustGit(t, repo, "config", "gc.auto", "1")
	mustGit(t, repo, "config", "gc.autoDetach", "false")

	var treeInput strings.Builder
	for i, content := range pickSeventeenSeeds() {
		seed := filepath.Join(t.TempDir(), fmt.Sprintf("seed%d.txt", i))
		if err := os.WriteFile(seed, []byte(content), 0o644); err != nil {
			t.Fatalf("write seed file: %v", err)
		}
		blob := strings.TrimSpace(mustGit(t, repo, "hash-object", "-w", seed))
		if !strings.HasPrefix(blob, "17") {
			t.Fatalf("test bug: blob %s is not in the objects/17 bucket", blob)
		}
		fmt.Fprintf(&treeInput, "100644 blob %s\tseed%d.txt\n", blob, i)
	}
	tree := gitStdin(t, repo, treeInput.String(), "mktree")
	commit := strings.TrimSpace(mustGit(t, repo,
		"-c", "user.name=janitor-test",
		"-c", "user.email=janitor-test@example.com",
		"commit-tree", tree, "-m", "janitor gc seed"))
	mustGit(t, repo, "update-ref", "refs/heads/main", commit)
	return commit
}

// TestGCOncePacksLooseObjectsAndIsIdempotent: one GCOnce pass over a real
// bare repo with loose objects packs them (loose count drops to zero, a
// pack file appears) and returns nil; a second pass is a side-effect-free
// no-op — nil, nothing logged, loose/pack sets unchanged, history intact,
// and the repo still passes git fsck.
func TestGCOncePacksLooseObjectsAndIsIdempotent(t *testing.T) {
	requireGitBin(t)
	dataDir := t.TempDir()
	mgr, err := NewManager(dataDir, nil)
	if err != nil {
		t.Fatalf("git manager: %v", err)
	}
	const wiID = "wi_janitor_test"
	if err := mgr.EnsureBare(wiID); err != nil {
		t.Fatalf("ensure bare: %v", err)
	}
	repo := mgr.RepoDir(wiID)
	commit := seedReachableLooseObjects(t, repo)

	if before := len(looseObjects(t, repo)); before < 4 { // 2 blobs + tree + commit
		t.Fatalf("seeding produced only %d loose objects, want >= 4", before)
	}
	if packs := len(packFiles(t, repo)); packs != 0 {
		t.Fatalf("test bug: seeded repo already contains %d pack file(s)", packs)
	}

	j := NewJanitor(mgr, time.Minute)

	// First pass: gc provably took effect and the pass reports success.
	if err := j.GCOnce(); err != nil {
		t.Fatalf("GCOnce: %v", err)
	}
	if got := len(looseObjects(t, repo)); got != 0 {
		t.Fatalf("GCOnce left %d loose objects unpacked, want 0", got)
	}
	packs := packFiles(t, repo)
	if len(packs) == 0 {
		t.Fatalf("GCOnce packed nothing: no pack file under objects/pack")
	}

	// Second pass: idempotent — nil, silent, and nothing observable moves.
	var secondErr error
	logged := captureLog(t, func() { secondErr = j.GCOnce() })
	if secondErr != nil {
		t.Fatalf("second GCOnce: %v", secondErr)
	}
	if strings.TrimSpace(logged) != "" {
		t.Fatalf("second GCOnce logged %q, want silence", logged)
	}
	if got := len(looseObjects(t, repo)); got != 0 {
		t.Fatalf("second GCOnce left %d loose objects, want 0", got)
	}
	if got := len(packFiles(t, repo)); got != len(packs) {
		t.Fatalf("second GCOnce changed the pack set: %d pack files, want %d", got, len(packs))
	}
	mustGit(t, repo, "fsck", "--strict")
	if head := strings.TrimSpace(mustGit(t, repo, "rev-parse", "refs/heads/main")); head != commit {
		t.Fatalf("main moved across the passes: %s, want %s", head, commit)
	}
}

// TestGCOnceMissingRootIsNoop: a Janitor whose Manager.Root does not
// exist (data dir vanished before the pass) returns nil without error.
func TestGCOnceMissingRootIsNoop(t *testing.T) {
	requireGitBin(t)
	dataDir := t.TempDir()
	mgr, err := NewManager(dataDir, nil)
	if err != nil {
		t.Fatalf("git manager: %v", err)
	}
	// NewManager creates the root; make it missing to simulate a data
	// dir that disappeared before the janitor ran.
	if err := os.RemoveAll(mgr.Root); err != nil {
		t.Fatalf("remove git root: %v", err)
	}
	if err := NewJanitor(mgr, time.Minute).GCOnce(); err != nil {
		t.Fatalf("GCOnce on missing root: %v, want nil", err)
	}
}
