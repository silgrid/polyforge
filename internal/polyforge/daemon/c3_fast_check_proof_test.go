package daemon

// c3_fast_check_proof_test.go (polyforge#211, C3'): a step whose lane
// declares `fast_check_proof` hands off done only with a passing
// pf-step-result/1 result for every task repo its round changed — the file
// the lane's template names, verdict pass, heads equal to the repo's HEAD.
// The refusal names the repo, the failing check and the line that writes a
// fresh result; nothing is written, so a retry after the command succeeds.
// The tests run the production hand-off path (c4Call -> handoffVerb) over
// c3Lane, the c4Lane fixture plus the two declarations.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow/workflowtest"
)

// c3Lane is c4Lane plus develop's own fast_check_proof shape for build and
// sync. The command is deliberately a name no tool has: the daemon only
// relays it, which is also the proof that the daemon holds no repository's
// command.
const c3Lane = `max_rewinds: 3
uncounted_rewinds:
  reason_contains: the default branch moved since verification
  max: 1
steps:
  - id: build
    fast_check_proof:
      result: artifacts/build/fast-checks/{repo}.json
      command: run-fast-check --repo {repo} --out artifacts/build/fast-checks/{repo}.json
  - id: review
    gate: true
    on_fail: { target: build }
  - id: sync
    fast_check_proof:
      result: artifacts/sync/fast-checks/{repo}.json
      command: run-fast-check --repo {repo} --out artifacts/sync/fast-checks/{repo}.json
  - id: verification
    gate: true
    waivable: true
    on_fail: { target: build }
  - id: publish
  - id: merge
    resources: { locks: [merge] }
    wrap_attrs: [integration]
`

// newC3Fixture is newC4Fixture on c3Lane.
func newC3Fixture(t *testing.T, wi string) *c4Fixture {
	t.Helper()
	f := newC4Fixture(t, wi)
	workflowtest.WriteLane(t, filepath.Join(f.capDir, capsule.DirWorkflows), "develop", c3Lane)
	return f
}

