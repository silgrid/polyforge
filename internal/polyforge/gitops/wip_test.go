package gitops

// wip_test.go: the task-repo WIP lane (polyforge#167) against real git —
// PushTaskWIP snapshots uncommitted work to refs/pf-wip/<name> from a
// daemon-owned temp repo without touching the agent's repo, deletes a
// stale WIP when the tree is clean (or the close is wrapped), and never
// runs anything the agent-controlled repo config names.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// wipEnv: a hub bare (no hook) and a capsule with repos/demo-src at one
// work commit. Answers the opts, the repo dir and the work head.
func wipEnv(t *testing.T, wi string) (Opts, string, string) {
	t.Helper()
	setIdentity(t)
	// never the operator's real global config (TestMain exposes it for
	// the identity): an empty throwaway one for the whole test
	gc := filepath.Join(t.TempDir(), "gitconfig")
	writeT(t, gc, "")
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	o, base := taskOpts(t, wi)
	mkBare(t, o.RemoteURL, false)
	mkTaskCapsule(t, o)
	head := mkTaskRepoWithWork(t, o.CapsuleDir, "demo-src", filepath.Join(base, "origin"))
	return o, filepath.Join(o.CapsuleDir, capsule.DirRepos, "demo-src"), head
}

func writeT(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// looseObjects counts the repo's loose objects (the WIP lane must write
// none of its blobs, trees or commit into the agent's store).
func looseObjects(t *testing.T, gitDir string) int {
	t.Helper()
	n := 0
	ents, err := os.ReadDir(filepath.Join(gitDir, "objects"))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		if len(e.Name()) != 2 {
			continue
		}
		sub, _ := os.ReadDir(filepath.Join(gitDir, "objects", e.Name()))
		n += len(sub)
	}
	return n
}

func TestPushTaskWIPSnapshotsUncommittedWork(t *testing.T) {
	o, repo, head := wipEnv(t, "wi_wip1")
	writeT(t, filepath.Join(repo, "work.txt"), "agent work\nmore\n")
	mustRun(t, "git", "-C", repo, "add", "work.txt") // staged in the agent's index
	writeT(t, filepath.Join(repo, "new.txt"), "untracked\n")
	writeT(t, filepath.Join(repo, ".gitignore"), "*.tmp\n")
	writeT(t, filepath.Join(repo, "scratch.tmp"), "ignored\n")
	writeT(t, filepath.Join(repo, ".git", "info", "exclude"), "local-only\n")
	writeT(t, filepath.Join(repo, "local-only"), "excluded\n")
	if err := os.Remove(filepath.Join(repo, "hello.txt")); err != nil {
		t.Fatal(err)
	}
	statusBefore := mustRun(t, "git", "-C", repo, "status", "--porcelain")
	indexBefore, err := os.ReadFile(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatal(err)
	}
	objsBefore := looseObjects(t, filepath.Join(repo, ".git"))

	PushTaskWIP(o, true, true)

	if got, ok := bareRefSHA(t, o.RemoteURL, TaskRefName("demo-src")); !ok || got != head {
		t.Fatalf("carrier = %q (%v), want %q (mirrored before the WIP)", got, ok, head)
	}
	wip, ok := bareRefSHA(t, o.RemoteURL, TaskWIPRefName("demo-src"))
	if !ok {
		t.Fatal("no refs/pf-wip/demo-src for a dirty task repo")
	}
	if parents := strings.Fields(mustRun(t, "git", "--git-dir", o.RemoteURL, "rev-list", "--parents", "-n", "1", wip)); len(parents) != 2 || parents[1] != head {
		t.Fatalf("WIP parents = %v, want exactly HEAD %s", parents, head)
	}
	files := strings.Fields(mustRun(t, "git", "--git-dir", o.RemoteURL, "ls-tree", "-r", "--name-only", wip))
	want := ".gitignore new.txt work.txt"
	if strings.Join(files, " ") != want {
		t.Fatalf("WIP tree = %v, want %s (untracked in, ignored/excluded/deleted out)", files, want)
	}
	if got := mustRun(t, "git", "--git-dir", o.RemoteURL, "show", wip+":work.txt"); got != "agent work\nmore\n" {
		t.Fatalf("WIP work.txt = %q", got)
	}
	// the agent's repo is untouched: HEAD, index, status, object store
	if got := strings.TrimSpace(mustRun(t, "git", "-C", repo, "rev-parse", "HEAD")); got != head {
		t.Fatalf("HEAD moved to %s", got)
	}
	if indexAfter, _ := os.ReadFile(filepath.Join(repo, ".git", "index")); string(indexAfter) != string(indexBefore) {
		t.Fatal("the agent's index was rewritten")
	}
	if got := mustRun(t, "git", "-C", repo, "status", "--porcelain"); got != statusBefore {
		t.Fatalf("status changed:\n%s\nwas\n%s", got, statusBefore)
	}
	if got := looseObjects(t, filepath.Join(repo, ".git")); got != objsBefore {
		t.Fatalf("the agent's object store grew %d -> %d", objsBefore, got)
	}
}

