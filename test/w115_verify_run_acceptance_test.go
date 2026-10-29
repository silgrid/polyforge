package test

// w115_verify_run_acceptance_test.go: `pf verify-run`, the verification
// step's mechanical half, driven end to end through the real pf binary
// over a throwaway capsule: a Go task repo cloned from a bare origin, a
// fake scope engine answering a canned `--json` report (a script beside
// the test, the way fakepi stands in for a model), and real `go test`
// runs through the green, red, flaky and pre-existing paths.
//
// Run (targeted): GOWORK=off go test ./test/ -run 'W115' -count=1 -v

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// w115Result is the slice of result.json the scenarios assert on.
type w115Result struct {
	MechanicalVerdict string `json:"mechanical_verdict"`
	FailReason        string `json:"fail_reason"`
	Incomplete        string `json:"incomplete"`
	Checks            []struct {
		Name, Status, Detail string
	} `json:"checks"`
	PreExisting []string `json:"pre_existing_failures"`
	TestBar     []struct {
		Repo, Command string
	} `json:"test_bar"`
	CommandEvidence []struct {
		Argv       []string `json:"argv"`
		Cwd        string   `json:"cwd"`
		ExitCode   int      `json:"exit_code"`
		StartedAt  string   `json:"started_at"`
		DurationMs int64    `json:"duration_ms"`
		LogPath    string   `json:"log_path"`
	} `json:"command_evidence"`
	MergeOutcomes map[string]struct {
		Outcome string `json:"outcome"`
		Touched bool   `json:"touched"`
		Base    string `json:"base"`
	} `json:"merge_outcomes"`
	Units []struct {
		Repo, Check, Kind, Name, File, Status, Detail string
	} `json:"units"`
	BoundScenarios []struct {
		Title, Status, Detail string
	} `json:"bound_scenarios"`
	UnboundTitles []string        `json:"unbound_titles"`
	ConfChanged   map[string]bool `json:"conf_changed_in_diff"`
}

func (r *w115Result) check(t *testing.T, name string) (status, detail string) {
	t.Helper()
	for _, c := range r.Checks {
		if c.Name == name {
			return c.Status, c.Detail
		}
	}
	t.Fatalf("no check %q in %+v", name, r.Checks)
	return "", ""
}

func (r *w115Result) unit(t *testing.T, name string) (status, detail string) {
	t.Helper()
	for _, u := range r.Units {
		if u.Name == name {
			return u.Status, u.Detail
		}
	}
	t.Fatalf("no unit %q in %+v", name, r.Units)
	return "", ""
}

func w115Git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=w115", "GIT_AUTHOR_EMAIL=w115@example.invalid",
		"GIT_COMMITTER_NAME=w115", "GIT_COMMITTER_EMAIL=w115@example.invalid")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func w115Write(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func w115Commit(t *testing.T, dir string, files map[string]string, msg string) {
	t.Helper()
	w115Write(t, dir, files)
	w115Git(t, dir, "add", "-A")
	w115Git(t, dir, "commit", "-q", "-m", msg)
}

// w115Base is the origin's main: a tiny Go module.
var w115Base = map[string]string{
	"go.mod":  "module example.invalid/w115app\n\ngo 1.21\n",
	"calc.go": "package w115app\n\n// Add adds.\nfunc Add(a, b int) int { return a + b }\n",
}

// w115Capsule builds a capsule whose repos each clone a bare origin
// seeded with base files; feature commits land on the clone's feature
// branch. It answers the capsule dir and each repo's seed clone (which
// pushes to its origin).
func w115Capsule(t *testing.T, repos map[string]map[string]string) (string, map[string]string) {
	t.Helper()
	for _, bin := range []string{"git", "go", "bash"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not installed", bin)
		}
	}
	root := t.TempDir()
	capsuleDir := filepath.Join(root, "capsule")
	seeds := map[string]string{}
	ids := map[string]string{}
	for name, files := range repos {
		origin := filepath.Join(root, name+".git")
		w115Git(t, root, "init", "-q", "--bare", "-b", "main", origin)
		seed := filepath.Join(root, "seed-"+name)
		w115Git(t, root, "clone", "-q", origin, seed)
		w115Git(t, seed, "checkout", "-q", "-B", "main")
		w115Commit(t, seed, files, "base")
		w115Git(t, seed, "push", "-q", "origin", "main")
		clone := filepath.Join(capsuleDir, "repos", name)
		w115Git(t, root, "clone", "-q", origin, clone)
		w115Git(t, clone, "config", "user.email", "w115@example.invalid")
		w115Git(t, clone, "config", "user.name", "w115")
		w115Git(t, clone, "checkout", "-q", "-b", "feature")
		ids[name] = w115Git(t, clone, "rev-parse", "HEAD")
		seeds[name] = seed
	}
	raw, _ := json.Marshal(map[string]any{"work_item_id": "wi_w115", "bundle_name": "feature-full", "repos": ids})
	w115Write(t, capsuleDir, map[string]string{"capsule.json": string(raw)})
	return capsuleDir, seeds
}

