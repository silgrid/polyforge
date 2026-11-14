package stepscripts

// verify_test.go: the light verification — the workflow script
// workflows/_shared/scripts/verify.py (pf-verify's
// successor), run as the verify skill runs it, `python3 verify.py --tier
// light …` — over a capsule fixture with a fake scope engine: a passing
// plan (exit 0, the checks, heads and logs in the result), a failing one
// (exit 1), the tree verified as it stands (no fetch, no merge: origin
// moving after the sync changes nothing; a merge in progress is not
// judged), the invocations it refuses (another tier, no engine, no
// capsule), the container of a repo carrying the Dockerfile with the
// step's labels, and a repo without one running on the host. A fake
// `docker` first on the PATH stands in for the engine (and keeps the
// host's out of every run).

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"
)

// verifyScriptPath is the script, from this package's directory.
var verifyScriptPath = filepath.Join("..", "..", "..", "workflows", "_shared", "scripts", "verify.py")

// verifyDetails is the result's details as verify.py writes them.
type verifyDetails struct {
	Tier struct {
		Requested string   `json:"requested"`
		Effective string   `json:"effective"`
		Reasons   []string `json:"reasons"`
	} `json:"tier"`
	MechanicalVerdict string        `json:"mechanical_verdict"`
	FailReason        string        `json:"fail_reason"`
	Incomplete        string        `json:"incomplete"`
	Checks            []verifyCheck `json:"checks"`
	PreExisting       []string      `json:"pre_existing_failures"`
	Repos             []verifyRepo  `json:"repos"`
	Result            string        `json:"result"`
	Report            string        `json:"report"`
}

// verifyCheck is one check of the details.
type verifyCheck struct {
	ID     string       `json:"id"`
	Repo   string       `json:"repo"`
	Argv   []string     `json:"argv"`
	Cwd    string       `json:"cwd"`
	Exit   *int         `json:"exit"`
	Status string       `json:"status"`
	Detail string       `json:"detail"`
	Log    string       `json:"log"`
	Units  []verifyUnit `json:"units"`
}

// verifyUnit is one unit of a check.
type verifyUnit struct {
	Unit        string `json:"unit"`
	Kind        string `json:"kind"`
	Status      string `json:"status"`
	Flaky       bool   `json:"flaky"`
	PreExisting bool   `json:"pre_existing"`
	Detail      string `json:"detail"`
}

// verifyRepo is one task repo of the details.
type verifyRepo struct {
	Name          string `json:"name"`
	Touched       bool   `json:"touched"`
	Head          string `json:"head"`
	DefaultBranch string `json:"default_branch"`
	Base          string `json:"base"`
	Plan          string `json:"plan"`
	Container     string `json:"container"`
}

// fakeDockerScript is the container engine on the PATH: its calls go to
// $DOCKER_LOG; an image is present once built; a --workdir exec runs here
// under env -i with the image's environment plus the --env it is given.
const fakeDockerScript = `#!/bin/sh
log=${DOCKER_LOG:-/dev/null}
case "$1" in
  version) echo 29.0 ;;
  image) case "$2" in
      ls) [ -e "$log.image" ] && echo sha256:built ;;
      inspect) echo '["PATH=/usr/bin:/bin","HOME=/root"]' ;;
    esac ;;
  build) cat > "$log.dockerfile"; : > "$log.image"; echo "docker build $*" >> "$log" ;;
  run) echo "docker run $*" >> "$log"; echo cid-1 ;;
  rm) echo "docker rm $*" >> "$log" ;;
  ps) echo "docker ps $*" >> "$log" ;;
  exec) shift; wd=""; envf=$(mktemp)
    while [ $# -gt 0 ]; do case "$1" in
      --workdir) wd=$2; shift 2 ;;
      --env) printf '%s\n' "$2" >> "$envf"; shift 2 ;;
      *) break ;;
    esac; done
    shift
    if [ -z "$wd" ]; then
      rm -f "$envf"
      case "$*" in *pf-probe:*) exec "$@" ;; esac
      exit 0
    fi
    echo "docker exec --workdir $wd $*" >> "$log"
    mkdir -p "$log.tmp"
    while IFS= read -r kv; do
      case "$kv" in TMPDIR=*) kv="TMPDIR=$log.tmp/t" ;; esac
      set -- "$kv" "$@"
    done < "$envf"
    rm -f "$envf"
    cd "$wd" || exit 125
    exec env -i PATH=/usr/bin:/bin HOME=/root "$@" ;;
esac
exit 0
`

