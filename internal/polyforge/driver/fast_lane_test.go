package driver

// fast_lane_test.go: the verification-less delivery lane's machine
// boundary (fast_lane.go,): the micro_path glob grammar, the
// per-repo verdict over real git fixtures, the credential plan, and the
// pre-dispatch guard's scope (shape, position, build step done).

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

func TestMicroGlobMatch(t *testing.T) {
	cases := []struct {
		pattern, path string
		want          bool
	}{
		{"docs/**", "docs/a.md", true},
		{"docs/**", "docs/x/y/z.md", true},
		{"docs/*", "docs/a.md", true},
		{"docs/*", "docs/x/a.md", false}, // * never crosses /
		{"docs/*.md", "docs/a.txt", false},
		{"**/*.md", "a.md", true}, // ** is zero or more segments
		{"**/*.md", "x/y/a.md", true},
		{"lib/**/en.json", "lib/en.json", true},
		{"lib/**/en.json", "lib/i18n/v2/en.json", true},
		{"README.md", "README.md", true}, // no wildcard = exact
		{"README.md", "docs/README.md", false},
		{"Docs/**", "docs/a.md", false}, // case-sensitive
		{"docs/**", "docsx/a.md", false},
		{"docs/[", "docs/[", false}, // a malformed pattern matches nothing
	}
	for _, c := range cases {
		if got := microGlobMatch(c.pattern, c.path); got != c.want {
			t.Errorf("microGlobMatch(%q, %q) = %v, want %v", c.pattern, c.path, got, c.want)
		}
	}
}

func TestMicroDecls(t *testing.T) {
	conf := "# c\n  micro_path=docs/**\nmicro_path=/abs\nmicro_path=./x\nmicro_path=a/../b\nmicro_path=\nmicro_path=docs/**\n" +
		"micro_check=sh check.sh\nmicro_check=$(evil)\nmicro_check=\nfast_gate=echo\n"
	paths, checks := microDecls(conf)
	if strings.Join(paths, ",") != "docs/**" || strings.Join(checks, ",") != "sh check.sh" {
		t.Fatalf("microDecls: paths %q checks %q", paths, checks)
	}
}

// flRepo lays reposDir/<name> with a bare origin whose main carries base
// (the files) and answers the clone dir; the caller commits the change.
func flRepo(t *testing.T, reposDir, name string, base map[string]string) string {
	t.Helper()
	dir := filepath.Join(reposDir, name)
	bare := filepath.Join(t.TempDir(), name+".git")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	tgit(t, filepath.Dir(bare), "init", "-q", "--bare", "-b", "main", bare)
	tgit(t, dir, "init", "-q", "-b", "main")
	tgit(t, dir, "remote", "add", "origin", bare)
	for rel, body := range base {
		flWrite(t, dir, rel, body)
	}
	tgit(t, dir, "add", "-A")
	tgit(t, dir, "commit", "-q", "--allow-empty", "-m", "base")
	tgit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/main")
	tgit(t, dir, "fetch", "-q", "origin")
	tgit(t, dir, "remote", "set-head", "origin", "main")
	return dir
}

