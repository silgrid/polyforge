// carries: .polyforge/release.sh

package test

// Fake-env tests for .polyforge/release.sh: a local bare repo is origin, and
// git/docker/gh/npm are stubs first on PATH that append to a call log. No
// network, no docker, no gh, no npm registry, no real remote is ever touched.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
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
// "ghcr.io/silgrid/polyforge-aihub@" + that).
const relFakeDigest = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

// imageRepoDigest is the RepoDigests entry the registry reports for a digest.
func imageRepoDigest(digest string) string { return "ghcr.io/silgrid/polyforge-aihub@" + digest }

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

// relPiwebPkg is an npm-shaped (pretty-printed) pi-web package.json.
func relPiwebPkg(version string) string {
	return fmt.Sprintf("{\n  \"name\": \"@silgrid/pi-web\",\n  \"version\": \"%s\"\n}\n", version)
}

// relPiwebTgz is an npm-pack-shaped tarball: package/package.json at version
// (in-memory writers cannot fail).
func relPiwebTgz(version string) string {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	body := relPiwebPkg(version)
	_ = tw.WriteHeader(&tar.Header{Name: "package/package.json", Mode: 0o644, Size: int64(len(body))})
	_, _ = tw.Write([]byte(body))
	_ = tw.Close()
	_ = gz.Close()
	return buf.String()
}

// relRepin recomputes a fake release's SHA256SUMS over its 5 payloads and its
// manifest (keeping the manifest's commit), so a case that swaps a payload
// fails only the check it targets.
func relRepin(f map[string]string, tag string) {
	tgz := "silgrid-pi-web-" + tag + ".tgz"
	var sums strings.Builder
	for _, n := range []string{"pf-linux-amd64", "pf-darwin-arm64", "pf-darwin-amd64", "aihub-image.digest", tgz} {
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256([]byte(f[n])), n)
	}
	f["SHA256SUMS"] = sums.String()
	commit := ""
	if m := strings.SplitN(f["release-manifest.json"], `"commit": "`, 2); len(m) == 2 {
		commit = strings.SplitN(m[1], `"`, 2)[0]
	}
	f["release-manifest.json"] = relManifest(tag, commit, sums.String())
}

// relManifest is release-manifest.json as release.sh's manifest_of writes it
// for tag/commit over the SHA256SUMS content sums.
func relManifest(tag, commit, sums string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "{\n  \"tag\": \"%s\",\n  \"commit\": \"%s\",\n", tag, commit)
	b.WriteString("  \"order\": \"aihub first: the aihub image and the pf binaries are published with this tag before tools/install.sh can consume the release; the pi-web package rides the same release\",\n")
	b.WriteString("  \"artifacts\": [\n")
	var rows []string
	for _, l := range strings.Split(strings.TrimSpace(sums), "\n") {
		if f := strings.Fields(l); len(f) == 2 {
			rows = append(rows, fmt.Sprintf("    {\"name\": \"%s\", \"sha256\": \"%s\"}", strings.TrimPrefix(f[1], "*"), f[0]))
		}
	}
	b.WriteString(strings.Join(rows, ",\n") + "\n  ]\n}\n")
	return b.String()
}

// relGateConf and relGateDockerfile are a fake world's test-gate definition
// (the real tools/gate-id and tools/green-tag read them).
const (
	relGateConf       = "stack=none\nfast_gate=echo gate-fast\nfull_command=echo gate-full\n"
	relGateDockerfile = "FROM gate-image\n"
)

// relGateTree writes the gate definition and copies the real gate tools
// (and the scope engine whose plan the gate runs) into dir.
func relGateTree(t *testing.T, dir string) {
	t.Helper()
	relWrite(t, filepath.Join(dir, ".polyforge", "verify-scope.conf"), relGateConf, 0o644)
	relWrite(t, filepath.Join(dir, ".polyforge", "Dockerfile.verify"), relGateDockerfile, 0o644)
	for _, tool := range []string{"tools/gate-id", "tools/green-tag", "workflows/_shared/scripts/verify-scope"} {
		raw, err := os.ReadFile(filepath.Join("..", filepath.FromSlash(tool)))
		if err != nil {
			t.Fatal(err)
		}
		relWrite(t, filepath.Join(dir, filepath.FromSlash(tool)), string(raw), 0o755)
	}
	// the gate itself: the tree's workflow script and its library
	// (workflows/_shared/scripts/gate.py, pfstep), as the release runs it
	scripts := filepath.Join("..", "workflows", "_shared", "scripts")
	files, err := filepath.Glob(filepath.Join(scripts, "pfstep", "*.py"))
	if err != nil || len(files) == 0 {
		t.Fatalf("the gate's library: %v %v", files, err)
	}
	for _, src := range append(files, filepath.Join(scripts, "gate.py")) {
		raw, err := os.ReadFile(src)
		if err != nil {
			t.Fatal(err)
		}
		rel, _ := filepath.Rel(filepath.Join("..", "workflows"), src)
		relWrite(t, filepath.Join(dir, "workflows", rel), string(raw), 0o644)
	}
}

// The fake world's workflow contract: the two files the
// release's contract check reads, with the version and the pending list's
// explanation as the real tree carries them (an empty list: no line starts
// with a level-two heading).
const (
	relContractGo           = "internal/common/workflow/contract.go"
	relContractPending      = "internal/common/workflow/contract-pending.md"
	relContractPendingEmpty = "# Incompatible workflow-contract changes awaiting the next version bump\n\n" +
		"Register an incompatible change below, one level-two heading per change.\n"
)