// verifyWorld is a fixture's fake engine bin directory and its call log.
type verifyWorld struct {
	bin, log string
}

// newVerifyWorld puts the fake docker in a bin directory of its own.
func newVerifyWorld(t *testing.T) *verifyWorld {
	t.Helper()
	dir := t.TempDir()
	w := &verifyWorld{bin: filepath.Join(dir, "bin"), log: filepath.Join(dir, "docker.log")}
	writeFile(t, w.bin, "docker", fakeDockerScript)
	if err := os.Chmod(filepath.Join(w.bin, "docker"), 0o755); err != nil {
		t.Fatal(err)
	}
	return w
}

// calls answers the docker calls logged so far.
func (w *verifyWorld) calls() string {
	raw, _ := os.ReadFile(w.log)
	return string(raw)
}

// run runs verify.py with args under the step environment env (the fake
// docker first on its PATH), answering the exit code and the streams; a
// result file it writes satisfies the published schema.
func (w *verifyWorld) run(t *testing.T, args []string, env ...string) (int, string, string) {
	t.Helper()
	return w.script(t, verifyScriptPath, args, env...)
}

// script runs the workflow script at path with args under the step
// environment env, the fake docker first on its PATH (whatever PATH env
// names), answering the exit code and the streams; a result file it writes
// satisfies the published schema.
func (w *verifyWorld) script(t *testing.T, path string, args []string, env ...string) (int, string, string) {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	if err := exec.Command(py, "-I", "-c", "import sys; sys.exit(sys.version_info < (3, 9))").Run(); err != nil {
		t.Skipf("python3 older than 3.9: %v", err)
	}
	path0 := os.Getenv("PATH")
	full := []string{"HOME=" + os.Getenv("HOME")}
	for _, kv := range env {
		if v, ok := strings.CutPrefix(kv, "PATH="); ok {
			path0 = v
			continue
		}
		full = append(full, kv)
	}
	full = append(full, "PATH="+w.bin+string(os.PathListSeparator)+path0, "DOCKER_LOG="+w.log)
	cmd := exec.Command(py, append([]string{"-B", path}, args...)...)
	cmd.Env = full
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	exit := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("%s did not run: %v", filepath.Base(path), err)
		}
		exit = ee.ExitCode()
	}
	checkWrittenResult(t, args)
	return exit, out.String(), errb.String()
}

// verifyCapsule is a capsule whose repo app has one change on its feature
// branch (no Dockerfile: the bar runs on the host).
func verifyCapsule(t *testing.T) *capsuleFixture {
	t.Helper()
	f := newCapsule(t, map[string]string{"a.txt": "a\n", ".polyforge/verify-scope.conf": "stack=none\n"})
	f.change(t, map[string]string{"a.txt": "a2\n"})
	return f
}

// verifyArgs answers the skill's invocation plus the test's engine and
// capsule.
func verifyArgs(f *capsuleFixture, engine string, extra ...string) []string {
	return append([]string{"--tier", "light", "--capsule", f.dir, "--engine", engine, "--json", "--out", filepath.Join(f.dir, "artifacts", "verify", "result.json")}, extra...)
}

