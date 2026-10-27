package runtime

// Spawn regressions: the birth isolation profile (three flags — the
// extension system stays reachable because the subagent tool is
// extension-provided, per the v0 live-tested baseline), the env sanitize
// point (POLYFORGE_*/PF_* prefixes + renamed secret values + the
// deliberate PF_DAEMON_SOCKET addition), the spawn journal, the RecordTap
// binding, and the real RPC envelope end-to-end against a scripted harness
// process (prompt commands in, ack routed to its waiter by
// (method, correlation)).

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/spawn"
)

func TestSpawnIsolationFlags(t *testing.T) {
	sp := newPiSpawner(&Config{PiBin: "/nonexistent-pi"})
	cmd := sp.buildCmd(SpawnParams{
		WorkItemID: "wi_iso", CapsuleDir: "/caps", SessionHandle: "ab12cd34", SpawnN: 7,
	})
	args := cmd.Args
	if args[0] != "/nonexistent-pi" {
		t.Fatalf("argv[0] must be the harness binary, got %v", args)
	}
	joined := strings.Join(args, " ")
	for _, want := range []string{"--mode rpc", "--no-skills", "--no-context-files", "--no-prompt-templates"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("argv must contain %q, got %v", want, args)
		}
	}
	// --no-extensions is DELIBERATELY ABSENT (the v0 baseline): it would
	// remove the harness-native subagent tool the executor depends on.
	if strings.Contains(joined, "--no-extensions") {
		t.Fatal("argv must NOT contain --no-extensions")
	}
	if cmd.Dir != "/caps" {
		t.Fatalf("cwd must be the capsule root, got %q", cmd.Dir)
	}
}

// The step process's harness identity is the DEDICATED agent directory
// under the daemon data dir, with its own session store (addendum 2,
// owner ruling A): the operator's agent dir — settings.json and the
// user-level packages it declares — is mechanically out of reach.
func TestSpawnStepAgentDirectoryEnv(t *testing.T) {
	cfg := Config{PiBin: "/nonexistent-pi", DataDir: t.TempDir(), OperatorAgentDir: t.TempDir()}
	cfg.FillDefaults()
	sp := newPiSpawner(&cfg)
	cmd := sp.buildCmd(SpawnParams{WorkItemID: "wi_dir", CapsuleDir: "/caps", SpawnN: 1})
	env := strings.Join(cmd.Env, "\n")
	if !strings.Contains(env, "PI_CODING_AGENT_DIR="+cfg.StepAgentDir()+"\n") &&
		!strings.HasSuffix(env, "PI_CODING_AGENT_DIR="+cfg.StepAgentDir()) {
		t.Fatalf("the child env must point the harness at the dedicated agent dir %s, got:\n%s", cfg.StepAgentDir(), env)
	}
	if !strings.Contains(env, "PI_CODING_AGENT_SESSION_DIR="+cfg.StepAgentSessionsDir()) {
		t.Fatalf("the child env must carry the step session store %s, got:\n%s", cfg.StepAgentSessionsDir(), env)
	}
	// the dedicated dir is NOT the operator's
	if strings.Contains(env, "PI_CODING_AGENT_DIR="+cfg.OperatorAgentDir) {
		t.Fatal("the step process must never run against the operator agent dir")
	}
	// and the isolation stays flag-compatible: no --no-extensions (the
	// subagent tool is extension-provided)
	if strings.Contains(strings.Join(cmd.Args, " "), "--no-extensions") {
		t.Fatal("argv must NOT contain --no-extensions")
	}
	// an override of the sessions root governs both the locator and the
	// child (tests, relocated installs)
	cfg2 := Config{PiBin: "/nonexistent-pi", DataDir: t.TempDir(), HarnessRoot: "/elsewhere"}
	cfg2.FillDefaults()
	env2 := strings.Join(newPiSpawner(&cfg2).buildCmd(SpawnParams{CapsuleDir: "/caps"}).Env, "\n")
	if !strings.Contains(env2, "PI_CODING_AGENT_SESSION_DIR=/elsewhere/step-agent-sessions") {
		t.Fatalf("a harness-root override must move the pi session store with the root, got:\n%s", env2)
	}
}

