package supervisor

// supervise.go (polyforge#160, 84-supervise): the container process
// supervisor — `pf supervise`, the one the workstation container's entry
// point (84-image) runs as PID 1's child. It starts and watches the
// daemon, Pi Web and (with --with-aihub) the aihub server as its own
// child processes, restarts a child that dies on it (a crash, a kill -9)
// with a growing delay, ends the whole run when the daemon exits 0 or
// NeedsOperatorExit (Q3: the container stops with that code; the reason
// is in daemon.log), and on SIGTERM/SIGINT — what `docker stop` sends,
// and catatonit forwards to its direct child only — forwards the signal
// ONCE to every child (a second daemon signal would hard-abort its
// shutdown and lose the checkpoint) and then waits out the stop grace
// for the daemon's shutdown before SIGKILL.
//
// The host-side counterpart is the tmux loop (ensure.go); this package
// holds both so `pf daemon ensure` and `pf doctor` can never disagree
// with either.

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// NeedsOperatorExit is the daemon's operator-only failure convention:
// a daemon exit with it is never restarted into the same failure. The
// daemon package aliases this constant for its own ExitNeedsOperator
// (it imports this package), so the convention is stated once, here.
const NeedsOperatorExit = 78

// UpgradeRestartExit is the restart-for-an-upgrade convention
// (polyforge#175, 84-components-B): the daemon exits with it once an
// upgrade switched the components volume's current — the supervisor
// relaunches it AT ONCE from the same current-resolved argv, with no
// failure backoff, and the run never ends on it. The trigger itself
// (the drain and the current switch) is #92's upgrade orchestration;
// the daemon package aliases this constant for its own
// ExitUpgradeRestart, so the convention is stated once, here.
const UpgradeRestartExit = 75

// MinStopGrace is the smallest stop grace `pf supervise` accepts (the
// daemon's shutdown can take up to 30s of WIP pushes plus agent-tree
// teardown, so a shorter grace would cut a checkpoint in half).
const MinStopGrace = 45 * time.Second

// The restart backoff: the first delay doubles per consecutive failure,
// capped, and a run that lasted backoffResetUptime or longer resets the
// count (a child that served for a while is not in a crash loop).
const (
	restartDelayCap     = 60 * time.Second
	backoffResetUptime  = 60 * time.Second
	defaultRestartDelay = 5 * time.Second
)

// ChildSpec is one supervised process.
type ChildSpec struct {
	// Name is the child's role ("daemon", "pi-web", "aihub") — the
	// diagnostics lines name it.
	Name string
	// Argv is the child's full argv, binary first.
	Argv []string
	// Dir is the child's working directory ("" = inherit).
	Dir string
	// Env is the child's whole environment.
	Env []string
	// Log is the child's stdout/stderr log, appended ("" = the
	// supervisor's own stdout/stderr).
	Log string
}

// RunSpec wires one supervisor run.
type RunSpec struct {
	// Daemon is the supervised daemon (required: its exit rules are the
	// run's).
	Daemon ChildSpec
	// Others are the remaining children (Pi Web, the aihub server).
	Others []ChildSpec
	// Grace is how long a stop (a forwarded signal, or the daemon's own
	// clean exit) waits for a child to end before SIGKILL (<= 0: the
	// documented minimum).
	Grace time.Duration
	// RestartDelay is the first restart delay after a child died on its
	// own (<= 0: 5s, the tmux loop's own delay); it doubles per
	// consecutive failure up to restartDelayCap and resets after a run
	// that lasted backoffResetUptime.
	RestartDelay time.Duration
}

// supExit reports one child's death: which process, with what status
// (its exit code, or -1 when a signal ended it).
type supExit struct {
	proc *supProc
	code int
}

// supProc is one supervised child: its spec, the running command and
// the stop state shared with its manager goroutine.
type supProc struct {
	spec ChildSpec
	// daemon names the child whose exit ends the run.
	daemon bool
	// baseDelay is its configured first restart delay.
	baseDelay time.Duration
	mu        sync.Mutex
	cmd       *exec.Cmd
	alive     bool
	// starting marks a start in flight (the loop's stop check passed,
	// the process does not exist yet): the run is not settled while one
	// is open, and the stop that lands inside the window is delivered
	// once the process exists.
	starting bool
	// termed records that this run's one forwarded stop signal was
	// delivered (the window's make-up and a later term() never both).
	termed bool
	// inflight marks a run that ended whose exit event is still on its
	// way to the main loop (allDead waits for it, so no manager is left
	// blocked on a channel nobody reads).
	inflight bool
	fails    int
	// stop marks "do not restart"; done is closed with it so a manager
	// sleeping its backoff wakes at once.
	stop bool
	done chan struct{}
}

