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
