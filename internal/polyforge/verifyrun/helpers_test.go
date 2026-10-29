package verifyrun

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// gitT runs git in dir with a fixed identity and fails the test on error.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=vr", "GIT_AUTHOR_EMAIL=vr@example.invalid",
		"GIT_COMMITTER_NAME=vr", "GIT_COMMITTER_EMAIL=vr@example.invalid")
	out, err := c.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// writeFile writes rel under dir.
func writeFile(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// commitAll stages everything in dir and commits.
func commitAll(t *testing.T, dir, msg string) string {
	t.Helper()
	gitT(t, dir, "add", "-A")
	gitT(t, dir, "commit", "-q", "-m", msg)
	return gitT(t, dir, "rev-parse", "HEAD")
}

// fixture is a capsule with task repos cloned from bare origins.
type fixture struct {
	capsule string
	origins map[string]string // repo -> bare origin
	seeds   map[string]string // repo -> a working clone that pushes to origin
}

// newFixture makes a capsule whose repos each start from files on the
// origin's main branch (origin/HEAD set).
func newFixture(t *testing.T, repos map[string]map[string]string) *fixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	f := &fixture{capsule: filepath.Join(root, "capsule"), origins: map[string]string{}, seeds: map[string]string{}}
	ids := map[string]string{}
	for name, files := range repos {
		origin := filepath.Join(root, "origin-"+name+".git")
		gitT(t, root, "init", "-q", "--bare", "-b", "main", origin)
		seed := filepath.Join(root, "seed-"+name)
		gitT(t, root, "clone", "-q", origin, seed)
		gitT(t, seed, "checkout", "-q", "-B", "main")
		for rel, body := range files {
			writeFile(t, seed, rel, body)
		}
		commitAll(t, seed, "base")
		gitT(t, seed, "push", "-q", "origin", "main")
		clone := filepath.Join(f.capsule, "repos", name)
		gitT(t, root, "clone", "-q", origin, clone)
		gitT(t, clone, "config", "user.email", "vr@example.invalid")
		gitT(t, clone, "config", "user.name", "vr")
		gitT(t, clone, "checkout", "-q", "-b", "feature")
		ids[name] = gitT(t, clone, "rev-parse", "HEAD")
		f.origins[name], f.seeds[name] = origin, seed
	}
	raw, _ := json.Marshal(map[string]any{"work_item_id": "wi_x", "bundle_name": "feature-full", "repos": ids})
	writeFile(t, f.capsule, "capsule.json", string(raw))
	return f
}

// repo answers a capsule repo's dir.
func (f *fixture) repo(name string) string { return filepath.Join(f.capsule, "repos", name) }

// commitInRepo commits files on the capsule repo's feature branch.
func (f *fixture) commitInRepo(t *testing.T, name string, files map[string]string, msg string) string {
	t.Helper()
	for rel, body := range files {
		writeFile(t, f.repo(name), rel, body)
	}
	return commitAll(t, f.repo(name), msg)
}

// advanceOrigin pushes a new commit on origin's main.
func (f *fixture) advanceOrigin(t *testing.T, name string, files map[string]string, msg string) {
	t.Helper()
	seed := f.seeds[name]
	for rel, body := range files {
		writeFile(t, seed, rel, body)
	}
	commitAll(t, seed, msg)
	gitT(t, seed, "push", "-q", "origin", "main")
}

// execution writes runtime-state/execution.json with the given done
// step outputs.
func (f *fixture) execution(t *testing.T, outputs map[string]map[string]any) {
	t.Helper()
	steps := map[string]any{}
	for id, out := range outputs {
		steps[id] = map[string]any{"status": "done", "output": out}
	}
	raw, _ := json.Marshal(map[string]any{"steps": steps})
	writeFile(t, f.capsule, "runtime-state/execution.json", string(raw))
}

// fakeEngine writes an engine script answering the report keyed by the
// working directory's base name (<repo>.full under --full, when given)
// and logging its arguments to args.log beside it.
func fakeEngine(t *testing.T, reports map[string]any) string {
	t.Helper()
	dir := t.TempDir()
	for repo, rep := range reports {
		raw, err := json.Marshal(rep)
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, dir, "reports/"+repo+".json", string(raw))
	}
	script := `#!/bin/bash
dir=$(cd "$(dirname "$0")" && pwd)
echo "$(basename "$PWD") $*" >>"$dir/args.log"
full=0
while [ $# -gt 0 ]; do case "$1" in --base) shift 2;; --full) full=1; shift;; *) shift;; esac; done
repo=$(basename "$PWD")
f="$dir/reports/$repo.json"
[ "$full" = 1 ] && [ -f "$dir/reports/$repo.full.json" ] && f="$dir/reports/$repo.full.json"
[ -f "$f" ] || { echo "no report for $repo" >&2; exit 3; }
cat "$f"
`
	p := filepath.Join(dir, "verify-scope")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// report builds an engine report.
func report(mode string, cmds ...EngineCommand) map[string]any {
	return map[string]any{
		"schema": 1, "root": "/x", "stack": "none", "analyzer": nil, "conf": "present",
		"conf_changed_in_diff": false, "base": "b", "head": "h", "default_branch": "main",
		"mode": mode, "paths": []string{"a.txt"}, "commands": cmds,
		"test_glob": []string{}, "base_setup": []string{}, "runner": nil, "ci_evidence": []string{}, "diagnostics": []string{},
	}
}

// cmdOf is one planned command.
func cmdOf(name, phase, cmd string) EngineCommand {
	return EngineCommand{Name: name, Phase: phase, Cmd: cmd, Timeout: "20m", Source: "conf",
		RequiresEnv: []string{}, RequiresCmd: []string{}}
}

// runFixture runs verify-run over the fixture.
func runFixture(t *testing.T, f *fixture, engine string, mod func(*Options)) *Result {
	t.Helper()
	o := Options{CapsuleDir: f.capsule, Engine: engine}
	if mod != nil {
		mod(&o)
	}
	res, err := Run(t.Context(), o)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

// checkOf answers the named check.
func checkOf(t *testing.T, res *Result, name string) Check {
	t.Helper()
	for _, c := range res.Checks {
		if c.Name == name {
			return c
		}
	}
	t.Fatalf("no check %q in %+v", name, res.Checks)
	return Check{}
}
