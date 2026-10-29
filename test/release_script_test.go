package test

// Fake-env tests for .polyforge/release.sh: a local bare repo is origin, and
// git/docker/gh are stubs first on PATH that append to a call log. No network,
// no docker, no gh, no real remote is ever touched.

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// The CROSS-SCRIPT CONTRACT fixture, shared with deploy_image_test.go: the
// canonical aihub-image.digest content is the bare "sha256:<64 hex>" that
// release.sh writes and deploy.sh consumes (its repo digest is
// "ghcr.io/silgrid/aihub@" + that).
const relFakeDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// imageRepoDigest is the RepoDigests entry the registry reports for a digest.
func imageRepoDigest(digest string) string { return "ghcr.io/silgrid/aihub@" + digest }

type relWorld struct {
	t       *testing.T
	root    string
	origin  string
	work    string
	bin     string
	log     string
	fakeRel string
	script  string
}

func relWrite(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func relGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newRelWorld builds origin (bare) + a clone on main at origin/main, with the
// given tags pushed to origin and localOnly tags kept only in the clone.
func newRelWorld(t *testing.T, originTags, localTags []string) *relWorld {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not installed")
	}
	script, err := filepath.Abs(filepath.Join("..", ".polyforge", "release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	w := &relWorld{t: t, root: root, origin: filepath.Join(root, "origin.git"), work: filepath.Join(root, "work"),
		bin: filepath.Join(root, "bin"), log: filepath.Join(root, "calls.log"), fakeRel: filepath.Join(root, "fakegh"), script: script}
	relGit(t, root, "init", "-q", "--bare", "-b", "main", w.origin)
	relGit(t, root, "clone", "-q", w.origin, w.work)
	relGit(t, w.work, "checkout", "-q", "-b", "main")
	relWrite(t, filepath.Join(w.work, "deploy", "Dockerfile"), "FROM scratch\n", 0o644)
	relWrite(t, filepath.Join(w.work, "README"), "x\n", 0o644)
	relGit(t, w.work, "add", "-A")
	relGit(t, w.work, "commit", "-q", "-m", "first")
	for _, tg := range originTags {
		relGit(t, w.work, "tag", "-a", "-m", tg, tg)
	}
	relGit(t, w.work, "push", "-q", "origin", "main")
	if len(originTags) > 0 {
		relGit(t, w.work, "push", "-q", "origin", "--tags")
	}
	// a second change, landed through a (fake) merged PR, so a log range exists
	w.landPR("second", "second change")
	for _, tg := range localTags {
		relGit(t, w.work, "tag", "-a", "-m", tg, tg)
	}
	relWrite(t, w.log, "", 0o644)

	relWrite(t, filepath.Join(w.bin, "git"), fmt.Sprintf(`#!/bin/sh
case "$1" in push) echo "git $*" >> "$CALLLOG" ;; esac
if [ "$1" = ls-remote ] && [ -n "$FAKE_LSREMOTE_FAIL" ]; then echo "fatal: unable to access" >&2; exit 128; fi
exec %s "$@"
`, realGit), 0o755)
	relWrite(t, filepath.Join(w.bin, "docker"), `#!/bin/sh
case "$1" in
  build) echo "docker $*" >> "$CALLLOG" ;;
  push) echo "docker $*" >> "$CALLLOG"; [ -n "$FAKE_PUSH_FAIL" ] && exit 1 ;;
  inspect) echo "docker inspect" >> "$CALLLOG"; echo "ghcr.io/silgrid/aihub@$FAKE_PUSH_DIGEST" ;;
  login) echo "docker login" >> "$CALLLOG"; cat >/dev/null ;;
  buildx) [ -n "$FAKE_NO_BUILDX" ] && { echo "docker: 'buildx' is not a docker command." >&2; exit 1; }
    echo "Name: x"; echo "Digest: $FAKE_REGISTRY_DIGEST" ;;
  # config/layer digests come BEFORE the Descriptor here, so only the
  # Descriptor-scoped read can find the manifest digest
  manifest) printf '{\n\t"Ref": "x",\n\t"SchemaV2Manifest": {\n\t\t"config": {\n\t\t\t"digest": "sha256:cccc"\n\t\t},\n\t\t"layers": [\n\t\t\t{\n\t\t\t\t"Digest": "sha256:dddd"\n\t\t\t}\n\t\t]\n\t},\n\t"Descriptor": {\n\t\t"mediaType": "m",\n\t\t"digest": "%s",\n\t\t"size": 952\n\t}\n}\n' "$FAKE_REGISTRY_DIGEST" ;;
esac
exit 0
`, 0o755)
	relWrite(t, filepath.Join(w.bin, "gh"), `#!/bin/sh
cmd="$1 $2"
case "$cmd" in
  "release create")
    echo "gh release create $3" >> "$CALLLOG"
    tag="$3"; shift 3
    mkdir -p "$FAKE_GH/$tag"
    while [ $# -gt 0 ]; do
      case "$1" in
        --notes-file) cp "$2" "$FAKE_GH/$tag/.body"; shift 2 ;;
        --title|-R) shift 2 ;;
        --verify-tag) shift ;;
        *) cp "$1" "$FAKE_GH/$tag/"; shift ;;
      esac
    done
    echo "https://github.com/silgrid/polyforge/releases/tag/$tag" ;;
  "release view")
    d="$FAKE_GH/$3"; [ -d "$d" ] || { echo "release not found" >&2; exit 1; }
    case "$*" in *.body*) cat "$d/.body" ;; *) ls "$d" ;; esac ;;
  "release download")
    d="$FAKE_GH/$3"; shift 3
    out=.; while [ $# -gt 0 ]; do case "$1" in -D) out="$2"; shift 2 ;; *) shift ;; esac; done
    for f in "$d"/*; do cp "$f" "$out/"; done ;;
  "api repos/"*)
    # commit -> merged-PR association: a sha listed in $FAKE_GH/pr-merges is
    # the merge commit of a merged PR (the --jq filter's answer is its number).
    [ -n "$FAKE_GH_API_FAIL" ] && { echo "gh: HTTP 502" >&2; exit 1; }
    sha=$(printf '%s\n' "$2" | sed -n 's|.*/commits/\([0-9a-f]*\)/pulls$|\1|p')
    if [ -n "$sha" ] && grep -qx "$sha" "$FAKE_GH/pr-merges" 2>/dev/null; then echo 7; fi ;;
  "auth token") echo "gh auth token" >> "$CALLLOG"; echo tok ;;
  "api user") echo me ;;
esac
exit 0
`, 0o755)
	return w
}

// commitOnMain adds one file in a direct (non-PR) commit on main and pushes it.
func (w *relWorld) commitOnMain(file, msg string) string {
	w.t.Helper()
	relWrite(w.t, filepath.Join(w.work, file), msg+"\n", 0o644)
	relGit(w.t, w.work, "add", "-A")
	relGit(w.t, w.work, "commit", "-q", "-m", msg)
	relGit(w.t, w.work, "push", "-q", "origin", "main")
	return relGit(w.t, w.work, "rev-parse", "HEAD")
}

// mergeOnMain lands one file the way `gh pr merge --merge` does — a topic
// branch commit merged with --no-ff onto main — and pushes main. recordPR
// registers the merge commit as a merged PR's merge commit in the fake gh.
func (w *relWorld) mergeOnMain(file, content, msg string, recordPR bool) string {
	w.t.Helper()
	topic := "topic-" + filepath.Base(file)
	relGit(w.t, w.work, "checkout", "-q", "-b", topic)
	relWrite(w.t, filepath.Join(w.work, file), content, 0o644)
	relGit(w.t, w.work, "add", "-A")
	relGit(w.t, w.work, "commit", "-q", "-m", msg)
	relGit(w.t, w.work, "checkout", "-q", "main")
	relGit(w.t, w.work, "merge", "-q", "--no-ff", "-m", "Merge pull request from "+topic, topic)
	relGit(w.t, w.work, "branch", "-q", "-D", topic)
	relGit(w.t, w.work, "push", "-q", "origin", "main")
	sha := relGit(w.t, w.work, "rev-parse", "HEAD")
	if recordPR {
		w.recordPR(sha)
	}
	return sha
}

// landPR is mergeOnMain with the merged-PR record: the normal way a change
// reaches main.
func (w *relWorld) landPR(file, msg string) string { return w.mergeOnMain(file, msg+"\n", msg, true) }

// recordPR registers sha as the merge commit of a merged PR in the fake gh.
func (w *relWorld) recordPR(sha string) {
	w.t.Helper()
	p := filepath.Join(w.fakeRel, "pr-merges")
	if err := os.MkdirAll(w.fakeRel, 0o755); err != nil {
		w.t.Fatal(err)
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		w.t.Fatal(err)
	}
	defer f.Close()
	fmt.Fprintln(f, sha)
}

func (w *relWorld) env(extra ...string) []string {
	dockerCfg := filepath.Join(w.root, "dockercfg")
	if _, err := os.Stat(dockerCfg); err != nil {
		relWrite(w.t, filepath.Join(dockerCfg, "config.json"), `{"auths":{"ghcr.io":{}}}`, 0o644)
	}
	env := append(os.Environ(),
		"PATH="+w.bin+string(os.PathListSeparator)+os.Getenv("PATH"),
		"CALLLOG="+w.log, "FAKE_GH="+w.fakeRel, "FAKE_PUSH_DIGEST="+relFakeDigest, "FAKE_REGISTRY_DIGEST="+relFakeDigest,
		"DOCKER_CONFIG="+dockerCfg, "HOME="+w.root,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
		"RELEASE_XREFS_CMD=echo xrefs >> \"$CALLLOG\"",
		"RELEASE_TEST_CMD=echo tests >> \"$CALLLOG\"",
		"RELEASE_AUDIT_CMD=echo audit >> \"$CALLLOG\"",
		"RELEASE_GH_REPO=silgrid/polyforge",
		"RELEASE_BUILD_CMD=echo build >> \"$CALLLOG\"; mkdir -p artifacts/release-$TAG; echo aihub-bin > artifacts/release-$TAG/aihub; echo pf-bin > artifacts/release-$TAG/pf; "+
			"echo aihub-arm64-bin > artifacts/release-$TAG/aihub-darwin-arm64; echo pf-arm64-bin > artifacts/release-$TAG/pf-darwin-arm64; "+
			"echo aihub-amd64-bin > artifacts/release-$TAG/aihub-darwin-amd64; echo pf-amd64-bin > artifacts/release-$TAG/pf-darwin-amd64",
	)
	return append(env, extra...)
}

func (w *relWorld) run(extra []string, args ...string) (string, error) {
	c := exec.Command("sh", append([]string{w.script}, args...)...)
	c.Dir = w.work
	c.Env = w.env(extra...)
	out, err := c.CombinedOutput()
	return string(out), err
}

func (w *relWorld) calls() []string {
	b, _ := os.ReadFile(w.log)
	var out []string
	for _, l := range strings.Split(string(b), "\n") {
		if l != "" {
			out = append(out, l)
		}
	}
	return out
}

func (w *relWorld) originTags() string {
	return relGit(w.t, w.origin, "tag", "-l")
}

func relIndex(calls []string, prefix string) int {
	for i, c := range calls {
		if strings.HasPrefix(c, prefix) {
			return i
		}
	}
	return -1
}

func TestReleaseScriptHappyPathOrderAndAssets(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	out, err := w.run(nil)
	if err != nil {
		t.Fatalf("release failed: %v\n%s", err, out)
	}
	calls := w.calls()
	order := []string{"xrefs", "tests", "audit", "build", "docker build", "git push", "docker push", "docker inspect", "gh release create"}
	last := -1
	for _, p := range order {
		i := relIndex(calls, p)
		if i < 0 || i <= last {
			t.Fatalf("effect %q out of order or missing in %v", p, calls)
		}
		last = i
	}
	for _, c := range calls {
		if strings.Contains(c, "force") || (strings.HasPrefix(c, "git push") && strings.Contains(c, " -f")) {
			t.Fatalf("forced operation: %s", c)
		}
	}
	if !strings.Contains(strings.Join(calls, "\n"), "docker build -f deploy/Dockerfile -t ghcr.io/silgrid/aihub:v1.2.1") {
		t.Fatalf("image build line wrong: %v", calls)
	}
	if got := w.originTags(); !strings.Contains(got, "v1.2.1") {
		t.Fatalf("v1.2.1 not on origin: %q", got)
	}
	rel := filepath.Join(w.fakeRel, "v1.2.1")
	for _, f := range []string{"aihub-linux-amd64", "pf-linux-amd64", "aihub-darwin-arm64", "pf-darwin-arm64", "aihub-darwin-amd64", "pf-darwin-amd64", "aihub-image.digest", "SHA256SUMS", ".body"} {
		if _, err := os.Stat(filepath.Join(rel, f)); err != nil {
			t.Fatalf("release missing %s: %v", f, err)
		}
	}
	for n, want := range map[string]string{"aihub-darwin-arm64": "aihub-arm64-bin", "pf-darwin-arm64": "pf-arm64-bin", "aihub-darwin-amd64": "aihub-amd64-bin", "pf-darwin-amd64": "pf-amd64-bin"} {
		if got, _ := os.ReadFile(filepath.Join(rel, n)); strings.TrimSpace(string(got)) != want {
			t.Fatalf("%s content = %q, want %q (the staged darwin binary, copied under its asset name)", n, got, want)
		}
	}
	dg, _ := os.ReadFile(filepath.Join(rel, "aihub-image.digest"))
	if strings.TrimSpace(string(dg)) != relFakeDigest {
		t.Fatalf("digest file = %q", dg)
	}
	sums, _ := os.ReadFile(filepath.Join(rel, "SHA256SUMS"))
	if n := strings.Count(string(sums), "\n"); n != 7 || strings.Contains(string(sums), "SHA256SUMS") {
		t.Fatalf("SHA256SUMS should cover exactly 7 files: %q", sums)
	}
	for _, n := range []string{"aihub-linux-amd64", "pf-linux-amd64", "aihub-darwin-arm64", "pf-darwin-arm64", "aihub-darwin-amd64", "pf-darwin-amd64", "aihub-image.digest"} {
		if !strings.Contains(string(sums), "  "+n+"\n") {
			t.Fatalf("SHA256SUMS lacks %s: %q", n, sums)
		}
	}
	head := relGit(t, w.work, "rev-parse", "HEAD")
	tail := strings.Split(strings.TrimSpace(out), "\n")
	tail = tail[len(tail)-3:]
	want := []string{"version=v1.2.1", "commit=" + head, "notes=https://github.com/silgrid/polyforge/releases/tag/v1.2.1"}
	for i := range want {
		if tail[i] != want[i] {
			t.Fatalf("tail line %d = %q want %q\n%s", i, tail[i], want[i], out)
		}
	}
	// the script's own verify passes against what it just published
	vout, verr := w.run(nil, "--verify", "v1.2.1")
	if verr != nil {
		t.Fatalf("verify of fresh release failed: %v\n%s", verr, vout)
	}
}

// The design-doc cross-reference gate runs before the test gate; a
// failure stops the release before tests, build and any publish. The
// default (RELEASE_XREFS_CMD empty) runs check-xrefs.py itself — absent
// from the fake repo, so it fails the same way — and RELEASE_TEST_CMD
// being set does not skip it.
func TestReleaseScriptXrefsGateFailsBeforeTests(t *testing.T) {
	for name, cmd := range map[string]string{
		"override": "RELEASE_XREFS_CMD=echo xrefs >> \"$CALLLOG\"; exit 3",
		"default":  "RELEASE_XREFS_CMD=",
	} {
		t.Run(name, func(t *testing.T) {
			w := newRelWorld(t, []string{"v1.2.0"}, nil)
			out, err := w.run([]string{cmd})
			if err == nil {
				t.Fatalf("release passed with a failing xrefs gate:\n%s", out)
			}
			if !strings.Contains(out, "design-doc cross-reference check failed") && !strings.Contains(out, "python3 not found") {
				t.Fatalf("no clear xrefs message:\n%s", out)
			}
			if !strings.Contains(out, "nothing built or published") {
				t.Fatalf("message does not say nothing was published:\n%s", out)
			}
			for _, p := range []string{"tests", "build", "docker", "git push", "gh release"} {
				if i := relIndex(w.calls(), p); i >= 0 {
					t.Fatalf("%q ran after the xrefs gate failed: %v", p, w.calls())
				}
			}
			if got := w.originTags(); strings.Contains(got, "v1.2.1") {
				t.Fatalf("tag pushed: %q", got)
			}
		})
	}
}

func TestReleaseScriptAuditGateFailsBeforeBuild(t *testing.T) {
	// The production-dependency audit gate sits between
	// the test gate and the build: a vulnerable production dependency
	// stops the release with nothing built or published, and a failing
	// default (no webui tree) stops it the same way.
	for name, cmd := range map[string]string{
		"override": "RELEASE_AUDIT_CMD=echo audit >> \"$CALLLOG\"; exit 1",
		"default":  "RELEASE_AUDIT_CMD=",
	} {
		t.Run(name, func(t *testing.T) {
			w := newRelWorld(t, []string{"v1.2.0"}, nil)
			out, err := w.run([]string{cmd})
			if err == nil {
				t.Fatalf("release passed with a failing audit gate:\n%s", out)
			}
			if !strings.Contains(out, "production-dependency audit") {
				t.Fatalf("no clear audit message:\n%s", out)
			}
			if !strings.Contains(out, "nothing built or published") {
				t.Fatalf("message does not say nothing was published:\n%s", out)
			}
			// The test gate already ran; the build and every publish effect
			// must NOT have.
			if i := relIndex(w.calls(), "tests"); i < 0 {
				t.Fatalf("the test gate did not run before the audit gate: %v", w.calls())
			}
			for _, p := range []string{"build", "docker", "git push", "gh release"} {
				if i := relIndex(w.calls(), p); i >= 0 {
					t.Fatalf("%q ran after the audit gate failed: %v", p, w.calls())
				}
			}
			if got := w.originTags(); strings.Contains(got, "v1.2.1") {
				t.Fatalf("tag pushed: %q", got)
			}
		})
	}
}

func TestReleaseScriptVersionFloorAndBump(t *testing.T) {
	cases := []struct {
		name          string
		origin, local []string
		args          []string
		want          string
	}{
		// the default bump is patch: no argument never
		// crosses the middle number; minor/major are refused (bump guard test)
		{"default patch", []string{"v1.2.0", "v1.10.1"}, nil, nil, "v1.10.2"},
		{"patch", []string{"v1.2.0", "v1.10.1"}, nil, []string{"patch"}, "v1.10.2"},
		{"origin newer than local", []string{"v1.2.0", "v3.1.4"}, nil, []string{"patch"}, "v3.1.5"},
		// local tags are NOT a source of truth: only origin counts
		{"local tag newer than origin is ignored", []string{"v1.2.0"}, []string{"v1.9.0"}, nil, "v1.2.1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newRelWorld(t, c.origin, c.local)
			out, err := w.run(nil, c.args...)
			if err != nil {
				t.Fatalf("release failed: %v\n%s", err, out)
			}
			if !strings.Contains(out, "version="+c.want+"\n") {
				t.Fatalf("want version %s, got:\n%s", c.want, out)
			}
		})
	}
	t.Run("origin tag unseen locally is honored", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		relGit(t, w.work, "tag", "-a", "-m", "x", "v7.0.0", "HEAD~1")
		relGit(t, w.work, "push", "-q", "origin", "v7.0.0")
		relGit(t, w.work, "tag", "-d", "v7.0.0")
		out, err := w.run(nil)
		if err != nil || !strings.Contains(out, "version=v7.0.1\n") {
			t.Fatalf("err=%v\n%s", err, out)
		}
	})
}