func TestVerifyLightPass(t *testing.T) {
	f := verifyCapsule(t)
	w := newVerifyWorld(t)
	engine := fakeEngine(t, map[string]any{"app": lightReport(cmdOf("unit", "echo ok"), cmdOf("lint", "echo lint-ok"))})
	exit, stdout, errb := w.run(t, verifyArgs(f, engine), "PF_ROUND=r2")
	if exit != 0 {
		t.Fatalf("exit %d\n%s\n%s", exit, stdout, errb)
	}
	res := readResult(t, filepath.Join(f.dir, "artifacts", "verify", "result.json"))
	if res.Command != "verify.py" || res.Verdict != VerdictPass || res.Round != "r2" || res.Heads["app"] != f.head(t) {
		t.Fatalf("result: %+v", res)
	}
	var printed Result
	if err := json.Unmarshal([]byte(strings.TrimSpace(stdout)), &printed); err != nil || printed.Summary != res.Summary {
		t.Fatalf("--json prints the result: %v %q", err, stdout)
	}
	var d verifyDetails
	detailsOf(t, res, &d)
	if d.Tier.Requested != "light" || d.Tier.Effective != "light" || d.MechanicalVerdict != "pass" || d.Incomplete != "" {
		t.Fatalf("tier/verdict: %+v", d)
	}
	var unit *verifyCheck
	for i := range d.Checks {
		if d.Checks[i].ID == "app:unit" {
			unit = &d.Checks[i]
		}
	}
	if unit == nil || unit.Repo != "app" || unit.Status != "pass" || unit.Exit == nil || *unit.Exit != 0 || strings.Join(unit.Argv, " ") != "bash -c echo ok" || unit.Log == "" {
		t.Fatalf("the unit check: %+v (checks %+v)", unit, d.Checks)
	}
	if len(d.Repos) != 1 || d.Repos[0].Name != "app" || !d.Repos[0].Touched || d.Repos[0].Head != f.head(t) || d.Repos[0].DefaultBranch != "main" || d.Repos[0].Base == "" || d.Repos[0].Container != "" {
		t.Fatalf("repos: %+v", d.Repos)
	}
	if d.Result != "artifacts/verify/logs/result.json" || d.Report != "artifacts/verify/logs/report-mechanical.md" {
		t.Fatalf("the run's files: %+v", d)
	}
	if len(res.Logs) < 4 || res.Logs[0] != d.Result || res.Logs[1] != d.Report || res.Logs[2] != unit.Log && res.Logs[3] != unit.Log {
		t.Fatalf("logs: %v", res.Logs)
	}
	for _, rel := range res.Logs {
		if _, err := os.Stat(filepath.Join(f.dir, filepath.FromSlash(rel))); err != nil {
			t.Errorf("listed log %s: %v", rel, err)
		}
	}
	if raw, err := os.ReadFile(filepath.Join(f.dir, "artifacts", "verify", "logs", ".gitignore")); err != nil || !strings.HasSuffix(string(raw), "*\n") {
		t.Fatalf("the logs dir is not kept out of the chain: %v %q", err, raw)
	}
	// the engine was asked --light, never --full
	args, _ := os.ReadFile(filepath.Join(filepath.Dir(engine), "args.log"))
	if !strings.HasPrefix(string(args), "app --json ") || !strings.Contains(string(args), " --light") || !strings.Contains(string(args), " --base ") || strings.Contains(string(args), "--full") {
		t.Fatalf("engine asked: %q", args)
	}
	if !strings.Contains(res.Summary, "light verification passed: ") || !strings.Contains(res.Summary, "repos/app") {
		t.Fatalf("summary: %s", res.Summary)
	}
	// a repo without the Dockerfile runs on the host: no container
	if c := w.calls(); strings.Contains(c, "docker run") || strings.Contains(c, "docker exec") {
		t.Fatalf("a repo without %s ran in a container:\n%s", ".polyforge/Dockerfile.verify", c)
	}
}

func TestVerifyLightFail(t *testing.T) {
	f := verifyCapsule(t)
	w := newVerifyWorld(t)
	engine := fakeEngine(t, map[string]any{"app": lightReport(cmdOf("unit", "echo 'FAIL: boom'; exit 1"), cmdOf("lint", "echo lint-ok"))})
	exit, _, errb := w.run(t, verifyArgs(f, engine))
	if exit != 1 {
		t.Fatalf("exit %d\n%s", exit, errb)
	}
	res := readResult(t, filepath.Join(f.dir, "artifacts", "verify", "result.json"))
	var d verifyDetails
	detailsOf(t, res, &d)
	if res.Verdict != VerdictFail || d.MechanicalVerdict != "fail" || !strings.Contains(res.Summary, "1 check(s) failed: app:unit") {
		t.Fatalf("result %+v\nsummary %s", d, res.Summary)
	}
	for _, c := range d.Checks {
		switch c.ID {
		case "app:unit":
			if c.Status != "fail" || c.Exit == nil || *c.Exit != 1 || len(c.Units) == 0 {
				t.Errorf("the failing check: %+v", c)
			}
		case "app:lint":
			if c.Status != "pass" {
				t.Errorf("the whole plan runs after a failure: %+v", c)
			}
		}
	}
	if res.Heads["app"] != f.head(t) {
		t.Errorf("heads %v", res.Heads)
	}
}

