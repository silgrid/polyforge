package gitops

// gitops_taskref_test.go: the task-repo durability lane (polyforge#108)
// against REAL git — every publish mirrors each repos/<name> HEAD into
// the WI's bare repo as refs/pf-task/<name>, best-effort (a rejected
// carrier never fails the chain publish), and FetchTaskRef reads the
// carrier back for the claim-time restore.

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// taskOpts is testOpts with the base dir answered, so a test can lay a
// task-repo origin next to the capsule and the hub bare.
func taskOpts(t *testing.T, wi string) (Opts, string) {
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
	return o, base
}

// mkTaskCapsule is mkCapsule plus the capsule's own .gitignore — exactly
// what materialize writes before cloneTaskRepos runs — so the task clone
// under repos/ stays out of the capsule index the way production lays it
// (without it the gitlink precheck correctly refuses the whole publish).
func mkTaskCapsule(t *testing.T, o Opts) {
	t.Helper()
	mkCapsule(t, o)
	if err := os.WriteFile(filepath.Join(o.CapsuleDir, ".gitignore"), []byte(capsule.GitignoreContent), 0o644); err != nil {
		t.Fatal(err)
	}
}

// mkTaskRepoWithWork lays a task-repo origin (one init commit), clones it
// under the capsule's repos/<name>, and makes one local unpushed commit
// on top (the agent's work). Answers the work head SHA.
func mkTaskRepoWithWork(t *testing.T, capsuleDir, name, originDir string) string {
	t.Helper()
	origin := filepath.Join(originDir, name)
	if out, err := run("git", "init", "-q", "-b", "main", origin); err != nil {
		t.Fatalf("task origin init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(origin, "hello.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", origin, "add", "-A")
	mustRun(t, "git", "-C", origin, "commit", "-q", "-m", "init")
	repoDir := filepath.Join(capsuleDir, capsule.DirRepos, name)
	mustRun(t, "git", "clone", "-q", origin, repoDir)
	if err := os.WriteFile(filepath.Join(repoDir, "work.txt"), []byte("agent work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", repoDir, "add", "-A")
	mustRun(t, "git", "-C", repoDir, "commit", "-q", "-m", "repo work")
	return strings.TrimSpace(mustRun(t, "git", "-C", repoDir, "rev-parse", "HEAD"))
}

func mustRun(t *testing.T, args ...string) string {
	t.Helper()
	out, err := run(args...)
	if err != nil {
		t.Fatalf("%v: %s", args, out)
	}
	return out
}

func bareRefSHA(t *testing.T, bare, ref string) (string, bool) {
	t.Helper()
	out, err := run("git", "--git-dir", bare, "rev-parse", "--verify", "--quiet", ref)
	if err != nil {
		return "", false
	}
	return strings.TrimSpace(out), true
}

// A chain publish must carry the task-repo state with it: each
// repos/<name> HEAD lands in the WI's bare repo under
// refs/pf-task/<name> (the session-independent carrier a re-claim
// restores from), next to the chain tip.
func TestCommitPushCarriesTaskRefs(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_task1")
	mkBare(t, o.RemoteURL, false)
	mkTaskCapsule(t, o)
	workHead := mkTaskRepoWithWork(t, o.CapsuleDir, "demo-src", filepath.Join(base, "origin"))
	// A task repo with no commits carries nothing (unborn HEAD: honest skip).
	mustRun(t, "git", "init", "-q", "-b", "main", filepath.Join(o.CapsuleDir, capsule.DirRepos, "empty"))

	sha, _, _, err := CommitPush(context.Background(), o, "boundary")
	if err != nil {
		t.Fatalf("CommitPush: %v", err)
	}
	if got, ok := bareRefSHA(t, o.RemoteURL, "refs/heads/main"); !ok || got != sha {
		t.Fatalf("chain tip %q (%v), want the commit SHA %q", got, ok, sha)
	}
	got, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("demo-src"))
	if !ok || got != workHead {
		t.Fatalf("carrier ref = %q (%v), want the task work head %q", got, ok, workHead)
	}
	if _, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("empty")); ok {
		t.Fatal("an unborn task repo must not produce a carrier ref")
	}
}

// The task repo is agent-controlled: a pre-push hook planted in it must
// not run during the carrier push (it would run as the daemon with the
// credential askpass in its environment).
func TestTaskRefPushSkipsRepoHooks(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_taskhook")
	mkBare(t, o.RemoteURL, false)
	mkTaskCapsule(t, o)
	workHead := mkTaskRepoWithWork(t, o.CapsuleDir, "demo-src", filepath.Join(base, "origin"))
	marker := filepath.Join(base, "pre-push-ran")
	hook := filepath.Join(o.CapsuleDir, capsule.DirRepos, "demo-src", ".git", "hooks", "pre-push")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// Control: the planted hook is live for a plain push.
	ctl := filepath.Join(base, "ctl.git")
	mkBare(t, ctl, false)
	mustRun(t, "git", "-C", filepath.Join(o.CapsuleDir, capsule.DirRepos, "demo-src"), "push", "-q", ctl, "HEAD:refs/heads/x")
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("control: a plain push should have run the planted hook")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("CommitPush: %v", err)
	}
	if got, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("demo-src")); !ok || got != workHead {
		t.Fatalf("carrier ref = %q (%v), want %q", got, ok, workHead)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("the task repo's pre-push hook ran during the carrier push")
	}
}