func TestReleaseScriptTagTakenOnOriginStopsBeforePublish(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	// a parallel release claims the computed version while we build
	build := "echo build >> \"$CALLLOG\"; mkdir -p artifacts/release-$TAG; for n in aihub pf aihub-darwin-arm64 pf-darwin-arm64 aihub-darwin-amd64 pf-darwin-amd64; do echo bin > artifacts/release-$TAG/$n; done; " +
		"git tag -a -m other \"$TAG\" HEAD~1 && git push -q origin \"$TAG\" && git tag -d \"$TAG\" >/dev/null"
	out, err := w.run([]string{"RELEASE_BUILD_CMD=" + build})
	if err == nil {
		t.Fatalf("expected failure\n%s", out)
	}
	calls := w.calls()
	if relIndex(calls, "docker push") >= 0 || relIndex(calls, "gh release create") >= 0 {
		t.Fatalf("published despite taken tag: %v", calls)
	}
	if _, serr := os.Stat(filepath.Join(w.fakeRel, "v1.2.1")); serr == nil {
		t.Fatal("release created")
	}
	if relGit(t, w.work, "tag", "-l", "v1.2.1") != "" {
		t.Fatal("our local claim tag was left behind")
	}
}

func TestReleaseScriptLsRemoteFailureIsFatalBeforeAnyWork(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, []string{"v9.0.0"})
	out, err := w.run([]string{"FAKE_LSREMOTE_FAIL=1"})
	if err == nil || !strings.Contains(out, "ls-remote") {
		t.Fatalf("expected a fatal ls-remote failure: %v\n%s", err, out)
	}
	if c := w.calls(); len(c) != 0 {
		t.Fatalf("work happened despite the failing ls-remote (tests/build/push): %v", c)
	}
}

