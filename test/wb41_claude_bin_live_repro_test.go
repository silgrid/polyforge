package test

// wb41_claude_bin_live_repro_test.go: the repro for the `claude_bin`
// half of workbench#41 (F2).
//
// `pf config set claude_bin …` reports the key applied (live), and step
// spawns already read the live value. The daemon's own harness report —
// GET /status?harnesses=1, the fact `pf doctor`'s "harness claude" row
// judges — still answered the startup binary before this fix, so the
// daemon used and reported two different binaries after a reload.
//
// The test starts a real `pf daemon` whose claude_bin comes from the
// cli.toml FILE (a fake claude binary A — no --claude-bin flag, so no
// key is flag-pinned), checks that the harness report names A, reloads
// claude_bin to a second fake binary B through the shipped writer, and
// asserts the daemon's harness report names B. Real processes, real
// UDS, no internals.
//
// Baseline posture: on the claim tree `pf config` does not exist, so
// this test is an assertion-level red (the verb is unknown) and never a
// build red.

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/polyforge/cli"
)

// wb41FakeClaude writes an executable that answers `--version` the way
// the daemon's harness probe expects.
func wb41FakeClaude(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	body := "#!/bin/sh\necho '2.1.296 (Claude Code)'\n"
	if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// wb41StatusClaudeBin answers the daemon's reported claude binary —
// exactly the fact `pf doctor`'s harness claude row reads.
func wb41StatusClaudeBin(t *testing.T, sock string) string {
	t.Helper()
	raw, err := cli.NewDaemonClient(sock).StatusWithHarnesses()
	if err != nil {
		t.Fatalf("GET /status?harnesses=1 on %s: %v", sock, err)
	}
	var st cli.StatusResponse
	if err := json.Unmarshal(raw, &st); err != nil {
		t.Fatalf("status undecodable: %v (%s)", err, raw)
	}
	return st.Harnesses["claude"].Bin
}

// wb41ConfigFile writes a cli.toml with one [workspace] mapping and the
// given claude_bin, so the daemon's binary starts from the FILE (not a
// --claude-bin flag, which would pin the key against reload).
func wb41ConfigFile(t *testing.T, path, claudeBin string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("server = \"http://127.0.0.1:1\"\napi_key = %q\nuser_id = \"usr_root\"\nclaude_bin = %q\n\n"+
		"[workspace.%q]\nprojects = [\"e2e\"]\n", "wb41key", claudeBin, t.TempDir())
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// wb41Daemon is one real `pf daemon` process launched WITHOUT
// --claude-bin (the w113 helper pins it, which would make the reload
// report "overridden by flag" instead of exercising the live path).
type wb41Daemon struct {
	t        *testing.T
	sock     string
	home     string
	logPath  string
	logFile  *os.File
	cmd      *exec.Cmd
	waitExit chan error
}

func wb41StartDaemon(t *testing.T, cfg string) *wb41Daemon {
	t.Helper()
	dir := t.TempDir()
	d := &wb41Daemon{
		t:       t,
		sock:    filepath.Join(shortTmp(t, "wb41sock"), "d.sock"),
		home:    t.TempDir(),
		logPath: filepath.Join(dir, "daemon.log"),
	}
	xf, err := os.Create(d.logPath)
	if err != nil {
		t.Fatal(err)
	}
	d.logFile = xf
	pfPath := filepath.Join(v1Bins(t), "pf")
	fakepi := filepath.Join(v1Bins(t), "fakepi")
	dataDir := t.TempDir()
	args := []string{
		"daemon",
		"--socket", d.sock,
		"--api-key", "wb41key",
		"--machine-id", "m-wb41",
		"--data-dir", dataDir,
		"--workflows-dir", strippedWorkflowsDir(t, filepath.Join(v1Root(t), "workflows")),
		"--pi-bin", fakepi,
		"--config", cfg,
	}
	cmd := exec.Command(pfPath, args...)
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + d.home,
		"XDG_CONFIG_HOME=" + filepath.Join(d.home, ".config"),
		fakepiTestPIDEnv + "=" + os.Getenv(fakepiTestPIDEnv),
		"GIT_AUTHOR_NAME=pf-e2e",
		"GIT_AUTHOR_EMAIL=pf-e2e@invalid",
		"GIT_COMMITTER_NAME=pf-e2e",
		"GIT_COMMITTER_EMAIL=pf-e2e@invalid",
	}
	cmd.Dir = dataDir
	cmd.Stdout, cmd.Stderr = xf, xf
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	d.cmd = cmd
	d.waitExit = make(chan error, 1)
	go func() { d.waitExit <- cmd.Wait() }()
	t.Cleanup(d.stop)

	deadline := time.Now().Add(30 * time.Second)
	for {
		conn, derr := net.Dial("unix", d.sock)
		if derr == nil {
			conn.Close()
			return d
		}
		select {
		case werr := <-d.waitExit:
			t.Fatalf("daemon exited during startup: %v (log: %s)", werr, d.logTail())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon never served %s (log: %s)", d.sock, d.logTail())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func (d *wb41Daemon) logTail() string {
	raw, err := os.ReadFile(d.logPath)
	if err != nil {
		return fmt.Sprintf("(unreadable log: %v)", err)
	}
	if len(raw) > 4096 {
		raw = raw[len(raw)-4096:]
	}
	return string(raw)
}

func (d *wb41Daemon) stop() {
	if d.logFile != nil {
		_ = d.logFile.Close()
	}
	if d.cmd == nil || d.cmd.Process == nil {
		return
	}
	_ = d.cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-d.waitExit:
	case <-time.After(10 * time.Second):
		killGroup(d.cmd)
		<-d.waitExit
	}
	killGroup(d.cmd)
	d.cmd = nil
}

// TestWB41ReloadedClaudeBinIsWhatTheDaemonReports is F2: after a reload
// reports claude_bin applied, the daemon's harness status must name the
// reloaded binary, not the startup one.
func TestWB41ReloadedClaudeBinIsWhatTheDaemonReports(t *testing.T) {
	dir := t.TempDir()
	binA := wb41FakeClaude(t, dir, "claude-a")
	binB := wb41FakeClaude(t, dir, "claude-b")
	cfg := wb41ConfigFile(t, filepath.Join(dir, "cli.toml"), binA)
	d := wb41StartDaemon(t, cfg)

	// Premise: the harness report follows the daemon's claude binary at
	// startup, so a change in it is observable through the same route.
	if got := wb41StatusClaudeBin(t, d.sock); got != binA {
		t.Fatalf("premise broken: startup harness report names %q, want the configured %q", got, binA)
	}

	out, code := w113Pf(t, nil, w113Env{home: d.home, config: cfg, socket: d.sock},
		"config", "set", "claude_bin", binB)
	if code != 0 {
		t.Fatalf("pf config set claude_bin: exit %d (output %q)", code, out)
	}
	if !strings.Contains(out, "claude_bin: applied") {
		t.Fatalf("the reload did not report claude_bin applied live: %q", out)
	}

	if got := wb41StatusClaudeBin(t, d.sock); got != binB {
		t.Fatalf("F2 reproduced: the reload reported claude_bin applied (output %q) but the daemon's "+
			"harness report still names %q, not the reloaded %q — `pf doctor`'s harness claude row judges "+
			"the startup binary after the reload", out, got, binB)
	}
}