// TestVerifyVerifiesTheTreeAsItStands: origin's default branch moving after
// the sync changes nothing — no fetch (the clone's origin/main stays), no
// merge (HEAD stays), the base being the merge-base the tree carries.
func TestVerifyVerifiesTheTreeAsItStands(t *testing.T) {
	f := verifyCapsule(t)
	w := newVerifyWorld(t)
	base := gitT(t, f.repo(), "rev-parse", "refs/remotes/origin/main")
	head := f.head(t)
	f.advanceOrigin(t, map[string]string{"b.txt": "upstream\n"})
	engine := fakeEngine(t, map[string]any{"app": lightReport(cmdOf("unit", "test ! -f b.txt"))})
	exit, _, errb := w.run(t, verifyArgs(f, engine))
	if exit != 0 {
		t.Fatalf("exit %d (the upstream file must not be merged in)\n%s", exit, errb)
	}
	if got := f.head(t); got != head {
		t.Fatalf("HEAD moved %s → %s: the verification merged", head, got)
	}
	if got := gitT(t, f.repo(), "rev-parse", "refs/remotes/origin/main"); got != base {
		t.Fatalf("origin/main moved %s → %s: the verification fetched", base, got)
	}
	raw, err := os.ReadFile(filepath.Join(f.dir, "artifacts", "verify", "logs", "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var vr struct {
		MergeOutcomes map[string]struct {
			Outcome     string `json:"outcome"`
			Head        string `json:"head"`
			Base        string `json:"base"`
			Baseline    string `json:"baseline"`
			BaselineRef string `json:"baseline_ref"`
		} `json:"merge_outcomes"`
	}
	if err := json.Unmarshal(raw, &vr); err != nil {
		t.Fatal(err)
	}
	m, ok := vr.MergeOutcomes["app"]
	if !ok || m.Outcome != "none" || m.Head != head || m.Base != base || m.Baseline != base || m.BaselineRef != "merge-base" {
		t.Fatalf("merge outcome: %+v", m)
	}
}

func TestVerifyMergeInProgressIsNotJudged(t *testing.T) {
	f := verifyCapsule(t)
	w := newVerifyWorld(t)
	f.advanceOrigin(t, map[string]string{"a.txt": "theirs\n"})
	gitT(t, f.repo(), "fetch", "-q", "origin")
	merge := exec.Command("git", "merge", "--no-edit", "origin/main")
	merge.Dir = f.repo()
	if err := merge.Run(); err == nil {
		t.Fatal("the merge did not conflict: the fixture is wrong")
	}
	if _, err := os.Stat(filepath.Join(f.repo(), ".git", "MERGE_HEAD")); err != nil {
		t.Fatalf("no MERGE_HEAD after the conflict: %v", err)
	}
	engine := fakeEngine(t, map[string]any{"app": lightReport(cmdOf("unit", "echo ok"))})
	exit, _, _ := w.run(t, verifyArgs(f, engine))
	res := readResult(t, filepath.Join(f.dir, "artifacts", "verify", "result.json"))
	if exit != 2 || res.Verdict != VerdictError || !strings.Contains(res.Summary, "a merge is in progress") {
		t.Fatalf("exit %d, result %+v", exit, res)
	}
	var d verifyDetails
	detailsOf(t, res, &d)
	if d.Incomplete == "" || len(d.Checks) != 0 {
		t.Fatalf("details: %+v", d)
	}
}

func TestVerifyRefusesUnusableInvocations(t *testing.T) {
	f := verifyCapsule(t)
	w := newVerifyWorld(t)
	engine := fakeEngine(t, map[string]any{"app": lightReport(cmdOf("unit", "echo ok"))})
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"full tier", []string{"--tier", "full", "--capsule", f.dir, "--engine", engine}, "light tier only"},
		{"no tier", []string{"--capsule", f.dir, "--engine", engine}, "--tier light is required"},
		{"no engine", []string{"--tier", "light", "--capsule", f.dir, "--engine", engine + ".nope"}, "no scope engine at"},
		{"argument", []string{"--tier", "light", "--capsule", f.dir, "--engine", engine, "extra"}, "unrecognized arguments"},
		{"single-dash flag", []string{"-tier", "light", "--capsule", f.dir, "--engine", engine}, "unrecognized arguments"},
		{"bad deadline", []string{"--tier", "light", "--capsule", f.dir, "--engine", engine, "--deadline", "soon"}, "--deadline"},
	} {
		exit, _, errb := w.run(t, c.args)
		if exit != 2 || !strings.Contains(errb, c.want) {
			t.Errorf("%s: exit %d (want 2), stderr %q (want %q)", c.name, exit, errb, c.want)
		}
	}
	// the default engine is the shared tree's scripts/verify-scope (no
	// release-copy fallback): it is not here
	empty := t.TempDir()
	exit, _, errb := w.run(t, []string{"--tier", "light", "--capsule", f.dir}, "PF_SHARED_DIR="+empty)
	if exit != 2 || !strings.Contains(errb, "no scope engine at "+filepath.Join(empty, "scripts", "verify-scope")) {
		t.Errorf("default engine: exit %d, stderr %q", exit, errb)
	}
	writeFile(t, empty, "scripts/verify-scope", "#!/bin/sh\nexit 3\n")
	exit, _, _ = w.run(t, []string{"--tier", "light", "--capsule", f.dir, "--out", filepath.Join(empty, "r.json")}, "PF_SHARED_DIR="+empty)
	if res := readResult(t, filepath.Join(empty, "r.json")); exit == 2 && strings.Contains(res.Summary, "no scope engine") {
		t.Errorf("the shared tree's engine was not taken: %+v", res)
	}
	// not a capsule: not judged, the result written
	out := filepath.Join(empty, "result.json")
	exit, _, _ = w.run(t, []string{"--tier", "light", "--capsule", empty, "--engine", engine, "--out", out})
	res := readResult(t, out)
	if exit != 2 || res.Verdict != VerdictError || !strings.Contains(res.Summary, "not a capsule root") {
		t.Errorf("no capsule: exit %d, result %+v", exit, res)
	}
}

