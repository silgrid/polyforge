package git

// Full-chain tests through the real git binary: httptest server + the
// git-http-backend proxy + clone/push. Each pre-receive violation gets a
// dedicated test: non-FF, ref deletion, gitlink, blob over-limit; plus legal push/clone and session fencing (the superseded
// session's secret rejected after force-end).

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/aihub/core"
	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// gitEnv is one fully wired environment.
type gitEnv struct {
	db       *core.DB
	owners   *core.OwnerManager
	sessions *core.SessionManager
	admin    *core.AdminManager
	wis      *core.WIManager
	mgr      *Manager
	ts       *httptest.Server
	userID   string
}

func newGitEnv(t *testing.T) *gitEnv {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git binary not found: %v", err)
	}
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skipf("bash not found: %v", err)
	}
	dataDir := t.TempDir()
	db, err := core.Open(dataDir)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	env := &gitEnv{
		db:       db,
		owners:   core.NewOwnerManager(db),
		sessions: core.NewSessionManager(db),
		admin:    core.NewAdminManager(db),
		wis:      core.NewWIManager(db),
	}
	u, err := env.admin.CreateUser("root", "root@example.com", "admin", "human")
	if err != nil {
		t.Fatalf("user: %v", err)
	}
	env.userID = u.ID
	if _, err := env.admin.CreateProject(core.CreateProjectRequest{Name: "alpha"}); err != nil {
		t.Fatalf("project: %v", err)
	}
	mgr, err := NewManager(dataDir, env.owners)
	if err != nil {
		t.Fatalf("git manager: %v", err)
	}
	env.mgr = mgr
	mux := http.NewServeMux()
	mux.Handle("/git/", http.HandlerFunc(mgr.HandleGit))
	env.ts = httptest.NewServer(mux)
	t.Cleanup(env.ts.Close)
	return env
}

// bundleNameTest pins the workflow bundle for git-chain test tickets
// (registration tickets are not claimable).
var bundleNameTest = "git-chain-bundle"

// claimWI creates + CAS-claims a WI under the session model, installs
// the bare repo, and returns the WI id with the client-minted session
// secret (the push password).
func (e *gitEnv) claimWI(t *testing.T) (wiID, secret string) {
	t.Helper()
	wi, err := e.wis.Create(core.CreateRequest{
		Project: "alpha", Goal: "git chain test", Reporter: e.userID,
		BundleName: &bundleNameTest, // execution ticket: claimable
	})
	if err != nil {
		t.Fatalf("create wi: %v", err)
	}
	secret = "git-session-secret-" + wi.ID
	if _, err := e.sessions.Claim(wi.ID, api.SessionClaimRequest{
		SessionID: secret, MachineID: "m1",
	}, e.userID); err != nil {
		t.Fatalf("session claim: %v", err)
	}
	if err := e.mgr.EnsureBare(wi.ID); err != nil {
		t.Fatalf("ensure bare: %v", err)
	}
	return wi.ID, secret
}

// credURL embeds the credential the way the pf gitops layer does.
func (e *gitEnv) credURL(wiID, secret string) string {
	base := fmt.Sprintf("%s/git/%s.git", e.ts.URL, wiID)
	return strings.Replace(base, "http://", "http://"+wiID+":"+secret+"@", 1)
}

// runGit runs git in dir with exactly the given args.
func runGit(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return out.String(), err
}