func flWrite(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func flCommit(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, body := range files {
		flWrite(t, dir, rel, body)
	}
	tgit(t, dir, "add", "-A")
	tgit(t, dir, "commit", "-q", "-m", "change")
}

const flConf = "micro_path=docs/**\nmicro_path=*.md\nfast_gate=sh fast.sh\nmicro_check=sh keys.sh\n"

func flBase() map[string]string {
	return map[string]string{".polyforge/verify-scope.conf": flConf, "docs/a.md": "a\n", "src/x.go": "package x\n", "AGENTS.md": "rules\n"}
}

func TestFastLaneVerdictInBoundsAndPlan(t *testing.T) {
	repos := t.TempDir()
	dir := flRepo(t, repos, "demo", flBase())
	flCommit(t, dir, map[string]string{"docs/a.md": "b\n", "docs/new/n.md": "n\n", "README.md": "r\n"})
	// an untouched repo (no commit beyond its base) is skipped, opted in or not
	flRepo(t, repos, "other", map[string]string{"x": "x\n"})
	v := fastLaneVerdict(repos, []testBarCmd{{Repo: "demo", Command: "sh fast.sh"}, {Repo: "nope", Command: "x"}})
	if !v.InBounds || v.Reason != "" || len(v.Repos) != 1 {
		t.Fatalf("in-bounds verdict: %+v", v)
	}
	r := v.Repos[0]
	if r.Name != "demo" || strings.Join(r.ChangedPaths, ",") != "README.md,docs/a.md,docs/new/n.md" {
		t.Fatalf("repo facts: %+v", r)
	}
	// the reported fast evidence, then the declared micro_check it omitted
	if got, _ := json.Marshal(r.Commands); string(got) != `[{"repo":"demo","command":"sh fast.sh"},{"repo":"demo","command":"sh keys.sh"}]` {
		t.Fatalf("credential plan %s", got)
	}
}

func TestFastLaneVerdictRefusals(t *testing.T) {
	fix := []testBarCmd{{Repo: "demo", Command: "sh fast.sh"}}
	cases := []struct {
		name   string
		base   map[string]string
		change map[string]string
		setup  func(t *testing.T, dir string)
		want   string
	}{
		{name: "code path", base: flBase(), change: map[string]string{"docs/a.md": "b\n", "src/x.go": "package x\n// y\n"}, want: "src/x.go"},
		{name: "hard-excluded despite *.md", base: flBase(), change: map[string]string{"AGENTS.md": "new rules\n"}, want: "AGENTS.md (an agent-contract file"},
		{name: "nested CLAUDE.md", base: flBase(), change: map[string]string{"docs/CLAUDE.md": "x\n"}, want: "docs/CLAUDE.md (an agent-contract file"},
		{name: "undeclared repo", base: map[string]string{".polyforge/verify-scope.conf": "fast_gate=true\n", "docs/a.md": "a\n"},
			change: map[string]string{"docs/a.md": "b\n"}, want: "declares no micro_path"},
		{name: "no conf at base", base: map[string]string{"docs/a.md": "a\n"},
			change: map[string]string{"docs/a.md": "b\n", ".polyforge/verify-scope.conf": flConf}, want: "no .polyforge/verify-scope.conf is readable at the diff base"},
		{name: "base conf wins over a widened one", base: flBase(),
			change: map[string]string{".polyforge/verify-scope.conf": flConf + "micro_path=src/**\nmicro_path=.polyforge/**\n", "src/x.go": "package x\n// y\n"},
			want:   "src/x.go"},
		{name: "rename judges the old side", base: flBase(), setup: func(t *testing.T, dir string) {
			tgit(t, dir, "mv", "src/x.go", "docs/x.go")
			tgit(t, dir, "commit", "-q", "-m", "rename")
		}, want: "src/x.go"},
		{name: "deletion", base: flBase(), setup: func(t *testing.T, dir string) {
			tgit(t, dir, "rm", "-q", "src/x.go")
			tgit(t, dir, "commit", "-q", "-m", "delete")
		}, want: "src/x.go"},
		{name: "unreadable diff base", base: flBase(), change: map[string]string{"docs/a.md": "b\n"}, setup: func(t *testing.T, dir string) {
			tgit(t, dir, "remote", "set-head", "origin", "-d")
			tgit(t, dir, "update-ref", "-d", "refs/remotes/origin/main")
		}, want: "origin default branch is unknown"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			repos := t.TempDir()
			dir := flRepo(t, repos, "demo", c.base)
			if c.change != nil {
				flCommit(t, dir, c.change)
			}
			if c.setup != nil {
				c.setup(t, dir)
			}
			v := fastLaneVerdict(repos, fix)
			if v.InBounds || len(v.Repos) != 0 {
				t.Fatalf("refused verdict expected: %+v", v)
			}
			if !strings.Contains(v.Reason, c.want) || !strings.Contains(v.Reason, "autopilot") {
				t.Fatalf("reason %q lacks %q or the autopilot re-file", v.Reason, c.want)
			}
		})
	}
}

func TestFastLaneVerdictPerRepoNoUnion(t *testing.T) {
	repos := t.TempDir()
	a := flRepo(t, repos, "a", map[string]string{".polyforge/verify-scope.conf": "micro_path=workflows/**\nfast_gate=true\n", "workflows/w.md": "w\n"})
	b := flRepo(t, repos, "b", map[string]string{".polyforge/verify-scope.conf": "micro_path=docs/**\nfast_gate=true\n", "workflows/w.md": "w\n"})
	flCommit(t, a, map[string]string{"workflows/w.md": "w2\n"})
	flCommit(t, b, map[string]string{"workflows/w.md": "w2\n"})
	ev := []testBarCmd{{Repo: "a", Command: "true"}, {Repo: "b", Command: "true"}}
	v := fastLaneVerdict(repos, ev)
	if v.InBounds || !strings.Contains(v.Reason, "repos/b:") || strings.Contains(v.Reason, "repos/a:") {
		t.Fatalf("repo b's workflow change must be out of bounds on its own declaration (a's is in bounds): %+v", v)
	}
}

