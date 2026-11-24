package supervisor

// supervise_test.go (polyforge#160, AC1): the container supervisor's own
// rules, each driven through RunSupervise with real /bin/sh children —
// a killed child comes back, the daemon's 0/78 exit ends the run with
// that code, a stop signal is forwarded exactly once and the daemon
// finishes its shutdown inside the grace, and a stubborn daemon is
// SIGKILLed at the grace's end (exit 1).

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// supDeadline bounds one supervised observation in a test.
const supDeadline = 15 * time.Second

// supPoll waits for cond, failing the test at the deadline.
func supPoll(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(supDeadline)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// supSh answers a ChildSpec running one /bin/sh script.
func supSh(name, script string) ChildSpec {
	return ChildSpec{Name: name, Argv: []string{"/bin/sh", "-c", script}}
}

// pidAlive reports whether pid answers signal 0.
func pidAlive(pid int) bool {
	return pid > 0 && syscall.Kill(pid, 0) == nil
}

// supReadPid answers a pid file's pid (0 when absent or unparsable).
func supReadPid(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		return 0
	}
	return pid
}

// supPidChild answers a child that writes its pid to path and then
// sleeps — the observable, killable shape.
func supPidChild(name, path string) ChildSpec {
	return supSh(name, "echo $$ > '"+path+"'; exec sleep 300")
}

// supRun starts one RunSupervise and answers its exit-code channel.
func supRun(rs RunSpec) chan int {
	code := make(chan int, 1)
	go func() { code <- RunSupervise(rs) }()
	return code
}

// supStop sends this test process SIGTERM (what `docker stop` sends the
// supervisor) and answers the run's exit code.
func supStop(t *testing.T, code chan int) int {
	t.Helper()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-code:
		return c
	case <-time.After(supDeadline):
		t.Fatal("RunSupervise did not end after the stop signal")
		return -1
	}
}

// supTrapDaemon answers a daemon child that writes its pid to pidF and
// observes a stop signal. finish=true: it traps SIGTERM (one line per
// delivery appended to termF), completes a bounded shutdown (the done
// line) before exiting 0. finish=false: it IGNORES SIGTERM outright —
// the stubborn daemon only a SIGKILL at the grace's end can end.
func supTrapDaemon(pidF, termF, doneF string, finish bool) ChildSpec {
	if !finish {
		return supSh("daemon", "echo $$ > '"+pidF+"'; trap '' TERM; exec sleep 300")
	}
	script := "echo $$ > '" + pidF + "'; trap 'echo term >> " + termF +
		"; sleep 0.4; echo done >> " + doneF + "; kill $p 2>/dev/null; exit 0' TERM\n" +
		"sleep 300 &\np=$!\nwait $p\n"
	return supSh("daemon", script)
}

// supLineCount answers a file's word count (0 when unreadable).
func supLineCount(path string) int {
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	return len(strings.Fields(string(raw)))
}

