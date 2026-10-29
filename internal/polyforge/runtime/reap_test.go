package runtime

// Crash-orphan reaping (v2-converge §6 bug 3): no handle, agent pid 0, the
// step pid only in the capsule spawn journal.

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func writeJournalPID(t *testing.T, capsuleDir string, pid int, startedAt string) {
	t.Helper()
	dir := filepath.Join(capsuleDir, "runtime-state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(map[string]any{"pid": pid, "started_at": startedAt, "spawn_n": 1})
	if err := os.WriteFile(filepath.Join(dir, "spawn-journal.jsonl"), append(blob, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestReapOrphansKillsJournaledTreeWithPidZero(t *testing.T) {
	wi := runMarker("wi_reap1")
	pid, startedAt := startTree(t, wi)
	capsuleDir := t.TempDir()
	writeJournalPID(t, capsuleDir, pid, startedAt)

	r := &Runtime{}
	j := &job{wi: wi, capsuleDir: capsuleDir} // agentPID 0, handle nil: the post-crash shape
	r.killAgentTree(j)

	waitDead(t, pid)
	if n := groupMemberCount(pid); n != 0 {
		t.Fatalf("group %d still holds %d members after reap", pid, n)
	}
}

func TestReapOrphansEnvMarkerSweepWithoutJournal(t *testing.T) {
	wi := runMarker("wi_reap2")
	pid, _ := startTree(t, wi)
	// escapee-shaped: not the journaled pid, no journal at all
	(&Runtime{}).reapOrphans(wi, t.TempDir())
	waitDead(t, pid)
}

func TestReapOrphansSkipsReusedPid(t *testing.T) {
	// an unrelated live process (no PF_WI_ID) whose pid the journal names
	// with a start stamp from long before it was born
	cmd := exec.Command("sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	capsuleDir := t.TempDir()
	writeJournalPID(t, capsuleDir, cmd.Process.Pid, "2001-01-01T00:00:00Z")

	(&Runtime{}).reapOrphans(runMarker("wi_reap3"), capsuleDir)

	if !procExists(cmd.Process.Pid) {
		t.Fatal("unrelated process was killed through a stale journal pid")
	}
	// and without a stamp, an unmarked live pid is not ours either
	writeJournalPID(t, capsuleDir, cmd.Process.Pid, "")
	(&Runtime{}).reapOrphans(runMarker("wi_reap3"), capsuleDir)
	if !procExists(cmd.Process.Pid) {
		t.Fatal("unmarked process killed through a stamp-less journal pid")
	}
}

// Two daemons on one host can both hold a step process for the SAME WI id
// (the WI moved from one to the other). A sweep by daemon A must leave
// daemon B's process alone: the env-marker scan needs BOTH PF_WI_ID and
// PF_DAEMON_ID to match.
//
// The markers are scoped to this test process (runMarker): the sweep and
// the scan read the WHOLE host's /proc, so constant markers made this
// test flaky whenever the runtime package ran concurrently in another
// process — that run's "daemon-b" sleeper doubled B's scan result, and
// its "daemon-a" sweep killed this run's processes.
func TestEnvMarkerSweepRequiresBothMarkers(t *testing.T) {
	wi := runMarker("wi_two_daemons")
	daemonA, daemonB := runMarker("daemon-a"), runMarker("daemon-b")
	start := func(daemon string) *exec.Cmd {
		cmd := exec.Command("sleep", "300")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Env = append(os.Environ(), "PF_WI_ID="+wi, "PF_DAEMON_ID="+daemon)
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		return cmd
	}
	a, b := start(daemonA), start(daemonB)

	(&Runtime{cfg: Config{MachineID: daemonA}}).reapOrphans(wi, t.TempDir())
	_ = a.Wait() // reaped by the sweep
	if procExists(a.Process.Pid) {
		t.Fatal("daemon A's own marked process must be swept")
	}
	if !procExists(b.Process.Pid) {
		t.Fatal("daemon A's sweep killed daemon B's process for the same WI id")
	}
	if got := scanEnvMarker(wi, daemonA); len(got) != 0 {
		t.Fatalf("scan by A still sees %v", got)
	}
	if got := scanEnvMarker(wi, daemonB); len(got) != 1 || got[0] != b.Process.Pid {
		t.Fatalf("scan by B = %v, want [%d]", got, b.Process.Pid)
	}
}

// TestReapOrphansWalksTheProcessListOncePerWI (D11): a WI whose journal
// names many long-dead step pids costs one liveness walk plus the one
// env-marker/cwd sweep — not a full Cleanup (L1/L2/L5 walks) per pid —
// while a journaled pid that is still alive is torn down as before.
func TestReapOrphansWalksTheProcessListOncePerWI(t *testing.T) {
	wi := runMarker("wi_reap_walks")
	capsuleDir := t.TempDir()
	dir := filepath.Join(capsuleDir, "runtime-state")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	var lines []byte
	for i := 0; i < 12; i++ {
		cmd := exec.Command("true")
		if err := cmd.Run(); err != nil {
			t.Fatal(err)
		}
		blob, _ := json.Marshal(map[string]any{"pid": cmd.Process.Pid, "started_at": "", "spawn_n": i + 1})
		lines = append(append(lines, blob...), '\n')
	}
	livePID, startedAt := startTree(t, wi)
	blob, _ := json.Marshal(map[string]any{"pid": livePID, "started_at": startedAt, "spawn_n": 13})
	lines = append(append(lines, blob...), '\n')
	if err := os.WriteFile(filepath.Join(dir, "spawn-journal.jsonl"), lines, 0o644); err != nil {
		t.Fatal(err)
	}
	var walks atomic.Int64
	orig := procListAllFn
	procListAllFn = func() ([]int, error) { walks.Add(1); return orig() }
	defer func() { procListAllFn = orig }()

	(&Runtime{}).reapOrphans(wi, capsuleDir)

	waitDead(t, livePID)
	// 1 liveness walk + the live tree's own Cleanup (L1 snapshot, L2
	// group proof/empty polls) + the WI sweep (L4, L5, verify) — 6 here,
	// bounded independently of the dead pids (each cost 3 walks before: 42).
	if n := walks.Load(); n > 8 {
		t.Fatalf("reap walked the process list %d times for 13 journal pids (12 dead)", n)
	}
}

func TestCleanupPollIsCoarse(t *testing.T) {
	if cleanupPoll < 100*time.Millisecond {
		t.Fatalf("cleanupPoll = %s, want at least 100ms", cleanupPoll)
	}
}