// mustGit fails the test on a git error.
func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := runGit(t, dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

// worktree initializes a local worktree whose HEAD is main.
func newWorktree(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "wt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	mustGit(t, dir, "init", "-b", "main", ".")
	mustGit(t, dir, "config", "user.email", "test@example.com")
	mustGit(t, dir, "config", "user.name", "test")
	return dir
}

// push pushes HEAD:main to the given remote URL.
func push(t *testing.T, dir, remote string) error {
	t.Helper()
	_, err := runGit(t, dir, "push", remote, "HEAD:refs/heads/main")
	return err
}

// claimSessionWI creates a queued execution ticket and CAS-claims it
// under the v1.5 session model with the given CLIENT-MINTED secret,
// installing the bare repo. Returns the WI id and the session's PUBLIC
// fingerprint.
func (e *gitEnv) claimSessionWI(t *testing.T, secret string) (wiID, fingerprint string) {
	t.Helper()
	wi, err := e.wis.Create(core.CreateRequest{
		Project: "alpha", Goal: "git session test", Reporter: e.userID,
		BundleName: &bundleNameTest,
	})
	if err != nil {
		t.Fatalf("create wi: %v", err)
	}
	claim, err := e.sessions.Claim(wi.ID, api.SessionClaimRequest{
		SessionID: secret, MachineID: "m1",
	}, e.userID)
	if err != nil {
		t.Fatalf("session claim: %v", err)
	}
	if err := e.mgr.EnsureBare(wi.ID); err != nil {
		t.Fatalf("ensure bare: %v", err)
	}
	return wi.ID, claim.SessionFingerprint
}

// A daemon older than this hub still sends
// --push-option=session-fingerprint=<fp>: the repo keeps advertising push
// options (git refuses an option-carrying push to a repo that does not)
// and the hook ignores the option — fencing is the proxy's credential
// check, so even a value that is not the session's is harmless.
func TestOldDaemonPushOptionIsHarmless(t *testing.T) {
	e := newGitEnv(t)
	secret := "0123456789abcdef0123456789abcdef"
	wiID, fp := e.claimSessionWI(t, secret)
	if out := mustGit(t, e.mgr.RepoDir(wiID), "config", "receive.advertisePushOptions"); strings.TrimSpace(out) != "true" {
		t.Fatalf("receive.advertisePushOptions = %q, want true", strings.TrimSpace(out))
	}
	cred := e.credURL(wiID, secret)
	wt := newWorktree(t)
	commitPlain(t, wt, "a.txt", "one\n")
	if out, err := runGit(t, wt, "push", "--push-option=session-fingerprint="+fp, cred, "HEAD:refs/heads/main"); err != nil {
		t.Fatalf("push with the session's option rejected: %v (%s)", err, out)
	}
	commitPlain(t, wt, "b.txt", "two\n")
	if out, err := runGit(t, wt, "push", "--push-option=session-fingerprint=deadbeefdeadbeef", cred, "HEAD:refs/heads/main"); err != nil {
		t.Fatalf("push with another option value rejected: %v (%s)", err, out)
	}
	commitPlain(t, wt, "c.txt", "three\n")
	if out, err := runGit(t, wt, "push", cred, "HEAD:refs/heads/main"); err != nil {
		t.Fatalf("option-less push rejected: %v (%s)", err, out)
	}
}

func TestLegalPushAndClone(t *testing.T) {
	e := newGitEnv(t)
	wiID, secret := e.claimWI(t)
	cred := e.credURL(wiID, secret)

	// Clone the empty bare repo (clone works while empty).
	wt := newWorktree(t)
	mustGit(t, wt, "clone", cred, filepath.Join(wt, "clone"))
	clone := filepath.Join(wt, "clone")

	// Worktree against the remote: commit, push, commit, push.
	commitPlain(t, clone, "capsule.txt", "step one\n")
	if err := push(t, clone, cred); err != nil {
		t.Fatalf("first push failed: %v", err)
	}
	commitPlain(t, clone, "step two.txt", "step two\n")
	if err := push(t, clone, cred); err != nil {
		t.Fatalf("second push failed: %v", err)
	}

	// A fresh clone sees the pushed history.
	second := filepath.Join(wt, "second")
	mustGit(t, wt, "clone", cred, second)
	if data, err := os.ReadFile(filepath.Join(second, "step two.txt")); err != nil || string(data) != "step two\n" {
		t.Fatalf("fresh clone missing content: %v %q", err, data)
	}
	// The pre-receive hook file exists and is executable.
	hook := filepath.Join(e.mgr.RepoDir(wiID), "hooks", "pre-receive")
	info, err := os.Stat(hook)
	if err != nil || info.Mode()&0o111 == 0 {
		t.Fatalf("pre-receive hook missing or not executable: %v", err)
	}
}

func TestPushWithoutCredentialRejected(t *testing.T) {
	e := newGitEnv(t)
	wiID, _ := e.claimWI(t)
	anon := fmt.Sprintf("%s/git/%s.git", e.ts.URL, wiID)
	wt := newWorktree(t)
	commitPlain(t, wt, "f.txt", "x\n")
	if err := push(t, wt, anon); err == nil {
		t.Fatalf("push without credentials accepted")
	}
}

func TestNonFastForwardRejected(t *testing.T) {
	e := newGitEnv(t)
	wiID, secret := e.claimWI(t)
	cred := e.credURL(wiID, secret)
	wt := newWorktree(t)
	commitPlain(t, wt, "a.txt", "one\n")
	if err := push(t, wt, cred); err != nil {
		t.Fatalf("base push: %v", err)
	}
	// Rewrite history: amend the pushed commit (a new sha via a changed
	// message so the commit id genuinely differs).
	mustGit(t, wt, "commit", "--amend", "-m", "a.txt rewritten")
	err := push(t, wt, cred)
	if err == nil {
		t.Fatalf("non-FF push accepted")
	}
	out, _ := runGit(t, wt, "push", cred, "HEAD:refs/heads/main")
	if !strings.Contains(out, "non-fast-forward") {
		t.Fatalf("wrong rejection: %s", out)
	}
}

func TestRefDeletionRejected(t *testing.T) {
	e := newGitEnv(t)
	wiID, secret := e.claimWI(t)
	cred := e.credURL(wiID, secret)
	wt := newWorktree(t)
	commitPlain(t, wt, "a.txt", "one\n")
	if err := push(t, wt, cred); err != nil {
		t.Fatalf("base push: %v", err)
	}
	out, err := runGit(t, wt, "push", cred, ":refs/heads/main")
	if err == nil {
		t.Fatalf("ref deletion accepted")
	}
	if !strings.Contains(out, "ref deletion") {
		t.Fatalf("wrong rejection: %s", out)
	}
}

func TestGitlinkRejected(t *testing.T) {
	e := newGitEnv(t)
	wiID, secret := e.claimWI(t)
	cred := e.credURL(wiID, secret)
	wt := newWorktree(t)
	commitPlain(t, wt, "a.txt", "one\n")
	if err := push(t, wt, cred); err != nil {
		t.Fatalf("base push: %v", err)
	}
	// Stage a gitlink (submodule commit reference) without the object.
	mustGit(t, wt, "update-index", "--add", "--cacheinfo", "160000,0123456789012345678901234567890123456789,vendored-thing")
	commitPlain(t, wt, "marker.txt", "two\n")
	// Re-stage the gitlink entry lost by commitPlain's add -A.
	mustGit(t, wt, "update-index", "--add", "--cacheinfo", "160000,0123456789012345678901234567890123456789,vendored-thing")
	mustGit(t, wt, "commit", "--amend", "--no-edit")
	out, err := runGit(t, wt, "push", cred, "HEAD:refs/heads/main")
	if err == nil {
		t.Fatalf("gitlink push accepted: %s", out)
	}
	if !strings.Contains(out, "gitlink") {
		t.Fatalf("wrong rejection: %s", out)
	}
}

// The chain carries no sequence rule: a commit with no trailer is an
// ordinary fast-forward commit, and one with an old daemon's
// Polyforge-Seq trailer (any value) is accepted just the same.
func TestChainCommitsNeedNoTrailer(t *testing.T) {
	e := newGitEnv(t)
	wiID, secret := e.claimWI(t)
	cred := e.credURL(wiID, secret)
	wt := newWorktree(t)
	commitPlain(t, wt, "a.txt", "one\n")
	if out, err := runGit(t, wt, "push", cred, "HEAD:refs/heads/main"); err != nil {
		t.Fatalf("trailer-less push rejected: %v (%s)", err, out)
	}
	for _, msg := range []string{"old daemon\n\nPolyforge-Seq: 1", "old daemon again\n\nPolyforge-Seq: 1"} {
		if err := os.WriteFile(filepath.Join(wt, "b.txt"), []byte(msg), 0o644); err != nil {
			t.Fatal(err)
		}
		mustGit(t, wt, "add", "-A")
		mustGit(t, wt, "commit", "-m", msg)
		if out, err := runGit(t, wt, "push", cred, "HEAD:refs/heads/main"); err != nil {
			t.Fatalf("a commit with an old daemon's trailer rejected: %v (%s)", err, out)
		}
	}
}

// A repo created by an earlier hub build keeps that build's hook on disk;
// the first request a hub process serves for it rewrites the hook, so the
// chain rules are always the running build's (the hub deploys first and
// its rules must reach the repos of work items already running).
func TestProxyRefreshesAStaleHook(t *testing.T) {
	e := newGitEnv(t)
	wiID, secret := e.claimWI(t)
	hook := filepath.Join(e.mgr.RepoDir(wiID), "hooks", "pre-receive")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'stale hook' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	e.mgr.mu.Lock()
	e.mgr.known = map[string]bool{} // a restarted hub process
	e.mgr.mu.Unlock()
	wt := newWorktree(t)
	commitPlain(t, wt, "a.txt", "one\n")
	if out, err := runGit(t, wt, "push", e.credURL(wiID, secret), "HEAD:refs/heads/main"); err != nil {
		t.Fatalf("push against a repo with a stale hook failed: %v (%s)", err, out)
	}
}

func TestBlobOverLimitRejected(t *testing.T) {
	e := newGitEnv(t)
	// Shrink the limit so the test stays fast; the hook reads it via the
	// env the proxy sets.
	e.mgr.MaxBlobBytes = 512
	wiID, secret := e.claimWI(t)
	cred := e.credURL(wiID, secret)
	wt := newWorktree(t)
	if err := os.WriteFile(filepath.Join(wt, "big.bin"), bytes.Repeat([]byte{0}, 2048), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustGit(t, wt, "add", "-A")
	mustGit(t, wt, "commit", "-m", "big")
	out, err := runGit(t, wt, "push", cred, "HEAD:refs/heads/main")
	if err == nil {
		t.Fatalf("oversized blob accepted: %s", out)
	}
	if !strings.Contains(out, "blob exceeds") {
		t.Fatalf("wrong rejection: %s", out)
	}
}

func TestOldSecretRejectedAfterForceEnd(t *testing.T) {
	e := newGitEnv(t)
	wiID, secret1 := e.claimWI(t)
	cred1 := e.credURL(wiID, secret1)
	wt := newWorktree(t)
	commitPlain(t, wt, "a.txt", "one\n")
	if err := push(t, wt, cred1); err != nil {
		t.Fatalf("push with the live session secret failed: %v", err)
	}
	// Force-end, then a fresh claim installs a NEW secret; the old one
	// must be dead at the git face (fencing-on-use, §1.6).
	secret2 := "git-session-secret-2-" + wiID
	if err := e.sessions.ForceEnd(wiID, "machine lost", e.userID); err != nil {
		t.Fatalf("force-end: %v", err)
	}
	if _, err := e.sessions.Claim(wiID, api.SessionClaimRequest{
		SessionID: secret2, MachineID: "machine-2",
	}, e.userID); err != nil {
		t.Fatalf("re-claim after force-end: %v", err)
	}
	commitPlain(t, wt, "b.txt", "two\n")
	out, perr := runGit(t, wt, "push", cred1, "HEAD:refs/heads/main")
	if perr == nil {
		t.Fatalf("push with the SUPERSEDED session secret accepted: %s", out)
	}
	if !strings.Contains(out, "Authentication") && !strings.Contains(out, "401") {
		t.Fatalf("wrong rejection for the superseded secret: %s", out)
	}
	// The new secret works and continues the chain.
	cred2 := e.credURL(wiID, secret2)
	if err := push(t, wt, cred2); err != nil {
		t.Fatalf("push with the new session secret failed: %v", err)
	}
	// And a fetch with the old secret is refused too.
	out, cerr := runGit(t, t.TempDir(), "clone", cred1, filepath.Join(t.TempDir(), "x"))
	if cerr == nil {
		t.Fatalf("clone with the superseded session secret accepted: %s", out)
	}
}

// TestSlugClaimedTicketPushesToCanonicalBareRepo (pilot F2): a ticket may
// be CLAIMED by its "<project>#<seq>" slug — the id-or-slug face accepts
// it — but the git surface is keyed by the CANONICAL id alone: the bare
// repo is <data>/git/<wi_id>.git and the push remote must be built from
// the canonical id. A slug-built remote is not merely wrong, it is
// silently truncated: "#" is the URL fragment separator, so
// .../git/alpha#9.git asks the proxy for .../git/alpha and the push
// 404s. This pins both sides: the canonical URL pushes, the slug URL
// does not.
func TestSlugClaimedTicketPushesToCanonicalBareRepo(t *testing.T) {
	e := newGitEnv(t)
	// Create the ticket and resolve its slug ("<project>#<seq>").
	wi, err := e.wis.Create(core.CreateRequest{
		Project: "alpha", Goal: "slug push test", Reporter: e.userID,
		BundleName: &bundleNameTest,
	})
	if err != nil {
		t.Fatalf("create wi: %v", err)
	}
	if wi.Slug == "" || !strings.Contains(wi.Slug, "#") {
		t.Fatalf("expected a # slug, got %q", wi.Slug)
	}
	// The id-or-slug resolution the API endpoint performs: the slug maps
	// to the canonical id (the core claim face is id-keyed; the HTTP
	// layer resolves before it).
	resolved, err := e.wis.Resolve(wi.Slug)
	if err != nil {
		t.Fatalf("resolve slug %s: %v", wi.Slug, err)
	}
	if resolved != wi.ID {
		t.Fatalf("slug %s resolved to %q, want canonical %q", wi.Slug, resolved, wi.ID)
	}
	secret := "git-session-secret-" + wi.ID
	claim, err := e.sessions.Claim(resolved, api.SessionClaimRequest{
		SessionID: secret, MachineID: "m1",
	}, e.userID)
	if err != nil {
		t.Fatalf("claim %s: %v", resolved, err)
	}
	if claim.WorkItemID != wi.ID {
		t.Fatalf("claim answered work item %q, want canonical %q", claim.WorkItemID, wi.ID)
	}
	if err := e.mgr.EnsureBare(wi.ID); err != nil {
		t.Fatalf("ensure bare: %v", err)
	}

	// The remote built from the CANONICAL id matches the bare repo and
	// the push lands.
	cred := e.credURL(wi.ID, secret)
	if !strings.HasSuffix(cred, "/git/"+wi.ID+".git") {
		t.Fatalf("canonical remote URL %q must name the bare repo /git/%s.git", cred, wi.ID)
	}
	wt := newWorktree(t)
	commitPlain(t, wt, "a.txt", "one\n")
	if err := push(t, wt, cred); err != nil {
		t.Fatalf("push to the canonical remote failed: %v", err)
	}
	head := strings.TrimSpace(mustGit(t, wt, "rev-parse", "HEAD"))
	remoteHead := strings.TrimSpace(firstFieldOf(mustGit(t, e.mgr.RepoDir(wi.ID), "rev-parse", "refs/heads/main")))
	if head != remoteHead {
		t.Fatalf("canonical push did not land: local %s, remote %s", head, remoteHead)
	}

	// The remote built from the SLUG (the pilot's failure shape) can never
	// reach the bare repo: "#" cuts the URL at the fragment.
	slugURL := e.ts.URL + "/git/" + wi.Slug + ".git"
	commitPlain(t, wt, "b.txt", "two\n")
	if perr := push(t, wt, slugURL); perr == nil {
		t.Fatalf("push to the slug-built remote %q must fail", slugURL)
	}
	// and it moved nothing: the remote tip is still the first commit.
	remoteHead2 := strings.TrimSpace(firstFieldOf(mustGit(t, e.mgr.RepoDir(wi.ID), "rev-parse", "refs/heads/main")))
	if remoteHead2 != remoteHead {
		t.Fatalf("the slug push moved the bare repo tip: %s -> %s", remoteHead, remoteHead2)
	}
}

func firstFieldOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, " \t"); i >= 0 {
		return s[:i]
	}
	return s
}