// supState is the run's stop state (why it ends, and the exit code it
// answers with).
type supState struct {
	// active: the run is ending (a forwarded signal, or the daemon's own
	// final exit).
	active bool
	// viaSignal: a stop signal started it (the answer is 0 when the
	// daemon ended inside the grace, 1 when it took a SIGKILL).
	viaSignal bool
	// daemonCode is the daemon's own exit code when it ended the run.
	daemonCode int
	// killed: the grace ran out and the stragglers took a SIGKILL.
	killed bool
}

// RunSupervise starts the children and watches them until the run ends,
// answering the exit code the container should stop with: the daemon's
// own exit code when it ended the run (0 or NeedsOperatorExit), 0 when a
// forwarded stop let the daemon finish inside the grace, 1 when the
// daemon had to be SIGKILLed at the grace's end.
func RunSupervise(rs RunSpec) int {
	if rs.Grace <= 0 {
		rs.Grace = MinStopGrace
	}
	if rs.RestartDelay <= 0 {
		rs.RestartDelay = defaultRestartDelay
	}
	logf := func(format string, args ...any) {
		fmt.Fprintf(os.Stderr, "pf supervise: "+format+"\n", args...)
	}
	procs := []*supProc{newSupProc(rs.Daemon, true, rs.RestartDelay)}
	for _, s := range rs.Others {
		procs = append(procs, newSupProc(s, false, rs.RestartDelay))
	}
	exits := make(chan supExit)
	runDone := make(chan struct{})
	for _, p := range procs {
		go p.manage(exits, runDone)
	}
	sigs := make(chan os.Signal, 2)
	signal.Notify(sigs, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(sigs)

	var st supState
	var deadline <-chan time.Time
	stopAll := func() {
		for _, p := range procs {
			p.beginStop()
			p.term()
		}
		deadline = time.After(rs.Grace)
	}
	// A start that never ran (a missing binary, an unopenable log) sends
	// no exit event, so the end condition is the procs' own flags: no
	// child running and no exit event still on its way.
	allSettled := func() bool {
		for _, p := range procs {
			p.mu.Lock()
			a, inflight, starting := p.alive, p.inflight, p.starting
			p.mu.Unlock()
			if a || inflight || starting {
				return false
			}
		}
		return true
	}
	for {
		select {
		case sig := <-sigs:
			if st.active {
				logf("second stop signal %v ignored (each child was already signalled once)", sig)
				continue
			}
			logf("stop signal %v: forwarding once to every child, waiting out the %s grace", sig, rs.Grace)
			st.active, st.viaSignal = true, true
			stopAll()
		case e := <-exits:
			if e.proc.daemon && !st.active && (e.code == 0 || e.code == NeedsOperatorExit) {
				// the daemon ended the run itself (Q3): the container
				// stops with its code
				st.active, st.daemonCode = true, e.code
				logf("the daemon exited %d: stopping the other children", e.code)
				stopAll()
			}
		case <-deadline:
			st.killed = true
			deadline = nil
			logf("the stop grace ran out: SIGKILL to every child still running")
			for _, p := range procs {
				p.kill()
			}
		}
		if st.active && allSettled() {
			close(runDone)
			switch {
			case st.viaSignal:
				if st.killed {
					return 1
				}
				return 0
			default:
				return st.daemonCode
			}
		}
	}
}

// supPreStartHold, when non-nil, runs inside every child start, after
// the loop's stop check and before exec.Start — the test seam that pins
// a stop signal into the start window (polyforge#160 r1#1); it gets the
// proc whose start is in flight. nil in production.
var supPreStartHold func(p *supProc)

// newSupProc answers one managed child.
func newSupProc(spec ChildSpec, daemon bool, baseDelay time.Duration) *supProc {
	return &supProc{spec: spec, daemon: daemon, baseDelay: baseDelay, done: make(chan struct{})}
}

// manage runs the child's start/wait/backoff loop until the run stops it.
func (p *supProc) manage(exits chan<- supExit, runDone <-chan struct{}) {
	for {
		p.mu.Lock()
		if p.stop {
			p.mu.Unlock()
			return
		}
		p.starting = true
		p.mu.Unlock()
		code, ran := p.runOnce()
		if !ran {
			// nothing started (an unopenable log, a missing binary): the
			// start marker goes with it — ONE place clears it, every
			// failed start (the start-window skip clears it in runOnce
			// itself, its run "ended" with an event), or a stop would
			// leave the run waiting on a start that never comes
			// (r2#1). The backoff below retries; the main loop learns
			// nothing to wait for.
			p.mu.Lock()
			p.starting = false
			p.mu.Unlock()
			if p.retrySleep() {
				continue
			}
			return
		}
		p.mu.Lock()
		p.inflight = true
		p.mu.Unlock()
		select {
		case exits <- supExit{proc: p, code: code}:
		case <-runDone:
			return
		}
		p.mu.Lock()
		p.inflight = false
		p.mu.Unlock()
		if p.retrySleep() {
			continue
		}
		return
	}
}

// retrySleep waits the child's backoff and answers whether to run
// again; it answers false once the run is stopping (the child is never
// restarted then).
func (p *supProc) retrySleep() bool {
	p.mu.Lock()
	if p.stop {
		p.mu.Unlock()
		return false
	}
	p.fails++
	fails := p.fails
	p.mu.Unlock()
	delay := p.backoff(fails)
	fmt.Fprintf(os.Stderr, "pf supervise: child %s: restarting in %s (failure #%d)\n", p.spec.Name, delay, fails)
	select {
	case <-time.After(delay):
		p.mu.Lock()
		defer p.mu.Unlock()
		return !p.stop
	case <-p.done:
		return false
	}
}

// runOnce starts the child and waits for it, answering its exit code
// (ran=false when it never started).
func (p *supProc) runOnce() (code int, ran bool) {
	cmd := exec.Command(p.spec.Argv[0], p.spec.Argv[1:]...)
	cmd.Dir = p.spec.Dir
	cmd.Env = p.spec.Env
	if p.spec.Log != "" {
		f, err := os.OpenFile(p.spec.Log, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o644)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pf supervise: child %s: open log %s: %v\n", p.spec.Name, p.spec.Log, err)
			return 0, false
		}
		defer f.Close()
		cmd.Stdout, cmd.Stderr = f, f
	} else {
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	}
	if supPreStartHold != nil {
		supPreStartHold(p)
	}
	p.mu.Lock()
	if p.stop {
		// the stop landed inside this start's window: the child is not
		// started at all — a signal delivered to a process that has not
		// installed its handlers would kill it without its shutdown,
		// and nothing that never ran needs a TERM. The ended run wakes
		// the loop with its exit event (the run is stopping, so the
		// daemon-final branch never sees it).
		p.starting = false
		p.mu.Unlock()
		return 0, true
	}
	p.mu.Unlock()
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "pf supervise: child %s: start: %v\n", p.spec.Name, err)
		return 0, false
	}
	started := time.Now()
	p.mu.Lock()
	p.cmd, p.alive, p.starting, p.termed = cmd, true, false, false
	if p.stop {
		// the stop landed in the instant between the check above and
		// this start — its term() found no process yet, so the one
		// forwarded signal is delivered here (termed was just reset:
		// this IS its first delivery of this run)
		p.termed = true
		_ = cmd.Process.Signal(syscall.SIGTERM)
	}
	p.mu.Unlock()
	err := cmd.Wait()
	p.mu.Lock()
	p.cmd, p.alive = nil, false
	// a run that served long enough resets the child's failure count
	if time.Since(started) >= backoffResetUptime {
		p.fails = 0
	}
	p.mu.Unlock()
	code = -1
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode() // -1 when a signal ended it
	} else if err == nil {
		code = 0
	}
	return code, true
}

// beginStop marks the child "no restart" (idempotent) and wakes its
// backoff sleep.
func (p *supProc) beginStop() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stop {
		return
	}
	p.stop = true
	close(p.done)
}

// term forwards the stop signal once (no effect on a child that already
// ended or was never started).
func (p *supProc) term() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil && p.cmd.Process != nil && !p.termed {
		p.termed = true
		_ = p.cmd.Process.Signal(syscall.SIGTERM)
	}
}

// kill is the grace's end: SIGKILL to a child still running.
func (p *supProc) kill() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil && p.cmd.Process != nil {
		_ = p.cmd.Process.Kill()
	}
}

// backoff answers the nth consecutive failure's restart delay: first
// the base, then doubling, capped at restartDelayCap. A run that lasted
// backoffResetUptime reset the count in runOnce.
func (p *supProc) backoff(fails int) time.Duration {
	d := p.baseDelay
	if d <= 0 {
		d = defaultRestartDelay
	}
	for i := 1; i < fails; i++ {
		d *= 2
		if d >= restartDelayCap {
			return restartDelayCap
		}
	}
	return d
}
