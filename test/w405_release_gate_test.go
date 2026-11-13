package test

// w405_release_gate_test.go: `.polyforge/release.sh`'s test gate is the
// verification's full plan inside its container, or the reused result of a
// green tag for the same gate on the released commit.
//
// The release runs in the fake world of release_script_test.go. The gate
// is the tree's own workflow script (workflows/_shared/scripts/gate.py and
// its library, which the fake tree carries like the real one; no Go
// build); its docker client is the fake docker, which records the gate's
// image build, container and every `docker exec` and runs a --workdir exec
// in that directory with only the image's PATH. That a host-only
// difference (a docker client on PATH) is invisible to the gate is proven
// against a real docker engine in
// workflows/_shared/scripts/tests/test_gate_docker.py.
//
// Run (targeted): GOWORK=off go test ./test/ -run '^TestW405' -count=1

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// w405GateWorld is a release world whose gate runs: python3 is on PATH
// (the gate script and the scope engine's --json need it).
func w405GateWorld(t *testing.T) *relWorld {
	t.Helper()
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not installed (the gate script and the scope engine's --json need it)")
	}
	return newRelWorld(t, []string{"v1.2.0"}, nil)
}

// w405Plan is the tree's engine --full plan, asked on the host: the
// commands of the fast and full phases, in order.
func w405Plan(t *testing.T, dir string) []string {
	t.Helper()
	c := exec.Command("sh", "workflows/_shared/scripts/verify-scope", "--json", "--full", "--base", "HEAD")
	c.Dir = dir
	raw, err := c.Output()
	if err != nil {
		t.Fatalf("engine: %v", err)
	}
	var rep struct {
		Mode     string `json:"mode"`
		Commands []struct {
			Phase string `json:"phase"`
			Cmd   string `json:"cmd"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(raw, &rep); err != nil || rep.Mode != "full" {
		t.Fatalf("engine report: %v mode %q", err, rep.Mode)
	}
	var out []string
	for _, c := range rep.Commands {
		if c.Phase == "fast" || c.Phase == "full" {
			out = append(out, c.Cmd)
		}
	}
	return out
}

// w405BoxedCommands answers the plan commands the gate ran through
// `docker exec` (the `bash -c` ones), in order.
func w405BoxedCommands(calls []string) []string {
	var out []string
	for _, c := range calls {
		if !strings.HasPrefix(c, "docker exec ") {
			continue
		}
		if i := strings.LastIndex(c, " bash -c "); i >= 0 {
			out = append(out, c[i+len(" bash -c "):])
		}
	}
	return out
}

func w405GateID(t *testing.T, dir string) string {
	t.Helper()
	c := exec.Command("sh", "tools/gate-id")
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		t.Fatalf("tools/gate-id: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// TestW405ReleaseGateIsTheConfPlanInTheContainer: with no green tag, the
// release's test gate is the tree's gate.py, and what it runs is exactly the
// tree's scope-engine full plan, every command through `docker exec` in the
// container built from .polyforge/Dockerfile.verify — never on the host,
// never `make test`. Changing the release's gate to anything else, or the
// runner to anything but the conf's plan, turns this red.
func TestW405ReleaseGateIsTheConfPlanInTheContainer(t *testing.T) {
	w := w405GateWorld(t)
	out, err := w.run([]string{"RELEASE_TEST_CMD="})
	if err != nil {
		t.Fatalf("release failed: %v\n%s", err, out)
	}
	calls := w.calls()
	want := w405Plan(t, w.work)
	if !reflect.DeepEqual(want, []string{"echo gate-fast", "echo gate-full"}) {
		t.Fatalf("the fake tree's engine planned %q", want)
	}
	if got := w405BoxedCommands(calls); !reflect.DeepEqual(got, want) {
		t.Fatalf("the gate ran %q in the container, want the conf's full plan %q\n%v", got, want, calls)
	}
	// the order: the xrefs gate, the image, the container, the engine, the
	// plan, the container's removal, then the audit gate and the release
	// build
	order := []string{"xrefs", "docker gate-build ", "docker run ", "docker exec ", "docker rm ", "audit", "build"}
	last := -1
	for _, p := range order {
		i := relIndex(calls[last+1:], p)
		if i < 0 {
			t.Fatalf("%q missing or out of order in %v", p, calls)
		}
		last += 1 + i
	}
	if relIndex(calls, "tests") >= 0 {
		t.Fatalf("a host test command ran: %v", calls)
	}
	// the engine ran in the container, asked for the full plan of HEAD
	sawEngine := false
	for _, c := range calls {
		if strings.HasPrefix(c, "docker exec ") && strings.HasSuffix(c, "/workflows/_shared/scripts/verify-scope --json --full --base HEAD") {
			sawEngine = true
		}
	}
	if !sawEngine {
		t.Fatalf("the scope engine did not run in the container: %v", calls)
	}
	// the image: the tree's Dockerfile.verify alone, the daemon's tag for
	// this clone's name
	sum := sha256.Sum256([]byte(relGateDockerfile))
	tag := "pf-verify-work:" + hex.EncodeToString(sum[:16])
	if relIndex(calls, "docker gate-build "+tag) < 0 {
		t.Fatalf("no image build of %s: %v", tag, calls)
	}
	if df, _ := os.ReadFile(w.log + ".gatedockerfile"); string(df) != relGateDockerfile {
		t.Fatalf("the image was built from %q, want the tree's Dockerfile.verify", df)
	}
	// the container: a gate's, mounting the clone alone, no docker socket
	run := calls[relIndex(calls, "docker run ")]
	for _, want := range []string{"--label pf.verify.daemon=pf-gate", "--mount type=bind,source=" + w.work + ",target=" + w.work + " ", "--mount type=volume,source=pf-verify-cache-work-" + hex.EncodeToString(sum[:16]) + ",target=/cache", " " + tag + " "} {
		if !strings.Contains(run+" ", want) {
			t.Errorf("container start lacks %q: %s", want, run)
		}
	}
	if strings.Count(run, "type=bind") != 1 || strings.Contains(run, "docker.sock") {
		t.Errorf("the gate's container must mount the clone alone: %s", run)
	}
	for _, want := range []string{"gate: PASS - 2 commands passed", "release: test gate: no reusable green tag"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// the release notes record how the gate passed
	body, _ := os.ReadFile(filepath.Join(w.fakeRel, "v1.2.1", ".body"))
	if !strings.Contains(string(body), "\n## Test gate\n\nRan in the verification container") || !strings.Contains(string(body), w405GateID(t, w.work)) {
		t.Fatalf("notes lack the gate record: %q", body)
	}
	if vout, verr := w.run(nil, "--verify", "v1.2.1"); verr != nil {
		t.Fatalf("verify: %v\n%s", verr, vout)
	}
}

// TestW405ReleaseRedOrUnjudgedGateStopsBeforeTheBuild: a failing plan
// command, or a gate gate.py cannot judge (no docker engine), stops the
// release with nothing built, published or claimed.
func TestW405ReleaseRedOrUnjudgedGateStopsBeforeTheBuild(t *testing.T) {
	for name, tc := range map[string]struct {
		extra []string
		red   bool
		want  []string
	}{
		"red":      {nil, true, []string{"test gate failed (gate.py exited 1", "full-1 FAIL - exit 4", "about-to-fail"}},
		"unjudged": {[]string{"FAKE_DOCKER_DOWN=1"}, false, []string{"test gate not judged: gate.py exited 2", "which is not a red gate", "container engine is unavailable"}},
	} {
		t.Run(name, func(t *testing.T) {
			w := w405GateWorld(t)
			if tc.red {
				w.mergeOnMain(".polyforge/verify-scope.conf", "stack=none\nfast_gate=echo gate-fast\nfull_command=echo about-to-fail; exit 4\n", "a failing gate", true)
			}
			out, err := w.run(append([]string{"RELEASE_TEST_CMD="}, tc.extra...))
			if err == nil {
				t.Fatalf("the release passed:\n%s", out)
			}
			for _, want := range append(tc.want, "nothing built or published", "no version claimed") {
				if !strings.Contains(out, want) {
					t.Errorf("output lacks %q:\n%s", want, out)
				}
			}
			if !tc.red && strings.Contains(out, "test gate failed") {
				t.Errorf("a gate that could not be judged was reported as failed:\n%s", out)
			}
			for _, p := range []string{"audit", "build", "git push", "docker push", "gh release"} {
				if relIndex(w.calls(), p) >= 0 {
					t.Fatalf("%q ran after the gate failed: %v", p, w.calls())
				}
			}
			if got := w.originTags(); strings.Contains(got, "v1.2.1") {
				t.Fatalf("tag pushed: %q", got)
			}
		})
	}
}

// w405PlantTag pushes a hand-made annotated tag named name on commit with
// message msg (lightweight when msg is empty); push=false keeps it local.
func w405PlantTag(t *testing.T, dir, name, commit, msg string, push bool) {
	t.Helper()
	if msg == "" {
		relGit(t, dir, "tag", name, commit)
	} else {
		relGit(t, dir, "tag", "-a", "-m", msg, name, commit)
	}
	if push {
		relGit(t, dir, "push", "-q", "origin", "refs/tags/"+name)
	}
}

// w405MarkGreen runs `gate.py --mark-green` (with extra flags) in the
// world's clone and answers the tag it names.
func w405MarkGreen(t *testing.T, w *relWorld, extra ...string) string {
	t.Helper()
	out, errb, code := w405RunGate(t, w, nil, append([]string{"--mark-green", "--summary", "gate.py run by the test"}, extra...)...)
	if code != 0 {
		t.Fatalf("gate.py --mark-green: %d\n%s\n%s", code, out, errb)
	}
	lines := strings.Split(strings.TrimSpace(out), "\n")
	return lines[len(lines)-1]
}

// TestW405ReleaseReusesAGreenTagForTheSameGate: a released commit carrying
// a green tag gate.py --mark-green wrote on origin, for this tree's gate id
// and HEAD's plan digest, skips the test gate (no plan command runs; only
// the plan is recomputed) and the release notes name the tag; every other
// shape runs the gate.
func TestW405ReleaseReusesAGreenTagForTheSameGate(t *testing.T) {
	t.Run("reused", func(t *testing.T) {
		w := w405GateWorld(t)
		head := relGit(t, w.work, "rev-parse", "HEAD")
		green := w405MarkGreen(t, w)
		relWrite(t, w.log, "", 0o644) // the marking run's calls are not the release's
		out, err := w.run([]string{"RELEASE_TEST_CMD="})
		if err != nil {
			t.Fatalf("release failed: %v\n%s", err, out)
		}
		if got := w405BoxedCommands(w.calls()); len(got) != 0 {
			t.Fatalf("the gate ran %q although %s carries %s for this gate", got, head, green)
		}
		gate := w405GateID(t, w.work)
		if !strings.Contains(out, "carries the green tag "+green+" on origin for gate "+gate+", written by the gate for this plan - that result is reused") {
			t.Fatalf("the run does not say which green tag it reused:\n%s", out)
		}
		body, _ := os.ReadFile(filepath.Join(w.fakeRel, "v1.2.1", ".body"))
		if !strings.Contains(string(body), "\n## Test gate\n\nNot run again: the commit carries the green tag `"+green+"` for gate `"+gate+"`") {
			t.Fatalf("notes lack the reused green tag: %q", body)
		}
		// everything else is unchanged: the audit, the build, the publish
		for _, p := range []string{"xrefs", "audit", "build", "git push", "docker push", "gh release create"} {
			if relIndex(w.calls(), p) < 0 {
				t.Fatalf("%q did not run: %v", p, w.calls())
			}
		}
		if vout, verr := w.run(nil, "--verify", "v1.2.1"); verr != nil {
			t.Fatalf("verify: %v\n%s", verr, vout)
		}
	})
	for name, plant := range map[string]func(t *testing.T, w *relWorld, head string){
		"another runner's record": func(t *testing.T, w *relWorld, head string) {
			green := w405MarkGreen(t, w)
			rec := w405TagRecord(t, w, green)
			relGit(t, w.origin, "update-ref", "-d", "refs/tags/"+green)
			relGit(t, w.work, "tag", "-d", green)
			rec.Runner = "verifyrun.Run"
			w405PlantTag(t, w.work, green, head, rec.json(t), true)
		},
		"another plan": func(t *testing.T, w *relWorld, head string) {
			// the tree is the same, the plan is not: a gate run under
			// other container limits than the release's (the defaults)
			w405MarkGreen(t, w, "--pids", "99")
		},
		"another commit": func(t *testing.T, w *relWorld, head string) {
			green := w405MarkGreen(t, w)
			rec := w405TagRecord(t, w, green)
			relGit(t, w.origin, "update-ref", "-d", "refs/tags/"+green)
			relGit(t, w.work, "tag", "-d", green)
			rec.Commit = relGit(t, w.work, "rev-parse", "HEAD^1")
			w405PlantTag(t, w.work, green, rec.Commit, rec.json(t), true)
		},
		"a lightweight tag": func(t *testing.T, w *relWorld, head string) {
			w405PlantTag(t, w.work, "green/20261108-1", head, "", true)
		},
		"a local tag only": func(t *testing.T, w *relWorld, head string) {
			green := w405MarkGreen(t, w)
			relGit(t, w.origin, "update-ref", "-d", "refs/tags/"+green)
		},
	} {
		t.Run(name, func(t *testing.T) {
			w := w405GateWorld(t)
			plant(t, w, relGit(t, w.work, "rev-parse", "HEAD"))
			relWrite(t, w.log, "", 0o644)
			out, err := w.run([]string{"RELEASE_TEST_CMD="})
			if err != nil {
				t.Fatalf("release failed: %v\n%s", err, out)
			}
			if got := w405BoxedCommands(w.calls()); !reflect.DeepEqual(got, []string{"echo gate-fast", "echo gate-full"}) {
				t.Fatalf("the gate did not run its plan (ran %q):\n%s", got, out)
			}
			if !strings.Contains(out, "release: test gate: no reusable green tag") {
				t.Fatalf("output:\n%s", out)
			}
		})
	}
	// RELEASE_TEST_CMD replaces the whole gate: no green tag is consulted
	t.Run("override", func(t *testing.T) {
		w := w405GateWorld(t)
		w405MarkGreen(t, w)
		out, err := w.run(nil)
		if err != nil {
			t.Fatalf("release failed: %v\n%s", err, out)
		}
		if relIndex(w.calls(), "tests") < 0 || !strings.Contains(out, "RELEASE_TEST_CMD replaces the gate") {
			t.Fatalf("the override did not run:\n%s", out)
		}
	})
}

// TestW405ReleaseNeverRunsMakeTest pins the one gate definition in the
// script: no executable line runs `make test` or builds a gate, the default
// gate asks the green-tag check, then runs the tree's gate.py in the
// container; the script never writes a green tag.
func TestW405ReleaseNeverRunsMakeTest(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", ".polyforge", "release.sh"))
	if err != nil {
		t.Fatal(err)
	}
	var code []string
	for _, ln := range strings.Split(string(raw), "\n") {
		if tl := strings.TrimSpace(ln); tl != "" && !strings.HasPrefix(tl, "#") {
			code = append(code, ln)
		}
	}
	src := strings.Join(code, "\n")
	if strings.Contains(src, "make test") {
		t.Fatal("release.sh runs `make test`: the release's test gate is the verification's plan in its container (gate.py)")
	}
	if strings.Contains(src, "cmd/pf-gate") {
		t.Fatal("release.sh builds the retired cmd/pf-gate: the gate is the tree's gate.py")
	}
	check := strings.Index(src, "tools/green-tag check \"$COMMIT\" --remote \"$REMOTE\"")
	run := strings.Index(src, "python3 \"$GATE_PY\" --isolation container --log-dir \"artifacts/release-$TAG/gate\" || TGRC=$?")
	if check < 0 || run < 0 || check > run || !strings.Contains(src, "GATE_PY=workflows/_shared/scripts/gate.py") {
		t.Fatalf("release.sh's test gate must check for a green tag, then run the tree's gate.py in the container (positions %d %d)", check, run)
	}
	if strings.Contains(src, "green-tag create") || strings.Contains(src, "gate.py\" --isolation container --mark-green") {
		t.Fatal("release.sh writes a green tag: only gate.py --mark-green does")
	}
}

// TestW405ReleasePreflightRefusesAnOldPython: the test gate is a Python
// 3.9 script, so a release host whose python3 is older fails the preflight
// — every problem in its one message, nothing built, published or claimed
// — and never reaches the gate (an old interpreter is no red gate). With
// RELEASE_TEST_CMD replacing the gate the version is not asked.
func TestW405ReleasePreflightRefusesAnOldPython(t *testing.T) {
	w := w405GateWorld(t)
	realPy, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	// a python3 that answers like 3.8 to the version questions and runs
	// everything else as the real one
	relWrite(t, filepath.Join(w.bin, "python3"), `#!/bin/sh
case "$*" in
  *"version_info < (3, 9)"*) exit 1 ;;
  *python_version*) echo 3.8.10; exit 0 ;;
esac
exec `+realPy+` "$@"
`, 0o755)
	out, err := w.run([]string{"RELEASE_TEST_CMD="})
	if err == nil {
		t.Fatalf("the release passed with python3 3.8:\n%s", out)
	}
	for _, want := range []string{"preflight failed", "python3 3.8.10 is older than 3.9", "workflows/_shared/scripts/gate.py", "no version claimed"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "release: running test gate") || relIndex(w.calls(), "docker run ") >= 0 {
		t.Fatalf("the gate ran on an old python3:\n%s", out)
	}
	if got := w.originTags(); strings.Contains(got, "v1.2.1") {
		t.Fatalf("tag pushed: %q", got)
	}
	// RELEASE_TEST_CMD replaces the gate: the old python3 is no problem
	if out, err := w.run(nil); err != nil {
		t.Fatalf("with RELEASE_TEST_CMD the release refused the old python3: %v\n%s", err, out)
	}
}
