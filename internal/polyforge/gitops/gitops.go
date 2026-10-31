// Package gitops is the shared capsule-git operations layer used by Runtime
// (clone at Start, dirty-commit at Recover) and Observe (async commit+push
// on execution.json change). It enforces the v1 invariants that need a
// single choke point:
//
//   - per-WI git mutex: Runtime and Observe never run git against the same
//     worktree concurrently (reference.md §8);
//   - pushes carry the session-scoped credential and go through aihub's
//     git HTTP proxy; the credential travels via a one-shot GIT_ASKPASS
//     helper (never in argv, never in persistent env) and reaches both
//     http:// and https:// remotes;
//   - an index.lock that has outlived the stale bound is removed at
//     acquisition (see cleanStaleLocks for why age, not PID, is the only
//     safe signal while step processes may run); once they are reaped,
//     ClearIndexLocks removes them whatever their age;
//   - NO gitlink ever enters local history: `git add -A` followed by a
//     `git ls-files --stage` scan refuses mode-160000 entries with the
//     typed GIT-412 LOCAL_GITLINK_DETECTED error BEFORE the commit is
//     created (the pilot's #48 poison chain made every later push of the
//     standing HEAD unfetchable; the precheck keeps the poison out of
//     history entirely).
//   - task-repo durability lane (polyforge#108): every publish also
//     mirrors each repos/<name> worktree's HEAD into the WI's own bare
//     repo as refs/pf-task/<name> (pushTaskRefsLocked) — the capsule
//     chain itself cannot carry task-repo state (v1 ruling C1 (archive/v1-architecture-spec.md): /repos/ is
//     gitignored), and without this lane the only copy of a session's
//     code commits is the capsule directory a re-claim renames away.
//     Best-effort-loud: a carrier failure never fails the
//     execution-state publish; the claim-time clone lane reads the
//     carrier back via FetchTaskRef. aihub's pre-receive hook exempts
//     the namespace from the chain-only gitlink invariant.
//
// git-dir is ALWAYS external to the capsule (never inside agent-writable
// space). All git access goes through the real git binary.
package gitops

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// wiLock is one work item's git mutex and the count of its holders and
// waiters; the map entry lives exactly as long as that count is non-zero.
type wiLock struct {
	sync.Mutex
	refs int
}

var (
	mu    sync.Mutex
	locks = map[string]*wiLock{}
)

// Lock acquires the per-WI git mutex and returns the release func. A
// waiter counts itself under mu before it blocks, so the entry is deleted
// only when nobody holds or awaits it — never a second mutex for one WI.
func Lock(workItemID string) func() {
	mu.Lock()
	l, ok := locks[workItemID]
	if !ok {
		l = &wiLock{}
		locks[workItemID] = l
	}
	l.refs++
	mu.Unlock()
	l.Lock()
	return func() {
		l.Unlock()
		mu.Lock()
		if l.refs--; l.refs == 0 {
			delete(locks, workItemID)
		}
		mu.Unlock()
	}
}

// Opts describe one capsule worktree + its external git-dir.
type Opts struct {
	WorkItemID string // mutex key and push identity
	CapsuleDir string // worktree root (agent-writable)
	GitDir     string // external git-dir (e.g. <data>/git/wi_xxx.git)
	RemoteURL  string // aihub git proxy URL, e.g. http://host:8420/git/wi_xxx.git
	Username   string // push credential username (= work item id)
	Secret     string // push credential secret (v1.5: the session id; askpass only — never argv, never env)
	// EventSink receives the durable error events the gitops lanes raise
	// (e.g. EventTaskRefDiverged), already bound to this work item by the
	// runtime. nil drops them.
	EventSink func(kind string, body map[string]any)
	// CarrierAlerts is the job's once-per-repo state for
	// carrier_push_failed (owned by the runtime job, so it ends with the
	// job and starts fresh on a re-claim). nil raises every failure.
	CarrierAlerts *CarrierAlerts
	// Ctx, when set, bounds every carrier-lane git call (the task carrier
	// and the WIP lane): each call's timeout is cut to its deadline, and
	// when it ends (deadline or cancellation) the call's git process group
	// is killed and the lane skips what is left — so a cancelled step-end
	// or shutdown WIP push releases the per-WI lock at once. nil = the
	// lane's own timeouts only.
	Ctx context.Context
}

// expired reports whether o.Ctx is set and has ended.
func (o Opts) expired() bool {
	return o.Ctx != nil && o.Ctx.Err() != nil
}

// laneCancelled reports a cancelled carrier lane (a pause, recovery or
// shutdown), which is not a failure.
func (o Opts) laneCancelled() bool {
	return o.Ctx != nil && errors.Is(o.Ctx.Err(), context.Canceled)
}

// ended names why an expired o.Ctx ended, for the lane's failure details.
func (o Opts) ended() string {
	if errors.Is(o.Ctx.Err(), context.Canceled) {
		return "cancelled"
	}
	return "deadline exceeded"
}

// bound cuts a carrier-lane timeout to the time left before o.Ctx's
// deadline (never below 1ms: a zero timeout would mean unbounded).
func (o Opts) bound(d time.Duration) time.Duration {
	if o.Ctx == nil {
		return d
	}
	dl, ok := o.Ctx.Deadline()
	if !ok {
		return d
	}
	left := time.Until(dl)
	if left < time.Millisecond {
		left = time.Millisecond
	}
	if left < d {
		return left
	}
	return d
}

// Env returns the environment every raw git invocation against this
// capsule must carry (inherited environ + the external git-dir binding).
func (o Opts) Env() []string {
	return append(os.Environ(),
		"GIT_DIR="+o.GitDir,
		"GIT_WORK_TREE="+o.CapsuleDir,
	)
}

// hubEnv is the environment of every chain-lane git call that talks to
// the hub with the session credential (the chain push, the capsule
// clone, the carrier ls-remote/fetch): the daemon's environment minus
// every inherited GIT_* variable and with no system or global config —
// the carrier lane's rule (carrierEnv). An operator's ~/.gitconfig must
// not reroute the credentialed push (url.*.insteadOf) nor hand the
// session secret to a credential helper that stores it. bindRepo
// adds the capsule's external git-dir binding; additions (the askpass
// pair) come last.
func hubEnv(o Opts, bindRepo bool, additions []string) []string {
	env := make([]string, 0, len(os.Environ())+4+len(additions))
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	env = append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null",
		// a stalled HTTP transfer (below 1 KB/s for 60 s) aborts long
		// before chainRemoteTimeout — the task-repo lane's values
		"GIT_HTTP_LOW_SPEED_LIMIT=1000", "GIT_HTTP_LOW_SPEED_TIME=60")
	if bindRepo {
		env = append(env, "GIT_DIR="+o.GitDir, "GIT_WORK_TREE="+o.CapsuleDir)
	}
	return append(env, additions...)
}

// CEnv returns env (nil = inherited environ) pinned to the C locale:
// LC_ALL=C and no LANGUAGE, so git's messages are never translated and
// the output this package pattern-matches stays stable.
func CEnv(env []string) []string {
	if env == nil {
		env = os.Environ()
	}
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if strings.HasPrefix(kv, "LC_ALL=") || strings.HasPrefix(kv, "LANGUAGE=") {
			continue
		}
		out = append(out, kv)
	}
	return append(out, "LC_ALL=C")
}

// exitCoder is satisfied by *exec.ExitError.
type exitCoder interface{ ExitCode() int }

// gitError is the failure of one git invocation: the formatted message
// plus the underlying exec error (for exit-code checks).
type gitError struct {
	msg string
	err error
}

func (e *gitError) Error() string { return e.msg }
func (e *gitError) Unwrap() error { return e.err }

// exitCode answers err's git exit code (-1 when it is not an exit error).
func exitCode(err error) int {
	var ec exitCoder
	if errors.As(err, &ec) {
		return ec.ExitCode()
	}
	return -1
}

// git runs git once with the given environment (nil = inherit) and dir.
// It is a var so the tests can probe the argv the package builds.
var git = func(env []string, dir string, args ...string) (string, error) {
	return runGit(0, env, dir, args...)
}

// gitTimedCtx is git bounded by timeout (0 = none) and ctx: when either
// ends the whole git process group is killed (git spawns remote helpers
// and pack-objects), so a hung remote cannot hold the per-WI lock
// forever. A var for the same test seam as git.
var gitTimedCtx = func(ctx context.Context, timeout time.Duration, env []string, dir string, args ...string) (string, error) {
	return runGitCtx(ctx, timeout, env, dir, args...)
}

// gitT runs one carrier-lane git call: its timeout cut by o.bound, and
// killed with its process group when o.Ctx ends.
func (o Opts) gitT(timeout time.Duration, env []string, dir string, args ...string) (string, error) {
	ctx := o.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return gitTimedCtx(ctx, o.bound(timeout), env, dir, args...)
}

// runGit runs git once; timeout 0 = unbounded.
func runGit(timeout time.Duration, env []string, dir string, args ...string) (string, error) {
	return runGitCtx(context.Background(), timeout, env, dir, args...)
}

// runGitCtx is runGit under a caller context as well: cancelling ctx
// kills the git process group like the timeout does (the claim lane's
// capsule clone, which a stop or a shutdown must be able to abort).
func runGitCtx(ctx context.Context, timeout time.Duration, env []string, dir string, args ...string) (string, error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Env = CEnv(env)
	cmd.Dir = dir
	if timeout > 0 || ctx.Done() != nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = 5 * time.Second
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil && ctx.Err() != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			err = fmt.Errorf("timed out after %s: %w", timeout, err)
		} else {
			err = fmt.Errorf("canceled: %w", err)
		}
	}
	if err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return stdout.String(), &gitError{msg: fmt.Sprintf("git %s: %s", strings.Join(args, " "), msg), err: err}
	}
	return stdout.String(), nil
}

