package gitops

// the capsule chain's blob-size guard, against the
// production aihub pre-receive hook at its shipped limit
// (capsule.MaxBlobBytes). The field failure: a step put a
// 30 MB trace under artifacts/, the observe commit took it in, and the
// hook refused every chain push after that. The manifest path and the
// event kind are spelled as literals on purpose: they are the contract
// the agents and the watch face read.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aihubgit "github.com/silgrid/polyforge/internal/aihub/git"
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// w391Hub lays a capsule whose remote is a bare repo of aihub's own
// git Manager at its default limits — the production pre-receive hook —
// and records the gitops events.
func w391Hub(t *testing.T, wi string) (Opts, *[]string, *[]map[string]any) {
	t.Helper()
	setIdentity(t)
	o, base := taskOpts(t, wi)
	mgr, err := aihubgit.NewManager(filepath.Join(base, "hubdata"), nil)
	if err != nil {
		t.Fatalf("aihub git manager: %v", err)
	}
	if mgr.MaxBlobBytes != capsule.MaxBlobBytes {
		t.Fatalf("the hub's chain limit is %d, want the shipped %d", mgr.MaxBlobBytes, capsule.MaxBlobBytes)
	}
	if err := mgr.EnsureBare(wi); err != nil {
		t.Fatalf("ensure bare: %v", err)
	}
	o.RemoteURL = mgr.RepoDir(wi)
	mkTaskCapsule(t, o)
	kinds, bodies := &[]string{}, &[]map[string]any{}
	o.EventSink = func(kind string, body map[string]any) {
		*kinds = append(*kinds, kind)
		*bodies = append(*bodies, body)
	}
	return o, kinds, bodies
}

