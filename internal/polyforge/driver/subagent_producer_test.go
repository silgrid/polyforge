package driver

// subagent_producer_test.go (polyforge#228 AC1): the producer→recorder leg.
//
// subagentrec_test.go replays a recorded pi stdout. This test derives the
// same records from the REAL embedded extension: the fake CLI child base
// (runtime/testdata/pi_child_harness.mjs) runs one subagent.execute() in
// node, the harness translates the extension's onUpdate/final result the
// way pi does (tool_execution_update / tool_execution_end), and the
// production recorder consumes those lines. The child's two messages are
// fixed by the test, so the transcript's order and its update prefix are
// the requirement's, not the recorder's output.

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/runtime/stepagentassets"
)

// producerProject lays out a throwaway node project that loads the embedded
// extension and runs the delivered fake child base as the child (a PATH
// `pi` wrapper over the same harness). It returns the project root.
func producerProject(t *testing.T) (string, string) {
	t.Helper()
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatalf("node is required to drive the embedded subagent extension: %v", err)
	}
	piBin, err := exec.LookPath("pi")
	if err != nil {
		t.Fatalf("pi is required to resolve the extension's dependencies: %v", err)
	}
	real, err := filepath.EvalSymlinks(piBin)
	if err != nil {
		t.Fatal(err)
	}
	pkg := ""
	for d := filepath.Dir(real); ; d = filepath.Dir(d) {
		raw, err := os.ReadFile(filepath.Join(d, "package.json"))
		if err == nil && bytes.Contains(raw, []byte(`"@earendil-works/pi-coding-agent"`)) {
			pkg = d
			break
		}
		if d == filepath.Dir(d) {
			t.Fatalf("could not locate the pi package above %s", real)
		}
	}
	root := t.TempDir()
	nm := filepath.Join(root, "node_modules", "@earendil-works")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, target := range map[string]string{
		"pi-agent-core":   filepath.Join(pkg, "node_modules", "@earendil-works", "pi-agent-core"),
		"pi-ai":           filepath.Join(pkg, "node_modules", "@earendil-works", "pi-ai"),
		"pi-tui":          filepath.Join(pkg, "node_modules", "@earendil-works", "pi-tui"),
		"pi-coding-agent": pkg,
	} {
		if err := os.Symlink(target, filepath.Join(nm, name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(pkg, "node_modules", "typebox"), filepath.Join(root, "node_modules", "typebox")); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"index.ts", "agents.ts"} {
		raw, err := stepagentassets.FS.ReadFile("extensions/subagent/" + rel)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, rel), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(root, "agentdir", "agents"), 0o755); err != nil {
		t.Fatal(err)
	}
	worker := "---\nname: worker\ndescription: test worker\n---\ntest worker body\n"
	if err := os.WriteFile(filepath.Join(root, "agentdir", "agents", "worker.md"), []byte(worker), 0o644); err != nil {
		t.Fatal(err)
	}
	harness, err := os.ReadFile(filepath.Join("..", "runtime", "testdata", "pi_child_harness.mjs"))
	if err != nil {
		t.Fatalf("the delivered fake child base runtime/testdata/pi_child_harness.mjs is missing: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "harness.mjs"), harness, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	wrapper := "#!/bin/sh\nexec \"" + node + "\" \"" + filepath.Join(root, "harness.mjs") + "\" \"$@\"\n"
	if err := os.WriteFile(filepath.Join(root, "bin", "pi"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	return root, node
}

type producerResult struct {
	ExecPath string           `json:"execPath"`
	Result   map[string]any   `json:"result"`
	Updates  []map[string]any `json:"updates"`
	Error    string           `json:"error"`
}

// runProducer runs the harness parent once and returns its result and the
// pi-shaped tool_execution_* lines it produced for the recorder.
func runProducer(t *testing.T, root, node string, scenario map[string]any) (producerResult, []string) {
	t.Helper()
	scenarioPath := filepath.Join(root, "scenario.json")
	producerPath := filepath.Join(root, "producer.jsonl")
	scenario["producerFile"] = producerPath
	blob, _ := json.Marshal(scenario)
	if err := os.WriteFile(scenarioPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, filepath.Join(root, "harness.mjs"), scenarioPath)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"PATH="+filepath.Join(root, "bin")+":"+os.Getenv("PATH"),
		"PI_CODING_AGENT_DIR="+filepath.Join(root, "agentdir"),
		"FAKEPI_ARGV_FILE="+filepath.Join(root, "child-argv.json"),
		"TMPDIR="+t.TempDir(),
	)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(60 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatalf("the producer harness still ran after 60s; output:\n%s", out.String())
	}
	line := ""
	for _, l := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(l, "PFEXT-RESULT ") {
			line = strings.TrimPrefix(l, "PFEXT-RESULT ")
		}
	}
	if line == "" {
		t.Fatalf("no PFEXT-RESULT line; harness output:\n%s", out.String())
	}
	var res producerResult
	if err := json.Unmarshal([]byte(line), &res); err != nil {
		t.Fatalf("bad PFEXT-RESULT %q: %v", line, err)
	}
	if res.Error != "" {
		t.Fatalf("the extension call failed: %s", res.Error)
	}
	raw, err := os.ReadFile(producerPath)
	if err != nil {
		t.Fatalf("the harness produced no pi-shaped records for the recorder (producer translation missing): %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) == "" {
		t.Fatalf("the producer file is empty")
	}
	return res, lines
}

// transcriptMessages reads one recorded child transcript's message texts in
// order.
func transcriptMessages(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}
	var out []string
	for _, l := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		if strings.TrimSpace(l) == "" {
			continue
		}
		var rec struct {
			Type    string `json:"type"`
			Message struct {
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(l), &rec); err != nil {
			t.Fatalf("a recorded transcript line is not whole JSON: %q", l)
		}
		if rec.Type != "message" {
			t.Fatalf("a recorded transcript line is not a message record: %q", l)
		}
		text := ""
		for _, c := range rec.Message.Content {
			text += c.Text
		}
		out = append(out, text)
	}
	return out
}

func producerUpdateLines(t *testing.T, lines []string) (updates []string, end string) {
	t.Helper()
	for _, l := range lines {
		var rec struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(l), &rec); err != nil {
			t.Fatalf("a producer line is not JSON: %q", l)
		}
		switch rec.Type {
		case "tool_execution_update":
			updates = append(updates, l)
		case "tool_execution_end":
			end = l
		}
	}
	if len(updates) == 0 || end == "" {
		t.Fatalf("the producer emitted %d update(s) and end=%v; want the extension's streamed updates and one end", len(updates), end != "")
	}
	return updates, end
}

