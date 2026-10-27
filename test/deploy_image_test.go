package test

// Fake-environment tests for .polyforge/deploy.sh's image-pull swap: the
// deployer machine pulls ghcr.io/silgrid/aihub:<tag>, verifies the repo
// digest against the release's aihub-image.digest, then ships the image to
// the server with docker save | ssh docker load. Everything external is a
// stub on PATH (gh, docker, ssh, scp, curl): the "server" is a local temp
// tree reached through the ssh stub, the registry is a directory. Nothing
// touches a real host, registry or GitHub.

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const fakeDocker = `#!/bin/sh
# fake docker. FAKE_SIDE=server (set by the ssh stub) or local.
side="${FAKE_SIDE:-local}"
S="$FAKE_W/dk-$side"; mkdir -p "$S/img"
echo "$side docker $*" >> "$FAKE_W/log-docker"
key() { printf '%s' "$1" | tr '/:@' '___'; }
fmt=""
case "$1" in
  login) cat >/dev/null; echo "docker login $*" >> "$FAKE_W/log-login"; exit 0 ;;
  pull)
    r="$FAKE_W/registry/$(key "$2")"
    [ -f "$r" ] || { echo "manifest unknown" >&2; exit 1; }
    cp "$r" "$S/img/$(key "$2")"; echo "pulled $2"; exit 0 ;;
  image)
    shift; [ "$1" = inspect ] || exit 1; shift
    if [ "$1" = "-f" ]; then fmt="$2"; shift 2; fi
    f="$S/img/$(key "$1")"; [ -f "$f" ] || exit 1
    id=$(sed -n 1p "$f"); dg=$(sed -n 3p "$f")
    case "$fmt" in
      '{{.Id}}') echo "$id" ;;
      '{{range .RepoDigests}}{{println .}}{{end}}') [ -n "$dg" ] && echo "$dg" ;;
      '{{.RepoDigests}}') echo "[$dg]" ;;
      '{{.Size}}') echo "${FAKE_IMAGE_SIZE:-1000}" ;;
    esac
    exit 0 ;;
  run)
    shift; ep=""; img=""
    while [ $# -gt 0 ]; do case "$1" in --rm) shift ;; --entrypoint) ep="$2"; shift 2 ;; *) img="$1"; shift; break ;; esac; done
    f="$S/img/$(key "$img")"; [ -f "$f" ] || exit 1
    case "$ep" in /aihub) echo "aihub $(sed -n 2p "$f")" ;; git|curl) echo "$ep version 1" ;; esac
    exit 0 ;;
  save) shift; [ "$1" = -o ] && { cp "$S/img/$(key "$3")" "$2.body"; { echo "$3"; cat "$2.body"; } > "$2"; rm -f "$2.body"; }; exit 0 ;;
  load) ref=$(head -1 /dev/stdin >/dev/null 2>&1; true); in="$(cat)"; ref=$(printf '%s\n' "$in" | sed -n 1p)
        # simulate the lock ssh session dying while this image is being loaded
        if [ -n "$FAKE_KILL_LOCK_ON_LOAD" ] && [ "$ref" = "$FAKE_KILL_LOCK_ON_LOAD" ]; then
          lp=$(cat "$FAKE_W/lockpid"); pkill -KILL -P "$lp"; kill -KILL "$lp"; sleep 0.3
        fi
        printf '%s\n' "$in" | sed 1d > "$S/img/$(key "$ref")"; echo "Loaded image: $ref"; exit 0 ;;
  tag) cp "$S/img/$(key "$2")" "$S/img/$(key "$3")"; exit 0 ;;
  rmi) rm -f "$S/img/$(key "$2")"; exit 0 ;;
  images) for f in "$S/img/"*; do [ -e "$f" ] && basename "$f"; done; exit 0 ;;
  inspect)
    c="$S/container"; [ -f "$c" ] || exit 1
    bad=false; [ -n "$FAKE_BAD_IMG" ] && [ "$(cat "$c")" = "$FAKE_BAD_IMG" ] && bad=true
    case "$3" in
      '{{.State.Status}}') echo running ;; '{{.State.Running}}') if $bad; then echo false; else echo true; fi ;;
      '{{.Config.Image}}') cat "$c" ;;
    esac; exit 0 ;;
  exec)
    c="$S/container"; [ -n "$FAKE_BAD_IMG" ] && [ "$(cat "$c")" = "$FAKE_BAD_IMG" ] && exit 1
    f="$S/img/$(key "$(cat "$c")")"; echo "aihub $(sed -n 2p "$f")"; exit 0 ;;
  cp)
    c="$S/container"; v=$(sed -n 2p "$S/img/$(key "$(cat "$c")")")
    dest="${3}"; printf '#!/bin/sh\necho "aihub %s"\n' "$v" > "$dest"; exit 0 ;;
  logs) [ -n "$FAKE_LOGS_FILE" ] && cat "$FAKE_LOGS_FILE"
        # simulate a TERM signal reaching the deploy script right after the swap
        if [ -n "$FAKE_TERM_ON_LOGS" ]; then kill -TERM "$(cat "$FAKE_W/deploypid")"; sleep 1; fi
        exit 0 ;;
  build)
    shift; t=""; while [ $# -gt 0 ]; do case "$1" in -t) t="$2"; shift 2 ;; *) ctx="$1"; shift ;; esac; done
    v=$(sh "$ctx/aihub" -version | awk '{print $NF}')
    printf 'sha256:built%s\n%s\n\n' "$v" "$v" > "$S/img/$(key "$t")"; exit 0 ;;
  compose)
    shift; f=""; verb=""
    while [ $# -gt 0 ]; do case "$1" in
      -f) f="$2"; shift 2 ;;
      -p) shift 2 ;;
      config) [ -f "$f" ]; exit $? ;;
      version) exit 0 ;;
      up|stop) verb="$1"; break ;;
      *) shift ;;
    esac; done
    if [ "$verb" = stop ]; then
      # FAKE_BEFORE_STOP_SQL: python source run against the fixture db just
      # before the old service exits - a "live write" after the step-3
      # snapshot (the declared-delta guard measures across the window).
      [ -z "${FAKE_BEFORE_STOP_SQL:-}" ] || python3 -I -c "$FAKE_BEFORE_STOP_SQL" "$FAKE_W/srv/data/aihub/aihub.db" || exit 1
      # the switch window: the old container exits (graceful drain); the
      # service stays DOWN until the next up - recorded for ordering asserts
      printf '%s\n' "$(sed -n 's/^    image: //p' "$f" | head -1)" > "$S/stopped-from"
      exit 0
    fi
    img=$(sed -n 's/^    image: //p' "$f" | head -1)
    [ -n "$FAKE_FAIL_UP_IMG" ] && [ "$img" = "$FAKE_FAIL_UP_IMG" ] && { echo "fake: compose up failed" >&2; exit 1; }
    # FAKE_AFTER_UP_SQL: python source run against the fixture db after the
    # new container starts - a row loss in the post-SQL part of the window
    # (the declared table's post-swap baseline must still catch it).
    [ -z "${FAKE_AFTER_UP_SQL:-}" ] || python3 -I -c "$FAKE_AFTER_UP_SQL" "$FAKE_W/srv/data/aihub/aihub.db" || exit 1
    printf '%s\n' "$img" > "$S/container"; exit 0 ;;
esac
echo "fake docker: unhandled: $*" >&2; exit 99
`