// refuseCarrier installs a pre-receive hook that refuses every
// refs/pf-task/* update.
func refuseCarrier(t *testing.T, b *testBare) {
	t.Helper()
	refuse := "#!/bin/sh\nwhile read old new ref; do case \"$ref\" in refs/pf-task/*) echo \"carrier refused for the test\" >&2; exit 1 ;; esac; done\nexit 0\n"
	if err := os.WriteFile(filepath.Join(b.dir, "hooks", "pre-receive"), []byte(refuse), 0o755); err != nil {
		t.Fatal(err)
	}
}

// Best-effort-loud, mechanically: a carrier push REJECTED by the remote
// must not fail the chain publish. The chain tip lands; the rejected
// carrier does not; CommitPush answers success.
func TestCommitPushTaskRefRejectionNeverBlocksTheChain(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_task2")
	refuseCarrier(t, mkBare(t, o.RemoteURL, false))
	mkTaskCapsule(t, o)
	mkTaskRepoWithWork(t, o.CapsuleDir, "demo-src", filepath.Join(base, "origin"))

	sha, _, _, err := CommitPush(context.Background(), o, "boundary")
	if err != nil {
		t.Fatalf("a rejected task-ref push must never fail the chain publish: %v", err)
	}
	if got, ok := bareRefSHA(t, o.RemoteURL, "refs/heads/main"); !ok || got != sha {
		t.Fatalf("chain tip missing after a rejected carrier: %q (%v)", got, ok)
	}
	if _, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("demo-src")); ok {
		t.Fatal("the rejected carrier ref must not be present")
	}
}

// The claim-side read: a fresh clone of the task origin (the re-claim
// shape — the previous session's capsule directory is gone) plus
// FetchTaskRef answers the carried head, and the update-ref + reset
// recipe lands the worktree on it, work files and all. With no carrier
// recorded, FetchTaskRef answers "" quietly.
func TestFetchTaskRefRoundTrip(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_task3")
	mkBare(t, o.RemoteURL, false)
	mkTaskCapsule(t, o)
	origin := filepath.Join(base, "origin", "demo-src")
	workHead := mkTaskRepoWithWork(t, o.CapsuleDir, "demo-src", filepath.Join(base, "origin"))
	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("CommitPush: %v", err)
	}

	// No carrier for a repo that was never published: quiet absence.
	emptyDir := filepath.Join(base, "fresh-empty")
	mustRun(t, "git", "clone", "-q", origin, emptyDir)
	sha, err := FetchTaskRef(context.Background(), o, emptyDir, "demo-src-nothing")
	if err != nil || sha != "" {
		t.Fatalf("absent carrier: sha %q err %v, want \"\" nil", sha, err)
	}

	// The re-claim shape: a FRESH clone of the bare origin, previous
	// session gone.
	fresh := filepath.Join(base, "fresh-clone")
	mustRun(t, "git", "clone", "-q", origin, fresh)
	sha, err = FetchTaskRef(context.Background(), o, fresh, "demo-src")
	if err != nil {
		t.Fatalf("FetchTaskRef: %v", err)
	}
	if sha != workHead {
		t.Fatalf("carrier answered %q, want the work head %q", sha, workHead)
	}
	// The restore recipe (runtime.restoreTaskRepoState's git side): point
	// the checked-out branch at the carried head, sync index+worktree.
	mustRun(t, "git", "-C", fresh, "update-ref", "-m", "pf: restore task-repo state across re-claim", "refs/heads/main", sha)
	mustRun(t, "git", "-C", fresh, "reset", "--hard", "--quiet")
	if got := strings.TrimSpace(mustRun(t, "git", "-C", fresh, "rev-parse", "HEAD")); got != workHead {
		t.Fatalf("restored HEAD %q, want %q", got, workHead)
	}
	if b, err := os.ReadFile(filepath.Join(fresh, "work.txt")); err != nil || string(b) != "agent work\n" {
		t.Fatalf("restored work.txt wrong: %q, %v", b, err)
	}
}

// The lane is a no-op on a capsule with no repos/ tree (the common
// no-inventory shape): nothing pushed, nothing logged, publish unaffected.
func TestPushTaskRefsWithoutReposIsANoop(t *testing.T) {
	setIdentity(t)
	o, _ := taskOpts(t, "wi_task4")
	mkBare(t, o.RemoteURL, false)
	mkTaskCapsule(t, o)
	writeDirty(t, o, "state.txt", "one\n") // a publishable chain commit
	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("CommitPush: %v", err)
	}
	if _, ok := bareRefSHA(t, o.RemoteURL, TaskCarrierNamespace+"x"); ok {
		t.Fatal("no carrier ref may exist without a repos/ tree")
	}
}