// No uncommitted changes → no WIP ref; a later clean close (or a wrapped
// close over a dirty tree) deletes a stale one.
func TestPushTaskWIPCleanTreeAndStaleRef(t *testing.T) {
	o, repo, _ := wipEnv(t, "wi_wip2")
	PushTaskWIP(o, true, true)
	if wip, ok := bareRefSHA(t, o.RemoteURL, TaskWIPRefName("demo-src")); ok {
		t.Fatalf("a clean task repo produced a WIP ref %s", wip)
	}
	writeT(t, filepath.Join(repo, "new.txt"), "x\n")
	PushTaskWIP(o, true, true)
	if _, ok := bareRefSHA(t, o.RemoteURL, TaskWIPRefName("demo-src")); !ok {
		t.Fatal("setup: no WIP ref for a dirty repo")
	}
	if err := os.Remove(filepath.Join(repo, "new.txt")); err != nil {
		t.Fatal(err)
	}
	PushTaskWIP(o, true, true)
	if wip, ok := bareRefSHA(t, o.RemoteURL, TaskWIPRefName("demo-src")); ok {
		t.Fatalf("a clean close left the stale WIP ref %s", wip)
	}
	writeT(t, filepath.Join(repo, "new.txt"), "y\n")
	PushTaskWIP(o, true, true)
	PushTaskWIP(o, false, true) // wrapped
	if wip, ok := bareRefSHA(t, o.RemoteURL, TaskWIPRefName("demo-src")); ok {
		t.Fatalf("a wrapped close left the WIP ref %s", wip)
	}
}