// TestSuperviseRestartsAKilledChild: each child that dies on it — a
// kill -9, once for the daemon's role and once for a Pi-Web/AIHub-role
// other — is restarted (AC1), and the restart honours the restart
// delay: the killed pid comes back as a new pid no sooner than the
// configured delay. The run still answers a later stop with 0.
func TestSuperviseRestartsAKilledChild(t *testing.T) {
	dir := t.TempDir()
	pidF := filepath.Join(dir, "daemon.pid")
	otherF := filepath.Join(dir, "other.pid")
	rs := RunSpec{Daemon: supPidChild("daemon", pidF),
		Others: []ChildSpec{supPidChild("other", otherF)},
		Grace:  5 * time.Second, RestartDelay: 300 * time.Millisecond}
	code := supRun(rs)

	old := 0
	supPoll(t, "the daemon's pid", func() bool {
		old = supReadPid(pidF)
		return pidAlive(old)
	})
	if err := syscall.Kill(old, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	killedAt := time.Now()
	// the daemon died on its own (a crash): a NEW pid appears
	restarted := 0
	supPoll(t, "the restarted daemon", func() bool {
		restarted = supReadPid(pidF)
		return restarted != 0 && restarted != old && pidAlive(restarted)
	})
	if waited := time.Since(killedAt); waited < 300*time.Millisecond {
		t.Fatalf("the restart came after %s, faster than the configured delay", waited)
	}
	// the same watch restarts a killed other child too (Pi Web and the
	// aihub server are children of this same loop)
	other := 0
	supPoll(t, "the other child's pid", func() bool {
		other = supReadPid(otherF)
		return pidAlive(other)
	})
	if err := syscall.Kill(other, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	supPoll(t, "the restarted other child", func() bool {
		again := supReadPid(otherF)
		return again != 0 && again != other && pidAlive(again)
	})
	if c := supStop(t, code); c != 0 {
		t.Fatalf("stop after a restart: exit %d, want 0", c)
	}
}

// TestSuperviseDaemonExit0Or78EndsTheRun: the daemon exiting 0 or 78 is
// not restarted; the run ends with the same code and the other children
// are terminated (AC1, Q3 A).
func TestSuperviseDaemonExit0Or78EndsTheRun(t *testing.T) {
	for _, tc := range []struct {
		code int
		name string
	}{{0, "exit0"}, {78, "exit78"}} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			otherF := filepath.Join(dir, "other.pid")
			rs := RunSpec{
				Daemon: supSh("daemon", "sleep 0.3; exit "+strconv.Itoa(tc.code)),
				Others: []ChildSpec{supPidChild("other", otherF)},
				Grace:  5 * time.Second, RestartDelay: 100 * time.Millisecond,
			}
			code := supRun(rs)
			other := 0
			supPoll(t, "the other child's pid", func() bool {
				other = supReadPid(otherF)
				return pidAlive(other)
			})
			select {
			case c := <-code:
				if c != tc.code {
					t.Fatalf("daemon exit %d: RunSupervise exit %d, want %d", tc.code, c, tc.code)
				}
			case <-time.After(supDeadline):
				t.Fatalf("daemon exit %d: the run did not end", tc.code)
			}
			// the other child was terminated with the run
			supPoll(t, "the other child to die", func() bool { return !pidAlive(other) })
		})
	}
}

// TestSuperviseStopForwardsOnceAndWaitsWithinGrace: a stop signal is
// forwarded exactly once (a second daemon signal would hard-abort its
// shutdown), the daemon completes its shutdown work inside the grace
// (the done line), and the run exits 0 (AC1).
func TestSuperviseStopForwardsOnceAndWaitsWithinGrace(t *testing.T) {
	dir := t.TempDir()
	pidF := filepath.Join(dir, "daemon.pid")
	termF := filepath.Join(dir, "term.log")
	doneF := filepath.Join(dir, "done.log")
	rs := RunSpec{Daemon: supTrapDaemon(pidF, termF, doneF, true),
		Others: []ChildSpec{supPidChild("other", filepath.Join(dir, "other.pid"))},
		Grace:  5 * time.Second, RestartDelay: 100 * time.Millisecond}
	code := supRun(rs)
	supPoll(t, "the daemon child to start", func() bool {
		return pidAlive(supReadPid(pidF))
	})
	// two stop signals: the second must not reach the children. The
	// first is OBSERVED delivered (the daemon's term line) before the
	// second is sent — a fixed delay could let the two merge into one
	// delivery and the exactly-once assertion pass vacuously (r1#n2).
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	supPoll(t, "the first TERM to be delivered", func() bool {
		return supLineCount(termF) == 1
	})
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("a stop inside the grace: exit %d, want 0", c)
		}
	case <-time.After(supDeadline):
		t.Fatal("the run did not end after the stop signals")
	}
	// the daemon saw exactly one TERM and finished its shutdown
	if n := supLineCount(termF); n != 1 {
		t.Fatalf("the daemon saw %d TERM lines, want exactly 1", n)
	}
	if raw, err := os.ReadFile(doneF); err != nil || !strings.Contains(string(raw), "done") {
		t.Fatalf("the daemon did not complete its shutdown inside the grace (done file: %v)", err)
	}
}

