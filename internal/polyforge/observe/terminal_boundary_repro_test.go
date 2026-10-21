package observe

// terminal_boundary_repro_test.go — the polyforge#211 reproduction (the
// #204 incident shape): a gate FAIL straight to terminal — one long final
// dispatch, no mid-step state change, no step_done — must ride an
// execution-state push (the terminal-carrying execution.json version
// lands on the capsule git chain) and the commit-boundary transcript
// sync (the dispatch's transcript lands in session/).
//
// The reported failure theory was that the final version ends
// UNPERSISTED on the git chain and the transcript is never synced.
// #211's root-cause REFUTED the terminal-push half: in #204 the terminal
// version was published and pushed. The empty session/ came from the pi
// session-dir/locator mismatch, which is pinned by
// TestSpawnPiSessionDirIsWhereTheLocatorLooks in
// internal/polyforge/runtime/spawn_test.go. This test stubs the live
// locator, so it does not exercise that cause. It passes on base
// 2a879f0d and is kept (owner ruling, #211) as a regression bar for the
// terminal push and the boundary sync.
//
// The fact sequence below is the driver's real emission order for that
// flow: the step_start transition's exec_committed (v1) precedes
// step_started; the long dispatch emits nothing else (no mid-step state
// change); step_ended precedes the gate-verdict transition's
// exec_committed (v2 — the ONE write that carries workflow_status
// failed_gate plus the FAIL verdict output on the gate step); the
// handle exit (Run returned at the terminal status) follows immediately.
// The push is the REAL gitops.CommitPush against a real capsule worktree
// + external git-dir + bare remote, and the transcript sync locates a
// real live harness session file — so "persisted on the git chain" and
// "session/ empty" are asserted literally, not through a recording double.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
)

// chainExec answers the remote chain's execution.json (nil when the chain
// has none yet).
func chainExec(t *testing.T, bare string) *capsule.Execution {
	t.Helper()
	out, err := exec.Command("git", "--git-dir", bare, "show",
		"refs/heads/main:runtime-state/execution.json").Output()
	if err != nil {
		return nil
	}
	var e capsule.Execution
	if json.Unmarshal(out, &e) != nil {
		return nil
	}
	return &e
}

// publishedVersion reports whether the monitor's events log carries a
// published event naming version v.
func publishedVersion(m *Monitor, v int64) bool {
	for _, e := range eventsOf(m, EvPublished) {
		var b struct {
			Version int64 `json:"version"`
		}
		if json.Unmarshal(e.Body, &b) == nil && b.Version == v {
			return true
		}
	}
	return false
}

