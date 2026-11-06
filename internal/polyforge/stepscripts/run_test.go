package stepscripts

// run_test.go: run.py (workflows/_shared/scripts, the successor of pf-run)
// — one line and a plan in the repo's verification container (the fake
// docker client runs them on the host) or on the host, the result's
// heads, details and logs, the container labels and bounded keep-alive the
// step environment dictates, the owner's expired containers swept at
// start, the exit codes (a failing line stops the plan; a container that
// cannot be had, or a container run without a Dockerfile, is not judged),
// and — against a real docker engine when one answers — a real container
// that is gone when the line ends.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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

// runCapsule is a capsule whose repo app carries the Dockerfile at HEAD.
func runCapsule(t *testing.T) *capsuleFixture {
	t.Helper()
	f := newCapsule(t, map[string]string{"a.txt": "a\n", DockerfileVerify: testDockerfile})
	f.change(t, map[string]string{"a.txt": "a2\n"})
	return f
}

// dockerRun is one `docker run` the fake recorded: its labels and the
// keep-alive's lifetime (seconds).
type dockerRun struct {
	labels   map[string]string
	lifetime int
}

var keepAliveSecs = regexp.MustCompile(`\+ ([0-9]+) \)\)`)

// dockerRuns answers the containers the fake docker started.
func dockerRuns(t *testing.T, state string) []dockerRun {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(state, "runs.log"))
	if err != nil {
		return nil
	}
	var out []dockerRun
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		args := strings.Split(strings.TrimSuffix(line, "\x1f"), "\x1f")
		r := dockerRun{labels: map[string]string{}}
		for i, a := range args {
			if a == "--label" && i+1 < len(args) {
				k, v, _ := strings.Cut(args[i+1], "=")
				r.labels[k] = v
			}
		}
		if m := keepAliveSecs.FindStringSubmatch(args[len(args)-1]); m != nil {
			r.lifetime, _ = strconv.Atoi(m[1])
		}
		out = append(out, r)
	}
	return out
}

// dockerCalls answers the fake docker's calls of a kind ("rm", "ps", …).
func dockerCalls(t *testing.T, state, kind string) []string {
	t.Helper()
	raw, _ := os.ReadFile(filepath.Join(state, "calls.log"))
	var out []string
	for _, l := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(l, "docker "+kind+" ") {
			out = append(out, l)
		}
	}
	return out
}

func TestRunOneLineInTheContainerWritesTheResult(t *testing.T) {
	f := runCapsule(t)
	out := filepath.Join(f.dir, "artifacts", "run", "result.json")
	env, state := fakeDockerEnv(t, stepEnv("PF_ROUND=r1", "PF_WI_ID=wi_sc", "PF_TOOL_WINDOW_MS=1800000"))
	before := time.Now()
	exit, _, errb := invokePy(t, scriptRun, []string{"--isolation", "container", "--capsule", f.dir, "--json", "--out", out, "--repo", "app", "--", "echo hi there && test -f .polyforge/Dockerfile.verify"}, env)
	if exit != 0 {
		t.Fatalf("exit %d\n%s", exit, errb)
	}
	res := readResult(t, out)
	if res.Verdict != VerdictPass || res.Round != "r1" || res.Heads["app"] != f.head(t) || res.Command != scriptRun {
		t.Fatalf("result: %+v", res)
	}
	var d runDetails
	detailsOf(t, res, &d)
	if len(d.Runs) != 1 || d.Runs[0].ExitCode != 0 || d.Runs[0].TimedOut || d.Runs[0].Repo != "app" || !strings.Contains(d.Runs[0].OutputTail, "hi there") {
		t.Fatalf("runs: %+v", d.Runs)
	}
	if d.Runs[0].Log != "artifacts/run/logs/app/1/command.log" || len(res.Logs) != 1 || res.Logs[0] != d.Runs[0].Log || !strings.HasPrefix(d.Runs[0].Image, "pf-verify-app:") {
		t.Fatalf("log paths: %+v / %v", d.Runs[0], res.Logs)
	}
	if raw, err := os.ReadFile(filepath.Join(f.dir, "artifacts", "run", "logs", "app", "1", "command.log")); err != nil || !strings.Contains(string(raw), "hi there") {
		t.Fatalf("the log: %v %q", err, raw)
	}
	if raw, err := os.ReadFile(filepath.Join(f.dir, "artifacts", "run", "logs", ".gitignore")); err != nil || string(raw) != logsGitignore {
		t.Fatalf("the logs dir is not kept out of the chain: %v %q", err, raw)
	}
	// the container: the scripts' owner, a fresh boot, the work item, the
	// deadline label, the keep-alive
	runs := dockerRuns(t, state)
	if len(runs) != 1 {
		t.Fatalf("containers started: %d", len(runs))
	}
	s := runs[0]
	for k, want := range map[string]string{LabelDaemon: StepOwner, LabelWI: "wi_sc", LabelRepo: "app", LabelRun: "1"} {
		if s.labels[k] != want {
			t.Errorf("label %s = %q, want %q", k, s.labels[k], want)
		}
	}
	if len(s.labels[LabelBoot]) != 16 {
		t.Errorf("label %s = %q, want a fresh 16-hex nonce", LabelBoot, s.labels[LabelBoot])
	}
	window := 30*time.Minute - 2*time.Minute // the tool window less the margin
	exp, err := strconv.ParseInt(s.labels[LabelExpires], 10, 64)
	if err != nil {
		t.Fatalf("no readable %s: %q", LabelExpires, s.labels[LabelExpires])
	}
	if got := time.Unix(exp, 0).Sub(before); got < window-time.Minute || got > window+time.Minute {
		t.Errorf("expires in %s, want about %s", got, window)
	}
	if got := time.Duration(s.lifetime) * time.Second; got < window+KeepAliveGrace-time.Minute || got > window+KeepAliveGrace+time.Minute {
		t.Errorf("keep-alive %s, want about the deadline plus the grace (%s)", got, window+KeepAliveGrace)
	}
	if rms := dockerCalls(t, state, "rm"); len(rms) == 0 || !strings.HasSuffix(rms[len(rms)-1], "cid-1") {
		t.Errorf("the container was not removed when the line ended: %v", rms)
	}
	// the sweep looked for the owner's expired containers first
	if ps := dockerCalls(t, state, "ps"); len(ps) == 0 || !strings.Contains(ps[0], "label="+LabelDaemon+"="+StepOwner) {
		t.Errorf("no expiry sweep for the owner: %v", ps)
	}
}