// ErrEmptyHistory is the typed error for the (practically unreachable)
// clean-tree-with-no-standing-HEAD shape: the porcelain is empty but
// rev-parse HEAD fails, so there is nothing to answer and nothing to
// push. Materialization always leaves untracked files, so reaching it
// means the capsule was emptied out from under the daemon. It is NOT
// retry-worthy: a retry cannot conjure a history.
var ErrEmptyHistory = errors.New("gitops: empty capsule history (clean tree with no standing HEAD)")

// localGitlinkPrefix marks the LOCAL precheck's typed refusal (the
// sentinel observe classifies push_poisoned_local; a server-side
// pre-receive gitlink rejection never carries it). The refusal is
// returned as an api.Error carrying code GIT-412 with this sentinel at
// the head of its Detail; see checkLocalGitlink for the full shape.
//
// Why the precheck exists: the pilot's poison-chain incident (#48) was
// this shape discovered too late — the gitlink had already been
// committed, and every push of the standing HEAD was then rejected by
// aihub's pre-receive hook (185 consecutive push_failed events, ~1h
// storm). The precheck moves the refusal BEFORE the commit: the poison
// never enters local history, so no retry loop can ever re-offer it to
// the server. NOT retry-worthy: the identical error would answer every
// retry until the tree is cleaned.
const localGitlinkPrefix = "LOCAL_GITLINK_DETECTED"

// IsLocalGitlink reports whether err is the local precheck's typed
// gitlink refusal (as opposed to a server-side hook rejection of an
// already-committed gitlink, which stays an ordinary push error).
func IsLocalGitlink(err error) bool {
	var ae *api.Error
	return errors.As(err, &ae) &&
		ae.Code == api.ErrGitGitlink &&
		strings.HasPrefix(ae.Detail, localGitlinkPrefix)
}

// checkLocalGitlink scans the staged index for gitlink (mode 160000)
// entries and refuses them (refuseGitlink). Repos under the capsule's
// ignored trees (repos/, artifacts/release-*/) never reach the index,
// so ANY staged gitlink is poison by definition.
func checkLocalGitlink(env []string, dir string) error {
	out, err := git(env, dir, "ls-files", "--stage")
	if err != nil {
		return err
	}
	var paths []string
	for _, line := range strings.Split(out, "\n") {
		meta, path, ok := strings.Cut(line, "\t")
		if f := strings.Fields(meta); ok && len(f) > 0 && f[0] == "160000" {
			paths = append(paths, path)
		}
	}
	if len(paths) == 0 {
		return nil
	}
	return refuseGitlink(env, dir, "staged gitlink (mode 160000) at "+strings.Join(paths, ", "), paths[0])
}

// refuseGitlink drops the staged index (best-effort `git reset -q`: the
// worktree is untouched and the next healthy CommitPush re-stages it, so
// the poison does not linger staged) and answers the typed GIT-412
// LOCAL_GITLINK_DETECTED refusal (an *api.Error, so IsLocalGitlink sees
// the code + sentinel pair) naming what was found and the cleanup recipe
// for path. A path under an ignored capsule tree (a task repo under
// repos/) only reaches the index when the capsule .gitignore lost its
// line: the fix is restoring it — deleting that repository's .git would
// destroy the task repo's history. Anywhere else the embedded repository
// is agent residue.
func refuseGitlink(env []string, dir, found, path string) error {
	_, _ = git(env, dir, "reset", "-q")
	return gitlinkRefusal(found, "No commit was created. ", path)
}

// gitlinkRefusal is the typed GIT-412 refusal: what was found, what the
// daemon did (done) and the cleanup recipe for path.
func gitlinkRefusal(found, done, path string) error {
	remedy := "Clean it: move the embedded repository out of the capsule (or add " + path + " to the capsule .gitignore); " +
		"if, and only if, its history is disposable, delete its .git directory (" + filepath.Join(path, ".git") + ") instead. " +
		"Then `git rm --cached " + path + "` if it is still staged, and retry; " +
		"verify with `git ls-files --stage | grep ^160000` (must be empty)."
	if strings.HasPrefix(path, "repos/") || strings.HasPrefix(path, "artifacts/release-") {
		remedy = "The capsule .gitignore no longer ignores " + path + ": restore it (it must contain " +
			strings.ReplaceAll(strings.TrimSpace(capsule.GitignoreContent), "\n", " and ") +
			") and retry. Do NOT delete " + filepath.Join(path, ".git") + " — that destroys the task repo's history."
	}
	return &api.Error{Code: api.ErrGitGitlink, Detail: localGitlinkPrefix + ": " + found +
		" — an embedded git repository would poison the capsule chain (aihub's pre-receive rejects mode 160000 on every push, forever). " +
		done + remedy}
}

// pushedRef records the chain head the hub last accepted from this
// capsule: the clone's tracking ref, moved after every chain push that
// landed.
const pushedRef = "refs/remotes/origin/main"

// checkUnpushedGitlinks refuses, before the chain push, any commit about
// to be pushed (pushedRef..HEAD; all of HEAD's history when the ref is
// absent) that records a gitlink — a step's own `git commit` in the
// capsule root included, which the commit-time precheck never sees. The
// hook would refuse that push forever; the typed GIT-412 names the
// commit, and nothing is pushed or rewritten.
func checkUnpushedGitlinks(o Opts) error {
	env := o.Env()
	rng := "HEAD"
	if _, err := git(env, o.CapsuleDir, "rev-parse", "--verify", "-q", pushedRef); err == nil {
		rng = pushedRef + "..HEAD"
	}
	out, err := git(env, o.CapsuleDir, "log", "--no-renames", "--raw", "--format=commit %H", rng)
	if err != nil {
		return nil // unborn HEAD: the push answers for itself
	}
	commit := ""
	for _, line := range strings.Split(out, "\n") {
		if c, ok := strings.CutPrefix(line, "commit "); ok {
			commit = c
			continue
		}
		meta, path, ok := strings.Cut(line, "\t")
		f := strings.Fields(meta) // :oldmode newmode oldsha newsha status
		if ok && len(f) == 5 && f[1] == "160000" && f[4] != "D" {
			return gitlinkRefusal("unpushed commit "+commit+" records a gitlink (mode 160000) at "+path,
				"Nothing was pushed. Undo the unpushed commits first: `git reset --soft "+pushedRef+"` in the capsule (the work stays staged), then: ", path)
		}
	}
	return nil
}

// uncheckedOutPath answers the path git's "error: '<path>/' does not
// have a commit checked out" names ("" when it names none).
func uncheckedOutPath(msg string) string {
	before, _, ok := strings.Cut(msg, "' does not have a commit checked out")
	if !ok {
		return ""
	}
	return strings.TrimSuffix(before[strings.LastIndex(before, "'")+1:], "/")
}

// PushTimings carries the wall-clock durations of the underlying git
// invocations a publish attempt is made of (integer milliseconds, as
// seen by this process around the raw git calls): CommitMS spans the
// git commit call, PushMS spans the git push call. Zero is the honest
// reading for a call that did not happen on this path (a clean-tree
// publish performs no commit). Observed-only bookkeeping (M8 latency
// samples in the published event bodies); no judgment consumes it here.
type PushTimings struct {
	CommitMS int64 `json:"commit_ms"`
	PushMS   int64 `json:"push_ms"`
}

// CommitPush stages everything, commits and pushes to the aihub bare
// repo. It takes the per-WI git lock itself; callers must not hold it. A clean
// tree (nothing staged after the porcelain check) PUSHES the standing
// HEAD — an idempotent no-op ("Everything up-to-date") when the server
// already holds it — so the pushed flag is honest on every return shape,
// including the retry-after-failed-push one a wrap/seal re-drive needs:
// a commit that landed locally on a failed push finally reaches the
// server HERE, and a still-dead server answers with its error, not a
// fake success. ctx is checked once the per-WI lock is held (already
// ended: nothing is committed) and bounds the chain push itself: when it
// ends the push's git process group is killed. The local status, add and
// commit are not cut short (a killed one would leave index.lock behind).
// The task-repo lane runs only when ctx has not ended by the time the
// chain push returns, and then on its own per-call timeouts (o.Ctx when
// set), not ctx.
// Returns the local commit SHA, whether the push reached the server, and
// the commit/push wall-clock timings.
func CommitPush(ctx context.Context, o Opts, message string) (string, bool, PushTimings, error) {
	release := Lock(o.WorkItemID)
	defer release()

	var timings PushTimings
	if err := ctx.Err(); err != nil {
		return "", false, timings, err // ended while waiting for the lock: commit nothing that would not be pushed
	}

	if err := cleanStaleLocks(o.GitDir); err != nil {
		return "", false, timings, err
	}
	if err := excludeTempFiles(o); err != nil {
		return "", false, timings, err
	}
	env := o.Env()
	// Clean-tree check BEFORE committing: git reports "nothing to commit"
	// on stdout, so the failure cannot be told apart from a real commit
	// error by the error string. The porcelain listing is the honest
	// signal: empty output means the standing HEAD is already the answer.
	status, err := git(env, o.CapsuleDir, "status", "--porcelain")
	if err != nil {
		return "", false, timings, err
	}
	if strings.TrimSpace(status) != "" {
		if _, err := git(env, o.CapsuleDir, "add", "-A"); err != nil {
			// An embedded repository with no commit checked out fails the
			// add itself — the same poison shape one step earlier.
			if strings.Contains(err.Error(), "does not have a commit checked out") {
				return "", false, timings, refuseGitlink(env, o.CapsuleDir, "embedded git repository without a commit checked out at "+uncheckedOutPath(err.Error()), uncheckedOutPath(err.Error()))
			}
			return "", false, timings, err
		}
		// LOCAL gitlink precheck (BEFORE the commit is created): after
		// `add -A`, so it catches both a standing poisoned index and the
		// fresh nested .git the add just staged.
		if err := checkLocalGitlink(env, o.CapsuleDir); err != nil {
			return "", false, timings, err
		}
		commitStart := time.Now()
		_, cerr := git(env, o.CapsuleDir, "commit", "-m", message)
		timings.CommitMS = time.Since(commitStart).Milliseconds()
		if cerr != nil {
			return "", false, timings, cerr
		}
	}
	// A clean tree's standing HEAD is the answer too — and it is PUSHED,
	// not presumed: skipping the push would let a clean-tree retry after
	// a failed push fake success and silence §九 backpressure.
	sha, serr := git(env, o.CapsuleDir, "rev-parse", "HEAD")
	if serr != nil {
		return "", false, timings, fmt.Errorf("%w: %v", ErrEmptyHistory, serr)
	}
	pushed, pushMS, perr := pushLocked(ctx, o)
	timings.PushMS = pushMS
	return strings.TrimSpace(sha), pushed, timings, perr
}

