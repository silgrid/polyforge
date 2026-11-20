package supervisor

// upgrade_restart_test.go (polyforge#175, 84-components-B): the
// restart-for-an-upgrade exit code's supervisor rules — a daemon exit
// with UpgradeRestartExit (75) is relaunched AT ONCE (no retrySleep, no
// failure count), a start that then fails still backs off (a missing
// current never becomes a zero-delay loop), and a stop landing while
// the upgrade-restart start is in flight wins (the daemon is not
// relaunched inside the stop window). The 0/78/stop rules beside them
// stay supervise_test.go's own.

import (
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// supExit75Child answers a daemon child that writes its pid to path,
// lives briefly (so the test can observe the run), then exits
// UpgradeRestartExit for its first runs — and once countFile has seen
// count stays up as the observable, killable shape: the test doubles
// of a daemon the upgrade orchestration (#92) restarts.
func supExit75Child(path, countFile string, count int) ChildSpec {
	script := "n=$(cat '" + countFile + "' 2>/dev/null || echo 0); echo $((n+1)) > '" + countFile +
		"'; echo $$ > '" + path + "'; if [ \"$n\" -lt " + strconv.Itoa(count) +
		" ]; then sleep 0.3; exit " + strconv.Itoa(UpgradeRestartExit) + "; fi\nexec sleep 300\n"
	return supSh("daemon", script)
}

// TestSuperviseUpgradeRestartRelaunchesWithoutBackoff: a daemon exit 75
// is relaunched immediately (well inside the configured restart delay)
// and counts as NO failure — the kill of the relaunched daemon is
// answered with the FIRST failure's base delay, not a doubled one; the
// supervisor process itself stays up through the 75 (the run never
// ends on it) and still answers a later stop with 0.
func TestSuperviseUpgradeRestartRelaunchesWithoutBackoff(t *testing.T) {
	dir := t.TempDir()
	pidF := filepath.Join(dir, "daemon.pid")
	countF := filepath.Join(dir, "count")
	rs := RunSpec{Daemon: supExit75Child(pidF, countF, 1),
		Grace: 3 * time.Second, RestartDelay: 400 * time.Millisecond}
	code := supRun(rs)

	// the first daemon run lives briefly and exits 75 (the
	// upgrade-restart request)
	first := 0
	supPoll(t, "the 75-exiting daemon's pid", func() bool {
		first = supReadPid(pidF)
		return pidAlive(first)
	})
	supPoll(t, "the 75-exiting daemon to be gone", func() bool {
		return !pidAlive(first)
	})
	// the 75 relaunch is immediate: the new pid exists well inside the
	// base restart delay (a failure's backoff would sit AT it or past
	// it — time.After never fires early)
	restarted := 0
	startedAt := time.Now()
	supPoll(t, "the upgrade-restarted daemon", func() bool {
		restarted = supReadPid(pidF)
		return restarted != 0 && restarted != first && pidAlive(restarted)
	})
	if waited := time.Since(startedAt); waited >= rs.RestartDelay {
		t.Fatalf("the 75 relaunch took %s — the upgrade restart must be immediate, not the %s backoff", waited, rs.RestartDelay)
	}
	// the supervisor process is still the same run: no exit code arrived
	select {
	case c := <-code:
		t.Fatalf("the run ended with %d on a daemon 75 exit — it must run on", c)
	case <-time.After(300 * time.Millisecond):
	}
	// the 75 counted as no failure: killing the relaunched daemon is
	// answered with the FIRST failure's base delay (had 75 counted, the
	// kill would be failure #2 and the delay would double past it)
	if err := syscall.Kill(restarted, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	killedAt := time.Now()
	third := 0
	supPoll(t, "the crash-restarted daemon", func() bool {
		third = supReadPid(pidF)
		return third != 0 && third != restarted && pidAlive(third)
	})
	if waited := time.Since(killedAt); waited < rs.RestartDelay {
		t.Fatalf("the crash restart came after %s, before the base backoff %s", waited, rs.RestartDelay)
	}
	if waited := time.Since(killedAt); waited >= 2*rs.RestartDelay {
		t.Fatalf("the crash restart came after %s — the 75 before it must not have counted as a failure (the doubled backoff is %s)", waited, 2*rs.RestartDelay)
	}
	// the run still answers a stop with 0
	if c := supStop(t, code); c != 0 {
		t.Fatalf("a stop after the upgrade restart: exit %d, want 0", c)
	}
}

// TestSuperviseUpgradeRestartThenMissingBinaryBacksOff: a 75 followed
// by a start that cannot run (the current pf missing) goes through the
// failure backoff — the next start attempt waits out the base delay, so
// a missing current never becomes a zero-delay restart loop.
func TestSuperviseUpgradeRestartThenMissingBinaryBacksOff(t *testing.T) {
	dir := t.TempDir()
	pidF := filepath.Join(dir, "daemon.pid")
	countF := filepath.Join(dir, "count")
	script := filepath.Join(dir, "daemon-sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nn=$(cat '"+countF+
		"' 2>/dev/null || echo 0); echo $((n+1)) > '"+countF+"'; echo $$ > '"+pidF+
		"'; if [ \"$n\" -lt 2 ]; then exit "+strconv.Itoa(UpgradeRestartExit)+
		"; fi\nexec sleep 300\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	// the start gate: the second start (the immediate 75 relaunch) is
	// held while the binary goes missing, the third is held until the
	// backoff is measured — the holds make the race (the script reading
	// on while the file is deleted) impossible.
	entered, released := make(chan int, 4), make(chan struct{}, 4)
	starts := 0
	oldHold := supPreStartHold
	supPreStartHold = func(*supProc) {
		starts++
		entered <- starts
		<-released
	}
	t.Cleanup(func() { supPreStartHold = oldHold })

	rs := RunSpec{Daemon: ChildSpec{Name: "daemon", Argv: []string{script}},
		Grace: 3 * time.Second, RestartDelay: 400 * time.Millisecond}
	code := supRun(rs)
	// start #1: release at once — the daemon runs and exits 75
	<-entered
	released <- struct{}{}
	supPoll(t, "the 75-exited daemon's pid", func() bool { return supReadPid(pidF) > 0 })
	// start #2 (the immediate relaunch) is held: the binary goes missing
	if n := <-entered; n != 2 {
		t.Fatalf("held start #%d, want the immediate 75 relaunch as start #2", n)
	}
	if err := os.Remove(script); err != nil {
		t.Fatal(err)
	}
	failReleasedAt := time.Now()
	released <- struct{}{}
	// the start failed (nothing to run): the backoff must elapse before
	// the next attempt — the third start enters the gate no sooner than
	// the base delay after the failure
	select {
	case <-entered:
	case <-time.After(supDeadline):
		t.Fatal("no third start attempt — the failed start was never retried")
	}
	if waited := time.Since(failReleasedAt); waited < rs.RestartDelay {
		t.Fatalf("the start after a failed one came after %s, before the %s backoff — a missing current would loop at zero delay", waited, rs.RestartDelay)
	}
	// the binary is back: the third start runs it and stays up
	if err := os.WriteFile(script, []byte("#!/bin/sh\nn=$(cat '"+countF+
		"' 2>/dev/null || echo 0); echo $((n+1)) > '"+countF+"'; echo $$ > '"+pidF+
		"'; exec sleep 300\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := supReadPid(pidF)
	released <- struct{}{}
	third := 0
	supPoll(t, "the daemon restarted after the backoff", func() bool {
		third = supReadPid(pidF)
		return third != 0 && third != before && pidAlive(third)
	})
	if c := supStop(t, code); c != 0 {
		t.Fatalf("a stop after the backoff: exit %d, want 0", c)
	}
}

// TestSuperviseUpgradeRestartDuringStopDoesNotRelaunch: a stop landing
// while the 75 relaunch is still inside its start window wins — the
// daemon is not started at all (stopping beats the upgrade restart),
// and the run answers the signal path's 0.
func TestSuperviseUpgradeRestartDuringStopDoesNotRelaunch(t *testing.T) {
	dir := t.TempDir()
	pidF := filepath.Join(dir, "daemon.pid")
	countF := filepath.Join(dir, "count")
	enter, release := make(chan *supProc, 2), make(chan struct{}, 2)
	oldHold := supPreStartHold
	supPreStartHold = func(p *supProc) { enter <- p; <-release }
	t.Cleanup(func() { supPreStartHold = oldHold })

	rs := RunSpec{Daemon: supExit75Child(pidF, countF, 1),
		Grace: 3 * time.Second, RestartDelay: 400 * time.Millisecond}
	code := supRun(rs)
	// start #1: release, the daemon exits 75
	<-enter
	release <- struct{}{}
	supPoll(t, "the 75-exited daemon's pid", func() bool { return supReadPid(pidF) > 0 })
	first := supReadPid(pidF)
	// start #2 (the 75 relaunch) is held: the stop lands inside its
	// window and must win — the daemon is not relaunched
	p2 := <-enter
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	supPoll(t, "the stop to be recorded on the relaunching child", func() bool {
		return p2.supStopping()
	})
	release <- struct{}{}
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("a stop inside the 75 relaunch window: exit %d, want 0", c)
		}
	case <-time.After(supDeadline):
		t.Fatal("the run did not end after the in-window stop")
	}
	if after := supReadPid(pidF); after != 0 && after != first {
		t.Fatalf("the daemon was relaunched inside the stop window: pid %d (was %d)", after, first)
	}
}