const fakeSSH = `#!/bin/sh
while [ $# -gt 0 ]; do case "$1" in -o) shift 2 ;; *) break ;; esac; done
shift   # host
export FAKE_SIDE=server
echo "ssh $*" >> "$FAKE_W/log-ssh"
# Unlike a real remote host this fake shares the deployer's filesystem. Refuse
# any argument under the deployer's mktemp work dir ($WORK, pf-deploy.*), so a
# remote program handed a deployer-local path fails here as it would in
# production instead of quietly reading the deployer's copy.
for a in "$@"; do
  case "$a" in
    */pf-deploy.*/*) echo "sh: cannot open $a: No such file or directory" >&2; exit 1 ;;
  esac
done
case "$*" in flock\ -n*) echo $$ > "$FAKE_W/lockpid" ;; esac
# FAKE_SSH_DROP_AFTER=<substring>: the matching remote command RUNS, then the
# connection "drops" (exit 255) - a commit that landed but was never confirmed.
if [ -n "$FAKE_SSH_DROP_AFTER" ]; then
  case "$*" in *"$FAKE_SSH_DROP_AFTER"*)
    sh -c "$*" >/dev/null 2>&1
    echo "Connection to fakehost closed by remote host." >&2; exit 255 ;;
  esac
fi
# FAKE_SQLITE_VERSION: fake the server python3's linked sqlite3 library
# version (the deploy's >= 3.35 preflight gate). Only the gate's program is
# intercepted; every other python3 program runs for real (the SQL really
# executes against the local sqlite fixture).
if [ -n "$FAKE_SQLITE_VERSION" ] && [ "$1" = python3 ] && [ "$2" = - ]; then
  prog="$(cat)"
  case "$prog" in *sqlite3.sqlite_version*) echo "$FAKE_SQLITE_VERSION"; exit 0 ;; esac
  printf '%s\n' "$prog" | exec python3 - "$3" "$4"
fi
if [ -n "$FAKE_MISSING_TOOL" ]; then
  exec sh -c "command() { [ \"\$2\" = $FAKE_MISSING_TOOL ] && return 1; type \"\$2\" >/dev/null 2>&1; }; $*"
fi
exec sh -c "$*"
`