// Head answers the capsule's current HEAD commit SHA ("" with an error
// for an unborn branch / unreadable repo). Read-only.
func Head(o Opts) (string, error) {
	release := Lock(o.WorkItemID)
	defer release()
	out, err := git(o.Env(), o.CapsuleDir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// Push sends the capsule's standing HEAD to the aihub bare repo's main
// branch (the retry path for a commit that landed locally but never
// reached the server). Takes the per-WI lock. The push timing is
// measured but not answered: this entry point serves the runtime seal
// retry, which emits no published event and has no consumer for it.
func Push(o Opts) (bool, error) {
	release := Lock(o.WorkItemID)
	defer release()
	pushed, _, err := pushLocked(context.Background(), o)
	return pushed, err
}

// pushLocked is push under an already-held per-WI lock: the chain push
// (HEAD:refs/heads/main, bounded by ctx and chainRemoteTimeout), then
// the task-repo durability lane (pushTaskRefsLocked) — best-effort,
// after the chain push either way, never blocking it, and skipped once
// ctx has ended. The returned pushMS is the wall-clock duration of the
// git push invocation, measured on failure too (the observed-only
// latency sample).
func pushLocked(ctx context.Context, o Opts) (bool, int64, error) {
	if o.RemoteURL == "" {
		return false, 0, &api.Error{Code: api.ErrGitUnauthorized, Detail: "no push remote configured"}
	}
	if err := checkUnpushedGitlinks(o); err != nil {
		return false, 0, err
	}
	args := []string{"push", o.RemoteURL, "HEAD:refs/heads/main"}
	additions, cleanup, err := credentialEnv(o)
	if err != nil {
		return false, 0, err
	}
	defer cleanup()
	pushStart := time.Now()
	out, perr := gitTimedCtx(ctx, chainRemoteTimeout, hubEnv(o, true, additions), o.CapsuleDir, args...)
	pushMS := time.Since(pushStart).Milliseconds()
	if perr != nil {
		perr = &api.Error{Code: api.ErrGitInternal, Detail: strings.TrimSpace(out) + ": " + perr.Error()}
	} else {
		// the hub now holds HEAD: the next gitlink scan starts above it
		_, _ = git(o.Env(), o.CapsuleDir, "update-ref", pushedRef, "HEAD")
	}
	if ctx.Err() != nil {
		fmt.Fprintf(os.Stderr, "gitops: %s: task-ref push skipped: the publish ended (%v)\n", o.WorkItemID, ctx.Err())
		return perr == nil, pushMS, perr
	}
	// Task-repo durability lane: its own invocations, its own failures —
	// never the chain's result. A carrier failure is raised as a human
	// decision only when the chain push itself landed: a hub outage or a
	// fenced session fails both, and that is not a "cannot be saved"
	// decision (a still-failing carrier is reported on the next
	// successful publish).
	_, _, _ = pushTaskRefsLocked(o, perr == nil)
	return perr == nil, pushMS, perr
}

// TaskCarrierNamespace is the ref namespace, inside the work item's own
// bare repo, that carries each task repo's local head across session
// boundaries (polyforge#108): pushTaskRefsLocked mirrors every
// repos/<name> HEAD to refs/pf-task/<name> on each publish, and the
// claim-time clone lane (runtime.cloneTaskRepos, via FetchTaskRef)
// restores it when a bounded-fail re-claim must rebuild repos/<name>
// from its bare origin. The capsule chain cannot carry this state
// (v1 ruling C1 (archive/v1-architecture-spec.md): /repos/ is gitignored), so without the lane the only copy
// of a session's code commits is the capsule directory — exactly what a
// re-claim renames away. The namespace lives OUTSIDE refs/heads/* so a
// plain clone of the bare repo never materializes task branches, and
// aihub's pre-receive hook exempts it from the chain-only gitlink
// invariant while keeping deletion, fast-forward and a blob limit (the
// carrier's own, capsule.MaxTaskBlobBytes).
const TaskCarrierNamespace = "refs/pf-task/"

// TaskRefName answers the carrier ref for one task repo.
func TaskRefName(repo string) string { return TaskCarrierNamespace + repo }

// pushTaskRefsLocked mirrors every task-repo worktree's HEAD under the
// capsule into the WI's bare repo (HEAD:refs/pf-task/<name>). Runs under
// the per-WI lock (called from pushLocked), AFTER the chain push, and is
// BEST-EFFORT-LOUD: each push is its own git invocation — a rejected,
// refused or failed carrier never fails the execution-state publish this
// lane rides; it is logged loudly and, when the chain push landed
// (chainOK), raised as a carrier_push_failed event (a human decision,
// once per repo per job until the repo's carrier push succeeds again) so
// the human sees that a re-claim may lose that repo's state. An unborn
// task HEAD is nothing to carry (honest skip, not a failure).
//
// The task repo is agent-controlled (agents are untrusted) and the push
// carries the session credential, so the credentialed push NEVER runs
// inside it (git has no switch to ignore a repo's own config, and an
// agent can rewrite that config at any time): carrierSource validates
// the repo's layout and reads its HEAD SHA — a repo that only borrows
// objects from another local store (objects/info/alternates) is made
// self-contained there first, a write that copies its objects/pack and
// then drops the alternates file (carrierSelfContainBorrowedStore, no
// credential) — and pushCarrier pushes
// that SHA from a daemon-owned temporary bare repo that borrows the task
// repo's object store through objects/info/alternates (data only — git
// reads objects from an alternate, it runs nothing from it). It answers
// the symlink-resolved repos dir and the repos that passed carrierSource
// with a born HEAD (the WIP lane, PushTaskWIP, works on exactly those),
// and the repos whose carrier was not written (skipped, refused or a
// failed push).
func pushTaskRefsLocked(o Opts, chainOK bool) (string, []carrierSrc, []string) {
	reposDir := filepath.Join(o.CapsuleDir, capsule.DirRepos)
	ents, err := os.ReadDir(reposDir)
	if err != nil {
		return "", nil, nil // no repos/ tree: no inventory, nothing to carry
	}
	// ONE symlink-resolved repos dir for every check and push (the
	// GIT_CEILING_DIRECTORIES value and the object paths agree).
	if reposDir, err = filepath.EvalSymlinks(reposDir); err != nil {
		return "", nil, []string{"repos/ (" + err.Error() + ")"}
	}
	var srcs []carrierSrc
	var failed []string
	for _, e := range ents {
		if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
			continue
		}
		if o.expired() {
			fmt.Fprintf(os.Stderr, "gitops: %s: task-ref push for repo %s skipped: %s\n", o.WorkItemID, e.Name(), o.ended())
			o.carrierFailed(e.Name(), chainOK, o.ended())
			failed = append(failed, e.Name())
			continue
		}
		objects, sha, reason := carrierSourceT(o, reposDir, e.Name())
		if reason != "" && o.expired() {
			reason = o.ended() + " (" + reason + ")"
		}
		if reason != "" {
			fmt.Fprintf(os.Stderr, "gitops: %s: task-ref push for repo %s refused (task-repo state may not survive a re-claim): %s\n", o.WorkItemID, e.Name(), reason)
			o.carrierFailed(e.Name(), chainOK, "carrier push refused: "+reason)
			failed = append(failed, e.Name())
			continue
		}
		if sha == "" {
			continue // unborn HEAD: nothing to carry yet
		}
		srcs = append(srcs, carrierSrc{e.Name(), objects, sha})
	}
	if len(srcs) == 0 {
		return reposDir, nil, failed
	}
	additions, cleanup, cerr := credentialEnv(o)
	if cerr != nil {
		fmt.Fprintf(os.Stderr, "gitops: %s: task-ref push skipped (credential prep failed): %v\n", o.WorkItemID, cerr)
		for _, s := range srcs {
			failed = append(failed, s.name)
		}
		return reposDir, srcs, failed
	}
	defer cleanup()
	for _, s := range srcs {
		out, behind, err := pushCarrier(o, reposDir, s.objects, s.sha, s.name, additions)
		if behind {
			// The worktree sits on an ancestor of the carried head (an
			// agent's detached checkout of an older commit,):
			// the carrier already holds a superset, so there is nothing to
			// carry — not a divergence, and never a backwards push.
			fmt.Fprintf(os.Stderr, "gitops: %s: task-ref push for repo %s: HEAD %s is behind the carried head — nothing to carry, carrier kept\n", o.WorkItemID, s.name, s.sha)
		}
		if err == nil {
			o.CarrierAlerts.clear(TaskRefName(s.name))
			continue
		}
		detail := err.Error()
		if o.expired() {
			detail = o.ended() + ": " + detail
		}
		fmt.Fprintf(os.Stderr, "gitops: %s: task-ref push for repo %s failed (task-repo state may not survive a re-claim): %s: %s\n", o.WorkItemID, s.name, detail, strings.TrimSpace(out))
		o.carrierFailed(s.name, chainOK, detail)
		failed = append(failed, s.name)
		// A non-fast-forward rejection that is not "behind" and whose
		// stale tip is not contained in this head (pushCarrier answered
		// both cases above) means the carrier and this worktree DIVERGED
		// (e.g. a re-claim that started from a clone lacking the carried
		// commits, or foreign content). The carrier is left untouched and
		// the divergence is raised as an error event, not left to stderr —
		// once per repo per job, like carrier_push_failed.
		if IsNonFastForward(err, out) && chainOK && !o.laneCancelled() && o.EventSink != nil && o.CarrierAlerts.firstDiverged(TaskRefName(s.name)) {
			o.EventSink(EventTaskRefDiverged, map[string]any{
				"repo":  s.name,
				"ref":   TaskRefName(s.name),
				"error": EventDetail(err.Error()),
			})
		}
	}
	return reposDir, srcs, failed
}

// carrierSrc is one task repo that passed carrierSource with a born HEAD:
// its name, its (validated) object directory and its HEAD commit.
type carrierSrc struct{ name, objects, sha string }

// carrierEnv is the ONE environment of every git call on the carrier
// lane (the task-repo reads and the push alike): the daemon's environment
// minus every GIT_* variable, no system or global config (nothing an
// operator's ~/.gitconfig says can reroute a credentialed push), and
// repository discovery fenced at reposDir so a task repo without its own
// .git can never resolve to the capsule or anything above it.
func carrierEnv(reposDir string) []string {
	env := make([]string, 0, len(os.Environ())+3)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "GIT_") {
			env = append(env, kv)
		}
	}
	return append(env,
		"GIT_CONFIG_NOSYSTEM=1",
		"GIT_CONFIG_GLOBAL=/dev/null",
		"GIT_CEILING_DIRECTORIES="+reposDir,
	)
}

