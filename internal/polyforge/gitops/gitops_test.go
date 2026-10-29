package gitops

// gitops_test.go: REAL-git regressions for the capsule chain choke point —
// init→commit→push against a local bare repo, seq trailer monotonicity
// (validated by a pre-receive hook shaped like aihub's), the clean-tree
// path, the session-fingerprint push option, credential delivery over http via the
// one-shot askpass helper (never argv), and stale-lock aging.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
)

// gitArgsProbe, when set, observes every git argv the package builds
// (test seam: proves the push credential never rides argv).
var gitArgsProbe func(args []string)

func init() {
	wrapped := git
	git = func(env []string, dir string, args ...string) (string, error) {
		if gitArgsProbe != nil {
			gitArgsProbe(args)
		}
		return wrapped(env, dir, args...)
	}
	wrappedTimedCtx := gitTimedCtx
	gitTimedCtx = func(ctx context.Context, timeout time.Duration, env []string, dir string, args ...string) (string, error) {
		if gitArgsProbe != nil {
			gitArgsProbe(args)
		}
		return wrappedTimedCtx(ctx, timeout, env, dir, args...)
	}
}

func setIdentity(t *testing.T) {
	t.Helper()
	for _, kv := range [][2]string{
		{"GIT_AUTHOR_NAME", "pf-test"},
		{"GIT_AUTHOR_EMAIL", "pf-test@invalid"},
		{"GIT_COMMITTER_NAME", "pf-test"},
		{"GIT_COMMITTER_EMAIL", "pf-test@invalid"},
	} {
		t.Setenv(kv[0], kv[1])
	}
	// keep the machine's credential helpers out of the http tests
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("HOME", t.TempDir())
}

// mkBare creates the aihub-side bare repo with push options advertised
// (every push from the choke point carries one) and records what the
// pre-receive hook saw.
type testBare struct {
	dir      string
	optsFile string
}

