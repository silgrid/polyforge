package runtime

// stepagent_guard_test.go (polyforge#214): the daemon-owned step guard
// extension. Three proofs live here:
//
//   - TestStepGuardNodeSuite wraps the guard's node:test suites for the
//     Go bar, so `go test ./...` (the CI and the full tail) runs the AC1
//     bash-rule table and the AC2 handler table too.
//   - TestStepAgentGuardLoadedByProductionLaunch (AC3): a pi process
//     launched EXACTLY as the daemon launches a step process — the
//     production buildCmd, the dedicated step agent directory — answers
//     get_commands with the guard's step-guard command, extension-sourced
//     from the step dir, and with no command sourced from the operator's
//     agent dir. Skips (never fails) where pi is not installed.
//   - TestStepAgentRealPiGuardBlocksBackground (AC4, opt-in like the
//     other real-world drives): a REAL pi step session's bash tool call
//     `sleep 30 &` comes back blocked with the step guard reason, and
//     the counter-proof (`echo ok`) passes untouched. Burns model
//     tokens: it runs only with POLYFORGE_TEST_REAL_PI=1.

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// killProcessGroup ends a step process the test started: it owns the
// process group (Setpgid below), so the kill reaches the whole group and
// nothing outlives the test.
func killProcessGroup(t *testing.T, cmd *exec.Cmd) {
	t.Helper()
	if cmd.Process == nil {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Log("the pi process did not exit within 5 s of SIGKILL")
	}
}

// TestStepGuardNodeSuite runs the guard extension's node:test suites
// (rules.test.ts, index.test.ts) from the asset directory: the AC1 rule
// table and the AC2 handler table belong to the Go bar too.
func TestStepGuardNodeSuite(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not on PATH: the guard's node suites cannot run")
	}
	dir := filepath.Join("stepagentassets", "extensions", "guard")
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("the guard asset directory is missing: %v", err)
	}
	var args []string
	for _, e := range ents {
		if strings.HasSuffix(e.Name(), ".test.ts") {
			args = append(args, filepath.Join(dir, e.Name()))
		}
	}
	if len(args) == 0 {
		t.Fatal("the guard asset directory carries no node test file")
	}
	cmd := exec.Command(node, append([]string{"--test"}, args...)...)
	// the suites' scratch files stay inside this test's own temp dir: the
	// package's testenv guard fails on leftovers in the private TMPDIR
	cmd.Env = append(os.Environ(), "TMPDIR="+t.TempDir(), "NO_COLOR=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the guard node suite failed:\n%s", out)
	}
}

// guardCommand is one get_commands entry.
type guardCommand struct {
	Name       string `json:"name"`
	Source     string `json:"source"`
	SourceInfo *struct {
		Path   string `json:"path"`
		Source string `json:"source"`
	} `json:"sourceInfo"`
}