// Carrier-lane git timeouts: the read-only checks and the temp-repo
// set-up are local and short; the push talks to the hub and the
// borrow-healing repack copies a whole store, so both get the long
// budget.
var (
	carrierCheckTimeout = time.Minute
	carrierPushTimeout  = 10 * time.Minute
)

// chainRemoteTimeout bounds every git call of the chain lane that talks
// to the hub — the chain push, the capsule clone, the carrier ls-remote
// and fetch. Each runs under the per-WI lock; unbounded, a hung
// remote would hold the lock forever. On expiry the git process group is
// killed, like the carrier lane's calls.
var chainRemoteTimeout = 10 * time.Minute

// carrierFlags lead every git call on the carrier lane: no hooks, no
// fsmonitor (both would run programs), no credential helper (an empty
// value resets the helper list), no http proxy.
var carrierFlags = []string{
	"-c", "core.hooksPath=/dev/null",
	"-c", "core.fsmonitor=false",
	"-c", "credential.helper=",
	"-c", "http.proxy=",
}

// carrierSourceT validates the task repo repos/<name> and reads its HEAD
// (carrier-lane calls, o.gitT).
// reposDir is already symlink-resolved. It answers the repo's object
// directory and the HEAD commit SHA ("" = unborn: nothing to carry), or a
// refusal reason. A repo that borrows objects from another local store
// (objects/info/alternates, e.g. a `git clone --shared`, polyforge#264) is
// made self-contained first (carrierSelfContainBorrowedStore); only the
// repo that cannot be healed must own its storage outright
// (carrierLayoutRefusal), and git must resolve the repo to exactly its
// own .git. Apart from the heal (repack, connectivity check, alternates
// removal) the calls only READ (rev-parse); none of them holds the
// credential.
func carrierSourceT(o Opts, reposDir, name string) (objects, sha, reason string) {
	dir := filepath.Join(reposDir, name)
	gitDir := filepath.Join(dir, ".git")
	env := carrierEnv(reposDir)
	// safe.directory: the repo may be owned by another uid (#135).
	flags := append(append([]string{}, carrierFlags...), "-c", "safe.directory="+dir)
	reason = carrierLayoutRefusal(gitDir)
	if reason == carrierAlternatesRefusal {
		// A local borrowed store is recoverable: copy the borrowed objects
		// into the repo, drop the alternates reference, then check the
		// layout again (the healed repo must pass the strict check). A
		// failed heal keeps the original refusal reason.
		reason = carrierSelfContainBorrowedStore(o, dir, gitDir, env, flags)
		if reason == "" {
			reason = carrierLayoutRefusal(gitDir)
		}
	}
	if reason != "" {
		return "", "", "repos/" + name + ": " + reason
	}
	objects = filepath.Join(gitDir, "objects")
	out, err := o.gitT(carrierCheckTimeout, env, dir, append(flags, "rev-parse", "--absolute-git-dir")...)
	if err != nil || strings.TrimSpace(out) != gitDir {
		return "", "", "git does not resolve repos/" + name + " to its own .git directory"
	}
	head, err := o.gitT(carrierCheckTimeout, env, dir, append(flags, "rev-parse", "--verify", "-q", "HEAD^{commit}")...)
	if err != nil {
		return objects, "", "" // unborn HEAD
	}
	return objects, strings.TrimSpace(head), ""
}

// carrierAlternatesRefusal is the carrierLayoutRefusal reason for a task
// repo whose object store is borrowed through objects/info/alternates. It
// is also the reason kept when the borrowed store cannot be copied in or
// the healed repo fails its connectivity check.
const carrierAlternatesRefusal = "borrows objects from another store (objects/info/alternates)"

// carrierHealAside is the suffix carrierSelfContainBorrowedStore renames the
// alternates file to while it cuts the borrow for the connectivity check. The
// record of where the borrowed store was is kept (restored on failure) and
// carrierLayoutRefusal still answers "borrowed" for it, so a heal interrupted
// after the cut is healed (and checked) again by the next publication.
const carrierHealAside = ".pf-heal"

// carrierHealCut, when set, runs between cutting the borrowed store and the
// connectivity check (test seam: a concurrent agent git writes a ref in
// exactly that window).
var carrierHealCut func(dir string)

// carrierSelfContainBorrowedStore makes a task repo that borrows its object
// store from another local store own those objects, then drops the
// objects/info/alternates reference. A step agent may clone under repos/
// with `git clone --shared` (polyforge#264): that is a RECOVERABLE carrier
// state — every object the carrier needs is still reachable — so the repo
// must not be refused wholesale. It answers "" when the repo is (now)
// self-contained, else the layout refusal to report: the original alternates
// reason when the repack cannot read the borrowed store (the unrecoverable
// case) or the healed repo is not self-contained after all.
//
// The order is repack first, then cut the borrow, then check: the repack
// copies the borrowed objects in while the stored record still works, so a
// crash in between leaves a still-borrowed but already complete repo that
// the next publish heals again. Only the check runs without the borrowed
// store: the alternates file is renamed aside (its content kept), the repo
// must pass `fsck --connectivity-only` on its own, and a failure — a
// concurrent agent git wrote a ref to an object that exists only in the
// borrowed store after the repack read the refs — puts the record back and
// refuses, so nothing is left pointing at objects the repo no longer has and
// the next publish heals again. The repack and the check run in the task repo
// under the carrier env/flags with the push budget (the repack copies the
// whole borrowed store and the check walks it, so the 1-minute check budget
// can be too short); a timeout counts as a failure. http-alternates is never
// healed: git reads it only for dumb HTTP, so no repack can make that store
// local — it stays refused.
func carrierSelfContainBorrowedStore(o Opts, dir, gitDir string, env, flags []string) string {
	altPath := filepath.Join(gitDir, "objects", "info", "alternates")
	aside := altPath + carrierHealAside
	// A crash after the borrow was cut left the record aside and no
	// alternates file: put it back and heal (idempotently) again.
	if _, err := os.Lstat(altPath); os.IsNotExist(err) {
		if _, err := os.Lstat(aside); err != nil {
			return "" // not borrowed after all
		}
		if err := os.Rename(aside, altPath); err != nil {
			return carrierAlternatesRefusal
		}
	}
	if out, err := o.gitT(carrierPushTimeout, env, dir, append(flags, "repack", "-a", "-d")...); err != nil {
		fmt.Fprintf(os.Stderr, "gitops: %s: cannot self-contain %s (repack): %v: %s\n", o.WorkItemID, dir, err, strings.TrimSpace(out))
		return carrierAlternatesRefusal
	}
	if err := os.Rename(altPath, aside); err != nil {
		return carrierAlternatesRefusal
	}
	if carrierHealCut != nil {
		carrierHealCut(dir)
	}
	if out, err := o.gitT(carrierPushTimeout, env, dir, append(flags, "fsck", "--connectivity-only")...); err != nil {
		fmt.Fprintf(os.Stderr, "gitops: %s: cannot self-contain %s (still incomplete without the borrowed store): %v: %s\n", o.WorkItemID, dir, err, strings.TrimSpace(out))
		if rerr := os.Rename(aside, altPath); rerr != nil {
			fmt.Fprintf(os.Stderr, "gitops: %s: cannot restore the borrowed store of %s: %v\n", o.WorkItemID, dir, rerr)
		}
		return carrierAlternatesRefusal
	}
	if err := os.Remove(aside); err != nil {
		fmt.Fprintf(os.Stderr, "gitops: %s: cannot drop the borrowed-store record of %s: %v\n", o.WorkItemID, dir, err)
		return carrierAlternatesRefusal
	}
	return ""
}