// reKeepAlive reads a bounded keep-alive's lifetime off `docker run`.
var reKeepAlive = regexp.MustCompile(`\+ ([0-9]+) \)\)`)

// TestVerifyContainerCarriesTheStepLabels: a repo whose HEAD carries the
// Dockerfile runs its engine and bar in a container with the scripts'
// owner, a fresh boot, the step's work item, the deadline label and the
// bounded keep-alive, removed when the run ends.
func TestVerifyContainerCarriesTheStepLabels(t *testing.T) {
	f := newCapsule(t, map[string]string{"a.txt": "a\n"})
	f.change(t, map[string]string{"a.txt": "a2\n", ".polyforge/Dockerfile.verify": testDockerfile})
	w := newVerifyWorld(t)
	engine := fakeEngine(t, map[string]any{"app": lightReport(cmdOf("unit", "echo boxed"))})
	exit, _, errb := w.run(t, verifyArgs(f, engine, "--deadline", "20m"), "PF_WI_ID=wi_v")
	if exit != 0 {
		t.Fatalf("exit %d\n%s\n%s", exit, errb, w.calls())
	}
	calls := w.calls()
	var run string
	for _, l := range strings.Split(calls, "\n") {
		if strings.HasPrefix(l, "docker run ") {
			if run != "" {
				t.Fatalf("two containers started:\n%s", calls)
			}
			run = l
		}
	}
	if run == "" {
		t.Fatalf("no container started:\n%s", calls)
	}
	if !regexp.MustCompile(`--label pf\.verify\.boot=[0-9a-f]{16} `).MatchString(run) {
		t.Errorf("docker run carries no fresh boot nonce: %s", run)
	}
	for _, want := range []string{"--label pf.verify.daemon=" + StepOwner, "--label pf.verify.wi=wi_v", "--label pf.verify.repo=app", "--label pf.verify.expires=", " --rm "} {
		if !strings.Contains(run, want) {
			t.Errorf("docker run lacks %q: %s", want, run)
		}
	}
	m := reKeepAlive.FindStringSubmatch(run)
	if m == nil {
		t.Fatalf("no bounded keep-alive: %s", run)
	}
	secs, _ := strconv.Atoi(m[1])
	if want := 20*time.Minute + 5*time.Minute; time.Duration(secs)*time.Second < want-time.Minute || time.Duration(secs)*time.Second > want+time.Minute {
		t.Errorf("keep-alive %ds, want about %s", secs, want)
	}
	if !strings.Contains(calls, "docker exec --workdir "+f.repo()+" ") || !strings.Contains(calls, "echo boxed") {
		t.Errorf("the engine and the bar did not run in the container:\n%s", calls)
	}
	if !strings.Contains(calls, "rm --force cid-1") {
		t.Errorf("the container was not removed:\n%s", calls)
	}
	res := readResult(t, filepath.Join(f.dir, "artifacts", "verify", "result.json"))
	var d verifyDetails
	detailsOf(t, res, &d)
	if len(d.Repos) != 1 || !strings.HasPrefix(d.Repos[0].Container, "pf-verify-wi_v-1-app-") {
		t.Fatalf("the repo's container is not recorded: %+v", d.Repos)
	}
	if len(res.Logs) < 3 || !strings.HasSuffix(res.Logs[2], "image-build.log") {
		t.Fatalf("logs: %v", res.Logs)
	}
}