// w115Engine writes the fake scope engine: a script answering the canned
// report of the repo it runs in (by the working directory's name), the
// contract's --json shape; a repo without a report fails it.
func w115Engine(t *testing.T, conf map[string]bool, cmds map[string][][3]string) string {
	t.Helper()
	dir := t.TempDir()
	for repo, list := range cmds {
		var commands []map[string]any
		for _, c := range list {
			commands = append(commands, map[string]any{"name": c[0], "phase": c[1], "cmd": c[2],
				"requires_env": []string{}, "requires_cmd": []string{}, "timeout": "5m", "source": "conf"})
		}
		raw, _ := json.Marshal(map[string]any{"schema": 1, "root": "/x", "stack": "go", "analyzer": "go",
			"conf": "present", "conf_changed_in_diff": conf[repo], "base": "b", "head": "h", "default_branch": "main",
			"mode": "full", "paths": []string{"calc.go"}, "commands": commands,
			"test_glob": []string{}, "base_setup": []string{}, "runner": nil, "ci_evidence": []string{}, "diagnostics": []string{}})
		w115Write(t, dir, map[string]string{repo + ".json": string(raw)})
	}
	script := "#!/bin/bash\nf=\"$(dirname \"$0\")/$(basename \"$PWD\").json\"\n[ -f \"$f\" ] || { echo \"no report for $PWD\" >&2; exit 3; }\ncat \"$f\"\n"
	p := filepath.Join(dir, "verify-scope")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// w115Run runs `pf verify-run` at the capsule root and reads result.json.
func w115Run(t *testing.T, capsuleDir, engine string, env ...string) (*w115Result, string) {
	t.Helper()
	bins := v1Bins(t)
	c := exec.Command(filepath.Join(bins, "pf"), "verify-run", "--tier", "full", "--engine", engine)
	c.Dir = capsuleDir
	// the outer harness's own selection must not leak into the fixture's
	c.Env = append(append(os.Environ(), "E2E_RUN=", "E2E_SKIP="), env...)
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("pf verify-run: %v\n%s", err, out)
	}
	var resultPath string
	for _, l := range strings.Split(string(out), "\n") {
		if p, ok := strings.CutPrefix(l, "verify-run: result "); ok {
			resultPath = filepath.Join(capsuleDir, p)
		}
	}
	raw, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("result.json (%q): %v\n%s", resultPath, err, out)
	}
	var res w115Result
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("result.json: %v\n%s", err, raw)
	}
	return &res, string(out)
}

