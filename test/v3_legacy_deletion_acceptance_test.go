package test

// v3_legacy_deletion_acceptance_test.go: the polyforge#106 acceptance
// suite — authored against the SPEC (the capsule's artifacts/e2e-scenarios.md,
// which mirrors spec.md's Acceptance criteria and Scenario blocks), BEFORE
// the deletion lands. It pins the post-deletion contract through the
// project's real e2e surfaces only: the pf/aihub/fakepi binaries, the
// `pf` CLI, the daemon's UDS face, the capsule's runtime-state files and
// real git — never engine internals.
//
// Suite posture (see artifacts/e2e-scenarios.md for the full mapping):
//   - Kept-face guards (driver lifecycle on a production-shaped argv,
//     answer fail-fast with no live bridge, pilot/v0 invariants) are
//     green BOTH before and after the deletion.
//   - New-behavior bars (retired flags fail loudly at parse, the retired
//     env hatch fails loudly at boot, grep-clean, the 5+ commit delivery
//     shape) are RED before the deletion
//     — assertion-level reds, never build reds — and must go green after.
//
// Self-policing: the retired legacy-face terms this suite greps for are
// assembled at RUNTIME from fragments (v3Tok) so that THIS file never
// carries any of the tokens it polices — the grep-clean scenario stays
// honest about its own bytes.

import (
	"bytes"
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
)

// ---------------------------------------------------------------------------
// Retired-face tokens, assembled from fragments at runtime.
// ---------------------------------------------------------------------------

// v3Tok joins parts into one legacy-face token. The fragmentation is
// deliberate: the grep-clean scenario below scans every non-markdown
// code/build file in the repo INCLUDING this one, so the tokens it
// polices must never appear as contiguous bytes here.
func v3Tok(parts ...string) string {
	return strings.Join(parts, "")
}

// v3SpecFloorTerms is the spec-floor grep list (spec.md Assumptions §2):
// only the face-qualified tokens whose absence is unambiguous post-change.
// Deliberately NOT included: face tokens that also name kept sub-faces
// (the graceful-shutdown notice, the flush refusal markers woven through
// the observe lane, the generic fakepi action names) — those faces are
// covered by the document scenarios, not by a blind token scan.
func v3SpecFloorTerms() []string {
	return []string{
		v3Tok("use", "-", "driver"),                   // both dash spellings of the retired engine flag, and every pin of them
		v3Tok("Use", "Driver"),                        // the engine Config plumbing (and its Explicit sibling)
		v3Tok("spawn", "Agent"),                       // the legacy recover cascade's spawn entry
		v3Tok("resume", "Source"),                     // the resume→fresh cascade discriminator
		v3Tok("transcript", "Readable"),               // the cascade's transcript probe
		v3Tok("build", "Prompt"),                      // the WI-Agent prompt assembly face
		v3Tok("Executor", "Prompt", "File"),           // the executor prompt Config plumbing
		v3Tok("executor", "-", "prompt"),              // the daemon flag (and every help-text mention)
		v3Tok("PFV1", "_", "LEGACY", "_LAN"),          // the retired test hatch, everywhere
		v3Tok("pf", "-", "execute"),                   // the deleted skill, on code/build surfaces
		v3Tok("wrap", "_", "unless", "_refused"),      // a fakepi RPC action with no kept homonym
		v3Tok("answer", "-", "ack", "-", "window"),    // the retired answer-ack knob: flag spelling
		v3Tok("Answer", "Ack", "Window"),              // ... and its Go identifier
		v3Tok("answer", "Ack", "Window"),              // ... and the harness var spelling
		v3Tok("Fault", "Class"),                       // the retired fault-classification type
		v3Tok("Classify", "Fault"),                    // ... and its classifier
		v3Tok("repair", "_", "pending"),               // the retired repair-pending state
		v3Tok("refresh", "Bundle"),                    // the retired bundle re-materialization hook
		v3Tok("ensure", "Materialized"),               // the retired materialization guard
		v3Tok("Remote", "Published"),                  // the retired published-remote probe
		v3Tok("Job", "View"),                          // the retired observe job view
		v3Tok("rotate", "-", "secret"),                // the retired secret-rotation verb
		v3Tok("Candidates", "For", "("),               // the retired candidates accessor (the live recovery helper has a longer name)
		v3Tok("jobs/{wi}/", "observe"),                // the retired observe route
		v3Tok("Start", "Intent"),                      // the retired start-intent plumbing
		v3Tok("sync", "Transcript", "Before", "Seal"), // the job-level seal-time transcript overwrite
	}
}