// carrierLayoutRefusal answers why the git dir at gitDir does not own its
// storage outright ("" = it does). Every piece git would read refs or
// objects through must be a real file or directory of THIS repo: .git
// itself (not a symlink, not a gitfile), no commondir (a worktree-style
// redirect of refs and objects), no symlink at HEAD, packed-refs,
// anywhere under refs/, at any entry of objects/ (pack, info, the ??
// fan-out dirs) or of objects/pack/, and no objects/info/alternates or
// http-alternates (borrowed object stores). A local alternates file is
// answered with carrierAlternatesRefusal so carrierSourceT can heal it, and
// so is the aside file an interrupted heal leaves behind (carrierHealAside);
// the healed repo is then checked again here.
func carrierLayoutRefusal(gitDir string) string {
	if st, err := os.Lstat(gitDir); err != nil || !st.IsDir() {
		return ".git is not a real directory (missing, a symlink or a gitfile)"
	}
	if _, err := os.Lstat(filepath.Join(gitDir, "commondir")); err == nil {
		return ".git/commondir redirects to another repository"
	}
	for _, rel := range []string{"HEAD", "packed-refs"} {
		if st, err := os.Lstat(filepath.Join(gitDir, rel)); err == nil && st.Mode()&os.ModeSymlink != 0 {
			return ".git/" + rel + " is a symlink"
		}
	}
	refs := filepath.Join(gitDir, "refs")
	if st, err := os.Lstat(refs); err == nil {
		if !st.IsDir() {
			return ".git/refs is not a real directory"
		}
		bad := ""
		_ = filepath.WalkDir(refs, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				bad = "cannot read .git/refs"
				return filepath.SkipAll
			}
			if d.Type()&os.ModeSymlink != 0 {
				rel, _ := filepath.Rel(gitDir, path)
				bad = ".git/" + rel + " is a symlink"
				return filepath.SkipAll
			}
			return nil
		})
		if bad != "" {
			return bad
		}
	}
	objects := filepath.Join(gitDir, "objects")
	if st, err := os.Lstat(objects); err != nil || !st.IsDir() {
		return ".git/objects is not a real directory"
	}
	for _, dir := range []string{objects, filepath.Join(objects, "pack")} {
		ents, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return "cannot read " + dir
		}
		for _, e := range ents {
			if e.Type()&os.ModeSymlink != 0 {
				rel, _ := filepath.Rel(gitDir, filepath.Join(dir, e.Name()))
				return ".git/" + rel + " is a symlink"
			}
		}
	}
	info := filepath.Join(objects, "info")
	if _, err := os.Lstat(filepath.Join(info, "http-alternates")); err == nil {
		return "borrows objects from another store (objects/info/http-alternates)"
	}
	if _, err := os.Lstat(filepath.Join(info, "alternates")); err == nil {
		return carrierAlternatesRefusal
	}
	if _, err := os.Lstat(filepath.Join(info, "alternates"+carrierHealAside)); err == nil {
		return carrierAlternatesRefusal
	}
	return ""
}

// pushCarrier pushes sha to refs/pf-task/<name> on the WI's bare repo
// from a daemon-owned temporary bare repo whose only object source is
// the task repo's object store (objects/info/alternates), with the
// carrier env plus the credential additions, a protocol allow-list of
// the remote's own scheme, and no submodule recursion. The temporary
// repo lives outside the capsule and is removed afterwards (a daemon
// killed mid-push leaves it behind: SweepCarrierTemps). reposDir is the
// symlink-resolved repos dir carrierSource used.
//
// The carrier ref must never carry the task repo's upstream history
// the aihub pre-receive hook walks the whole reachable
// history of a NEW ref, so a single upstream blob above the carrier cap
// would refuse the whole push and lose the WI's commits. Each push
// therefore confines the ref's reachable history to the WI's own line:
// the shallow roots are the WI line's upstream boundary — the boundary
// commits of `rev-list --boundary <head> --not
// --exclude=origin/polyforge/* --remotes=origin`, i.e. the origin
// commits the WI's commits rest on, including any upstream merge
// parent. The WI's own feature branch (origin/polyforge/<name>, which
// deliver must push to HEAD) is NOT upstream and is excluded: otherwise
// a delivered merge of a moved default branch would leave the walk empty
// and cut at the head itself, which the hub refuses as a non-fast-forward
// update (code review). Every WI commit is therefore carried
// and no upstream commit below that boundary (and no upstream blob, of
// any size) is. When the origin refs are unusable, or the head adds
// nothing above them, the cut is the hub's own carrier tip when head
// descends from it (a root the hub already holds keeps the update
// fast-forwardable), then the recorded per-repo baseline (capsule.json
// Repos), then the pushed head itself (the head stays durable even though
// its ancestors are not on the hub). The hook, the caps and the
// claim-time restore are unchanged.
//
// behind reports a non-fast-forward rejection whose pushed sha is an
// ancestor of the hub's carrier tip: the carrier already holds it, so
// the push is answered as done (err nil) — nothing to carry.
//
// A non-fast-forward whose pushed sha is a sibling of the hub's tip is a
// rewind that rewrote a carried commit. When every entry
// of the hub tip's tree (path, mode, type, blob) is present unchanged in
// the new head's tree — the stale tip's content is contained in the new
// head: the same line's work, rewritten — the carrier is replaced with
// this head under a lease on the observed tip (`--force-with-lease`).
// Foreign, unrelated or content-dropping heads keep the old refusal; an
// unreadable or moved tip is never clobbered.
func pushCarrier(o Opts, reposDir, objects, sha, name string, credAdditions []string) (out string, behind bool, err error) {
	env := carrierEnv(reposDir)
	tmp, out, err := carrierTempRepo(o, env, objects)
	if tmp != "" {
		defer os.RemoveAll(tmp)
	}
	if err != nil {
		return out, false, err
	}
	if out, err := o.gitT(carrierCheckTimeout, env, tmp, append(carrierTmpFlags(tmp), "update-ref", "refs/pf-carrier/tmp", sha)...); err != nil {
		return out, false, err
	}
	ref := TaskRefName(name)
	if boundary := carrierBoundary(o, env, tmp, reposDir, name, sha, credAdditions); boundary != "" {
		// The shallow file is git's only switch for a shallow push: it
		// names the commits the send side treats as roots, so the pack
		// and the hub's shallow record stop there. It is one commit per
		// line, so a merged line's several boundary commits all fit.
		if err := os.WriteFile(filepath.Join(tmp, "shallow"), []byte(boundary+"\n"), 0o600); err != nil {
			return "", false, err
		}
	}
	out, err = carrierPush(o, env, tmp, sha+":"+ref, credAdditions)
	if err == nil || !IsNonFastForward(err, out) {
		return out, false, err
	}
	tip := carrierTip(o, env, tmp, ref, credAdditions)
	if tip == "" {
		return out, false, err
	}
	if carrierIsAncestor(o, env, tmp, sha, tip) {
		return out, true, nil
	}
	if !carrierContainsTip(o, env, tmp, tip, sha) {
		return out, false, err
	}
	if rout, rerr := carrierReconcilePush(o, env, tmp, sha, ref, tip, credAdditions); rerr == nil {
		return rout, false, nil
	} else {
		return rout, false, rerr
	}
}

// carrierBoundary answers the shallow boundary of one carrier push, or
// "" to send the full history. The cut is the WI line's upstream
// boundary (see carrierUpstreamBoundary), not the hub's current tip and
// not the WI's first own commit: cutting at the hub tip would still
// upload the full history of an upstream merge parent, and cutting at
// the first own commit would drop the claim-base ancestry the restored
// clone needs.
//
// When the upstream walk answers nothing (the origin refs are unusable,
// or the head adds nothing above them), a root the hub does not hold
// would make the pushed head look parentless there and turn an update
// into a non-fast-forward refusal (code review). The hub's
// own carrier tip is an ancestor of head in that case (it is the carried
// WI line), so it is the cut when it is one. The recorded baseline, then
// the head itself, are the fallbacks (the head keeps the head durable
// but not its intermediate own commits).
func carrierBoundary(o Opts, env []string, tmp, reposDir, name, sha string, credAdditions []string) string {
	if up, usable := carrierUpstreamBoundary(o, reposDir, name, sha); usable && up != "" {
		return up
	}
	if tip := carrierTip(o, env, tmp, TaskRefName(name), credAdditions); tip != "" && tip != sha && carrierIsAncestor(o, env, tmp, tip, sha) {
		return tip
	}
	if base := carrierBaselineBoundary(o, env, tmp, name, sha); base != "" {
		return base
	}
	return sha
}