// TestW115GreenBarUntouchedRepoAndBindings: a touched repo merged with a
// moved default branch runs its whole plan green; an untouched repo runs
// no bar; the bound scenario resolves from its own authored run; the
// evidence has the envelope's shape and every log stays out of artifacts/.
func TestW115GreenBarUntouchedRepoAndBindings(t *testing.T) {
	capsuleDir, seeds := w115Capsule(t, map[string]map[string]string{"app": w115Base, "docs": {"README.md": "docs\n"}})
	app := filepath.Join(capsuleDir, "repos", "app")
	w115Commit(t, app, map[string]string{"calc_test.go": `package w115app

import (
	"fmt"
	"testing"
)

func TestAdds(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatal("2+3 is not 5")
	}
	fmt.Println("PASS S1")
}
`}, "feature")
	w115Commit(t, seeds["app"], map[string]string{"NOTES.md": "upstream moved\n"}, "upstream")
	w115Git(t, seeds["app"], "push", "-q", "origin", "main")
	bound := "go test -count=1 -v -run '^TestAdds$' ./..."
	execJSON, _ := json.Marshal(map[string]any{"steps": map[string]any{"e2e-author": map[string]any{"status": "done", "output": map[string]any{
		"scenario_titles": []string{"S1 adds", "S2 is a document"},
		"test_files":      []string{"calc_test.go"},
		"bindings":        []map[string]string{{"title": "S1 adds", "repo": "app", "command": bound, "unit": "TestAdds", "marker": "^PASS S1$"}},
	}}}})
	w115Write(t, capsuleDir, map[string]string{"runtime-state/execution.json": string(execJSON)})
	engine := w115Engine(t, map[string]bool{"app": true}, map[string][][3]string{"app": {
		{"fast-1", "fast", "go vet ./..."}, {"full-1", "full", "go test -count=1 ./..."}}})

	res, out := w115Run(t, capsuleDir, engine)
	if res.MechanicalVerdict != "pass" || res.Incomplete != "" {
		t.Fatalf("verdict %q incomplete %q: %s\n%s", res.MechanicalVerdict, res.Incomplete, res.FailReason, out)
	}
	if m := res.MergeOutcomes["app"]; m.Outcome != "merge-commit" || !m.Touched {
		t.Errorf("app merge = %+v", m)
	}
	if m := res.MergeOutcomes["docs"]; m.Outcome != "up-to-date" || m.Touched {
		t.Errorf("docs merge = %+v", m)
	}
	for name, want := range map[string]string{"app:fast-1": "pass", "app:full-1": "pass", "app:authored-1": "pass",
		"docs:untouched": "skipped", "app:conf-changed": "skipped", "app:no-untracked-test-files": "pass", "test-files-present": "pass"} {
		if got, detail := res.check(t, name); got != want {
			t.Errorf("check %s = %s (%s), want %s", name, got, detail, want)
		}
	}
	wantBar := []string{"go vet ./...", "go test -count=1 ./...", bound}
	if len(res.TestBar) != len(wantBar) {
		t.Fatalf("test_bar = %+v", res.TestBar)
	}
	for i, w := range wantBar {
		if res.TestBar[i].Repo != "app" || res.TestBar[i].Command != w {
			t.Errorf("test_bar[%d] = %+v, want app %q", i, res.TestBar[i], w)
		}
	}
	if len(res.BoundScenarios) != 1 || res.BoundScenarios[0].Status != "pass" || len(res.UnboundTitles) != 1 || res.UnboundTitles[0] != "S2 is a document" {
		t.Errorf("bound %+v unbound %v", res.BoundScenarios, res.UnboundTitles)
	}
	var bash int
	for _, e := range res.CommandEvidence {
		if len(e.Argv) == 0 || e.DurationMs < 1 || !strings.HasPrefix(e.LogPath, "runtime-state/verify/1/") {
			t.Errorf("evidence %+v", e)
		}
		if _, err := time.Parse(time.RFC3339, e.StartedAt); err != nil {
			t.Errorf("started_at %q is not RFC3339", e.StartedAt)
		}
		if e.Argv[0] == "bash" && e.Argv[1] == "-c" {
			bash++
			if e.Cwd != "repos/app" {
				t.Errorf("bar evidence cwd %q", e.Cwd)
			}
		}
	}
	if bash != 3 {
		t.Errorf("bash -c evidence entries = %d, want the 3 bar commands", bash)
	}
	if _, err := os.Stat(filepath.Join(capsuleDir, "artifacts")); err == nil {
		t.Error("pf verify-run wrote under artifacts/")
	}
	if raw, err := os.ReadFile(filepath.Join(capsuleDir, "runtime-state", "verify", "1", "report-mechanical.md")); err != nil || !strings.Contains(string(raw), "Mechanical verdict: **pass**") {
		t.Errorf("report-mechanical.md: %v\n%s", err, raw)
	}
}