// TestStepAgentGuardLoadedByProductionLaunch (AC3): a pi process born the
// way the daemon births a step process actually loads the guard extension
// from the dedicated step agent directory.
func TestStepAgentGuardLoadedByProductionLaunch(t *testing.T) {
	if _, err := exec.LookPath("pi"); err != nil {
		t.Skip("pi is not on PATH (CI): the load proof runs where the harness is installed")
	}
	// an IMPLICIT operator source with no credentials: the warning path —
	// the extension set does not depend on credentials
	cfg := Config{DataDir: t.TempDir(), OperatorAgentDir: t.TempDir()}
	cfg.FillDefaults()
	if err := cfg.SyncStepAgentDir(); err != nil {
		t.Fatalf("sync: %v", err)
	}
	sp := newPiSpawner(&cfg)
	cmd := sp.buildCmd(SpawnParams{WorkItemID: "wi_guard", CapsuleDir: t.TempDir(), SpawnN: 1})
	// owner plan-review #9: the test owns the process group (buildCmd
	// does not set it; the daemon's spawn layers do)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Stderr = nil // the no-credentials warning is expected noise
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start pi: %v", err)
	}
	defer killProcessGroup(t, cmd)

	if _, err := io.WriteString(stdin, "{\"id\":\"g\",\"type\":\"get_commands\"}\n"); err != nil {
		t.Fatalf("write get_commands: %v", err)
	}
	_ = stdin.Close()

	// a bounded read: the harness answers or the deadline fails the test
	if f, ok := stdout.(*os.File); ok {
		_ = f.SetReadDeadline(time.Now().Add(30 * time.Second))
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	answered := false
	for sc.Scan() {
		line := sc.Text()
		var rec struct {
			ID   string `json:"id"`
			Data struct {
				Commands []guardCommand `json:"commands"`
			} `json:"data"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil || rec.ID != "g" {
			continue
		}
		answered = true
		stepAgentDir := filepath.Join(cfg.StepAgentDir(), "extensions", "guard", "index.ts")
		found := false
		for _, c := range rec.Data.Commands {
			if c.SourceInfo != nil && strings.HasPrefix(c.SourceInfo.Path, cfg.OperatorAgentDir+string(os.PathSeparator)) {
				t.Errorf("a command sourced from the operator agent dir reached the step process: %s (%s)", c.Name, c.SourceInfo.Path)
			}
			if c.Name != "step-guard" {
				continue
			}
			found = true
			if c.Source != "extension" {
				t.Errorf("step-guard is not extension-sourced: %q", c.Source)
			}
			if c.SourceInfo == nil || c.SourceInfo.Path != stepAgentDir {
				t.Errorf("step-guard sourceInfo.path = %v, want %s", c.SourceInfo, stepAgentDir)
			}
		}
		if !found {
			t.Fatalf("the step process did not load the guard extension (no step-guard in get_commands):\n%s", line)
		}
		break
	}
	if !answered {
		t.Fatal("pi never answered get_commands (30 s deadline)")
	}
}

// TestStepAgentRealPiGuardBlocksBackground (AC4, opt-in): in a REAL pi
// step session a bash call that starts a background job nobody waits for
// comes back blocked with the step guard reason — read from the RPC
// record stream's tool-result entry — while an ordinary command passes.
func TestStepAgentRealPiGuardBlocksBackground(t *testing.T) {
	if os.Getenv("POLYFORGE_TEST_REAL_PI") != "1" {
		t.Skip("this drive burns real model tokens with the operator's credentials; set POLYFORGE_TEST_REAL_PI=1 to run it")
	}
	if testing.Short() {
		t.Skip("short mode: skipping the real-pi guard drive")
	}
	piBin := os.Getenv("POLYFORGE_TEST_PI_BIN")
	if piBin == "" {
		piBin = "pi"
	}
	src := os.Getenv("POLYFORGE_TEST_OPERATOR_AGENT_DIR")
	if src == "" {
		src = OperatorAgentDirDefault()
	}
	if _, err := os.Stat(filepath.Join(src, "auth.json")); err != nil {
		t.Skipf("no model service credentials at %s/auth.json: %v", src, err)
	}

	capDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(capDir, "session"), 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := Config{PiBin: piBin, DataDir: t.TempDir(), OperatorAgentDir: src, OperatorAgentDirExplicit: true}
	cfg.FillDefaults()
	sp := newPiSpawner(&cfg)

	var (
		mu      sync.Mutex
		records []string
	)
	h, err := sp.Spawn(context.Background(), SpawnParams{
		WorkItemID:    "wi_guardreal",
		CapsuleDir:    capDir,
		SpawnN:        1,
		InitialPrompt: "Call the bash tool exactly once with the command: sleep 30 & — then reply DONE.",
		Journal:       func(pid int, startedAt string) error { return nil },
		OnRecord: func(line string) {
			mu.Lock()
			records = append(records, line)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("spawn of the real harness failed: %v", err)
	}
	defer func() { _ = h.Terminate(5 * time.Second) }()

	// a bounded wait for the blocked tool result: the RPC stream's
	// tool_execution_end record carries isError and the result text
	blocked := false
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) && !blocked {
		time.Sleep(500 * time.Millisecond)
		mu.Lock()
		blocked, _ = scanGuardBashResults(records, true)
		mu.Unlock()
	}
	if !blocked {
		mu.Lock()
		dump := strings.Join(records, "\n")
		mu.Unlock()
		t.Fatalf("the background bash call was never blocked with the step guard reason:\n%s", dump)
	}

	// the counter-proof on the SAME session (owner plan-review #8): an
	// ordinary command passes, written through the handle's own line
	// writer. The first run must be SETTLED first — a bare prompt sent
	// mid-run is refused by pi (r1#2: "Agent is already processing…"),
	// so the proof would never arrive.
	firstSettled := false
	deadline = time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) && !firstSettled {
		time.Sleep(500 * time.Millisecond)
		mu.Lock()
		firstSettled = scanRecordType(records, "agent_settled")
		mu.Unlock()
	}
	if !firstSettled {
		mu.Lock()
		dump := strings.Join(records, "\n")
		mu.Unlock()
		t.Fatalf("the first run never settled before the counter-proof could be sent:\n%s", dump)
	}
	p, ok := h.(*proc)
	if !ok {
		t.Fatalf("unexpected handle type %T", h)
	}
	mu.Lock()
	records = nil
	mu.Unlock()
	second := `{"id":"pf-g2","type":"prompt","message":"Call the bash tool once with the command: echo ok — then reply DONE."}` + "\n"
	if err := p.writeLine("pf-g2", []byte(second)); err != nil {
		t.Fatalf("send the counter-proof prompt: %v", err)
	}
	// pi must ACCEPT the prompt: the response record for pf-g2 says so
	accepted := false
	deadline = time.Now().Add(1 * time.Minute)
	for time.Now().Before(deadline) && !accepted {
		time.Sleep(500 * time.Millisecond)
		mu.Lock()
		for _, line := range records {
			var rec struct {
				Type    string `json:"type"`
				Command string `json:"command"`
				ID      string `json:"id"`
				Success *bool  `json:"success"`
			}
			if json.Unmarshal([]byte(line), &rec) != nil || rec.ID != "pf-g2" {
				continue
			}
			if rec.Success != nil && *rec.Success {
				accepted = true
			} else {
				t.Fatalf("pi refused the counter-proof prompt: %s", line)
			}
			break
		}
		mu.Unlock()
	}
	if !accepted {
		mu.Lock()
		dump := strings.Join(records, "\n")
		mu.Unlock()
		t.Fatalf("pi never acknowledged the counter-proof prompt:\n%s", dump)
	}
	passed := false
	deadline = time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) && !passed {
		time.Sleep(500 * time.Millisecond)
		mu.Lock()
		passed, _ = scanGuardBashResults(records, false)
		mu.Unlock()
	}
	if !passed {
		mu.Lock()
		dump := strings.Join(records, "\n")
		mu.Unlock()
		t.Fatalf("the ordinary echo ok call did not pass cleanly:\n%s", dump)
	}
}

// scanRecordType answers whether any record on the stream is of the
// given type (the harness's own settled marker, not a timing guess).
func scanRecordType(lines []string, want string) bool {
	for _, line := range lines {
		var rec struct {
			Type string `json:"type"`
		}
		if json.Unmarshal([]byte(line), &rec) == nil && rec.Type == want {
			return true
		}
	}
	return false
}

// scanGuardBashResults reads the session's bash tool_execution_end records
// (owner plan-review #8: the verdict comes from isError and the result
// text, never from timing): wantBlocked matches an errored result naming
// the step guard reason; the counter-proof matches a clean result whose
// text carries `ok` and no step-guard note.
func scanGuardBashResults(lines []string, wantBlocked bool) (bool, string) {
	for _, line := range lines {
		var rec struct {
			Type     string `json:"type"`
			ToolName string `json:"toolName"`
			IsError  bool   `json:"isError"`
			Result   struct {
				Content []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if json.Unmarshal([]byte(line), &rec) != nil {
			continue
		}
		if rec.Type != "tool_execution_end" || rec.ToolName != "bash" {
			continue
		}
		var text strings.Builder
		for _, c := range rec.Result.Content {
			text.WriteString(c.Text)
		}
		blob := text.String()
		if wantBlocked {
			if rec.IsError && strings.Contains(blob, "step guard: blocked") {
				return true, blob
			}
		} else if !rec.IsError && strings.Contains(blob, "ok") && !strings.Contains(blob, "step guard") {
			return true, blob
		}
	}
	return false, ""
}
