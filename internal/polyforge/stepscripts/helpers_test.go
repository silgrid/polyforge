package stepscripts

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/testenv"
)

// TestMain isolates HOME/XDG/POLYFORGE_* so no test can touch the real install.
func TestMain(m *testing.M) { testenv.Main(m) }

// gitT runs git in dir with a fixed identity and fails the test on error.
func gitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	c := exec.Command("git", args...)
	c.Dir = dir
	c.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=sc", "GIT_AUTHOR_EMAIL=sc@example.invalid",
		"GIT_COMMITTER_NAME=sc", "GIT_COMMITTER_EMAIL=sc@example.invalid")
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

// testDockerfile is a repo's .polyforge/Dockerfile.verify under the fake
// runtime (never built for real).
const testDockerfile = "FROM scratch-test-image\nRUN true\n"

// capsuleFixture is a capsule with one task repo `app`, cloned from a bare
// origin whose main holds the base files, checked out on a feature branch.
type capsuleFixture struct {
	dir    string // the capsule root
	origin string // the bare origin
	seed   string // a working clone that pushes to origin
}

func (f *capsuleFixture) repo() string { return filepath.Join(f.dir, "repos", "app") }

// newCapsule makes the fixture; files are the base commit's.
func newCapsule(t *testing.T, files map[string]string) *capsuleFixture {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	f := &capsuleFixture{dir: filepath.Join(root, "capsule"), origin: filepath.Join(root, "origin.git"), seed: filepath.Join(root, "seed")}
	gitT(t, root, "init", "-q", "--bare", "-b", "main", f.origin)
	gitT(t, root, "clone", "-q", f.origin, f.seed)
	gitT(t, f.seed, "checkout", "-q", "-B", "main")
	for rel, body := range files {
		writeFile(t, f.seed, rel, body)
	}
	commitAll(t, f.seed, "base")
	gitT(t, f.seed, "push", "-q", "origin", "main")
	gitT(t, root, "clone", "-q", f.origin, f.repo())
	gitT(t, f.repo(), "config", "user.email", "sc@example.invalid")
	gitT(t, f.repo(), "config", "user.name", "sc")
	gitT(t, f.repo(), "checkout", "-q", "-b", "feature")
	raw, _ := json.Marshal(map[string]any{"work_item_id": "wi_sc", "bundle_name": "develop", "repos": map[string]string{"app": gitT(t, f.repo(), "rev-parse", "HEAD")}})
	writeFile(t, f.dir, "capsule.json", string(raw))
	return f
}

// change commits files on the feature branch and answers HEAD.
func (f *capsuleFixture) change(t *testing.T, files map[string]string) string {
	t.Helper()
	for rel, body := range files {
		writeFile(t, f.repo(), rel, body)
	}
	return commitAll(t, f.repo(), "change")
}

// advanceOrigin pushes a new commit on origin's main.
func (f *capsuleFixture) advanceOrigin(t *testing.T, files map[string]string) string {
	t.Helper()
	for rel, body := range files {
		writeFile(t, f.seed, rel, body)
	}
	sha := commitAll(t, f.seed, "upstream")
	gitT(t, f.seed, "push", "-q", "origin", "main")
	return sha
}

// head answers the repo's HEAD.
func (f *capsuleFixture) head(t *testing.T) string { return gitT(t, f.repo(), "rev-parse", "HEAD") }

// fakeEngine writes a scope engine script answering one report (keyed by
// the working directory's base name) and logging its arguments to
// args.log beside it; it answers the engine's path.
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
repo=$(basename "$PWD")
f="$dir/reports/$repo.json"
[ -f "$f" ] || { echo "no report for $repo" >&2; exit 3; }
cat "$f"
`
	p := filepath.Join(dir, "verify-scope")
	if err := os.WriteFile(p, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// lightReport is an engine report of mode scoped — what a --light ask
// answers — with the given commands.
func lightReport(cmds ...map[string]any) map[string]any {
	return map[string]any{
		"schema": 1, "root": "/x", "stack": "none", "analyzer": nil, "conf": "present",
		"conf_changed_in_diff": false, "base": "b", "head": "h", "default_branch": "main",
		"mode": "scoped", "light": true, "paths": []string{"a.txt"}, "commands": cmds,
		"test_glob": []string{}, "base_setup": []string{}, "runner": nil, "diagnostics": []string{},
	}
}

// cmdOf is one planned fast command, as the engine's report carries it.
func cmdOf(name, cmd string) map[string]any {
	return map[string]any{"name": name, "phase": "fast", "cmd": cmd, "requires_env": []string{}, "requires_cmd": []string{},
		"timeout": "5m", "report": nil, "source": "conf"}
}

// fakeRuntime records the containers a script started and removed through
// the fake docker client on its PATH (scripts_test.go: dockerDir,
// absorbDockerLog); unavailable makes that client's engine not answer.
type fakeRuntime struct {
	mu          sync.Mutex
	unavailable error

	starts  []containerSpec
	removes []string
}

func newFakeRuntime(*testing.T) *fakeRuntime { return &fakeRuntime{} }

// stepEnv is a step environment: the process PATH and HOME plus the given
// PF_* variables.
func stepEnv(extra ...string) []string {
	env := []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME")}
	return append(env, extra...)
}

// invoke runs the workflow script that took over the step command name
// (scriptOf) with args under env, its containers through rt's fake docker,
// answering the exit code and the two streams.
func invoke(t *testing.T, name string, args, env []string, rt *fakeRuntime) (int, string, string) {
	t.Helper()
	if scriptOf(name) == "" {
		t.Fatalf("%s is no workflow script", name)
	}
	return invokeScript(t, name, args, env, rt, nil)
}

// readResult reads a result file.
func readResult(t *testing.T, path string) *Result {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("result file: %v", err)
	}
	var r Result
	if err := json.Unmarshal(raw, &r); err != nil {
		t.Fatalf("result file %s is not JSON: %v\n%s", path, err, raw)
	}
	return &r
}

// detailsOf decodes a result's details into v.
func detailsOf(t *testing.T, r *Result, v any) {
	t.Helper()
	raw, err := json.Marshal(r.Details)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, v); err != nil {
		t.Fatalf("details: %v\n%s", err, raw)
	}
}