// The WIP is built and pushed from a pf-carrier-* temp repo outside the
// capsule with the carrier-lane flags (argv asserted like the carrier's
// own TestCarrierPushRunsFromATempRepo), and a clean filter / fsmonitor
// planted in the agent's repo config never runs.
func TestPushTaskWIPRunsFromATempRepo(t *testing.T) {
	o, repo, _ := wipEnv(t, "wi_wip3")
	marker := filepath.Join(t.TempDir(), "ran")
	mustRun(t, "git", "-C", repo, "config", "filter.evil.clean", "touch "+marker+"; cat")
	mustRun(t, "git", "-C", repo, "config", "core.fsmonitor", "touch "+marker+"; true")
	writeT(t, filepath.Join(repo, ".gitattributes"), "* filter=evil\n")
	writeT(t, filepath.Join(repo, "new.txt"), "x\n")
	var push, add []string
	gitArgsProbe = func(args []string) {
		for _, a := range args {
			if strings.HasSuffix(a, ":"+TaskWIPRefName("demo-src")) {
				push = append([]string{}, args...)
			}
			if a == "add" {
				add = append([]string{}, args...)
			}
		}
	}
	t.Cleanup(func() { gitArgsProbe = nil })
	PushTaskWIP(o, true, true)
	wip, ok := bareRefSHA(t, o.RemoteURL, TaskWIPRefName("demo-src"))
	if !ok {
		t.Fatal("no WIP ref")
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a program named by the agent's repo config ran during the snapshot")
	}
	if len(push) == 0 || !strings.HasPrefix(push[0], "--git-dir=") {
		t.Fatalf("WIP push argv = %q, want a --git-dir temp repo", push)
	}
	tmp := strings.TrimPrefix(push[0], "--git-dir=")
	if !strings.HasPrefix(filepath.Base(tmp), "pf-carrier-") || strings.HasPrefix(tmp, o.CapsuleDir) {
		t.Fatalf("temp repo %q must be a pf-carrier- dir outside the capsule", tmp)
	}
	if _, err := os.Stat(tmp); !os.IsNotExist(err) {
		t.Fatalf("the temp repo %q must be removed after the push", tmp)
	}
	wantPush := []string{
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "credential.helper=", "-c", "http.proxy=",
		"-c", "protocol.allow=never", "-c", "protocol.file.allow=always",
		"push", "--no-recurse-submodules", o.RemoteURL, "+" + wip + ":" + TaskWIPRefName("demo-src"),
	}
	if strings.Join(push[1:], " ") != strings.Join(wantPush, " ") {
		t.Fatalf("WIP push argv = %q\nwant --git-dir=<tmp> %q", push, wantPush)
	}
	wantAdd := []string{"--git-dir=" + tmp,
		"-c", "core.hooksPath=/dev/null", "-c", "core.fsmonitor=false", "-c", "credential.helper=", "-c", "http.proxy=",
		"--work-tree=" + repo, "add", "-A", "--ignore-errors"}
	if strings.Join(add, " ") != strings.Join(wantAdd, " ") {
		t.Fatalf("WIP add argv = %q\nwant %q", add, wantAdd)
	}
}

// wipTree answers the WIP ref's file list (and fails when there is none).
func wipTree(t *testing.T, o Opts) (string, []string) {
	t.Helper()
	wip, ok := bareRefSHA(t, o.RemoteURL, TaskWIPRefName("demo-src"))
	if !ok {
		t.Fatal("no refs/pf-wip/demo-src")
	}
	return wip, strings.Fields(mustRun(t, "git", "--git-dir", o.RemoteURL, "ls-tree", "-r", "--name-only", wip))
}

// captureEvents binds o's event sink to a recorder.
func captureEvents(o *Opts) func() []map[string]any {
	var mu sync.Mutex
	var evs []map[string]any
	o.EventSink = func(kind string, body map[string]any) {
		mu.Lock()
		defer mu.Unlock()
		b := map[string]any{"kind": kind}
		for k, v := range body {
			b[k] = v
		}
		evs = append(evs, b)
	}
	return func() []map[string]any { mu.Lock(); defer mu.Unlock(); return append([]map[string]any{}, evs...) }
}

func skippedText(evs []map[string]any) string {
	var b strings.Builder
	for _, e := range evs {
		if e["kind"] == EventTaskWIPSkipped {
			b.WriteString(e["skipped"].(string) + "\n")
		}
	}
	return b.String()
}