func relContractGoAt(version int) string {
	return fmt.Sprintf("package workflow\n\nconst ContractVersion = %d\n", version)
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
	// like the real repo: the build output never dirties the worktree, so a
	// second run (a resume) starts from a clean tree
	relWrite(t, filepath.Join(w.work, ".gitignore"), "/artifacts/release-*/\n", 0o644)
	// the pi-web package the release packs: main keeps its
	// development version; the tag's version reaches only the packed copy.
	relWrite(t, filepath.Join(w.work, "pi-web", "package.json"), relPiwebPkg("0.2.0-dev.0"), 0o644)
	// the test gate's definition and its tools (the gate id and the green
	// tag), like the real tree: the release reads them from
	// the commit it releases
	relGateTree(t, w.work)
	// the workflow contract the release's contract check reads:
	// a fixed version and an empty pending list, never the real files (the
	// real list may hold entries one day and turn every release test red)
	relWrite(t, filepath.Join(w.work, filepath.FromSlash(relContractGo)), relContractGoAt(1), 0o644)
	relWrite(t, filepath.Join(w.work, filepath.FromSlash(relContractPending)), relContractPendingEmpty, 0o644)
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
registry_has() {
  _t=${1##*:}
  [ -e "$CALLLOG.pushed" ] && return 0
  case " $FAKE_REGISTRY_HAS " in *" $_t "*) return 0 ;; esac
  git --git-dir="$FAKE_ORIGIN" rev-parse -q --verify "refs/tags/$_t" >/dev/null 2>&1
}
case "$1" in
  # The test gate's container (; gate.py's docker client):
  # the image built from stdin (no context), a container whose commands
  # with a --workdir run here, in that directory, with only the image's
  # PATH and the given environment (TMPDIR mapped under the world); an exec
  # without --workdir (the keep-alive wait, the reap) never runs here - the
  # reap would kill this machine's processes - except the command probe.
  version) [ -n "$FAKE_DOCKER_DOWN" ] && { echo "Cannot connect to the Docker daemon at unix:///var/run/docker.sock" >&2; exit 1; }
    echo 29.0 ;;
  image) case "$2" in
      ls) [ -e "$CALLLOG.gateimage" ] && echo sha256:gate ;;
      inspect) echo '["PATH=/usr/bin:/bin","IMAGE_ONLY=from-image"]' ;;
    esac ;;
  run) echo "docker run $*" >> "$CALLLOG"; echo gatecid ;;
  rm) echo "docker rm $*" >> "$CALLLOG" ;;
  exec) shift; wd=""; envf=$(mktemp)
    while [ $# -gt 0 ]; do case "$1" in
      --workdir) wd=$2; shift 2 ;;
      --env) printf '%s\n' "$2" >> "$envf"; shift 2 ;;
      *) break ;;
    esac; done
    shift   # the container id
    if [ -z "$wd" ]; then
      rm -f "$envf"
      case "$*" in *pf-probe:*) exec "$@" ;; esac
      exit 0
    fi
    echo "docker exec $*" >> "$CALLLOG"
    mkdir -p "$CALLLOG.gatetmp" "$CALLLOG.gatehome"
    while IFS= read -r kv; do
      case "$kv" in TMPDIR=*) kv="TMPDIR=$CALLLOG.gatetmp/t" ;; esac
      set -- "$kv" "$@"
    done < "$envf"
    rm -f "$envf"
    cd "$wd" || exit 125
    exec env -i PATH=/usr/bin:/bin HOME="$CALLLOG.gatehome" IMAGE_ONLY=from-image "$@" ;;
  build)
    for last; do :; done
    if [ "$last" = - ]; then
      cat > "$CALLLOG.gatedockerfile"; : > "$CALLLOG.gateimage"
      echo "docker gate-build $3" >> "$CALLLOG"; exit 0
    fi
    echo "docker $*" >> "$CALLLOG"
    # the image build context: its file list (the last argument is the dir)
    for ctx; do :; done; echo "docker build context: $(ls "$ctx" | tr '\n' ' ')" >> "$CALLLOG" ;;
  push) echo "docker $*" >> "$CALLLOG"; [ -n "$FAKE_PUSH_FAIL" ] && exit 1
    : > "$CALLLOG.pushed" ;;
  inspect) echo "docker inspect" >> "$CALLLOG"; echo "ghcr.io/silgrid/polyforge-aihub@$FAKE_PUSH_DIGEST" ;;
  login) echo "docker login" >> "$CALLLOG"; cat >/dev/null; : > "$CALLLOG.loggedin" ;;
  # Registry knobs: FAKE_REGISTRY_MISSING - "not found"
  # until a docker push of this world succeeded; FAKE_REGISTRY_APPEARS_ON=<file>
  # - "not found" until <file> exists (another run pushes meanwhile);
  # FAKE_REGISTRY_UNREADABLE - no read works; FAKE_BUILDX_ERROR=<text> -
  # buildx fails with <text> while docker manifest still answers;
  # FAKE_REGISTRY_NEEDS_LOGIN - unauthorized until a docker login succeeded.
  # Otherwise the registry holds a tag when origin carries it as a release,
  # this world pushed an image, or FAKE_REGISTRY_HAS names it (another
  # build's tag in the same package).
  buildx) [ -n "$FAKE_NO_BUILDX" ] && { echo "docker: 'buildx' is not a docker command." >&2; exit 1; }
    [ -n "$FAKE_REGISTRY_NEEDS_LOGIN" ] && [ ! -e "$CALLLOG.loggedin" ] && { echo "ERROR: unauthorized: authentication required" >&2; exit 1; }
    [ -n "$FAKE_REGISTRY_UNREADABLE" ] && { echo "ERROR: failed to authorize: 403 Forbidden" >&2; exit 1; }
    [ -n "$FAKE_BUILDX_ERROR" ] && { echo "ERROR: $FAKE_BUILDX_ERROR" >&2; exit 1; }
    [ -n "$FAKE_REGISTRY_MISSING" ] && [ ! -e "$CALLLOG.pushed" ] && { echo "ERROR: $4: not found" >&2; exit 1; }
    [ -n "$FAKE_REGISTRY_APPEARS_ON" ] && [ ! -e "$FAKE_REGISTRY_APPEARS_ON" ] && { echo "ERROR: $4: not found" >&2; exit 1; }
    registry_has "$4" || { echo "ERROR: $4: not found" >&2; exit 1; }
    echo "Name: x"; echo "Digest: $FAKE_REGISTRY_DIGEST" ;;
  # config/layer digests come BEFORE the Descriptor here, so only the
  # Descriptor-scoped read can find the manifest digest
  manifest)
    [ -n "$FAKE_REGISTRY_UNREADABLE" ] && { echo "unauthorized: authentication required" >&2; exit 1; }
    [ -n "$FAKE_REGISTRY_NEEDS_LOGIN" ] && [ ! -e "$CALLLOG.loggedin" ] && { echo "unauthorized: authentication required" >&2; exit 1; }
    [ -n "$FAKE_REGISTRY_MISSING" ] && [ ! -e "$CALLLOG.pushed" ] && { echo "no such manifest: $4" >&2; exit 1; }
    [ -n "$FAKE_REGISTRY_APPEARS_ON" ] && [ ! -e "$FAKE_REGISTRY_APPEARS_ON" ] && { echo "no such manifest: $4" >&2; exit 1; }
    registry_has "$4" || { echo "no such manifest: $4" >&2; exit 1; }
    printf '{\n\t"Ref": "x",\n\t"SchemaV2Manifest": {\n\t\t"config": {\n\t\t\t"digest": "sha256:cccc"\n\t\t},\n\t\t"layers": [\n\t\t\t{\n\t\t\t\t"Digest": "sha256:dddd"\n\t\t\t}\n\t\t]\n\t},\n\t"Descriptor": {\n\t\t"mediaType": "m",\n\t\t"digest": "%s",\n\t\t"size": 952\n\t}\n}\n' "$FAKE_REGISTRY_DIGEST" ;;
esac
exit 0
`, 0o755)
	// npm: the pi-web pack stage in the exported tree. `version X` rewrites the
	// exported package.json like npm does; `pack` writes a real
	// silgrid-pi-web-<version>.tgz (package/package.json inside).
	// FAKE_NPM_FAIL=<subcommand> fails that subcommand.
	relWrite(t, filepath.Join(w.bin, "npm"), `#!/bin/sh
[ -n "$FAKE_NPM_FAIL" ] && [ "$1" = "$FAKE_NPM_FAIL" ] && { echo "npm $1 failed" >&2; exit 1; }
case "$1" in
  ci) echo "npm ci" >> "$CALLLOG" ;;
  run) echo "npm run $2 PI_WEB_RELEASE_VERSION=$PI_WEB_RELEASE_VERSION" >> "$CALLLOG" ;;
  version) echo "npm $*" >> "$CALLLOG"
    printf '{\n  "name": "@silgrid/pi-web",\n  "version": "%s"\n}\n' "$2" > package.json ;;
  pack) echo "npm $*" >> "$CALLLOG"
    dest=.; [ "$2" = --pack-destination ] && dest=$3
    v=$(sed -n 's/^ *"version": *"\([^"]*\)".*/\1/p' package.json)
    st=$(mktemp -d); mkdir "$st/package"; cp package.json "$st/package/"
    tar -czf "$dest/silgrid-pi-web-$v.tgz" -C "$st" package; rm -rf "$st" ;;
