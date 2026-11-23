package main

// w241 proof of the scripted agent's done-gate proof (polyforge#241): the
// e2e fake agent must write the mechanical facts proof a real build writes
// (artifacts/build/handoff-facts/<repo>.json) before it hands off done, or
// every existing e2e times out on the new gate (polyforge#211's lesson).
// The proof is one pf-step-result/1 document carrying verdict pass and each
// task repo at its HEAD; the expected head comes from git, independently of
// the writer.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func TestWriteProofsWritesAPassAtEveryHead(t *testing.T) {
	root := t.TempDir()
	repo := filepath.Join(root, "repos", "app")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, repo, "add", ".")
	gitIn(t, repo, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit", "-q", "-m", "one")
	head := gitIn(t, repo, "rev-parse", "HEAD")
	head = head[:len(head)-1] // trim git's newline

	const dir = "artifacts/build/handoff-facts"
	if err := writeProofs(root, dir); err != nil {
		t.Fatalf("writeProofs: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(dir), "app.json"))
	if err != nil {
		t.Fatalf("no proof at %s/app.json: %v", dir, err)
	}
	var res struct {
		Schema  string            `json:"schema"`
		Verdict string            `json:"verdict"`
		Heads   map[string]string `json:"heads"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		t.Fatalf("the proof is not JSON: %v\n%s", err, raw)
	}
	if res.Schema != "pf-step-result/1" || res.Verdict != "pass" {
		t.Errorf("the proof is %s/%s, want pf-step-result/1/pass", res.Schema, res.Verdict)
	}
	if res.Heads["app"] != head {
		t.Errorf("the proof's head for app is %q, want %q", res.Heads["app"], head)
	}
}