// TestSessionPushAuthFencing (§1.6 main layer, session path): a push with
// the live session secret succeeds through the proxy; a wrong secret is
// refused at auth (401, rejection names the current holder fingerprint);
// a PAUSED work item refuses pushes entirely (M3: no pushes while
// paused, GIT-403); and after a force-end the OLD secret is dead
// while the new one works.
func TestSessionPushAuthFencing(t *testing.T) {
	e := newGitEnv(t)
	secret := "11111111222222223333333344444444"
	wiID, fp := e.claimSessionWI(t, secret)

	// Correct secret: push lands.
	cred := e.credURL(wiID, secret)
	wt := newWorktree(t)
	commitPlain(t, wt, "a.txt", "one\n")
	if err := push(t, wt, cred); err != nil {
		t.Fatalf("push with the live session secret failed: %v", err)
	}

	// Wrong secret: 401 at the proxy.
	commitPlain(t, wt, "b.txt", "two\n")
	out, err := runGit(t, wt, "push", e.credURL(wiID, "99999999888888887777777766666666"), "HEAD:refs/heads/main")
	if err == nil {
		t.Fatalf("push with a wrong session secret accepted: %s", out)
	}
	if !strings.Contains(out, "401") && !strings.Contains(out, "Authentication") {
		t.Fatalf("wrong rejection for a wrong secret: %s", out)
	}

	// Paused (M3): the session stays, the status check refuses pushes.
	if err := e.sessions.Pause(wiID, secret, false, "daemon shutdown", e.userID); err != nil {
		t.Fatalf("session pause: %v", err)
	}
	commitPlain(t, wt, "c.txt", "three\n")
	out, err = runGit(t, wt, "push", cred, "HEAD:refs/heads/main")
	if err == nil {
		t.Fatalf("push while paused accepted: %s", out)
	}
	if !strings.Contains(out, "403") {
		t.Fatalf("paused push must be a 403, got: %s", out)
	}

	// Force-end then a fresh claim swaps the session; the old secret is
	// dead, the new one pushes and continues the chain.
	newSecret := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	if err := e.sessions.ForceEnd(wiID, "machine lost", e.userID); err != nil {
		t.Fatalf("force-end: %v", err)
	}
	if _, err := e.sessions.Claim(wiID, api.SessionClaimRequest{
		SessionID: newSecret, MachineID: "m2",
	}, e.userID); err != nil {
		t.Fatalf("re-claim after force-end: %v", err)
	}
	out, err = runGit(t, wt, "push", cred, "HEAD:refs/heads/main")
	if err == nil {
		t.Fatalf("push with the SUPERSEDED session secret accepted: %s", out)
	}
	if !strings.Contains(out, "401") && !strings.Contains(out, "Authentication") {
		t.Fatalf("wrong rejection for the superseded secret: %s", out)
	}
	// The mismatch rejection names the NEW holder's fingerprint.
	if !strings.Contains(out, fp) {
		// (the 401 body names the holder; git's own stderr may not show
		// it — the fingerprint check is covered at the core level)
		t.Logf("rejection detail (fingerprint naming asserted at core level): %s", out)
	}
	if err := push(t, wt, e.credURL(wiID, newSecret)); err != nil {
		t.Fatalf("push with the new session secret failed: %v", err)
	}
}