// TestW115RedBarRunsTheWholePlan: a failing command never stops the plan;
// a test the base does not have and a test failing differently on the
// base are this change's failures, never pre-existing; red exits 0.
func TestW115RedBarRunsTheWholePlan(t *testing.T) {
	base := map[string]string{"changed_test.go": "package w115app\n\nimport \"testing\"\n\nfunc TestChanged(t *testing.T) { t.Fatal(\"alpha breakage\") }\n"}
	for k, v := range w115Base {
		base[k] = v
	}
	capsuleDir, _ := w115Capsule(t, map[string]map[string]string{"app": base})
	app := filepath.Join(capsuleDir, "repos", "app")
	w115Commit(t, app, map[string]string{
		"new_test.go":     "package w115app\n\nimport \"testing\"\n\nfunc TestNew(t *testing.T) { t.Fatal(\"new breakage\") }\n",
		"changed_test.go": "package w115app\n\nimport \"testing\"\n\nfunc TestChanged(t *testing.T) { t.Fatal(\"beta breakage\") }\n",
	}, "feature")
	engine := w115Engine(t, nil, map[string][][3]string{"app": {
		{"full-1", "full", "go test -count=1 ./..."}, {"full-2", "full", "echo the plan went on"}}})
	res, out := w115Run(t, capsuleDir, engine)
	if res.MechanicalVerdict != "fail" || !strings.Contains(res.FailReason, "TestNew") {
		t.Fatalf("verdict %q fail_reason %q\n%s", res.MechanicalVerdict, res.FailReason, out)
	}
	if st, _ := res.check(t, "app:full-1"); st != "fail" {
		t.Errorf("full-1 = %s", st)
	}
	if st, _ := res.check(t, "app:full-2"); st != "pass" {
		t.Errorf("the plan stopped at the first failure: full-2 = %s", st)
	}
	if st, d := res.unit(t, "TestNew"); st != "fail" || !strings.Contains(d, "base does not have this test") {
		t.Errorf("TestNew = %s (%s)", st, d)
	}
	if st, d := res.unit(t, "TestChanged"); st != "fail" || !strings.Contains(d, "fails differently") {
		t.Errorf("TestChanged = %s (%s)", st, d)
	}
	if len(res.PreExisting) != 0 {
		t.Errorf("pre_existing_failures = %v", res.PreExisting)
	}
}

// TestW115FlakyUnitPassesOnRerun: a unit failing once and passing on its
// targeted re-run at HEAD is a pass recorded `flaky n/m`.
func TestW115FlakyUnitPassesOnRerun(t *testing.T) {
	capsuleDir, _ := w115Capsule(t, map[string]map[string]string{"app": w115Base})
	w115Commit(t, filepath.Join(capsuleDir, "repos", "app"), map[string]string{"wobbly_test.go": `package w115app

import (
	"os"
	"testing"
)

func TestWobbly(t *testing.T) {
	state := os.Getenv("W115_FLAKY_STATE")
	if _, err := os.Stat(state); err != nil {
		os.WriteFile(state, []byte("x"), 0o644)
		t.Fatal("first run wobble")
	}
}
`}, "feature")
	engine := w115Engine(t, nil, map[string][][3]string{"app": {{"full-1", "full", "go test -count=1 ./..."}}})
	res, out := w115Run(t, capsuleDir, engine, "W115_FLAKY_STATE="+filepath.Join(t.TempDir(), "state"))
	if res.MechanicalVerdict != "pass" {
		t.Fatalf("verdict %q: %s\n%s", res.MechanicalVerdict, res.FailReason, out)
	}
	if st, d := res.check(t, "app:full-1"); st != "pass" || !strings.Contains(d, "flaky 1/2") {
		t.Errorf("full-1 = %s (%s)", st, d)
	}
	if st, _ := res.unit(t, "TestWobbly"); st != "flaky" {
		t.Errorf("TestWobbly = %s", st)
	}
	var rerun bool
	for _, e := range res.CommandEvidence {
		rerun = rerun || (len(e.Argv) == 3 && strings.Contains(e.Argv[2], "-run '^TestWobbly$'"))
	}
	if !rerun {
		t.Error("the targeted re-run carries no command evidence")
	}
}