// Never force: a re-claim that started from a clone LACKING the carried
// commits publishes a diverged head. The carrier push is rejected
// non-fast-forward, the carrier on the hub stays on the WI's commits, and
// the divergence is raised as a task_ref_diverged error event (not only
// stderr).
func TestTaskRefDivergenceIsRejectedAndRaisedAsEvent(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_task3")
	mkBare(t, o.RemoteURL, false)
	mkTaskCapsule(t, o)
	workHead := mkTaskRepoWithWork(t, o.CapsuleDir, "demo-src", filepath.Join(base, "origin"))
	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("first CommitPush: %v", err)
	}
	if got, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("demo-src")); !ok || got != workHead {
		t.Fatalf("carrier = %q (%v), want %q", got, ok, workHead)
	}

	// Diverge: drop the work commit, commit something else.
	repoDir := filepath.Join(o.CapsuleDir, capsule.DirRepos, "demo-src")
	mustRun(t, "git", "-C", repoDir, "reset", "-q", "--hard", "HEAD~1")
	if err := os.WriteFile(filepath.Join(repoDir, "other.txt"), []byte("diverged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", repoDir, "add", "-A")
	mustRun(t, "git", "-C", repoDir, "commit", "-q", "-m", "diverged work")

	var kinds []string
	var body map[string]any
	o.EventSink = func(kind string, b map[string]any) {
		kinds = append(kinds, kind)
		body = b
	}
	if _, _, _, err := CommitPush(context.Background(), o, "boundary 2"); err != nil {
		t.Fatalf("a rejected carrier must not fail the chain publish: %v", err)
	}
	if got, _ := bareRefSHA(t, o.RemoteURL, TaskRefName("demo-src")); got != workHead {
		t.Fatalf("carrier moved to %q: a diverged push must never overwrite it (want %q)", got, workHead)
	}
	// A divergence is a carrier failure too: the human decision first,
	// then the divergence detail.
	if len(kinds) != 2 || kinds[0] != EventCarrierPushFailed || kinds[1] != EventTaskRefDiverged {
		t.Fatalf("events = %v, want [%s %s]", kinds, EventCarrierPushFailed, EventTaskRefDiverged)
	}
	if body["repo"] != "demo-src" || body["ref"] != TaskRefName("demo-src") {
		t.Fatalf("event body = %v", body)
	}
}

// nonEnglishLocale returns an installed non-C/POSIX/English locale name
// (as `locale -a` lists it), or "" when none is available.
func nonEnglishLocale() string {
	out, err := exec.Command("locale", "-a").Output()
	if err != nil {
		return ""
	}
	for _, l := range strings.Fields(string(out)) {
		if l == "C" || l == "POSIX" || strings.HasPrefix(l, "C.") || strings.HasPrefix(l, "en") {
			continue
		}
		return l
	}
	return ""
}

// CEnv is deterministic: it drops every inherited LC_ALL/LANGUAGE and
// pins LC_ALL=C last, whatever the daemon's own environment says.
func TestCEnvPinsCLocale(t *testing.T) {
	got := CEnv([]string{"PATH=/bin", "LC_ALL=zh_CN.UTF-8", "LANGUAGE=zh_CN", "LANG=zh_CN.UTF-8"})
	var lcAll []string
	for _, kv := range got {
		if strings.HasPrefix(kv, "LANGUAGE=") {
			t.Fatalf("LANGUAGE leaked: %v", got)
		}
		if strings.HasPrefix(kv, "LC_ALL=") {
			lcAll = append(lcAll, kv)
		}
	}
	if len(lcAll) != 1 || lcAll[0] != "LC_ALL=C" || got[len(got)-1] != "LC_ALL=C" {
		t.Fatalf("LC_ALL must be exactly one trailing LC_ALL=C: %v", got)
	}
}

// IsNonFastForward must match REAL git push output, and git must run
// under the C locale however the daemon's environment is set.
func TestIsNonFastForwardOnRealGitOutput(t *testing.T) {
	setIdentity(t)
	loc := nonEnglishLocale()
	if loc == "" {
		t.Skip("no non-English locale installed (locale -a): the C-locale guarantee cannot be exercised against real git")
	}
	t.Setenv("LC_ALL", loc)
	t.Setenv("LANGUAGE", "zh_CN")
	base := t.TempDir()
	bare := filepath.Join(base, "b.git")
	mustRun(t, "git", "init", "-q", "--bare", "-b", "main", bare)
	a, b := filepath.Join(base, "a"), filepath.Join(base, "b")
	mustRun(t, "git", "clone", "-q", bare, a)
	mustRun(t, "git", "-C", a, "commit", "-q", "--allow-empty", "-m", "one")
	mustRun(t, "git", "-C", a, "push", "-q", "origin", "HEAD:main")
	mustRun(t, "git", "clone", "-q", bare, b)
	mustRun(t, "git", "-C", b, "commit", "-q", "--allow-empty", "-m", "b-side")
	mustRun(t, "git", "-C", a, "commit", "-q", "--allow-empty", "-m", "a-side")
	mustRun(t, "git", "-C", a, "push", "-q", "origin", "HEAD:main")

	out, err := git(nil, b, "push", "origin", "HEAD:main")
	if err == nil {
		t.Fatal("the diverged push must be rejected")
	}
	if !IsNonFastForward(err, out) {
		t.Fatalf("real git rejection not recognised: err=%v out=%q", err, out)
	}
	if !strings.Contains(err.Error(), "[rejected]") {
		t.Fatalf("git did not run under the C locale: %v", err)
	}
	if IsNonFastForward(errors.New("git push: Could not resolve host"), "") {
		t.Fatal("an unreachable remote is not a non-fast-forward")
	}
	// The carrier-absent answer is the ls-remote exit code, locale-free.
	o := Opts{WorkItemID: "wi_loc", RemoteURL: bare, Username: "u", Secret: "s"}
	if sha, err := FetchTaskRef(context.Background(), o, b, "nothing"); err != nil || sha != "" {
		t.Fatalf("absent carrier under a foreign locale: %q, %v", sha, err)
	}
}