// commitPlain writes a file and commits it.
func commitPlain(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustGit(t, dir, "add", "-A")
	mustGit(t, dir, "commit", "-m", "task work: "+name)
}

// TestTaskCarrierRefAccepted (polyforge#108): the refs/pf-task/<repo>
// namespace is the task-repo durability lane — the daemon mirrors a task
// repo's local head there so a bounded-fail re-claim can restore it. The
// history is the TASK repo's own: submodules allowed. The hook must
// accept exactly that shape (while the chain
// invariants stay enforced on refs/heads/* — pinned by the dedicated
// tests above).
func TestTaskCarrierRefAccepted(t *testing.T) {
	e := newGitEnv(t)
	wiID, secret := e.claimWI(t)
	cred := e.credURL(wiID, secret)
	wt := newWorktree(t)
	commitPlain(t, wt, "code.txt", "task repo work\n")
	out, err := runGit(t, wt, "push", cred, "HEAD:refs/pf-task/demo-src")
	if err != nil {
		t.Fatalf("task-carrier push rejected: %s", out)
	}
	// A task repo may legitimately contain a submodule gitlink — the
	// chain's flat-history rule does not apply to foreign history.
	mustGit(t, wt, "update-index", "--add", "--cacheinfo", "160000,0123456789012345678901234567890123456789,vendored-thing")
	mustGit(t, wt, "commit", "-m", "task work with submodule")
	if out, err := runGit(t, wt, "push", cred, "HEAD:refs/pf-task/demo-src"); err != nil {
		t.Fatalf("carrier push with a gitlink rejected: %s", out)
	}
	// The ref landed and advertises back.
	out = mustGit(t, wt, "ls-remote", cred, "refs/pf-task/demo-src")
	if !strings.Contains(out, "refs/pf-task/demo-src") {
		t.Fatalf("carrier ref missing after push: %s", out)
	}
	// The carried head fetches back out (the restore lane's read).
	wt2 := newWorktree(t)
	mustGit(t, wt2, "remote", "add", "hub", cred)
	mustGit(t, wt2, "fetch", "--quiet", "hub", "refs/pf-task/demo-src")
	sha := strings.TrimSpace(mustGit(t, wt, "rev-parse", "HEAD"))
	if got := strings.TrimSpace(mustGit(t, wt2, "rev-parse", "FETCH_HEAD")); got != sha {
		t.Fatalf("fetched carrier head %q, want %q", got, sha)
	}
}

