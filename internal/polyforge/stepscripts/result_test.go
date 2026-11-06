package stepscripts

// result_test.go: a script's result file as its readers see it (the
// verdict follows the exit code and nothing else; the round is PF_ROUND;
// the file matches stdout's JSON and leaves nothing beside it; it is
// written even when the run is not judged), driven through gate.py.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// gatePins writes pins gating nothing in dir: `gate.py --capsule dir --pins`
// of them is a judged pass that touches no container — a vehicle for the
// result writer alone.
func gatePins(t *testing.T, dir string) string {
	t.Helper()
	pins := filepath.Join(dir, "pins.json")
	if err := os.MkdirAll(filepath.Join(dir, "repos"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pins, []byte(`{"heads": {}, "gated": []}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return pins
}

// TestResultFileRoundAndAtomicWrite: `gate.py` over pins gating nothing is
// a judged pass that touches no container, so it exercises the writer
// alone: the file carries the round, the argv, the verdict of the exit
// code and the timestamps; stdout's JSON (--json) is the same document; no
// temp file is left beside it.
func TestResultFileRoundAndAtomicWrite(t *testing.T) {
	dir := t.TempDir()
	pins := gatePins(t, dir)
	out := filepath.Join(dir, "artifacts", "gate", "result.json")
	args := []string{"--capsule", dir, "--pins", pins, "--json", "--out", out}
	exit, stdout, _ := invoke(t, NameGate, args, stepEnv("PF_ROUND=round-7"), newFakeRuntime(t))
	if exit != 0 {
		t.Fatalf("exit %d, want 0 (nothing gated)", exit)
	}
	res := readResult(t, out)
	if res.Schema != Schema || res.Command != NameGate || res.Round != "round-7" || res.ExitCode != 0 || res.Verdict != VerdictPass {
		t.Fatalf("result: %+v", res)
	}
	if strings.Join(res.Argv, " ") != strings.Join(append(append([]string{}, gateIsolation...), args...), " ") {
		t.Errorf("argv %v, want %v", res.Argv, args)
	}
	if res.StartedAt == "" || res.EndedAt == "" || res.EndedAt < res.StartedAt || res.Version == "" {
		t.Errorf("timestamps/version: %+v", res)
	}
	if !strings.Contains(res.Summary, "nothing gated") || len(res.Heads) != 0 || len(res.Logs) != 0 {
		t.Errorf("summary/heads/logs: %+v", res)
	}
	var fromStdout Result
	if err := json.Unmarshal([]byte(stdout), &fromStdout); err != nil {
		t.Fatalf("stdout is not the JSON result: %v\n%s", err, stdout)
	}
	fileRaw, _ := json.Marshal(res)
	stdoutRaw, _ := json.Marshal(&fromStdout)
	if string(fileRaw) != string(stdoutRaw) {
		t.Fatalf("stdout and the file differ:\n%s\n%s", stdoutRaw, fileRaw)
	}
	entries, _ := os.ReadDir(filepath.Dir(out))
	for _, e := range entries {
		if e.Name() != "result.json" && e.Name() != "logs" {
			t.Errorf("left beside the result: %s", e.Name())
		}
	}
	// without --json stdout is one line: the summary and the verdict
	exit, stdout, _ = invoke(t, NameGate, []string{"--capsule", dir, "--pins", pins}, stepEnv(), newFakeRuntime(t))
	if exit != 0 || !strings.HasPrefix(stdout, NameGate+": ") || !strings.HasSuffix(strings.TrimSpace(stdout), "(pass)") {
		t.Fatalf("exit %d, stdout %q", exit, stdout)
	}
}

// TestResultWrittenWhenNotJudged: unusable pins are exit 2 and the result
// file still records it (verdict error, no round outside a step).
func TestResultWrittenWhenNotJudged(t *testing.T) {
	dir := t.TempDir()
	pins := gatePins(t, dir)
	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := filepath.Join(dir, "result.json")
	exit, _, errb := invoke(t, NameGate, []string{"--capsule", dir, "--pins", bad, "--out", out}, stepEnv(), newFakeRuntime(t))
	res0 := readResult(t, out)
	if exit != 2 || !strings.Contains(errb+res0.Summary, "do not decode") {
		t.Fatalf("exit %d, stderr %q, summary %q", exit, errb, res0.Summary)
	}
	res := readResult(t, out)
	if res.ExitCode != 2 || res.Verdict != VerdictError || res.Round != "" {
		t.Fatalf("result: %+v", res)
	}
	// a result that cannot be written turns the exit into 2
	blocked := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocked, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	exit, _, errb = invoke(t, NameGate, []string{"--capsule", dir, "--pins", pins, "--out", filepath.Join(blocked, "result.json")}, stepEnv(), newFakeRuntime(t))
	if exit != 2 || !strings.Contains(errb, "could not be written") {
		t.Fatalf("exit %d, stderr %q", exit, errb)
	}
}