// TestSuperviseStopKillsTheStubbornAtGrace: a daemon that ignores the
// forwarded TERM is SIGKILLed at the grace's end and the run exits 1
// (Q4 B: the grace is bounded, never an open-ended wait).
func TestSuperviseStopKillsTheStubbornAtGrace(t *testing.T) {
	dir := t.TempDir()
	pidF := filepath.Join(dir, "daemon.pid")
	rs := RunSpec{Daemon: supTrapDaemon(pidF, "", "", false),
		Grace: 700 * time.Millisecond, RestartDelay: 100 * time.Millisecond}
	code := supRun(rs)
	stubborn := 0
	supPoll(t, "the stubborn daemon to start", func() bool {
		stubborn = supReadPid(pidF)
		return pidAlive(stubborn)
	})
	started := time.Now()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-code:
		if c != 1 {
			t.Fatalf("a stop that had to SIGKILL: exit %d, want 1", c)
		}
	case <-time.After(supDeadline):
		t.Fatal("the run did not end at the grace")
	}
	if waited := time.Since(started); waited < 700*time.Millisecond {
		t.Fatalf("the SIGKILL came after %s, before the grace ran out", waited)
	}
	// the stubborn child is gone (the SIGKILL, not the TERM, ended it)
	supPoll(t, "the stubborn child to die", func() bool { return !pidAlive(stubborn) })
}

// supStopping answers whether the run has stopped this child (the test
// seam reader beside supPreStartHold).
func (p *supProc) supStopping() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stop
}

// TestSuperviseStopInTheStartWindowSkipsTheStart (r1#1): a stop signal
// landing inside a child's start window — after the loop's stop check,
// before the process exists — leaves NOTHING behind: the start counts
// as unsettled while it is in flight (the run waits it out), the child
// is not started at all (a TERM delivered to a process that has not
// installed its handlers would kill it without its shutdown), and the
// run answers 0 well inside the grace.
func TestSuperviseStopInTheStartWindowSkipsTheStart(t *testing.T) {
	dir := t.TempDir()
	pidF := filepath.Join(dir, "daemon.pid")
	termF := filepath.Join(dir, "term.log")
	enter, release := make(chan *supProc), make(chan struct{})
	oldHold := supPreStartHold
	supPreStartHold = func(p *supProc) { enter <- p; <-release }
	t.Cleanup(func() { supPreStartHold = oldHold })

	rs := RunSpec{Daemon: supTrapDaemon(pidF, termF, "", false),
		Grace: 3 * time.Second, RestartDelay: 100 * time.Millisecond}
	code := supRun(rs)
	p := <-enter
	// the stop signal lands while the start is still in flight
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	// the main loop has recorded the stop on the child before the start
	supPoll(t, "the stop to be recorded on the starting child", func() bool {
		return p.supStopping()
	})
	started := time.Now()
	close(release)
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("a stop in the start window: exit %d, want 0", c)
		}
	case <-time.After(supDeadline):
		t.Fatal("the run did not end after the in-window stop")
	}
	if waited := time.Since(started); waited > rs.Grace {
		t.Fatalf("the run waited %s, past its %s grace", waited, rs.Grace)
	}
	// the child was never started: no pid, no signal delivered to a
	// process that does not exist, nothing left running
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(pidF); err == nil {
			t.Fatal("the child started although the stop landed in its start window")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if n := supLineCount(termF); n != 0 {
		t.Fatalf("a child that never ran saw %d TERM lines, want 0", n)
	}
}