// TestPiSubagentProducerToRecorder: the real extension's streamed updates
// arrive as a growing prefix and the end settles the child exactly once
// with the production meta.
func TestPiSubagentProducerToRecorder(t *testing.T) {
	root, node := producerProject(t)
	const callID = "call_producer_1"
	_, lines := runProducer(t, root, node, map[string]any{
		"toolCallId": callID,
		"params":     map[string]any{"agent": "worker", "task": "producer probe"},
	})
	updates, end := producerUpdateLines(t, lines)
	if len(updates) != 2 {
		t.Fatalf("the extension streamed %d update(s); want one per child message", len(updates))
	}
	capDir, transcript := recorderCapsule(t)
	rec := newPiSubagentRecorder(capDir, transcript)
	dir := filepath.Join(capDir, capsule.DirSession, "session-tok", capsule.DirSubagents)
	stem := subagentFileStem(callID, 0)

	// the first update: M1 only, not finished
	rec.Feed(updates[0])
	if got := transcriptMessages(t, filepath.Join(dir, stem+".jsonl")); len(got) != 1 || got[0] != "M1" {
		t.Fatalf("after the first update the transcript = %v, want [M1]", got)
	}
	if m := readMeta(t, filepath.Join(dir, stem+".meta.json")); m["finished"] != false {
		t.Fatalf("a running child must not be finished yet: %v", m)
	}

	// the second update: M1,M2; still not finished
	rec.Feed(updates[1])
	if got := transcriptMessages(t, filepath.Join(dir, stem+".jsonl")); len(got) != 2 || got[0] != "M1" || got[1] != "M2" {
		t.Fatalf("after both updates the transcript = %v, want [M1 M2]", got)
	}
	if m := readMeta(t, filepath.Join(dir, stem+".meta.json")); m["finished"] != false {
		t.Fatalf("the child is finished only at the call's end: %v", m)
	}

	// the end: the same two messages once, the child settled
	rec.Feed(end)
	if got := transcriptMessages(t, filepath.Join(dir, stem+".jsonl")); len(got) != 2 || got[0] != "M1" || got[1] != "M2" {
		t.Fatalf("the end must not duplicate or drop messages: transcript = %v", got)
	}
	meta := readMeta(t, filepath.Join(dir, stem+".meta.json"))
	if meta["agentType"] != "worker" || meta["toolUseId"] != callID || meta["index"] != float64(0) {
		t.Fatalf("the meta does not name the child: %v", meta)
	}
	if meta["finished"] != true || meta["isError"] != false || meta["exitCode"] != float64(0) {
		t.Fatalf("a successful child's meta = %v, want finished/exitCode 0/no error", meta)
	}
}

// TestPiSubagentProducerNonZeroExit: the producer's failure is the child's
// own — the end records finished with isError, never a success.
func TestPiSubagentProducerNonZeroExit(t *testing.T) {
	root, node := producerProject(t)
	const callID = "call_producer_fail"
	t.Setenv("FAKEPI_CHILD_EXIT", "3")
	res, lines := runProducer(t, root, node, map[string]any{
		"toolCallId": callID,
		"params":     map[string]any{"agent": "worker", "task": "producer probe"},
	})
	if isErr, _ := res.Result["isError"].(bool); !isErr {
		t.Fatalf("a non-zero child must make the tool result an error: %v", res.Result)
	}
	_, end := producerUpdateLines(t, lines)
	capDir, transcript := recorderCapsule(t)
	rec := newPiSubagentRecorder(capDir, transcript)
	rec.Feed(end)
	dir := filepath.Join(capDir, capsule.DirSession, "session-tok", capsule.DirSubagents)
	meta := readMeta(t, filepath.Join(dir, subagentFileStem(callID, 0)+".meta.json"))
	if meta["finished"] != true || meta["isError"] != true || meta["exitCode"] != float64(3) {
		t.Fatalf("a non-zero child's meta = %v, want finished/isError/exitCode 3", meta)
	}
}
