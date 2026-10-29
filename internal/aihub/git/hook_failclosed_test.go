package git

// hook_failclosed_test.go: the pre-receive hook is
// fail-closed — a check command that ERRORS (blob pipeline's cat-file,
// its rev-list, the gitlink check's ls-tree) rejects the push instead of
// reading an empty result as "no violation". The failure is injected
// through a PATH git wrapper that only fails inside the hook's
// environment (parent process bash AND the proxy's GIT_PF_MAX_BLOB
// marker), so receive-pack, http-backend and the test's own git stay
// healthy: a red is about the hook's logic, not a broken git. Every case
// here is a push that the pre- hook accepted (the failed
// pipeline's empty badblob / grep read as "clean").

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// newHookFailEnv boots a gitEnv whose hook-context git fails any
// command whose arguments contain failSubstr — unless they also contain
// spareSubstr (non-empty), which keeps one command family healthy so a
// later check is the one that fails (the gitlink check's bare rev-list
// vs the blob check's `rev-list --objects`, which runs first). The injection rides
// GIT_EXEC_PATH, not PATH alone: git http-backend PREPENDS its exec path
// to PATH inside the CGI, so a PATH-only wrapper is never reached from
// the hook — the shim dir mirrors the real exec path (symlinks for every
// helper, a `git` wrapper instead) and GIT_EXEC_PATH points there. The
// wrapper fails only in the hook's context (parent comm bash AND the
// proxy's GIT_PF_MAX_BLOB marker), so receive-pack, http-backend and the
// test's own git stay healthy: a red is about the hook's fail-closed
// logic, not a broken git.
func newHookFailEnv(t *testing.T, failSubstr, spareSubstr string) *gitEnv {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skipf("git binary not found: %v", err)
	}
	out, err := exec.Command(realGit, "--exec-path").Output() // before GIT_EXEC_PATH is set below
	if err != nil {
		t.Skipf("git --exec-path: %v", err)
	}
	realExec := strings.TrimSpace(string(out))
	dir := t.TempDir()
	ents, err := os.ReadDir(realExec)
	if err != nil {
		t.Skipf("read git exec path: %v", err)
	}
	for _, e := range ents {
		if e.Name() == "git" {
			continue
		}
		if err := os.Symlink(filepath.Join(realExec, e.Name()), filepath.Join(dir, e.Name())); err != nil {
			t.Fatal(err)
		}
	}
	spare := ""
	if spareSubstr != "" {
		spare = fmt.Sprintf("*%q*) ;;\n", spareSubstr)
	}
	script := fmt.Sprintf(`#!/bin/sh
parent=
if [ -r /proc/$PPID/comm ]; then parent=$(cat /proc/$PPID/comm); fi
case "$*" in
%s*"%s"*)
  if [ "$parent" = "bash" ] && [ -n "${GIT_PF_MAX_BLOB:-}" ]; then
    echo "w81 injected hook git failure: %s" >&2
    exit 1
  fi ;;
esac
exec %q "$@"
`, spare, failSubstr, failSubstr, realGit)
	wrapper := filepath.Join(dir, "git")
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GIT_EXEC_PATH", dir)
	return newGitEnv(t)
}

// hookFailPush pushes one commit through the shimmed hook and asserts
// the push was REJECTED with refs unchanged, that the wrapper actually
// fired (otherwise the test would be red for the wrong reason), and that
// the rejection names the check under test (wantReason) — so a case can
// never pass on an earlier check's rejection.
func hookFailPush(t *testing.T, e *gitEnv, failSubstr string, wantReason ...string) {
	t.Helper()
	wiID, secret := e.claimWI(t)
	cred := e.credURL(wiID, secret)
	wt := newWorktree(t)
	commitPlain(t, wt, "a.txt", "one\n")
	out, err := runGit(t, wt, "push", cred, "HEAD:refs/heads/main")
	if !strings.Contains(out, "w81 injected hook git failure: "+failSubstr) {
		t.Fatalf("the hook wrapper never failed (the test would be red for the wrong reason); push output:\n%s", out)
	}
	if err == nil {
		t.Fatalf("the push was accepted although %s failed (refs: %d commits):\n%s",
			failSubstr, e.bareCommitCount(t, wiID), out)
	}
	if n := e.bareCommitCount(t, wiID); n != 0 {
		t.Fatalf("the rejected push moved refs (main has %d commits), want them unchanged:\n%s", n, out)
	}
	for _, r := range wantReason {
		if !strings.Contains(out, r) {
			t.Fatalf("the rejection does not name the check under test (want %q); push output:\n%s", r, out)
		}
	}
}

// TestHookBlobPipelineFailureRejects: the blob check's cat-file failing
// rejects the push (the old hook read the empty badblob as "no oversized
// blob" and accepted).
func TestHookBlobPipelineFailureRejects(t *testing.T) {
	e := newHookFailEnv(t, "cat-file --batch-check", "")
	hookFailPush(t, e, "cat-file --batch-check", "blob size check failed for refs/heads/main (rev-list/cat-file pipeline)")
}

// TestHookBlobRevListFailureRejects: the blob check's rev-list failing
// rejects the push the same way.
func TestHookBlobRevListFailureRejects(t *testing.T) {
	e := newHookFailEnv(t, "rev-list --objects", "")
	hookFailPush(t, e, "rev-list --objects", "blob size check failed for refs/heads/main (rev-list/cat-file pipeline)")
}

// TestHookGitlinkLsTreeFailureRejects: the gitlink check's ls-tree
// failing rejects the push (the old hook's failed grep read as "no
// gitlink" and accepted).
func TestHookGitlinkLsTreeFailureRejects(t *testing.T) {
	e := newHookFailEnv(t, "ls-tree -r", "")
	hookFailPush(t, e, "ls-tree -r", "gitlink check failed for commit ", "(git ls-tree)")
}

// TestHookGitlinkRevListFailureRejects: the gitlink check's rev-list
// failing rejects the push (the old hook iterated an empty commit list
// and accepted). The blob check's `rev-list --objects` is spared, so the
// push reaches the gitlink check and the rejection must name it.
func TestHookGitlinkRevListFailureRejects(t *testing.T) {
	e := newHookFailEnv(t, "rev-list", "--objects")
	hookFailPush(t, e, "rev-list", "gitlink check failed for refs/heads/main (git rev-list)")
}

// TestHookHealthyPushWithWrapperStillAccepted: a wrapper matching
// nothing (pure pass-through) leaves a healthy push accepted through
// the PATH-shimmed environment — the fail-closed checks reject only
// genuinely failing commands.
func TestHookHealthyPushWithWrapperStillAccepted(t *testing.T) {
	e := newHookFailEnv(t, "w81-never-matching-command", "")
	wiID, secret := e.claimWI(t)
	cred := e.credURL(wiID, secret)
	wt := newWorktree(t)
	commitPlain(t, wt, "a.txt", "one\n")
	if err := push(t, wt, cred); err != nil {
		t.Fatalf("first push through the shimmed PATH rejected: %v", err)
	}
	commitPlain(t, wt, "b.txt", "two\n")
	if out, err := runGit(t, wt, "push", cred, "HEAD:refs/heads/main"); err != nil {
		t.Fatalf("healthy push through the inert wrapper rejected: %v\n%s", err, out)
	}
	if n := e.bareCommitCount(t, wiID); n != 2 {
		t.Fatalf("bare main carries %d commits, want 2", n)
	}
}