func TestReleaseScriptRefusesAnAlreadyReleasedHead(t *testing.T) {
	for name, annotated := range map[string]bool{"annotated tag": true, "lightweight tag": false} {
		t.Run(name, func(t *testing.T) {
			w := newRelWorld(t, []string{"v1.2.0"}, nil)
			// a previous release run already tagged exactly this commit on origin
			if annotated {
				relGit(t, w.work, "tag", "-a", "-m", "v1.3.0", "v1.3.0")
			} else {
				relGit(t, w.work, "tag", "v1.3.0")
			}
			relGit(t, w.work, "push", "-q", "origin", "v1.3.0")
			relGit(t, w.work, "tag", "-d", "v1.3.0")
			out, err := w.run(nil)
			if err == nil {
				t.Fatalf("expected a refusal\n%s", out)
			}
			for _, want := range []string{"already released as v1.3.0", ".polyforge/deploy.sh v1.3.0 --confirm v1.3.0"} {
				if !strings.Contains(out, want) {
					t.Errorf("message lacks %q\n%s", want, out)
				}
			}
			if c := w.calls(); len(c) != 0 {
				t.Fatalf("something ran before the refusal: %v", c)
			}
			if got := w.originTags(); strings.Contains(got, "v1.4.0") {
				t.Fatalf("a duplicate version was cut: %q", got)
			}
		})
	}
}