// TestW115PreExistingFailureProvenOnBase: a test failing on the base the
// same way is pre-existing — its check, its entry, the base sha — and the
// derived test-bar command excludes it and passes when re-run.
func TestW115PreExistingFailureProvenOnBase(t *testing.T) {
	base := map[string]string{"old_test.go": "package w115app\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) { t.Fatal(\"old breakage\") }\n"}
	for k, v := range w115Base {
		base[k] = v
	}
	capsuleDir, _ := w115Capsule(t, map[string]map[string]string{"app": base})
	app := filepath.Join(capsuleDir, "repos", "app")
	w115Commit(t, app, map[string]string{"sub.go": "package w115app\n\n// Sub subtracts.\nfunc Sub(a, b int) int { return a - b }\n",
		"sub_test.go": "package w115app\n\nimport \"testing\"\n\nfunc TestSub(t *testing.T) {\n\tif Sub(3, 2) != 1 {\n\t\tt.Fatal(\"3-2\")\n\t}\n}\n"}, "feature")
	engine := w115Engine(t, nil, map[string][][3]string{"app": {{"full-1", "full", "go test -count=1 ./..."}}})
	res, out := w115Run(t, capsuleDir, engine)
	if res.MechanicalVerdict != "pass" {
		t.Fatalf("verdict %q: %s\n%s", res.MechanicalVerdict, res.FailReason, out)
	}
	baseSHA := res.MergeOutcomes["app"].Base
	// go test proves nothing about its own completion: the explained
	// failure is confirmed by re-running the whole command in its skip form
	if st, d := res.check(t, "app:full-1"); st != "pre_existing" || !strings.Contains(d, baseSHA) ||
		!strings.Contains(d, "re-run whole as `go test -count=1 ./... -skip '^(TestOld)$'`: exit 0") {
		t.Errorf("full-1 = %s (%s), want pre_existing naming %s, confirmed in its skip form", st, d, baseSHA)
	}
	if len(res.PreExisting) != 1 || res.PreExisting[0] != "old_test.go: TestOld" {
		t.Errorf("pre_existing_failures = %v", res.PreExisting)
	}
	if len(res.TestBar) != 1 || res.TestBar[0].Command != "go test -count=1 ./... -skip '^(TestOld)$'" {
		t.Fatalf("test_bar = %+v", res.TestBar)
	}
	c := exec.Command("bash", "-c", res.TestBar[0].Command)
	c.Dir = app
	if out, err := c.CombinedOutput(); err != nil {
		t.Errorf("the derived test-bar command fails: %v\n%s", err, out)
	}
	if list := w115Git(t, app, "worktree", "list"); strings.Count(list, "\n") != 0 {
		t.Errorf("the base worktree was not removed:\n%s", list)
	}
}

