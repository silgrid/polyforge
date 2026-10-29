package cli

import (
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// vrCapsule makes a capsule at a fresh dir: one task repo cloned from a
// bare origin, a commit of its own on a feature branch, and an engine
// script answering one passing (or failing) full command.
func vrCapsule(t *testing.T, cmd string) (capsuleDir, engine string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	write := func(p, body string, mode os.FileMode) {
		t.Helper()
		if err := os.WriteFile(p, []byte(body), mode); err != nil {
			t.Fatal(err)
		}
	}
	git := func(dir string, args ...string) {
		t.Helper()
		c := exec.Command("git", args...)
		c.Dir = dir
		c.Env = append(os.Environ(), "GIT_AUTHOR_NAME=vr", "GIT_AUTHOR_EMAIL=vr@e.invalid", "GIT_COMMITTER_NAME=vr", "GIT_COMMITTER_EMAIL=vr@e.invalid")
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	origin := filepath.Join(root, "origin.git")
	git(root, "init", "-q", "--bare", "-b", "main", origin)
	seed := filepath.Join(root, "seed")
	git(root, "clone", "-q", origin, seed)
	git(seed, "checkout", "-q", "-B", "main")
	write(filepath.Join(seed, "a.txt"), "a\n", 0o644)
	git(seed, "add", "-A")
	git(seed, "commit", "-q", "-m", "base")
	git(seed, "push", "-q", "origin", "main")
	capsuleDir = filepath.Join(root, "capsule")
	repo := filepath.Join(capsuleDir, "repos", "app")
	git(root, "clone", "-q", origin, repo)
	git(repo, "config", "user.email", "vr@e.invalid")
	git(repo, "config", "user.name", "vr")
	git(repo, "checkout", "-q", "-b", "feature")
	write(filepath.Join(repo, "b.txt"), "b\n", 0o644)
	git(repo, "add", "-A")
	git(repo, "commit", "-q", "-m", "feature")
	write(filepath.Join(capsuleDir, "capsule.json"), `{"work_item_id":"wi_x","repos":{"app":"x"}}`, 0o644)
	rep, _ := json.Marshal(map[string]any{"schema": 1, "stack": "none", "conf": "present", "mode": "full",
		"commands": []map[string]any{{"name": "full-1", "phase": "full", "cmd": cmd}}})
	engine = filepath.Join(root, "verify-scope")
	write(engine, "#!/bin/sh\ncat <<'EOF'\n"+string(rep)+"\nEOF\n", 0o755)
	return capsuleDir, engine
}

// TestVerifyRunVerb: the verb runs at the capsule root, prints where
// result.json and the mechanical report are, exits 0 on a red bar (red
// is data) and works inside a step (step mode does not refuse it).
func TestVerifyRunVerb(t *testing.T) {
	capsuleDir, engine := vrCapsule(t, "echo red; exit 1")
	t.Chdir(capsuleDir)
	code, stdout, stderr := runPf(t, map[string]string{"PF_WI_ID": "wi_x", "PF_DAEMON_ID": "d1"}, "verify-run", "--engine", engine)
	if code != 0 {
		t.Fatalf("exit %d, stderr %q", code, stderr)
	}
	if !strings.Contains(stdout, "verify-run: result runtime-state/verify/1/result.json") || !strings.Contains(stdout, "mechanical verdict fail") {
		t.Errorf("stdout = %q", stdout)
	}
	raw, err := os.ReadFile(filepath.Join(capsuleDir, "runtime-state", "verify", "1", "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	var res struct {
		MechanicalVerdict string                           `json:"mechanical_verdict"`
		TestBar           []struct{ Repo, Command string } `json:"test_bar"`
	}
	if err := json.Unmarshal(raw, &res); err != nil || res.MechanicalVerdict != "fail" || len(res.TestBar) != 1 {
		t.Errorf("result.json = %s (%v)", raw, err)
	}
	if _, err := os.Stat(filepath.Join(capsuleDir, "runtime-state", "verify", "1", "report-mechanical.md")); err != nil {
		t.Error(err)
	}
	// --json: one machine document
	code, stdout, _ = runPf(t, nil, "--json", "verify-run", "--engine", engine)
	var doc map[string]any
	if code != 0 || json.Unmarshal([]byte(stdout), &doc) != nil || doc["result"] != "runtime-state/verify/2/result.json" {
		t.Errorf("--json: exit %d stdout %q", code, stdout)
	}
}

// TestVerifyRunVerbErrors: outside a capsule, an unknown tier and stray
// arguments are local failures (exit 1); a bad flag is a parse error (2);
// an impossible merged-tree proof exits 1 naming it.
func TestVerifyRunVerbErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	if code, _, stderr := runPf(t, nil, "verify-run", "--engine", "/bin/true"); code != 1 || !strings.Contains(stderr, "not a capsule root") {
		t.Errorf("outside a capsule: %d %q", code, stderr)
	}
	if code, _, stderr := runPf(t, nil, "verify-run", "--tier", "quick"); code != 1 || !strings.Contains(stderr, "--tier") {
		t.Errorf("bad tier: %d %q", code, stderr)
	}
	if code, _, _ := runPf(t, nil, "verify-run", "extra"); code != 1 {
		t.Errorf("stray argument: %d", code)
	}
	if code, _, _ := runPf(t, nil, "verify-run", "--no-such-flag"); code != 2 {
		t.Errorf("bad flag: %d", code)
	}
	capsuleDir, engine := vrCapsule(t, "true")
	c := exec.Command("git", "remote", "set-url", "origin", filepath.Join(t.TempDir(), "gone.git"))
	c.Dir = filepath.Join(capsuleDir, "repos", "app")
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("%v %s", err, out)
	}
	t.Chdir(capsuleDir)
	if code, _, stderr := runPf(t, nil, "verify-run", "--engine", engine); code != 1 || !strings.Contains(stderr, "incomplete") || !strings.Contains(stderr, "git fetch origin") {
		t.Errorf("failed fetch: %d %q", code, stderr)
	}
}

// TestVerifyRunPromptFlagsExist: every flag the shipped verification step
// teaches after `pf verify-run` is a flag of the verb — a renamed flag
// fails here, not in a step agent's run.
func TestVerifyRunPromptFlagsExist(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "workflows", "_shared", "steps", "verification.md"))
	if err != nil {
		t.Fatal(err)
	}
	fs := flag.NewFlagSet("verify-run", flag.ContinueOnError)
	cmdVerifyRun.Setup(fs)
	seen := 0
	for _, line := range strings.Split(string(raw), "\n") {
		i := strings.Index(line, "pf verify-run ")
		if i < 0 {
			continue
		}
		seg := line[i:]
		if j := strings.Index(seg, "`"); j >= 0 {
			seg = seg[:j]
		}
		for _, w := range strings.Fields(seg) {
			if !strings.HasPrefix(w, "--") {
				continue
			}
			seen++
			if fs.Lookup(strings.TrimPrefix(w, "--")) == nil {
				t.Errorf("verification.md teaches `pf verify-run %s`, which the verb does not define", w)
			}
		}
	}
	if seen < 2 {
		t.Fatalf("verification.md teaches %d pf verify-run flags, want at least --tier and --engine", seen)
	}
}