func mkBare(t *testing.T, dir string, hook bool) *testBare {
	t.Helper()
	if out, err := run("git", "init", "--bare", dir); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	for _, args := range [][]string{
		{"git", "--git-dir", dir, "config", "receive.advertisePushOptions", "true"},
		// Production hubs take the shallow carrier push (EnsureBare); a test
		// hub the carrier lane pushes to must model that.
		{"git", "--git-dir", dir, "config", "receive.shallowUpdate", "true"},
	} {
		if out, err := run(args...); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	b := &testBare{dir: dir, optsFile: filepath.Join(dir, "push-options.log")}
	if hook {
		writeTestHook(t, b)
	}
	return b
}

// writeTestHook installs a pre-receive hook that records every push's
// options.
func writeTestHook(t *testing.T, b *testBare) {
	t.Helper()
	script := fmt.Sprintf("#!/bin/sh\necho \"$GIT_PUSH_OPTION_COUNT|$GIT_PUSH_OPTION_0\" >> %q\nexit 0\n", b.optsFile)
	path := filepath.Join(b.dir, "hooks", "pre-receive")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
}

func (b *testBare) pushesSeen(t *testing.T) []string {
	t.Helper()
	raw, err := os.ReadFile(b.optsFile)
	if err != nil {
		return nil
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

// commits counts the bare repo's main commits.
func (b *testBare) commits(t *testing.T) int64 {
	t.Helper()
	out, err := run("git", "--git-dir", b.dir, "rev-list", "--count", "main")
	if err != nil {
		t.Fatalf("bare rev-list: %v: %s", err, out)
	}
	n, _ := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	return n
}

func (b *testBare) hasMain(t *testing.T) bool {
	t.Helper()
	out, err := run("git", "--git-dir", b.dir, "rev-parse", "--verify", "refs/heads/main")
	return err == nil && strings.TrimSpace(out) != ""
}

func run(args ...string) (string, error) {
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	return string(out), err
}

// mkCapsule builds an initialized, externalized capsule worktree.
func mkCapsule(t *testing.T, o Opts) {
	t.Helper()
	if err := initLocal(t, o); err != nil {
		t.Fatalf("initLocal: %v", err)
	}
	if !isPointerFile(t, o.CapsuleDir) {
		t.Fatal("capsule .git must be a pointer file after externalization")
	}
}

func isPointerFile(t *testing.T, capsuleDir string) bool {
	t.Helper()
	st, err := os.Stat(filepath.Join(capsuleDir, ".git"))
	return err == nil && st.Mode().IsRegular()
}

func writeDirty(t *testing.T, o Opts, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(o.CapsuleDir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testOpts(t *testing.T, wi string) (Opts, string) {
	t.Helper()
	base := t.TempDir()
	o := Opts{
		WorkItemID: wi,
		CapsuleDir: filepath.Join(base, "capsules", wi),
		GitDir:     filepath.Join(base, "git", wi+".git"),
		RemoteURL:  filepath.Join(base, "hub", wi+".git"),
		Username:   wi,
		Secret:     "push-secret",
	}
	if err := os.MkdirAll(o.CapsuleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	return o, o.RemoteURL
}

// ---------------------------------------------------------------------------
// commit + push

func TestCommitPushEveryRoundLands(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_seq")
	bare := mkBare(t, barePath, true)
	mkCapsule(t, o)

	for i := 1; i <= 3; i++ {
		writeDirty(t, o, fmt.Sprintf("f%d.txt", i), fmt.Sprintf("round %d\n", i))
		sha, pushed, _, err := CommitPush(context.Background(), o, fmt.Sprintf("observe: execution v%d", i))
		if err != nil || !pushed {
			t.Fatalf("round %d: CommitPush = (%q, %v, %v)", i, sha, pushed, err)
		}
		if sha == "" {
			t.Fatalf("round %d: local SHA missing", i)
		}
		if got := bare.commits(t); got != int64(i) {
			t.Fatalf("round %d: bare main has %d commits, want %d", i, got, i)
		}
	}
	if n := len(bare.pushesSeen(t)); n != 3 {
		t.Fatalf("pushes seen by hook: %d, want 3", n)
	}
}

// A step's own `git commit` in the capsule root lands on the chain as an
// ordinary commit: the next publish pushes it as it is, on top of the
// daemon's history, and the daemon's next commit builds on it.
func TestCommitPushPublishesAForeignCommitAsIs(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_foreign")
	bare := mkBare(t, barePath, true)
	mkCapsule(t, o)
	writeDirty(t, o, "a.txt", "a\n")
	if _, pushed, _, err := CommitPush(context.Background(), o, "observe: v1"); err != nil || !pushed {
		t.Fatalf("CommitPush: pushed=%v err=%v", pushed, err)
	}
	writeDirty(t, o, "agent.txt", "agent\n")
	if out, err := run("git", "-C", o.CapsuleDir, "add", "agent.txt"); err != nil {
		t.Fatalf("agent add: %v: %s", err, out)
	}
	if out, err := run("git", "-C", o.CapsuleDir, "commit", "-q", "-m", "agent's own commit"); err != nil {
		t.Fatalf("agent commit: %v: %s", err, out)
	}
	foreign, err := run("git", "-C", o.CapsuleDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	writeDirty(t, o, "b.txt", "b\n")
	sha, pushed, _, err := CommitPush(context.Background(), o, "observe: v2")
	if err != nil || !pushed {
		t.Fatalf("CommitPush over a foreign commit: pushed=%v err=%v", pushed, err)
	}
	if out, err := run("git", "--git-dir", barePath, "merge-base", "--is-ancestor", strings.TrimSpace(foreign), "main"); err != nil {
		t.Fatalf("the foreign commit is not on the hub's chain as it was: %v: %s", err, out)
	}
	if got, ok := bareRefSHA(t, barePath, "refs/heads/main"); !ok || got != sha {
		t.Fatalf("hub main = %q, want the daemon's commit %q", got, sha)
	}
	if got := bare.commits(t); got != 3 {
		t.Fatalf("hub main has %d commits, want 3", got)
	}
}

// A publish whose context ended while it waited for the per-WI lock
// commits nothing (a local commit it would never push).
func TestCommitPushEndedContextCommitsNothing(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_ended")
	bare := mkBare(t, barePath, true)
	mkCapsule(t, o)
	writeDirty(t, o, "a.txt", "a\n")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, pushed, _, err := CommitPush(ctx, o, "observe: v1"); err == nil || pushed {
		t.Fatalf("an ended context must refuse the publish: pushed=%v err=%v", pushed, err)
	}
	if _, err := run("git", "--git-dir", o.GitDir, "rev-parse", "--verify", "-q", "HEAD"); err == nil {
		t.Fatal("a commit was created under an ended context")
	}
	if bare.hasMain(t) {
		t.Fatal("something was pushed under an ended context")
	}
}

// A step's own commit in the capsule root that records a gitlink is
// refused locally with the typed GIT-412 before the push (the hook would
// refuse it on every push), and nothing reaches the hub.
func TestCommitPushRefusesAnUnpushedGitlinkCommit(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_agentlink")
	bare := mkBare(t, barePath, true)
	mkCapsule(t, o)
	writeDirty(t, o, "a.txt", "a\n")
	if _, pushed, _, err := CommitPush(context.Background(), o, "observe: v1"); err != nil || !pushed {
		t.Fatalf("CommitPush: pushed=%v err=%v", pushed, err)
	}
	if out, err := run("git", "-C", o.CapsuleDir, "update-index", "--add", "--cacheinfo", "160000,0123456789012345678901234567890123456789,vendored"); err != nil {
		t.Fatalf("agent update-index: %v: %s", err, out)
	}
	if out, err := run("git", "-C", o.CapsuleDir, "commit", "-q", "-m", "agent commit with a gitlink"); err != nil {
		t.Fatalf("agent commit: %v: %s", err, out)
	}
	_, pushed, _, err := CommitPush(context.Background(), o, "observe: v2")
	if pushed || !IsLocalGitlink(err) {
		t.Fatalf("want the typed local gitlink refusal, got pushed=%v err=%v", pushed, err)
	}
	if !strings.Contains(err.Error(), "vendored") || !strings.Contains(err.Error(), "git reset --soft") {
		t.Fatalf("the refusal must name the path and the undo: %v", err)
	}
	if got := bare.commits(t); got != 1 {
		t.Fatalf("hub main has %d commits, want only the first", got)
	}
}

func TestCommitPushRetryCarriesTheStandingCommit(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_retry")
	bare := mkBare(t, barePath, true)
	mkCapsule(t, o)

	// Round 1: the remote is dead — the commit lands locally, push fails.
	dead := o
	dead.RemoteURL = filepath.Join(t.TempDir(), "nowhere.git")
	writeDirty(t, o, "a.txt", "a\n")
	sha1, pushed, _, err := CommitPush(context.Background(), dead, "first")
	if err == nil || pushed {
		t.Fatalf("push to a dead remote must fail: (%q, %v, %v)", sha1, pushed, err)
	}
	if sha1 == "" {
		t.Fatal("the local commit must still be reported on push failure")
	}
	// Round 2: a new change against the live remote — the push carries
	// BOTH commits.
	writeDirty(t, o, "b.txt", "b\n")
	sha2, pushed, _, err := CommitPush(context.Background(), o, "second")
	if err != nil || !pushed {
		t.Fatalf("CommitPush = (%q, %v, %v)", sha2, pushed, err)
	}
	if sha2 == sha1 {
		t.Fatal("the second commit must be a new SHA")
	}
	if got := bare.commits(t); got != 2 {
		t.Fatalf("bare main has %d commits, want both", got)
	}
}

func TestCommitPushCleanTreePushesStandingHead(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_clean")
	bare := mkBare(t, barePath, true)
	mkCapsule(t, o)

	writeDirty(t, o, "a.txt", "a\n")
	sha, pushed, _, err := CommitPush(context.Background(), o, "one")
	if err != nil || !pushed {
		t.Fatalf("CommitPush = (%q, %v, %v)", sha, pushed, err)
	}
	pushes := len(bare.pushesSeen(t))

	// Clean tree: no error, the standing HEAD — and an HONEST push
	// result. The re-push of an already-pushed HEAD is an idempotent
	// no-op ("Everything up-to-date": the hook saw nothing more), but
	// pushed reports the truthful server-reached state, not a
	// presumed one.
	sha2, pushed, pt, err := CommitPush(context.Background(), o, "wrap: wrapped")
	if err != nil {
		t.Fatalf("clean tree must not error: %v", err)
	}
	if !pushed {
		t.Fatal("clean tree must report the standing HEAD's push honestly (pushed=true)")
	}
	// Clean tree performs no commit call: commit_ms is the honest zero,
	// while the push round-trip is timed just the same.
	if pt.CommitMS != 0 {
		t.Fatalf("clean tree must carry commit_ms=0 (no commit call), got %d", pt.CommitMS)
	}
	if pt.PushMS < 0 {
		t.Fatalf("push_ms must be non-negative, got %d", pt.PushMS)
	}
	if sha2 != sha {
		t.Fatalf("clean tree must answer the standing HEAD: %q vs %q", sha2, sha)
	}
	if got := len(bare.pushesSeen(t)); got != pushes {
		t.Fatalf("clean tree re-push must be an idempotent no-op: %d pushes, want %d", got, pushes)
	}
}

// TestCommitPushCleanTreeRetryIsHonestAfterFailedPush is the N1
// regression: a commit lands locally, the push fails, and the retry hits
// a CLEAN tree. The retry must attempt the push of the standing HEAD
// (so a still-dead server answers with its error, not a fake success)
// and once the server is back it must report pushed=true with the
// standing SHA — never let a clean tree fake a publish.
func TestCommitPushCleanTreeRetryIsHonestAfterFailedPush(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_cleantree")
	bare := mkBare(t, barePath, true)
	mkCapsule(t, o)

	// Round 1: the remote is dead — the commit lands locally, push fails.
	dead := o
	dead.RemoteURL = filepath.Join(t.TempDir(), "nowhere.git")
	writeDirty(t, o, "a.txt", "a\n")
	sha1, pushed, _, err := CommitPush(context.Background(), dead, "first")
	if err == nil || pushed {
		t.Fatalf("push to a dead remote must fail: (%q, %v, %v)", sha1, pushed, err)
	}
	if sha1 == "" {
		t.Fatal("the local commit must still be reported on push failure")
	}

	// Round 2: the tree is clean, the remote STILL dead — the retry
	// must NOT fake success. It pushes the standing HEAD, fails, and
	// reports the error.
	sha2, pushed, _, err := CommitPush(context.Background(), dead, "retry on clean tree")
	if err == nil {
		t.Fatal("a clean-tree retry against a dead remote must surface the push error")
	}
	if pushed {
		t.Fatal("a failed clean-tree retry must not report pushed=true")
	}
	if sha2 != sha1 {
		t.Fatalf("clean tree must answer the standing HEAD: %q vs %q", sha2, sha1)
	}
	if bare.hasMain(t) {
		t.Fatal("nothing may have reached the server through the dead remote")
	}

	// Round 3: the tree is still clean, the server is back — the retry
	// pushes the standing HEAD through and reports pushed=true.
	sha3, pushed, _, err := CommitPush(context.Background(), o, "retry once the server is back")
	if err != nil || !pushed {
		t.Fatalf("clean-tree retry against the live remote = (%q, %v, %v)", sha3, pushed, err)
	}
	if sha3 != sha1 {
		t.Fatalf("the standing HEAD must be the one pushed: %q vs %q", sha3, sha1)
	}
	if got := bare.commits(t); got != 1 {
		t.Fatalf("bare main has %d commits, want 1", got)
	}
}

// TestCommitPushCleanTreeWithNoHistoryIsTyped covers the practically
// unreachable shape (materialization always leaves untracked files): a
// clean porcelain with no standing HEAD must answer the typed
// ErrEmptyHistory, not raw rev-parse noise.
func TestCommitPushCleanTreeWithNoHistoryIsTyped(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_nohist")
	mkBare(t, barePath, false)
	mkCapsule(t, o) // initialized, unborn branch, empty tree: clean porcelain

	sha, pushed, _, err := CommitPush(context.Background(), o, "nothing anywhere")
	if err == nil {
		t.Fatalf("clean tree with no history must error: (%q, %v)", sha, pushed)
	}
	if !errors.Is(err, ErrEmptyHistory) {
		t.Fatalf("the error must be the typed ErrEmptyHistory, got %v", err)
	}
}

func TestCommitPushFirstCommitOnUnbornBranch(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_unborn")
	bare := mkBare(t, barePath, true)
	mkCapsule(t, o)

	writeDirty(t, o, "first.txt", "first\n")
	if _, pushed, _, err := CommitPush(context.Background(), o, "first"); err != nil || !pushed {
		t.Fatalf("first CommitPush = (%v, %v)", pushed, err)
	}
	if got := bare.commits(t); got != 1 {
		t.Fatalf("bare main has %d commits, want 1", got)
	}
}

// A push carries no push option: session fencing is the hub proxy's
// credential check.
func TestPushCarriesNoPushOption(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_popt")
	bare := mkBare(t, barePath, true)
	mkCapsule(t, o)

	writeDirty(t, o, "a.txt", "a\n")
	if _, pushed, _, err := CommitPush(context.Background(), o, "one"); err != nil || !pushed {
		t.Fatalf("CommitPush = (%v, %v)", pushed, err)
	}
	seen := bare.pushesSeen(t)
	if len(seen) != 1 {
		t.Fatalf("hook invocations: %d, want 1", len(seen))
	}
	if seen[0] != "0|" {
		t.Fatalf("push options seen: %q, want none", seen[0])
	}
}

// ---------------------------------------------------------------------------
// credential delivery: askpass helper, http:// transport, argv hygiene

func TestAskpassScriptShapeAndCleanup(t *testing.T) {
	setIdentity(t)
	o, _ := testOpts(t, "wi_askpass")
	o.RemoteURL = "http://127.0.0.1:1/wi_askpass.git"

	additions, cleanup, err := credentialEnv(o)
	if err != nil {
		t.Fatalf("credentialEnv: %v", err)
	}
	defer cleanup()
	script := ""
	prompt := ""
	for _, kv := range additions {
		if strings.HasPrefix(kv, "GIT_ASKPASS=") {
			script = strings.TrimPrefix(kv, "GIT_ASKPASS=")
		}
		if kv == "GIT_TERMINAL_PROMPT=0" {
			prompt = kv
		}
	}
	if script == "" || prompt == "" {
		t.Fatalf("env additions wrong: %v", additions)
	}
	// the helper lives OUTSIDE the capsule, in a 0700 dir under the git
	// root the startup sweep covers
	if strings.HasPrefix(script, o.CapsuleDir) {
		t.Fatal("the askpass helper must not live inside the capsule")
	}
	if filepath.Dir(script) != filepath.Join(filepath.Dir(o.GitDir), "askpass") {
		t.Fatalf("askpass helper %s is not under the git root's askpass/", script)
	}
	st, err := os.Stat(filepath.Dir(script))
	if err != nil || st.Mode().Perm() != 0o700 {
		t.Fatalf("askpass dir must be 0700, got %v (%v)", st, err)
	}
	sst, err := os.Stat(script)
	if err != nil || sst.Mode().Perm() != 0o700 {
		t.Fatalf("askpass script must be 0700, got %v (%v)", sst, err)
	}
	// the helper answers username and password prompts
	for _, tc := range []struct{ ask, want string }{
		{"Username for 'http://x': ", o.Username},
		{"Password for 'http://x': ", o.Secret},
	} {
		out, err := run(script, tc.ask)
		if err != nil {
			t.Fatalf("askpass %q: %v", tc.ask, err)
		}
		if strings.TrimSpace(out) != tc.want {
			t.Fatalf("askpass %q = %q, want %q", tc.ask, out, tc.want)
		}
	}
	cleanup()
	if _, err := os.Stat(script); !os.IsNotExist(err) {
		t.Fatalf("cleanup must remove the script, stat err = %v", err)
	}
}

func TestCredentialEnvSkipsNonHTTPRemotes(t *testing.T) {
	o, barePath := testOpts(t, "wi_local")
	o.RemoteURL = barePath // local path: no credential consulted
	additions, cleanup, err := credentialEnv(o)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if len(additions) != 0 {
		t.Fatalf("local remote must not build an askpass helper: %v", additions)
	}
}

// TestCredentialEnvRefusesEmptyGitDir (C6): without an external git-dir
// there is no askpass home the startup sweep covers, so the delivery is
// refused and nothing — no temp base, no script — is written anywhere.
func TestCredentialEnvRefusesEmptyGitDir(t *testing.T) {
	o, _ := testOpts(t, "wi_fallback")
	o.GitDir = ""
	o.RemoteURL = "http://127.0.0.1:1/wi_fallback.git"
	tmp := t.TempDir()
	t.Setenv("TMPDIR", tmp)

	additions, cleanup, err := credentialEnv(o)
	cleanup()
	if err == nil || len(additions) != 0 {
		t.Fatalf("an empty GitDir must refuse the delivery: additions %v err %v", additions, err)
	}
	if ents, _ := os.ReadDir(tmp); len(ents) != 0 {
		t.Fatalf("a refused delivery wrote %d entr(ies) under the temp dir", len(ents))
	}
}

// TestSweepAskpassRemovesCrashResidue: a killed daemon leaves one-shot
// askpass scripts (holding push secrets) under <gitRoot>/askpass; the
// startup sweep must remove them, leave unrelated entries alone, and be
// a no-op when the directory does not exist.
func TestSweepAskpassRemovesCrashResidue(t *testing.T) {
	o, _ := testOpts(t, "wi_sweep")
	gitRoot := filepath.Dir(o.GitDir) // <base>/git
	askpassDir := filepath.Join(gitRoot, "askpass")
	if err := os.MkdirAll(askpassDir, 0o700); err != nil {
		t.Fatal(err)
	}
	residue := filepath.Join(askpassDir, "askpass-wi_dead-0123456789abcdef")
	if err := os.WriteFile(residue, []byte("#!/bin/sh\nsecret\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	unrelated := filepath.Join(askpassDir, "keep-me.txt")
	if err := os.WriteFile(unrelated, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := SweepAskpass(gitRoot); err != nil {
		t.Fatalf("SweepAskpass: %v", err)
	}
	if _, serr := os.Stat(residue); !os.IsNotExist(serr) {
		t.Fatal("the crash-residue script must be swept")
	}
	if _, serr := os.Stat(unrelated); serr != nil {
		t.Fatalf("unrelated entries must survive the sweep: %v", serr)
	}
	// a gitRoot without an askpass dir is a clean no-op, not an error
	if err := SweepAskpass(filepath.Join(t.TempDir(), "absent")); err != nil {
		t.Fatalf("SweepAskpass on an absent dir must be a no-op: %v", err)
	}
}

func TestPushArgsNeverCarrySecret(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_argv")
	mkBare(t, barePath, false)
	mkCapsule(t, o)

	var mu atomic.Value // []string
	mu.Store([]string{})
	gitArgsProbe = func(args []string) {
		prev := mu.Load().([]string)
		mu.Store(append(prev, strings.Join(args, "\x00")))
	}
	t.Cleanup(func() { gitArgsProbe = nil })

	writeDirty(t, o, "a.txt", "a\n")
	if _, pushed, _, err := CommitPush(context.Background(), o, "one"); err != nil || !pushed {
		t.Fatalf("CommitPush = (%v, %v)", pushed, err)
	}
	var sawPush bool
	for _, blob := range mu.Load().([]string) {
		args := strings.Split(blob, "\x00")
		for _, a := range args {
			if strings.Contains(a, o.Secret) {
				t.Fatalf("secret found in git argv %q", args)
			}
			if strings.Contains(a, "HEAD:refs/heads/main") {
				sawPush = true
			}
		}
	}
	if !sawPush {
		t.Fatal("the push must carry the refspec")
	}
}

// TestPushAndCloneOverHTTPWithCredential drives the whole chain against a
// REAL git http server: clone over http:// (the default stack's scheme)
// must receive the attempt credential through the askpass helper, and a
// CommitPush must reach the same server.
func TestPushAndCloneOverHTTPWithCredential(t *testing.T) {
	setIdentity(t)
	root := t.TempDir()
	repo := filepath.Join(root, "wi_http.git")
	if out, err := run("git", "init", "--bare", repo); err != nil {
		t.Fatalf("bare init: %v: %s", err, out)
	}
	if out, err := run("git", "--git-dir", repo, "config", "http.receivepack", "true"); err != nil {
		t.Fatalf("receivepack: %v: %s", err, out)
	}
	if out, err := run("git", "--git-dir", repo, "config", "receive.advertisePushOptions", "true"); err != nil {
		t.Fatalf("advertisePushOptions: %v: %s", err, out)
	}
	url := startGitHTTP(t, root)

	base := t.TempDir()
	o := Opts{
		WorkItemID: "wi_http",
		CapsuleDir: filepath.Join(base, "capsules", "wi_http"),
		GitDir:     filepath.Join(base, "git", "wi_http.git"),
		RemoteURL:  url + "/wi_http.git",
		Username:   "wi_http",
		Secret:     "http-push-secret",
	}
	if err := os.MkdirAll(filepath.Dir(o.CapsuleDir), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := Clone(context.Background(), o); err != nil {
		t.Fatalf("clone over http:// must receive the credential: %v", err)
	}
	writeDirty(t, o, "http.txt", "over http\n")
	sha, pushed, _, err := CommitPush(context.Background(), o, "http push")
	if err != nil || !pushed {
		t.Fatalf("CommitPush over http:// = (%q, %v, %v)", sha, pushed, err)
	}
	out, err := run("git", "--git-dir", repo, "rev-parse", "--verify", "refs/heads/main")
	if err != nil || strings.TrimSpace(out) == "" {
		t.Fatalf("the http push never landed (%v): %s", err, out)
	}

	// without a credential the same remote answers 401
	noCred := o
	noCred.Username, noCred.Secret = "", ""
	noCred.CapsuleDir = filepath.Join(base, "capsules", "wi_http2")
	noCred.GitDir = filepath.Join(base, "git", "wi_http2.git")
	if err := Clone(context.Background(), noCred); err == nil {
		t.Fatal("clone without a credential must fail against the authed server")
	}
}

// startGitHTTP serves git http-backend over plain http:// with basic auth.
func startGitHTTP(t *testing.T, root string) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	const user, pass = "wi_http", "http-push-secret"
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		if !ok || u != user || p != pass {
			w.Header().Set("WWW-Authenticate", `Basic realm="pf-test"`)
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		env := append(os.Environ(),
			"GIT_PROJECT_ROOT="+root,
			"GIT_HTTP_EXPORT_ALL=1",
			"PATH_INFO="+r.URL.Path,
			"REQUEST_METHOD="+r.Method,
			"QUERY_STRING="+r.URL.RawQuery,
			"REMOTE_ADDR=127.0.0.1",
			"REMOTE_USER="+user,
			"CONTENT_TYPE="+r.Header.Get("Content-Type"),
		)
		if r.ContentLength >= 0 {
			env = append(env, "CONTENT_LENGTH="+strconv.FormatInt(r.ContentLength, 10))
		}
		cmd := exec.Command("git", "http-backend")
		cmd.Env = env
		cmd.Stdin = r.Body
		out, cerr := cmd.Output()
		if cerr != nil {
			http.Error(w, "http-backend failed", http.StatusInternalServerError)
			return
		}
		sep := []byte("\r\n\r\n")
		head, body := out, []byte{}
		if i := bytes.Index(out, sep); i >= 0 {
			head, body = out[:i], out[i+len(sep):]
		} else {
			if i := bytes.Index(out, []byte("\n\n")); i >= 0 {
				head, body = out[:i], out[i+2:]
			}
		}
		status := http.StatusOK
		for _, line := range bytes.Split(head, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			if bytes.HasPrefix(line, []byte("Status:")) {
				if code, perr := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(string(line), "Status:"))); perr == nil {
					status = code
				}
				continue
			}
			if k, v, found := strings.Cut(string(line), ":"); found {
				w.Header().Add(strings.TrimSpace(k), strings.TrimSpace(v))
			}
		}
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return "http://" + ln.Addr().String()
}

// ---------------------------------------------------------------------------
// stale locks

func TestCleanStaleLocksAges(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_locks")
	mkBare(t, barePath, false)
	mkCapsule(t, o)
	lock := filepath.Join(o.GitDir, "index.lock")

	// A YOUNG lock is left alone (git's lock content is a binary
	// in-progress index, not a PID — nothing proves its holder dead) and
	// the push against it fails loudly.
	if err := os.WriteFile(lock, []byte("in-progress index (binary)"), 0o644); err != nil {
		t.Fatal(err)
	}
	writeDirty(t, o, "a.txt", "a\n")
	_, _, _, err := CommitPush(context.Background(), o, "one")
	if err == nil {
		t.Fatal("a live young lock must surface as a push failure, not be deleted")
	}
	if _, serr := os.Stat(lock); serr != nil {
		t.Fatal("a young lock must NOT be removed")
	}

	// An OLD lock (outlived the bound) is garbage by any honest reading:
	// removed, and the same CommitPush goes through.
	past := time.Now().Add(-(staleLockAge + time.Minute))
	if err := os.Chtimes(lock, past, past); err != nil {
		t.Fatal(err)
	}
	sha, pushed, _, err := CommitPush(context.Background(), o, "one")
	if err != nil || !pushed {
		t.Fatalf("CommitPush after stale-lock removal = (%q, %v, %v)", sha, pushed, err)
	}
	if _, serr := os.Stat(lock); !os.IsNotExist(serr) {
		t.Fatal("the stale lock must be removed")
	}
}

// ---------------------------------------------------------------------------
// Clone: the external git-dir layout in one git call

// Clone lays the capsule out with its git-dir EXTERNAL (a .git pointer
// file, no inner git-dir) on main — from an empty remote (aihub's fresh
// bare repo) and from one with history.
func TestCloneSeparatesGitDirOnMain(t *testing.T) {
	setIdentity(t)
	for _, withHistory := range []bool{false, true} {
		o, barePath := testOpts(t, "wi_clone")
		mkBare(t, barePath, false)
		// aihub's bare repos answer HEAD -> main (aihub/git/repo.go)
		if out, err := run("git", "--git-dir", barePath, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
			t.Fatalf("bare HEAD: %v: %s", err, out)
		}
		if withHistory {
			seed, _ := testOpts(t, "wi_clone_seed")
			seed.RemoteURL = barePath
			mkCapsule(t, seed)
			writeDirty(t, seed, "seed.txt", "seed\n")
			if _, pushed, _, err := CommitPush(context.Background(), seed, "seed"); err != nil || !pushed {
				t.Fatalf("seed push: %v", err)
			}
		}
		if err := os.RemoveAll(o.CapsuleDir); err != nil {
			t.Fatal(err)
		}
		if err := Clone(context.Background(), o); err != nil {
			t.Fatalf("Clone (history=%v): %v", withHistory, err)
		}
		if !isPointerFile(t, o.CapsuleDir) {
			t.Fatal(".git must be a pointer file")
		}
		if !dirExists(o.GitDir) {
			t.Fatalf("external git-dir missing at %s", o.GitDir)
		}
		out, err := git(o.Env(), o.CapsuleDir, "symbolic-ref", "HEAD")
		if err != nil || strings.TrimSpace(out) != "refs/heads/main" {
			t.Fatalf("working branch = %q (%v), want refs/heads/main", out, err)
		}
		if withHistory {
			if _, err := os.Stat(filepath.Join(o.CapsuleDir, "seed.txt")); err != nil {
				t.Fatalf("the clone did not check the history out: %v", err)
			}
		}
	}
}

// repo review 2026-10-16 B-15: the chain push runs under the per-WI lock
// and must be bounded — a hung remote (here: a pre-receive hook that
// never returns) fails the push after chainRemoteTimeout, its process
// group killed, instead of holding the lock forever.
func TestChainPushIsBounded(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_hang")
	mkBare(t, barePath, false)
	marker := filepath.Join(t.TempDir(), "hook-alive")
	hook := "#!/bin/sh\necho $$ > " + marker + "\nexec sleep 300\n"
	if err := os.WriteFile(filepath.Join(barePath, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	mkCapsule(t, o)
	writeDirty(t, o, "f.txt", "x\n")
	old := chainRemoteTimeout
	chainRemoteTimeout = time.Second
	defer func() { chainRemoteTimeout = old }()
	start := time.Now()
	_, pushed, _, err := CommitPush(context.Background(), o, "observe: hang")
	if err == nil || pushed {
		t.Fatalf("a hung remote must fail the push: pushed=%v err=%v", pushed, err)
	}
	if took := time.Since(start); took > 20*time.Second {
		t.Fatalf("push took %s: not bounded", took)
	}
	// the lock is free again
	done := make(chan struct{})
	go func() { Lock(o.WorkItemID)(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the per-WI lock is still held after the bounded push")
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the hook never ran: %v", err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	deadline := time.Now().Add(5 * time.Second)
	for pid > 0 && syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the hook's sleeper %d survived the push timeout", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// The publish context bounds the chain push: when it ends, the hung git
// process group is killed and the per-WI lock is free before CommitPush
// returns — no push outlives its caller (chainRemoteTimeout stays at its
// 10-minute default here).
func TestCommitPushContextKillsTheHungPush(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_ctxhang")
	mkBare(t, barePath, false)
	marker := filepath.Join(t.TempDir(), "hook-alive")
	hook := "#!/bin/sh\necho $$ > " + marker + "\nexec sleep 300\n"
	if err := os.WriteFile(filepath.Join(barePath, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	mkCapsule(t, o)
	writeDirty(t, o, "f.txt", "x\n")
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	start := time.Now()
	_, pushed, _, err := CommitPush(ctx, o, "observe: hang")
	if err == nil || pushed {
		t.Fatalf("an ended context must fail the push: pushed=%v err=%v", pushed, err)
	}
	if took := time.Since(start); took > 20*time.Second {
		t.Fatalf("push took %s after its context ended", took)
	}
	done := make(chan struct{})
	go func() { Lock(o.WorkItemID)(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the per-WI lock is still held after CommitPush returned")
	}
	raw, err := os.ReadFile(marker)
	if err != nil {
		t.Fatalf("the hook never ran: %v", err)
	}
	pid, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	deadline := time.Now().Add(5 * time.Second)
	for pid > 0 && syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			_ = syscall.Kill(pid, syscall.SIGKILL)
			t.Fatalf("the hook's sleeper %d survived the ended context", pid)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// A lost ref-transaction race (an overlapping push of the same ref won)
// is not permanent: the next push re-advertises and lands, so a close
// must retry it rather than give the push up.
func TestRefCasRejectionIsNotPermanent(t *testing.T) {
	cas := &api.Error{Code: api.ErrGitInternal, Detail: "error: cannot lock ref 'refs/heads/main': is at 1 but expected 2\n ! [remote rejected] HEAD -> main (failed to update ref)"}
	if IsPermanentPushRejection(cas) {
		t.Fatal("a lost ref-transaction race must not classify as permanent")
	}
	hook := &api.Error{Code: api.ErrGitInternal, Detail: " ! [remote rejected] HEAD -> main (pre-receive hook declined)"}
	if !IsPermanentPushRejection(hook) {
		t.Fatal("a hook refusal stays permanent")
	}
}

// repo review 2026-10-16 V-5: the operator's global git config must not
// reroute the credentialed chain push (url.*.insteadOf) — it reads no
// system/global config, like the carrier lane.
func TestChainPushIgnoresGlobalGitConfig(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_insteadof")
	bare := mkBare(t, barePath, true)
	decoy := filepath.Join(t.TempDir(), "decoy.git")
	mkBare(t, decoy, false)
	cfg := "[url \"" + decoy + "\"]\n\tinsteadOf = " + barePath + "\n"
	gc := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gc, []byte(cfg), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gc) // the operator's global config
	mkCapsule(t, o)
	writeDirty(t, o, "f.txt", "x\n")
	if _, pushed, _, err := CommitPush(context.Background(), o, "observe: v1"); err != nil || !pushed {
		t.Fatalf("CommitPush: pushed=%v err=%v", pushed, err)
	}
	if got := bare.commits(t); got < 1 {
		t.Fatalf("the real hub repo did not receive the push (%d commits)", got)
	}
	if out, err := run("git", "--git-dir", decoy, "rev-parse", "-q", "--verify", "refs/heads/main"); err == nil {
		t.Fatalf("the push was rerouted to the decoy by ~/.gitconfig: %s", out)
	}
}

// hub transfers abort on a stalled HTTP transfer (review nit on V-5).
func TestHubEnvBoundsStalledTransfers(t *testing.T) {
	t.Setenv("GIT_HTTP_LOW_SPEED_LIMIT", "1")
	env := strings.Join(hubEnv(Opts{}, false, nil), "\n") + "\n"
	for _, want := range []string{"GIT_HTTP_LOW_SPEED_LIMIT=1000\n", "GIT_HTTP_LOW_SPEED_TIME=60\n", "GIT_CONFIG_GLOBAL=/dev/null\n"} {
		if !strings.Contains(env, want) {
			t.Fatalf("hubEnv lacks %q", want)
		}
	}
	if strings.Contains(env, "GIT_HTTP_LOW_SPEED_LIMIT=1\n") {
		t.Fatal("an inherited GIT_* value must not survive")
	}
}

// An in-flight atomicfile temp file (the driver writing execution.json or
// a transcript copy during a publish) is never content: `add -A` skips it,
// so one vanishing between status and add cannot fail the publish. The
// exclusion lives in the git-dir, not in the tracked .gitignore.
func TestCommitPushSkipsInFlightTempFiles(t *testing.T) {
	setIdentity(t)
	o, barePath := testOpts(t, "wi_tmp")
	mkBare(t, barePath, true)
	mkCapsule(t, o)
	writeDirty(t, o, "a.txt", "a\n")
	if err := os.MkdirAll(filepath.Join(o.CapsuleDir, "runtime-state"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeDirty(t, o, "runtime-state/.execution.json.12345.tmp", "{}")
	writeDirty(t, o, ".x.98.tmp", "x")
	if _, pushed, _, err := CommitPush(context.Background(), o, "observe: v1"); err != nil || !pushed {
		t.Fatalf("CommitPush: pushed=%v err=%v", pushed, err)
	}
	out, err := run("git", "--git-dir", barePath, "ls-tree", "-r", "--name-only", "main")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, ".tmp") || !strings.Contains(out, "a.txt") {
		t.Fatalf("published tree:\n%s\nwant a.txt and no temp file", out)
	}
	if _, pushed, _, err := CommitPush(context.Background(), o, "observe: v2"); err != nil || !pushed {
		t.Fatalf("second CommitPush (exclude already present): pushed=%v err=%v", pushed, err)
	}
	if raw, _ := os.ReadFile(filepath.Join(o.CapsuleDir, ".gitignore")); strings.Contains(string(raw), ".tmp") {
		t.Fatal("the tracked .gitignore must not change")
	}
}

// lockEntries answers the per-WI lock map's size.
func lockEntries() int {
	mu.Lock()
	defer mu.Unlock()
	return len(locks)
}

// TestLockMapReleasesEntries pins A2: a released per-WI lock leaves no map
// entry behind, and concurrent holders of one id share one mutex whose
// entry survives until the last of them releases.
func TestLockMapReleasesEntries(t *testing.T) {
	base := lockEntries()
	for i := 0; i < 50; i++ {
		Lock(fmt.Sprintf("wi_lockmap_%d", i))()
	}
	if n := lockEntries(); n != base {
		t.Fatalf("lock map holds %d entries after 50 released locks, want %d", n, base)
	}
	release := Lock("wi_lockmap_shared")
	acquired := make(chan func())
	go func() { acquired <- Lock("wi_lockmap_shared") }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		refs := locks["wi_lockmap_shared"].refs
		mu.Unlock()
		if refs == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the waiter never registered on the shared entry (refs %d)", refs)
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case <-acquired:
		t.Fatal("the second holder got the lock while the first still holds it")
	case <-time.After(20 * time.Millisecond):
	}
	release()
	second := <-acquired
	if n := lockEntries(); n != base+1 {
		t.Fatalf("the shared entry vanished while a holder remains: %d entries, want %d", n, base+1)
	}
	second()
	if n := lockEntries(); n != base {
		t.Fatalf("lock map holds %d entries after the last release, want %d", n, base)
	}
}