const fakeSCP = `#!/bin/sh
while [ $# -gt 0 ]; do case "$1" in -o) shift 2 ;; *) break ;; esac; done
for last; do :; done
dst="${last#*:}"; n=$#; i=0; srcs=""
for a; do i=$((i+1)); [ $i -lt $n ] && srcs="$srcs $a"; done
echo "scp$srcs -> $dst" >> "$FAKE_W/log-ssh"
cp $srcs "$dst"
`

const fakeGH = `#!/bin/sh
echo "gh $*" >> "$FAKE_W/log-gh"
case "$1 $2" in
  "release download")
    tag="$3"; shift 3; dir=""; pats=""
    while [ $# -gt 0 ]; do case "$1" in -R) shift 2 ;; -D) dir="$2"; shift 2 ;; -p) pats="$pats $2"; shift 2 ;; *) shift ;; esac; done
    [ -d "$FAKE_W/ghrel/$tag" ] || { echo "release not found" >&2; exit 1; }
    for p in $pats; do [ -f "$FAKE_W/ghrel/$tag/$p" ] && cp "$FAKE_W/ghrel/$tag/$p" "$dir/$p"; done; exit 0 ;;
  "release view")
    [ -d "$FAKE_W/ghrel/$3" ] || { echo "release not found" >&2; exit 1; }
    ls "$FAKE_W/ghrel/$3"; exit 0 ;;
  "auth token") echo faketoken; exit 0 ;;
  "api user") echo fakeuser; exit 0 ;;
esac
echo "fake gh: unhandled: $*" >&2; exit 99
`

const fakeCurl = "#!/bin/sh\necho 200\n"

type deployWorld struct {
	t      *testing.T
	w      string
	path   string
	repo   string // the deployer's throwaway git repo: the real deploy.sh + deploy/Dockerfile; release() tags it
	script string // the deploy.sh under test (inside repo)
	env    []string
}

