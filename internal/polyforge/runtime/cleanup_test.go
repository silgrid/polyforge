package runtime

// Layered cleanup regressions (reference.md §6.2), against REAL child
// processes: a parent that forks a grandchild, the (pid,starttime)
// PID-reuse guard, the env-marker sweep and the warn-only cwd scan.

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// runMarker scopes a test's PF_WI_ID / PF_DAEMON_ID marker value to this
// test process. The env-marker layer scans the WHOLE host's /proc, so a
// constant marker collides with the same test running concurrently in
// another process (a second checkout, a release gate): one run's sweep
// kills, or its scan counts, the other run's processes.
func runMarker(base string) string { return base + "-" + strconv.Itoa(os.Getpid()) }

// startTree spawns `sh -c "sleep 300 & exec sleep 300"` as its own group
// leader: a parent with one grandchild in the same pgid.
func startTree(t *testing.T, wi string) (pid int, startedAt string) {
	t.Helper()
	cmd := spawnTreeCmd(wi)
	startedAt = nowStamp()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid = cmd.Process.Pid
	// the tree never outlives the test, whatever the test's path
	t.Cleanup(func() { _ = syscall.Kill(-pid, syscall.SIGKILL); _ = cmd.Wait() })
	// wait until the tree has both members
	deadline := time.Now().Add(5 * time.Second)
	for groupMemberCount(pid) < 2 {
		if time.Now().After(deadline) {
			t.Fatalf("tree never grew a grandchild (members=%d)", groupMemberCount(pid))
		}
		time.Sleep(10 * time.Millisecond)
	}
	return pid, startedAt
}

func spawnTreeCmd(wi string) *exec.Cmd {
	c := exec.Command("sh", "-c", "sleep 300 & exec sleep 300")
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if wi != "" {
		c.Env = append(os.Environ(), "PF_WI_ID="+wi, "PF_DAEMON_ID=")
	}
	return c
}

func groupMemberCount(pgid int) int {
	entries, err := os.ReadDir(procRoot)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil || pid <= 1 {
			continue
		}
		state, _, pgrp, _, ok := procStat(pid)
		if ok && state != "Z" && int(pgrp) == pgid {
			n++
		}
	}
	return n
}

func TestCleanupFullTreeWithoutCgroup(t *testing.T) {
	wi := runMarker("wi_c1")
	pid, startedAt := startTree(t, wi)
	rep, err := Cleanup(CleanupTarget{PID: pid, Pgid: pid, StartedAt: startedAt, WIID: wi}, 5*time.Second)
	if err != nil {
		t.Fatalf("cleanup errored: %v (report %+v)", err, rep)
	}
	waitDead(t, pid)
	if n := groupMemberCount(pid); n != 0 {
		t.Fatalf("group %d still holds %d members", pid, n)
	}
	for _, layer := range []string{"l1-ppid-snapshot", "l2-pgid", "l3-identity", "l4-env-marker", "l5-cwd-scan"} {
		if !layerApplied(rep, layer) {
			t.Fatalf("layer %s not applied: %+v", layer, rep.Layers)
		}
	}
	if len(rep.Survivors) != 0 {
		t.Fatalf("survivors: %v", rep.Survivors)
	}
}

func TestCleanupPidReuseGuard(t *testing.T) {
	// signalIdent refuses to touch a pid whose CURRENT occupant no longer
	// matches the snapshot's starttime — the mis-kill protection.
	cmd := exec.Command("sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	defer func() { _ = killFn(-pid, syscall.SIGKILL) }()
	id, ok := procIdentOf(pid)
	if !ok {
		t.Fatal("no ident for the fresh sleeper")
	}
	rep := &CleanupReport{}
	// a snapshot from "before" this process existed: mismatched starttime
	signalIdent(procIdent{pid: pid, start: id.start - 12345}, syscall.SIGKILL, rep)
	mustBeAlive(t, pid)
	if len(rep.Killed) != 0 {
		t.Fatalf("mismatched identity must not be killed: %v", rep.Killed)
	}
	// matching identity: killed
	signalIdent(id, syscall.SIGKILL, rep)
	waitDead(t, pid)
}

func TestCleanupReusedRootPidIsNotKilled(t *testing.T) {
	// the recorded pid is now occupied by an UNRELATED process whose
	// creation time does not match: the root/group signals are skipped and
	// the process survives
	cmd := exec.Command("sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	defer func() { _ = killFn(-pid, syscall.SIGKILL) }()

	stale := time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
	rep, err := Cleanup(CleanupTarget{PID: pid, Pgid: pid, StartedAt: stale}, 3*time.Second)
	if err != nil {
		t.Fatalf("cleanup errored: %v", err)
	}
	mustBeAlive(t, pid)
	if !mentionsReuse(rep) {
		t.Fatalf("the report must warn about the pid reuse: %+v", rep.Warnings)
	}
}

func TestCleanupLayer4EnvMarkerSweep(t *testing.T) {
	wi := runMarker("wi_c5_marker")
	cmd := exec.Command("sleep", "300")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Env = append(os.Environ(), "PF_WI_ID="+wi, "PF_DAEMON_ID=")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	t.Cleanup(func() { _ = killFn(-pid, syscall.SIGKILL); _ = cmd.Wait() })
	// the escapee sits OUTSIDE any target tree: only the env marker finds it
	rep, err := Cleanup(CleanupTarget{WIID: wi}, 5*time.Second)
	if err != nil {
		t.Fatalf("cleanup errored: %v (report %+v)", err, rep)
	}
	if !layerApplied(rep, "l4-env-marker") {
		t.Fatalf("layer 4 must be applied: %+v", rep.Layers)
	}
	waitDead(t, pid)
}

func TestCleanupLayer5CwdScanWarnsOnly(t *testing.T) {
	capDir := t.TempDir()
	cmd := exec.Command("sleep", "300")
	cmd.Dir = capDir // cwd inside the capsule; no marker, own group
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	pid := cmd.Process.Pid
	defer func() { _ = killFn(-pid, syscall.SIGKILL) }()

	rep, err := Cleanup(CleanupTarget{WIID: runMarker("wi_nobody"), CapsuleDir: capDir}, 3*time.Second)
	if err != nil {
		t.Fatalf("cleanup errored: %v", err)
	}
	mustBeAlive(t, pid) // layer 5 NEVER kills
	found := false
	for _, w := range rep.Warnings {
		if strings.Contains(w, "cwd inside the capsule") {
			found = true
		}
	}
	if !found {
		t.Fatalf("layer 5 must warn about the cwd-squatter: %+v", rep.Warnings)
	}
}

func layerApplied(rep *CleanupReport, layer string) bool {
	for _, l := range rep.Layers {
		if l == layer {
			return true
		}
	}
	return false
}

func mentionsReuse(rep *CleanupReport) bool {
	for _, w := range rep.Warnings {
		if strings.Contains(w, "reuse") {
			return true
		}
	}
	return false
}