// polyforge#158: a failed carrier push is raised as a carrier_push_failed
// event (the watcher's human decision) — ONCE per repo per job (the
// runtime's CarrierAlerts), not on every publish, until a carrier push of that repo succeeds again; a
// later failure then alerts again. The body names the repo and keeps the
// git error as detail (the watcher's line never shows it).
func TestCarrierPushFailureAlertsOnceUntilSuccess(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_task6")
	bare := mkBare(t, o.RemoteURL, false)
	refuseCarrier(t, bare)
	mkTaskCapsule(t, o)
	workHead := mkTaskRepoWithWork(t, o.CapsuleDir, "demo-src", filepath.Join(base, "origin"))
	o.CarrierAlerts = &CarrierAlerts{} // the runtime's per-job state

	var kinds []string
	var bodies []map[string]any
	o.EventSink = func(kind string, b map[string]any) {
		kinds = append(kinds, kind)
		bodies = append(bodies, b)
	}
	publish := func(label string) {
		t.Helper()
		if _, _, _, err := CommitPush(context.Background(), o, label); err != nil {
			t.Fatalf("CommitPush(%s): %v", label, err)
		}
	}

	// 1. Rejected carrier: one alert.
	publish("boundary 1")
	if len(kinds) != 1 || kinds[0] != EventCarrierPushFailed {
		t.Fatalf("events = %v, want one %s", kinds, EventCarrierPushFailed)
	}
	if bodies[0]["repo"] != "demo-src" || bodies[0]["ref"] != TaskRefName("demo-src") {
		t.Fatalf("event body = %v", bodies[0])
	}
	if e, _ := bodies[0]["error"].(string); !strings.Contains(e, "carrier refused for the test") {
		t.Fatalf("event body must keep the git error as detail: %v", bodies[0])
	}

	// 2. Still failing on the next publish: no second alert.
	publish("boundary 2")
	if len(kinds) != 1 {
		t.Fatalf("a persisting failure must not alert again: %v", kinds)
	}

	// 3. The carrier push succeeds (the hub accepts it now): no event,
	// and the alert state is cleared.
	if err := os.Remove(filepath.Join(bare.dir, "hooks", "pre-receive")); err != nil {
		t.Fatal(err)
	}
	publish("boundary 3")
	if len(kinds) != 1 {
		t.Fatalf("a successful carrier push must raise nothing: %v", kinds)
	}
	if got, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("demo-src")); !ok || got != workHead {
		t.Fatalf("carrier = %q (%v), want %q", got, ok, workHead)
	}

	// 4. A new failure after the success alerts again (here: a diverged
	// head, rejected non-fast-forward).
	repoDir := filepath.Join(o.CapsuleDir, capsule.DirRepos, "demo-src")
	mustRun(t, "git", "-C", repoDir, "reset", "-q", "--hard", "HEAD~1")
	if err := os.WriteFile(filepath.Join(repoDir, "other.txt"), []byte("diverged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRun(t, "git", "-C", repoDir, "add", "-A")
	mustRun(t, "git", "-C", repoDir, "commit", "-q", "-m", "diverged work")
	publish("boundary 4")
	if len(kinds) != 3 || kinds[1] != EventCarrierPushFailed || kinds[2] != EventTaskRefDiverged {
		t.Fatalf("events = %v, want a second %s after the success", kinds, EventCarrierPushFailed)
	}
}

// The once-per-repo state is per repo (and, held by the runtime job, per
// job): a failure of one repo never silences another's, a success clears
// it, and a nil state (no job wiring) raises every failure.
func TestCarrierAlertsOncePerRepoUntilSuccess(t *testing.T) {
	a := &CarrierAlerts{}
	if !a.first("r1") || a.first("r1") {
		t.Fatal("r1: the first failure alerts, the second does not")
	}
	if !a.first("r2") {
		t.Fatal("another repo must alert on its own")
	}
	a.clear("r1")
	if !a.first("r1") {
		t.Fatal("after a success the next failure alerts again")
	}
	if !(&CarrierAlerts{}).first("r1") {
		t.Fatal("a fresh job's state (a re-claim) must alert again")
	}
	var none *CarrierAlerts
	if !none.first("r1") || !none.first("r1") {
		t.Fatal("a nil state raises every failure")
	}
	none.clear("r1") // must not panic
}

// SHOULD-1: a carrier failure is a "cannot be saved" decision only when
// the chain push landed. A publish whose chain push fails too (hub
// outage, fenced session — here: a hub that refuses every ref) raises no
// carrier_push_failed; the still-failing carrier is reported on the next
// publish whose chain push succeeds.
func TestCarrierPushFailedOnlyAfterTheChainLanded(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_task7")
	mkBare(t, o.RemoteURL, false)
	mkTaskCapsule(t, o)
	mkTaskRepoWithWork(t, o.CapsuleDir, "demo-src", filepath.Join(base, "origin"))
	o.CarrierAlerts = &CarrierAlerts{}
	var kinds []string
	o.EventSink = func(kind string, _ map[string]any) { kinds = append(kinds, kind) }
	hook := filepath.Join(o.RemoteURL, "hooks", "pre-receive")
	setHook := func(body string) {
		t.Helper()
		if err := os.WriteFile(hook, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// Every ref refused: the chain push fails, so does the carrier.
	setHook("echo 'hub refuses everything' >&2; exit 1\n")
	if _, _, _, err := CommitPush(context.Background(), o, "boundary 1"); err == nil {
		t.Fatal("the chain push must fail against a hub refusing every ref")
	}
	if len(kinds) != 0 {
		t.Fatalf("a failed chain push must raise no carrier decision: %v", kinds)
	}

	// The chain lands, the carrier is still refused: reported now.
	setHook(`while read old new ref; do case "$ref" in refs/pf-task/*) echo "carrier refused" >&2; exit 1;; esac; done; exit 0
`)
	if _, _, _, err := CommitPush(context.Background(), o, "boundary 2"); err != nil {
		t.Fatalf("CommitPush: %v", err)
	}
	if len(kinds) != 1 || kinds[0] != EventCarrierPushFailed {
		t.Fatalf("events = %v, want one %s after the chain landed", kinds, EventCarrierPushFailed)
	}
}

// NIT-1: event bodies carry a bounded git error.
func TestEventDetailIsBounded(t *testing.T) {
	if got := EventDetail("short"); got != "short" {
		t.Fatalf("short detail changed: %q", got)
	}
	long := strings.Repeat("é", eventDetailMax) // 2 bytes per rune
	got := EventDetail(long)
	if len(got) > eventDetailMax+len("…(truncated)") || !strings.HasSuffix(got, "…(truncated)") {
		t.Fatalf("detail not truncated: %d bytes", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatal("truncation split a rune")
	}
}

// SECURITY: the credentialed carrier push never runs inside the task
// repo. It runs from a daemon-owned temporary bare repo (--git-dir, a
// pf-carrier- temp dir outside the capsule) with the hardening flags, a
// protocol allow-list of the remote's own scheme and no submodule
// recursion, pushing the HEAD SHA read from the task repo.
func TestCarrierPushRunsFromATempRepo(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_task8")
	mkBare(t, o.RemoteURL, false)
	mkTaskCapsule(t, o)
	workHead := mkTaskRepoWithWork(t, o.CapsuleDir, "demo-src", filepath.Join(base, "origin"))
	var carrier []string
	gitArgsProbe = func(args []string) {
		for _, a := range args {
			if strings.HasSuffix(a, ":"+TaskRefName("demo-src")) {
				carrier = append([]string{}, args...)
			}
		}
	}
	t.Cleanup(func() { gitArgsProbe = nil })
	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("CommitPush: %v", err)
	}
	if len(carrier) == 0 || !strings.HasPrefix(carrier[0], "--git-dir=") {
		t.Fatalf("carrier push argv = %q, want a --git-dir temp repo", carrier)
	}
	tmp := strings.TrimPrefix(carrier[0], "--git-dir=")
	if !strings.HasPrefix(filepath.Base(tmp), "pf-carrier-") || strings.HasPrefix(tmp, o.CapsuleDir) {
		t.Fatalf("temp repo %q must be a pf-carrier- dir outside the capsule", tmp)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("the temp repo %q must be removed after the push", tmp)
	}
	want := []string{
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "credential.helper=", "-c", "http.proxy=",
		"-c", "protocol.allow=never", "-c", "protocol.file.allow=always",
		"push", "--no-recurse-submodules", o.RemoteURL, workHead + ":" + TaskRefName("demo-src"),
	}
	if strings.Join(carrier[1:], " ") != strings.Join(want, " ") {
		t.Fatalf("carrier push argv = %q\nwant --git-dir=<tmp> %q", carrier, want)
	}
	if got, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("demo-src")); !ok || got != workHead {
		t.Fatalf("carrier = %q (%v), want %q", got, ok, workHead)
	}
}

// hostileCarrierEnv is an http:// hub with basic auth (startGitHTTP) plus
// a capsule whose task repo the test then poisons. Answers the opts, the
// task repo dir, the hub bare and the task repo's work head.
func hostileCarrierEnv(t *testing.T) (Opts, string, string, string) {
	t.Helper()
	setIdentity(t)
	root := t.TempDir()
	repo := filepath.Join(root, "wi_http.git")
	mustRun(t, "git", "init", "-q", "--bare", repo)
	mustRun(t, "git", "--git-dir", repo, "config", "http.receivepack", "true")
	mustRun(t, "git", "--git-dir", repo, "config", "receive.advertisePushOptions", "true")
	url := startGitHTTP(t, root)
	o, base := taskOpts(t, "wi_http")
	o.RemoteURL = url + "/wi_http.git"
	o.Secret = "http-push-secret"
	mkTaskCapsule(t, o)
	head := mkTaskRepoWithWork(t, o.CapsuleDir, "demo-src", filepath.Join(base, "origin"))
	return o, filepath.Join(o.CapsuleDir, capsule.DirRepos, "demo-src"), repo, head
}

// attackerServer records every request it receives (asking for Basic
// auth, so a git client would hand over a credential).
func attackerServer(t *testing.T) (string, func() []string) {
	t.Helper()
	var mu sync.Mutex
	var hits []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.Method+" "+r.URL.String()+" auth="+r.Header.Get("Authorization"))
		mu.Unlock()
		w.Header().Set("WWW-Authenticate", `Basic realm="attacker"`)
		http.Error(w, "auth", http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() []string { mu.Lock(); defer mu.Unlock(); return append([]string{}, hits...) }
}

// publishCarrier runs one publish and answers the event kinds raised.
func publishCarrier(t *testing.T, o Opts) []string {
	t.Helper()
	var kinds []string
	o.EventSink = func(kind string, _ map[string]any) { kinds = append(kinds, kind) }
	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("CommitPush: %v", err)
	}
	return kinds
}

// assertCarrierLanded: the carrier landed on the real hub at head and no
// carrier event was raised.
func assertCarrierLanded(t *testing.T, kinds []string, hub, head string) {
	t.Helper()
	if got, ok := bareRefSHA(t, hub, TaskRefName("demo-src")); !ok || got != head {
		t.Fatalf("carrier on the real hub = %q (%v), want %q", got, ok, head)
	}
	if len(kinds) != 0 {
		t.Fatalf("a landed carrier must raise nothing: %v", kinds)
	}
}

// Control: over http:// with the credential, a clean task repo's carrier
// lands.
func TestCarrierPushOverHTTPLands(t *testing.T) {
	o, _, hub, head := hostileCarrierEnv(t)
	assertCarrierLanded(t, publishCarrier(t, o), hub, head)
}

// SECURITY: a credential.helper planted in the task repo's config never
// runs (the push does not read that config) and the carrier lands.
func TestCarrierIgnoresRepoCredentialHelper(t *testing.T) {
	o, repoDir, hub, head := hostileCarrierEnv(t)
	marker := filepath.Join(t.TempDir(), "helper-ran")
	mustRun(t, "git", "-C", repoDir, "config", "credential.helper", "!f() { cat >> "+marker+"; }; f")
	kinds := publishCarrier(t, o)
	if b, err := os.ReadFile(marker); err == nil {
		t.Fatalf("the planted credential helper ran (secret seen: %v): %q", strings.Contains(string(b), o.Secret), b)
	}
	assertCarrierLanded(t, kinds, hub, head)
}

// SECURITY: url.<attacker>.insteadOf / pushInsteadOf planted in the task
// repo cannot redirect the credentialed push: the attacker gets nothing
// and the carrier lands on the real hub.
func TestCarrierIgnoresRepoURLRewrite(t *testing.T) {
	for _, key := range []string{"insteadOf", "pushInsteadOf"} {
		t.Run(key, func(t *testing.T) {
			o, repoDir, hub, head := hostileCarrierEnv(t)
			evil, hits := attackerServer(t)
			mustRun(t, "git", "-C", repoDir, "config", "url."+evil+"/x.git."+key, o.RemoteURL)
			kinds := publishCarrier(t, o)
			if h := hits(); len(h) != 0 {
				t.Fatalf("the attacker received requests: %v", h)
			}
			assertCarrierLanded(t, kinds, hub, head)
		})
	}
}

// SECURITY (BLOCK 1): a remote named like the carrier URL with a vcs
// helper (`[remote "<url>"] vcs=evil`, git-remote-evil on PATH) would
// turn the push into running the helper with GIT_ASKPASS. It never runs.
func TestCarrierIgnoresRemoteHelper(t *testing.T) {
	o, repoDir, hub, head := hostileCarrierEnv(t)
	bin := t.TempDir()
	marker := filepath.Join(t.TempDir(), "helper-ran")
	script := "#!/bin/sh\n{ echo ran; [ -n \"$GIT_ASKPASS\" ] && \"$GIT_ASKPASS\" Password; } >> " + marker + "\nexit 1\n"
	if err := os.WriteFile(filepath.Join(bin, "git-remote-evil"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	mustRun(t, "git", "-C", repoDir, "config", "remote."+o.RemoteURL+".url", o.RemoteURL)
	mustRun(t, "git", "-C", repoDir, "config", "remote."+o.RemoteURL+".vcs", "evil")
	// Control: a push from INSIDE the repo does run the planted helper.
	ctl := exec.Command("git", "-C", repoDir, "push", o.RemoteURL, "HEAD:refs/heads/ctl")
	ctl.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	_ = ctl.Run()
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("control: the planted remote helper should run for an in-repo push")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}

	kinds := publishCarrier(t, o)
	if b, err := os.ReadFile(marker); err == nil {
		t.Fatalf("the planted remote helper ran (secret seen: %v): %q", strings.Contains(string(b), o.Secret), b)
	}
	assertCarrierLanded(t, kinds, hub, head)
}

// SECURITY (BLOCK 2): push.recurseSubmodules=on-demand /
// submodule.recurse=true plus a submodule whose origin is the attacker
// and a bumped gitlink would push the submodule first, credential and
// all. The carrier push never recurses: the attacker gets nothing.
func TestCarrierIgnoresSubmoduleRecursion(t *testing.T) {
	o, repoDir, hub, _ := hostileCarrierEnv(t)
	evil, hits := attackerServer(t)
	subOrigin := filepath.Join(t.TempDir(), "sub")
	mustRun(t, "git", "init", "-q", "-b", "main", subOrigin)
	mustRun(t, "git", "-C", subOrigin, "commit", "-q", "--allow-empty", "-m", "sub init")
	mustRun(t, "git", "-C", repoDir, "-c", "protocol.file.allow=always", "submodule", "add", "-q", subOrigin, "sub")
	mustRun(t, "git", "-C", repoDir, "commit", "-q", "-m", "add sub")
	sub := filepath.Join(repoDir, "sub")
	mustRun(t, "git", "-C", sub, "commit", "-q", "--allow-empty", "-m", "sub bump")
	mustRun(t, "git", "-C", sub, "remote", "set-url", "origin", evil+"/sub.git")
	mustRun(t, "git", "-C", repoDir, "add", "sub")
	mustRun(t, "git", "-C", repoDir, "commit", "-q", "-m", "bump sub")
	mustRun(t, "git", "-C", repoDir, "config", "push.recurseSubmodules", "on-demand")
	mustRun(t, "git", "-C", repoDir, "config", "submodule.recurse", "true")
	head := strings.TrimSpace(mustRun(t, "git", "-C", repoDir, "rev-parse", "HEAD"))

	kinds := publishCarrier(t, o)
	if h := hits(); len(h) != 0 {
		t.Fatalf("the attacker received requests: %v", h)
	}
	assertCarrierLanded(t, kinds, hub, head)
}

// SECURITY (confused deputy): a task repo whose .git is not its own real
// directory — a gitfile or a symlink to another repository, a missing
// .git (discovery would walk up to the capsule), or an object store
// borrowing from another one (objects/info/alternates) — is refused
// before any credential is prepared, as a carrier_push_failed.
func TestCarrierRefusesForeignGitDir(t *testing.T) {
	cases := map[string]func(t *testing.T, repoDir string){
		"gitfile": func(t *testing.T, repoDir string) {
			other := filepath.Join(t.TempDir(), "other.git")
			if err := os.Rename(filepath.Join(repoDir, ".git"), other); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(repoDir, ".git"), []byte("gitdir: "+other+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, repoDir string) {
			other := filepath.Join(t.TempDir(), "other.git")
			if err := os.Rename(filepath.Join(repoDir, ".git"), other); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(other, filepath.Join(repoDir, ".git")); err != nil {
				t.Fatal(err)
			}
		},
		"missing": func(t *testing.T, repoDir string) {
			if err := os.RemoveAll(filepath.Join(repoDir, ".git")); err != nil {
				t.Fatal(err)
			}
		},
		"commondir": func(t *testing.T, repoDir string) {
			// Another (valid) repository's storage: git would read its
			// refs and objects through this repo's .git.
			other := filepath.Join(t.TempDir(), "other")
			mustRun(t, "git", "init", "-q", "-b", "main", other)
			mustRun(t, "git", "-C", other, "commit", "-q", "--allow-empty", "-m", "another work item")
			if err := os.WriteFile(filepath.Join(repoDir, ".git", "commondir"), []byte(filepath.Join(other, ".git")+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"http-alternates": func(t *testing.T, repoDir string) {
			info := filepath.Join(repoDir, ".git", "objects", "info")
			if err := os.MkdirAll(info, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(info, "http-alternates"), []byte("http://elsewhere/objects\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
		"symlinked-packed-refs": func(t *testing.T, repoDir string) {
			mustRun(t, "git", "-C", repoDir, "pack-refs", "--all")
			symlinkAway(t, filepath.Join(repoDir, ".git", "packed-refs"))
		},
		"symlinked-refs":       func(t *testing.T, repoDir string) { symlinkAway(t, filepath.Join(repoDir, ".git", "refs")) },
		"symlinked-refs-heads": func(t *testing.T, repoDir string) { symlinkAway(t, filepath.Join(repoDir, ".git", "refs", "heads")) },
		"symlinked-HEAD":       func(t *testing.T, repoDir string) { symlinkAway(t, filepath.Join(repoDir, ".git", "HEAD")) },
		"symlinked-objects-pack": func(t *testing.T, repoDir string) {
			if err := os.MkdirAll(filepath.Join(repoDir, ".git", "objects", "pack"), 0o755); err != nil {
				t.Fatal(err)
			}
			symlinkAway(t, filepath.Join(repoDir, ".git", "objects", "pack"))
		},
		"symlinked-fanout": func(t *testing.T, repoDir string) {
			objects := filepath.Join(repoDir, ".git", "objects")
			ents, err := os.ReadDir(objects)
			if err != nil {
				t.Fatal(err)
			}
			for _, e := range ents {
				if len(e.Name()) == 2 {
					symlinkAway(t, filepath.Join(objects, e.Name()))
					return
				}
			}
			t.Fatal("no loose-object fan-out dir to symlink")
		},
		"alternates": func(t *testing.T, repoDir string) {
			info := filepath.Join(repoDir, ".git", "objects", "info")
			if err := os.MkdirAll(info, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(info, "alternates"), []byte(t.TempDir()+"\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, poison := range cases {
		t.Run(name, func(t *testing.T) {
			setIdentity(t)
			o, base := taskOpts(t, "wi_task9")
			mkBare(t, o.RemoteURL, false)
			mkTaskCapsule(t, o)
			mkTaskRepoWithWork(t, o.CapsuleDir, "demo-src", filepath.Join(base, "origin"))
			poison(t, filepath.Join(o.CapsuleDir, capsule.DirRepos, "demo-src"))
			var kinds []string
			var bodies []map[string]any
			o.EventSink = func(kind string, b map[string]any) { kinds = append(kinds, kind); bodies = append(bodies, b) }
			if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
				t.Fatalf("a refused carrier must not fail the chain publish: %v", err)
			}
			if _, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("demo-src")); ok {
				t.Fatal("a refused carrier must not land")
			}
			if len(kinds) != 1 || kinds[0] != EventCarrierPushFailed {
				t.Fatalf("events = %v, want one %s", kinds, EventCarrierPushFailed)
			}
			if e, _ := bodies[0]["error"].(string); !strings.Contains(e, "carrier push refused") {
				t.Fatalf("event body = %v", bodies[0])
			}
		})
	}
}

// symlinkAway moves path to a temp location and leaves a symlink to it
// in its place (the same content, reached through a link).
func symlinkAway(t *testing.T, path string) {
	t.Helper()
	other := filepath.Join(t.TempDir(), filepath.Base(path))
	if err := os.Rename(path, other); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(other, path); err != nil {
		t.Fatal(err)
	}
}

// The in-task-repo reads carry -c safe.directory=<repo> (they keep
// working once the repo belongs to another uid, #135).
func TestCarrierChecksCarrySafeDirectory(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_task10")
	mkBare(t, o.RemoteURL, false)
	mkTaskCapsule(t, o)
	mkTaskRepoWithWork(t, o.CapsuleDir, "demo-src", filepath.Join(base, "origin"))
	repos, err := filepath.EvalSymlinks(filepath.Join(o.CapsuleDir, capsule.DirRepos))
	if err != nil {
		t.Fatal(err)
	}
	want := "safe.directory=" + filepath.Join(repos, "demo-src")
	var mu sync.Mutex
	var revParse [][]string
	gitArgsProbe = func(args []string) {
		for _, a := range args {
			if a == "rev-parse" {
				mu.Lock()
				revParse = append(revParse, append([]string{}, args...))
				mu.Unlock()
			}
		}
	}
	t.Cleanup(func() { gitArgsProbe = nil })
	if _, _, _, err := CommitPush(context.Background(), o, "boundary"); err != nil {
		t.Fatalf("CommitPush: %v", err)
	}
	seen := 0
	for _, args := range revParse {
		if strings.Contains(strings.Join(args, " "), "--absolute-git-dir") || strings.Contains(strings.Join(args, " "), "HEAD^{commit}") {
			seen++
			if !strings.Contains(strings.Join(args, " "), want) {
				t.Fatalf("in-repo read without %s: %q", want, args)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("saw %d carrier reads, want 2: %q", seen, revParse)
	}
}

// A hung hub cannot hold the per-WI lock forever: the carrier push is
// bounded (carrierPushTimeout) and its git process group is killed.
func TestCarrierPushTimesOut(t *testing.T) {
	setIdentity(t)
	o, base := taskOpts(t, "wi_task11")
	mkBare(t, o.RemoteURL, false)
	mkTaskCapsule(t, o)
	mkTaskRepoWithWork(t, o.CapsuleDir, "demo-src", filepath.Join(base, "origin"))
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() { // accept and never answer
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = c.Close() })
		}
	}()
	prev := carrierPushTimeout
	carrierPushTimeout = 2 * time.Second
	t.Cleanup(func() { carrierPushTimeout = prev })
	repos, err := filepath.EvalSymlinks(filepath.Join(o.CapsuleDir, capsule.DirRepos))
	if err != nil {
		t.Fatal(err)
	}
	objects, sha, reason := carrierSourceT(Opts{}, repos, "demo-src")
	if reason != "" || sha == "" {
		t.Fatalf("carrierSource = %q, %q", sha, reason)
	}
	o.RemoteURL = "http://" + ln.Addr().String() + "/wi_task11.git"
	start := time.Now()
	_, err = pushCarrier(o, repos, objects, sha, "demo-src", nil)
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("pushCarrier against a hung hub = %v, want a timeout", err)
	}
	if d := time.Since(start); d > 20*time.Second {
		t.Fatalf("the timed-out push took %s", d)
	}
}

// Startup sweep: pf-carrier-* leftovers next to the git-dirs go, nothing
// else does.
func TestSweepCarrierTemps(t *testing.T) {
	root := t.TempDir()
	for _, d := range []string{"pf-carrier-123", "pf-carrier-abc/objects", "wi_x.git", "askpass"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := SweepCarrierTemps(root); err != nil {
		t.Fatalf("SweepCarrierTemps: %v", err)
	}
	ents, _ := os.ReadDir(root)
	var left []string
	for _, e := range ents {
		left = append(left, e.Name())
	}
	if strings.Join(left, ",") != "askpass,wi_x.git" {
		t.Fatalf("left after the sweep: %v", left)
	}
	if err := SweepCarrierTemps(filepath.Join(root, "absent")); err != nil {
		t.Fatalf("an absent root must be a no-op: %v", err)
	}
}

// repo review 2026-10-16 B-8: the claim lane's carrier fetch dies with
// its context (a stop or a shutdown during the claim), not after the
// 10-minute chain timeout.
func TestFetchTaskRefHonoursContext(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var conns []net.Conn
	var mu sync.Mutex
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		ln.Close()
		mu.Lock()
		for _, c := range conns {
			c.Close()
		}
		mu.Unlock()
	})
	dir := t.TempDir()
	if out, err := exec.Command("git", "init", "-q", dir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	o := Opts{WorkItemID: "wi_ctx", RemoteURL: "http://" + ln.Addr().String() + "/wi_ctx.git", Username: "wi_ctx", Secret: "s"}
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	t0 := time.Now()
	_, ferr := FetchTaskRef(ctx, o, dir, "app")
	if ferr == nil {
		t.Fatal("a cancelled fetch against a silent remote must fail")
	}
	if d := time.Since(t0); d > 15*time.Second {
		t.Fatalf("the cancelled fetch took %s", d)
	}
}

// IsCredentialRefused must match a REAL push the git face refused for
// its credential (an HTTP 401), and not a push that failed otherwise.
func TestIsCredentialRefusedOnRealGitOutput(t *testing.T) {
	setIdentity(t)
	o, _ := testOpts(t, "wi_cred")
	mkBare(t, o.RemoteURL, true)
	mkCapsule(t, o)
	writeDirty(t, o, "a.txt", "a\n")
	refused := o
	refused.RemoteURL, _ = attackerServer(t)
	_, _, _, err := CommitPush(context.Background(), refused, "refused")
	if err == nil || !IsCredentialRefused(err) {
		t.Fatalf("a 401 push: err=%v, want a credential refusal", err)
	}
	dead := o
	dead.RemoteURL = filepath.Join(t.TempDir(), "nowhere.git")
	if _, _, _, err := CommitPush(context.Background(), dead, "dead"); err == nil || IsCredentialRefused(err) {
		t.Fatalf("a push to a missing remote: err=%v, want a failure that is no credential refusal", err)
	}
}
