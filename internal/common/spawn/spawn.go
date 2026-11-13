// Package spawn carries the process-derivation contract shared by the
// runtime (daemon assembly) and the driver (step-process spawning).
package spawn

import (
	"context"
	"time"
)

// Params describes one agent derivation.
type Params struct {
	WorkItemID     string
	CapsuleDir     string
	SessionHandle  string // PUBLIC fingerprint (B3), never the secret
	SpawnN         int
	TranscriptPath string
	Resume         bool
	InitialPrompt  string
	Model          string
	// Effort is the candidate's thinking effort (polyforge#176; "" = the
	// harness's own default — no flag). The spawner maps it onto its
	// harness's flag (harness.Candidate.EffortArgs).
	Effort string
	// Harness is the step harness this derivation runs on (polyforge#159;
	// "" = pi). The runtime spawner dispatches on it.
	Harness string
	// HarnessSession is the harness-native session id the daemon chose
	// for this derivation (claude: the --session-id / --resume uuid; ""
	// for pi, whose session rides TranscriptPath).
	HarnessSession string
	// Round is the step round this derivation belongs to (EnvRound): the
	// step's fresh entries count, so a resume, a crash re-derivation and
	// an owner's answer stay in the round and a rewind's re-entry opens
	// the next. 0 = not stated (no variable).
	Round int64
	// StepID is the workflow step this derivation runs (EnvStep; "" = not
	// stated, no variable). With SpawnN (EnvSpawn) it is the identity `pf
	// ask` records its question under.
	StepID string
	// LaneDir is the lane snapshot the derivation's step runs
	// (<CapsuleDir>/workflows/<lane>; EnvLaneDir, and EnvSharedDir its
	// shared tree beside it); "" = not stated, neither variable.
	LaneDir string
	// ToolWindow is the step's own supervision tool window when its lane
	// declares one (budget.tool_minutes); zero = none declared, the
	// daemon's global window applies. A declared window is also what the
	// claude adapter sizes the Bash tool's default timeout to (the max).
	ToolWindow time.Duration
	Journal    func(pid int, startedAt string) error
	// OnRecord, when set, receives every raw stdout record line the
	// process emits (the harness translator's input). It runs on the
	// stdout reader: it must be cheap and must never block.
	OnRecord func(line string)
	// OnExit, when set, is called exactly once when the process is gone
	// (after the stdout reader — every record through OnRecord — and the
	// stderr copy each caught up with what the process left in its pipe,
	// each within its own bound; a descendant merely holding a pipe open
	// does not delay it), BEFORE Done() turns true.
	OnExit func(ExitInfo)
}

// StderrIncompleteNote is the record of a stderr tail read before its
// copy caught up with the exit (ExitInfo.StderrIncomplete), as the
// runtime and the driver render it into a failure's text. It names no
// failure class: the classification never reads it as one.
const StderrIncompleteNote = "stderr tail incomplete: its copy had not caught up with the exit"

// StdoutIncompleteNote is the record of an exit announced before the
// stdout reader caught up with it (ExitInfo.StdoutIncomplete), as the
// driver renders it into a failure's text. It names no failure class:
// the classification never reads it as one.
const StdoutIncompleteNote = "stdout records incomplete: its reader had not caught up with the exit"

// ExitInfo describes how a derived process ended.
type ExitInfo struct {
	// Code is the exit status (-1 when the process was killed by a
	// signal).
	Code int
	// Signal names the terminating signal, "" when it exited normally.
	Signal string
	// StderrTail is the tail of the process's stderr (bounded), as it
	// stands when the ExitInfo is read.
	StderrTail string
	// StderrIncomplete: when the ExitInfo was read, the copy of the
	// process's stderr had not yet caught up with what the process left
	// in its pipe (its drain bound passed first), so StderrTail may lack
	// the process's last words.
	StderrIncomplete bool
	// StdoutIncomplete: when the ExitInfo was read, the stdout reader had
	// not yet caught up with what the process left in its pipe (its drain
	// bound passed first), so the process's last records may not have
	// reached OnRecord yet.
	StdoutIncomplete bool
}

// ExitReporter is the optional face of a Handle that can describe its
// process's exit once Done() is true.
type ExitReporter interface {
	ExitInfo() ExitInfo
}

// Handle is the supervised face over one derived agent process.
type Handle interface {
	PID() int
	StartedAt() string
	Alive() bool
	Done() bool
	Terminate(bound time.Duration) error
}

// Spawner derives agent processes.
type Spawner interface {
	Spawn(ctx context.Context, p Params) (Handle, error)
}

// EnvToolWindow is the environment variable through which the daemon
// threads its supervision tool window Y (milliseconds) into every step
// process, whatever the harness. The step scripts derive their deadline
// from it (and from BASH_MAX_TIMEOUT_MS when that is smaller,
// workflows/_shared/scripts/pfstep/deadline.py), so no step — pi
// included — can make one blocking call outlive the daemon's tool rule.
const EnvToolWindow = "PF_TOOL_WINDOW_MS"

// The step's place in the work item (workflow contract v2): the step
// round the dispatch belongs to (Params.Round) — a step reuses a script's
// result file only when its round is the current one.
const (
	EnvRound = "PF_ROUND"
	// EnvStep and EnvSpawn name the dispatch a step process is (the step
	// id and the spawn ordinal): the identity `pf ask` writes its question
	// under (capsule asks.jsonl), so the daemon knows which dispatch asked.
	EnvStep  = "PF_STEP"
	EnvSpawn = "PF_SPAWN"
)

// The places a step's scripts run from (the workflow contract's "Scripts"
// section): the capsule root (Params.CapsuleDir), the lane snapshot the
// step runs (Params.LaneDir: <capsule>/workflows/<lane>) and the shared
// tree beside it (<capsule>/workflows/_shared), and the Python interpreter
// the daemon resolved and checked at its start ([daemon] python, else
// python3 on its PATH). A skill runs a script as
// "$PF_PYTHON" "$PF_SHARED_DIR/scripts/<name>.py".
const (
	EnvCapsule   = "PF_CAPSULE"
	EnvLaneDir   = "PF_LANE_DIR"
	EnvSharedDir = "PF_SHARED_DIR"
	EnvPython    = "PF_PYTHON"
)