// carrierUpstreamBoundary answers the commits the WI line rests on: the
// "-" boundary commits of the task repo's `rev-list --boundary <head>
// --not --exclude=origin/polyforge/* --remotes=origin`. Every one of them
// is a shallow root, so the pack carries the WI's own commits (and the
// boundary commits themselves) but no upstream history below them. A
// merged upstream line answers several roots (each merge parent's
// boundary), which the shallow file takes one per line. The WI's own
// feature branch (origin/polyforge/<name>, pushed to origin by deliver)
// is excluded: it is this work item's line, not upstream, and treating it
// as upstream empties the walk after a delivered merge (
// code review). The exclude pattern is relative to `refs/remotes/`, as
// git-rev-list(1) requires for --remotes. usable is false when the origin
// refs are unusable (not a clone with origin-tracking refs, a failed
// walk); usable with an empty answer means head adds nothing above the
// origin refs.
func carrierUpstreamBoundary(o Opts, reposDir, name, head string) (roots string, usable bool) {
	dir := filepath.Join(reposDir, name)
	flags := append(append([]string{}, carrierFlags...), "-c", "safe.directory="+dir)
	env := carrierEnv(reposDir)
	refs, err := o.gitT(carrierCheckTimeout, env, dir, append(flags, "for-each-ref", "--format=%(objectname)", "refs/remotes/origin")...)
	if err != nil || strings.TrimSpace(refs) == "" {
		return "", false
	}
	out, err := o.gitT(carrierCheckTimeout, env, dir, append(flags, "rev-list", "--boundary", head, "--not", "--exclude=origin/polyforge/*", "--remotes=origin")...)
	if err != nil {
		return "", false
	}
	var found []string
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "-") {
			continue
		}
		if s := strings.TrimPrefix(line, "-"); s != "" {
			found = append(found, s)
		}
	}
	return strings.Join(found, "\n"), true
}

// carrierBaselineBoundary answers the recorded per-repo baseline (the
// capsule.json Repos checkout SHA) when it is an ancestor of head, or ""
// when none is recorded or it is not.
func carrierBaselineBoundary(o Opts, env []string, tmp, name, head string) string {
	baseline := capsuleRepoBaseline(o.CapsuleDir, name)
	if baseline == "" || baseline == head || !carrierIsAncestor(o, env, tmp, baseline, head) {
		return ""
	}
	return baseline
}

// carrierIsAncestor reports whether ancestor is reachable from commit in
// the temporary repo's borrowed object store.
func carrierIsAncestor(o Opts, env []string, tmp, ancestor, commit string) bool {
	_, err := o.gitT(carrierCheckTimeout, env, tmp, append(carrierTmpFlags(tmp), "merge-base", "--is-ancestor", ancestor, commit)...)
	return err == nil
}

// capsuleRepoBaseline reads the per-repo checkout SHA the capsule.json
// records ("" when the file or the repo entry is absent).
func capsuleRepoBaseline(capsuleDir, name string) string {
	raw, err := os.ReadFile(filepath.Join(capsuleDir, capsule.CapsuleJSON))
	if err != nil {
		return ""
	}
	var idn capsule.Identity
	if json.Unmarshal(raw, &idn) != nil {
		return ""
	}
	return idn.Repos[name]
}

// carrierTip reads the hub's current tip of ref with one ls-remote from
// the temporary repo tmp ("" when the hub holds no such ref or the read
// fails).
func carrierTip(o Opts, env []string, tmp, ref string, credAdditions []string) string {
	args := append(append(carrierTmpFlags(tmp), carrierProtocolFlags(o)...), "ls-remote", o.RemoteURL, ref)
	out, err := o.gitT(carrierPushTimeout, append(env, credAdditions...), tmp, args...)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Fields(line); len(f) == 2 && f[1] == ref {
			return f[0]
		}
	}
	return ""
}

// carrierContainsTip reports whether every entry of the hub tip's tree is
// present unchanged (same mode, type and object) in head's tree. Dropping
// or rewriting a carried path is not containment: a rewrite that changes
// the work itself keeps the old carrier rather than losing it. The stale
// commit must be readable from tmp's borrowed object store; when it is not
// (a fresh clone lacks the carried commits), containment cannot be proven
// and the carrier is kept.
func carrierContainsTip(o Opts, env []string, tmp, tip, head string) bool {
	tipEntries, ok := carrierTreeEntries(o, env, tmp, tip)
	if !ok {
		return false
	}
	headEntries, ok := carrierTreeEntries(o, env, tmp, head)
	if !ok {
		return false
	}
	for p, entry := range tipEntries {
		if headEntries[p] != entry {
			return false
		}
	}
	return true
}

// carrierTreeEntries answers every non-tree entry of commit's tree as
// path -> "mode type object" (the empty set for an empty tree), or false
// when the commit/tree cannot be read.
func carrierTreeEntries(o Opts, env []string, tmp, commit string) (map[string]string, bool) {
	out, err := o.gitT(carrierCheckTimeout, env, tmp, append(carrierTmpFlags(tmp), "ls-tree", "-r", "-z", commit)...)
	if err != nil {
		return nil, false
	}
	entries := map[string]string{}
	for _, rec := range strings.Split(out, "\x00") {
		meta, p, ok := strings.Cut(rec, "\t")
		if ok && p != "" {
			entries[p] = meta
		}
	}
	return entries, true
}

// carrierTempRepo creates the daemon-owned temporary bare repo of one
// carrier-lane push (next to the external git-dirs, never inside the
// capsule), borrowing objects (the task repo's object store; "" = none)
// through objects/info/alternates. The caller removes tmp (also on
// error, when it is non-empty).
func carrierTempRepo(o Opts, env []string, objects string) (tmp, out string, err error) {
	base := ""
	if o.GitDir != "" {
		base = filepath.Dir(o.GitDir)
	}
	if tmp, err = os.MkdirTemp(base, "pf-carrier-"); err != nil {
		return "", "", err
	}
	if out, err := o.gitT(carrierCheckTimeout, env, tmp, "init", "-q", "--bare", "--template=", tmp); err != nil {
		return tmp, out, err
	}
	if objects == "" {
		return tmp, "", nil
	}
	if err := os.MkdirAll(filepath.Join(tmp, "objects", "info"), 0o700); err != nil {
		return tmp, "", err
	}
	if err := os.WriteFile(filepath.Join(tmp, "objects", "info", "alternates"), []byte(objects+"\n"), 0o600); err != nil {
		return tmp, "", err
	}
	return tmp, "", nil
}

// carrierTmpFlags leads every git call against the temporary repo: the
// explicit --git-dir plus carrierFlags.
func carrierTmpFlags(tmp string) []string {
	return append([]string{"--git-dir=" + tmp}, carrierFlags...)
}

// carrierProtocolFlags allow exactly the remote's own transport scheme.
func carrierProtocolFlags(o Opts) []string {
	scheme := "file"
	if i := strings.Index(o.RemoteURL, "://"); i > 0 {
		scheme = o.RemoteURL[:i]
	}
	return []string{"-c", "protocol.allow=never", "-c", "protocol." + scheme + ".allow=always"}
}

// carrierPush runs the credentialed push of one refspec from the
// temporary repo tmp: the carrier env plus the credential additions, a
// protocol allow-list of the remote's own scheme, no submodule
// recursion, the push timeout. A
// failure is the caller's; the next close or publish pushes again.
func carrierPush(o Opts, env []string, tmp, refspec string, credAdditions []string) (string, error) {
	return carrierPushWithOpts(o, env, tmp, refspec, credAdditions)
}

// carrierReconcilePush replaces ref with the rewritten head sha, guarded
// by a lease on the tip the containment check read. If another writer
// moved the ref in between, the lease refuses the push instead of
// clobbering that update.
func carrierReconcilePush(o Opts, env []string, tmp, sha, ref, tip string, credAdditions []string) (string, error) {
	return carrierPushWithOpts(o, env, tmp, sha+":"+ref, credAdditions, "--force-with-lease="+ref+":"+tip)
}

// carrierPushWithOpts is carrierPush with extra push options before the
// remote and refspec.
func carrierPushWithOpts(o Opts, env []string, tmp, refspec string, credAdditions []string, opts ...string) (string, error) {
	args := append(append(carrierTmpFlags(tmp), carrierProtocolFlags(o)...),
		"push", "--no-recurse-submodules")
	args = append(args, opts...)
	args = append(args, o.RemoteURL, refspec)
	return o.gitT(carrierPushTimeout, append(env, credAdditions...), tmp, args...)
}

// carrierFailed raises carrier_push_failed for repo — only when the
// chain push landed (chainOK), and only the first time since the repo's
// last successful carrier push in this job (CarrierAlerts).
func (o Opts) carrierFailed(repo string, chainOK bool, detail string) {
	o.carrierRefFailed(repo, TaskRefName(repo), chainOK, detail)
}

// carrierRefFailed is carrierFailed for any carrier-lane ref (the task
// carrier or the WIP ref, polyforge#167); the once-per-job alert state is
// kept per ref. A cancelled lane raises nothing.
func (o Opts) carrierRefFailed(repo, ref string, chainOK bool, detail string) {
	if o.laneCancelled() {
		// a cancelled lane (a pause, recovery or shutdown stopped it) is
		// not a failure: the log line says it; the once-per-ref alert stays
		// for a real one
		return
	}
	if !chainOK || o.EventSink == nil || !o.CarrierAlerts.first(ref) {
		return
	}
	o.EventSink(EventCarrierPushFailed, map[string]any{
		"repo":  repo,
		"ref":   ref,
		"error": EventDetail(detail),
	})
}

// eventDetailMax bounds the free-text detail an event body carries.
const eventDetailMax = 2048

// EventDetail truncates an event body's free-text detail (a git error)
// to eventDetailMax bytes, cut on a rune boundary.
func EventDetail(s string) string {
	if len(s) <= eventDetailMax {
		return s
	}
	cut := eventDetailMax
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…(truncated)"
}