esac
exit 0
`, 0o755)
	relWrite(t, filepath.Join(w.bin, "gh"), `#!/bin/sh
# every repository a call targets (-R <repo>, or the <owner>/<repo> of an
# "api repos/..." path) is appended to $FAKE_GH/repos.log.
mkdir -p "$FAKE_GH"
prevr=
for a in "$@"; do [ "$prevr" = "-R" ] && echo "$a" >> "$FAKE_GH/repos.log"; prevr=$a; done
case "$1 $2" in "api repos/"*) echo "$2" | cut -d/ -f2-3 >> "$FAKE_GH/repos.log" ;; esac
jqexpr=
prev=
for a in "$@"; do
  if [ "$prev" = "--jq" ] || [ "$prev" = "-q" ]; then jqexpr=$a; fi
  case "$a" in
    --jq=*) jqexpr=${a#--jq=} ;;
    -q=*) jqexpr=${a#-q=} ;;
  esac
  prev=$a
done
gh_filter() { if [ -n "$jqexpr" ]; then jq -r "$jqexpr"; else cat; fi; }
cmd="$1 $2"
case "$cmd" in
  "release create")
    echo "gh release create $3" >> "$CALLLOG"
    # FAKE_GH_CREATE_FAIL: the create fails after the claim (#120's shape)
    [ -n "$FAKE_GH_CREATE_FAIL" ] && { echo "tag $3 doesn't exist in the repo, aborting due to --verify-tag flag" >&2; exit 1; }
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
    [ -n "$FAKE_GH_VIEW_FAIL" ] && { echo "HTTP 502: Bad Gateway" >&2; exit 1; }
    case "$*" in *.body*) cat "$d/.body" ;; *isDraft*) cat "$d/.draft" 2>/dev/null || echo false ;; *) ls "$d" ;; esac ;;
  "release upload")
    # never --clobber: an asset already published is an error, like GitHub's
    echo "gh release upload $3" >> "$CALLLOG"
    d="$FAKE_GH/$3"; shift 3
    [ -d "$d" ] || { echo "release not found" >&2; exit 1; }
    while [ $# -gt 0 ]; do
      case "$1" in
        -R) shift 2 ;;
        --clobber) echo "gh release upload --clobber" >> "$CALLLOG"; shift ;;
        *) [ -e "$d/$(basename "$1")" ] && { echo "asset $(basename "$1") already exists" >&2; exit 1; }
           cp "$1" "$d/"; echo "gh release upload asset $(basename "$1")" >> "$CALLLOG"; shift ;;
      esac
    done ;;
  "release edit")
    echo "gh release edit $3 $4" >> "$CALLLOG"
    [ "$4" = --draft=false ] && rm -f "$FAKE_GH/$3/.draft" ;;
  "release download")
    d="$FAKE_GH/$3"; shift 3
    out=.; while [ $# -gt 0 ]; do case "$1" in -D) out="$2"; shift 2 ;; *) shift ;; esac; done
    for f in "$d"/*; do cp "$f" "$out/"; done ;;
  "api repos/"*)
    # merged-PR record: a sha listed in $FAKE_GH/pr-merges is some merged PR's
    # merge_commit_sha, and that PR record is the gate's association source (the
    # commit->pulls index is not modelled: the production gate never reads it).
    # pr-unmerged holds closed-but-unmerged PRs whose test-merge sha equals the
    # sha (merged_at null), pr-nosha holds closed PRs with no merge_commit_sha at
    # all, and pr-page2-merges holds page-2 merged PRs. Paged like the real list:
    # page 1 carries pr-merges then pr-unmerged then pr-nosha, page 2 carries
    # pr-page2-merges, and page 3 and beyond are empty.
    [ -n "$FAKE_GH_API_FAIL" ] && { echo "gh: HTTP 502" >&2; exit 1; }
    case "$2" in
      */pulls/[0-9]*)
        # one PR: $FAKE_GH/pull-<n> holds "merged <sha>" or
        # "unmerged <sha>"; no file is GitHub's 404; FAKE_GH_PULL_FAIL fails
        # the read itself (a transient API error).
        [ -n "$FAKE_GH_PULL_FAIL" ] && { echo "gh: HTTP 502 Bad Gateway" >&2; exit 1; }
        n=${2##*/}
        [ -f "$FAKE_GH/pull-$n" ] || { echo "gh: Not Found (HTTP 404)" >&2; exit 1; }
        read -r st sha < "$FAKE_GH/pull-$n"
        if [ "$st" = merged ]; then ma='"2026-01-01T00:00:00Z"'; else ma=null; fi
        printf '{"number":%s,"merged_at":%s,"merge_commit_sha":"%s"}' "$n" "$ma" "$sha" | gh_filter
        exit 0 ;;
      */pulls?*)
        page=1
        case "$2" in *page=*) page=${2##*page=} ;; esac
        case "$page" in *[!0-9]*) page=1 ;; esac
        now=$(date -u +%Y-%m-%dT%H:%M:%SZ)
        emit_row() {
          [ "$first" = 1 ] || printf ','
          case "$1" in
            merged) printf '{"number":7,"state":"closed","merged_at":"%s","merge_commit_sha":"%s","updated_at":"%s"}' "$now" "$2" "$now" ;;
            unmerged) printf '{"number":7,"state":"closed","merged_at":null,"merge_commit_sha":"%s","updated_at":"%s"}' "$2" "$now" ;;
            nosha) printf '{"number":7,"state":"closed","merged_at":null,"merge_commit_sha":null,"updated_at":"%s"}' "$now" ;;
          esac
          first=0
        }
        emit_file() {
          [ -f "$1" ] || return 0
          while IFS= read -r s; do
            [ -n "$s" ] || continue
            emit_row "$2" "$s"
          done < "$1"
        }
        {
          printf '['
          first=1
          if [ "$page" = 1 ]; then
            emit_file "$FAKE_GH/pr-merges" merged
            emit_file "$FAKE_GH/pr-unmerged" unmerged
            emit_file "$FAKE_GH/pr-nosha" nosha
          elif [ "$page" = 2 ]; then
            emit_file "$FAKE_GH/pr-page2-merges" merged
          fi
          printf ']'
        } | gh_filter
        exit 0 ;;
    esac ;;
  "auth token") echo "gh auth token" >> "$CALLLOG"; echo tok ;;
  "api user")
    # gh api user -i: the response headers carry the token's scopes;
    # FAKE_GH_SCOPES models them (unset: no scopes header, like a
    # fine-grained token).
    case " $* " in
      *" -i "*) [ -n "$FAKE_GH_SCOPES" ] && printf 'HTTP/2.0 200 OK\r\nX-Oauth-Scopes: %s\r\n\r\n{"login":"me"}\n' "$FAKE_GH_SCOPES" ;;
      *) echo me ;;
    esac ;;
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

// recordPR registers sha as the merge commit of a merged PR on page 1 of the
// fake closed-PR list.
func (w *relWorld) recordPR(sha string) { w.recordPRIn("pr-merges", sha) }