// TestW115SignaturesCompareLikeWithLike: the pre-existing signature is the
// whole failure from the same targeted -v command on both sides — a
// changed message after an unchanged t.Log, or a changed message digit, is
// this change's failure; an unchanged red test whose failing subtest is
// one of several is pre-existing.
func TestW115SignaturesCompareLikeWithLike(t *testing.T) {
	base := map[string]string{
		"logged_test.go": "package w115app\n\nimport \"testing\"\n\nfunc TestLogged(t *testing.T) {\n\tt.Log(\"setting up\")\n\tt.Fatal(\"old breakage\")\n}\n",
		"digits_test.go": "package w115app\n\nimport \"testing\"\n\nfunc TestDigits(t *testing.T) { t.Fatalf(\"got %d, want 4\", 3) }\n",
		"subs_test.go":   "package w115app\n\nimport \"testing\"\n\nfunc TestSubs(t *testing.T) {\n\tt.Run(\"one\", func(t *testing.T) { t.Fatal(\"sub broken\") })\n\tt.Run(\"two\", func(t *testing.T) {})\n}\n",
	}
	for k, v := range w115Base {
		base[k] = v
	}
	capsuleDir, _ := w115Capsule(t, map[string]map[string]string{"app": base})
	w115Commit(t, filepath.Join(capsuleDir, "repos", "app"), map[string]string{
		"logged_test.go": "package w115app\n\nimport \"testing\"\n\nfunc TestLogged(t *testing.T) {\n\tt.Log(\"setting up\")\n\tt.Fatal(\"new breakage\")\n}\n",
		"digits_test.go": "package w115app\n\nimport \"testing\"\n\nfunc TestDigits(t *testing.T) { t.Fatalf(\"got %d, want 4\", 5) }\n",
		"feature.go":     "package w115app\n\n// Twice doubles.\nfunc Twice(a int) int { return 2 * a }\n",
	}, "feature")
	engine := w115Engine(t, nil, map[string][][3]string{"app": {{"full-1", "full", "go test -count=1 ./..."}}})
	res, out := w115Run(t, capsuleDir, engine)
	for name, want := range map[string]string{"TestLogged": "fail", "TestDigits": "fail", "TestSubs": "pre_existing"} {
		if st, d := res.unit(t, name); st != want {
			t.Errorf("%s = %s (%s), want %s\n%s", name, st, d, want, out)
		}
	}
	if len(res.PreExisting) != 1 || res.PreExisting[0] != "subs_test.go: TestSubs" {
		t.Errorf("pre_existing_failures = %v", res.PreExisting)
	}
	if st, _ := res.check(t, "app:full-1"); st != "fail" {
		t.Errorf("full-1 = %s, want fail (two new failures)", st)
	}
}

// TestW115PreExistingWithoutSkipFormStaysFailed: a command that cannot
// exclude its pre-existing failure (here a list running a second program)
// never passes on it: the rest of the command was never shown to pass.
func TestW115PreExistingWithoutSkipFormStaysFailed(t *testing.T) {
	base := map[string]string{"old_test.go": "package w115app\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) { t.Fatal(\"old breakage\") }\n",
		"e2e.sh": "exit 0\n"}
	for k, v := range w115Base {
		base[k] = v
	}
	capsuleDir, _ := w115Capsule(t, map[string]map[string]string{"app": base})
	w115Commit(t, filepath.Join(capsuleDir, "repos", "app"), map[string]string{"e2e.sh": "echo e2e broke; exit 1\n"}, "feature")
	engine := w115Engine(t, nil, map[string][][3]string{"app": {{"full-1", "full", "go test -count=1 ./... && sh e2e.sh"}}})
	res, out := w115Run(t, capsuleDir, engine)
	if st, d := res.check(t, "app:full-1"); st != "fail" || !strings.Contains(d, "no skip switch") {
		t.Errorf("full-1 = %s (%s)\n%s", st, d, out)
	}
	if res.MechanicalVerdict != "fail" {
		t.Errorf("verdict = %s", res.MechanicalVerdict)
	}
}