func wf(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

// release lays one fake GitHub release (+ optional GHCR image) into the world.
// digestInRelease is what aihub-image.digest says; registryDigest is what the
// registry's image really carries (differ to simulate tampering).
func (d *deployWorld) release(tag string, withImage bool, digestInRelease, registryDigest string) {
	t := d.t
	rel := filepath.Join(d.w, "ghrel", tag)
	wf(t, filepath.Join(rel, "aihub-linux-amd64"), "bin-aihub-"+tag+"\n", 0o644)
	wf(t, filepath.Join(rel, "pf-linux-amd64"), "bin-pf-"+tag+"\n", 0o644)
	files := []string{"aihub-linux-amd64", "pf-linux-amd64"}
	if withImage {
		wf(t, filepath.Join(rel, "aihub-image.digest"), digestInRelease+"\n", 0o644)
		files = append(files, "aihub-image.digest")
		wf(t, filepath.Join(d.w, "registry", "ghcr.io_silgrid_aihub_"+tag),
			fmt.Sprintf("sha256:id%s\n%s\n%s\n", tag, tag, repoDigestOf(registryDigest)), 0o644)
	}
	cmd := exec.Command("sha256sum", files...)
	cmd.Dir = rel
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	wf(t, filepath.Join(rel, "SHA256SUMS"), string(out), 0o644)
	// a published release is a git tag the deployer has fetched (deploy.sh
	// refuses a target tag missing from the local repo)
	if d.git("tag", "-l", tag) == "" {
		d.git("tag", tag)
	}
}

// git runs one git command in the deployer repo and returns its trimmed output.
func (d *deployWorld) git(args ...string) string {
	d.t.Helper()
	cmd := exec.Command("git", append([]string{"-c", "user.email=deploy@test", "-c", "user.name=deploy"}, args...)...)
	cmd.Dir = d.repo
	out, err := cmd.CombinedOutput()
	if err != nil {
		d.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func newDeployWorld(t *testing.T, oldVer string) *deployWorld {
	t.Helper()
	for _, tool := range []string{"python3", "rsync", "sha256sum", "flock", "mkfifo", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not available", tool)
		}
	}
	w := t.TempDir()
	d := &deployWorld{t: t, w: w}
	bin := filepath.Join(w, "bin")
	wf(t, filepath.Join(bin, "docker"), fakeDocker, 0o755)
	wf(t, filepath.Join(bin, "ssh"), fakeSSH, 0o755)
	wf(t, filepath.Join(bin, "scp"), fakeSCP, 0o755)
	wf(t, filepath.Join(bin, "gh"), fakeGH, 0o755)
	wf(t, filepath.Join(bin, "curl"), fakeCurl, 0o755)
	// the "server": running container on the old image, a compose file, a real sqlite db
	sdk := filepath.Join(w, "dk-server")
	wf(t, filepath.Join(sdk, "img", "aihub-app_aihub"), fmt.Sprintf("sha256:old%s\n%s\n\n", oldVer, oldVer), 0o644)
	wf(t, filepath.Join(sdk, "container"), "aihub-app_aihub\n", 0o644)
	data := filepath.Join(w, "srv", "data", "aihub")
	if err := os.MkdirAll(filepath.Join(data, "memory"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(data, "git"), 0o755); err != nil {
		t.Fatal(err)
	}
	py := `import sqlite3,sys
c=sqlite3.connect(sys.argv[1])
for t in "work_items memories projects users api_keys machines".split(): c.execute("create table %s(id integer)"%t)
c.commit()`
	if out, err := exec.Command("python3", "-I", "-c", py, filepath.Join(data, "aihub.db")).CombinedOutput(); err != nil {
		t.Fatalf("sqlite fixture: %v\n%s", err, out)
	}
	compose := filepath.Join(w, "srv", "compose", "docker-compose.yml")
	wf(t, compose, "services:\n  aihub:\n    image: polyforge/aihub:"+oldVer+"\n    ports:\n      - \"8080:8080\"\n  other:\n    image: busybox\n", 0o644)
	d.path = bin + ":" + os.Getenv("PATH")
	// the deployer machine: a throwaway git repo holding the real deploy.sh
	// and deploy/Dockerfile, so the local-tag lookups are hermetic
	d.repo = filepath.Join(w, "deployer")
	for _, f := range []string{".polyforge/deploy.sh", "deploy/Dockerfile"} {
		b, err := os.ReadFile(filepath.Join("..", f))
		if err != nil {
			t.Fatal(err)
		}
		wf(t, filepath.Join(d.repo, f), string(b), 0o755)
	}
	d.git("init", "-q", "-b", "main")
	d.git("add", "-A")
	d.git("commit", "-q", "-m", "deployer")
	d.script = filepath.Join(d.repo, ".polyforge", "deploy.sh")
	d.env = append(os.Environ(),
		"PATH="+d.path, "FAKE_W="+w, "TMPDIR="+w,
		"HOME="+filepath.Join(w, "home"), "DOCKER_CONFIG="+filepath.Join(w, "home", ".docker"),
		"AIHUB_HOST=fakehost", "AIHUB_DATA="+data,
		"BACKUP_ROOT="+filepath.Join(w, "backups"), "DEPLOY_LOCK_FILE="+filepath.Join(w, "srv", "deploy.lock"), "COMPOSE_FILE="+compose,
		"COMPOSE_PROJECT=aihub-app",
		"WAIT_SECS=4", "POLL_SECS=1", "UP_WAIT_SECS=5")
	return d
}

func (d *deployWorld) run(stdin string, args ...string) (string, error) {
	cmd := exec.Command("sh", append([]string{d.script}, args...)...)
	cmd.Env = d.env
	cmd.Stdin = strings.NewReader(stdin)
	var b bytes.Buffer
	cmd.Stdout, cmd.Stderr = &b, &b
	err := cmd.Run()
	return b.String(), err
}

func (d *deployWorld) logOf(name string) string {
	b, _ := os.ReadFile(filepath.Join(d.w, name))
	return string(b)
}

func (d *deployWorld) serverDockerMutations() []string {
	var m []string
	for _, ln := range strings.Split(d.logOf("log-docker"), "\n") {
		if !strings.HasPrefix(ln, "server docker ") {
			continue
		}
		for _, verb := range []string{" load", " tag ", " rmi ", " build "} {
			if strings.Contains(ln, "server docker"+verb) {
				m = append(m, ln)
			}
		}
		if strings.Contains(ln, " up -d") {
			m = append(m, ln)
		}
	}
	return m
}

// The release digest files hold the canonical bare "sha256:<64 hex>" (the
// format release.sh writes); the fake registry reports it as
// "ghcr.io/silgrid/aihub@<digest>" (imageRepoDigest, shared with
// release_script_test.go).
const digOld = "sha256:1111111111111111111111111111111111111111111111111111111111111111"
const digNew = "sha256:2222222222222222222222222222222222222222222222222222222222222222"

func repoDigestOf(d string) string {
	if d == "" {
		return ""
	}
	return imageRepoDigest(d)
}

func TestReleaseDeployImagePullSwap(t *testing.T) {
	d := newDeployWorld(t, "v2.2.1")
	d.release("v2.2.1", true, digOld, digOld) // rollback target has a GHCR image too
	d.release("v2.3.0", true, digNew, digNew)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("deploy failed: %v\n%s", err, out)
	}
	for _, want := range []string{
		"digest " + digNew + " verified", "rollback image: v2.2.1 has a GHCR image",
		"image polyforge/aihub:v2.3.0 shipped", "container image: polyforge/aihub:v2.3.0",
		"container running, version v2.3.0", "downtime:", "deployed=v2.3.0 previous=v2.2.1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q\n%s", want, out)
		}
	}
	if strings.Contains(out, "docker build") && strings.Contains(d.logOf("log-docker"), "server docker build") {
		t.Errorf("server must not build any image on the GHCR path:\n%s", d.logOf("log-docker"))
	}
	dl := d.logOf("log-docker")
	// order: local pull -> server load -> server tag -> compose up
	iPull := strings.Index(dl, "local docker pull ghcr.io/silgrid/aihub:v2.3.0")
	iLoad := strings.Index(dl, "server docker load")
	iTag := strings.Index(dl, "server docker tag ghcr.io/silgrid/aihub:v2.3.0 polyforge/aihub:v2.3.0")
	iUp := strings.Index(dl, " up -d --no-deps aihub")
	if !(iPull >= 0 && iPull < iLoad && iLoad < iTag && iTag < iUp) {
		t.Errorf("wrong order pull=%d load=%d tag=%d up=%d\n%s", iPull, iLoad, iTag, iUp, dl)
	}
	if !strings.Contains(d.logOf("log-login"), "ghcr.io") {
		t.Errorf("expected a docker login to ghcr.io from the deployer")
	}
	if strings.Contains(d.logOf("log-ssh"), "docker login") || strings.Contains(dl, "server docker login") || strings.Contains(dl, "server docker pull") {
		t.Errorf("the server must hold no credentials / never pull:\n%s", dl)
	}
	cf, _ := os.ReadFile(filepath.Join(d.w, "srv", "compose", "docker-compose.yml"))
	if !strings.Contains(string(cf), "image: polyforge/aihub:v2.3.0") || !strings.Contains(string(cf), "image: busybox") {
		t.Errorf("compose not swapped correctly:\n%s", cf)
	}
	if _, err := os.Stat(filepath.Join(d.w, "dk-server", "img", "polyforge_aihub_v2.2.1")); err != nil {
		t.Errorf("rollback image polyforge/aihub:v2.2.1 missing on the server: %v", err)
	}
}