// TestVerifyRepoWithoutTheDockerfileRunsOnTheHost: in a capsule of two
// repos, the one carrying the Dockerfile runs in its container, the other
// on the host under the step's PATH — where the bar runs is the
// repo's (the Go pf-verify's rule), never forced either way.
func TestVerifyRepoWithoutTheDockerfileRunsOnTheHost(t *testing.T) {
	f := newCapsule(t, map[string]string{"a.txt": "a\n"})
	f.change(t, map[string]string{"a.txt": "a2\n", ".polyforge/Dockerfile.verify": testDockerfile})
	// a second task repo, cloned from the same origin, without the Dockerfile
	plain := filepath.Join(f.dir, "repos", "plain")
	gitT(t, f.dir, "clone", "-q", f.origin, plain)
	gitT(t, plain, "config", "user.email", "sc@example.invalid")
	gitT(t, plain, "config", "user.name", "sc")
	gitT(t, plain, "checkout", "-q", "-b", "feature")
	writeFile(t, plain, "b.txt", "b\n")
	commitAll(t, plain, "plain change")
	raw, _ := json.Marshal(map[string]any{"work_item_id": "wi_sc", "bundle_name": "develop", "repos": map[string]string{"app": "x", "plain": "y"}})
	writeFile(t, f.dir, "capsule.json", string(raw))
	w := newVerifyWorld(t)
	bin := t.TempDir()
	writeFile(t, bin, "host-only-tool", "#!/bin/sh\necho on-the-host\n")
	if err := os.Chmod(filepath.Join(bin, "host-only-tool"), 0o755); err != nil {
		t.Fatal(err)
	}
	engine := fakeEngine(t, map[string]any{
		"app":   lightReport(cmdOf("unit", "echo boxed")),
		"plain": lightReport(cmdOf("unit", "host-only-tool")),
	})
	exit, _, errb := w.run(t, verifyArgs(f, engine), "PATH="+strings.Join([]string{bin, os.Getenv("PATH")}, string(os.PathListSeparator)))
	if exit != 0 {
		t.Fatalf("exit %d\n%s\n%s", exit, errb, w.calls())
	}
	calls := w.calls()
	if strings.Count(calls, "docker run ") != 1 || strings.Contains(calls, "--workdir "+plain) || !strings.Contains(calls, "--workdir "+f.repo()) {
		t.Fatalf("only app runs in a container:\n%s", calls)
	}
	res := readResult(t, filepath.Join(f.dir, "artifacts", "verify", "result.json"))
	var d verifyDetails
	detailsOf(t, res, &d)
	for _, r := range d.Repos {
		if (r.Name == "app") != (r.Container != "") {
			t.Errorf("repo %s container %q", r.Name, r.Container)
		}
	}
	plainUnit := checkByID(&d, "plain:unit")
	if plainUnit == nil {
		t.Fatalf("no plain:unit check: the host repo's bar did not run (checks %+v)", d.Checks)
	}
	if plainUnit.Status != "pass" || plainUnit.Exit == nil || *plainUnit.Exit != 0 {
		t.Errorf("the host repo's bar did not run under the step's PATH: %+v", plainUnit)
	}
}