// w115Harness is a miniature of the Go e2e harness protocol: one `go test`
// per listed top-level test, E2E_RUN / E2E_SKIP selection, `FAIL <Test>`
// lines, dumped logs and the `ok|FAIL\te2e\t` trailer counting
// ran/listed and the E2E_SKIP exclusions. W115_LOSE=1 makes it lose one
// test (a run that did not complete).
const w115Harness = `#!/bin/sh
work=$(mktemp -d); trap 'rm -rf "$work"' EXIT
go test -c -o "$work/t.test" . || exit 1
"$work/t.test" -test.list "${E2E_RUN:-.}" | grep '^Test' >"$work/listed"
if [ -n "${E2E_SKIP:-}" ]; then grep -Ev -- "$E2E_SKIP" "$work/listed" >"$work/names" || true; else cp "$work/listed" "$work/names"; fi
listed=$(wc -l <"$work/listed" | tr -d ' '); total=$(wc -l <"$work/names" | tr -d ' ')
excluded=$((listed - total)); note=""; [ "$excluded" -eq 0 ] || note=", $excluded excluded by E2E_SKIP"
ran=0; failed=""
for t in $(cat "$work/names"); do
	ran=$((ran + 1))
	if ! "$work/t.test" -test.run "^$t\$" -test.v >"$work/$t.log" 2>&1; then echo "FAIL $t"; failed="$failed $t"; fi
done
[ "${W115_LOSE:-}" = 1 ] && ran=$((ran - 1))
if [ -n "$failed" ] || [ "$ran" -ne "$total" ]; then
	for t in $failed; do echo "=== output of $t"; cat "$work/$t.log"; done
	echo "FAIL	e2e	$ran/$listed tests ran$note, failed:$failed 	1s (E2E_JOBS=1)"
	exit 1
fi
echo "ok	e2e	$total tests (0 skipped$note)	1s (E2E_JOBS=1)"
`

// TestW115HarnessPreExistingSkipsWithE2ESkip: a harness run whose trailer
// proves every listed test ran, with its only failure pre-existing, is
// pre_existing; its bar entry excludes the test with E2E_SKIP, and that
// entry passes when re-run. A harness run that lost a test proves no
// completion and stays failed.
func TestW115HarnessPreExistingSkipsWithE2ESkip(t *testing.T) {
	base := map[string]string{
		".polyforge/verify-scope.conf": "go_harness=harness.sh\n",
		"harness.sh":                   w115Harness,
		"old_test.go":                  "package w115app\n\nimport \"testing\"\n\nfunc TestOld(t *testing.T) { t.Fatal(\"old breakage\") }\n",
		"add_test.go":                  "package w115app\n\nimport \"testing\"\n\nfunc TestAdd(t *testing.T) {\n\tif Add(1, 2) != 3 {\n\t\tt.Fatal(\"1+2\")\n\t}\n}\n",
	}
	for k, v := range w115Base {
		base[k] = v
	}
	capsuleDir, _ := w115Capsule(t, map[string]map[string]string{"app": base})
	app := filepath.Join(capsuleDir, "repos", "app")
	w115Commit(t, app, map[string]string{"twice.go": "package w115app\n\n// Twice doubles.\nfunc Twice(a int) int { return 2 * a }\n"}, "feature")
	engine := w115Engine(t, nil, map[string][][3]string{"app": {{"e2e", "full", "sh harness.sh"}}})

	res, out := w115Run(t, capsuleDir, engine)
	st, d := res.check(t, "app:e2e")
	if st != "pre_existing" || !strings.Contains(d, "complete: the harness trailer shows all 2 listed tests ran") {
		t.Fatalf("e2e = %s (%s)\n%s", st, d, out)
	}
	if len(res.PreExisting) != 1 || res.PreExisting[0] != "old_test.go: TestOld" {
		t.Errorf("pre_existing_failures = %v", res.PreExisting)
	}
	if len(res.TestBar) != 1 || res.TestBar[0].Command != "E2E_SKIP='^(TestOld)$' sh harness.sh" {
		t.Fatalf("test_bar = %+v", res.TestBar)
	}
	c := exec.Command("bash", "-c", res.TestBar[0].Command)
	c.Dir = app
	c.Env = append(os.Environ(), "E2E_RUN=")
	if out, err := c.CombinedOutput(); err != nil || !strings.Contains(string(out), "1 excluded by E2E_SKIP") {
		t.Errorf("the E2E_SKIP bar entry must pass and count the exclusion: %v\n%s", err, out)
	}

	res, out = w115Run(t, capsuleDir, engine, "W115_LOSE=1")
	if st, d := res.check(t, "app:e2e"); st != "fail" || strings.Contains(d, "complete:") {
		t.Errorf("a harness run that lost a test proves no completion: %s (%s)\n%s", st, d, out)
	}
}