func TestReleaseScriptPostFailTextIsAccurate(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	out, err := w.run([]string{"FAKE_PUSH_FAIL=1"})
	if err == nil {
		t.Fatalf("expected failure\n%s", out)
	}
	for _, want := range []string{"BURNED", "immutable", "artifacts/release-v1.2.1/aihub", "staging directory"} {
		if !strings.Contains(out, want) {
			t.Errorf("post-fail text lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "delete the tag by hand") {
		t.Errorf("must not suggest deleting a pushed tag\n%s", out)
	}
}

func TestReleaseScriptRefusals(t *testing.T) {
	t.Run("dirty tree", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.0.0"}, nil)
		relWrite(t, filepath.Join(w.work, "README"), "dirty\n", 0o644)
		out, err := w.run(nil)
		if err == nil || !strings.Contains(out, "dirty") || len(w.calls()) != 0 {
			t.Fatalf("err=%v calls=%v\n%s", err, w.calls(), out)
		}
	})
	t.Run("HEAD not origin/main", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.0.0"}, nil)
		relWrite(t, filepath.Join(w.work, "third"), "3\n", 0o644)
		relGit(t, w.work, "add", "-A")
		relGit(t, w.work, "commit", "-q", "-m", "unpushed")
		out, err := w.run(nil)
		if err == nil || !strings.Contains(out, "not origin/main") || len(w.calls()) != 0 {
			t.Fatalf("err=%v calls=%v\n%s", err, w.calls(), out)
		}
	})
	t.Run("bad bump", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.0.0"}, nil)
		if _, err := w.run(nil, "huge"); err == nil {
			t.Fatal("expected failure")
		}
	})
	// minor/major need an owner ruling the script cannot judge: refused
	// before any work, with the floor's minor/major successor never computed
	// (; the repro test pins the no-side-effect contract).
	for _, bump := range []string{"minor", "major"} {
		t.Run(bump+" refused", func(t *testing.T) {
			w := newRelWorld(t, []string{"v1.10.1"}, nil)
			out, err := w.run(nil, bump)
			if err == nil || !strings.Contains(out, "bump "+bump+" refused") || !strings.Contains(out, "owner") || len(w.calls()) != 0 {
				t.Fatalf("err=%v calls=%v\n%s", err, w.calls(), out)
			}
		})
	}
}