// TestSuperviseStopEndsEvenWhenAChildCannotStart (r2#1): a child whose
// log cannot be opened never leaves the run hanging — every failed
// start clears the start marker, so a stop ends the run (and the
// daemon's own 78 ends it with the needs-operator signal, AC1: the
// container must stop, not sit there past its grace).
func TestSuperviseStopEndsEvenWhenAChildCannotStart(t *testing.T) {
	dir := t.TempDir()
	// a regular file where a log path is needed: OpenFile fails, always
	blocker := filepath.Join(dir, "blocker")
	if err := os.WriteFile(blocker, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	badLog := ChildSpec{Name: "pi-web",
		Argv: []string{"/bin/sh", "-c", "exec sleep 300"},
		Log:  filepath.Join(blocker, "pi-web.log")}

	// a stop must end the run although the other child cannot start
	pidF := filepath.Join(dir, "daemon.pid")
	rs := RunSpec{Daemon: supPidChild("daemon", pidF), Others: []ChildSpec{badLog},
		Grace: 2 * time.Second, RestartDelay: 100 * time.Millisecond}
	code := supRun(rs)
	supPoll(t, "the daemon's pid", func() bool { return pidAlive(supReadPid(pidF)) })
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("a stop with a child that cannot start: exit %d, want 0", c)
		}
	case <-time.After(supDeadline):
		t.Fatal("the run hung after the stop: a child that cannot start must not hold it open")
	}

	// the daemon's own 78 must end the run the same way, carrying the
	// needs-operator signal out
	rs = RunSpec{Daemon: supSh("daemon", "sleep 0.2; exit 78"), Others: []ChildSpec{badLog},
		Grace: 2 * time.Second, RestartDelay: 100 * time.Millisecond}
	code = supRun(rs)
	select {
	case c := <-code:
		if c != 78 {
			t.Fatalf("the daemon's 78 with a child that cannot start: exit %d, want 78", c)
		}
	case <-time.After(supDeadline):
		t.Fatal("the run hung on the daemon's 78: a child that cannot start must not hold it open")
	}
}

// supTrapSh answers a daemon child that writes its pid to pidF, then
// loops a short foreground sleep so a forwarded TERM is caught between
// iterations (one term line per delivery, the done line, exit 0). A
// SIGKILL of the shell leaves no long-lived orphan behind, unlike
// supTrapDaemon's background sleep — the restart tests kill the child.
func supTrapSh(pidF, termF, doneF string) ChildSpec {
	script := "echo $$ > '" + pidF + "'; trap 'echo term >> " + termF +
		"; echo done >> " + doneF + "; exit 0' TERM\n" +
		"while :; do sleep 0.05; done\n"
	return supSh("daemon", script)
}

// TestSuperviseSettlesWhenTheLastExitEventRacesItsManager (polyforge#251,
// AC1/AC2): the manager is held right after it handed its exit event to
// the main loop — the scheduling race the flaky
// TestSuperviseRestartsAKilledChild loses — and the stop that follows
// must still settle at once: every child ended inside the grace, so the
// run answers 0 well before it.
func TestSuperviseSettlesWhenTheLastExitEventRacesItsManager(t *testing.T) {
	dir := t.TempDir()
	pidF := filepath.Join(dir, "daemon.pid")
	oldHold := supPostExitSendHold
	supPostExitSendHold = func(*supProc) { time.Sleep(300 * time.Millisecond) }
	t.Cleanup(func() { supPostExitSendHold = oldHold })

	rs := RunSpec{Daemon: supPidChild("daemon", pidF),
		Grace: 3 * time.Second, RestartDelay: 100 * time.Millisecond}
	code := supRun(rs)
	supPoll(t, "the daemon's pid", func() bool { return pidAlive(supReadPid(pidF)) })
	started := time.Now()
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case c := <-code:
		if c != 0 {
			t.Fatalf("stop after the last exit event: exit %d, want 0", c)
		}
	case <-time.After(supDeadline):
		t.Fatal("the run did not end after the stop")
	}
	if waited := time.Since(started); waited >= time.Second {
		t.Fatalf("the run waited %s to settle after every child had ended; the grace is %s", waited, rs.Grace)
	}
}