// TestW115UnitLineRunsUnderAnOuterGoWork: this repository's declared unit
// line runs in a clone that sits below a go.work naming other modules (a
// capsule under a workspace): the inner `go list` needs its own GOWORK=off,
// or it lists nothing and go test degrades to a setup failure.
func TestW115UnitLineRunsUnderAnOuterGoWork(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go not installed")
	}
	raw, err := os.ReadFile(filepath.Join(v1Root(t), ".polyforge", "verify-scope.conf"))
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, l := range strings.Split(string(raw), "\n") {
		if v, ok := strings.CutPrefix(l, "full_command="); ok && strings.Contains(v, "go list") {
			line = v
		}
	}
	if line == "" {
		t.Fatal("the conf declares no go-list unit line")
	}
	root := t.TempDir()
	w115Write(t, root, map[string]string{
		"go.work":              "go 1.21\n\nuse ./other\n",
		"other/go.mod":         "module example.invalid/other\n\ngo 1.21\n",
		"app/go.mod":           "module example.invalid/w115ws\n\ngo 1.21\n",
		"app/a/a.go":           "package a\n\n// A answers.\nfunc A() int { return 1 }\n",
		"app/a/a_test.go":      "package a\n\nimport \"testing\"\n\nfunc TestA(t *testing.T) {\n\tif A() != 1 {\n\t\tt.Fatal(\"A\")\n\t}\n}\n",
		"app/test/e2e_test.go": "package test\n\nimport \"testing\"\n\nfunc TestHarnessOnly(t *testing.T) { t.Fatal(\"the unit line must not run the harness package\") }\n",
	})
	run := func(cmd string) (string, error) {
		c := exec.Command("bash", "-c", cmd)
		c.Dir = filepath.Join(root, "app")
		c.Env = append(os.Environ(), "GOWORK=")
		out, err := c.CombinedOutput()
		return string(out), err
	}
	out, err := run(line)
	if err != nil || !strings.Contains(out, "ok  \texample.invalid/w115ws/a") || strings.Contains(out, "setup failed") {
		t.Fatalf("the declared unit line fails under an outer go.work: %v\n%s", err, out)
	}
	// the control: without the inner GOWORK=off the same line breaks here
	if out, err := run(strings.Replace(line, "$(GOWORK=off go list", "$(go list", 1)); err == nil {
		t.Errorf("control: the line without the inner GOWORK=off unexpectedly passed:\n%s", out)
	}
}

// TestW115RealHarnessE2ESkip drives the REAL test/e2e-parallel.sh: an
// E2E_SKIP-excluded test does not run and the ok trailer counts it; an
// invalid extended regexp is refused before anything builds.
func TestW115RealHarnessE2ESkip(t *testing.T) {
	script := filepath.Join(v1Root(t), "test", "e2e-parallel.sh")
	run := func(env ...string) (string, error) {
		c := exec.Command("sh", script)
		c.Env = append(os.Environ(), env...)
		out, err := c.CombinedOutput()
		return string(out), err
	}
	out, err := run("E2E_SKIP=(")
	if err == nil || !strings.Contains(out, "E2E_SKIP is not a valid extended regexp") {
		t.Fatalf("an invalid E2E_SKIP must be refused: %v\n%s", err, out)
	}
	out, err = run("E2E_JOBS=1", "E2E_RUN=^TestW115(UnitLineRunsUnderAnOuterGoWork|FlakyUnitPassesOnRerun)$",
		"E2E_SKIP=^(TestW115FlakyUnitPassesOnRerun)$")
	if err != nil || !strings.Contains(out, "ok\te2e\t1 tests (0 skipped, 1 excluded by E2E_SKIP)") {
		t.Fatalf("E2E_SKIP run: %v\n%s", err, out)
	}
}
