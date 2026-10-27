package test

// proc_hygiene_test.go: process-leak discipline for the acceptance
// harness (polyforge#168). Every aihub / `pf daemon` the harness starts
// leads its own process group (Setpgid) and carries Pdeathsig=SIGKILL,
// its teardown ends the whole group and waits, and fakepi agents (which
// the daemon detaches into their own groups by design: they must outlive
// a daemon crash) watch the test binary's pid and exit once it is gone.
// On top of that, two sweeps over the binaries this run built:
//
//   - per top-level test, after every other cleanup of the test ran: any
//     process still executing from the bin dir is a leak of THAT test —
//     reported (t.Errorf) and killed. The acceptance tests never run in
//     parallel (no t.Parallel in this package), so a live process from
//     the bin dir at that point belongs to the finishing test.
//   - at package exit (TestMain): any survivor fails the run, is killed,
//     and the bin dir is removed.
//
// The bin dir is a fresh os.MkdirTemp per test binary, so both sweeps
// only ever touch processes this run started — never a concurrent run's,
// never the operator's installed pf.

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakepiTestPIDEnv names the env entry carrying the test binary's pid to
// every fakepi agent (through the daemon's environment): fakepi exits
// once that pid is gone, so an agent orphaned by a killed or timed-out
// test binary (no cleanup runs then) does not linger for hours.
const fakepiTestPIDEnv = "FAKEPI_TEST_PID"

// ownGroup prepares cmd (before Start) to lead its own process group and
// to be SIGKILLed by the kernel if the test binary dies first (a timeout
// panic or a kill skips every t.Cleanup).
func ownGroup(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}

// killGroup SIGKILLs the whole process group cmd leads (see ownGroup).
// Called right after the leader exited it reaps stragglers left in the
// group (e.g. a git child of the daemon): the group id cannot be reused
// while a member lives, and Linux allocates pids sequentially, so the id
// is not handed to an unrelated new group in that window.
func killGroup(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil || cmd.Process.Pid <= 1 {
		return
	}
	_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

// stopGroup ends a process started with ownGroup: SIGTERM to the leader
// (its graceful path), SIGKILL to the whole group after grace, and a
// final group kill once the leader is gone. exited must deliver the
// leader's exit (a goroutine running cmd.Wait / Process.Wait).
//
// An already-stopped process is left alone: its group id may since have
// been reused, so no signal is sent.
func stopGroup(cmd *exec.Cmd, exited <-chan struct{}, grace time.Duration) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	select {
	case <-exited:
		return
	default:
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case <-exited:
	case <-time.After(grace):
		killGroup(cmd)
		<-exited
	}
	killGroup(cmd)
}

// binSweepOnce marks the top-level tests whose bin-dir sweep is
// registered.
var binSweepOnce sync.Map // test name -> struct{}

// registerBinSweep registers the per-test bin-dir sweep for t's top-level
// test. Subtests are skipped: their cleanups run while the parent's
// processes are legitimately still alive. Called from v1Bins, the first
// harness call of every scenario, so the sweep is registered before (and
// therefore runs after) every daemon/aihub/orphan teardown.
func registerBinSweep(t *testing.T, dir string) {
	if strings.Contains(t.Name(), "/") {
		return
	}
	if _, dup := binSweepOnce.LoadOrStore(t.Name(), struct{}{}); dup {
		return
	}
	t.Cleanup(func() {
		binSweepOnce.Delete(t.Name()) // -count=N reruns register again
		if leaks := sweepBinProcs(dir); len(leaks) > 0 {
			t.Errorf("process leak: %d process(es) started from %s were still alive after the test's teardown (now killed):\n  %s",
				len(leaks), dir, strings.Join(leaks, "\n  "))
		}
	})
}

// binProcs lists the live processes (pid -> cmdline) executing a binary
// from dir.
func binProcs(dir string) map[int]string {
	out := map[int]string{}
	ents, err := os.ReadDir("/proc")
	if err != nil {
		return out
	}
	prefix := filepath.Clean(dir) + string(filepath.Separator)
	for _, e := range ents {
		pid, perr := strconv.Atoi(e.Name())
		if perr != nil || pid <= 1 {
			continue
		}
		exe, lerr := os.Readlink(filepath.Join("/proc", e.Name(), "exe"))
		if lerr != nil { // gone, a zombie, or not ours to read
			continue
		}
		if !strings.HasPrefix(strings.TrimSuffix(exe, " (deleted)"), prefix) {
			continue
		}
		raw, _ := os.ReadFile(filepath.Join("/proc", e.Name(), "cmdline"))
		out[pid] = strings.Join(strings.Fields(strings.ReplaceAll(string(raw), "\x00", " ")), " ")
	}
	return out
}

// sweepBinProcs SIGKILLs every live process executing from dir, waits
// (bounded) until none is left, and describes what it found.
func sweepBinProcs(dir string) []string {
	if dir == "" {
		return nil
	}
	found := binProcs(dir)
	if len(found) == 0 {
		return nil
	}
	var desc []string
	for pid, cmdline := range found {
		desc = append(desc, fmt.Sprintf("pid %d: %s", pid, cmdline))
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		live := binProcs(dir)
		if len(live) == 0 {
			break
		}
		for pid, cmdline := range live {
			if _, seen := found[pid]; !seen {
				found[pid] = cmdline
				desc = append(desc, fmt.Sprintf("pid %d: %s", pid, cmdline))
			}
			_ = syscall.Kill(pid, syscall.SIGKILL)
		}
		if time.Now().After(deadline) {
			desc = append(desc, fmt.Sprintf("(still alive after SIGKILL: %v)", live))
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	return desc
}

// binDirAtExit is the package-exit check (TestMain): no process started
// from this run's bin dir may outlive the run. It kills any survivor,
// removes the bin dir (kept when the instrumented-coverage round wrote
// its data there) and reports the survivors as an error.
func binDirAtExit() error {
	dir := v1BinDir
	if dir == "" {
		return nil
	}
	leaks := sweepBinProcs(dir)
	if os.Getenv("PFV1_COVER_BINS") == "" {
		_ = os.RemoveAll(dir)
	}
	if len(leaks) > 0 {
		return fmt.Errorf("process leak: %d process(es) started from %s outlived the test run (now killed):\n  %s",
			len(leaks), dir, strings.Join(leaks, "\n  "))
	}
	return nil
}