// S1: an embedded repository — even one without a commit, which makes a
// plain `add -A` fail outright — is left out and reported; the rest of
// the uncommitted work is still saved.
func TestPushTaskWIPSkipsEmbeddedRepos(t *testing.T) {
	o, repo, _ := wipEnv(t, "wi_wip4")
	events := captureEvents(&o)
	writeT(t, filepath.Join(repo, "new.txt"), "keep me\n")
	mustRun(t, "git", "init", "-q", filepath.Join(repo, "vendor", "empty"))
	writeT(t, filepath.Join(repo, "vendor", "empty", "f.txt"), "x\n")
	mustRun(t, "git", "init", "-q", filepath.Join(repo, "with space"))
	writeT(t, filepath.Join(repo, "with space", "g.txt"), "y\n")
	mustRun(t, "git", "-C", filepath.Join(repo, "with space"), "add", "-A")
	mustRun(t, "git", "-C", filepath.Join(repo, "with space"), "commit", "-q", "-m", "nested")
	PushTaskWIP(o, true, true)
	_, files := wipTree(t, o)
	if strings.Join(files, " ") != "hello.txt new.txt work.txt" {
		t.Fatalf("WIP tree = %v, want the embedded repos left out", files)
	}
	sk := skippedText(events())
	if !strings.Contains(sk, "vendor/empty (embedded git repository)") || !strings.Contains(sk, "with space (embedded git repository)") {
		t.Fatalf("task_wip_skipped = %q, want both embedded repos named", sk)
	}
}

// S2: a blob over the carrier limit is left out (a tracked one keeps its
// HEAD content — never turned into a deletion); above the total cap the
// largest changed files go first.
func TestPushTaskWIPBoundsBlobsAndTotal(t *testing.T) {
	o, repo, _ := wipEnv(t, "wi_wip5")
	events := captureEvents(&o)
	oldBlob, oldTotal := wipBlobCap, wipTotalCap
	t.Cleanup(func() { wipBlobCap, wipTotalCap = oldBlob, oldTotal })
	wipBlobCap, wipTotalCap = 100, 50
	writeT(t, filepath.Join(repo, "hello.txt"), strings.Repeat("h", 200)) // tracked, over the blob cap
	writeT(t, filepath.Join(repo, "huge.bin"), strings.Repeat("u", 150))  // untracked, over the blob cap
	writeT(t, filepath.Join(repo, "a.txt"), strings.Repeat("a", 30))
	writeT(t, filepath.Join(repo, "b.txt"), strings.Repeat("b", 20))
	writeT(t, filepath.Join(repo, "c.txt"), strings.Repeat("c", 10))
	PushTaskWIP(o, true, true)
	wip, files := wipTree(t, o)
	if strings.Join(files, " ") != "b.txt c.txt hello.txt work.txt" {
		t.Fatalf("WIP tree = %v, want huge.bin and the largest (a.txt) left out, hello.txt kept", files)
	}
	if got := mustRun(t, "git", "--git-dir", o.RemoteURL, "show", wip+":hello.txt"); got != "hello\n" {
		t.Fatalf("an over-limit tracked file must keep its HEAD content in the WIP, got %q", got)
	}
	sk := skippedText(events())
	for _, p := range []string{"hello.txt (", "huge.bin (", "a.txt ("} {
		if !strings.Contains(sk, p) {
			t.Fatalf("task_wip_skipped = %q, want %s", sk, p)
		}
	}
	if msg := mustRun(t, "git", "--git-dir", o.RemoteURL, "log", "-1", "--format=%B", wip); !strings.Contains(msg, "huge.bin") {
		t.Fatalf("the WIP commit message does not record the left-out paths: %q", msg)
	}
}