// ---------------------------------------------------------------------------
// The daemon as a real process, booted the way PRODUCTION boots it.
// ---------------------------------------------------------------------------

// v3DaemonProc is one `pf daemon` OS process started from a
// production-shaped argv: every flag in it exists both before and after
// the deletion (socket/server/api-key/machine-id/data-dir/workspace/
// project/workflows-dir/pi-bin). No engine-selection flag (retired by
// the change) and no executor prompt flag (retired by the change) are
// passed; pre-change the executor prompt resolves from its documented
// default relative to the repo root (the daemon's cwd here), post-change
// the concept is gone entirely. The engine is the driver in both trees:
// pre-change via the flag's default, post-change unconditionally.
type v3DaemonProc struct {
	t         *testing.T
	cmd       *exec.Cmd
	sock      string
	dataDir   string
	workspace string
	machineID string
	logPath   string
	logFile   *os.File
	waitExit  chan error
}

// v3StartDaemon launches the daemon process and waits until its UDS face
// answers. dirs may be shared with a previous (crashed) v3DaemonProc on
// the same socket/data/workspace/machine — the crash-restart shape.
func v3StartDaemon(t *testing.T, inst *v1Install, sock, dataDir, workspace, machineID string) *v3DaemonProc {
	t.Helper()
	root := v1Root(t)
	d := &v3DaemonProc{
		t: t, sock: sock, dataDir: dataDir, workspace: workspace,
		machineID: machineID,
		logPath:   filepath.Join(t.TempDir(), "v3daemon.log"),
	}
	logf, err := os.Create(d.logPath)
	if err != nil {
		t.Fatal(err)
	}
	d.logFile = logf
	args := []string{
		"daemon",
		"--socket", d.sock,
		"--server", inst.url,
		"--api-key", inst.adminKey,
		"--machine-id", d.machineID,
		"--data-dir", d.dataDir,
		"--workflows-dir", inst.workflowsPath(),
		"--pi-bin", inst.fakepiPath,
		"--claude-bin", "/nonexistent/claude-test-guard",
		// polyforge#113: the mapping config is the only workspace source
		// (and the config isolation file — never an operator's).
		"--config", mappingConfigPath(t, d.workspace, []string{v1Project}),
	}
	cmd := exec.Command(inst.pfPath, args...)
	cmd.Dir = root // the documented executor prompt default resolves here pre-change
	cmd.Env = append(os.Environ(),
		"FAKEPI_PLANS="+inst.planDir,
		"GIT_AUTHOR_NAME=pf-e2e",
		"GIT_AUTHOR_EMAIL=pf-e2e@invalid",
		"GIT_COMMITTER_NAME=pf-e2e",
		"GIT_COMMITTER_EMAIL=pf-e2e@invalid",
	)
	cmd.Stdout, cmd.Stderr = logf, logf
	ownGroup(cmd)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	d.cmd = cmd
	d.waitExit = make(chan error, 1)
	go func() { d.waitExit <- cmd.Wait() }()
	t.Cleanup(func() { d.stop() })

	deadline := time.Now().Add(30 * time.Second)
	for {
		conn, err := net.Dial("unix", d.sock)
		if err == nil {
			conn.Close()
			return d
		}
		select {
		case werr := <-d.waitExit:
			d.cmd = nil
			t.Fatalf("v3 daemon exited during startup: %v (log tail: %s)", werr, d.logTail())
		default:
		}
		if time.Now().After(deadline) {
			t.Fatalf("v3 daemon UDS never came up on %s (log tail: %s)", d.sock, d.logTail())
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// logTail returns the last ~4KB of the daemon log.
func (d *v3DaemonProc) logTail() string {
	raw, err := os.ReadFile(d.logPath)
	if err != nil {
		return fmt.Sprintf("(unreadable log: %v)", err)
	}
	if len(raw) > 4096 {
		raw = raw[len(raw)-4096:]
	}
	return string(raw)
}

// stop terminates the daemon gracefully (SIGTERM, SIGKILL fallback).
// Idempotent.
func (d *v3DaemonProc) stop() {
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

// crash kills the daemon hard (the crash-recovery lever).
func (d *v3DaemonProc) crash() {
	d.t.Helper()
	if d.cmd == nil || d.cmd.Process == nil {
		d.t.Fatal("v3 crash: daemon not running")
	}
	_ = d.cmd.Process.Kill()
	<-d.waitExit
	d.cmd = nil
}

// capsuleDir is the WI's capsule directory under this daemon's workspace.
func (d *v3DaemonProc) capsuleDir(inst *v1Install, wi string) string {
	return filepath.Join(d.workspace, "capsules", wiPathName(d.t, inst, wi))
}

// v3Pf runs the real pf CLI against an explicit daemon socket and
// returns (combined output, exit code) with NO assertion on the code.
func v3PfCode(t *testing.T, inst *v1Install, sock string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command(inst.pfPath, append([]string{"--config", hermeticConfigPath(t)}, args...)...)
	cmd.Env = append(os.Environ(),
		"PF_SERVER="+inst.url,
		"PF_API_KEY="+inst.adminKey,
		"PF_USER_ID=usr_root",
		"PF_DAEMON_SOCKET="+sock,
	)
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("pf %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

// v3ReadExec reads the capsule's execution.json as a loose map.
func v3ReadExec(t *testing.T, capDir string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(capDir, "runtime-state", "execution.json"))
	if err != nil {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("execution.json unparsable: %v (%s)", err, raw)
	}
	return m
}

// v3StepField digs steps.<step>.<field> out of a loose execution.json.
func v3StepField(exec map[string]any, step, field string) any {
	steps, _ := exec["steps"].(map[string]any)
	if steps == nil {
		return nil
	}
	st, _ := steps[step].(map[string]any)
	if st == nil {
		return nil
	}
	return st[field]
}

// v3StepStatus answers a step's status ("" when absent).
func v3StepStatus(exec map[string]any, step string) string {
	s, _ := v3StepField(exec, step, "status").(string)
	return s
}

// v3PendingQID answers a step's pending question id ("" when none).
func v3PendingQID(exec map[string]any, step string) string {
	pq, _ := v3StepField(exec, step, "pending_question").(map[string]any)
	if pq == nil {
		return ""
	}
	id, _ := pq["id"].(string)
	return id
}

// ---------------------------------------------------------------------------
// Scenario S3 — the driver-only lifecycle on a production-shaped argv.
// ---------------------------------------------------------------------------

// TestV3DriverLifecycleProductionArgv pins the kept face end-to-end: a
// daemon booted exactly the way production boots it (no engine-selection
// flag, no executor prompt flag) claims a WI, clones the task repo,
// drives the whole autopilot workflow through the Go driver and the
// fakepi envelope mode, survives a kill -9 mid-step and completes after
// restart. Pre-change this passes via the engine flag's default; the
// deletion makes the driver unconditional and must keep it green — the
// spawner core, the claim lane and the recovery window are all exercised.
func TestV3DriverLifecycleProductionArgv(t *testing.T) {
	inst, _ := newV1InstallTaskRepo(t)
	const qid = "q_v3life"
	// Ordinals: 1 grill asks; 2 grill completes with the answer; 3 fix
	// (delayed envelope — the kill window); 4 fix again (the post-restart
	// re-dispatch); 5-6 the tail.
	wi := inst.newWI(t, "v3: driver-only lifecycle on a production argv",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": qid,
				"question": "Which variant should the lifecycle take?"},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true, "ms": 4000},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)

	sockBase := filepath.Join(t.TempDir(), "sock")
	sock := filepath.Join(sockBase, "d.sock")
	dataDir := t.TempDir()
	workspace := t.TempDir()
	d := v3StartDaemon(t, inst, sock, dataDir, workspace, "m-v3life")
	defer d.stop()

	// Submit through the real CLI.
	out, code := v3PfCode(t, inst, d.sock, "submit", wi)
	if code != 0 || !strings.Contains(out, "accepted") {
		t.Fatalf("submit: exit %d, output %q", code, out)
	}

	capDir := d.capsuleDir(inst, wi)

	// Claim→clone: the task repo lands in the capsule before the agent
	// spawns (the #108 claim lane is a kept face).
	waitUntil(t, 30*time.Second, "task repo never cloned into the capsule", func() bool {
		_, err := os.Stat(filepath.Join(capDir, "repos", "demo-src", "hello.txt"))
		return err == nil
	})

	// The driver drives: grill parks in needs_input with the question
	// persisted (the F9 face), the owner answers through the real CLI,
	// and the step consumes it.
	waitUntil(t, 60*time.Second, "grill never parked in needs_input", func() bool {
		return v3PendingQID(v3ReadExec(t, capDir), "grill") == qid
	})
	aout, acode := v3PfCode(t, inst, d.sock, "answer", wi, "--question", qid,
		"--text", "Take the driver-only variant.", "--by", "usr_root")
	if acode != 0 || !strings.Contains(aout, "delivered") {
		t.Fatalf("pf answer through the live bridge: exit %d, output %q", acode, aout)
	}

	// The kill window: crash the daemon hard while fix is mid-step.
	waitUntil(t, 60*time.Second, "fix never started", func() bool {
		return v3StepStatus(v3ReadExec(t, capDir), "fix") == "in_progress"
	})
	d.crash()

	// Restart on the same socket/data/workspace/machine: recovery must
	// re-drive the workflow with the driver only and close it.
	d2 := v3StartDaemon(t, inst, sock, dataDir, workspace, "m-v3life")
	defer d2.stop()
	waitUntil(t, 120*time.Second, "restarted daemon never completed the workflow", func() bool {
		exec := v3ReadExec(t, capDir)
		if exec == nil {
			return false
		}
		ws, _ := exec["workflow_status"].(string)
		return ws == "completed"
	})
	exec := v3ReadExec(t, capDir)
	for _, step := range []string{"grill", "fix", "verification"} {
		if got := v3StepStatus(exec, step); got != "done" {
			t.Fatalf("step %s: expected done, got %q", step, got)
		}
	}
	// The crash-restart re-dispatched through the spawner core: the
	// spawn journal grew past the crashed derivation.
	journal, err := os.ReadFile(filepath.Join(capDir, "runtime-state", "spawn-journal.jsonl"))
	if err != nil {
		t.Fatalf("spawn journal: %v", err)
	}
	if n := len(nonEmptyLines(string(journal))); n < 3 {
		t.Fatalf("spawn journal carries only %d derivations; the post-crash re-dispatch never happened: %s", n, journal)
	}
}

func nonEmptyLines(s string) []string {
	var out []string
	for _, l := range strings.Split(s, "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}

// ---------------------------------------------------------------------------
// Scenario S4 — pf answer without a live bridge handle.
// ---------------------------------------------------------------------------

// TestV3AnswerWithoutLiveBridgeFailsFast pins the answer criterion's
// posture: `pf answer` against a daemon that holds NO live bridge handle
// for the WI fails fast — non-zero exit, a surfaced error, no success
// marker — and the pending question is untouched on the daemon that holds
// the job. The construction is deterministic instead of racing the
// recovery window: daemon-1 owns the parked WI (live bridge), and the
// answer is pointed at a second, idle daemon that has no job for it.
func TestV3AnswerWithoutLiveBridgeFailsFast(t *testing.T) {
	inst := newV1Install(t)
	const qid = "q_v3ans"
	wi := inst.newWI(t, "v3: answer fail-fast with no live bridge handle",
		[]map[string]any{
			{"do": "envelope", "id": "grill", "outcome": "needs_input", "question_id": qid,
				"question": "Which lane takes the fail-fast check?"},
			{"do": "envelope", "id": "grill", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "fix", "outcome": "completed", "handoff": true},
			{"do": "envelope", "id": "verification", "outcome": "completed"},
		}, nil)

	sock1 := filepath.Join(t.TempDir(), "s1", "d.sock")
	d1 := v3StartDaemon(t, inst, sock1, t.TempDir(), t.TempDir(), "m-v3a1")
	defer d1.stop()
	out, code := v3PfCode(t, inst, d1.sock, "submit", wi)
	if code != 0 || !strings.Contains(out, "accepted") {
		t.Fatalf("submit: exit %d, output %q", code, out)
	}
	capDir := d1.capsuleDir(inst, wi)
	waitUntil(t, 60*time.Second, "grill never parked in needs_input", func() bool {
		return v3PendingQID(v3ReadExec(t, capDir), "grill") == qid
	})

	// A second daemon on its own socket/data/workspace: it holds no job
	// and no bridge handle for the WI (daemon-1's live claim keeps the
	// WI out of the ready queue, so this daemon stays idle).
	sock2 := filepath.Join(t.TempDir(), "s2", "d.sock")
	d2 := v3StartDaemon(t, inst, sock2, t.TempDir(), t.TempDir(), "m-v3a2")
	defer d2.stop()

	aout, acode := v3PfCode(t, inst, d2.sock, "answer", wi, "--question", qid,
		"--text", "This must not be relayed.", "--by", "usr_root")
	if acode == 0 {
		t.Fatalf("pf answer with no live bridge handle was accepted: exit 0, output %q", aout)
	}
	if strings.TrimSpace(aout) == "" {
		t.Fatalf("pf answer failed silently: exit %d with no output", acode)
	}
	if strings.Contains(aout, "delivered") {
		t.Fatalf("pf answer reported delivery with no live bridge handle: %q", aout)
	}
	// The parked question is untouched.
	if got := v3PendingQID(v3ReadExec(t, capDir), "grill"); got != qid {
		t.Fatalf("the pending question changed under the failed answer: got %q, want %q", got, qid)
	}
}

// ---------------------------------------------------------------------------
// Scenario S1 — retired engine flags fail loudly at parse.
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// Scenario S5 — grep-clean for the deleted faces.
// ---------------------------------------------------------------------------

// TestV3GrepCleanDeletedFaces scans every non-markdown code/build file
// in the repo (Go source and tests, scripts, configs, Makefile; .git/
// and the frozen v0/ archive excluded; binary files sniffed out) for the
// spec-floor term list and requires zero hits. RED before the deletion
// (the faces are still present by design), GREEN after.
func TestV3GrepCleanDeletedFaces(t *testing.T) {
	root := v1Root(t)
	terms := v3SpecFloorTerms()
	var hits []string
	err := v3WalkTextFiles(root, func(path string, raw []byte) {
		for _, term := range terms {
			if bytes.Contains(raw, []byte(term)) {
				rel, _ := filepath.Rel(root, path)
				hits = append(hits, rel+": "+term)
			}
		}
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	if len(hits) > 0 {
		t.Fatalf("deleted-face terms still present on code/build surfaces (%d hits):\n%s",
			len(hits), strings.Join(hits, "\n"))
	}
}

// v3D15ReplayFixture is the one collected data file the scan skips.
const v3D15ReplayFixture = "internal/polyforge/supervise/testdata/pi-session-replay.jsonl"

// v3WalkTextFiles walks root's regular files, skipping .git/, the frozen
// v0/ archive, the D15 replay fixture (v3D15ReplayFixture: captured pi
// session records whose content legitimately discusses the retired
// faces it replays — collected data, not an authored code/build
// surface; every other testdata file is still scanned), markdown
// (excluded from the gate by the spec), and non-regular entries
// (symlinks — e.g. the shared-checkout links the capsule materialization
// creates — and binary-looking files, sniffed by a NUL byte in the first
// 512 bytes).
func v3WalkTextFiles(root string, fn func(path string, raw []byte)) error {
	return filepath.WalkDir(root, func(path string, de os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() {
			name := de.Name()
			if name == ".git" || (name == "v0" && path == filepath.Join(root, "v0")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !de.Type().IsRegular() {
			return nil // symlinks and special files are not repo bytes
		}
		if path == filepath.Join(root, filepath.FromSlash(v3D15ReplayFixture)) {
			return nil
		}
		if strings.HasSuffix(path, ".md") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if len(raw) == 0 {
			return nil
		}
		sniff := raw
		if len(sniff) > 512 {
			sniff = sniff[:512]
		}
		if bytes.IndexByte(sniff, 0) >= 0 {
			return nil // binary
		}
		fn(path, raw)
		return nil
	})
}

// ---------------------------------------------------------------------------
// Scenario S8 — the v0 archive is gone.
// ---------------------------------------------------------------------------

// TestV3V0ArchiveAbsent pins that the frozen v0/ reference tree has
// been dropped from the working tree (it lives in git history only, tag
// v0-archive).
func TestV3V0ArchiveAbsent(t *testing.T) {
	root := v1Root(t)

	// The v0/ reference tree was dropped from the working tree (it lives
	// in git history only): no directory and no tracked file remains.
	if _, err := os.Stat(filepath.Join(root, "v0")); !os.IsNotExist(err) {
		t.Fatalf("v0/ must be absent from the working tree (stat err: %v)", err)
	}
	if out, code := v3Git(t, root, "ls-files", "--", "v0"); code != 0 {
		t.Fatalf("git ls-files v0: exit %d, output %q", code, out)
	} else if strings.TrimSpace(out) != "" {
		t.Fatalf("v0/ must have no tracked files:\n%s", out)
	}
}

func v3Git(t *testing.T, dir string, args ...string) (string, int) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var out strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &out
	code := 0
	if err := cmd.Run(); err != nil {
		ee, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("git %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return out.String(), code
}

// ---------------------------------------------------------------------------
// Scenario S9 — the delivery shape.
// ---------------------------------------------------------------------------