func TestReleaseScriptNotesGeneratedWhenMissing(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	if out, err := w.run(nil); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	body, _ := os.ReadFile(filepath.Join(w.fakeRel, "v1.2.1", ".body"))
	if !strings.Contains(string(body), "second change") || !strings.Contains(string(body), "v1.2.0") {
		t.Fatalf("generated notes lack the log: %q", body)
	}
	if relGit(t, w.work, "status", "--porcelain", "--", "docs") != "" {
		t.Fatal("generated notes were written into the worktree")
	}
}

func TestReleaseScriptCommittedNotesUsedAsIs(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	w.mergeOnMain(filepath.Join("docs", "release-notes", "v1.2.1.md"), "# hand written\n", "notes", true)
	if out, err := w.run(nil); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	body, _ := os.ReadFile(filepath.Join(w.fakeRel, "v1.2.1", ".body"))
	if string(body) != "# hand written\n" {
		t.Fatalf("notes = %q", body)
	}
}

func TestReleaseScriptLoginWhenNotLoggedIn(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	empty := filepath.Join(w.root, "emptycfg")
	relWrite(t, filepath.Join(empty, "config.json"), `{}`, 0o644)
	if out, err := w.run([]string{"DOCKER_CONFIG=" + empty}); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	if relIndex(w.calls(), "docker login") < 0 {
		t.Fatalf("no login: %v", w.calls())
	}
}