// FetchTaskRef reads the carrier lane from the claim side: it fetches
// refs/pf-task/<name> from the WI's bare repo into the task worktree at
// dir and answers the SHA the carrier names. "" with a nil error means
// the carrier holds nothing for this repo yet — the NORMAL first-claim
// shape (no previous session has published a head), answered quietly;
// any other failure comes back loud to the caller. The fetch carries the
// session credential (one-shot askpass) and never touches the capsule
// worktree: dir is the task repo's own clone with its own git-dir. Takes
// the per-WI lock, mirroring Clone — the claim lane is single-threaded
// per WI, but the invariant (one git face per WI at a time) stays
// mechanical, not remembered.
func FetchTaskRef(ctx context.Context, o Opts, dir, name string) (string, error) {
	return fetchCarrierRef(ctx, o, dir, TaskRefName(name))
}

// fetchCarrierRef is FetchTaskRef for any carrier-lane ref (the task
// carrier or the WIP ref): ("", nil) when the hub holds no such ref.
// Cancelling ctx kills the remote calls (the claim lane, B-8).
func fetchCarrierRef(ctx context.Context, o Opts, dir, ref string) (string, error) {
	if o.RemoteURL == "" {
		return "", nil // no capsule remote: nowhere a carrier could exist
	}
	release := Lock(o.WorkItemID)
	defer release()
	additions, cleanup, err := credentialEnv(o)
	if err != nil {
		return "", err
	}
	defer cleanup()
	env := hubEnv(o, false, additions)
	// ls-remote --exit-code answers 2 when no ref matches: the
	// language-independent "no carrier yet" signal (never git's message).
	if _, err := gitTimedCtx(ctx, o.bound(chainRemoteTimeout), env, dir, "ls-remote", "--exit-code", o.RemoteURL, ref); err != nil {
		if exitCode(err) == 2 {
			return "", nil // no carrier recorded yet: first claim, nothing to restore
		}
		return "", err
	}
	// The carrier carries only the WI's own commits and is rooted at a
	// shallow boundary, so the fetch must be allowed to
	// record that boundary: without --update-shallow git refuses to write
	// the new shallow root into a full clone and silently updates no ref
	// ("shallow roots are not allowed to be updated"), which would read as
	// a carrier that cannot be restored. Harmless for a non-shallow ref.
	if _, err := gitTimedCtx(ctx, o.bound(chainRemoteTimeout), env, dir, "fetch", "--quiet", "--no-tags", "--update-shallow", o.RemoteURL, ref); err != nil {
		return "", err
	}
	out, err := git(env, dir, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// refCasRejected reports whether a failed push's combined output is the
// ref-transaction compare-and-swap rejection: git-receive-pack refuses
// the whole push when the ref moved ("cannot lock ref ... is at X but
// expected Y") or came into existence ("reference already exists")
// after the client fixed its expected-old value at advertisement time.
// Those shapes mean an overlapping push of the same ref (one left
// running by an exited daemon) won the race: the next push re-advertises
// and lands or answers up-to-date, so it is never a permanent rejection.
func refCasRejected(out string) bool {
	return strings.Contains(out, "cannot lock ref") && strings.Contains(out, "failed to update ref")
}

// Clone materializes a fresh capsule worktree from the aihub bare repo
// with its git-dir EXTERNAL at o.GitDir (the design record: the agent must never
// reach hooks/config/index): `git clone --separate-git-dir` writes the
// git-dir there directly and leaves the one-line .git pointer file in the
// worktree, so no move — and no cross-device copy — follows. The working
// branch is pinned to main (an empty remote's clone otherwise sits on the
// init default). Takes the per-WI lock. Cancelling ctx kills the clone
// (the claim lane runs it outside the runtime's global lock, and a
// stop or a shutdown during the claim aborts it).
//
// An existing git-dir target is a STALE git-dir (a previous session's
// HEAD/index): silently adopting it (or merging the fresh clone over it)
// leaves a worktree at the remote tip under a diverged history. Clone
// refuses it loudly and touches nothing; the caller quarantines the unit
// before cloning. (git itself refuses an existing target too; the check
// here keeps the refusal explicit and independent of git's wording.)
func Clone(ctx context.Context, o Opts) error {
	release := Lock(o.WorkItemID)
	defer release()
	if _, serr := os.Lstat(o.GitDir); serr == nil {
		return fmt.Errorf("gitops: refusing to clone %s: git-dir target %s already exists (stale git-dir from a previous session; quarantine it first)", o.CapsuleDir, o.GitDir)
	} else if !os.IsNotExist(serr) {
		return serr
	}
	if err := os.MkdirAll(filepath.Dir(o.GitDir), 0o755); err != nil {
		return err
	}
	additions, cleanup, err := credentialEnv(o)
	if err != nil {
		return err
	}
	defer cleanup()
	env := hubEnv(o, false, additions)
	if _, err := runGitCtx(ctx, chainRemoteTimeout, env, "", "clone", "--separate-git-dir="+o.GitDir, o.RemoteURL, o.CapsuleDir); err != nil {
		return err
	}
	if _, err := git(o.Env(), o.CapsuleDir, "symbolic-ref", "HEAD", "refs/heads/main"); err != nil {
		// The real diagnosis lives in the error (stderr); stdout is empty
		// on failure.
		return fmt.Errorf("git symbolic-ref: %s", err.Error())
	}
	return nil
}

// HasCommit reports whether the git-dir gitDir holds sha as a commit
// (read-only).
func HasCommit(o Opts, gitDir, sha string) bool {
	_, err := git(append(hubEnv(o, false, nil), "GIT_DIR="+gitDir), "", "cat-file", "-e", sha+"^{commit}")
	return err == nil
}

// AdoptCommit moves a freshly cloned capsule's chain to sha, taking the
// commit from the local git-dir src (git fetch from a path: nothing
// leaves the machine, src is only read). The worktree is reset to sha, so
// the chain continues from it and the next publish pushes the adopted
// commits. Takes the per-WI lock; ctx bounds the fetch.
func AdoptCommit(ctx context.Context, o Opts, sha, src string) error {
	release := Lock(o.WorkItemID)
	defer release()
	env := hubEnv(o, true, nil)
	if _, err := runGitCtx(ctx, chainRemoteTimeout, env, o.CapsuleDir, "fetch", "--no-tags", "--quiet", src, sha); err != nil {
		return fmt.Errorf("gitops: fetching %s from %s: %w", sha, src, err)
	}
	if _, err := git(env, o.CapsuleDir, "reset", "--quiet", "--hard", sha); err != nil {
		return fmt.Errorf("gitops: moving the chain to %s: %w", sha, err)
	}
	return nil
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// ---------------------------------------------------------------------------
// Credential delivery: one-shot GIT_ASKPASS (never argv, never persistent)

// staleLockAge is how old an index.lock must be before cleanStaleLocks
// removes it.
const staleLockAge = 10 * time.Minute

// credentialEnv prepares the one-shot credential delivery for an http(s)
// remote: a GIT_ASKPASS helper script written OUTSIDE the capsule, in a
// 0700 directory sibling of the external git-dir, containing the
// username/password answers. The returned env additions are meant for ONE
// git invocation; cleanup removes the script. The secret never appears in
// argv (visible to the whole agent tree via /proc/*/cmdline) nor in a
// persistent environment — only inside the 0700 script, for the duration
// of the call. Non-http remotes (local bare repos, file://) consult no
// credential: no helper is written.
func credentialEnv(o Opts) (additions []string, cleanup func(), err error) {
	noop := func() {}
	if o.Username == "" || o.Secret == "" {
		return nil, noop, nil
	}
	if !strings.HasPrefix(o.RemoteURL, "http://") && !strings.HasPrefix(o.RemoteURL, "https://") {
		return nil, noop, nil
	}
	// The script lives only under <GitDir>/../askpass, the directory
	// SweepAskpass clears at startup: no git-dir, no credential delivery.
	base := filepath.Dir(o.GitDir)
	if o.GitDir == "" || base == "." {
		return nil, noop, fmt.Errorf("gitops: credential delivery for %s needs the external git-dir (Opts.GitDir is %q)", o.WorkItemID, o.GitDir)
	}
	dir := filepath.Join(base, "askpass")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, noop, err
	}
	// MkdirAll keeps an existing dir's mode: pin 0700 every time.
	if err := os.Chmod(dir, 0o700); err != nil {
		return nil, noop, err
	}
	suffix, err := randomSuffix()
	if err != nil {
		return nil, noop, err
	}
	script := filepath.Join(dir, "askpass-"+o.WorkItemID+"-"+suffix)
	body := "#!/bin/sh\n" +
		"case \"$1\" in\n" +
		"  Username*) printf '%s\\n' " + shellQuote(o.Username) + " ;;\n" +
		"  *) printf '%s\\n' " + shellQuote(o.Secret) + " ;;\n" +
		"esac\n"
	if err := os.WriteFile(script, []byte(body), 0o700); err != nil {
		return nil, noop, err
	}
	cleanup = func() {
		_ = os.Remove(script)
		_ = os.Remove(dir) // best-effort: another WI's script may keep it alive
	}
	return []string{
		"GIT_ASKPASS=" + script,
		"GIT_TERMINAL_PROMPT=0",
	}, cleanup, nil
}

func randomSuffix() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// shellQuote single-quotes s for /bin/sh.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

// SweepAskpass removes the one-shot askpass scripts a killed process
// left behind. credentialEnv's cleanup only runs when the call unwinds:
// a daemon killed mid-push (SIGKILL, power loss) leaves its 0700
// script — which holds the push secret — under <gitRoot>/askpass
// forever. Call it ONCE at daemon startup with the directory that holds
// the external git-dirs (filepath.Dir of any GitDir, i.e.
// <DataDir>/git), holding the data-dir lock: nothing of another daemon
// can be live then, so every askpass-* entry is crash residue. Best-effort — an unreadable entry is skipped
// (the first removal error is reported, the sweep continues).
func SweepAskpass(gitRoot string) error {
	dir := filepath.Join(gitRoot, "askpass")
	ents, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var firstErr error
	for _, e := range ents {
		if e.IsDir() || !strings.HasPrefix(e.Name(), "askpass-") {
			continue
		}
		if rerr := os.Remove(filepath.Join(dir, e.Name())); rerr != nil && firstErr == nil {
			firstErr = rerr
		}
	}
	return firstErr
}

// SweepCarrierTemps removes the pf-carrier-* temporary bare repos a
// killed process left next to the external git-dirs (pushCarrier removes
// its own when the call unwinds). Call it ONCE at daemon startup with
// the same gitRoot as SweepAskpass, holding the data-dir lock (nothing of
// another daemon can be live then).
// Best-effort — the first removal error is reported, the sweep
// continues.
func SweepCarrierTemps(gitRoot string) error {
	ents, err := os.ReadDir(gitRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var firstErr error
	for _, e := range ents {
		if !e.IsDir() || !strings.HasPrefix(e.Name(), "pf-carrier-") {
			continue
		}
		if rerr := os.RemoveAll(filepath.Join(gitRoot, e.Name())); rerr != nil && firstErr == nil {
			firstErr = rerr
		}
	}
	return firstErr
}

// ---------------------------------------------------------------------------
// Stale lock handling

// cleanStaleLocks removes an index.lock only after it has outlived
// staleLockAge. git's index.lock holds the in-progress index (binary
// content), not a holder PID — nothing in the file can prove the holder
// dead, so a "PID parse" would delete live locks as garbage. Age is the
// only safe signal: our own locks are short-lived by construction (the
// per-WI git mutex serializes every git invocation of this WI, and each
// holds the index lock for one command only), so an index.lock older
// than the bound is stale by any honest reading — and a YOUNG lock,
// foreign or not, is left alone for git to report loudly against (the
// push fails and is retried at the next boundary).
// tempFilePattern matches atomicfile's in-flight temp files
// (".<name>.<rand>.tmp"): the driver writes execution.json and the
// transcript copies that way inside the capsule while a publish runs.
const tempFilePattern = ".*.tmp"

// excludeTempFiles keeps those temp files out of `git add -A`: one may
// vanish between the status listing and the add ("unable to stat"), and
// none is ever capsule content. It lives in the capsule git-dir's
// info/exclude, never in the tracked .gitignore.
func excludeTempFiles(o Opts) error {
	gitDir := o.GitDir
	if gitDir == "" {
		gitDir = filepath.Join(o.CapsuleDir, ".git")
	}
	if st, err := os.Stat(gitDir); err != nil || !st.IsDir() {
		return nil // no git-dir: the git call itself reports it
	}
	path := filepath.Join(gitDir, "info", "exclude")
	raw, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.TrimSpace(line) == tempFilePattern {
			return nil
		}
	}
	if len(raw) > 0 && !strings.HasSuffix(string(raw), "\n") {
		raw = append(raw, '\n')
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return atomicfile.Write(path, append(raw, tempFilePattern+"\n"...), 0o644)
}

func cleanStaleLocks(gitDir string) error {
	if gitDir == "" {
		return nil
	}
	lockPath := filepath.Join(gitDir, "index.lock")
	st, err := os.Stat(lockPath)
	if err != nil {
		return nil // absent or unreadable: git reports loudly if it matters
	}
	if time.Since(st.ModTime()) < staleLockAge {
		return nil
	}
	return os.Remove(lockPath)
}

// ClearIndexLocks removes the work item's index.lock files whatever
// their age: the capsule git-dir's and each task repo's under
// capsuleDir/repos. Only for a caller that has just reaped every step
// process of the work item; the per-WI git mutex taken here excludes
// the daemon's own git invocations, so no holder of those locks is
// alive. Returns the removed paths.
func ClearIndexLocks(workItemID, gitDir, capsuleDir string) []string {
	release := Lock(workItemID)
	defer release()
	var paths []string
	if gitDir != "" {
		paths = append(paths, filepath.Join(gitDir, "index.lock"))
	}
	if capsuleDir != "" {
		repos, _ := filepath.Glob(filepath.Join(capsuleDir, capsule.DirRepos, "*", ".git", "index.lock"))
		paths = append(paths, repos...)
	}
	var removed []string
	for _, p := range paths {
		if os.Remove(p) == nil {
			removed = append(removed, p)
		}
	}
	return removed
}

// EventTaskRefDiverged is the error event kind raised when the carrier
// push of one task repo is rejected non-fast-forward, its HEAD is not an
// ancestor of the carried head (a HEAD merely behind it is no divergence:
// nothing to carry), and the carried tip's content is not contained in
// that HEAD (a same-line rewind is reconciled instead). Raised once per
// repo per job until a carrier push of that repo succeeds again, like
// carrier_push_failed.
const EventTaskRefDiverged = "task_ref_diverged"

// EventCarrierPushFailed is the event kind raised when the carrier push
// of one task repo fails for any reason (blob limit, divergence,
// transport, a refused repo config): a re-claim restores that repo
// without the commits since its last carried head, so the watcher
// classifies it as a human decision (polyforge#158). Raised only after a
// chain push that landed, and once per repo per job until a carrier push
// of that repo succeeds again (CarrierAlerts).
const EventCarrierPushFailed = "carrier_push_failed"

// CarrierAlerts is one job's in-memory record of the carrier-lane refs
// (refs/pf-task/<name>, refs/pf-wip/<name>) whose failure has already
// been raised; a successful push of that ref clears it, so a later
// failure alerts again. The runtime keeps
// one per job: it ends with the job, and a re-claim (a new job) starts
// clean. The zero value is ready; a nil *CarrierAlerts raises every
// failure. The divergence event has its own once-until-success state, so
// a true divergence raises carrier_push_failed and task_ref_diverged
// together on its first failure and neither again while it persists.
type CarrierAlerts struct {
	mu       sync.Mutex
	m        map[string]bool
	diverged map[string]bool
}

// first records a failure of repo and reports whether it is the first
// since the repo's last success (i.e. whether to alert).
func (a *CarrierAlerts) first(repo string) bool {
	if a == nil {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.m[repo] {
		return false
	}
	if a.m == nil {
		a.m = map[string]bool{}
	}
	a.m[repo] = true
	return true
}

// firstDiverged records a task_ref_diverged failure of ref and reports
// whether it is the first since the ref's last successful carrier push.
func (a *CarrierAlerts) firstDiverged(ref string) bool {
	if a == nil {
		return true
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.diverged[ref] {
		return false
	}
	if a.diverged == nil {
		a.diverged = map[string]bool{}
	}
	a.diverged[ref] = true
	return true
}

// clear forgets repo's raised failure after a successful carrier push.
func (a *CarrierAlerts) clear(repo string) {
	if a == nil {
		return
	}
	a.mu.Lock()
	delete(a.m, repo)
	delete(a.diverged, repo)
	a.mu.Unlock()
}

// IsPermanentPushRejection reports a push the server answered and refused
// for a reason no retry can change: the pre-receive hook declined it
// (gitlink, blob size limit) or the ref moved
// non-fast-forward. A transport failure (unreachable, timeout, auth
// against a down proxy) is NOT permanent, nor is a lost ref-transaction
// race (refCasRejected). Matches git's C-locale output
// as carried in the push error (callers run git under CEnv).
func IsPermanentPushRejection(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	if refCasRejected(msg) {
		return false
	}
	return strings.Contains(msg, "[remote rejected]") ||
		strings.Contains(msg, "pre-receive hook declined") ||
		IsNonFastForward(err, "")
}

// IsTransientRemoteFailure reports a remote git call that failed for a
// reason a retry can change: the hub unreachable, the connection cut, a
// timeout, or a 5xx answer. An answer the server gave on purpose (auth
// refused, ref or repository not found, any other 4xx) and a local
// failure are not transient. Matches git's C-locale output (callers run
// git under CEnv).
func IsTransientRemoteFailure(err error) bool {
	var text string
	for e := err; e != nil; e = errors.Unwrap(e) {
		text += " " + strings.ToLower(e.Error())
	}
	for _, m := range []string{
		"timed out", "could not resolve host", "connection refused", "connection reset",
		"failed to connect", "couldn't connect", "early eof", "remote end hung up",
		"empty reply from server", "recv failure", "send failure", "returned error: 5",
	} {
		if strings.Contains(text, m) {
			return true
		}
	}
	return false
}

// IsCredentialRefused reports a remote git call the git face refused for
// its credential (HTTP 401/403 — for a session-scoped credential: the
// session no longer holds the work item). Matches git's C-locale output.
func IsCredentialRefused(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Authentication failed") ||
		strings.Contains(msg, "returned error: 401") ||
		strings.Contains(msg, "returned error: 403")
}

// IsNonFastForward reports a push refused because the remote ref is not an
// ancestor of the pushed commit. It matches git's C-locale output
// ("! [rejected] HEAD -> main (non-fast-forward)" / "(fetch first)"), so
// callers must run git under CEnv. err may be nil; out is the push output.
func IsNonFastForward(err error, out string) bool {
	msg := out
	if err != nil {
		msg += " " + err.Error()
	}
	return strings.Contains(msg, "non-fast-forward") || strings.Contains(msg, "(fetch first)")
}