// polyforge#211: pi uses PI_CODING_AGENT_SESSION_DIR verbatim as its
// session directory and writes sessions FLAT into it (the per-cwd
// dashified subdirectory exists only in its default store). The dir the
// spawn hands the child must therefore be exactly the dir the live-
// transcript locator scans, or no pi transcript ever reaches the
// capsule's session/ (every commit boundary emits transcript_missing).
// The test writes a session file where pi would — flat into the env
// value — and requires both locator entry points to find it.
func TestSpawnPiSessionDirIsWhereTheLocatorLooks(t *testing.T) {
	cfg := Config{PiBin: "/nonexistent-pi", DataDir: t.TempDir(), OperatorAgentDir: t.TempDir()}
	cfg.FillDefaults()
	capsuleDir := filepath.Join(t.TempDir(), "capsules", "polyforge-211")
	if err := os.MkdirAll(capsuleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := newPiSpawner(&cfg).buildCmd(SpawnParams{WorkItemID: "wi_loc", CapsuleDir: capsuleDir, SpawnN: 1})
	var sessionDir string
	for _, kv := range cmd.Env {
		if v, ok := strings.CutPrefix(kv, "PI_CODING_AGENT_SESSION_DIR="); ok {
			sessionDir = v
		}
	}
	if sessionDir == "" {
		t.Fatalf("the child env must carry PI_CODING_AGENT_SESSION_DIR, got:\n%s", strings.Join(cmd.Env, "\n"))
	}
	spawnAt := time.Now()
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	live := filepath.Join(sessionDir, "2026-10-18T12-33-17-007Z_01a14f00-86ce-75c6-872f-5c6fe65c675d.jsonl")
	if err := os.WriteFile(live, []byte("{\"type\":\"session\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := LocateLiveTranscript(cfg.StepAgentSessionsDir(), capsuleDir, spawnAt); got != live {
		t.Fatalf("LocateLiveTranscript = %q, want the session pi wrote into the spawn's session dir %q", got, live)
	}
	if got := LocateTranscript(cfg.StepAgentSessionsDir(), cfg.ClaudeConfigDir(), capsuleDir, spawnAt, "pi", ""); got != live {
		t.Fatalf("LocateTranscript(pi) = %q, want %q", got, live)
	}
	// another capsule's spawn gets its own dir: the store is shared by
	// every workspace, so a flat root would let one WI adopt another's
	other := newPiSpawner(&cfg).buildCmd(SpawnParams{CapsuleDir: filepath.Join(t.TempDir(), "polyforge-212")})
	for _, kv := range other.Env {
		if v, ok := strings.CutPrefix(kv, "PI_CODING_AGENT_SESSION_DIR="); ok && v == sessionDir {
			t.Fatalf("two capsules must not share one pi session dir %q", v)
		}
	}
}

func TestSpawnResumeAddsSessionArg(t *testing.T) {
	sp := newPiSpawner(&Config{})
	cmd := sp.buildCmd(SpawnParams{TranscriptPath: "/caps/session/t.jsonl", Resume: true, CapsuleDir: "/caps"})
	joined := strings.Join(cmd.Args, " ")
	if !strings.Contains(joined, "--session /caps/session/t.jsonl") {
		t.Fatalf("resume derivation must pass the transcript, got %v", cmd.Args)
	}
	fresh := sp.buildCmd(SpawnParams{CapsuleDir: "/caps"})
	if strings.Contains(strings.Join(fresh.Args, " "), "--session") {
		t.Fatal("fresh derivation must not pass --session")
	}
}

func TestSpawnEnvSanitized(t *testing.T) {
	t.Setenv("POLYFORGE_API_KEY", "machine-key")
	t.Setenv("PF_LEAK", "leak")
	t.Setenv("PLAIN_VAR", "keep-me")
	// the retired PF_AGENT_ROLE marker and an inherited PF_DAEMON_SOCKET
	// are stripped with the rest of the inherited PF_* family; the socket
	// the step gets is the daemon's own.
	t.Setenv("PF_AGENT_ROLE", "spoofed-role")
	t.Setenv("PF_CHAT_ID", "chat_leak") // a chat id is a notification label: never reaches a step agent
	t.Setenv("PF_DAEMON_SOCKET", "/spoofed.sock")
	t.Setenv("SOME_OTHER_NAME", "machine-key") // renamed secret: value match
	sp := newPiSpawner(&Config{MachineID: "m-env", DaemonSocket: "/d/daemon.sock", EnvSecrets: []string{"machine-key"}})
	cmd := sp.buildCmd(SpawnParams{WorkItemID: "wi_env", CapsuleDir: "/caps", SessionHandle: "ab12cd34", SpawnN: 2})
	env := strings.Join(cmd.Env, "\n")
	if strings.Contains(env, "POLYFORGE_API_KEY") || strings.Contains(env, "PF_LEAK=") {
		t.Fatalf("POLYFORGE_*/PF_* must be stripped, got:\n%s", env)
	}
	if strings.Contains(env, "PF_CHAT_ID") {
		t.Fatalf("PF_CHAT_ID must be stripped, got:\n%s", env)
	}
	if strings.Contains(env, "/spoofed.sock") {
		t.Fatalf("an inherited PF_DAEMON_SOCKET must be stripped, got:\n%s", env)
	}
	if strings.Contains(env, "SOME_OTHER_NAME=machine-key") {
		t.Fatal("a renamed variable carrying the configured secret must be stripped")
	}
	if !strings.Contains(env, "PLAIN_VAR=keep-me") {
		t.Fatal("the blacklist must not strip unrelated variables")
	}
	for _, want := range []string{
		"PF_WI_ID=wi_env", "PF_DAEMON_ID=m-env", "PF_DAEMON_SOCKET=/d/daemon.sock",
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("env must carry %q, got:\n%s", want, env)
		}
	}
	// PF_SESSION_HANDLE / PF_SPAWN_N are never injected: the derivation
	// identity reaches the harness through the capsule spawn journal,
	// never env.
	for _, gone := range []string{"PF_SESSION_HANDLE=", "PF_SPAWN_N="} {
		if strings.Contains(env, gone) {
			t.Fatalf("the deleted %s must not be injected, got:\n%s", gone, env)
		}
	}
}

func TestSpawnEnvRetiredVarsNeverInjected(t *testing.T) {
	sp := newPiSpawner(&Config{})
	cmd := sp.buildCmd(SpawnParams{WorkItemID: "wi_env", CapsuleDir: "/caps"})
	env := strings.Join(cmd.Env, "\n")
	// the deleted identity/role vars must never come back; a Config
	// without a daemon socket injects none
	for _, gone := range []string{"PF_INSTANCE_ID", "PF_SESSION_ID", "PF_GENERATION", "PF_DAEMON_SOCKET", "PF_AGENT_ROLE"} {
		if strings.Contains(env, gone) {
			t.Fatalf("the deleted %s must not be injected, got:\n%s", gone, env)
		}
	}
}

// TestSpawnSessionIdentityEnvReachesChild drives the REAL spawn path
// (scripted harness) and proves the child's environment carries the
// cleanup markers and NOT the deleted identity pair (PF_SESSION_HANDLE /
// PF_SPAWN_N reach the harness through the capsule spawn journal) nor
// the session secret.
func TestSpawnSessionIdentityEnvReachesChild(t *testing.T) {
	capDir, sp := scriptedHarness(t)
	envDump := filepath.Join(capDir, "child-env.txt")
	t.Setenv("GO_BE_ENV", envDump)
	sock := filepath.Join(t.TempDir(), "elsewhere", "daemon.sock")
	sp.cfg.DaemonSocket = sock
	secret := fakeSession("wi_inst")
	h, err := sp.Spawn(context.Background(), SpawnParams{
		WorkItemID:    "wi_inst",
		CapsuleDir:    capDir,
		SessionHandle: api.SessionFingerprint(secret),
		SpawnN:        2,
		InitialPrompt: "begin",
		Journal:       func(pid int, startedAt string) error { return nil },
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer func() { _ = h.Terminate(3 * time.Second) }()

	raw, err := os.ReadFile(envDump)
	for deadline := time.Now().Add(5 * time.Second); (err != nil || len(raw) == 0) && time.Now().Before(deadline); {
		time.Sleep(20 * time.Millisecond)
		raw, err = os.ReadFile(envDump)
	}
	if err != nil {
		t.Fatalf("the harness never dumped its env: %v", err)
	}
	fp := api.SessionFingerprint(secret)
	if strings.Contains(string(raw), "PF_SESSION_HANDLE=") || strings.Contains(string(raw), "PF_SPAWN_N=") {
		t.Fatalf("the deleted identity env must not reach the child, got:\n%s", raw)
	}
	if strings.Contains(string(raw), fp) {
		t.Fatalf("the public fingerprint is journal-only now; it must not ride the env, got:\n%s", raw)
	}
	if strings.Contains(string(raw), "PF_AGENT_ROLE") {
		t.Fatalf("the retired role marker must not reach the child, got:\n%s", raw)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatalf("the session SECRET must never reach the child env, got:\n%s", raw)
	}
	// the step's pf dials the daemon where it actually listens
	if !strings.Contains(string(raw), `"PF_DAEMON_SOCKET=`+sock+`"`) {
		t.Fatalf("the child env lacks the daemon's socket %s, got:\n%s", sock, raw)
	}
}

func TestEnvSanitizerUnit(t *testing.T) {
	t.Setenv("POLYFORGE_URL", "https://x")
	t.Setenv("PF_TOKEN", "y")
	t.Setenv("KEEP", "z")
	e := newEnvSanitizer("the-secret", "")
	t.Setenv("RENAMED_SECRET", "the-secret")
	env := e.childEnv("PF_WI_ID=w")
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "POLYFORGE_URL") || strings.Contains(joined, "PF_TOKEN") || strings.Contains(joined, "RENAMED_SECRET") {
		t.Fatalf("sanitize failed:\n%s", joined)
	}
	if !strings.Contains(joined, "KEEP=z") || !strings.Contains(joined, "PF_WI_ID=w") {
		t.Fatalf("sanitize over-stripped:\n%s", joined)
	}
}

func TestSpawnJournalWritten(t *testing.T) {
	capDir := t.TempDir()
	p := SpawnParams{WorkItemID: "wi_j", CapsuleDir: capDir, SessionHandle: "ab12cd34", SpawnN: 1, TranscriptPath: "/t"}
	if err := writeSpawnJournal(p, 4242, "then", "ab12cd34"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(capDir, capsuleDirRuntimeState, "spawn-journal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var entry map[string]any
	if err := json.Unmarshal(raw[:len(raw)-1], &entry); err != nil {
		t.Fatalf("journal line must be JSON: %v (%s)", err, raw)
	}
	if entry["pid"].(float64) != 4242 || entry["spawn_token"] != "ab12cd34" || entry["work_item"] != "wi_j" {
		t.Fatalf("journal entry wrong: %+v", entry)
	}
	// v1.5 additive fields: the derivation counter and the PUBLIC handle
	if entry["spawn_n"].(float64) != 1 || entry["session_handle"] != "ab12cd34" {
		t.Fatalf("journal entry must carry spawn_n and session_handle: %+v", entry)
	}
	// the SECRET never lands in the capsule journal (B3)
	if _, ok := entry["session_id"]; ok {
		t.Fatalf("the journal must not carry a session_id key: %+v", entry)
	}
	// appends, not truncates — and the second line's spawn_n is the max
	if err := writeSpawnJournal(p, 4243, "then2", "ab12cd34#1"); err != nil {
		t.Fatal(err)
	}
	raw2, _ := os.ReadFile(filepath.Join(capDir, capsuleDirRuntimeState, "spawn-journal.jsonl"))
	if n := strings.Count(string(raw2), "\n"); n != 2 {
		t.Fatalf("journal must append, found %d lines", n)
	}
	if got := spawnJournalMaxN(capDir); got != 1 {
		t.Fatalf("spawnJournalMaxN = %d, want 1", got)
	}
}

// spawnJournalMaxN: the B2 fresh-clone source — the highest recorded
// spawn_n wins, missing/unparsable journals answer 0.
func TestMaxSpawnNFromJournal(t *testing.T) {
	capDir := t.TempDir()
	if got := spawnJournalMaxN(capDir); got != 0 {
		t.Fatalf("absent journal must answer 0, got %d", got)
	}
	path := filepath.Join(capDir, capsuleDirRuntimeState, "spawn-journal.jsonl")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	lines := []byte(
		`{"work_item":"wi","spawn_n":1,"pid":1}` + "\n" +
			`{"work_item":"wi","spawn_n":5,"pid":2}` + "\n" +
			`{"work_item":"wi","pid":3}` + "\n" + // pre-v1.5 line: no spawn_n
			`not json` + "\n")
	if err := os.WriteFile(path, lines, 0o644); err != nil {
		t.Fatal(err)
	}
	if got := spawnJournalMaxN(capDir); got != 5 {
		t.Fatalf("spawnJournalMaxN = %d, want 5", got)
	}
}

// TestPiRPCInitialPrompt: the initial prompt rides a prompt command
// ({"id":"pf-init","type":"prompt","message":..}) on the harness's stdin.
func TestPiRPCInitialPrompt(t *testing.T) {
	capDir, sp := scriptedHarness(t)
	h, err := sp.Spawn(context.Background(), SpawnParams{
		WorkItemID: "wi_rpc", CapsuleDir: capDir, SessionHandle: "ab12cd34", SpawnN: 1,
		InitialPrompt: "begin the work item",
		Journal:       func(pid int, startedAt string) error { return nil },
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer func() { _ = h.Terminate(3 * time.Second) }()
	var raw []byte
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		raw, _ = os.ReadFile(filepath.Join(capDir, "stdin-capture.jsonl"))
		if len(raw) > 0 {
			break
		}
	}
	var cmd struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &cmd); err != nil {
		t.Fatalf("stdin line must be a prompt command: %v (%q)", err, raw)
	}
	if cmd.Type != "prompt" || cmd.ID != "pf-init" || cmd.Message != "begin the work item" {
		t.Fatalf("initial prompt envelope wrong: %+v", cmd)
	}
}

// TestSpawnOnRecordFeedsEveryStdoutRecord pins the OnRecord binding: every
// raw stdout record the production spawner reads also reaches the
// dispatch's callback — the driver binds it to the harness translator, so
// the supervisor's per-dispatch state receives exactly this line.
func TestSpawnOnRecordFeedsEveryStdoutRecord(t *testing.T) {
	capDir, sp := scriptedHarness(t)

	var mu sync.Mutex
	var seen []string
	h, err := sp.Spawn(context.Background(), SpawnParams{
		WorkItemID:    "wi_tap",
		CapsuleDir:    capDir,
		SessionHandle: "ab12cd34",
		SpawnN:        1,
		InitialPrompt: "begin",
		Journal:       func(pid int, startedAt string) error { return nil },
		OnRecord: func(line string) {
			mu.Lock()
			seen = append(seen, line)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	defer func() { _ = h.Terminate(3 * time.Second) }()

	// the scripted harness writes one fixture record at derivation; the
	// tap must see it verbatim
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n := len(seen)
		mu.Unlock()
		if n >= 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("OnRecord never saw the startup record, saw %q", seen)
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if !strings.Contains(seen[0], `"type":"agent_start"`) {
		t.Fatalf("tap must see the raw record verbatim, got %q", seen[0])
	}
}

// TestSpawnExitInfoCarriesCodeStderrAndTailRecords pins the process-exit
// signal: a harness that writes its last record and an error line to
// stderr and exits non-zero is announced through OnExit AFTER its stdout
// drained (the tail record reached the tap first) and ExitInfo reports the
// code and the stderr tail — the raw material of the driver's
// exited_without_result classification.
func TestSpawnExitInfoCarriesCodeStderrAndTailRecords(t *testing.T) {
	capDir, sp := scriptedHarness(t)
	t.Setenv("GO_BE_DIE", "model example-gateway/nope not found")

	var mu sync.Mutex
	var order []string
	var exitInfo spawn.ExitInfo
	h, err := sp.Spawn(context.Background(), SpawnParams{
		WorkItemID:    "wi_exit",
		CapsuleDir:    capDir,
		SessionHandle: "ab12cd34",
		SpawnN:        1,
		InitialPrompt: "begin",
		Journal:       func(pid int, startedAt string) error { return nil },
		OnRecord: func(line string) {
			mu.Lock()
			order = append(order, "record")
			mu.Unlock()
		},
		OnExit: func(info spawn.ExitInfo) {
			mu.Lock()
			order = append(order, "exit")
			exitInfo = info
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !h.Done() {
		if time.Now().After(deadline) {
			t.Fatal("the dying harness never reported Done")
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) < 3 || order[len(order)-1] != "exit" {
		t.Fatalf("OnExit must come after every record: %v", order)
	}
	if exitInfo.Code != 7 || !strings.Contains(exitInfo.StderrTail, "model example-gateway/nope not found") {
		t.Fatalf("exit info = %+v, want code 7 and the stderr tail", exitInfo)
	}
	er, ok := h.(spawn.ExitReporter)
	if !ok {
		t.Fatal("a harness process must implement spawn.ExitReporter")
	}
	if got := er.ExitInfo(); got.Code != 7 {
		t.Fatalf("ExitInfo() after Done = %+v", got)
	}
}

// scriptedHarness builds a piSpawner whose PiBin is a wrapper re-invoking
// THIS test binary as the harness (TestHelperProcess): the harness emits one
// fixture record at derivation and captures every stdin line.
func scriptedHarness(t *testing.T) (string, *piSpawner) {
	t.Helper()
	capDir := t.TempDir()
	t.Setenv("GO_BE_HARNESS", "1")
	t.Setenv("GO_BE_CAPTURE", filepath.Join(capDir, "stdin-capture.jsonl"))
	// PiBin points at a wrapper that re-invokes THIS test binary with only
	// the helper test selected; the spawner's argv (--mode rpc ...) rides
	// after `--` so the test framework's flag parsing ignores it.
	t.Setenv("GO_TESTBIN", os.Args[0])
	wrapper := filepath.Join(capDir, "fake-pi")
	script := "#!/bin/sh\nexec \"$GO_TESTBIN\" -test.run '^TestHelperProcess$' -- \"$@\"\n"
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(capDir, "session"), 0o755); err != nil {
		t.Fatal(err)
	}
	return capDir, newPiSpawner(&Config{PiBin: wrapper})
}

// ---------------------------------------------------------------------------
// the scripted harness: a re-invocation of the test binary (GO_BE_HARNESS)
// that emits one fixture record at derivation, echoes correlation-carrying
// ack records onto stdout for the pf/* control methods and captures every
// stdin line for the assertions above.

func TestHelperProcess(t *testing.T) {
	if os.Getenv("GO_BE_HARNESS") != "1" {
		return // the normal test run: nothing to do
	}
	// optional env dump for the deliberate-addition regressions (e.g.
	// PF_INSTANCE_ID reaching the real child).
	if dump := os.Getenv("GO_BE_ENV"); dump != "" {
		if blob, err := json.Marshal(os.Environ()); err == nil {
			_ = os.WriteFile(dump, append(blob, '\n'), 0o644)
		}
	}
	capture := os.Getenv("GO_BE_CAPTURE")
	f, err := os.OpenFile(capture, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
	if err != nil {
		os.Exit(3)
	}
	defer f.Close()
	// one fixture record at derivation: the tap must see it verbatim
	os.Stdout.WriteString(`{"type":"agent_start"}` + "\n")
	if die := os.Getenv("GO_BE_DIE"); die != "" {
		// the dying harness: a last error-turn record, a stderr line, exit 7
		os.Stdout.WriteString(`{"type":"agent_end","messages":[{"role":"assistant","stopReason":"error","errorMessage":"` + die + `"}]}` + "\n")
		fmt.Fprintln(os.Stderr, "fatal: "+die)
		os.Exit(7)
	}
	br := bufio.NewReader(os.Stdin)
	for {
		line, rerr := br.ReadString('\n')
		if line != "" {
			fmt.Fprintln(f, strings.TrimRight(line, "\n"))
		}
		if rerr != nil {
			os.Exit(0) // stdin EOF: graceful shutdown
		}
	}
}

const capsuleDirRuntimeState = "runtime-state"