func w391Write(t *testing.T, o Opts, rel string, data []byte) {
	t.Helper()
	p := filepath.Join(o.CapsuleDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// w391HubTree answers the hub main's paths.
func w391HubTree(t *testing.T, o Opts) map[string]bool {
	t.Helper()
	out := mustRun(t, "git", "--git-dir", o.RemoteURL, "ls-tree", "-r", "--name-only", "main")
	got := map[string]bool{}
	for _, p := range strings.Split(strings.TrimSpace(out), "\n") {
		got[p] = true
	}
	return got
}

type w391Manifest struct {
	Limit int64 `json:"limit"`
	Files []struct {
		Path   string `json:"path"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

// w391HubManifest reads the manifest as the hub holds it.
func w391HubManifest(t *testing.T, o Opts) w391Manifest {
	t.Helper()
	raw := mustRun(t, "git", "--git-dir", o.RemoteURL, "show", "main:artifacts/oversize.json")
	var m w391Manifest
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("the hub's artifacts/oversize.json does not decode: %v (%s)", err, raw)
	}
	return m
}

func w391Digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// AC1 (+AC4): a file over the limit written under artifacts/ stays out
// of the chain, the push lands, and the manifest and one event name it;
// a file exactly at the limit and every small file beside it are in the
// chain.
func TestW391OversizeFileStaysOutAndThePushLands(t *testing.T) {
	o, kinds, bodies := w391Hub(t, "wi_w391_gate")
	big := bytes.Repeat([]byte{7}, capsule.MaxBlobBytes+1)
	w391Write(t, o, "artifacts/ci-42412860259/trace.zip", big)
	w391Write(t, o, "artifacts/ci-42412860259/summary.txt", []byte("1 failed\n"))
	w391Write(t, o, "artifacts/at-limit.bin", bytes.Repeat([]byte{9}, capsule.MaxBlobBytes))

	if _, pushed, _, err := CommitPush(context.Background(), o, "observe: execution v1"); err != nil || !pushed {
		t.Fatalf("the chain push must land with a file over the limit in the capsule: pushed=%v err=%v", pushed, err)
	}
	tree := w391HubTree(t, o)
	if tree["artifacts/ci-42412860259/trace.zip"] {
		t.Fatal("the file over the limit reached the hub")
	}
	for _, want := range []string{"artifacts/ci-42412860259/summary.txt", "artifacts/at-limit.bin", "artifacts/oversize.json"} {
		if !tree[want] {
			t.Errorf("the chain is missing %s (hub tree %v)", want, tree)
		}
	}
	m := w391HubManifest(t, o)
	if m.Limit != capsule.MaxBlobBytes || len(m.Files) != 1 || m.Files[0].Path != "artifacts/ci-42412860259/trace.zip" ||
		m.Files[0].Size != int64(len(big)) || m.Files[0].SHA256 != w391Digest(big) {
		t.Fatalf("manifest = %+v, want the trace with its size and sha256", m)
	}
	if _, err := os.Stat(filepath.Join(o.CapsuleDir, "artifacts/ci-42412860259/trace.zip")); err != nil {
		t.Fatalf("the left-out file must stay in the capsule: %v", err)
	}
	if len(*kinds) != 1 || (*kinds)[0] != "chain_oversize_left_out" {
		t.Fatalf("events = %v, want one chain_oversize_left_out", *kinds)
	}
	if body := fmt.Sprint((*bodies)[0]); !strings.Contains(body, "artifacts/ci-42412860259/trace.zip") || !strings.Contains(body, w391Digest(big)) {
		t.Fatalf("the event must name the file and its sha256: %s", body)
	}

	// the next boundary: still out, still pushed, no second announcement
	w391Write(t, o, "artifacts/next.md", []byte("next\n"))
	if _, pushed, _, err := CommitPush(context.Background(), o, "observe: execution v2"); err != nil || !pushed {
		t.Fatalf("the next push must land too: pushed=%v err=%v", pushed, err)
	}
	if tree := w391HubTree(t, o); tree["artifacts/ci-42412860259/trace.zip"] || !tree["artifacts/next.md"] {
		t.Fatalf("hub tree after the next boundary: %v", tree)
	}
	if len(*kinds) != 1 {
		t.Fatalf("an unchanged left-out file is announced once, events = %v", *kinds)
	}
}

// AC1 (tracked) + AC4: a tracked file whose new version is over the limit
// leaves the chain (the commit records its removal) and returns once it
// is back under the limit.
func TestW391TrackedFileGrowsOutAndComesBack(t *testing.T) {
	o, kinds, _ := w391Hub(t, "wi_w391_tracked")
	w391Write(t, o, "artifacts/run.log", []byte("small\n"))
	if _, _, _, err := CommitPush(context.Background(), o, "v1"); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if !w391HubTree(t, o)["artifacts/run.log"] {
		t.Fatal("the small log must be in the chain")
	}
	big := bytes.Repeat([]byte{1}, capsule.MaxBlobBytes+10)
	w391Write(t, o, "artifacts/run.log", big)
	if _, pushed, _, err := CommitPush(context.Background(), o, "v2"); err != nil || !pushed {
		t.Fatalf("a tracked file grown over the limit must not fail the push: pushed=%v err=%v", pushed, err)
	}
	if w391HubTree(t, o)["artifacts/run.log"] {
		t.Fatal("the grown file must leave the chain")
	}
	if m := w391HubManifest(t, o); len(m.Files) != 1 || m.Files[0].Path != "artifacts/run.log" || m.Files[0].SHA256 != w391Digest(big) {
		t.Fatalf("manifest = %+v", m)
	}
	if len(*kinds) != 1 || (*kinds)[0] != "chain_oversize_left_out" {
		t.Fatalf("events = %v", *kinds)
	}

	w391Write(t, o, "artifacts/run.log", []byte("small again\n"))
	if _, pushed, _, err := CommitPush(context.Background(), o, "v3"); err != nil || !pushed {
		t.Fatalf("v3: pushed=%v err=%v", pushed, err)
	}
	if got := mustRun(t, "git", "--git-dir", o.RemoteURL, "show", "main:artifacts/run.log"); got != "small again\n" {
		t.Fatalf("the file back under the limit must return to the chain, hub has %q", got)
	}
	if m := w391HubManifest(t, o); len(m.Files) != 0 {
		t.Fatalf("the manifest must list nothing once the file is back, got %+v", m)
	}
	raw, _ := os.ReadFile(infoExcludePath(o))
	if strings.Contains(string(raw), "run.log") {
		t.Fatalf("the exclude block must release the file: %q", raw)
	}
}

// The guard's second half: a file that grows over the limit between the
// working-tree pass and `git add -A` (a download still being written) is
// caught on the staged tree, so the commit never holds it.
func TestW391FileGrowingDuringTheAddIsCaught(t *testing.T) {
	o, kinds, _ := w391Hub(t, "wi_w391_race")
	w391Write(t, o, "artifacts/download.part", []byte("partial\n"))
	big := bytes.Repeat([]byte{3}, capsule.MaxBlobBytes+1)
	grown := false
	gitArgsProbe = func(args []string) {
		if !grown && len(args) >= 2 && args[0] == "add" && args[1] == "-A" {
			grown = true
			w391Write(t, o, "artifacts/download.part", big)
		}
	}
	t.Cleanup(func() { gitArgsProbe = nil })
	if _, pushed, _, err := CommitPush(context.Background(), o, "v1"); err != nil || !pushed {
		t.Fatalf("pushed=%v err=%v", pushed, err)
	}
	if !grown {
		t.Fatal("the probe never grew the file")
	}
	if tree := w391HubTree(t, o); tree["artifacts/download.part"] || !tree["artifacts/oversize.json"] {
		t.Fatalf("hub tree %v: the grown file must stay out and the manifest go in", tree)
	}
	if m := w391HubManifest(t, o); len(m.Files) != 1 || m.Files[0].Path != "artifacts/download.part" {
		t.Fatalf("manifest = %+v", m)
	}
	if len(*kinds) != 1 {
		t.Fatalf("events = %v", *kinds)
	}
}

// AC2 at the commit: an existing capsule whose chain tracks a file under
// artifacts/downloads/ (committed under the old .gitignore) gets the new
// .gitignore — what claim and resume write — and from the next commit on
// nothing under the downloads directory is in the chain, whatever its
// size; the files stay on disk.
func TestW391DownloadsDirLeavesTheChain(t *testing.T) {
	o, kinds, _ := w391Hub(t, "wi_w391_downloads")
	w391Write(t, o, ".gitignore", []byte("/repos/\n/artifacts/release-*/\n"))
	w391Write(t, o, "artifacts/downloads/old.txt", []byte("an old download\n"))
	if _, _, _, err := CommitPush(context.Background(), o, "v1"); err != nil {
		t.Fatalf("v1: %v", err)
	}
	if !w391HubTree(t, o)["artifacts/downloads/old.txt"] {
		t.Fatal("setup: the old .gitignore lets the download in")
	}
	w391Write(t, o, ".gitignore", []byte(capsule.GitignoreContent))
	w391Write(t, o, "artifacts/downloads/new.txt", []byte("small\n"))
	w391Write(t, o, "artifacts/downloads/ci/trace.zip", bytes.Repeat([]byte{5}, capsule.MaxBlobBytes+1))
	w391Write(t, o, "artifacts/conclusion.md", []byte("the trace shows a timeout\n"))
	if _, pushed, _, err := CommitPush(context.Background(), o, "v2"); err != nil || !pushed {
		t.Fatalf("v2: pushed=%v err=%v", pushed, err)
	}
	tree := w391HubTree(t, o)
	for p := range tree {
		if strings.HasPrefix(p, "artifacts/downloads/") {
			t.Errorf("%s is in the chain", p)
		}
	}
	if !tree["artifacts/conclusion.md"] {
		t.Error("the conclusion beside the downloads must be in the chain")
	}
	if tree["artifacts/oversize.json"] || len(*kinds) != 0 {
		t.Errorf("a download is not an oversize file: manifest in tree=%v, events=%v", tree["artifacts/oversize.json"], *kinds)
	}
	for _, p := range []string{"artifacts/downloads/old.txt", "artifacts/downloads/new.txt", "artifacts/downloads/ci/trace.zip"} {
		if _, err := os.Stat(filepath.Join(o.CapsuleDir, p)); err != nil {
			t.Errorf("%s must stay on disk: %v", p, err)
		}
	}
}

// AC3: a size refusal of a blob already in the unpushed range (a step's
// own `git commit` in the capsule root bypasses the guard; a blob from
// before the guard is the same shape) answers an error that names the
// path, the commit that records it and the unpushed range.
func TestW391SizeRefusalNamesPathAndRange(t *testing.T) {
	o, _, _ := w391Hub(t, "wi_w391_refusal")
	w391Write(t, o, "artifacts/a.md", []byte("a\n"))
	if _, _, _, err := CommitPush(context.Background(), o, "v1"); err != nil {
		t.Fatalf("v1: %v", err)
	}
	pushed := strings.TrimSpace(mustRun(t, "git", "--git-dir", o.RemoteURL, "rev-parse", "main"))

	big := bytes.Repeat([]byte{8}, capsule.MaxBlobBytes+5)
	w391Write(t, o, "artifacts/ci/trace.zip", big)
	if _, err := git(o.Env(), o.CapsuleDir, "add", "artifacts/ci/trace.zip"); err != nil {
		t.Fatal(err)
	}
	if _, err := git(o.Env(), o.CapsuleDir, "commit", "-q", "-m", "a step's own commit"); err != nil {
		t.Fatal(err)
	}
	offender := strings.TrimSpace(mustRun(t, "git", "--git-dir", o.GitDir, "rev-parse", "HEAD"))
	blob := strings.TrimSpace(mustRun(t, "git", "--git-dir", o.GitDir, "rev-parse", "HEAD:artifacts/ci/trace.zip"))

	_, ok, _, err := CommitPush(context.Background(), o, "v2")
	if err == nil || ok {
		t.Fatalf("the hub must refuse a range holding a blob over the limit: pushed=%v err=%v", ok, err)
	}
	head := strings.TrimSpace(mustRun(t, "git", "--git-dir", o.GitDir, "rev-parse", "HEAD"))
	var ref *OversizeRefusal
	if !errors.As(err, &ref) {
		t.Fatalf("the size refusal is not resolved: %T %v", err, err)
	}
	wantRange := pushed + ".." + head
	if ref.Range != wantRange || ref.Limit != capsule.MaxBlobBytes || len(ref.Blobs) != 1 {
		t.Fatalf("refusal = %+v, want range %s and one blob", ref, wantRange)
	}
	if b := ref.Blobs[0]; b.Path != "artifacts/ci/trace.zip" || b.Commit != offender || b.Blob != blob || b.Size != int64(len(big)) {
		t.Fatalf("refused blob = %+v, want the trace committed in %s (blob %s)", b, offender, blob)
	}
	for _, want := range []string{"artifacts/ci/trace.zip", wantRange, offender, "blob exceeds"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error must name %q: %s", want, err)
		}
	}
	if !IsPermanentPushRejection(err) {
		t.Errorf("the resolved refusal must still read as permanent: %v", err)
	}
	facts := PushFailureFacts(err)
	if facts["range"] != wantRange || fmt.Sprint(facts["oversize"]) == "" || !strings.Contains(fmt.Sprint(facts["oversize"]), "artifacts/ci/trace.zip") {
		t.Fatalf("push failure facts = %v", facts)
	}
	if PushFailureFacts(errors.New("connection refused")) != nil {
		t.Fatal("another failure carries no size facts")
	}
}

// The guard names agent-chosen paths to git as literal paths, never as
// pathspecs: a `:`-prefixed name is pathspec magic and a `[x]` name a
// glob, so either one could otherwise drop a small sibling (`big`,
// `s1.png`) from the chain while the oversize file itself stays staged
// and refuses every later commit. Both shapes are covered for a tracked
// file grown over the limit (the working-tree pass) and for one that
// grows during `git add -A` (the staged-tree pass).
func TestW391PathspecMagicNamesStayLiteral(t *testing.T) {
	for _, c := range []struct{ name, big, sibling string }{
		{"colon", ":big", "big"},
		{"glob", "artifacts/s[1].png", "artifacts/s1.png"},
	} {
		for _, during := range []bool{false, true} {
			pass := "working-tree"
			if during {
				pass = "staged-tree"
			}
			t.Run(c.name+"/"+pass, func(t *testing.T) {
				o, _, _ := w391Hub(t, "wi_w391_literal")
				w391Write(t, o, c.big, []byte("small\n"))
				w391Write(t, o, c.sibling, []byte("small sibling\n"))
				if _, _, _, err := CommitPush(context.Background(), o, "v1"); err != nil {
					t.Fatalf("v1: %v", err)
				}
				huge := bytes.Repeat([]byte{6}, capsule.MaxBlobBytes+1)
				if during {
					w391Write(t, o, "artifacts/note.md", []byte("a change\n"))
					grown := false
					gitArgsProbe = func(args []string) {
						if !grown && len(args) >= 2 && args[0] == "add" && args[1] == "-A" {
							grown = true
							w391Write(t, o, c.big, huge)
						}
					}
					t.Cleanup(func() { gitArgsProbe = nil })
				} else {
					w391Write(t, o, c.big, huge)
				}
				for round := 2; round <= 3; round++ {
					w391Write(t, o, "artifacts/round.md", []byte(fmt.Sprintf("round %d\n", round)))
					if _, pushed, _, err := CommitPush(context.Background(), o, fmt.Sprintf("v%d", round)); err != nil || !pushed {
						t.Fatalf("v%d: the chain must keep advancing: pushed=%v err=%v", round, pushed, err)
					}
					// every pushed commit, not only the last: a later
					// `add -A` would re-add a sibling one commit dropped
					tree := w391HubTree(t, o)
					if tree[c.big] {
						t.Errorf("v%d: %s (over the limit) reached the hub", round, c.big)
					}
					if !tree[c.sibling] {
						t.Errorf("v%d: the small sibling %s left the chain (tree %v)", round, c.sibling, tree)
					}
				}
				if got := mustRun(t, "git", "--git-dir", o.RemoteURL, "show", "main:"+c.sibling); got != "small sibling\n" {
					t.Errorf("the small sibling %s must keep its content, hub has %q", c.sibling, got)
				}
				if m := w391HubManifest(t, o); len(m.Files) != 1 || m.Files[0].Path != c.big {
					t.Errorf("manifest = %+v, want only %s", m, c.big)
				}
			})
		}
	}
}