// N1: files whose attributes name the lfs or git-crypt filter are left
// out (the snapshot applies no filter: raw content is the smudged form).
func TestPushTaskWIPSkipsFilteredFiles(t *testing.T) {
	o, repo, _ := wipEnv(t, "wi_wip6")
	events := captureEvents(&o)
	writeT(t, filepath.Join(repo, ".gitattributes"), "*.psd filter=lfs diff=lfs merge=lfs -text\nsecret/** filter=git-crypt diff=git-crypt\n")
	writeT(t, filepath.Join(repo, "art.psd"), "big binary\n")
	if err := os.MkdirAll(filepath.Join(repo, "secret"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeT(t, filepath.Join(repo, "secret", "key"), "plaintext\n")
	PushTaskWIP(o, true, true)
	_, files := wipTree(t, o)
	if strings.Join(files, " ") != ".gitattributes hello.txt work.txt" {
		t.Fatalf("WIP tree = %v, want the lfs/git-crypt files left out", files)
	}
	sk := skippedText(events())
	if !strings.Contains(sk, "art.psd (filter=lfs") || !strings.Contains(sk, "secret/key (filter=git-crypt") {
		t.Fatalf("task_wip_skipped = %q", sk)
	}
}

// S4: the ignore rules the carrier env would drop still apply — the
// operator's global ignore file (XDG default and core.excludesFile) and
// the repo's own core.excludesFile.
func TestPushTaskWIPHonoursGlobalAndRepoExcludes(t *testing.T) {
	o, repo, _ := wipEnv(t, "wi_wip7")
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	if err := os.MkdirAll(filepath.Join(xdg, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeT(t, filepath.Join(xdg, "git", "ignore"), "*.secret\n")
	writeT(t, filepath.Join(repo, "x.secret"), "global-ignored\n")
	writeT(t, filepath.Join(repo, "new.txt"), "keep\n")
	PushTaskWIP(o, true, true)
	if _, files := wipTree(t, o); strings.Join(files, " ") != "hello.txt new.txt work.txt" {
		t.Fatalf("WIP tree = %v, want the XDG-ignored file left out", files)
	}
	// the operator's global core.excludesFile (a throwaway global config:
	// never the real one TestMain exposes for the identity)
	gc := filepath.Join(t.TempDir(), "gitconfig")
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	global := filepath.Join(t.TempDir(), "global-ignore")
	writeT(t, global, "*.op\n")
	mustRun(t, "git", "config", "--file", gc, "core.excludesFile", global)
	writeT(t, filepath.Join(repo, "y.op"), "op-ignored\n")
	PushTaskWIP(o, true, true)
	if _, files := wipTree(t, o); strings.Join(files, " ") != "hello.txt new.txt work.txt" {
		t.Fatalf("WIP tree = %v, want the globally ignored file left out", files)
	}
	// the repo's own core.excludesFile wins over the global one
	repoIgnore := filepath.Join(t.TempDir(), "repo-ignore")
	writeT(t, repoIgnore, "*.local\n*.op\n")
	mustRun(t, "git", "-C", repo, "config", "core.excludesFile", repoIgnore)
	writeT(t, filepath.Join(repo, "z.local"), "repo-ignored\n")
	PushTaskWIP(o, true, true)
	if _, files := wipTree(t, o); strings.Join(files, " ") != "hello.txt new.txt work.txt" {
		t.Fatalf("WIP tree = %v, want the repo-excludesFile ignored file left out", files)
	}
}

// S-C: when the keep snapshot cannot be pushed, the hub's older WIP ref
// is left alone (it is only applied on its own parent — the state it was
// taken on — so it beats nothing) and carrier_push_failed names the WIP
// ref and says the hub's WIP predates this close.
func TestPushTaskWIPFailureKeepsOlderRef(t *testing.T) {
	o, repo, _ := wipEnv(t, "wi_wip8")
	events := captureEvents(&o)
	writeT(t, filepath.Join(repo, "new.txt"), "v1\n")
	PushTaskWIP(o, true, true)
	first, ok := bareRefSHA(t, o.RemoteURL, TaskWIPRefName("demo-src"))
	if !ok {
		t.Fatal("setup: no first WIP")
	}
	// the hub now refuses WIP updates (deletion still allowed)
	hook := "#!/bin/sh\nzero=0000000000000000000000000000000000000000\nwhile read old new ref; do\n  case \"$ref\" in refs/pf-wip/*) [ \"$new\" = \"$zero\" ] || { echo refused >&2; exit 1; } ;; esac\ndone\nexit 0\n"
	if err := os.WriteFile(filepath.Join(o.RemoteURL, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
	writeT(t, filepath.Join(repo, "new.txt"), "v2\n")
	PushTaskWIP(o, true, true)
	if wip, ok := bareRefSHA(t, o.RemoteURL, TaskWIPRefName("demo-src")); !ok || wip != first {
		t.Fatalf("a failed WIP push must leave the older snapshot %s alone, got %q (%v)", first, wip, ok)
	}
	var failed bool
	for _, e := range events() {
		if e["kind"] == EventCarrierPushFailed && e["ref"] == TaskWIPRefName("demo-src") &&
			strings.Contains(e["error"].(string), "predates this close") {
			failed = true
		}
	}
	if !failed {
		t.Fatalf("no carrier_push_failed for the WIP ref: %v", events())
	}
}

// N2: an expired Deadline cuts every lane call: nothing is pushed.
func TestPushTaskWIPHonoursDeadline(t *testing.T) {
	o, repo, _ := wipEnv(t, "wi_wip9")
	events := captureEvents(&o)
	writeT(t, filepath.Join(repo, "new.txt"), "x\n")
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	o.Ctx = ctx
	start := time.Now()
	PushTaskWIP(o, true, true)
	if _, ok := bareRefSHA(t, o.RemoteURL, TaskWIPRefName("demo-src")); ok {
		t.Fatal("a WIP was pushed past the deadline")
	}
	if el := time.Since(start); el > 30*time.Second {
		t.Fatalf("the lane ran %s past an expired deadline", el)
	}
	// the alerts say why: the deadline, not a misleading layout refusal
	var n int
	for _, e := range events() {
		if e["kind"] != EventCarrierPushFailed {
			continue
		}
		n++
		if !strings.Contains(e["error"].(string), "deadline exceeded") {
			t.Fatalf("carrier_push_failed after the deadline = %v, want it to name the deadline", e)
		}
	}
	if n == 0 {
		t.Fatal("no carrier_push_failed for the lost snapshot")
	}
}

// S-A: a file git cannot read (root-owned build output, mode 000) is left
// out and reported; the rest of the work is still saved. Root reads any
// file regardless of mode, so the test needs a non-root euid.
func TestPushTaskWIPSkipsUnreadableFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root reads mode-000 files: run as a non-root user")
	}
	o, repo, _ := wipEnv(t, "wi_wip10")
	events := captureEvents(&o)
	writeT(t, filepath.Join(repo, "new.txt"), "keep\n")
	writeT(t, filepath.Join(repo, "locked.out"), "docker output\n")
	if err := os.Chmod(filepath.Join(repo, "locked.out"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(filepath.Join(repo, "locked.out"), 0o644) })
	PushTaskWIP(o, true, true)
	if _, files := wipTree(t, o); strings.Join(files, " ") != "hello.txt new.txt work.txt" {
		t.Fatalf("WIP tree = %v, want the unreadable file left out and the rest saved", files)
	}
	if sk := skippedText(events()); !strings.Contains(sk, "locked.out (unreadable") {
		t.Fatalf("task_wip_skipped = %q, want locked.out named", sk)
	}
}

// N: the repo's .git/info/attributes counts for the lfs/git-crypt rule,
// and an embedded repository inside an ignored directory is neither
// walked nor reported.
func TestPushTaskWIPInfoAttributesAndIgnoredEmbedded(t *testing.T) {
	o, repo, _ := wipEnv(t, "wi_wip11")
	events := captureEvents(&o)
	writeT(t, filepath.Join(repo, ".git", "info", "attributes"), "*.psd filter=lfs\n")
	writeT(t, filepath.Join(repo, "art.psd"), "smudged\n")
	writeT(t, filepath.Join(repo, ".gitignore"), "build/\n")
	mustRun(t, "git", "init", "-q", filepath.Join(repo, "build", "dep"))
	writeT(t, filepath.Join(repo, "new.txt"), "keep\n")
	PushTaskWIP(o, true, true)
	if _, files := wipTree(t, o); strings.Join(files, " ") != ".gitignore hello.txt new.txt work.txt" {
		t.Fatalf("WIP tree = %v", files)
	}
	sk := skippedText(events())
	if !strings.Contains(sk, "art.psd (filter=lfs") {
		t.Fatalf("task_wip_skipped = %q, want art.psd (info/attributes lfs)", sk)
	}
	if strings.Contains(sk, "build") {
		t.Fatalf("an embedded repo under an ignored dir was reported: %q", sk)
	}
}

// A cancelled WIP lane (the driver or job context ending mid-push) kills
// its hung git call's process group and releases the per-WI lock at once,
// so the work item's next close does not wait out the lane's deadline; it
// raises no carrier_push_failed and keeps the once-per-ref alert.
func TestPushTaskWIPStopsPromptlyOnCancel(t *testing.T) {
	o, repo, _ := wipEnv(t, "wi_wip_cancel")
	events := captureEvents(&o)
	o.CarrierAlerts = &CarrierAlerts{}
	writeT(t, filepath.Join(repo, "new.txt"), "x\n")
	marker := filepath.Join(t.TempDir(), "push-entered")
	if err := os.MkdirAll(filepath.Join(o.RemoteURL, "hooks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(o.RemoteURL, "hooks", "pre-receive"), []byte("#!/bin/sh\ntouch '"+marker+"'\nsleep 120\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	o.Ctx = ctx
	done := make(chan struct{})
	go func() { PushTaskWIP(o, true, true); close(done) }()
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the lane's push never reached the hub")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	start := time.Now()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("the cancelled lane did not return")
	}
	locked := make(chan func())
	go func() { locked <- Lock(o.WorkItemID) }()
	select {
	case release := <-locked:
		release()
	case <-time.After(5 * time.Second):
		t.Fatal("the per-WI lock is still held after the cancelled lane returned")
	}
	if el := time.Since(start); el > 2*time.Second {
		t.Fatalf("the cancelled lane released the lock after %s, want about at once", el)
	}
	// a cancellation is not a failure: no carrier_push_failed, and the
	// once-per-ref alert stays for a real failure
	for _, e := range events() {
		if e["kind"] == EventCarrierPushFailed {
			t.Fatalf("a cancelled lane raised %v", e)
		}
	}
	for _, ref := range []string{TaskRefName("demo-src"), TaskWIPRefName("demo-src")} {
		if !o.CarrierAlerts.first(ref) {
			t.Fatalf("the cancelled lane consumed the alert for %s", ref)
		}
	}
}

// B4: the task repo's config is read without includes — an include.path
// naming another file that sets core.excludesFile is not followed, while
// a key set in the repo config itself still wins. A pin, not a repro:
// `git config --file` already defaults to --no-includes, so this holds
// on the code the audit read too.
func TestEffectiveInfoFileIgnoresRepoIncludes(t *testing.T) {
	o, repo, _ := wipEnv(t, "wi_wip_inc")
	xdg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", xdg)
	gc := filepath.Join(t.TempDir(), "gitconfig")
	writeT(t, gc, "")
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	included := filepath.Join(t.TempDir(), "included.cfg")
	writeT(t, included, "[core]\n\texcludesFile = /from/the/included/file\n")
	mustRun(t, "git", "-C", repo, "config", "include.path", included)
	if got := effectiveInfoFile(o, repo, t.TempDir(), "core.excludesFile", "ignore"); got != filepath.Join(xdg, "git", "ignore") {
		t.Fatalf("effective excludes file = %q, want the XDG default (the include must not be followed)", got)
	}
	mustRun(t, "git", "-C", repo, "config", "core.excludesFile", "/set/directly")
	if got := effectiveInfoFile(o, repo, t.TempDir(), "core.excludesFile", "ignore"); got != "/set/directly" {
		t.Fatalf("effective excludes file = %q, want the repo config's own value", got)
	}
}