// ---- --verify

// publishFake plants a release in the fake gh store; mutate lets a case break it.
func (w *relWorld) publishFake(tag string, files map[string]string) {
	d := filepath.Join(w.fakeRel, tag)
	for n, c := range files {
		relWrite(w.t, filepath.Join(d, n), c, 0o644)
	}
	relWrite(w.t, filepath.Join(d, ".body"), "notes\n", 0o644)
}

func goodRelease() map[string]string {
	f := map[string]string{
		"aihub-linux-amd64": "A", "pf-linux-amd64": "P",
		"aihub-darwin-arm64": "Aa", "pf-darwin-arm64": "Pa",
		"aihub-darwin-amd64": "Ax", "pf-darwin-amd64": "Px",
		"aihub-image.digest": relFakeDigest + "\n",
	}
	var sums strings.Builder
	for _, n := range []string{"aihub-linux-amd64", "pf-linux-amd64", "aihub-darwin-arm64", "pf-darwin-arm64", "aihub-darwin-amd64", "pf-darwin-amd64", "aihub-image.digest"} {
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256([]byte(f[n])), n)
	}
	f["SHA256SUMS"] = sums.String()
	return f
}

func TestReleaseScriptVerify(t *testing.T) {
	cases := []struct {
		name   string
		tag    string
		origin []string
		mut    func(f map[string]string)
		env    []string
		fail   string // substring of the FAIL line; empty = expect pass
	}{
		{name: "pass", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"}},
		{name: "pass first release", tag: "v1.0.0", origin: []string{"v1.0.0"}},
		{name: "checksum mismatch (linux)", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			mut: func(f map[string]string) { f["pf-linux-amd64"] = "tampered" }, fail: "sha256sum"},
		{name: "checksum mismatch (darwin)", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			mut: func(f map[string]string) { f["pf-darwin-arm64"] = "tampered" }, fail: "sha256sum"},
		{name: "wrong asset set", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			mut: func(f map[string]string) { f["extra.txt"] = "x" }, fail: "asset set"},
		{name: "missing asset", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			mut: func(f map[string]string) { delete(f, "pf-linux-amd64") }, fail: "asset set"},
		{name: "digest mismatch", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			env: []string{"FAKE_REGISTRY_DIGEST=sha256:bbbb"}, fail: "digest mismatch"},
		// no buildx: the `docker manifest inspect -v` fallback reads the
		// lowercase "digest" inside "Descriptor"
		{name: "pass via manifest fallback", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			env: []string{"FAKE_NO_BUILDX=1"}},
		{name: "digest mismatch via manifest fallback", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			env: []string{"FAKE_NO_BUILDX=1", "FAKE_REGISTRY_DIGEST=sha256:bbbb"}, fail: "digest mismatch"},
		{name: "non-monotonic", tag: "v1.2.0", origin: []string{"v1.02.0", "v1.2.0"}, fail: "monotonic"},
		{name: "newer tag exists", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0", "v1.4.0"}, fail: "not strictly greater"},
		{name: "equal key spelling exists", tag: "v1.3.0", origin: []string{"v1.3.0", "v1.03.0"}, fail: "not strictly greater"},
		{name: "digest file not canonical", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			mut: func(f map[string]string) { f["aihub-image.digest"] = imageRepoDigest(relFakeDigest) + "\n" }, fail: "aihub-image.digest format"},
		{name: "ls-remote fails", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			env: []string{"FAKE_LSREMOTE_FAIL=1"}, fail: "ls-remote"},
		{name: "bad format", tag: "1.3", origin: []string{"v1.2.0"}, fail: "tag format"},
		{name: "tag not on origin", tag: "v1.3.0", origin: []string{"v1.2.0"}, fail: "not found on origin"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newRelWorld(t, c.origin, nil)
			f := goodRelease()
			if c.mut != nil {
				c.mut(f)
			}
			w.publishFake(c.tag, f)
			before := relGit(t, w.origin, "tag", "-l")
			out, err := w.run(c.env, "--verify", c.tag)
			if c.fail == "" {
				if err != nil || !strings.Contains(out, "PASS") || strings.Contains(out, "FAIL") {
					t.Fatalf("expected pass: %v\n%s", err, out)
				}
			} else {
				ok := false
				for _, l := range strings.Split(out, "\n") {
					if strings.HasPrefix(l, "FAIL") && strings.Contains(l, c.fail) {
						ok = true
					}
				}
				if err == nil || !ok {
					t.Fatalf("expected FAIL containing %q: err=%v\n%s", c.fail, err, out)
				}
			}
			if len(w.calls()) != 0 || relGit(t, w.origin, "tag", "-l") != before {
				t.Fatalf("verify mutated state: calls=%v", w.calls())
			}
		})
	}
}

func TestReleaseScriptHelpMentionsVerify(t *testing.T) {
	w := newRelWorld(t, []string{"v1.0.0"}, nil)
	for _, a := range []string{"--help", "-h"} {
		out, err := w.run(nil, a)
		if err != nil || !strings.Contains(out, "--verify") {
			t.Fatalf("%s: %v\n%s", a, err, out)
		}
	}
}

// ---- PR-only check (polyforge#163)

// TestReleaseScriptRefusesCommitsNotFromAMergedPR: every first-parent commit
// that entered main since the previous release tag must be the merge commit of
// a merged PR; anything else refuses the release before the test gate, with
// nothing tagged or published.
func TestReleaseScriptRefusesCommitsNotFromAMergedPR(t *testing.T) {
	cases := []struct {
		name  string
		setup func(w *relWorld) string // returns the offending sha
		env   []string
		why   string
	}{
		{"direct commit on main", func(w *relWorld) string { return w.commitOnMain("direct", "direct push") }, nil, "not a merge commit"},
		{"local merge never a PR", func(w *relWorld) string { return w.mergeOnMain("local", "l\n", "local merge", false) }, nil, "no merged PR"},
		{"squash-style PR commit", func(w *relWorld) string {
			sha := w.commitOnMain("squashed", "squashed PR")
			w.recordPR(sha) // associated with a PR, but not a merge commit
			return sha
		}, nil, "not a merge commit"},
		{"gh api failure", func(w *relWorld) string { return relGit(w.t, w.work, "rev-parse", "HEAD") }, []string{"FAKE_GH_API_FAIL=1"}, "gh api failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := newRelWorld(t, []string{"v1.2.0"}, nil)
			bad := c.setup(w)
			w.landPR("later", "a later PR") // a good PR after it does not launder it
			if c.env != nil {
				bad = relGit(t, w.work, "rev-parse", "HEAD")
			}
			out, err := w.run(c.env)
			if err == nil {
				t.Fatalf("expected a refusal\n%s", out)
			}
			for _, want := range []string{"without a merged PR", bad, c.why, "nothing built or published"} {
				if !strings.Contains(out, want) {
					t.Errorf("refusal lacks %q\n%s", want, out)
				}
			}
			if calls := w.calls(); len(calls) != 0 {
				t.Fatalf("work happened despite the refusal: %v", calls)
			}
			if got := w.originTags(); strings.Contains(got, "v1.2.1") {
				t.Fatalf("tag cut despite the refusal: %q", got)
			}
		})
	}
}

