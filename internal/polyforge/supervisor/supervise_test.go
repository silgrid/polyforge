package supervisor

// supervise_test.go (polyforge#160, AC1): the container supervisor's own
// rules, each driven through RunSupervise with real /bin/sh children —
// a killed child comes back, the daemon's 0/78 exit ends the run with
// that code, a stop signal is forwarded exactly once and the daemon
// finishes its shutdown inside the grace, and a stubborn daemon is
// SIGKILLed at the grace's end (exit 1).

import (
	"os"
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
	// two stop signals: the second must not reach the children
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
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