func TestRunPlanStopsAtTheFirstFailure(t *testing.T) {
	for _, isolation := range []string{"container", "host"} {
		t.Run(isolation, func(t *testing.T) {
			f := runCapsule(t)
			plan := filepath.Join(t.TempDir(), "evidence.json")
			raw, _ := json.Marshal([]planLine{{"app", "echo one"}, {"app", "echo two >&2; exit 3"}, {"app", "echo three"}})
			if err := os.WriteFile(plan, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			out := filepath.Join(f.dir, "artifacts", "run", "result.json")
			env, state := fakeDockerEnv(t, stepEnv())
			exit, _, _ := invokePy(t, scriptRun, []string{"--isolation", isolation, "--capsule", f.dir, "--plan-from", plan, "--out", out}, env)
			if exit != 1 {
				t.Fatalf("exit %d, want 1", exit)
			}
			res := readResult(t, out)
			var d runDetails
			detailsOf(t, res, &d)
			if res.Verdict != VerdictFail || len(d.Runs) != 2 || d.Runs[1].ExitCode != 3 || len(d.NotRun) != 1 || d.NotRun[0].Line != "echo three" {
				t.Fatalf("result %+v\nruns %+v\nnot run %+v", res, d.Runs, d.NotRun)
			}
			if !strings.Contains(res.Summary, "`echo two >&2; exit 3` exited 3") || !strings.Contains(res.Summary, "1 later line(s) did not run") {
				t.Fatalf("summary: %s", res.Summary)
			}
			if d.Plan != plan || d.Runs[0].Log != "artifacts/run/logs/app/1/command.log" || d.Runs[1].Log != "artifacts/run/logs/app/2/command.log" {
				t.Fatalf("details: %+v", d)
			}
			if !strings.Contains(d.Runs[1].OutputTail, "two") || res.Heads["app"] != f.head(t) {
				t.Fatalf("tail/heads: %+v %v", d.Runs[1], res.Heads)
			}
			// the host runs no container, the container run one per line
			if n, want := len(dockerRuns(t, state)), map[string]int{"container": 2, "host": 0}[isolation]; n != want || (isolation == "host" && d.Runs[0].Image != "") {
				t.Fatalf("containers started: %d, want %d (%+v)", n, want, d.Runs[0])
			}
			// every line green: pass, every log listed
			raw, _ = json.Marshal([]planLine{{"app", "echo one"}, {"app", "echo three"}})
			if err := os.WriteFile(plan, raw, 0o644); err != nil {
				t.Fatal(err)
			}
			exit, _, _ = invokePy(t, scriptRun, []string{"--isolation", isolation, "--capsule", f.dir, "--plan-from", plan, "--out", out}, env)
			res = readResult(t, out)
			if exit != 0 || res.Verdict != VerdictPass || len(res.Logs) != 2 || !strings.Contains(res.Summary, "2 line(s) ran and exited 0 in repos/app") {
				t.Fatalf("exit %d, result %+v", exit, res)
			}
		})
	}
}

func TestRunRefusesUnusableInvocations(t *testing.T) {
	f := runCapsule(t)
	plan := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(plan, []byte(`[{"repo":"app"}]`), 0o644); err != nil {
		t.Fatal(err)
	}
	env, state := fakeDockerEnv(t, stepEnv())
	for _, c := range []struct {
		name string
		args []string
		want string
	}{
		{"no isolation", []string{"--capsule", f.dir, "--repo", "app", "--", "true"}, "--isolation (none)"},
		{"unknown isolation", []string{"--isolation", "vm", "--capsule", f.dir, "--repo", "app", "--", "true"}, "--isolation vm"},
		{"neither", []string{"--isolation", "container", "--capsule", f.dir}, "exactly one of"},
		{"both", []string{"--isolation", "container", "--capsule", f.dir, "--repo", "app", "--plan-from", plan, "--", "true"}, "exactly one of"},
		{"no line", []string{"--isolation", "container", "--capsule", f.dir, "--repo", "app"}, "exactly one argument"},
		{"two words", []string{"--isolation", "container", "--capsule", f.dir, "--repo", "app", "--", "make", "smoke"}, "exactly one argument"},
		{"plan with line", []string{"--isolation", "container", "--capsule", f.dir, "--plan-from", plan, "--", "true"}, "takes no line"},
		{"plan entry without line", []string{"--isolation", "container", "--capsule", f.dir, "--plan-from", plan}, "needs a repo and a line"},
		{"missing plan", []string{"--isolation", "container", "--capsule", f.dir, "--plan-from", plan + ".nope"}, "no such file"},
		{"unknown repo", []string{"--isolation", "container", "--capsule", f.dir, "--repo", "nope", "--", "true"}, "repos/nope"},
	} {
		exit, _, errb := invokePy(t, scriptRun, c.args, env)
		if exit != 2 || !strings.Contains(errb, c.want) {
			t.Errorf("%s: exit %d (want 2), stderr %q (want %q)", c.name, exit, errb, c.want)
		}
	}
	if n := len(dockerRuns(t, state)); n != 0 {
		t.Errorf("a refused invocation started %d container(s)", n)
	}
}

// TestRunContainerWithoutADockerfileIsNotJudged: a container run in a repo
// without .polyforge/Dockerfile.verify at HEAD is not judged (exit 2) and
// never runs the line on the host instead.
func TestRunContainerWithoutADockerfileIsNotJudged(t *testing.T) {
	f := newCapsule(t, map[string]string{"a.txt": "a\n"})
	out := filepath.Join(f.dir, "result.json")
	env, state := fakeDockerEnv(t, stepEnv())
	exit, _, _ := invokePy(t, scriptRun, []string{"--isolation", "container", "--capsule", f.dir, "--out", out, "--repo", "app", "--", "touch ran-on-the-host"}, env)
	res := readResult(t, out)
	if exit != 2 || res.Verdict != VerdictError || !strings.Contains(res.Summary, DockerfileVerify) {
		t.Fatalf("exit %d, result %+v", exit, res)
	}
	if _, err := os.Stat(filepath.Join(f.repo(), "ran-on-the-host")); err == nil || len(dockerRuns(t, state)) != 0 {
		t.Fatal("the line ran without its container")
	}
	// the same line on the host runs there
	if exit, _, errb := invokePy(t, scriptRun, []string{"--isolation", "host", "--capsule", f.dir, "--out", out, "--repo", "app", "--", "touch ran-on-the-host"}, env); exit != 0 {
		t.Fatalf("host: exit %d\n%s", exit, errb)
	}
	if _, err := os.Stat(filepath.Join(f.repo(), "ran-on-the-host")); err != nil {
		t.Fatalf("the host line did not run: %v", err)
	}
}

func TestRunWithoutAnEngineIsNotJudged(t *testing.T) {
	f := runCapsule(t)
	env, state := fakeDockerEnv(t, stepEnv())
	if err := os.WriteFile(filepath.Join(state, "unavailable"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(f.dir, "result.json")
	exit, _, _ := invokePy(t, scriptRun, []string{"--isolation", "container", "--capsule", f.dir, "--out", out, "--repo", "app", "--", "true"}, env)
	res := readResult(t, out)
	if exit != 2 || res.Verdict != VerdictError || !strings.Contains(res.Summary, "container") || !strings.Contains(res.Summary, "never the line's verdict") {
		t.Fatalf("exit %d, result %+v", exit, res)
	}
	var d runDetails
	detailsOf(t, res, &d)
	if len(d.Runs) != 0 || len(d.NotRun) != 1 {
		t.Fatalf("details: %+v", d)
	}
}

func TestRunSweepsTheOwnersExpiredContainers(t *testing.T) {
	f := runCapsule(t)
	env, state := fakeDockerEnv(t, stepEnv("PF_WI_ID=wi_sc"))
	now := time.Now()
	expiring := "old\t" + strconv.FormatInt(now.Add(-time.Hour).Unix(), 10) + "\nlive\t" + strconv.FormatInt(now.Add(time.Hour).Unix(), 10) + "\nunlabled\t\n"
	if err := os.WriteFile(filepath.Join(state, "expiring"), []byte(expiring), 0o644); err != nil {
		t.Fatal(err)
	}
	exit, _, errb := invokePy(t, scriptRun, []string{"--isolation", "container", "--capsule", f.dir, "--repo", "app", "--", "true"}, env)
	if exit != 0 {
		t.Fatalf("exit %d\n%s", exit, errb)
	}
	removed := strings.Join(dockerCalls(t, state, "rm"), "\n")
	if !strings.Contains(removed, " old") || strings.Contains(removed, " live") || strings.Contains(removed, "unlabled") {
		t.Fatalf("removed %v: want the expired one only", removed)
	}
	if !strings.Contains(errb, "removed container old") || strings.Contains(errb, "unlabled") {
		t.Fatalf("progress: %q", errb)
	}
}

func TestRunOutsideAStepLabelsItsOwnOwner(t *testing.T) {
	f := runCapsule(t)
	env, state := fakeDockerEnv(t, stepEnv())
	exit, _, errb := invokePy(t, scriptRun, []string{"--isolation", "container", "--capsule", f.dir, "--repo", "app", "--", "true"}, env)
	runs := dockerRuns(t, state)
	if exit != 0 || len(runs) != 1 {
		t.Fatalf("exit %d, starts %d\n%s", exit, len(runs), errb)
	}
	l := runs[0].labels
	if l[LabelDaemon] != StepOwner || len(l[LabelBoot]) != 16 || l[LabelWI] != "" {
		t.Fatalf("labels: %v", l)
	}
	if ps := dockerCalls(t, state, "ps"); len(ps) == 0 || !strings.Contains(ps[0], "label="+LabelDaemon+"="+StepOwner) {
		t.Fatalf("the sweep looked for %v, want the step owner", ps)
	}
	// the default deadline outside a step: 25 minutes, so the keep-alive is
	// that plus the grace
	if want := DefaultDeadline + KeepAliveGrace; time.Duration(runs[0].lifetime)*time.Second < want-time.Minute || time.Duration(runs[0].lifetime)*time.Second > want+time.Minute {
		t.Fatalf("keep-alive %ds, want about %s", runs[0].lifetime, want)
	}
}

// realDockerfile is a Dockerfile.verify the real-docker tests build: the
// small public image with a bash that is its sh.
const realDockerfile = "FROM busybox:1.37\nRUN printf '#!/bin/sh\\nexec /bin/sh \"$@\"\\n' > /bin/bash && chmod 0755 /bin/bash\n"

// realDocker answers a directory a real docker engine sees at its own
// path (skipping without an engine, the small public image, or such a
// directory).
func realDocker(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "docker", "version", "--format", "{{.Server.Version}}").CombinedOutput(); err != nil {
		t.Skipf("no docker engine: %v %s", err, out)
	}
	if exec.CommandContext(ctx, "docker", "image", "inspect", "busybox:1.37").Run() != nil {
		if out, err := exec.CommandContext(ctx, "docker", "pull", "-q", "busybox:1.37").CombinedOutput(); err != nil {
			t.Skipf("cannot pull busybox:1.37: %v %s", err, out)
		}
	}
	for _, mk := range []func() (string, error){
		func() (string, error) { return t.TempDir(), nil },
		func() (string, error) {
			p, err := os.MkdirTemp(".", ".stepscripts-real-")
			if err == nil {
				t.Cleanup(func() { _ = os.RemoveAll(p) })
				p, err = filepath.Abs(p)
			}
			return p, err
		},
	} {
		p, err := mk()
		if err != nil {
			continue
		}
		if err := os.WriteFile(filepath.Join(p, ".sentinel"), []byte("seen\n"), 0o644); err != nil {
			continue
		}
		out, err := exec.CommandContext(ctx, "docker", "run", "--rm", "--mount", "type=bind,source="+p+",target="+p+",readonly", "busybox:1.37", "cat", filepath.Join(p, ".sentinel")).Output()
		_ = os.Remove(filepath.Join(p, ".sentinel"))
		if err == nil && strings.TrimSpace(string(out)) == "seen" {
			return p
		}
	}
	t.Skip("the docker engine does not see this process's directories at their own paths")
	return ""
}

// repoContainers answers the ids of the containers (running or not)
// labelled with repo.
func repoContainers(ctx context.Context, t *testing.T, repo string) []string {
	t.Helper()
	out, err := exec.CommandContext(ctx, "docker", "ps", "-a", "-q", "--filter", "label="+LabelRepo+"="+repo).Output()
	if err != nil {
		t.Fatalf("docker ps: %v", err)
	}
	return strings.Fields(string(out))
}

// realCapsule lays out a capsule under dir whose repo carries the real
// Dockerfile, named uniquely, and registers the cleanup of its image,
// volume and containers.
func realCapsule(t *testing.T, dir string) (capsuleDir, repo string) {
	t.Helper()
	var rnd [5]byte
	_, _ = rand.Read(rnd[:])
	repo = "stepscripts-real-" + hex.EncodeToString(rnd[:])
	capsuleDir = filepath.Join(dir, "capsule")
	clone := filepath.Join(capsuleDir, "repos", repo)
	t.Cleanup(func() {
		c, cc := context.WithTimeout(context.Background(), time.Minute)
		defer cc()
		if out, err := exec.CommandContext(c, "docker", "ps", "-a", "-q", "--filter", "label="+LabelRepo+"="+repo).Output(); err == nil {
			for _, id := range strings.Fields(string(out)) {
				_ = exec.CommandContext(c, "docker", "rm", "--force", id).Run()
			}
		}
		if out, err := exec.CommandContext(c, "docker", "images", "-q", "--filter", "label="+LabelRepo+"="+repo).Output(); err == nil {
			for _, id := range strings.Fields(string(out)) {
				_ = exec.CommandContext(c, "docker", "rmi", "--force", id).Run()
			}
		}
		_ = exec.CommandContext(c, "docker", "volume", "rm", "--force", cacheVolume(repo, realDockerfile)).Run()
	})
	if err := os.MkdirAll(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	gitT(t, clone, "init", "-q", "-b", "main")
	writeFile(t, clone, DockerfileVerify, realDockerfile)
	writeFile(t, clone, "a.txt", "a\n")
	commitAll(t, clone, "tree")
	return capsuleDir, repo
}

func TestRunRealDockerLineRunsInTheContainerAndLeavesNothing(t *testing.T) {
	dir := realDocker(t)
	capsuleDir, repo := realCapsule(t, dir)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	out := filepath.Join(capsuleDir, "artifacts", "run", "result.json")
	// the line proves it runs in the clone, inside a container (no docker
	// client, the image's own /bin/bash shim) and leaves a mark the host
	// reads back through the mount
	line := `test -f .polyforge/Dockerfile.verify && ! command -v docker >/dev/null 2>&1 && grep -q 'exec /bin/sh' /bin/bash && echo ran-in-container > ran.txt`
	env := stepEnv("PF_WI_ID=wi_real", "PF_ROUND=r9")
	exit, stdout, stderr := invokePy(t, scriptRun, []string{"--isolation", "container", "--capsule", capsuleDir, "--deadline", "3m", "--json", "--out", out, "--repo", repo, "--", line}, env)
	if exit != 0 {
		t.Fatalf("exit %d\n%s\n%s", exit, stdout, stderr)
	}
	res := readResult(t, out)
	if res.Verdict != VerdictPass || res.Round != "r9" || res.Heads[repo] == "" {
		t.Fatalf("result: %+v", res)
	}
	if raw, err := os.ReadFile(filepath.Join(capsuleDir, "repos", repo, "ran.txt")); err != nil || strings.TrimSpace(string(raw)) != "ran-in-container" {
		t.Fatalf("the line's mark: %v %q", err, raw)
	}
	var det runDetails
	detailsOf(t, res, &det)
	if len(det.Runs) != 1 || det.Runs[0].Container == "" || !strings.HasPrefix(det.Runs[0].Image, "pf-verify-") {
		t.Fatalf("runs: %+v", det.Runs)
	}
	// gone once the line ended: nothing of the repo's is left
	if found := repoContainers(ctx, t, repo); len(found) != 0 {
		t.Fatalf("the container is still there: %v", found)
	}
	if !strings.Contains(stderr, repo) {
		t.Errorf("progress names no repo: %q", stderr)
	}
}