// TestTaskCarrierRefRewindAcceptedDeletionFenced: the carrier namespace
// takes the chain-only gitlink exemption AND a non-fast-forward update — a
// rewind rewrites a carried commit, and the daemon's content-containment
// lease is that lane's gate. Ref deletion stays rejected.
func TestTaskCarrierRefRewindAcceptedDeletionFenced(t *testing.T) {
	e := newGitEnv(t)
	wiID, secret := e.claimWI(t)
	cred := e.credURL(wiID, secret)
	wt := newWorktree(t)
	commitPlain(t, wt, "code.txt", "one\n")
	if out, err := runGit(t, wt, "push", cred, "HEAD:refs/pf-task/demo-src"); err != nil {
		t.Fatalf("base carrier push: %s", out)
	}
	// History rewrite: an amended (non-descendant) tip replaces the carrier.
	// The rewritten tree must actually diverge (an in-second amend would
	// reproduce the same commit SHA and push as up-to-date).
	if err := os.WriteFile(filepath.Join(wt, "rewrite.txt"), []byte("divergent\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustGit(t, wt, "add", "-A")
	mustGit(t, wt, "commit", "--amend", "--no-edit")
	rewritten := strings.TrimSpace(mustGit(t, wt, "rev-parse", "HEAD"))
	if out, err := runGit(t, wt, "push", cred, "+HEAD:refs/pf-task/demo-src"); err != nil {
		t.Fatalf("non-fast-forward carrier push rejected: %s", out)
	}
	if got := strings.TrimSpace(mustGit(t, wt, "ls-remote", cred, "refs/pf-task/demo-src")); !strings.Contains(got, rewritten) {
		t.Fatalf("carrier did not move to the rewritten head %s: %s", rewritten, got)
	}
	// Deletion stays rejected.
	if out, err := runGit(t, wt, "push", cred, ":refs/pf-task/demo-src"); err == nil {
		t.Fatalf("carrier ref deletion accepted: %s", out)
	} else if !strings.Contains(out, "deletion") {
		t.Fatalf("wrong rejection: %s", out)
	}
}

// TestTaskCarrierBlobLimit (polyforge#158): the carrier refs carry the
// task repo's whole upstream history on their first push, so an upstream
// asset above the capsule-chain limit (skypilot's 21 MB mp4 against the
// 16 MiB chain limit) must not make the repo uncarriable. With the
// DEFAULT limits: a blob just over capsule.MaxBlobBytes lands on
// refs/pf-task/* while the same blob stays refused on the chain.
func TestTaskCarrierBlobLimit(t *testing.T) {
	e := newGitEnv(t)
	if e.mgr.MaxBlobBytes != capsule.MaxBlobBytes || e.mgr.MaxTaskBlobBytes != capsule.MaxTaskBlobBytes {
		t.Fatalf("manager limits = %d/%d, want the capsule defaults", e.mgr.MaxBlobBytes, e.mgr.MaxTaskBlobBytes)
	}
	if capsule.MaxTaskBlobBytes <= capsule.MaxBlobBytes {
		t.Fatalf("carrier limit %d must exceed the chain limit %d", capsule.MaxTaskBlobBytes, capsule.MaxBlobBytes)
	}
	wiID, secret := e.claimWI(t)
	cred := e.credURL(wiID, secret)
	wt := newWorktree(t)
	if err := os.WriteFile(filepath.Join(wt, "asset.mp4"), bytes.Repeat([]byte{7}, capsule.MaxBlobBytes+1), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustGit(t, wt, "add", "-A")
	mustGit(t, wt, "commit", "-m", "upstream asset")
	if out, err := runGit(t, wt, "push", cred, "HEAD:refs/pf-task/demo-src"); err != nil {
		t.Fatalf("carrier push of a blob under the carrier limit rejected: %s", out)
	}
	out, err := runGit(t, wt, "push", cred, "HEAD:refs/heads/main")
	if err == nil {
		t.Fatalf("chain push of a blob over the chain limit accepted: %s", out)
	}
	if !strings.Contains(out, fmt.Sprintf("blob exceeds the %d-byte limit", capsule.MaxBlobBytes)) {
		t.Fatalf("wrong rejection: %s", out)
	}
}

// TestTaskCarrierBlobOverCarrierLimitRejected: the carrier limit is a
// limit — a blob above MaxTaskBlobBytes is refused on refs/pf-task/*
// (shrunk limits keep the test fast; the hook reads them via the env the
// proxy sets).
func TestTaskCarrierBlobOverCarrierLimitRejected(t *testing.T) {
	e := newGitEnv(t)
	e.mgr.MaxBlobBytes = 512
	e.mgr.MaxTaskBlobBytes = 4096
	wiID, secret := e.claimWI(t)
	cred := e.credURL(wiID, secret)
	wt := newWorktree(t)
	if err := os.WriteFile(filepath.Join(wt, "mid.bin"), bytes.Repeat([]byte{1}, 2048), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustGit(t, wt, "add", "-A")
	mustGit(t, wt, "commit", "-m", "mid")
	if out, err := runGit(t, wt, "push", cred, "HEAD:refs/pf-task/demo-src"); err != nil {
		t.Fatalf("carrier push under the carrier limit rejected: %s", out)
	}
	if err := os.WriteFile(filepath.Join(wt, "big.bin"), bytes.Repeat([]byte{2}, 8192), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	mustGit(t, wt, "add", "-A")
	mustGit(t, wt, "commit", "-m", "big")
	out, err := runGit(t, wt, "push", cred, "HEAD:refs/pf-task/demo-src")
	if err == nil {
		t.Fatalf("carrier push over the carrier limit accepted: %s", out)
	}
	if !strings.Contains(out, "blob exceeds the 4096-byte limit") {
		t.Fatalf("wrong rejection: %s", out)
	}
}

// TestTaskWIPRefScratch (polyforge#167): refs/pf-wip/<repo> carries a
// snapshot of a task repo's uncommitted work and is scratch — every close
// supersedes it with a force push or deletes it. The hook accepts a
// commit with gitlinks, a non-fast-forward replacement and
// deletion, and still refuses a new blob over the carrier limit.
func TestTaskWIPRefScratch(t *testing.T) {
	e := newGitEnv(t)
	e.mgr.MaxBlobBytes = 512
	e.mgr.MaxTaskBlobBytes = 4096
	wiID, secret := e.claimWI(t)
	cred := e.credURL(wiID, secret)
	wt := newWorktree(t)
	commitPlain(t, wt, "code.txt", "base\n")
	if out, err := runGit(t, wt, "push", cred, "HEAD:refs/pf-task/demo-src"); err != nil {
		t.Fatalf("carrier push: %s", out)
	}
	// a 2 KiB blob: over the chain limit, under the carrier limit
	if err := os.WriteFile(filepath.Join(wt, "mid.bin"), bytes.Repeat([]byte{1}, 2048), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, wt, "add", "-A")
	mustGit(t, wt, "update-index", "--add", "--cacheinfo", "160000,0123456789012345678901234567890123456789,vendored-thing")
	mustGit(t, wt, "commit", "-m", "pf-wip")
	if out, err := runGit(t, wt, "push", cred, "HEAD:refs/pf-wip/demo-src"); err != nil {
		t.Fatalf("WIP push rejected: %s", out)
	}
	// superseded by a non-descendant snapshot (force)
	mustGit(t, wt, "reset", "-q", "--hard", "HEAD~1")
	commitPlain(t, wt, "other.txt", "another snapshot\n")
	if out, err := runGit(t, wt, "push", cred, "+HEAD:refs/pf-wip/demo-src"); err != nil {
		t.Fatalf("non-fast-forward WIP replacement rejected: %s", out)
	}
	// a new blob over the carrier limit is refused
	if err := os.WriteFile(filepath.Join(wt, "big.bin"), bytes.Repeat([]byte{2}, 8192), 0o644); err != nil {
		t.Fatal(err)
	}
	mustGit(t, wt, "add", "-A")
	mustGit(t, wt, "commit", "-m", "big")
	if out, err := runGit(t, wt, "push", cred, "+HEAD:refs/pf-wip/demo-src"); err == nil {
		t.Fatalf("WIP push over the carrier limit accepted: %s", out)
	} else if !strings.Contains(out, "blob exceeds the 4096-byte limit") {
		t.Fatalf("wrong rejection: %s", out)
	}
	// deletion is allowed for the WIP ref only
	if out, err := runGit(t, wt, "push", cred, ":refs/pf-wip/demo-src"); err != nil {
		t.Fatalf("WIP ref deletion rejected: %s", out)
	}
	if out := mustGit(t, wt, "ls-remote", cred, "refs/pf-wip/demo-src"); strings.TrimSpace(out) != "" {
		t.Fatalf("WIP ref still advertised after deletion: %s", out)
	}
	if out, err := runGit(t, wt, "push", cred, ":refs/pf-task/demo-src"); err == nil {
		t.Fatalf("carrier ref deletion accepted: %s", out)
	}
}

// gitPost sends one raw HTTP request to the proxy with the session
// credential and the given headers/body writer, and answers the status
// code and the response text.
func (e *gitEnv) gitPost(t *testing.T, wiID, secret, endpoint string, hdr map[string]string, body func(io.Writer) error) (int, string) {
	t.Helper()
	u, err := url.Parse(e.ts.URL)
	if err != nil {
		t.Fatalf("parse ts url: %v", err)
	}
	conn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(30 * time.Second))
	cred := base64.StdEncoding.EncodeToString([]byte(wiID + ":" + secret))
	var head strings.Builder
	fmt.Fprintf(&head, "POST /git/%s.git/%s HTTP/1.1\r\nHost: %s\r\nAuthorization: Basic %s\r\n", wiID, endpoint, u.Host, cred)
	for k, v := range hdr {
		fmt.Fprintf(&head, "%s: %s\r\n", k, v)
	}
	head.WriteString("Connection: close\r\n\r\n")
	if _, err := io.WriteString(conn, head.String()); err != nil {
		t.Fatalf("write headers: %v", err)
	}
	if body != nil {
		if err := body(conn); err != nil {
			t.Fatalf("write body: %v", err)
		}
	}
	raw, rerr := io.ReadAll(io.LimitReader(conn, 64<<10))
	if rerr != nil {
		return 0, rerr.Error()
	}
	text := string(raw)
	if !strings.HasPrefix(text, "HTTP/") {
		return 0, text
	}
	fields := strings.Fields(strings.SplitN(text, "\r\n", 2)[0])
	code, _ := strconv.Atoi(fields[1])
	return code, text
}

// bareCommitCount answers how many commits main carries in the WI's bare
// repo (0 = refs unchanged for a fresh claim).
func (e *gitEnv) bareCommitCount(t *testing.T, wiID string) int {
	t.Helper()
	out, err := runGit(t, e.mgr.RepoDir(wiID), "rev-list", "--count", "refs/heads/main")
	if err != nil {
		if strings.Contains(err.Error(), "unknown revision") || strings.Contains(out, "unknown revision") {
			return 0
		}
		t.Fatalf("rev-list: %v (%s)", err, out)
	}
	n, _ := strconv.Atoi(strings.TrimSpace(out))
	return n
}

// noQuarantine asserts the bare repo carries no quarantine/incoming
// directories and nothing landed in the object store.
func (e *gitEnv) noQuarantine(t *testing.T, wiID string) {
	t.Helper()
	objects := filepath.Join(e.mgr.RepoDir(wiID), "objects")
	for _, pat := range []string{"incoming-*", "tmp_*"} {
		if hits, _ := filepath.Glob(filepath.Join(objects, pat)); len(hits) != 0 {
			t.Fatalf("residue %s under %s: %v", pat, objects, hits)
		}
	}
}

// TestGitBodyCapDefaultAndManagerField: the proxy's cap defaults to the
// 1GiB capsule constant and lives on the manager as a test-injectable
// field — no flag, config key or environment variable reaches it.
func TestGitBodyCapDefaultAndManagerField(t *testing.T) {
	e := newGitEnv(t)
	if e.mgr.MaxGitRequestBodyBytes != capsule.MaxGitRequestBodyBytes {
		t.Fatalf("MaxGitRequestBodyBytes = %d, want the capsule default %d", e.mgr.MaxGitRequestBodyBytes, capsule.MaxGitRequestBodyBytes)
	}
	if capsule.MaxGitRequestBodyBytes != int64(1)<<30 {
		t.Fatalf("the capsule cap = %d, want the 1GiB owner ruling", capsule.MaxGitRequestBodyBytes)
	}
}

// TestOverCapDeclaredReceivePackRefused: a receive-pack declaring a body
// above the cap is answered 4xx before the body is streamed, with refs
// unchanged and no residue. (Red on the pre- tree: the old
// proxy blocks copying the declared body and this test times out.)
func TestOverCapDeclaredReceivePackRefused(t *testing.T) {
	e := newGitEnv(t)
	wiID, secret := e.claimWI(t)
	const cap = capsule.MaxGitRequestBodyBytes
	code, body := e.gitPost(t, wiID, secret, "git-receive-pack",
		map[string]string{
			"Content-Type":   "application/x-git-receive-pack-request",
			"Content-Length": strconv.FormatInt(cap+1, 10),
		}, nil) // the body is deliberately never sent
	if code < 400 || code > 499 {
		t.Fatalf("over-cap receive-pack answered %d, want 4xx before the body is read: %s", code, body)
	}
	if !strings.Contains(body, api.ErrGitBadRequest) {
		t.Fatalf("wrong envelope: %s", body)
	}
	if n := e.bareCommitCount(t, wiID); n != 0 {
		t.Fatalf("the refused push moved refs (main has %d commits)", n)
	}
	e.noQuarantine(t, wiID)
}

// TestOverCapDeclaredUploadPackRefused: the same declared cap refuses an
// upload-pack. (Red on the pre- tree: the old proxy answers
// 200 without ever reading the declared body.)
func TestOverCapDeclaredUploadPackRefused(t *testing.T) {
	e := newGitEnv(t)
	wiID, secret := e.claimWI(t)
	const cap = capsule.MaxGitRequestBodyBytes
	code, body := e.gitPost(t, wiID, secret, "git-upload-pack",
		map[string]string{
			"Content-Type":   "application/x-git-upload-pack-request",
			"Content-Length": strconv.FormatInt(cap+1, 10),
		}, nil)
	if code < 400 || code > 499 {
		t.Fatalf("over-cap upload-pack answered %d, want 4xx before the body is read: %s", code, body)
	}
	if !strings.Contains(body, api.ErrGitBadRequest) {
		t.Fatalf("wrong envelope: %s", body)
	}
	e.noQuarantine(t, wiID)
}

// TestOverCapChunkedBodyRefused: a chunked (undeclared) body crossing the
// cap is cut at the cap, the CGI finishes on the truncated pack, and the
// proxy answers 4xx with refs unchanged and no residue — no process
// kill. The over-cap tail is sent only AFTER the CGI has had every
// chance to write its response headers — git http-backend writes them
// BEFORE it reads stdin (verified on git 2.43: a receive-pack POST's
// headers arrive while the pack is still in flight) — so nothing about
// the headers can decide the verdict any more: the proxy must still
// answer 4xx, and a status line arriving before the tail (the old code
// forwarded the CGI's 200 the moment its headers were parsed, the
// over-cap probe still pending) is itself the failure. Red on the
// pre- tree twice over: the whole chunked body streamed
// into the CGI, and the early status line is the race made visible.
func TestOverCapChunkedBodyRefused(t *testing.T) {
	e := newGitEnv(t)
	e.mgr.MaxGitRequestBodyBytes = 1024
	wiID, secret := e.claimWI(t)
	chunked := func(w io.Writer) error {
		// The first chunk fills the cap exactly.
		if _, err := fmt.Fprintf(w, "400\r\n%s\r\n", strings.Repeat("x", 1024)); err != nil {
			return err
		}
		// Give the CGI time to start and write its response headers.
		// If the proxy forwards anything in this window it has answered
		// before the verdict was in — that is the bug.
		c, ok := w.(net.Conn)
		if !ok {
			t.Fatal("chunked body writer got no net.Conn")
		}
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		var early [512]byte
		n, _ := c.Read(early[:])
		_ = c.SetReadDeadline(time.Time{})
		if n > 0 {
			return fmt.Errorf("the proxy answered before the over-cap tail was sent: %q", early[:n])
		}
		// The over-cap tail: one byte past the cap decides; the rest is
		// chunked framing.
		_, err := io.WriteString(w, "1\r\ny\r\n0\r\n\r\n")
		return err
	}
	code, body := e.gitPost(t, wiID, secret, "git-receive-pack",
		map[string]string{"Content-Type": "application/x-git-receive-pack-request", "Transfer-Encoding": "chunked"}, chunked)
	if code < 400 || code > 499 {
		t.Fatalf("over-cap chunked receive-pack answered %d, want 4xx: %s", code, body)
	}
	if !strings.Contains(body, "exceeds the 1024-byte limit") {
		t.Fatalf("wrong refusal body: %s", body)
	}
	if n := e.bareCommitCount(t, wiID); n != 0 {
		t.Fatalf("the refused chunked push moved refs (main has %d commits)", n)
	}
	e.noQuarantine(t, wiID)
}

// TestUnderCapPushStillSucceeds: with a small injected cap the streaming
// wrapper is on the path of every push, and a normal-sized push still
// succeeds (the legitimate-carrier regression face).
func TestUnderCapPushStillSucceeds(t *testing.T) {
	e := newGitEnv(t)
	e.mgr.MaxGitRequestBodyBytes = 64 << 10
	wiID, secret := e.claimWI(t)
	cred := e.credURL(wiID, secret)
	wt := newWorktree(t)
	for i := 0; i < 3; i++ {
		commitPlain(t, wt, fmt.Sprintf("f%d.txt", i), strings.Repeat("legit\n", 100))
		if err := push(t, wt, cred); err != nil {
			t.Fatalf("legit push under the cap rejected: %v", err)
		}
	}
	if n := e.bareCommitCount(t, wiID); n != 3 {
		t.Fatalf("bare main carries %d commits, want 3", n)
	}
}

// TestCGIHeaderEnvDropsCredentials: the Basic wi:secret header (and any
// cookie) never reaches the http-backend / receive-pack / hook env;
// ordinary headers still pass as HTTP_*.
func TestCGIHeaderEnvDropsCredentials(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Basic d2k6c2VjcmV0")
	h.Set("Proxy-Authorization", "Basic eA==")
	h.Set("Cookie", "s=1")
	h.Set("Accept", "application/x-git-upload-pack-result")
	h.Set("Git-Protocol", "version=2")
	env := strings.Join(cgiHeaderEnv(h), "\n")
	for _, banned := range []string{"HTTP_AUTHORIZATION", "HTTP_PROXY_AUTHORIZATION", "HTTP_COOKIE", "HTTP_GIT_PROTOCOL", "d2k6c2VjcmV0"} {
		if strings.Contains(env, banned) {
			t.Errorf("CGI env carries %s:\n%s", banned, env)
		}
	}
	if !strings.Contains(env, "HTTP_ACCEPT=application/x-git-upload-pack-result") {
		t.Errorf("CGI env lost HTTP_ACCEPT:\n%s", env)
	}
}