func TestReleaseDeployLegacyRollbackBuild(t *testing.T) {
	d := newDeployWorld(t, "v2.2.0")
	d.release("v2.2.0", false, "", "") // first time: no GHCR image for the running version
	d.release("v2.3.0", true, digNew, digNew)
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err != nil {
		t.Fatalf("deploy failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "v2.2.0 has no GHCR image") {
		t.Errorf("expected the legacy rollback note\n%s", out)
	}
	if !strings.Contains(d.logOf("log-docker"), "server docker build -t polyforge/aihub:v2.2.0") {
		t.Errorf("legacy rollback image must be built on the server from the saved old binary:\n%s", d.logOf("log-docker"))
	}
	if strings.Contains(d.logOf("log-docker"), "server docker build -t polyforge/aihub:v2.3.0") {
		t.Errorf("the NEW image must never be built on the server")
	}
}

func TestReleaseDeployDigestMismatchStopsBeforeServerChange(t *testing.T) {
	d := newDeployWorld(t, "v2.2.1")
	d.release("v2.2.1", true, digOld, digOld)
	d.release("v2.3.0", true, digNew, digOld) // registry image carries a different digest
	out, err := d.run("", "v2.3.0", "--confirm", "v2.3.0")
	if err == nil {
		t.Fatalf("deploy must fail on a digest mismatch\n%s", out)
	}
	if !strings.Contains(out, "FAILED at step 0") || !strings.Contains(out, "refusing") {
		t.Errorf("expected a step-0 refusal\n%s", out)
	}
	if m := d.serverDockerMutations(); len(m) != 0 {
		t.Errorf("server was changed: %v", m)
	}
	cf, _ := os.ReadFile(filepath.Join(d.w, "srv", "compose", "docker-compose.yml"))
	if !strings.Contains(string(cf), "image: polyforge/aihub:v2.2.1") {
		t.Errorf("compose file changed:\n%s", cf)
	}
}

func TestReleaseDeployNoImageAssetFailsAtPreflight(t *testing.T) {
	d := newDeployWorld(t, "v2.2.1")
	d.release("v2.2.1", true, digOld, digOld)
	d.release("v2.3.0", false, "", "")
	out, err := d.run("", "v2.3.0", "--dry-run")
	if err == nil || !strings.Contains(out, "has no asset aihub-image.digest") {
		t.Fatalf("expected a clean preflight failure, err=%v\n%s", err, out)
	}
	if m := d.serverDockerMutations(); len(m) != 0 {
		t.Errorf("server was changed: %v", m)
	}
}

func TestReleaseDeployDryRunIsReadOnly(t *testing.T) {
	d := newDeployWorld(t, "v2.2.1")
	d.release("v2.2.1", true, digOld, digOld)
	d.release("v2.3.0", true, digNew, digNew)
	out, err := d.run("", "v2.3.0", "--dry-run")
	if err != nil {
		t.Fatalf("dry-run failed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "dry-run: steps 0-1 ran read-only") {
		t.Errorf("no dry-run marker\n%s", out)
	}
	if m := d.serverDockerMutations(); len(m) != 0 {
		t.Errorf("dry-run changed the server: %v", m)
	}
}

// CROSS-SCRIPT CONTRACT: the aihub-image.digest that release.sh really produced
// (the release fake run) passes deploy.sh's preflight (stubbed registry /
// server). Both scripts share relFakeDigest + imageRepoDigest.
func TestReleaseScriptDigestFileFeedsDeployPreflight(t *testing.T) {
	rw := newRelWorld(t, []string{"v1.2.0"}, nil)
	if out, err := rw.run(nil); err != nil {
		t.Fatalf("release failed: %v\n%s", err, out)
	}
	produced := filepath.Join(rw.fakeRel, "v1.2.1")
	if b, _ := os.ReadFile(filepath.Join(produced, "aihub-image.digest")); strings.TrimSpace(string(b)) != relFakeDigest {
		t.Fatalf("release.sh wrote %q, want the canonical %q", b, relFakeDigest)
	}
	d := newDeployWorld(t, "v1.2.0")
	d.release("v1.2.0", true, digOld, digOld)
	for _, f := range []string{
		"aihub-linux-amd64", "pf-linux-amd64",
		"aihub-darwin-arm64", "pf-darwin-arm64", "aihub-darwin-amd64", "pf-darwin-amd64",
		"aihub-image.digest", "SHA256SUMS",
	} {
		b, err := os.ReadFile(filepath.Join(produced, f))
		if err != nil {
			t.Fatal(err)
		}
		wf(t, filepath.Join(d.w, "ghrel", "v1.2.1", f), string(b), 0o644)
	}
	// the registry's image carries the repo digest the release's push produced
	wf(t, filepath.Join(d.w, "registry", "ghcr.io_silgrid_aihub_v1.2.1"),
		fmt.Sprintf("sha256:idv1.2.1\nv1.2.1\n%s\n", imageRepoDigest(relFakeDigest)), 0o644)
	d.git("tag", "v1.2.1") // the deployer has fetched the release's tag
	out, err := d.run("", "v1.2.1", "--dry-run")
	if err != nil {
		t.Fatalf("deploy preflight rejected release.sh's own digest file: %v\n%s", err, out)
	}
	if !strings.Contains(out, "digest "+relFakeDigest+" verified") {
		t.Errorf("digest not verified\n%s", out)
	}
}

func TestDeployRejectsNonCanonicalDigestFile(t *testing.T) {
	for name, content := range map[string]string{
		"repo@digest form": imageRepoDigest(digNew),
		"short hex":        "sha256:2222",
		"upper hex":        "sha256:" + strings.Repeat("A", 64),
	} {
		t.Run(name, func(t *testing.T) {
			d := newDeployWorld(t, "v2.2.1")
			d.release("v2.2.1", true, digOld, digOld)
			d.release("v2.3.0", true, content, digNew)
			out, err := d.run("", "v2.3.0", "--dry-run")
			if err == nil || !strings.Contains(out, "is not sha256:<64 hex>") {
				t.Fatalf("expected a canonical-format refusal: %v\n%s", err, out)
			}
			if m := d.serverDockerMutations(); len(m) != 0 {
				t.Errorf("server was changed: %v", m)
			}
		})
	}
}