func TestFastLaneVerdictEmptyPlanAndNoChange(t *testing.T) {
	repos := t.TempDir()
	dir := flRepo(t, repos, "demo", map[string]string{".polyforge/verify-scope.conf": "micro_path=docs/**\n", "docs/a.md": "a\n"})
	flCommit(t, dir, map[string]string{"docs/a.md": "b\n"})
	if v := fastLaneVerdict(repos, nil); v.InBounds || !strings.Contains(v.Reason, "no fast evidence command") {
		t.Fatalf("an empty credential plan must refuse: %+v", v)
	}
	repos2 := t.TempDir()
	flRepo(t, repos2, "demo", flBase())
	if v := fastLaneVerdict(repos2, nil); v.InBounds || !strings.Contains(v.Reason, "touches no task repository") {
		t.Fatalf("a change touching nothing must refuse: %+v", v)
	}
}

// flDriver is a driver over a capsule whose repos/demo carries a docs
// change and an out-of-bounds code line (a refusal, if the guard fires).
func flDriver(t *testing.T, wf *Workflow, script []fakeEntry) (*Driver, string, *fakeSpawner) {
	t.Helper()
	capDir := t.TempDir()
	dir := flRepo(t, filepath.Join(capDir, capsule.DirRepos), "demo", flBase())
	flCommit(t, dir, map[string]string{"src/x.go": "package x\n// y\n"})
	sp := &fakeSpawner{script: script}
	execPath := filepath.Join(capDir, capsule.ExecutionJSON)
	d, err := New(Config{WI: WIInfo{ID: "wi_fl", CapsuleDir: capDir}, ExecPath: execPath, Workflow: wf, Spawner: sp, Log: &eventLog{}})
	if err != nil {
		t.Fatal(err)
	}
	return d, execPath, sp
}

func TestFastLaneGuardScope(t *testing.T) {
	fixOut := `{"evidence":[{"repo":"demo","command":"sh fast.sh"}]}`
	t.Run("verification-bearing lane never judged", func(t *testing.T) {
		for _, wf := range []*Workflow{
			{Steps: []StepSpec{{ID: "fix"}, gateSpec("review", "fix"), gateSpec(verificationStepID, "fix"), {ID: deliverStepID, Gate: true, OnFail: &OnFail{Target: "fix"}}}},
			{Steps: []StepSpec{{ID: "fix"}, gateSpec("review", "fix")}}, // no deliver step
		} {
			if verificationLess(wf) {
				t.Fatalf("verificationLess(%v) = true", wf.Steps)
			}
			d, _, _ := flDriver(t, wf, nil)
			d.mu.Lock()
			d.exec.Steps["fix"] = &capsule.Step{Status: capsule.StepDone, Output: map[string]any{"evidence": []any{}}}
			d.mu.Unlock()
			if stopped, err := d.guardFastLane("review"); err != nil || stopped {
				t.Fatalf("the guard fired on %v: %v %v", wf.Steps, stopped, err)
			}
			if d.exec.Steps["fix"].Output[fastLaneKey] != nil || d.exec.TerminationReason != "" {
				t.Fatalf("the guard recorded a verdict on %v", wf.Steps)
			}
		}
	})
	t.Run("verification-less lane refuses before review", func(t *testing.T) {
		wf := &Workflow{Steps: []StepSpec{{ID: "fix"}, gateSpec("review", "fix"), {ID: deliverStepID, Gate: true, OnFail: &OnFail{Target: "fix"}}}, MaxTotal: 2, MaxPerStep: 1}
		d, execPath, sp := flDriver(t, wf, []fakeEntry{done(fixOut), done(`{"verdict":"pass"}`)})
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := d.Run(ctx); err != nil {
			t.Fatal(err)
		}
		ex := readExec(t, execPath)
		if ex.WorkflowStatus != StatusFailedGate || !strings.Contains(ex.TerminationReason, "src/x.go") || ex.RewindStats.Count != 0 {
			t.Fatalf("status %q reason %q rewinds %d", ex.WorkflowStatus, ex.TerminationReason, ex.RewindStats.Count)
		}
		if got := sp.dispatched(); len(got) != 1 {
			t.Fatalf("dispatched %v, want only the fix", got)
		}
		fl, _ := ex.Steps["fix"].Output[fastLaneKey].(map[string]any)
		if fl == nil || fl["in_bounds"] != false {
			t.Fatalf("no refused fast_lane recorded: %v", ex.Steps["fix"].Output)
		}
	})
	t.Run("not before the build step is done", func(t *testing.T) {
		wf := &Workflow{Steps: []StepSpec{{ID: "fix"}, gateSpec("review", "fix"), {ID: deliverStepID, Gate: true, OnFail: &OnFail{Target: "fix"}}}}
		d, _, _ := flDriver(t, wf, nil)
		if stopped, err := d.guardFastLane("fix"); err != nil || stopped {
			t.Fatalf("guard on the build step itself: %v %v", stopped, err)
		}
		if stopped, err := d.guardFastLane("review"); err != nil || stopped {
			t.Fatalf("guard before the build step is done: %v %v", stopped, err)
		}
	})
}