// TestSuperviseStopAfterARestart (polyforge#251, AC1's two timings,
// AC2): a stop right after a child was restarted forwards exactly one
// TERM to the restarted child and answers 0 inside the grace even with
// the manager delayed after the exit event's delivery; a stop landing
// while the restart is scheduled (the backoff not yet over) cancels the
// restart — no new process starts — and answers 0 well before the
// delay.
func TestSuperviseStopAfterARestart(t *testing.T) {
	t.Run("immediately", func(t *testing.T) {
		dir := t.TempDir()
		pidF := filepath.Join(dir, "daemon.pid")
		termF := filepath.Join(dir, "term.log")
		doneF := filepath.Join(dir, "done.log")
		oldHold := supPostExitSendHold
		supPostExitSendHold = func(*supProc) { time.Sleep(300 * time.Millisecond) }
		t.Cleanup(func() { supPostExitSendHold = oldHold })

		rs := RunSpec{Daemon: supTrapSh(pidF, termF, doneF),
			Grace: 5 * time.Second, RestartDelay: 300 * time.Millisecond}
		code := supRun(rs)
		first := 0
		supPoll(t, "the daemon's pid", func() bool {
			first = supReadPid(pidF)
			return pidAlive(first)
		})
		if err := syscall.Kill(first, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		restarted := 0
		supPoll(t, "the restarted daemon", func() bool {
			restarted = supReadPid(pidF)
			return restarted != 0 && restarted != first && pidAlive(restarted)
		})
		started := time.Now()
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		select {
		case c := <-code:
			if c != 0 {
				t.Fatalf("stop after a restart: exit %d, want 0", c)
			}
		case <-time.After(supDeadline):
			t.Fatal("the run did not end after the stop")
		}
		if waited := time.Since(started); waited >= 2*time.Second {
			t.Fatalf("the run waited %s after the stop; every child ended well inside the %s grace", waited, rs.Grace)
		}
		if n := supLineCount(termF); n != 1 {
			t.Fatalf("the restarted daemon saw %d TERM lines, want exactly 1", n)
		}
		if raw, err := os.ReadFile(doneF); err != nil || !strings.Contains(string(raw), "done") {
			t.Fatalf("the restarted daemon did not complete its shutdown inside the grace (done file: %v)", err)
		}
	})

	t.Run("scheduled", func(t *testing.T) {
		dir := t.TempDir()
		pidF := filepath.Join(dir, "daemon.pid")
		rs := RunSpec{Daemon: supPidChild("daemon", pidF),
			Grace: 2 * time.Second, RestartDelay: 2 * time.Second}
		code := supRun(rs)
		first := 0
		supPoll(t, "the daemon's pid", func() bool {
			first = supReadPid(pidF)
			return pidAlive(first)
		})
		if err := syscall.Kill(first, syscall.SIGKILL); err != nil {
			t.Fatal(err)
		}
		// the child is gone; the restart's backoff is running
		supPoll(t, "the killed daemon to be gone", func() bool { return !pidAlive(first) })
		started := time.Now()
		if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		select {
		case c := <-code:
			if c != 0 {
				t.Fatalf("stop during a scheduled restart: exit %d, want 0", c)
			}
		case <-time.After(supDeadline):
			t.Fatal("the run did not end after the stop")
		}
		if waited := time.Since(started); waited >= rs.RestartDelay {
			t.Fatalf("the run waited %s, past the %s restart delay it should have cancelled", waited, rs.RestartDelay)
		}
		if got := supReadPid(pidF); got != first {
			t.Fatalf("the daemon restarted after the stop: pid %d (was %d), want no relaunch", got, first)
		}
	})
}

// TestSupKillReportsOnlyARunningChild (polyforge#251, AC2): kill is
// the grace's end, so it answers whether it really SIGKILLed a running
// child — a child that already ended is not a straggler, and the run
// must not report the grace as having killed one.
func TestSupKillReportsOnlyARunningChild(t *testing.T) {
	cmd := exec.Command("/bin/sh", "-c", "exec sleep 300")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	p := &supProc{cmd: cmd}
	if !p.kill() {
		t.Fatalf("kill on a running child answered false, want true")
	}
	_ = cmd.Wait()
	if p.kill() {
		t.Fatalf("kill on a child that already exited answered true, want false")
	}
}