// TestReleaseScriptVerifyPROnly: --verify runs the same check over
// previous-tag..tag.
func TestReleaseScriptVerifyPROnly(t *testing.T) {
	for _, c := range []struct {
		name   string
		direct bool
		env    []string
		fail   string
	}{
		{name: "all through PRs", direct: false},
		{name: "a direct commit", direct: true, fail: "not a merge commit"},
		{name: "gh api failure", env: []string{"FAKE_GH_API_FAIL=1"}, fail: "gh api failed"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newRelWorld(t, []string{"v1.2.0"}, nil)
			if c.direct {
				w.commitOnMain("direct", "direct push")
			}
			w.landPR("third", "third change")
			relGit(t, w.work, "tag", "-a", "-m", "v1.3.0", "v1.3.0")
			relGit(t, w.work, "push", "-q", "origin", "v1.3.0")
			w.publishFake("v1.3.0", goodRelease())
			out, err := w.run(c.env, "--verify", "v1.3.0")
			if c.fail == "" {
				if err != nil || !strings.Contains(out, "PASS pr-only") || strings.Contains(out, "FAIL") {
					t.Fatalf("expected pass: %v\n%s", err, out)
				}
				return
			}
			ok := false
			for _, l := range strings.Split(out, "\n") {
				if strings.HasPrefix(l, "FAIL pr-only") && strings.Contains(l, c.fail) {
					ok = true
				}
			}
			if err == nil || !ok {
				t.Fatalf("expected FAIL pr-only containing %q: err=%v\n%s", c.fail, err, out)
			}
		})
	}
}

// ---- owner-direct allowlist (polyforge#240)

const (
	ownerDirectEmail = "owner@example.com" // the one allowlisted author (the test sets OWNER_DIRECT_EMAILS)
	agentEmail       = "agent@example.com" // workspace / step-agent identity: never allowlisted (#149)
)

// commitOnMainAs is commitOnMain with the given author email (the committer
// stays t@e: only the author is matched).
func (w *relWorld) commitOnMainAs(email, file, msg string) string {
	w.t.Helper()
	relWrite(w.t, filepath.Join(w.work, file), msg+"\n", 0o644)
	relGit(w.t, w.work, "add", "-A")
	relGit(w.t, w.work, "commit", "-q", "--author", "Someone <"+email+">", "-m", msg)
	relGit(w.t, w.work, "push", "-q", "origin", "main")
	return relGit(w.t, w.work, "rev-parse", "HEAD")
}