// recordPRPage2 registers sha as a merged PR on page 2 of the fake list.
func (w *relWorld) recordPRPage2(sha string) { w.recordPRIn("pr-page2-merges", sha) }

// recordUnmergedPR registers sha as the test-merge merge_commit_sha of a
// closed-but-unmerged PR (merged_at null): a record the gate must not accept.
func (w *relWorld) recordUnmergedPR(sha string) { w.recordPRIn("pr-unmerged", sha) }

// recordClosedPRWithoutSha adds a closed PR row with a null merge_commit_sha
// (real examples: #130, #64, #29); it still carries updated_at.
func (w *relWorld) recordClosedPRWithoutSha() { w.recordPRIn("pr-nosha", "x") }

func (w *relWorld) recordPRIn(list, sha string) {
	w.t.Helper()
	p := filepath.Join(w.fakeRel, list)
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
		"DOCKER_CONFIG="+dockerCfg, "HOME="+w.root, "FAKE_ORIGIN="+w.origin,
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@e", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@e",
		"RELEASE_XREFS_CMD=echo xrefs >> \"$CALLLOG\"",
		"RELEASE_TEST_CMD=echo tests >> \"$CALLLOG\"",
		"RELEASE_AUDIT_CMD=echo audit >> \"$CALLLOG\"",
		"RELEASE_GH_REPO=silgrid/polyforge",
		// origin is a local bare repo: state which GitHub repo it stands for
		// (release.sh refuses a remote it cannot read as one)
		"RELEASE_REMOTE_IS=silgrid/polyforge",
		"RELEASE_BUILD_CMD=echo build >> \"$CALLLOG\"; mkdir -p artifacts/release-$TAG; echo pf-bin > artifacts/release-$TAG/pf; "+
			"echo pf-arm64-bin > artifacts/release-$TAG/pf-darwin-arm64; "+
			"echo pf-amd64-bin > artifacts/release-$TAG/pf-darwin-amd64",
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
	order := []string{"xrefs", "tests", "audit", "build", "docker build", "npm ci", "npm run build", "npm version", "npm pack", "git push", "docker push", "docker inspect", "gh release create"}
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
	if !strings.Contains(strings.Join(calls, "\n"), "docker build -f deploy/Dockerfile -t ghcr.io/silgrid/polyforge-aihub:v1.2.1") {
		t.Fatalf("image build line wrong: %v", calls)
	}
	// the image is built from the linux pf binary alone
	// (deploy/Dockerfile copies pf and runs `pf aihub`)
	if relIndex(calls, "docker build context: pf ") < 0 {
		t.Fatalf("image build context must carry exactly the linux pf binary: %v", calls)
	}
	if got := w.originTags(); !strings.Contains(got, "v1.2.1") {
		t.Fatalf("v1.2.1 not on origin: %q", got)
	}
	// the pi-web bundle is built with the tag's version and the
	// tag's version (without v) is stamped into the packed metadata.
	for _, want := range []string{"npm run build PI_WEB_RELEASE_VERSION=v1.2.1", "npm version 1.2.1 --no-git-tag-version"} {
		if relIndex(calls, want) < 0 {
			t.Fatalf("pi-web pack stage lacks %q: %v", want, calls)
		}
	}
	if st := relGit(t, w.work, "status", "--porcelain", "--", "pi-web"); st != "" {
		t.Fatalf("the release touched the worktree's pi-web/ (the pi-web pack must run in the staging export): %q", st)
	}
	if got := relGit(t, w.work, "show", "HEAD:pi-web/package.json"); !strings.Contains(got, "0.2.0-dev.0") {
		t.Fatalf("main's pi-web/package.json must keep its development value: %q", got)
	}
	rel := filepath.Join(w.fakeRel, "v1.2.1")
	relFiles := []string{"pf-linux-amd64", "pf-darwin-arm64", "pf-darwin-amd64", "aihub-image.digest", "silgrid-pi-web-v1.2.1.tgz", "SHA256SUMS", "release-manifest.json", ".body"}
	for _, f := range relFiles {
		if _, err := os.Stat(filepath.Join(rel, f)); err != nil {
			t.Fatalf("release missing %s: %v", f, err)
		}
	}
	if ents, _ := os.ReadDir(rel); len(ents) != len(relFiles) {
		t.Fatalf("release has %d files, want exactly %v (no aihub binary asset,; the pi-web tarball and the manifest,)", len(ents), relFiles)
	}
	for n, want := range map[string]string{"pf-linux-amd64": "pf-bin", "pf-darwin-arm64": "pf-arm64-bin", "pf-darwin-amd64": "pf-amd64-bin"} {
		if got, _ := os.ReadFile(filepath.Join(rel, n)); strings.TrimSpace(string(got)) != want {
			t.Fatalf("%s content = %q, want %q (the staged darwin binary, copied under its asset name)", n, got, want)
		}
	}
	dg, _ := os.ReadFile(filepath.Join(rel, "aihub-image.digest"))
	if strings.TrimSpace(string(dg)) != relFakeDigest {
		t.Fatalf("digest file = %q", dg)
	}
	sums, _ := os.ReadFile(filepath.Join(rel, "SHA256SUMS"))
	if n := strings.Count(string(sums), "\n"); n != 5 || strings.Contains(string(sums), "SHA256SUMS") || strings.Contains(string(sums), "release-manifest") {
		t.Fatalf("SHA256SUMS should cover exactly the 5 payload files: %q", sums)
	}
	for _, n := range []string{"pf-linux-amd64", "pf-darwin-arm64", "pf-darwin-amd64", "aihub-image.digest", "silgrid-pi-web-v1.2.1.tgz"} {
		if !strings.Contains(string(sums), "  "+n+"\n") {
			t.Fatalf("SHA256SUMS lacks %s: %q", n, sums)
		}
	}
	head := relGit(t, w.work, "rev-parse", "HEAD")
	tgz := exec.Command("tar", "-xzOf", filepath.Join(rel, "silgrid-pi-web-v1.2.1.tgz"), "package/package.json")
	if pkg, err := tgz.Output(); err != nil || !strings.Contains(string(pkg), `"version": "1.2.1"`) {
		t.Fatalf("the packed pi-web package must carry the tag's version 1.2.1: %v %q", err, pkg)
	}
	if man, _ := os.ReadFile(filepath.Join(rel, "release-manifest.json")); string(man) != relManifest("v1.2.1", head, string(sums)) {
		t.Fatalf("release-manifest.json = %q, want the tag, the commit and every SHA256SUMS payload", man)
	}
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
	build := "echo build >> \"$CALLLOG\"; mkdir -p artifacts/release-$TAG; for n in pf pf-darwin-arm64 pf-darwin-amd64; do echo bin > artifacts/release-$TAG/$n; done; " +
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

// TestReleaseScriptRefusesAnImageTagAlreadyInThePackage: a fresh release
// (no resume) whose image tag already exists in the package - another
// build published it, e.g. another line pushing to the same package -
// refuses before any gate, build, claim or push, naming the tag; the same
// world with the tag absent releases.
func TestReleaseScriptRefusesAnImageTagAlreadyInThePackage(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	out, err := w.run([]string{"FAKE_REGISTRY_HAS=v1.2.1"})
	if err == nil {
		t.Fatalf("expected the release to refuse an image tag already in the package\n%s", out)
	}
	for _, want := range []string{"ghcr.io/silgrid/polyforge-aihub:v1.2.1 already exists in the registry", "no release of origin to resume", "RELEASE_IMAGE", "no version claimed"} {
		if !strings.Contains(out, want) {
			t.Errorf("refusal lacks %q\n%s", want, out)
		}
	}
	if c := w.calls(); len(c) != 0 {
		t.Fatalf("work happened despite the refusal: %v", c)
	}
	if got := w.originTags(); strings.Contains(got, "v1.2.1") {
		t.Fatalf("tag claimed despite the refusal: %q", got)
	}
	out, err = w.run(nil)
	if err != nil || !strings.Contains(out, "image ghcr.io/silgrid/polyforge-aihub:v1.2.1 is not in the registry yet") || !strings.Contains(out, "version=v1.2.1") {
		t.Fatalf("with the tag absent the release goes on: %v\n%s", err, out)
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

// TestReleaseScriptRefusesAHeadWithTwoReleaseTags: a HEAD already carrying ONE
// release tag on origin is resumed (test/w372_release_lane_test.go);
// two release tags on the same commit leave no single version to resume, so
// the run refuses before anything runs and cuts no third one.
func TestReleaseScriptRefusesAHeadWithTwoReleaseTags(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	relGit(t, w.work, "tag", "-a", "-m", "v1.3.0", "v1.3.0")
	relGit(t, w.work, "tag", "v1.3.1")
	relGit(t, w.work, "push", "-q", "origin", "v1.3.0", "v1.3.1")
	out, err := w.run(nil)
	if err == nil {
		t.Fatalf("expected a refusal\n%s", out)
	}
	for _, want := range []string{"more than one release tag", "v1.3.0 v1.3.1", "nothing published"} {
		if !strings.Contains(out, want) {
			t.Errorf("message lacks %q\n%s", want, out)
		}
	}
	if c := w.calls(); len(c) != 0 {
		t.Fatalf("something ran before the refusal: %v", c)
	}
	if got := w.originTags(); strings.Contains(got, "v1.3.2") {
		t.Fatalf("a third version was cut: %q", got)
	}
}

func TestReleaseScriptPostFailTextIsAccurate(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	out, err := w.run([]string{"FAKE_PUSH_FAIL=1"})
	if err == nil {
		t.Fatalf("expected failure\n%s", out)
	}
	// a failure after the claim no longer burns the version -
	// the text says so and how to resume it.
	for _, want := range []string{"FAILED AFTER v1.2.1 WAS CLAIMED", "failing step: docker push", "NOT\nburned", "resumes it", "immutable", "artifacts/release-v1.2.1/pf and the two darwin", "artifacts/release-v1.2.1/pf-darwin-{arm64,amd64}", "staging directory"} {
		if !strings.Contains(out, want) {
			t.Errorf("post-fail text lacks %q\n%s", want, out)
		}
	}
	for _, bad := range []string{"delete the tag by hand", "BURNED", "Do not retry"} {
		if strings.Contains(out, bad) {
			t.Errorf("post-fail text must not say %q\n%s", bad, out)
		}
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
	// (the repro test pins the no-side-effect contract).
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
	// the committed notes, byte for byte, then the test gate's record;
	// the committed file itself is not touched
	if !strings.HasPrefix(string(body), "# hand written\n\n## Test gate\n\n") || !strings.Contains(string(body), "Ran RELEASE_TEST_CMD (gate `") {
		t.Fatalf("notes = %q", body)
	}
	if got := relGit(t, w.work, "status", "--porcelain"); got != "" {
		t.Fatalf("the release changed the worktree: %q", got)
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

// goodRelease is a complete release of tag: the 5 payloads, SHA256SUMS and
// the manifest naming the tag's commit on origin (empty when origin has no
// such tag).
func (w *relWorld) goodRelease(tag string) map[string]string {
	w.t.Helper()
	tgz := "silgrid-pi-web-" + tag + ".tgz"
	f := map[string]string{
		"pf-linux-amd64": "P", "pf-darwin-arm64": "Pa", "pf-darwin-amd64": "Px",
		"aihub-image.digest": relFakeDigest + "\n",
		tgz:                  relPiwebTgz(strings.TrimPrefix(tag, "v")),
	}
	var sums strings.Builder
	for _, n := range []string{"pf-linux-amd64", "pf-darwin-arm64", "pf-darwin-amd64", "aihub-image.digest", tgz} {
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256([]byte(f[n])), n)
	}
	f["SHA256SUMS"] = sums.String()
	commit, _ := exec.Command("git", "-C", w.origin, "rev-list", "-n", "1", tag).Output()
	f["release-manifest.json"] = relManifest(tag, strings.TrimSpace(string(commit)), sums.String())
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
		{name: "aihub binary asset (pre-consolidation matrix)", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			mut: func(f map[string]string) { f["aihub-linux-amd64"] = "A" }, fail: "asset set"},
		{name: "missing asset", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			mut: func(f map[string]string) { delete(f, "pf-linux-amd64") }, fail: "asset set"},
		// the pi-web tarball and the manifest are part of the set
		{name: "missing pi-web tarball", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			mut: func(f map[string]string) { delete(f, "silgrid-pi-web-v1.3.0.tgz") }, fail: "asset set"},
		{name: "missing manifest", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			mut: func(f map[string]string) { delete(f, "release-manifest.json") }, fail: "asset set"},
		{name: "checksum mismatch (pi-web tarball)", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			mut: func(f map[string]string) { f["silgrid-pi-web-v1.3.0.tgz"] = "tampered" }, fail: "sha256sum"},
		{name: "manifest names another commit", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			mut: func(f map[string]string) {
				f["release-manifest.json"] = relManifest("v1.3.0", strings.Repeat("0", 40), f["SHA256SUMS"])
			}, fail: "release-manifest.json"},
		{name: "manifest misses a payload", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			mut: func(f map[string]string) {
				lines := strings.SplitAfter(f["SHA256SUMS"], "\n")
				f["release-manifest.json"] = relManifest("v1.3.0", "x", strings.Join(lines[:3], ""))
			}, fail: "release-manifest.json"},
		{name: "pi-web tarball carries another version", tag: "v1.3.0", origin: []string{"v1.2.0", "v1.3.0"},
			mut: func(f map[string]string) {
				f["silgrid-pi-web-v1.3.0.tgz"] = relPiwebTgz("0.2.0-dev.0")
				relRepin(f, "v1.3.0")
			}, fail: "pi-web package version"},
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
			f := w.goodRelease(c.tag)
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
		{"direct commit on main", func(w *relWorld) string { return w.commitOnMain("direct", "direct push") }, nil, "no merged PR"},
		{"local merge never a PR", func(w *relWorld) string { return w.mergeOnMain("local", "l\n", "local merge", false) }, nil, "no merged PR"},
		{"closed unmerged PR test merge", func(w *relWorld) string {
			sha := w.commitOnMain("unmerged", "test merge of a closed unmerged PR")
			w.recordUnmergedPR(sha)
			return sha
		}, nil, "no merged PR has it as its merge commit"},
		// a failing gh api is not this refusal: it is a retryable lookup
		// failure (TestW372PROnlyLookupFailureIsRetryableNotAMissingPR).
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

// TestReleaseScriptAcceptsAMergedPRSquashCommit: the association comes from the
// merged-PR record, so a PR merged as a one-parent squash commit passes the
// gate exactly like a merge commit; a one-parent commit with no PR record still
// refuses (TestReleaseScriptRefusesCommitsNotFromAMergedPR).
func TestReleaseScriptAcceptsAMergedPRSquashCommit(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	squashed := w.commitOnMain("squashed", "squashed PR")
	w.recordPR(squashed)
	out, err := w.run(nil)
	if err != nil {
		t.Fatalf("a PR merged as a squash commit must pass the PR-only gate: %v\n%s", err, out)
	}
	if !strings.Contains(out, "version=v1.2.1") {
		t.Fatalf("release did not publish v1.2.1:\n%s", out)
	}
	if strings.Contains(out, "without a merged PR") {
		t.Fatalf("the gate reports no merged PR for %s:\n%s", squashed, out)
	}
	if got := w.originTags(); !strings.Contains(got, "v1.2.1") {
		t.Fatalf("origin tags = %q: want v1.2.1", got)
	}
}

// TestReleaseScriptPagesPastANullMergeCommitSha: a page whose last row has a
// null merge_commit_sha (real examples: #130, #64, #29) must still yield that
// page's oldest updated_at, so paging continues and a legitimate merged PR on
// page 2 is found. A "" sha would print an empty sha field, awk would read the
// timestamp as field 1, and paging would stop early - refusing this release.
func TestReleaseScriptPagesPastANullMergeCommitSha(t *testing.T) {
	w := newRelWorld(t, []string{"v1.2.0"}, nil)
	page2 := w.mergeOnMain("third", "third\n", "third PR", false)
	w.recordPRPage2(page2)
	w.recordClosedPRWithoutSha()
	out, err := w.run(nil)
	if err != nil {
		t.Fatalf("a page ending with a null merge_commit_sha must not stop paging: %v\n%s", err, out)
	}
	if !strings.Contains(out, "version=v1.2.1") {
		t.Fatalf("release did not publish v1.2.1:\n%s", out)
	}
	if strings.Contains(out, "without a merged PR") {
		t.Fatalf("the gate reports no merged PR for page-2 merge %s:\n%s", page2, out)
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
		{name: "a direct commit", direct: true, fail: "no merged PR"},
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
			w.publishFake("v1.3.0", w.goodRelease("v1.3.0"))
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

// ---- named commits for one release (RELEASE_ALLOW_COMMITS)

// TestReleaseScriptAllowsNamedCommitsForOneRun: a first-parent commit
// without a merged PR passes the PR-only check when this run names its full
// id in RELEASE_ALLOW_COMMITS, with an audit line and a "PR-only
// exceptions" section in the release notes; every other commit is still
// checked. The list fails closed: a short id refuses before any work, and an
// id that is no first-parent commit of the range refuses too.
func TestReleaseScriptAllowsNamedCommitsForOneRun(t *testing.T) {
	t.Run("a named direct commit releases, audited and recorded", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		bad := w.commitOnMain("direct", "direct push")
		w.landPR("later", "a later PR")
		out, err := w.run([]string{"RELEASE_ALLOW_COMMITS=" + bad})
		if err != nil || !strings.Contains(out, "version=v1.2.1") {
			t.Fatalf("a named commit must let the release through: %v\n%s", err, out)
		}
		if !strings.Contains(out, "PASS pr-only: allowed: "+bad+" (RELEASE_ALLOW_COMMITS)") {
			t.Errorf("no audit line for the named commit\n%s", out)
		}
		body, rerr := os.ReadFile(filepath.Join(w.fakeRel, "v1.2.1", ".body"))
		if rerr != nil || !strings.Contains(string(body), "## PR-only exceptions") || !strings.Contains(string(body), "- "+bad+" direct push") {
			t.Fatalf("the release notes must record the named commit: %v\n%s", rerr, body)
		}
	})
	t.Run("another unnamed direct commit still refuses", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		named := w.commitOnMain("direct", "direct push")
		other := w.commitOnMain("other", "another direct push")
		out, err := w.run([]string{"RELEASE_ALLOW_COMMITS=" + named})
		if err == nil || !strings.Contains(out, "without a merged PR") || !strings.Contains(out, other) || w.originTags() != "v1.2.0" {
			t.Fatalf("the unnamed commit must still refuse: %v\n%s", err, out)
		}
	})
	t.Run("a short id refuses before any work", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		bad := w.commitOnMain("direct", "direct push")
		out, err := w.run([]string{"RELEASE_ALLOW_COMMITS=" + bad[:12]})
		if err == nil || !strings.Contains(out, "is not a full commit id") || len(w.calls()) != 0 {
			t.Fatalf("a short id must refuse before any work: %v %v\n%s", err, w.calls(), out)
		}
	})
	t.Run("an id outside the range refuses", func(t *testing.T) {
		w := newRelWorld(t, []string{"v1.2.0"}, nil)
		before := relGit(t, w.work, "rev-parse", "v1.2.0^{commit}")
		out, err := w.run([]string{"RELEASE_ALLOW_COMMITS=" + before})
		if err == nil || !strings.Contains(out, "no first-parent commit of") || !strings.Contains(out, before) || w.originTags() != "v1.2.0" {
			t.Fatalf("a named commit outside the range must refuse: %v\n%s", err, out)
		}
	})
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
	}, "no merged PR has it as its merge commit"},
	{"other identity direct commit", func(w *relWorld) (string, string) {
		return "", w.commitOnMainAs("someone@example.com", "other", "other direct")
	}, "no merged PR has it as its merge commit"},
	{"mixed range", func(w *relWorld) (string, string) {
		o := w.commitOnMainAs(ownerDirectEmail, "owner", "owner direct")
		b := w.commitOnMainAs(agentEmail, "agent", "agent direct")
		return o, b
	}, "no merged PR has it as its merge commit"},
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
			w.publishFake("v1.3.0", w.goodRelease("v1.3.0"))
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

// the pi-web package is packed before the tag push, so a
// failing npm stage stops the release with nothing claimed or published.
func TestReleaseScriptPiwebPackFailureStopsBeforeTag(t *testing.T) {
	for _, stage := range []string{"ci", "run", "pack"} {
		t.Run(stage, func(t *testing.T) {
			w := newRelWorld(t, []string{"v1.2.0"}, nil)
			out, err := w.run([]string{"FAKE_NPM_FAIL=" + stage})
			if err == nil || !strings.Contains(out, "pi-web package failed; nothing published") {
				t.Fatalf("expected the pi-web stage refusal: %v\n%s", err, out)
			}
			calls := w.calls()
			for _, p := range []string{"git push", "docker push", "gh release create"} {
				if relIndex(calls, p) >= 0 {
					t.Fatalf("%q ran after the pi-web stage failed: %v", p, calls)
				}
			}
			if got := w.originTags(); strings.Contains(got, "v1.2.1") {
				t.Fatalf("v1.2.1 was claimed: %q", got)
			}
			if st := relGit(t, w.work, "status", "--porcelain", "--", "pi-web"); st != "" {
				t.Fatalf("the failed pack touched the worktree's pi-web/: %q", st)
			}
		})
	}
}

// the one named version-line cutover. A v2.20.x floor (no
// v0.2.0 on origin yet) releases exactly v0.2.0; once v0.2.0 is on origin
// the floor is the v0.2 line's highest tag, so the retired, numerically
// higher v2.20.x tags never decide a version again; every other floor
// patch-bumps as before. --verify judges a v0.2.x tag inside its line and,
// for v0.2.0 alone, takes the highest v2.20.x tag as the previous release;
// every other older target stays refused.
func TestReleaseScriptVersionLineCutover(t *testing.T) {
	for _, c := range []struct {
		name   string
		origin []string
		want   string
	}{
		{"public v0.1.0 floor cuts over to v0.2.0", []string{"v0.0.1", "v0.1.0"}, "v0.2.0"},
		{"public v0.1 patch floor also cuts over", []string{"v0.1.0", "v0.1.8"}, "v0.2.0"},
		{"v2.20.x floor cuts over to exactly v0.2.0", []string{"v2.19.9", "v2.20.22"}, "v0.2.0"},
		{"after the cutover the v0.2 line is the floor", []string{"v2.20.22", "v0.2.0"}, "v0.2.1"},
		{"patch inside the v0.2 line", []string{"v2.20.22", "v0.2.0", "v0.2.1"}, "v0.2.2"},
		{"any other floor patch-bumps as before", []string{"v2.19.5"}, "v2.19.6"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newRelWorld(t, c.origin, nil)
			out, err := w.run(nil)
			if err != nil || !strings.Contains(out, "version="+c.want+"\n") {
				t.Fatalf("want version %s: %v\n%s", c.want, err, out)
			}
			vout, verr := w.run(nil, "--verify", c.want)
			if verr != nil || strings.Contains(vout, "FAIL") {
				t.Fatalf("--verify %s of the fresh release must pass: %v\n%s", c.want, verr, vout)
			}
		})
	}
	t.Run("the cutover tag's previous release is the highest v2.20.x tag", func(t *testing.T) {
		w := newRelWorld(t, []string{"v2.19.9", "v2.20.21", "v2.20.22"}, nil)
		if out, err := w.run(nil); err != nil || !strings.Contains(out, "version=v0.2.0\n") {
			t.Fatalf("%v\n%s", err, out)
		}
		vout, _ := w.run(nil, "--verify", "v0.2.0")
		if !strings.Contains(vout, "PASS monotonic: v0.2.0 > every other tag on origin (highest other v2.20.22)") ||
			!strings.Contains(vout, "since v2.20.22 came through a merged PR") {
			t.Fatalf("the cutover's verify must judge against v2.20.22:\n%s", vout)
		}
	})
	for _, c := range []struct {
		name   string
		origin []string
		tag    string
	}{
		{"v0.2.0 is refused once v0.2.2 exists", []string{"v2.20.22", "v0.2.0", "v0.2.2"}, "v0.2.0"},
		{"v0.1.9 below v2.20.22 is refused", []string{"v2.20.22", "v0.1.9"}, "v0.1.9"},
	} {
		t.Run(c.name, func(t *testing.T) {
			w := newRelWorld(t, c.origin, nil)
			w.publishFake(c.tag, w.goodRelease(c.tag))
			vout, verr := w.run(nil, "--verify", c.tag)
			if verr == nil || !strings.Contains(vout, "FAIL monotonic: "+c.tag+" is not strictly greater") {
				t.Fatalf("an older target outside the named cutover must fail --verify: %v\n%s", verr, vout)
			}
		})
	}
}

// relSuffixWorld is the internal line of the replacement model (
// AC2): origin (internal) carries the old v0.2.0 … v0.2.12 tags on its old
// history; upstream.git (the public repository) carries its own v0.2.0 on
// another commit; internal main is the replacement commit (the public v0.2.0
// tree, first parent the public commit, second the old internal main) and
// then one internal-layer change landed through a (fake) merged PR - the
// optional .polyforge/release.conf when conf is non-empty.
type relSuffixWorld struct {
	*relWorld
	upstream string // the public bare repository
	pub      string // a clone of it: where the public side commits and tags
	oldTip   string // the commit every old v0.2.x tag points at
}

func relOldLineTags() []string {
	var tags []string
	for i := 0; i <= 12; i++ {
		tags = append(tags, fmt.Sprintf("v0.2.%d", i))
	}
	return tags
}

func newRelSuffixWorld(t *testing.T, conf func(upstream string) string) *relSuffixWorld {
	t.Helper()
	w := &relSuffixWorld{relWorld: newRelWorld(t, relOldLineTags(), nil)}
	w.oldTip = relGit(t, w.work, "rev-list", "-n", "1", "v0.2.0")
	oldMain := relGit(t, w.work, "rev-parse", "HEAD")
	w.upstream = filepath.Join(w.root, "upstream.git")
	w.pub = filepath.Join(w.root, "pub")
	relGit(t, w.root, "init", "-q", "--bare", "-b", "main", w.upstream)
	// the public v0.2.0: the same release tooling, its own history
	relGit(t, w.work, "checkout", "-q", "--orphan", "public-tmp")
	relWrite(t, filepath.Join(w.work, "README"), "public v0.2.0\n", 0o644)
	relGit(t, w.work, "add", "-A")
	relGit(t, w.work, "commit", "-q", "-m", "public v0.2.0")
	relGit(t, w.work, "push", "-q", w.upstream, "public-tmp:main")
	relGit(t, w.work, "checkout", "-q", "main")
	relGit(t, w.work, "branch", "-q", "-D", "public-tmp")
	relGit(t, w.root, "clone", "-q", w.upstream, w.pub)
	relGit(t, w.pub, "tag", "-a", "-m", "v0.2.0", "v0.2.0")
	relGit(t, w.pub, "push", "-q", "origin", "v0.2.0")
	pub0 := relGit(t, w.pub, "rev-parse", "HEAD")
	// public commits came through public PRs
	w.recordPR(pub0)
	// the replacement commit: the public tree, two parents; it lands like any
	// change on main (a merged PR here)
	relGit(t, w.work, "fetch", "-q", "--no-tags", w.upstream, "main")
	repl := relGit(t, w.work, "commit-tree", pub0+"^{tree}", "-p", pub0, "-p", oldMain, "-m", "replace main with the public v0.2.0 tree")
	relGit(t, w.work, "merge", "-q", "--ff-only", repl)
	relGit(t, w.work, "push", "-q", "origin", "main")
	w.recordPR(repl)
	if conf != nil {
		w.mergeOnMain(".polyforge/release.conf", conf(w.upstream), "internal layer: release suffix", true)
	} else {
		w.landPR("internal-layer.txt", "internal layer")
	}
	return w
}

// absorbPublic tags a new public commit as tag on upstream and merges it into
// internal main through a (fake) merged PR.
func (w *relSuffixWorld) absorbPublic(tag string) {
	w.t.Helper()
	relWrite(w.t, filepath.Join(w.pub, "public-"+tag), tag+"\n", 0o644)
	relGit(w.t, w.pub, "add", "-A")
	relGit(w.t, w.pub, "commit", "-q", "-m", "public "+tag)
	relGit(w.t, w.pub, "tag", "-a", "-m", tag, tag)
	relGit(w.t, w.pub, "push", "-q", "origin", "main", tag)
	relGit(w.t, w.work, "fetch", "-q", "--no-tags", w.upstream, "main")
	relGit(w.t, w.work, "merge", "-q", "--no-ff", "-m", "Merge pull request from public "+tag, "FETCH_HEAD")
	relGit(w.t, w.work, "push", "-q", "origin", "main")
	w.recordPR(relGit(w.t, w.work, "rev-parse", "HEAD"))
}

// tagOn pushes an annotated tag on rev to origin.
func (w *relSuffixWorld) tagOn(tag, rev string) {
	w.t.Helper()
	relGit(w.t, w.work, "tag", "-a", "-m", tag, tag, rev)
	relGit(w.t, w.work, "push", "-q", "origin", tag)
}

// assertOldTagsUntouched: AC2 row 4 - the old same-named tags point at the
// old internal commit locally and on origin after the run, and the run
// reported no tag conflict.
func (w *relSuffixWorld) assertOldTagsUntouched(out string) {
	w.t.Helper()
	for _, bad := range []string{"already exists", "would clobber", "[rejected]"} {
		if strings.Contains(out, bad) {
			w.t.Errorf("AC2 row 4: the run reports a tag conflict (%q):\n%s", bad, out)
		}
	}
	for _, tg := range relOldLineTags() {
		for _, dir := range []string{w.work, w.origin} {
			if got := relGit(w.t, dir, "rev-list", "-n", "1", tg); got != w.oldTip {
				w.t.Errorf("AC2 row 4: %s in %s moved to %s (want the old internal %s)", tg, dir, got, w.oldTip)
			}
		}
	}
}

func relSuffixConf(name string) func(string) string {
	return func(upstream string) string { return "suffix=" + name + "\nupstream=" + upstream + "\n" }
}

// TestReleaseScriptSuffixVersion (AC2): with the optional
// .polyforge/release.conf (suffix=<name>, upstream=<public repository>) the
// release cuts vX.Y.Z-<name>.N: X.Y.Z is the highest vX.Y.Z tag of upstream
// whose commit is in HEAD's history, N the next number on origin for that
// base. The suffix name comes from the configuration only (the fixtures use
// the neutral `fork`); old same-named v0.2.x tags on other commits neither
// change the result nor conflict. Without the file the release is what it
// always was. Each case names the AC2 table row it carries.
func TestReleaseScriptSuffixVersion(t *testing.T) {
	// AC2 row 1: HEAD contains the public v0.2.0, no suffixed tag yet.
	// Case 7 (resume/verify, Q6): the fresh release verifies.
	t.Run("row 1: first suffixed release of the public v0.2.0", func(t *testing.T) {
		w := newRelSuffixWorld(t, relSuffixConf("fork"))
		head := relGit(t, w.work, "rev-parse", "HEAD")
		out, err := w.run(nil)
		if err != nil || !strings.Contains(out, "version=v0.2.0-fork.1\n") {
			t.Fatalf("want version v0.2.0-fork.1: %v\n%s", err, out)
		}
		if got := relGit(t, w.origin, "rev-list", "-n", "1", "v0.2.0-fork.1"); got != head {
			t.Fatalf("v0.2.0-fork.1 on origin points at %s, want HEAD %s", got, head)
		}
		w.assertOldTagsUntouched(out)
		vout, verr := w.run(nil, "--verify", "v0.2.0-fork.1")
		if verr != nil || strings.Contains(vout, "FAIL") ||
			!strings.Contains(vout, "since v0.2.0 came through a merged PR") {
			t.Fatalf("case 7: --verify v0.2.0-fork.1 must pass against the public v0.2.0: %v\n%s", verr, vout)
		}
	})

	// AC2 row 2: v0.2.0-fork.3 already on origin.
	t.Run("row 2: the next number of the same base", func(t *testing.T) {
		w := newRelSuffixWorld(t, relSuffixConf("fork"))
		w.tagOn("v0.2.0-fork.3", "HEAD^1")
		out, err := w.run(nil)
		if err != nil || !strings.Contains(out, "version=v0.2.0-fork.4\n") {
			t.Fatalf("want version v0.2.0-fork.4: %v\n%s", err, out)
		}
		w.assertOldTagsUntouched(out)
	})

	// AC2 row 3: HEAD absorbed the public v0.2.1, highest is v0.2.0-fork.4.
	t.Run("row 3: an absorbed public release opens its base at 1", func(t *testing.T) {
		w := newRelSuffixWorld(t, relSuffixConf("fork"))
		w.tagOn("v0.2.0-fork.4", "HEAD")
		w.absorbPublic("v0.2.1")
		out, err := w.run(nil)
		if err != nil || !strings.Contains(out, "version=v0.2.1-fork.1\n") {
			t.Fatalf("want version v0.2.1-fork.1: %v\n%s", err, out)
		}
		w.assertOldTagsUntouched(out)
		vout, verr := w.run(nil, "--verify", "v0.2.1-fork.1")
		if verr != nil || strings.Contains(vout, "FAIL") ||
			!strings.Contains(vout, "since v0.2.0-fork.4 came through a merged PR") {
			t.Fatalf("case 7: --verify v0.2.1-fork.1 must pass against v0.2.0-fork.4: %v\n%s", verr, vout)
		}
	})

	// AC2 "没配置时现有行为不变": the same repository, old tags included,
	// without the file patches the old line's floor as before.
	t.Run("case 5: no configuration keeps the plain version", func(t *testing.T) {
		w := newRelSuffixWorld(t, nil)
		out, err := w.run(nil)
		if err != nil || !strings.Contains(out, "version=v0.2.13\n") {
			t.Fatalf("want version v0.2.13: %v\n%s", err, out)
		}
	})

	// Case 6 (Q6): a HEAD already carrying its suffixed tag is
	// resumed, never released as a second version.
	t.Run("case 6: a claimed suffixed version is resumed", func(t *testing.T) {
		w := newRelSuffixWorld(t, relSuffixConf("fork"))
		if out, err := w.run([]string{"FAKE_PUSH_FAIL=1"}); err == nil || !strings.Contains(out, "FAILED AFTER v0.2.0-fork.1 WAS CLAIMED") {
			t.Fatalf("setup: the first run must fail after claiming v0.2.0-fork.1: %v\n%s", err, out)
		}
		out, err := w.run([]string{"FAKE_REGISTRY_MISSING=1"})
		if err != nil || !strings.Contains(out, "resuming v0.2.0-fork.1") || !strings.Contains(out, "version=v0.2.0-fork.1\n") {
			t.Fatalf("the re-run must resume v0.2.0-fork.1: %v\n%s", err, out)
		}
		if got := relGit(t, w.origin, "tag", "-l", "v0.2.0-fork.*"); got != "v0.2.0-fork.1" {
			t.Fatalf("suffixed tags on origin = %q, want only v0.2.0-fork.1", got)
		}
		w.assertOldTagsUntouched(out)
	})

	// Case 8: the name is the configuration's, not the script's.
	t.Run("case 8: another configured name", func(t *testing.T) {
		w := newRelSuffixWorld(t, relSuffixConf("other"))
		out, err := w.run(nil)
		if err != nil || !strings.Contains(out, "version=v0.2.0-other.1\n") {
			t.Fatalf("want version v0.2.0-other.1: %v\n%s", err, out)
		}
	})

	// A name that is not one alphanumeric identifier refuses before any work.
	t.Run("a malformed name refuses", func(t *testing.T) {
		w := newRelSuffixWorld(t, relSuffixConf("fork.1"))
		out, err := w.run(nil)
		if err == nil || !strings.Contains(out, "suffix") {
			t.Fatalf("want a refusal naming the suffix: %v\n%s", err, out)
		}
		if c := w.calls(); len(c) != 0 {
			t.Fatalf("work happened before the refusal: %v", c)
		}
	})
}