func TestGateFailStraightToTerminalRidesPushAndTranscriptSync(t *testing.T) {
	// --- the capsule: a real git worktree + external git-dir + a local
	// bare repo as the push remote (gitops.CommitPush's real shape) ---
	dir := t.TempDir()
	capsuleDir := filepath.Join(dir, "capsule")
	gitDir := filepath.Join(dir, "git", "wi_test.git")
	bare := filepath.Join(dir, "hub", "wi_test.git")
	for _, d := range []string{
		filepath.Dir(bare), filepath.Dir(gitDir),
		filepath.Join(capsuleDir, "runtime-state"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if out, err := exec.Command("git", "init", "--bare", bare).CombinedOutput(); err != nil {
		t.Fatalf("git init --bare: %v: %s", err, out)
	}
	if out, err := exec.Command("git", "--git-dir", bare, "config",
		"receive.advertisePushOptions", "true").CombinedOutput(); err != nil {
		t.Fatalf("git config advertisePushOptions: %v: %s", err, out)
	}
	if err := os.MkdirAll(capsuleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "--initial-branch=main",
		"--separate-git-dir="+gitDir, capsuleDir).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	if err := os.WriteFile(filepath.Join(capsuleDir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	commit := exec.Command("git", "commit", "--allow-empty", "-m", "init")
	commit.Dir = capsuleDir
	if out, err := commit.CombinedOutput(); err != nil {
		t.Fatalf("initial commit: %v: %s", err, out)
	}
	git := gitops.Opts{
		WorkItemID: "wi_test", CapsuleDir: capsuleDir, GitDir: gitDir,
		RemoteURL: bare, Username: "wi_test", Secret: "test-secret",
	}

	// --- the live harness transcript (the pi session-store shape): the
	// file appears mid-dispatch, as the harness writes its session file
	// while the 13-minute dispatch runs. ---
	liveSrc := filepath.Join(dir, "pi-sessions", "--capsule--", "2026-10-18T09-07-00-000Z_live.jsonl")
	liveBody := []byte("{\"type\":\"session\",\"id\":\"s204\"}\n" +
		"{\"type\":\"tool_call\",\"tool\":\"bash\",\"n\":23}\n" +
		"{\"type\":\"message_end\"}\n")

	// --- the monitor, over the real capsule: the real commit+push, a
	// locator that finds the live transcript when it exists ---
	log, err := NewEventLog(dir)
	if err != nil {
		t.Fatal(err)
	}
	ob := supervise.NewQueue[protocol.OBMsg]()
	cfg := testCfg()
	cfg.push = gitops.CommitPush
	cfg.locateLive = func(capsuleDir string, spawnAt time.Time, harness, session string) string {
		if _, err := os.Stat(liveSrc); err != nil {
			return "" // not born yet (the first seconds of the dispatch)
		}
		return liveSrc
	}
	h := newFakeHandle("att_1")
	m := &Monitor{
		wi:  WIInfo{WorkItem: "wi_test", CapsuleDir: capsuleDir, Git: git},
		cfg: cfg, log: log, ob: ob, link: supervise.NewLink("wi_test"),
		rtQ:        supervise.NewQueue[Attach](),
		stopCh:     make(chan struct{}),
		gate:       make(chan struct{}, 1),
		pushDone:   make(chan pushResult, 1),
		verifyDone: make(chan verifyResult, 1),
	}
	m.attach(Attach{
		Msg:    protocol.RTMsg{Kind: protocol.RTStarted, WorkItem: "wi_test", SpawnN: 1},
		Handle: h,
	})
	m.phase = phaseBetween
	m.phaseSince = m.now()
	m.announceStart()
	go m.run()
	t.Cleanup(func() {
		m.requestStop()
		_ = log.Close()
	})

	// writeExecVersion lands one execution.json the way the driver's
	// transition does (atomic tmp+rename, Version bumped).
	writeExecVersion := func(version int64, status string, out map[string]any) {
		t.Helper()
		exec := capsule.Execution{
			CapsuleID: "cap_wi_test", WorkItemID: "wi_test",
			CurrentStep: "review", WorkflowStatus: status,
			Steps: map[string]*capsule.Step{
				"review": {Status: capsule.StepInProgress, Output: out},
			},
			ReviewRounds: map[string]int64{"review": 1},
			UpdatedAt:    time.Now().UTC().Format(time.RFC3339Nano),
			Version:      version,
		}
		blob, err := json.Marshal(exec)
		if err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(capsuleDir, "runtime-state", "execution.json")
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, blob, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, path); err != nil {
			t.Fatal(err)
		}
	}

	// --- the claimed flow, in the driver's emission order ---
	dst := filepath.Join(capsuleDir, "session", "session-ra_wi_test.jsonl")

	// 1. the step_start transition (v1: running, review in_progress) —
	// the driver commits it BEFORE the dispatch is spawned.
	writeExecVersion(1, capsule.WorkflowStatusRunning, nil)
	m.link.Fact(supervise.ExecCommitted{Version: 1, Step: "review"})

	// 2. the final gate step's one long dispatch starts.
	stepStarted(t, m, 1, "review", dst, false)

	// the step_start version publishes (the #204 audit's "last published
	// = 2s after step start"): the chain carries v1 before the dispatch
	// runs on. The live transcript does not exist yet (the harness has
	// not written its session file), so this boundary syncs nothing.
	soon(t, 10*time.Second, "the step_start version published", func() bool {
		return publishedVersion(m, 1)
	})
	soon(t, 10*time.Second, "the step_start version on the chain", func() bool {
		e := chainExec(t, bare)
		return e != nil && e.Version == 1 && e.WorkflowStatus == capsule.WorkflowStatusRunning
	})

	// 3. the long dispatch (13 minutes, 23 tool calls in the incident):
	// no state change, no further transition — the harness writes its
	// live session file while it runs.
	if err := os.MkdirAll(filepath.Dir(liveSrc), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(liveSrc, liveBody, 0o644); err != nil {
		t.Fatal(err)
	}

	// 4. the dispatch ends (the driver announces step_ended before it
	// settles the collected envelope).
	m.link.Fact(supervise.StepEnded{
		Step: "review", SpawnN: 1, Outcome: supervise.OutcomeResult, ExitCode: 0,
	})

	// 5. the gate FAIL verdict straight to terminal (no on_fail rewind
	// edge): the ONE transition that carries workflow_status
	// failed_gate and the verdict output on the gate step.
	writeExecVersion(2, capsule.WorkflowStatusFailedGate, map[string]any{
		"verdict":     "FAIL: the fix does not remove the cause",
		"fail_reason": "the defect is still reachable through a second call site",
	})
	m.link.Fact(supervise.ExecCommitted{Version: 2, Step: "review"})

	// 6. the driver's Run returns at the terminal status: the handle
	// exits immediately after the verdict transition.
	h.die()

	// --- the claimed end state, asserted literally ---
	// (a) the terminal-carrying version rides an execution-state push:
	// the remote chain's execution.json is v2, failed_gate — not the
	// step_start version the #204 audit found.
	var chain *capsule.Execution
	soon(t, 15*time.Second, "the terminal version persisted on the git chain", func() bool {
		if e := chainExec(t, bare); e != nil {
			chain = e
			return e.Version == 2 && e.WorkflowStatus == capsule.WorkflowStatusFailedGate
		}
		return false
	})
	if chain == nil || chain.Steps["review"] == nil {
		t.Fatalf("the terminal chain state carries no review step: %+v", chain)
	}
	if v, _ := chain.Steps["review"].Output["verdict"].(string); v != "FAIL: the fix does not remove the cause" {
		t.Fatalf("SYMPTOM (#204): the FAIL verdict is not on the chain: review output %v",
			chain.Steps["review"].Output)
	}
	// (b) the transcript rode the terminal boundary: session/ carries
	// the dispatch's transcript bytes (the #204 audit found it empty).
	raw, err := os.ReadFile(dst)
	if err != nil || string(raw) != string(liveBody) {
		t.Fatalf("SYMPTOM (#204): session/ transcript missing at the terminal boundary: "+
			"read err=%v, got %q, want the live transcript bytes %q", err, raw, liveBody)
	}
	// (c) the publish is the monitor's own record of the push (the
	// event lands when the sub-goroutine's result is drained — after the
	// chain already shows the commit, so it is awaited, not assumed).
	soon(t, 10*time.Second, "a published event for the terminal-carrying version", func() bool {
		return publishedVersion(m, 2)
	})
	// the chain's commit history names the versions the boundary pushes
	// carried (the step_start one and the terminal one).
	out, err := exec.Command("git", "--git-dir", bare, "log", "--format=%s", "main").CombinedOutput()
	if err != nil {
		t.Fatalf("chain log: %v: %s", err, out)
	}
	logs := strings.Split(string(out), "\n")
	if !strings.Contains(strings.Join(logs, "\n"), "execution v2") {
		t.Fatalf("SYMPTOM (#204): the chain carries no commit for the terminal version; log: %s", out)
	}
}

func testEventsDump(m *Monitor) string {
	var out []string
	for _, e := range m.log.SinceAfter(m.wi.WorkItem, 0) {
		out = append(out, e.Kind+" "+string(e.Body))
	}
	return strings.Join(out, "\n")
}