// ownerDirectCases: the ranges both modes judge. setup returns the allowlisted
// sha ("" when none) and the refused sha ("" when the range must pass).
var ownerDirectCases = []struct {
	name  string
	setup func(w *relWorld) (owner, bad string)
	why   string
}{
	{"allowlisted direct commit", func(w *relWorld) (string, string) {
		return w.commitOnMainAs(ownerDirectEmail, "owner", "owner direct"), ""
	}, ""},
	{"allowlisted non-PR merge", func(w *relWorld) (string, string) {
		// a local --no-ff merge never recorded as a PR, authored by the owner
		relGit(w.t, w.work, "checkout", "-q", "-b", "topic-owner")
		relWrite(w.t, filepath.Join(w.work, "ownermerge"), "m\n", 0o644)
		relGit(w.t, w.work, "add", "-A")
		relGit(w.t, w.work, "commit", "-q", "-m", "owner topic")
		relGit(w.t, w.work, "checkout", "-q", "main")
		relGit(w.t, w.work, "merge", "-q", "--no-ff", "--no-commit", "topic-owner")
		relGit(w.t, w.work, "commit", "-q", "--author", "Owner <"+ownerDirectEmail+">", "-m", "owner merge")
		relGit(w.t, w.work, "push", "-q", "origin", "main")
		return relGit(w.t, w.work, "rev-parse", "HEAD"), ""
	}, ""},
	{"agent identity direct commit", func(w *relWorld) (string, string) {
		return "", w.commitOnMainAs(agentEmail, "agent", "agent direct")
	}, "not a merge commit"},
	{"other identity direct commit", func(w *relWorld) (string, string) {
		return "", w.commitOnMainAs("someone@example.com", "other", "other direct")
	}, "not a merge commit"},
	{"mixed range", func(w *relWorld) (string, string) {
		o := w.commitOnMainAs(ownerDirectEmail, "owner", "owner direct")
		b := w.commitOnMainAs(agentEmail, "agent", "agent direct")
		return o, b
	}, "not a merge commit"},
}

// TestReleaseScriptOwnerDirectAllowlist: release mode passes an allowlisted
// direct commit with an owner-direct audit line, and still refuses every other
// identity (the step-agent one included) and a mixed range.
func TestReleaseScriptOwnerDirectAllowlist(t *testing.T) {
	for _, c := range ownerDirectCases {
		t.Run(c.name, func(t *testing.T) {
			w := newRelWorld(t, []string{"v1.2.0"}, nil)
			owner, bad := c.setup(w)
			w.landPR("later", "a later PR")
			out, err := w.run([]string{"OWNER_DIRECT_EMAILS=" + ownerDirectEmail})
			if owner != "" && !strings.Contains(out, "PASS pr-only: owner-direct: "+owner+" "+ownerDirectEmail+"\n") {
				t.Errorf("no owner-direct audit line for %s\n%s", owner, out)
			}
			if bad == "" {
				if err != nil || !strings.Contains(out, "version=v1.2.1") {
					t.Fatalf("expected a release: %v\n%s", err, out)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected a refusal\n%s", out)
			}
			for _, want := range []string{"without a merged PR", bad + " (" + c.why + ")", "nothing built or published"} {
				if !strings.Contains(out, want) {
					t.Errorf("refusal lacks %q\n%s", want, out)
				}
			}
			if owner != "" && strings.Contains(out, owner+" (") {
				t.Errorf("allowlisted %s listed among the refused commits\n%s", owner, out)
			}
			if calls := w.calls(); len(calls) != 0 {
				t.Fatalf("work happened despite the refusal: %v", calls)
			}
			if got := w.originTags(); strings.Contains(got, "v1.2.1") {
				t.Fatalf("tag cut despite the refusal: %q", got)
			}
		})
	}
}

// TestReleaseScriptVerifyOwnerDirectAllowlist: --verify applies the same rule
// and prints the same audit line.
func TestReleaseScriptVerifyOwnerDirectAllowlist(t *testing.T) {
	for _, c := range ownerDirectCases {
		t.Run(c.name, func(t *testing.T) {
			w := newRelWorld(t, []string{"v1.2.0"}, nil)
			owner, bad := c.setup(w)
			w.landPR("third", "third change")
			relGit(t, w.work, "tag", "-a", "-m", "v1.3.0", "v1.3.0")
			relGit(t, w.work, "push", "-q", "origin", "v1.3.0")
			w.publishFake("v1.3.0", goodRelease())
			out, err := w.run([]string{"OWNER_DIRECT_EMAILS=" + ownerDirectEmail}, "--verify", "v1.3.0")
			if owner != "" && !strings.Contains(out, "PASS pr-only: owner-direct: "+owner+" "+ownerDirectEmail+"\n") {
				t.Errorf("no owner-direct audit line for %s\n%s", owner, out)
			}
			if bad == "" {
				if err != nil || strings.Contains(out, "FAIL") {
					t.Fatalf("expected pass: %v\n%s", err, out)
				}
				return
			}
			ok := false
			for _, l := range strings.Split(out, "\n") {
				if strings.HasPrefix(l, "FAIL pr-only") && strings.Contains(l, bad+" ("+c.why+")") {
					ok = true
				}
			}
			if err == nil || !ok {
				t.Fatalf("expected FAIL pr-only naming %s: err=%v\n%s", bad, err, out)
			}
		})
	}
}
