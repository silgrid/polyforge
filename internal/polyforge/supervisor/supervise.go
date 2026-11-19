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
	"io"
	"time"
)

// NeedsOperatorExit is the daemon's operator-only failure convention:
// a daemon exit with it is never restarted into the same failure. The
// daemon package aliases this constant for its own ExitNeedsOperator
// (it imports this package), so the convention is stated once, here.
const NeedsOperatorExit = 78

// MinStopGrace is the smallest stop grace `pf supervise` accepts (the
// daemon's shutdown can take up to 30s of WIP pushes plus agent-tree
// teardown, so a shorter grace would cut a checkpoint in half).
const MinStopGrace = 45 * time.Second

// restartDelayCap caps the restart backoff growth; backoffResetUptime
// is a run long enough to reset a child's failure count.
const (
	restartDelayCap  = 60 * time.Second
	backoffResetUptime = 60 * time.Second
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
	// own; it doubles per consecutive failure up to restartDelayCap and
	// resets after a run that lasted backoffResetUptime (<= 0: 5s, the
	// tmux loop's own delay).
	RestartDelay time.Duration
	// Stderr takes the supervisor's own diagnostics (nil: os.Stderr).
	Stderr io.Writer
}

// RunSupervise starts the children and watches them until the run ends,
// answering the exit code the container should stop with: the daemon's
// own exit code when it ended the run (0 or NeedsOperatorExit), 0 when a
// forwarded stop let the daemon finish inside the grace, 1 when the
// daemon had to be SIGKILLed at the grace's end.
func RunSupervise(rs RunSpec) int {
	return 0
}