// writeC3Result writes a minimal pf-step-result/1 file at rel (capsule
// relative) with verdict and heads. The exit code follows the verdict.
func writeC3Result(t *testing.T, capDir, rel, verdict string, heads map[string]string) {
	t.Helper()
	exit := 0
	switch verdict {
	case "fail":
		exit = 1
	case "error":
		exit = 2
	}
	raw, err := json.Marshal(map[string]any{
		"schema": "pf-step-result/1", "command": "test", "version": "test",
		"argv": []string{}, "round": "1",
		"started_at": "2026-11-21T00:00:00Z", "ended_at": "2026-11-21T00:00:01Z",
		"exit_code": exit, "verdict": verdict, "heads": heads, "summary": "",
		"details": map[string]any{}, "logs": []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(capDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

// writeC3Raw writes raw bytes at rel (capsule relative).
func writeC3Raw(t *testing.T, capDir, rel, body string) {
	t.Helper()
	p := filepath.Join(capDir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFastCheckProofRefusesDoneWithoutAResult(t *testing.T) {
	f := newC3Fixture(t, "wi_c3a")
	f.as("build", 1)
	before := f.handoffDoc(t)
	code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()})
	if code != 400 {
		t.Fatalf("build without a result = %d %s, want 400", code, body)
	}
	for _, want := range []string{
		"repos/app",
		"no fast-check result at artifacts/build/fast-checks/app.json",
		"run-fast-check --repo app --out artifacts/build/fast-checks/app.json",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the refusal does not carry %q:\n%s", want, body)
		}
	}
	if f.handoffDoc(t) != before {
		t.Errorf("a refused hand-off wrote the document")
	}
}

func TestFastCheckProofRefusesAnUnreadableResult(t *testing.T) {
	for _, c := range []struct{ name, body string }{
		{"a torn JSON file", "{"},
		{"another schema tag", `{"schema":"x"}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newC3Fixture(t, "wi_c3b"+strings.ReplaceAll(c.name, " ", "_"))
			f.as("build", 1)
			writeC3Raw(t, f.capDir, "artifacts/build/fast-checks/app.json", c.body)
			code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()})
			if code != 400 || !strings.Contains(body, "repos/app") || !strings.Contains(body, "is not a pf-step-result/1 result") {
				t.Fatalf("an unreadable result = %d %s", code, body)
			}
		})
	}
}

func TestFastCheckProofNamesEveryFailingRepo(t *testing.T) {
	f := newC3Fixture(t, "wi_c3c")
	lib := filepath.Join(f.capDir, capsule.DirRepos, "lib")
	if err := os.MkdirAll(lib, 0o755); err != nil {
		t.Fatal(err)
	}
	c4Git(t, lib, "init", "-q")
	if err := os.WriteFile(filepath.Join(lib, "l.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c4Git(t, lib, "add", ".")
	c4Git(t, lib, "commit", "-q", "-m", "one")
	l1 := c4Git(t, lib, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(lib, "l.txt"), []byte("two\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c4Git(t, lib, "add", ".")
	c4Git(t, lib, "commit", "-q", "-m", "two")
	l2 := c4Git(t, lib, "rev-parse", "HEAD")
	f.writeExec(t, map[string]*capsule.Step{
		"build": {Status: capsule.StepInProgress, Round: 1, StartHeads: &capsule.StartHeads{Round: 1, Heads: map[string]string{"app": f.c1, "lib": l1}}},
	})
	writeC3Result(t, f.capDir, "artifacts/build/fast-checks/lib.json", "pass", map[string]string{"lib": l2})
	f.as("build", 1)
	code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()})
	if code != 400 || !strings.Contains(body, "repos/app") {
		t.Fatalf("only app fails = %d %s", code, body)
	}
	if strings.Contains(body, "repos/lib") {
		t.Errorf("a repo whose result passes at HEAD is named:\n%s", body)
	}
}

func TestFastCheckProofLeavesUndeclaredStepsAlone(t *testing.T) {
	f := newC3Fixture(t, "wi_c3d")
	steps := map[string]*capsule.Step{}
	for _, id := range []string{"build", "review", "sync", "verification", "publish", "merge"} {
		steps[id] = &capsule.Step{Status: capsule.StepInProgress, Round: 1, StartHeads: &capsule.StartHeads{Round: 1, Heads: map[string]string{"app": f.c1}}}
	}
	f.writeExec(t, steps)
	for _, id := range []string{"review", "verification", "publish"} {
		f.as(id, 1)
		if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 200 {
			t.Fatalf("%s = %d %s", id, code, body)
		}
	}
	f.as("merge", 1)
	if code, body := c4Call(t, f.td, "hub.wrap-attrs", map[string]any{"name": "integration", "value": json.RawMessage(`{"state":"landed"}`)}); code != 200 {
		t.Fatalf("merge wrap-attrs = %d %s", code, body)
	}
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 200 {
		t.Fatalf("merge = %d %s", code, body)
	}
	// the counterexample: the same tree and inputs on build, which declares
	// the proof, is refused
	f.as("build", 1)
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 400 || !strings.Contains(body, "repos/app") {
		t.Fatalf("build on the same inputs = %d %s, want 400 naming repos/app", code, body)
	}
}

func TestFastCheckProofRefusesARedResult(t *testing.T) {
	for _, c := range []struct{ name, verdict string }{
		{"a failing result", "fail"},
		{"an errored result", "error"},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newC3Fixture(t, "wi_c3v"+c.verdict)
			f.as("build", 1)
			writeC3Result(t, f.capDir, "artifacts/build/fast-checks/app.json", c.verdict, map[string]string{"app": f.c2})
			code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()})
			if code != 400 {
				t.Fatalf("build with a %s result = %d %s, want 400", c.verdict, code, body)
			}
			for _, want := range []string{"repos/app", "has verdict " + c.verdict + ", not pass", "run-fast-check --repo app --out artifacts/build/fast-checks/app.json"} {
				if !strings.Contains(body, want) {
					t.Errorf("the refusal does not carry %q:\n%s", want, body)
				}
			}
		})
	}
}

func TestFastCheckProofRefusesAStaleHead(t *testing.T) {
	for _, c := range []struct {
		name  string
		heads func(f *c4Fixture) map[string]string
		want  func(f *c4Fixture) string
	}{
		{"a result from the start commit", func(f *c4Fixture) map[string]string { return map[string]string{"app": f.c1} },
			func(f *c4Fixture) string { return "ran at " + f.c1 }},
		{"a result with no head for the repo", func(f *c4Fixture) map[string]string { return map[string]string{"other": f.c2} },
			func(f *c4Fixture) string { return "ran at no head of repos/app" }},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newC3Fixture(t, "wi_c3h"+strings.ReplaceAll(c.name, " ", "_"))
			f.as("build", 1)
			writeC3Result(t, f.capDir, "artifacts/build/fast-checks/app.json", "pass", c.heads(f))
			code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()})
			if code != 400 {
				t.Fatalf("build at a stale head = %d %s, want 400", code, body)
			}
			for _, w := range []string{"repos/app", c.want(f), "but HEAD is " + f.c2, "run-fast-check --repo app --out artifacts/build/fast-checks/app.json"} {
				if !strings.Contains(body, w) {
					t.Errorf("the refusal does not carry %q:\n%s", w, body)
				}
			}
		})
	}
}

func TestFastCheckProofSkipsAnUnchangedRepo(t *testing.T) {
	f := newC3Fixture(t, "wi_c3u")
	// the fixture's sync round starts at c2, and HEAD is still c2
	f.as("sync", 1)
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 200 {
		t.Fatalf("sync at its start commit = %d %s, want 200", code, body)
	}
}

func TestFastCheckProofSkipsAFastForward(t *testing.T) {
	for _, c := range []struct {
		name, origin string
		want         int
	}{
		{"a fast-forward", "head", 200},
		{"a local commit no remote carries", "start", 400},
	} {
		t.Run(c.name, func(t *testing.T) {
			f := newC3Fixture(t, "wi_c3f"+strings.ReplaceAll(c.name, " ", "_"))
			f.writeExec(t, map[string]*capsule.Step{
				"sync": {Status: capsule.StepInProgress, Round: 1, StartHeads: &capsule.StartHeads{Round: 1, Heads: map[string]string{"app": f.c1}}},
			})
			ref := f.c2
			if c.origin == "start" {
				ref = f.c1
			}
			c4Git(t, filepath.Join(f.capDir, capsule.DirRepos, "app"), "update-ref", "refs/remotes/origin/main", ref)
			f.as("sync", 1)
			code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()})
			if code != c.want {
				t.Fatalf("%s = %d %s, want %d", c.name, code, body, c.want)
			}
			if c.want == 400 && !strings.Contains(body, "repos/app") {
				t.Errorf("the refusal does not name repos/app:\n%s", body)
			}
		})
	}
}

func TestFastCheckProofUnknownStartNeedsAResult(t *testing.T) {
	f := newC3Fixture(t, "wi_c3n")
	// the round's own StartHeads are missing: the recorded ones are round 0's
	f.writeExec(t, map[string]*capsule.Step{
		"build": {Status: capsule.StepInProgress, Round: 1, StartHeads: &capsule.StartHeads{Round: 0, Heads: map[string]string{"app": f.c1}}},
	})
	f.as("build", 1)
	code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()})
	if code != 400 || !strings.Contains(body, "repos/app") || !strings.Contains(body, "no fast-check result") {
		t.Fatalf("an unknown start = %d %s, want 400 naming repos/app", code, body)
	}
}

// c3WriteFile writes body at dir/rel, the parent created.
func c3WriteFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// c3GitAllow runs git and answers its output and exit code without failing
// the test (a conflicting merge exits 1).
func c3GitAllow(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err == nil {
		return string(out), 0
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return string(out), ee.ExitCode()
	}
	t.Fatalf("git %v: %v\n%s", args, err, out)
	return "", -1
}

// TestFastCheckProofSyncStrayBrace is AC4: #147's shape end to end over the
// production hand-off path. A merge whose resolution carries a stray brace
// is red in the fast check, refused by sync's done; the fix committed but
// the check not re-run leaves the result at the merge commit and is
// refused; the check re-run at the fix is accepted.
func TestFastCheckProofSyncStrayBrace(t *testing.T) {
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 is not on the PATH")
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	fastChecks := filepath.Join(repoRoot, "workflows", "_shared", "scripts", "fast_checks.py")
	if _, err := os.Stat(fastChecks); err != nil {
		t.Fatalf("the lane's fast_checks.py: %v", err)
	}
	f := newC3Fixture(t, "wi_c3e")
	app := filepath.Join(f.capDir, capsule.DirRepos, "app")
	if err := os.RemoveAll(app); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(app, 0o755); err != nil {
		t.Fatal(err)
	}
	c4Git(t, app, "init", "-q", "-b", "main")
	c3WriteFile(t, app, "main.go", "package main\n\nfunc main(){ println(\"base\") }\n")
	c4Git(t, app, "add", ".")
	c4Git(t, app, "commit", "-q", "-m", "base")
	c4Git(t, app, "checkout", "-q", "-b", "feature")
	c3WriteFile(t, app, "main.go", "package main\n\nfunc main(){ println(\"feature\") }\n")
	c4Git(t, app, "add", ".")
	c4Git(t, app, "commit", "-q", "-m", "feature")
	fHead := c4Git(t, app, "rev-parse", "HEAD")
	c4Git(t, app, "checkout", "-q", "main")
	c3WriteFile(t, app, "main.go", "package main\n\nfunc main(){ println(\"upstream\") }\n")
	c4Git(t, app, "add", ".")
	c4Git(t, app, "commit", "-q", "-m", "upstream")
	c4Git(t, app, "checkout", "-q", "feature")
	if out, code := c3GitAllow(t, app, "merge", "main"); code == 0 {
		t.Fatalf("the merge must conflict:\n%s", out)
	}
	c3WriteFile(t, app, "main.go", "package main\n\nfunc main(){\n\tprintln(\"feature\")\n\tprintln(\"upstream\")\n}\n}\n")
	c4Git(t, app, "add", "main.go")
	c4Git(t, app, "commit", "-q", "--no-edit")
	merged := c4Git(t, app, "rev-parse", "HEAD")

	engineDir := t.TempDir()
	report, err := json.Marshal(map[string]any{
		"schema": 1, "root": "/x", "stack": "none", "analyzer": nil, "conf": "present",
		"conf_changed_in_diff": false, "base": "b", "head": "h", "default_branch": "main",
		"mode": "scoped", "light": true, "paths": []string{"main.go"},
		"commands": []map[string]any{{
			"name": "brace", "phase": "fast",
			"cmd":          `python3 -I -c "import sys;s=open('main.go').read();sys.exit(0 if s.count('{')==s.count('}') else 1)"`,
			"requires_env": []string{}, "requires_cmd": []string{}, "timeout": "1m", "report": nil, "source": "conf",
		}},
		"test_glob": []string{}, "base_setup": []string{}, "runner": nil, "diagnostics": []string{},
	})
	if err != nil {
		t.Fatal(err)
	}
	c3WriteFile(t, engineDir, "report.json", string(report))
	engine := filepath.Join(engineDir, "verify-scope")
	if err := os.WriteFile(engine, []byte("#!/bin/bash\ncat \"$(dirname \"$0\")/report.json\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	run := func() (int, map[string]any) {
		t.Helper()
		cmd := exec.Command(python, "-I", fastChecks, "--repo", "app", "--phase", "fast",
			"--capsule", f.capDir, "--engine", engine, "--json", "--out", "artifacts/sync/fast-checks/app.json")
		cmd.Dir = f.capDir
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			ee, ok := err.(*exec.ExitError)
			if !ok {
				t.Fatalf("fast_checks.py: %v\n%s", err, out)
			}
			code = ee.ExitCode()
		}
		raw, err := os.ReadFile(filepath.Join(f.capDir, "artifacts", "sync", "fast-checks", "app.json"))
		if err != nil {
			t.Fatalf("the fast check wrote no result: %v\n%s", err, out)
		}
		var r map[string]any
		if err := json.Unmarshal(raw, &r); err != nil {
			t.Fatalf("the fast check's result: %v\n%s", err, raw)
		}
		return code, r
	}
	headOf := func(r map[string]any) string {
		heads, _ := r["heads"].(map[string]any)
		s, _ := heads["app"].(string)
		return s
	}

	exit, r := run()
	if exit != 1 || r["verdict"] != "fail" || headOf(r) != merged {
		t.Fatalf("the stray brace: exit %d verdict %v heads %s, want 1/fail/%s", exit, r["verdict"], headOf(r), merged)
	}

	f.writeExec(t, map[string]*capsule.Step{
		"sync": {Status: capsule.StepInProgress, Round: 1, StartHeads: &capsule.StartHeads{Round: 1, Heads: map[string]string{"app": fHead}}},
	})
	f.as("sync", 1)
	code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()})
	if code != 400 || !strings.Contains(body, "repos/app") || !strings.Contains(body, "has verdict fail, not pass") {
		t.Fatalf("sync with the stray brace = %d %s, want 400 on the verdict", code, body)
	}

	c3WriteFile(t, app, "main.go", "package main\n\nfunc main(){\n\tprintln(\"feature\")\n\tprintln(\"upstream\")\n}\n")
	c4Git(t, app, "add", "main.go")
	c4Git(t, app, "commit", "-q", "-m", "drop the stray brace")
	fixed := c4Git(t, app, "rev-parse", "HEAD")
	code, body = c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()})
	if code != 400 || !strings.Contains(body, "ran at "+merged) || !strings.Contains(body, "but HEAD is "+fixed) {
		t.Fatalf("sync after the fix without a re-run = %d %s, want 400 on the stale head", code, body)
	}

	exit, r = run()
	if exit != 0 || r["verdict"] != "pass" || headOf(r) != fixed {
		t.Fatalf("the re-run after the fix: exit %d verdict %v heads %s, want 0/pass/%s", exit, r["verdict"], headOf(r), fixed)
	}
	if code, body := c4Call(t, f.td, "handoff", map[string]any{"status": "done", "body": c4Body()}); code != 200 {
		t.Fatalf("sync with a passing result at HEAD = %d %s, want 200", code, body)
	}
}
