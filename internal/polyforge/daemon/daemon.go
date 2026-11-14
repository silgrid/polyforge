// Package daemon is the phase-3 assembly of the v1 local runtime: the
// UDS HTTP face the pf CLI speaks (internal/common/protocol route
// family, pinned by internal/polyforge/cli/daemon.go), the Runtime
// (lifecycle) and Observe (supervision) modules wired over the five
// protocol messages, and the machine-level operations — the three
// admission modes, the pending-job dispatcher, the auto-claim loop and the
// shutdown.
//
// Current behavior: docs/design/polyforge-vnext/reference.md §6 (routes,
// lifecycle, shutdown, configuration); decisions: v2-converge.md §5–§6.
// The v1 records it was first built from (v1-architecture-spec, v1-spec-full,
// 04-local-runtime) are under docs/design/polyforge-vnext/archive/.
package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"crypto/ed25519"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/aihubclient"
	"github.com/silgrid/polyforge/internal/polyforge/cellcfg"
	"github.com/silgrid/polyforge/internal/polyforge/cli"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/liveconfig"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/modelhealth"
	"github.com/silgrid/polyforge/internal/polyforge/observe"
	"github.com/silgrid/polyforge/internal/polyforge/runtime"
	"github.com/silgrid/polyforge/internal/polyforge/summarize"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
	"github.com/silgrid/polyforge/internal/polyforge/task"
)

// Hub is the aihub face the daemon itself needs beyond Runtime's
// session-claim/end use: the ready-queue projection (batch recommendation,
// drain loop), the terminal-state check of the wrap/pause arbitration,
// and — through the generic Do — the memory faces the F6 lanes proxy
// through (search with an excerpt, memory create). Heartbeat is the M3
// machines liveness face the heartbeat loop POSTs on its own cadence.
// *aihubclient.Client satisfies it.
type Hub interface {
	runtime.Aihub
	Queue(project string) ([]api.WorkItem, error)
	// SessionVerify is the v1.5 M4 compare-only holder check — the
	// periodic per-RUNNING-job revalidation the observe monitor
	// piggybacks on its tick. *aihubclient.Client satisfies it.
	SessionVerify(wi, sessionID string) (*api.SessionVerifyResponse, error)
	// Heartbeat posts one machines/heartbeat upsert (M3 liveness face).
	Heartbeat(req aihubclient.MachineHeartbeat) (*aihubclient.MachineHeartbeatResult, error)
	// Do performs one typed request against aihub (the client's own
	// generic entry): the F6 memory adapters use it for the calls the
	// shared client carries no typed method for.
	Do(method, path string, body, out any) error
	// ProjectContactsWithCaller reads a project's human members with the
	// daemon key's owner kept (the member face's include_caller=true):
	// the task spawn route's one-time reporter resolution,
	// bounded by ctx so a slow hub cannot hold the spawn.
	ProjectContactsWithCaller(ctx context.Context, project string) ([]api.ProjectMemberContact, error)
}

// Config wires the daemon. Zero fields take the defaults in fill().
type Config struct {
	MachineID string
	// DisplayName is the display-only daemon name (default: hostname).
	DisplayName string
	// SocketPath is required (main.go resolves it like the CLI does).
	SocketPath string
	// ConfigPath is the cli.toml file this daemon loaded (the --config
	// flag, or the documented default): the reload entry point re-reads
	// exactly this file.
	ConfigPath string
	// StartupFile is the resolved config this daemon started from: the
	// baseline a reload compares restart-only keys against to decide
	// what is pending.
	StartupFile cli.Config
	// Mapping is the REQUIRED workspace→projects plan of record
	// (polyforge#113), assembled in main.go from the shared config's
	// [workspace."<path>"] sections. Capsules route per project through
	// the inverse mapping: <mapped-workspace>/capsules/<project>-<seq>. There is
	// no single workspace and no daemon project scope anymore — batch
	// and drain iterate ALL mapped projects. New validates the mapping
	// is non-empty and every mapped workspace exists with capsules/
	// creatable.
	Mapping       *mapping.Plan
	DataDir       string
	GitRemoteBase string
	WorkflowsDir  string // daemon checkout's workflows/
	PiBin         string
	// ClaudeBin is the Claude Code binary for steps whose harness is
	// claude (polyforge#159; --claude-bin / cli.toml claude_bin; default
	// "claude").
	ClaudeBin string
	// HarnessRoot is the parent root of the three harness state dirs
	// (polyforge#171: <harness-root>/step-agent, /step-agent-sessions,
	// /claude-config). "" = the default <data-dir>; the doctor's claude
	// entry reports the three dirs' existence and the claude-config
	// settings.json env keys (set/unset only) off it.
	HarnessRoot string
	// OperatorAgentDir is the SOURCE of the dedicated step agent
	// directory's minimal set (addendum 2, owner ruling A): the
	// operator's agent dir by default. Explicit (flag/env) sources whose
	// auth.json is missing refuse the daemon start.
	OperatorAgentDir         string
	OperatorAgentDirExplicit bool
	// Python is the absolute path of the interpreter a step's scripts run
	// under, every step's PF_PYTHON: Main resolves it once at the start
	// ([daemon] python, else python3 on the daemon's PATH) and refuses the
	// start when it is missing or older than cli.MinPython. "" (a test's
	// bare Config) = PF_PYTHON is set empty.
	Python string
	APIKey string // machine credential; NEVER reaches an agent env
	// Server is the Cell's AIHub base URL as configured (reported
	// normalized on GET /profiles; the Hub client carries the connection).
	Server string
	// Profile is the Cell's name ("" = default): the profile its work
	// references carry.
	Profile string
	// SharedAdmission is the Host's heavy-run admission queue every Cell's
	// daemon-run verification shares; nil = this daemon's
	// own.
	SharedAdmission *verifyQueue
	// SharedFeed is the DaemonHost's Host-global decision feed
	// (decisions.go; nil = this Daemon keeps its own under
	// its data dir — the unhosted test seam). Shared, every Cell records
	// into the one feed, each entry naming its Cell.
	SharedFeed *decisionFeed
	// LoadFile reads this Cell's file config for a reload (nil = parse
	// ConfigPath on its own): the Host binds the Host file for default and
	// the named file over the Host keys for a named Cell.
	LoadFile func() (cli.Config, error)
	// Hosted marks a Cell run by a DaemonHost (cells.go): the Host owns the
	// one socket and serves this Cell's routes through its own listener, so
	// Run opens no face of its own. Every production daemon is hosted; an
	// unhosted Daemon serving its own socket is the in-process test seam.
	Hosted bool
	// HostSlots is the Host's ceiling over every Cell (nil = this Cell's
	// own queue ceiling only: the unhosted test seam).
	HostSlots *hostSlots
	// PendingAPIKey is a verified rotation the Cell drains toward (restored
	// from its effective-connection record at a Host restart; "" = none).
	PendingAPIKey string
	// FileAPIKey is the Cell file's credential when the Cell started on its
	// persisted effective one and the two differ: it is a pending change
	// that goes through the identity check (adoptFileKey) before it is
	// used ("" = the file agrees with the effective credential).
	FileAPIKey string
	// ExecutionDisabled names why this Cell's execution plane is closed
	// ("" = open): no workspace configured, no model-credential source. A
	// closed Cell still serves its data plane; submits are refused naming
	// the reason and the auto-pull wave claims nothing.
	ExecutionDisabled string
	// CellMaxConcurrentJobs is the Cell's persisted optional ceiling (0 =
	// none; reported on GET /profiles).
	CellMaxConcurrentJobs int
	MaxConcurrent         int // cross-mode concurrency cap (default 4)
	// FlagPinned names the [daemon] keys an explicit command-line flag
	// pinned at startup (max_concurrent_jobs, step_timeout, stall_window,
	// claude_bin): a reload reports those overridden and never applies
	// them.
	FlagPinned map[string]bool
	// EnvWarnings are the boot warnings for retired POLYFORGE_* variables
	// still set to a differing value (main.go retiredEnvWarnings): each
	// lands one machine-level retired_env_ignored event and rides
	// GET /status for pf doctor.
	EnvWarnings []string
	// RecallTopK is the F6-A claim-time memory snapshot's hit count
	// (0 = the runtime default, 10; bounded at 50 there).
	RecallTopK int
	Auto       *bool // initial auto switch; nil = not specified (persisted state wins; default off)

	// HeartbeatEvery is the machines liveness cadence (M3): how often the
	// daemon POSTs /api/v1/machines/heartbeat. <= 0 disables the
	// heartbeat loop entirely; `pf daemon` wires the flag -heartbeat-every
	// whose default (60s) is the production cadence the aihub side's
	// 5×-default stale window assumes.
	HeartbeatEvery time.Duration

	// Launcher is the invoking environment kind (claude|pi-web|non-interactive)
	// reported to aihub; an interactive launcher asks for the interactive
	// role. Empty = non-interactive (runner).
	Launcher string

	// Test/ops knobs.
	Spawner runtime.Spawner // nil = the production pi spawner
	// SummarizeDispatcher replaces the summarize lane's dispatch seam
	// (polyforge#224; nil = the production exec dispatcher through the
	// configured harness binaries). Test seam: the in-process daemon
	// tests bind a fake so they can observe the lane's calls without a
	// real harness dispatch.
	SummarizeDispatcher summarize.Dispatcher

	// TaskRunner replaces the task lane's exec runner (tests); nil = the
	// real harness binaries run in the project's workspace directory.
	TaskRunner task.Runner
	// MachineFace replaces the signed cross-machine client (nil = the
	// production *aihubclient.Client when Hub is one). Test seam: the
	// daemon's query/message loops are driven against a fake without a
	// real aihub.
	MachineFace MachineFace
	// AgentStarter replaces the execution-engine starter the runtime
	// uses (nil = the production Go driver, driver.Start). Test seam:
	// the in-process daemon tests bind a fake engine so they can script
	// agent behavior without a real driver. There is no selection and
	// no fallback — the driver IS the engine; this only swaps which
	// starter binds it.
	AgentStarter AgentStartFunc
	Hub          Hub              // required; aihubclient.Client satisfies
	PushFunc     observe.PushFunc // nil = the shared gitops choke point
	ObservePoll  time.Duration    // observe execution.json poll tick
	// Supervision carries the supervisor's global bounds into the
	// assembly (X silence, Y tool, T step timeout, N driver stall, the
	// model-error grace; zero fields keep the documented defaults) and
	// the M4 revalidation cadence. Step overrides live in workflow.yaml.
	Supervision   SupervisionConfig
	DrainInterval time.Duration // drain wave-fetch period (5s)
	DispatchTick  time.Duration // dispatcher/backfill period (200ms)
}

// Agent is what the daemon's engine starters return: the runtime's agent
// handle plus the exit signal the observe monitor selects on.
type Agent interface {
	runtime.AgentHandle
	observe.Handle
}

// AgentStartFunc starts one agent derivation (driver.Start in production).
type AgentStartFunc func(ctx context.Context, in runtime.AgentStartInput) (Agent, error)

// SupervisionConfig is the supervisor's global-default surface
// (v2-converge 2.4): daemon flags map onto it one to one.
type SupervisionConfig struct {
	// SilenceWindow is X: no work-class signal after dispatch (rule 0)
	// and no model output while waiting for the model (rule 2). Zero =
	// by the harness's output granularity (10m token-level, 30m
	// message-level).
	SilenceWindow time.Duration
	// ToolWindow is Y: one tool running longer than this is hung
	// (default 30m).
	ToolWindow time.Duration
	// StepTimeout is T: cumulative step runtime (default 90m; a step's
	// own budget.wall_minutes overrides it).
	StepTimeout time.Duration
	// StallWindow is N: the driver between steps (not waiting on a human)
	// for longer than this is stalled (default 10m; negative disables).
	StallWindow time.Duration
	// ModelErrorGrace is the rule-1 grace after an error-ended turn
	// (default 1m).
	ModelErrorGrace time.Duration
	// AskWarmWindow is how long a step process that asked the owner (`pf
	// ask`) stays resident for the reply before the daemon ends it
	// (default 15m; [daemon] ask_warm_window / --ask-warm-window).
	AskWarmWindow time.Duration
	// SessionRevalidateEvery is the M4 periodic session-revalidation
	// cadence of the held records — observe's running jobs and the
	// runtime's retained paused records (0 = the default 30s; negative =
	// disabled).
	SessionRevalidateEvery time.Duration
}

func (c SupervisionConfig) limits() supervise.Defaults {
	return supervise.Defaults{
		Silence: c.SilenceWindow, Tool: c.ToolWindow,
		StepTimeout: c.StepTimeout, ModelErrorGrace: c.ModelErrorGrace,
		AskWarmWindow: c.AskWarmWindow,
	}
}

func (c *Config) fill() {
	// The Go execution driver is THE engine (polyforge#106): the legacy
	// WI-Agent selection surface is deleted — there is nothing to
	// default and nothing to flip.
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = cli.DefaultMaxConcurrentJobs
	}
	if c.ObservePoll == 0 {
		c.ObservePoll = 500 * time.Millisecond
	}
	if c.DrainInterval == 0 {
		c.DrainInterval = 5 * time.Second
	}
	if c.DispatchTick == 0 {
		c.DispatchTick = 200 * time.Millisecond
	}
}

// Daemon is the assembled local runtime.
type Daemon struct {
	// claudeVer caches the claude --version probe of GET /status.
	claudeVer versionCache
	// feed is the decision feed (decisions.go): the Host's shared one.
	feed  *decisionFeed
	cfg   Config
	hub   Hub
	modes *ModeCtl
	q     *jobQueue

	rt  *runtime.Runtime
	obs *observe.Observe
	// modelHealth is the durable model-health tracker: the
	// read-side consumer of the events stream, the writer of
	// <DataDir>/model-health.json and the source of GET /models + the
	// additive GET /status summary. It never touches dispatch.
	modelHealth *modelhealth.Tracker

	// remoteSnaps caches relayed remote snapshots for remoteSnapTTL so a
	// polling watcher cannot exhaust the holder's relay budget
	// (A3; progresslookup.go). Memory-only.
	remoteSnaps *remoteSnapCache

	// lane is the demand-gated summarize lane (polyforge#224): per-WI
	// memory-only state, the gates, the background dispatch — wired into
	// the watch route (the demand=1 marker on GET /jobs/{wi}/watch). The
	// exec dispatcher is bound in production; Config.SummarizeDispatcher
	// is the test seam.
	lane *summarize.Lane

	// tasks is the one-shot task manager (tasks.go).
	tasks *task.Manager
	// locateTranscript resolves the current dispatch's live transcript
	// through the shared per-dispatch rule (runtime.LiveTranscript — the
	// same function the commit-boundary sync sources from, not a
	// duplicate): a resumed pi dispatch's live transcript is the capsule
	// file itself (the validated destination), every other dispatch's
	// resolves through LocateTranscript (the session store / the claude
	// native session). The summarize route wiring calls it per
	// demand-marked request.
	locateTranscript func(capsuleDir string, entries []capsule.SpawnJournalEntry) string

	runCtx    context.Context // the loops' context; the shutdown cancels it
	cancelRun context.CancelFunc

	shutOnce sync.Once
	shutDone chan struct{} // closed when the shutdown finished
	// obsDone is closed when the observe lane's Run returned (its monitors
	// stopped, its event log closed); obsStarted says Run was started. The
	// shutdown waits for it (bounded): the lane's last appends — a release's
	// job_finished event and the decision feed's write from it — never land
	// after the shutdown returned.
	obsDone    chan struct{}
	obsStarted atomic.Bool

	// queueFetchDenied is the denial episode flag: set when the first
	// queue_fetch_denied event of a healthy→denied transition has been
	// emitted, cleared on the next successful wave fetch. Only the
	// auto-pull ticker goroutine touches it (fetchAutoWave), so it
	// carries no lock.
	queueFetchDenied bool
	// yamlRefused is project → the last workspace-yaml refusal logged for
	// it: auto-pull skips a project whose .polyforge.yaml is invalid
	// (nothing is claimed, the WIs stay queued for other machines) and
	// logs a given refusal once, not every tick. Same single-goroutine
	// ownership as queueFetchDenied.
	yamlRefused map[string]string
	// bundleRefused is `<project> <lane>` → the last lane refusal logged
	// for it (polyforge#175): auto-pull skips ready work items on a lane
	// of their project's lane set that fails to load, or one the set does
	// not carry, instead of enqueueing a claim bound to refuse every wave;
	// logged once per distinct refusal. Same single-goroutine ownership as
	// yamlRefused.
	bundleRefused map[string]string
	// userLaneRefused is a refused user lane's directory → the defects
	// last recorded for it (reportUserLanes): one user_lane_refused event
	// per lane and distinct defect set. Guarded by userLaneMu (the start
	// and a config reload both judge).
	userLaneMu      sync.Mutex
	userLaneRefused map[string]string

	// sup hands out the per-WI supervision links shared by the driver
	// (facts out, verdicts in, harness trackers) and the observe
	// monitors.
	sup *supervise.Registry

	// merges is the per-repo merge lock (mergelock.go): the merge-pr
	// step's check-then-merge and a lane's revert step.
	merges *mergeLocks
	// verifyQ is the heavy-run admission of daemon-run verification bars
	// (verifyqueue.go): the Host's shared queue, or this
	// daemon's own; re-evaluated on the dispatch tick.
	verifyQ *verifyQueue
	// commandPath is the live [daemon] command_path ("" = the daemon
	// process's own PATH). Guarded by reloadMu.
	commandPath string
	// pfDir is the directory of this daemon's own pf executable (selfDir):
	// first on a step's PATH when command_path is set. Set once in New.
	pfDir string

	lst net.Listener
	srv *http.Server
	// hb is the Cell's last heartbeat outcome (GET /profiles).
	hb heartbeatState
	// execDisabled is the live execution-disabled reason (Config's start
	// value; a reload that adds the first mapping clears the no-workspace
	// reason). Guarded by reloadMu.
	execDisabled string
	// effectiveKey is the credential the Cell runs on; pendingKey a
	// verified same-identity rotation waiting for safe idle (drain.go).
	// Guarded by reloadMu.
	effectiveKey, pendingKey string
	// connNotice reports a file credential the identity check did not
	// adopt (unverified, or another Hub user): the Cell keeps serving on
	// its effective credential. Guarded by reloadMu.
	connNotice string
	// ready is closed once Run reached the point where an unhosted daemon
	// starts serving (lock held, restart reconciled): the Host serves a
	// hosted Cell's routes only after it.
	ready chan struct{}

	// faceMu guards lst, srv, dataLock and the faceClosed latch: a
	// Shutdown can run concurrently with Run's startup, and the latch lets
	// whichever side loses the race tear the listener down instead of
	// serving on a face the shutdown already closed.
	faceMu     sync.Mutex
	faceClosed bool

	// dataLock is the M5 flock on the daemon data dir (held for the
	// process's lifetime, released by the kernel at exit): the
	// same-machine dual-daemon TOCTOU guard ALONGSIDE the socket probe.
	// The probe alone races (both processes can observe a dead socket
	// and both bind); the flock is the kernel-serialized half.
	dataLock *os.File

	// stepOriginOf resolves a /step/* caller to its work item (nil = the
	// UDS peer's process tree, peerStepOrigin; tests bind a stub).
	stepOriginOf func(*http.Request) (stepJob, string)
	// stepHubAllow answers the `pf step hub` verbs a step's dispatch may
	// call and stepHubAttrs the attrs keys it may write (stephub.go):
	// production binds laneHubAllow — the step's `hub.verbs` (and
	// wrap-attrs for its `wrap_attrs`, attrs for its `hub.attrs`) — and
	// laneHubAttrs — its `hub.attrs` — in the capsule's lane snapshot;
	// tests bind fixtures. nil = none.
	stepHubAllow func(stepJob) []string
	stepHubAttrs func(stepJob) []string

	// live holds the reloadable settings (mapping, claude_bin, supervision
	// bounds) behind a mutex; the daemon and the runtime read them at their
	// use sites, so a reload takes effect without a restart.
	live *liveconfig.State
	// reloadMu guards the reload BOOKKEEPING (pending/overridden/status),
	// which GET /status reads. It is only ever held for an in-memory
	// publish — never across file I/O (AGENTS.md runtime locking).
	reloadMu      sync.Mutex
	reloadStatus  cli.ReloadStatus
	maxConfigured int
	// reloadApplyMu serializes reloads, guards lastAppliedFile and
	// serializes the drain switch with them (the dispatch tick only
	// TryLocks it). No reader takes it, so a reload's disk I/O never
	// blocks GET /status.
	reloadApplyMu sync.Mutex
	// drainGate pairs every admission of new work — a submit's pending
	// check + enqueue, the auto wave's check + enqueue, a task's check +
	// slot reservation (admitTask) — with the drain switch's pending +
	// safe-idle decision (drain.go), so either the admission is refused
	// or the switch sees it and waits. Held only across in-memory
	// bookkeeping, never across file I/O or a Hub/AIHub round trip: a
	// submit or auto-wave entry takes an admitting reservation under it
	// and persists its queue entry after releasing it; it is taken before
	// reloadMu, the queue lock and the Host slot lock, never after them.
	drainGate sync.Mutex
	// admitting counts admissions that passed their pending check under
	// drainGate and are persisting their queue entry outside it; an open
	// reservation keeps the Cell from being safe-idle until the entry is
	// on the queue (or failed to land).
	admitting atomic.Int32
	// claiming counts the dispatch's claim goroutines still running
	// (startEntry); the shutdown does not wait for them.
	claiming atomic.Int32
	// submitWaitMu guards submitWaits only (no I/O under it): the
	// submit answers waiting on their entry's dispatch outcome,
	// keyed by canonical work-item id.
	submitWaitMu sync.Mutex
	submitWaits  map[string]*submitWait
	// tasksHeld counts this Cell's admitted one-off tasks not yet done
	// (admitTask..taskDone): they keep the Cell from being safe-idle.
	tasksHeld atomic.Int32
	// lastAppliedFile is the file-only config as of the last successful
	// reload (initially the startup file baseline). It is the comparison
	// point for "a reload that CHANGES max_concurrent_jobs", so an
	// unrelated reload never undoes `pf ops max`.
	lastAppliedFile cli.Config

	// Cross-machine identity and face: the Ed25519 key is
	// loaded once by Run, the face state is guarded by faceMu.
	machineKey   ed25519.PrivateKey
	publicKeyB64 string
	mfaceMu      sync.Mutex
	// roleMu serializes the operator's role moves (requestDesiredRole), so
	// the persisted machine-role.json and the face's desired role never
	// disagree: the file is written while it is held, never under mfaceMu.
	roleMu sync.Mutex
	face   machineFaceState
}

// New validates and assembles the daemon. Restart reconciliation is NOT
// run here — Run does it once its goroutines are live.
func New(cfg Config) (*Daemon, error) {
	if cfg.Hub == nil {
		return nil, errors.New("daemon: Config.Hub is required")
	}
	if cfg.DataDir == "" {
		return nil, errors.New("daemon: DataDir is required")
	}
	// The installed lanes are the release's (workflow contract J25): one
	// the one lane loader refuses is a release defect, and the daemon
	// refuses to start rather than claim work it cannot run.
	if err := checkInstalledLanes(cfg.WorkflowsDir); err != nil {
		return nil, err
	}
	// The mapping and workspace checks are the SHARED validator `pf config`
	// and `pf doctor` use: one implementation of the
	// workspace contract, no local copy. The daemon never creates a
	// workspace directory itself — the operator (or installer) selects
	// pre-existing ones — and the only subtree it ever writes under any of
	// them is capsules/.
	if cfg.Mapping == nil {
		cfg.Mapping = &mapping.Plan{}
	}
	if cfg.Mapping.Empty() {
		// A Cell with a complete connection and no workspace loads: its data
		// plane serves, its execution plane stays closed until a mapping
		// exists (the reason rides GET /status and GET /profiles).
		if cfg.ExecutionDisabled == "" {
			cfg.ExecutionDisabled = execDisabledNoWorkspace
		}
	} else if err := cli.ValidateMappingForDaemon(cli.Config{Mapping: cfg.Mapping}); err != nil {
		return nil, fmt.Errorf("daemon: %w", err)
	}
	// Relative paths are a footgun measured live on the window rig: the
	// spawned git commands run with cmd.Dir = the capsule, so a relative
	// DataDir resolves GIT_DIR under the capsule ('not a git repository').
	// Absolutize the root before anything derives paths from it.
	if abs, aerr := filepath.Abs(cfg.DataDir); aerr == nil {
		cfg.DataDir = abs
	}
	cfg.fill()
	live := liveconfig.New(cfg.Mapping, cfg.ClaudeBin, cfg.Supervision.StepTimeout, cfg.Supervision.StallWindow)
	// The [summarize] table and the memory_policy family come from the
	// file-only startup baseline; a reload swaps them.
	live.SetSummarize(cfg.StartupFile.Summarize)
	live.SetAgentLanguage(cfg.StartupFile.Daemon.AgentLanguage)
	live.SetMemoryPolicy(cfg.StartupFile.MemoryPolicy)

	// liveLimits answers the current supervision defaults (a live reload's
	// step_timeout / stall_window folded in). A zero duration means unset
	// and resolves to the documented default through supervise.Fill; a
	// negative stall window still disables the stall check at the consumer.
	liveLimits := func() supervise.Defaults {
		return supervise.Defaults{
			Silence:         cfg.Supervision.SilenceWindow,
			Tool:            cfg.Supervision.ToolWindow,
			StepTimeout:     live.StepTimeout(),
			DriverStall:     live.StallWindow(),
			ModelErrorGrace: cfg.Supervision.ModelErrorGrace,
			AskWarmWindow:   cfg.Supervision.AskWarmWindow,
		}
	}

	// addendum 2 (owner ruling A): the dedicated step agent directory is
	// synced at daemon start — the only point that also prunes entries
	// outside the minimal set; the spawner refreshes the allowlisted
	// members before every spawn. A source that is explicitly configured but cannot
	// present its credentials refuses startup; the implicit default
	// without file credentials carries on with a warning.
	rtCfg := runtime.Config{
		MachineID:                cfg.MachineID,
		DaemonSocket:             cfg.SocketPath,
		Mapping:                  cfg.Mapping,
		Live:                     live,
		DataDir:                  cfg.DataDir,
		GitRemoteBase:            cfg.GitRemoteBase,
		WorkflowsDir:             cfg.WorkflowsDir,
		PiBin:                    cfg.PiBin,
		ClaudeBin:                cfg.ClaudeBin,
		ToolWindow:               cfg.Supervision.ToolWindow,
		HarnessRoot:              cfg.HarnessRoot,
		OperatorAgentDir:         cfg.OperatorAgentDir,
		OperatorAgentDirExplicit: cfg.OperatorAgentDirExplicit,
		EnvSecrets:               []string{cfg.APIKey},
	}
	rtCfg.FillDefaults()
	if err := rtCfg.SyncStepAgentDir(); err != nil {
		return nil, err
	}
	// The step processes' session store is the locator's root too: the
	// filled runtime value (the dedicated step-agent-sessions dir, or the
	// operator's explicit override) governs both the child env and the
	// live-transcript scan below.
	cfg.HarnessRoot = rtCfg.HarnessRoot
	// Runtime-sourced diagnostic events (a seal push that failed after
	// its retries, pilot item 3) land in the observe lane's durable
	// events log next to its own entries; the tap reads d.obs at call
	// time, so it is bound below, once the Daemon value is live and
	// before runtime.New consumes the cfg.
	// The production spawner takes the runtime config BY POINTER
	// (runtime.DefaultSpawner): fields bound after assembly (the
	// RecordTap binding below included) are read at spawn time from the
	// shared config.
	sp := cfg.Spawner
	if sp == nil {
		sp = runtime.DefaultSpawner(&rtCfg)
	}
	// v2 (polyforge#106): the Go driver IS the execution engine — bound
	// unconditionally, no selection flag, no fallback. The AgentStarter
	// config knob only swaps WHICH starter binds (tests).
	var d *Daemon // bound below; the starter closure reads it at call time
	start := cfg.AgentStarter
	if start == nil {
		start = func(ctx context.Context, in runtime.AgentStartInput) (Agent, error) {
			return driver.Start(ctx, driver.StartConfig{
				WI:            in.WI,
				CapsuleDir:    in.CapsuleDir,
				BundleName:    in.BundleName,
				SessionHandle: in.SessionHandle,
				SpawnN:        in.SpawnN,
				Spawner:       in.Spawner,
				Compose:       in.Compose,
				Interrupted:   interruptedFromCause(in.InterruptedCause),
				StepEnded:     in.StepEnded,
				// polyforge#171: the resume decision for claude dispatches
				// locates the native session under the claude-config dir
				// the spawner hard-set (never the daemon's process env).
				ClaudeConfigDir: rtCfg.ClaudeConfigDir(),
				// driver events land in the durable per-WI event log
				// (monotonic ids); the supervision link is the WI's
				// shared driver <-> supervisor channel
				Log:    d.driverLog(),
				Link:   d.sup.Link(in.WI),
				Limits: cfg.Supervision.limits(),
				// a live reload of step_timeout/stall_window takes effect at
				// the next threshold judgement, including in-flight steps
				//
				LimitsFunc: func() supervise.Defaults { return liveLimits() },
				// the heavy-run admission a step declaring it is admitted by
				Admission: cellAdmission{d},
				// workflow contract 4: the pending `pf step hub` writes a
				// step round makes when it hands off done
				HubWrites: stepHubSink{d},
				// the forge binding: git and gh as the daemon, and the
				// per-repo merge lock (mergelock.go)
				Forge: cellForge{d},
				// a step waiting on the owner (`pf ask`) holds no running
				// slot (occupiedSlots reads the runtime's flag)
				OwnerWait: in.OwnerWait,
				// the agent language a new conversation takes: the live
				// `[daemon] agent_language` at dispatch time
				Language: live.AgentLanguage,
			})
		}
	}
	rtCfg.AgentStarter = func(ctx context.Context, in runtime.AgentStartInput) (runtime.AgentHandle, error) {
		a, err := start(ctx, in)
		if err != nil {
			return nil, err // never a typed-nil handle
		}
		return a, nil
	}

	feed, err := feedOf(cfg)
	if err != nil {
		return nil, fmt.Errorf("daemon: %w", err)
	}
	// polyforge#224: the summarize lane's dispatch seam — the exec
	// dispatcher through the configured harness binaries in production
	// (the same binary source the step spawn uses: pi_bin is restart-only,
	// claude_bin is read from the live state at every dispatch); the
	// Config knob swaps it (tests).
	laneDispatcher := cfg.SummarizeDispatcher
	if laneDispatcher == nil {
		laneDispatcher = &summarize.ExecDispatcher{
			PiBin:           cfg.PiBin,
			ClaudeBin:       live.ClaudeBin,
			ClaudeConfigDir: rtCfg.ClaudeConfigDir(),
			Dir:             rtCfg.SummarizeCwdDir(),
		}
	}
	d = &Daemon{
		feed:     feed,
		cfg:      cfg,
		hub:      cfg.Hub,
		live:     live,
		modes:    NewModeCtl(),
		q:        NewJobQueue(cfg.MaxConcurrent, func() map[string]bool { return d.occupiedSlots() }),
		shutDone: make(chan struct{}),
		obsDone:  make(chan struct{}),
		ready:    make(chan struct{}),
		sup:      supervise.NewRegistry(),
		merges:   newMergeLocks(),
		verifyQ:  cfg.SharedAdmission,
		// the Cell's own command_path (a reload moves it)
		commandPath: cfg.StartupFile.Daemon.CommandPath,
		pfDir:       selfDir(),
		lane:        summarize.New(laneDispatcher),
		remoteSnaps: newRemoteSnapCache(remoteSnapTTL, remoteSnapCap),
		face:        initialFace(cfg),
		// the file-only baseline the reload compares against until the
		// first successful reload moves it
		lastAppliedFile: cfg.StartupFile,
		maxConfigured:   cfg.MaxConcurrent,
		execDisabled:    cfg.ExecutionDisabled,
		effectiveKey:    cfg.APIKey,
		pendingKey:      cfg.PendingAPIKey,
		locateTranscript: func(capsuleDir string, entries []capsule.SpawnJournalEntry) string {
			return runtime.LiveTranscript(rtCfg.StepAgentSessionsDir(), rtCfg.ClaudeConfigDir(), capsuleDir, entries)
		},
		stepHubAllow: laneHubAllow,
		stepHubAttrs: laneHubAttrs,
	}
	if d.verifyQ == nil {
		// unhosted: this daemon's own heavy-run admission
		d.verifyQ = newVerifyQueue(cfg.StartupFile.Daemon.MaxConcurrentBars, cfg.StartupFile.Daemon.BarLoadThreshold)
	}
	d.runCtx, d.cancelRun = context.WithCancel(context.Background())
	d.tasks = d.newTaskManager(&rtCfg)
	rtCfg.EventTap = d.tapRuntimeEvent
	// the daemon-owned step environment every step process carries (the
	// live command PATH as its PATH, the Python interpreter; stepenv.go)
	rtCfg.StepEnv = d.stepEnv
	// a disabled Cell's reconciliation keeps held
	// operator-paused work paused (d is read at call time).
	rtCfg.ExecutionDisabled = func() string { return d.executionDisabled() }
	// An owner's cancel (an `intent: cancel`, intent.go), its last leg: once
	// the hub took the blocked close, the hub's existing blocked→canceled
	// door ends the work item.
	rtCfg.CancelRequested = d.cancelAnsweredWorkItem
	// a close stops the WI's monitor (drained) before
	// its seal push and SessionEnd; d.obs is read at call time.
	rtCfg.ReleaseMonitor = func(wi string) {
		if d.obs != nil {
			d.obs.Release(wi)
		}
	}

	// F6-A: the claim-time memory snapshot seam — the daemon binds it to
	// aihub's memory search (bounded excerpts; the machine credential
	// stays here, never in an agent env). See memface.go. The hit count
	// rides the runtime config (RecallTopK; 0 keeps the default 10).
	rtCfg.RecallTopK = cfg.RecallTopK
	rtCfg.MemoryRecall = recallSnapshot(cfg.Hub)
	// M4 cadence, resolved once: observe revalidates the running jobs,
	// the runtime the retained paused records (polyforge#217) — 0 = the
	// 30s default, negative = disabled for both.
	revalidateEvery := cfg.Supervision.SessionRevalidateEvery
	if revalidateEvery == 0 {
		revalidateEvery = observe.DefaultRevalidateEvery
	}
	rtCfg.SessionRevalidateEvery = revalidateEvery
	rtMsgs := supervise.NewQueue[protocol.RTMsg]()
	obMsgs := supervise.NewQueue[protocol.OBMsg]()
	rt := runtime.New(rtCfg, cfg.Hub, sp, rtMsgs, obMsgs)
	obs, err := observe.New(observe.Config{
		RT:           rtMsgs,
		OB:           obMsgs,
		Agent:        d.agentOf,
		DataDir:      cfg.DataDir,
		PollInterval: cfg.ObservePoll,
		PushFunc:     cfg.PushFunc,
		// F3: the live-transcript locator the monitor's per-dispatch
		// commit-boundary sync calls.
		LocateLive: func(capsuleDir string, spawnAt time.Time, harness, session string) string {
			return runtime.LocateTranscript(rtCfg.StepAgentSessionsDir(), rtCfg.ClaudeConfigDir(), capsuleDir, spawnAt, harness, session)
		},
		// the claude subagent sidecars ride the same sync.
		SyncSubagents: func(capsuleDir string) {
			runtime.SyncSubagentTranscripts(rtCfg.ClaudeConfigDir(), capsuleDir)
		},
		// M4 wiring: the periodic session revalidation rides the monitor
		// tick against the shared aihub client (fencing-on-use discovery —
		// a superseded daemon learns within ~30s even with zero push
		// traffic; unit-tested in observe, daemon-wired here).
		SessionVerify: cfg.Hub,
		// the supervisor's bounds pass through; zero fields keep the
		// documented defaults
		Registry:        d.sup,
		ModelErrorGrace: cfg.Supervision.ModelErrorGrace,
		DriverStall:     cfg.Supervision.StallWindow,
		// a live reload of step_timeout/stall_window governs the next tick
		LiveLimits: func() supervise.Defaults { return liveLimits() },
		// M4 cadence: the one resolved above.
		SessionRevalidateEvery: revalidateEvery,
	})
	if err != nil {
		return nil, err
	}
	d.rt = rt
	d.obs = obs
	// the model-health tracker is opened by Run once the
	// data-dir flock is held. Its rebuild writes <DataDir>/model-health.json,
	// and only the lock holder writes the data dir (AGENTS.md); opening it
	// here in New would let a start the guard refuses overwrite the running
	// daemon's snapshot.
	for _, w := range cfg.EnvWarnings {
		_ = obs.Events().Append("", observe.EvRetiredEnvIgnored, map[string]any{"warning": w})
	}
	d.q.persist = d.persistQueue

	// The persisted auto switch (the daemon's OWN state file;
	// jobs/attempts bookkeeping is Runtime's state.json and is never
	// duplicated here).
	if err := d.loadDaemonState(); err != nil {
		return nil, err
	}
	// cfg.Auto is an EXPLICIT flag override only (#18): nil keeps the
	// persisted switch — an auto daemon restarting stays auto — and
	// loadDaemonState has already defaulted to off when no state file
	// exists.
	if cfg.Auto != nil && *cfg.Auto != d.modes.Auto() {
		if err := d.modes.SetAuto(*cfg.Auto); err != nil {
			return nil, fmt.Errorf("daemon: initial auto: %w", err)
		}
		// Run persists the override once it holds daemon.lock.
	}
	return d, nil
}

// Run drives the daemon until a shutdown completes (or the caller's
// context ends, which triggers the same shutdown).
// It returns when the process should exit.
func (d *Daemon) Run(ctx context.Context) error {
	// M5: flock the daemon data dir BEFORE the socket probe — the two
	// together are the single-instance guard. The dial probe alone is a
	// TOCTOU: two daemons started in the same window can both observe a
	// dead socket and both bind it; the flock is the kernel-serialized
	// half (LOCK_EX|LOCK_NB on DataDir/daemon.lock), held for Run's
	// lifetime and released when Run returns. Both come before any lane
	// starts: a refused start has nothing to unwind, and only the lock
	// holder ever writes the data dir (see ownsDataDir).
	lock, err := acquireDataDirLock(d.cfg.DataDir)
	if err != nil {
		return err
	}
	defer d.releaseDataLock()
	// the cross-machine identity is loaded/created once,
	// with the data-dir flock held. A corrupt key refuses the start (an
	// operator fix); the key is never regenerated silently.
	key, pub, kerr := loadOrCreateMachineKey(d.cfg.DataDir)
	if kerr != nil {
		_ = lock.Close()
		return &needsOperatorError{kerr}
	}
	d.machineKey, d.publicKeyB64 = key, pub
	if hc := d.faceClient(); hc != nil {
		hc.SetMachineIdentity(d.cfg.MachineID, key)
	}
	// only now, with the data-dir flock held, rebuild the
	// model-health aggregate from events/*.jsonl and write its snapshot.
	// Open performs that write; running it in New would write the data dir
	// before the single-instance guard has refused a second daemon. This
	// runs before the socket appears, so a serving daemon always has its
	// tracker assembled. Attach the tracker as the event-log observer
	// before any lane appends, and flush it on every Run exit (the shutdown
	// also closes it; Close is idempotent). The tracker's own
	// model_unhealthy append goes through this same log; its Ingest sees it
	// as a no-op (the episode is already armed).
	mh, err := modelhealth.Open(d.cfg.DataDir, observe.EventsRetention, time.Now, func(a modelhealth.Alert) {
		_ = d.obs.Events().Append("", observe.EvModelUnhealthy, a)
	})
	if err != nil {
		_ = lock.Close()
		return err
	}
	d.obs.Events().SetObserver(func(e protocol.EventEntry) {
		mh.Ingest(e)
		d.ingestDecision(e) // the decision feed
	})
	d.faceMu.Lock()
	d.modelHealth = mh
	d.faceMu.Unlock()
	defer mh.Close()
	// The mapped workspaces' user lanes (workflow contract, "User lanes"):
	// unlike a built-in lane (New refuses the start), a refused one only
	// makes that lane unavailable — recorded, and the start goes on.
	d.reportUserLanes()

	// Startup askpass sweep (crash residue): a previous daemon killed
	// mid-push leaves one-shot credential scripts — they hold push
	// secrets — under <DataDir>/git/askpass. Only the data-dir lock holder
	// sweeps, so nothing of another daemon can be live there: every entry
	// is residue. Best-effort, non-fatal.
	if err := gitops.SweepAskpass(filepath.Join(d.cfg.DataDir, "git")); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: askpass sweep: %v\n", err)
	}
	// Same for the carrier lane's pf-carrier-* temporary bare repos.
	if err := gitops.SweepCarrierTemps(filepath.Join(d.cfg.DataDir, "git")); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: carrier temp sweep: %v\n", err)
	}
	// An explicit -auto must survive the restart it rode in on: without
	// this persist, `-auto=false` over a persisted auto-on would be undone
	// by the NEXT boot (the fx3 gap). Only the lock holder writes it.
	if d.cfg.Auto != nil {
		d.persistModeQuietly()
	}
	var lst net.Listener
	if !d.cfg.Hosted {
		lst, err = d.listenUDS()
		if err != nil {
			_ = lock.Close()
			return err
		}
	}
	// closeFace and ownsDataDir read both under faceMu: a shutdown
	// arriving in the next instant cannot race the assignment.
	d.faceMu.Lock()
	d.dataLock, d.lst = lock, lst
	d.faceMu.Unlock()

	stop := context.AfterFunc(ctx, d.cancelRun) // the caller giving up stops the loops too
	defer stop()
	go d.rt.Run(d.runCtx)
	d.obsStarted.Store(true)
	go func() {
		defer close(d.obsDone)
		d.obs.Run(d.runCtx)
	}()

	// Restart (§十一): every recorded job and claiming-journal entry is
	// handed to its runtime actor — strictly after this process owns the
	// data dir and the socket (no second process may push the same work
	// items). It asks aihub nothing, so the face serves at once; each
	// actor reconciles its work item (and retries the hub) on its own.
	if err := d.rt.Restart(d.runCtx); err != nil {
		// state.json is unreadable: stop without serving or persisting,
		// so it stays the truth for the next start after the manual
		// remediation the error names; the process exits non-zero.
		d.closeFace()
		d.cancelRun()
		d.rt.Close()
		return &needsOperatorError{fmt.Errorf("restart reconciliation failed: %w", err)}
	}
	// v2 §5/§6: replay the persisted submit queue (after Restart seeded
	// the claiming journal's starts, so a WI is never claimed twice) and
	// tell every recovered/parked WI the daemon restarted.
	d.replaySubmitQueue()
	d.announceRestart()
	// a task that was running when the previous daemon died
	// is marked interrupted (never resumed).
	if err := d.tasks.Recover(); err != nil {
		fmt.Fprintf(os.Stderr, "daemon: task recovery: %v\n", err)
	}

	var serveErr chan error
	if d.cfg.Hosted {
		if d.modes.ShuttingDown() {
			return nil
		}
	} else {
		var serving bool
		serveErr, serving = d.startFace(lst)
		if !serving {
			// A shutdown closed the face while this process was reconciling:
			// nothing is left to drive.
			return nil
		}
	}
	close(d.ready)

	dispatch := time.NewTicker(d.cfg.DispatchTick)
	defer dispatch.Stop()
	drain := time.NewTicker(d.cfg.DrainInterval)
	defer drain.Stop()
	// M3 heartbeat: its own goroutine on runCtx (the shutdown cancels it)
	// so liveness never blocks or is blocked by the dispatch/drain loop.
	// Log-only failures inside; never fatal.
	go d.heartbeatLoop(d.runCtx)
	// the decision feed's hub reconcile of blocked/paused work items with
	// no local job (decisions.go): its own goroutine on runCtx.
	go d.decisionReconcileLoop(d.runCtx)
	for {
		select {
		case <-ctx.Done():
			// The caller gave up: run the same shutdown.
			return d.Shutdown(context.Background(), "caller-context: the Run caller's context is done")
		case <-d.shutDone:
			return nil
		case err := <-serveErr:
			if err != nil && err != http.ErrServerClosed {
				_ = d.Shutdown(context.Background(), fmt.Sprintf("serve-error: %v", err))
				return err
			}
			// Only the shutdown closes the face, and it still has the
			// runtime's Close to run: wait for it.
			<-d.shutDone
			return nil
		case <-dispatch.C:
			d.maybeSwitchConnection()
			d.dispatch()
			// a queued heavy dispatch the gates now allow
			// starts here
			d.verifyQ.Tick()
		case <-drain.C:
			d.fetchAutoWave()
		}
	}
}

// startFace begins serving the UDS face on lst. It reports false
// when a shutdown already closed the face (the shutdown-
// during-reconciliation window): the listener is then dead and Run
// must not serve on it. The srv assignment happens under faceMu so a
// concurrent closeFace either sees this server (and closes it, making
// Serve answer ErrServerClosed) or has already latched faceClosed.
func (d *Daemon) startFace(lst net.Listener) (chan error, bool) {
	d.faceMu.Lock()
	defer d.faceMu.Unlock()
	if d.faceClosed {
		return nil, false
	}
	d.srv = &http.Server{Handler: d.handler(), ConnContext: credConnContext}
	serveErr := make(chan error, 1)
	go func() { serveErr <- d.srv.Serve(lst) }()
	return serveErr, true
}

// closeFace tears the UDS face down (the shutdown's last step): the serving
// server if Run started it, otherwise the bare listener (a shutdown
// that raced the startup reconciliation window, before Serve began).
// Idempotent under the mutex; a second close is a no-op.
func (d *Daemon) closeFace() {
	d.faceMu.Lock()
	defer d.faceMu.Unlock()
	if d.faceClosed {
		return
	}
	d.faceClosed = true
	if d.srv != nil {
		_ = d.srv.Close()
		return
	}
	if d.lst != nil {
		_ = d.lst.Close()
	}
}

// agentOf resolves the work item's live agent and the per-WI facts its
// observe monitor needs (observe.Config.Agent).
func (d *Daemon) agentOf(wi string) (observe.Handle, observe.WIInfo, bool) {
	ji, ok := d.rt.Info(wi)
	if !ok {
		return nil, observe.WIInfo{}, false
	}
	h, ok := ji.Agent.(observe.Handle) // every starter returns an Agent; nil = no live agent
	if !ok {
		return nil, observe.WIInfo{}, false
	}
	return h, observe.WIInfo{WorkItem: ji.WorkItem, CapsuleDir: ji.CapsuleDir, Git: ji.Git}, true
}

// interruptedFromCause maps the recovery lane's cause onto the driver's
// interrupted-dispatch archive: an empty cause or an operator-shutdown
// attach is a deliberate stop and archives nothing; every other cause is
// archived verbatim (an audit attempt, never a counted failure). The
// dispatch's wall time is booked toward the step's T only for a relaunch
// after a hang, stall or death — never for a daemon restart or an
// operator resume, so downtime never counts.
func interruptedFromCause(cause string) *driver.InterruptedDispatch {
	if cause == "" || cause == runtime.CauseOperatorAttach {
		return nil
	}
	return &driver.InterruptedDispatch{Detail: cause, BookWall: !runtime.LifecycleCause(cause)}
}

// tapRuntimeEvent lands one runtime-sourced diagnostic event in the
// observe lane's durable events log (the seal push_failed trace).
func (d *Daemon) tapRuntimeEvent(wi, kind string, body map[string]any) {
	if d.obs == nil {
		return
	}
	_ = d.obs.Events().Append(wi, kind, body)
}

// resolveWI maps any work-item id form to the canonical id the runtime
// and observe key everything by (F2): the runtime's alias table first
// (live jobs), aihub's id-or-slug endpoint otherwise (sealed or unknown
// work items). item is the aihub record when aihub answered, so a caller
// that needs the record does not read it again; it is nil when the
// alias table answered. "" only when both paths fail — callers degrade
// to the input spelling. Hence aihub was read iff item != nil or the id
// is "".
func (d *Daemon) resolveWI(wi string) (string, *api.WorkItem) {
	if c := d.rt.Resolve(wi); c != "" {
		return c, nil
	}
	item, err := d.hub.GetWorkItem(wi)
	if err != nil || item == nil || item.ID == "" {
		return "", nil
	}
	return item.ID, item
}

// canonWI is resolveWI with the pass-through fallback: an unresolvable
// spelling keeps its input form (the claim path resolves it
// server-side).
func (d *Daemon) canonWI(wi string) string {
	if c, _ := d.resolveWI(wi); c != "" {
		return c
	}
	return wi
}

// driverLog is the driver's EventLogger: it resolves the observe event
// log at call time (the log exists only after observe.New).
func (d *Daemon) driverLog() driver.EventLogger { return driverEventLog{d} }

type driverEventLog struct{ d *Daemon }

func (l driverEventLog) Append(wi, kind string, body any) error {
	if l.d.obs == nil {
		return nil
	}
	return l.d.obs.Events().Append(wi, kind, body)
}

// ---------------------------------------------------------------------------
// Dispatcher (reference.md §6.3 backfill) and auto-pull loop (reference.md §6.3).

// occupiedSlots is the set of WIs holding a concurrency slot: the
// runtime's live jobs, whoever started them (dispatch, restart recovery,
// a blocked resume). Finding 2 (v1.5 review): a RETAINED paused job
// holds no agent and no writer — it must not pin a slot (four
// operator-paused jobs would otherwise starve the whole daemon).
func (d *Daemon) occupiedSlots() map[string]bool {
	live := map[string]bool{}
	for _, ji := range d.rt.List() {
		if ji.State == runtime.StatePaused {
			continue
		}
		if d.verifyQ != nil && d.verifyQ.queuedFor(ji.WorkItem) {
			// a verification queued for the heavy-run
			// admission runs no process — it holds no slot either
			continue
		}
		if ji.AwaitingOwner {
			// workflow contract v2 §5.2 (owner ruling Q9): a step waiting on
			// the owner's reply to its `pf ask` releases its running slot;
			// the driver takes it back before its next dispatch
			continue
		}
		live[ji.WorkItem] = true
	}
	return live
}

// dispatch starts pending jobs into free slots, priority-desc then FIFO.
// Each claim runs in its own goroutine: a slow capsule or task-repo
// clone does not hold the Run loop, so the loop keeps serving shutdown,
// drain and the next dispatch ticks. The queue counts a Take()n entry
// against the slots until it settles, so the concurrency cap holds across
// the goroutines.
func (d *Daemon) dispatch() {
	for !d.modes.ShuttingDown() {
		e := d.takeNext()
		if e == nil {
			return
		}
		// A claim that races the shutdown defers itself in the runtime;
		// nothing here waits for it.
		d.claiming.Add(1)
		go func(e qEntry) {
			defer d.claiming.Add(-1)
			d.startEntry(e)
		}(*e)
	}
}

// takeNext answers the next entry to start: through the Host's two-level
// gate when hosted (hostslots.go), else the Cell's own queue; nothing
// while the Cell's execution is disabled.
func (d *Daemon) takeNext() *qEntry {
	if d.executionDisabled() != "" {
		return nil // a disabled Cell starts nothing; its queue is kept
	}
	if d.cfg.HostSlots != nil {
		return d.cfg.HostSlots.take(d)
	}
	return d.q.Take()
}

// submitWait carries one submit's dispatch outcome to the route that
// is answering it: nil once aihub answered the claim,
// else the error that ended the attempt. Exactly one outcome is ever
// delivered; a caller that hung up uses the once up (abandon), so a
// later outcome is left to the submit_rejected event.
type submitWait struct {
	ch   chan error
	once sync.Once
}

// settle delivers err unless an outcome was already delivered or the
// caller hung up. A nil receiver delivers nothing.
func (w *submitWait) settle(err error) {
	if w == nil {
		return
	}
	w.once.Do(func() { w.ch <- err })
}

// abandon is the hung-up caller's side: no outcome is delivered after it.
func (w *submitWait) abandon() {
	if w != nil {
		w.once.Do(func() {})
	}
}

// armSubmitWait registers the waiter of wi's submit before its entry is
// enqueued; nil when one is already armed (a concurrent submit of the
// same work item).
func (d *Daemon) armSubmitWait(wi string) *submitWait {
	d.submitWaitMu.Lock()
	defer d.submitWaitMu.Unlock()
	if d.submitWaits == nil {
		d.submitWaits = map[string]*submitWait{}
	}
	if d.submitWaits[wi] != nil {
		return nil
	}
	w := &submitWait{ch: make(chan error, 1)}
	d.submitWaits[wi] = w
	return w
}

// takeSubmitWait removes and answers wi's waiter (nil: nobody waits — a
// queued, replayed or auto-pulled entry).
func (d *Daemon) takeSubmitWait(wi string) *submitWait {
	d.submitWaitMu.Lock()
	defer d.submitWaitMu.Unlock()
	w := d.submitWaits[wi]
	delete(d.submitWaits, wi)
	return w
}

// dropSubmitWait removes w if it is still wi's registered waiter and
// answers whether it did: false means the dispatch already took it and
// will deliver the outcome.
func (d *Daemon) dropSubmitWait(wi string, w *submitWait) bool {
	d.submitWaitMu.Lock()
	defer d.submitWaitMu.Unlock()
	if d.submitWaits[wi] != w {
		return false
	}
	delete(d.submitWaits, wi)
	return true
}

// startEntry runs one Take()n queue entry's claim to its verdict and
// settles the entry. A submit waiting on the entry hears
// the outcome: claimed once aihub answered the claim (before the clone),
// else the error, delivered only after the queue settled and the refusal
// was recorded as the work item's submit_rejected event, whether or not
// a caller hears it.
func (d *Daemon) startEntry(e qEntry) {
	w := d.takeSubmitWait(e.WorkItemID)
	err := d.rt.Start(d.runCtx, e.WorkItemID, runtime.StartOpts{
		Origin: &e.Origin, Models: e.Models,
		OnClaimed: func() { w.settle(nil) },
	})
	if runtime.IsClaimDeferred(err) {
		// the shutdown stopped the claim: the entry stays in flight on
		// disk (the queue file keeps it) and a claim that landed stays
		// in the claiming journal — the next start replays them.
		w.settle(err)
		return
	}
	if errors.Is(err, runtime.ErrClaimClosed) {
		// an operator close settled the claim (Stop during CLAIMING)
		if serr := d.q.Settle(e.WorkItemID); serr != nil {
			fmt.Fprintf(os.Stderr, "daemon: submit queue persist after %s: %v\n", e.WorkItemID, serr)
		}
		w.settle(err)
		return
	}
	var ae *api.Error
	if errors.As(err, &ae) && ae.Code == api.ErrDaemonHubUnavailable {
		// The hub could not be reached, or the claim's answer was lost: the
		// submit stays queued and is retried with backoff (never
		// dropped on a transient blip, never blocking the other entries). A
		// claim that may have landed keeps its claiming-journal entry, so
		// the retry replays the SAME claim and adopts the session.
		// One log line per episode.
		if e.Deferrals == 0 {
			fmt.Fprintf(os.Stderr, "daemon: start %s deferred, will retry with backoff: %v\n", e.WorkItemID, err)
		}
		d.q.Requeue(e.WorkItemID)
		w.settle(err)
		return
	}
	replayHeld := errors.As(err, &ae) && ae.Code == api.ErrDaemonConflict
	if replayHeld {
		// the work item is already held here: a replayed submit whose
		// claiming-journal start the runtime adopted first
		// (replaySubmitQueue)
		d.dropReplay(e, "the work item is already held by this daemon (claiming-journal replay or kept record)")
	} else if err != nil {
		fmt.Fprintf(os.Stderr, "daemon: start %s failed: %v\n", e.WorkItemID, err)
	}
	if serr := d.q.Settle(e.WorkItemID); serr != nil {
		fmt.Fprintf(os.Stderr, "daemon: submit queue persist after %s: %v\n", e.WorkItemID, serr)
	}
	if err != nil && !replayHeld {
		// Every dispatch refusal is a durable event on the WI (v2 §5),
		// recorded BEFORE a waiting submit hears it: watchers and the
		// decision feed surface it whether or not a caller also heard it
		// on the spot (a queued or replayed entry, a claim already
		// answered claimed, a caller that hung up hears nothing).
		d.recordSubmitRejected(e, err)
	}
	w.settle(err) // a no-op once OnClaimed reported the claim
	// An origin=auto claim that settled warns on every
	// repo clone missing from the mapped workspace — one stderr line here
	// on the dispatch goroutine, holding no lock, the claim already
	// settled: nothing is blocked or delayed, no event type or field (an
	// auto-pulled claim has no submit output to read). A hub read failure
	// skips the line.
	if e.Origin.Kind == runtime.OriginAuto && err == nil {
		d.warnMissingClones(e.WorkItemID)
	}
}

// laneOf answers the lane bundle of project's lane set (the built-in lanes
// and the user lanes of the workspace the project maps to), the way
// laneExclusive reads it; nil when there is no workflows dir or the lane
// does not load — a lane that cannot be read requires acceptance
// ((*workflow.Lane).RequiresAcceptance).
func (d *Daemon) laneOf(project, bundle string) *workflow.Lane {
	if d.cfg.WorkflowsDir == "" {
		return nil
	}
	ws, err := d.plan().WorkspaceFor(project)
	if err != nil {
		ws = ""
	}
	lane, _, err := workflow.ForWorkspace(d.cfg.WorkflowsDir, ws).Load(bundle)
	if err != nil {
		return nil
	}
	return lane
}

// fetchAutoWave pulls the ready queue and enqueues what is claimable
// (reference.md §6.3): only with auto on, never once shutting_down latched, and
// never beyond the concurrency it can actually start — the wave's
// free-slot budget is spent per enqueued entry (audit B3a: one wave
// enqueues at most the CURRENT free-slot count; the rest of the ready
// queue stays on aihub for the next wave) and is reduced by the entries
// ALREADY queued — including ones held back in their DAEMON-503
// backoff: they occupy no running slot yet, but they are waiting for
// one, so a wave that ignored them could push the pending set past
// max_concurrent. The auto switch is RE-CHECKED before each enqueue:
// the wave's ready-queue fetch is a batch of HTTP round trips, and a
// `pf ops auto off` landing mid-fetch must stop THIS wave too — the
// switch flip's purge (autoOffPurge) only sees entries already landed.
// Authorization-class denials raise the denial signal — a typed
// queue_fetch_denied event in the observe lane's durable log, once per
// healthy→denied episode (the flag clears on the next successful
// fetch); transport-class losses stay silent idle.
func (d *Daemon) fetchAutoWave() {
	if !d.modes.AdmitAutoPull() || d.executionDisabled() != "" || d.pendingChange() != "" || d.degradedReason() != "" {
		// a degraded Cell (its Hub unreachable or refusing its credential)
		// stops its automatic claims until a Hub round trip succeeds again;
		// running work keeps the existing recovery and other Cells serve
		return
	}
	// all pending entries (backoff included) are waiting for a slot:
	// the wave may only enqueue what is left of the concurrency cap.
	budget := d.q.FreeSlots() - d.q.LenPending()
	if budget <= 0 {
		return
	}
	// polyforge#113: no daemon-level project scope exists — the drain
	// wave iterates ALL mapped projects. A per-project authorization-class
	// denial raises the denial signal naming THAT project; healthy
	// projects still enqueue (partial coverage beats none, and the
	// denial episode is visible).
	var items []api.WorkItem
	denied := false
	for _, project := range d.plan().Projects() {
		if _, lerr := d.rt.LoadWorkspace(project); lerr != nil {
			if d.yamlRefused == nil {
				d.yamlRefused = map[string]string{}
			}
			if d.yamlRefused[project] != lerr.Error() {
				d.yamlRefused[project] = lerr.Error()
				fmt.Fprintf(os.Stderr, "daemon: drain skips project %q (nothing claimed): %v\n", project, lerr)
			}
			continue
		}
		delete(d.yamlRefused, project)
		pis, err := d.hub.Queue(project)
		if err != nil {
			if authDenied(err) {
				denied = true
				fmt.Fprintf(os.Stderr, "daemon: auto-pull fetch denied: ready queue of project %q refused authorization (%v)\n", project, err)
				if !d.queueFetchDenied && d.obs != nil {
					d.queueFetchDenied = true
					_ = d.obs.Events().Append("", observe.EvQueueFetchDenied, map[string]any{
						"project": project,
						"class":   "authorization",
						"error":   err.Error(),
					})
				}
			}
			continue
		}
		items = append(items, pis...)
	}
	if !denied {
		d.queueFetchDenied = false // episode cleared: this fetch fully succeeded
	}
	var badBundles map[string]error
	if len(items) > 0 {
		var pairs [][2]string
		for _, it := range items {
			if it.BundleName != nil {
				pairs = append(pairs, [2]string{it.ProjectName, *it.BundleName})
			}
		}
		badBundles = d.rt.RefusedBundles(pairs)
	}
	for key := range d.bundleRefused {
		if _, still := badBundles[key]; !still {
			delete(d.bundleRefused, key)
		}
	}
	for _, it := range items {
		if budget == 0 {
			break // the wave spent its slots; the next wave claims the rest
		}
		if it.BundleName == nil {
			continue // registration tickets never execute
		}
		if (it.Content == nil || !workflow.HasAcceptance(*it.Content)) && d.laneOf(it.ProjectName, *it.BundleName).RequiresAcceptance() {
			// an execution ticket that lists no acceptance
			// criterion needs interaction first (the predicate pf submit
			// applies) when its lane requires one — the lane's
			// `acceptance` declaration; it stays queued
			// and pf queue marks it
			continue
		}
		if berr, bad := badBundles[it.ProjectName+" "+*it.BundleName]; bad {
			// polyforge#175: the bundle fails to load (e.g. an unprefixed
			// candidate) or the project's lane set does not carry it — a
			// claim would be refused; leave the WI queued for other
			// machines and log the refusal once per project and lane
			key := it.ProjectName + " " + *it.BundleName
			if d.bundleRefused == nil {
				d.bundleRefused = map[string]string{}
			}
			if d.bundleRefused[key] != berr.Error() {
				d.bundleRefused[key] = berr.Error()
				fmt.Fprintf(os.Stderr, "daemon: drain skips project %q's work items on bundle %q (nothing claimed): %v\n", it.ProjectName, *it.BundleName, berr)
			}
			continue
		}
		if d.q.IsRunning(it.ID) || d.q.IsPending(it.ID) {
			continue
		}
		// auto off may land while this wave was fetching (its round trips
		// are the window): the switch is re-checked under the queue lock
		// the auto-off purge takes, so an entry either sees auto off or
		// lands before the purge and is dropped by it (audit B3 race,
		// wi_seWTgHeKtaa8).
		// A rotation that became pending during the fetch stops the wave:
		// the check and the enqueue are one step under drainGate.
		// The persist runs outside the gate under an admitting reservation.
		d.drainGate.Lock()
		if d.pendingChange() != "" {
			d.drainGate.Unlock()
			return
		}
		d.admitting.Add(1)
		d.drainGate.Unlock()
		ok, err := d.q.EnqueueIf(qEntry{WorkItemID: it.ID, Priority: it.Priority, Origin: runtime.Origin{Kind: runtime.OriginAuto}}, d.modes.AdmitAutoPull)
		d.admitting.Add(-1)
		if err != nil {
			fmt.Fprintf(os.Stderr, "daemon: drain enqueue %s: queue persist failed: %v\n", it.ID, err)
			continue // nothing landed: the budget is not spent
		}
		if !ok {
			return // auto is off: nothing more may be enqueued
		}
		budget--
	}
}

// autoOffPurge removes the submit queue's not-yet-started origin=auto
// entries (v2-converge §5: auto off = only explicit pf submit; audit
// B3b) — they would otherwise start later as slots free (dispatch runs
// regardless of the switch) and replay after a restart. Each dropped
// entry lands a submit_dropped event; a persist failure is loud and
// leaves the queue as it was (the switch itself already flipped).
func (d *Daemon) autoOffPurge() {
	dropped, err := d.q.PurgeOrigin(runtime.OriginAuto)
	if err != nil {
		fmt.Fprintf(os.Stderr, "daemon: auto-off queue purge failed: %v\n", err)
		return
	}
	for _, e := range dropped {
		fmt.Fprintf(os.Stderr, "daemon: auto off: pending %s dropped from the submit queue\n", e.WorkItemID)
		if d.obs != nil {
			body := map[string]any{
				"reason":       "auto off",
				"submitted_at": e.SubmittedAt.UTC().Format(time.RFC3339Nano),
			}
			if e.Origin.Kind != "" {
				body["origin"] = e.Origin
			}
			_ = d.obs.Events().Append(e.WorkItemID, observe.EvSubmitDropped, body)
		}
	}
}

// authDenied classifies a ready-queue error as authorization-class: a
// typed aihub/api error whose code or derived HTTP status is 401/403/404
// (404 is the server folding a non-member's canRead denial). Everything
// else — transport loss, 5xx — is connection-class (drain's silent idle).
func authDenied(err error) bool {
	var ae *api.Error
	if !errors.As(err, &ae) {
		return false
	}
	switch ae.HTTPStatus() {
	case 401, 403, 404:
		return true
	}
	return false
}

// ---------------------------------------------------------------------------
// UDS listener

// listenUDS binds an unhosted daemon's own socket — the same bind the Host
// uses (listenHostUDS: single-instance probe, stale socket removal, and the
// credListener that captures each accepted conn's UDS peer credential at
// accept time, polyforge#110).
func (d *Daemon) listenUDS() (net.Listener, error) {
	return listenHostUDS(d.cfg.SocketPath)
}

// terminalStatus reports whether an aihub status is terminal for the
// wrap/pause arbitration's graceful-already-terminal answer.
func terminalStatus(s string) bool {
	switch s {
	case api.StatusWrapped, api.StatusFailed, api.StatusCanceled:
		return true
	}
	return false
}

// mapCloseErr translates a Runtime.Stop failure for the HTTP face:
// a typed api error passes through (CORE-* transparency), a lost race
// ("no active job") maps to the graceful already-terminal answer, and
// everything else is DAEMON-500.
func (d *Daemon) mapCloseErr(err error) (graceful bool, ae *api.Error) {
	if strings.Contains(err.Error(), "no active job") {
		return true, nil
	}
	if errors.As(err, &ae) {
		return false, ae
	}
	return false, &api.Error{Code: api.ErrDaemonInternal, Detail: err.Error()}
}

// ---------------------------------------------------------------------------
// M5: the data-dir flock (single-instance guard, kernel half).

// modelHealthTracker returns the assembled tracker (nil before Run has
// taken the data-dir lock). The field is written by Run and read here
// under faceMu, so a shutdown or test racing startup never races the
// assignment.
func (d *Daemon) modelHealthTracker() *modelhealth.Tracker {
	d.faceMu.Lock()
	defer d.faceMu.Unlock()
	return d.modelHealth
}

// releaseDataLock drops the data-dir flock (Run returning is the
// daemon's end; the next process may take over the data dir).
// ownsDataDir reports whether this process holds daemon.lock: only the
// holder writes the data dir's state (a start the lock refused never
// seeded its runtime and must not overwrite the running daemon's).
func (d *Daemon) ownsDataDir() bool {
	d.faceMu.Lock()
	defer d.faceMu.Unlock()
	return d.dataLock != nil
}

func (d *Daemon) releaseDataLock() {
	d.faceMu.Lock()
	defer d.faceMu.Unlock()
	if d.dataLock != nil {
		_ = d.dataLock.Close()
		d.dataLock = nil
	}
}

// acquireDataDirLock takes an exclusive non-blocking flock on
// <DataDir>/daemon.lock and answers the held file (the caller keeps it
// open for the daemon's lifetime; the kernel releases it at exit). A
// held lock is another daemon on this data dir — the same machine's
// second instance, the TOCTOU the socket probe alone cannot close.
func acquireDataDirLock(dataDir string) (*os.File, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	f, err := cellcfg.HoldHostLock(dataDir)
	if errors.Is(err, cellcfg.ErrHostLockHeld) {
		path := filepath.Join(dataDir, cellcfg.HostLockFile)
		pid, _ := os.ReadFile(path)
		holder := ""
		if p := strings.TrimSpace(string(pid)); p != "" {
			holder = " (pid " + p + ")"
		}
		return nil, fmt.Errorf("daemon: another daemon%s, or a running `pf migrate cell-layout`, holds the data-dir lock %s", holder, path)
	}
	if err != nil {
		return nil, err
	}
	// best-effort pid stamp (diagnostics; the lock itself is the guard)
	_ = f.Truncate(0)
	_, _ = f.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0)
	return f, nil
}

// cancelDoorPath is the hub's cancel door for one work item — the same
// door `pf cancel` uses.
func cancelDoorPath(wi string) string {
	return "/api/v1/work-items/" + url.PathEscape(wi) + "/cancel"
}

// cancelAnsweredWorkItem calls the hub's cancel door for a work item whose
// capsule records a cancel intent (an `intent: cancel` the step's agent
// delivered on an owner message, intent.go), after its blocked close: the
// owner's words with the agent's reading and the message's stamp ride the
// work_item_canceled event, and the hub records the authenticated caller
// as its actor. An already-terminal work item is done (the replay after a
// restart).
func (d *Daemon) cancelAnsweredWorkItem(wi string, intent capsule.CancelIntent) {
	err := d.hub.Do("POST", cancelDoorPath(wi), api.CancelRequest{Reason: intent.Answer, QuestionID: intent.QuestionID}, nil)
	var ae *api.Error
	if err == nil || (errors.As(err, &ae) && ae.Code == api.ErrCoreGone) {
		return
	}
	fmt.Fprintf(os.Stderr, "daemon: %s: the owner's cancel could not cancel the work item at the hub (it stays blocked; pf cancel finishes it): %v\n", wi, err)
}

// profile answers the Cell's name (default when unnamed).
func (d *Daemon) profile() string {
	if d.cfg.Profile == "" {
		return cellcfg.DefaultProfile
	}
	return d.cfg.Profile
}

// executionDisabled answers why this Cell's execution plane is closed
// ("" = open).
func (d *Daemon) executionDisabled() string {
	d.reloadMu.Lock()
	defer d.reloadMu.Unlock()
	return d.execDisabled
}

// execDisabledRefusal answers the one refusal of every entry point that
// would start execution in a Cell whose execution plane is closed (a
// submit, a task spawn, a resume, an owner message): DAEMON-409 naming
// the Cell's reason; nil while it is open. Queries, cancel and work
// already running are never refused by it.
func (d *Daemon) execDisabledRefusal() error {
	why := d.executionDisabled()
	if why == "" {
		return nil
	}
	return &api.Error{Code: api.ErrDaemonConflict, Detail: "execution is disabled on this Cell: " + why + " — nothing was started; queries, cancel and running work go on"}
}

// checkInstalledLanes loads every lane of the daemon's workflows dir
// through the one lane loader: nil when each loads (or no workflows dir is
// configured or present: nothing is installed, and every claim refuses its
// lane, WFC-123), else the error naming every defect of every refused lane.
func checkInstalledLanes(dir string) error {
	if dir == "" {
		return nil
	}
	lanes, err := workflow.LaneDirs(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("daemon: the workflows dir %s is unreadable: %w", dir, err)
	}
	var bad []string
	for _, l := range lanes {
		if _, errs := workflow.Load(l); len(errs) > 0 {
			bad = append(bad, workflow.AsLoadError(l, errs).Error())
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("daemon: the installed lanes do not load under workflow contract %d — a release defect; the daemon does not start (`pf workflows validate` prints the same report):\n%s",
			workflow.ContractVersion, strings.Join(bad, "\n"))
	}
	return nil
}

// reportUserLanes judges every mapped workspace's user lanes through the
// one lane loader (workflow contract, "User lanes"; the daemon's start and
// every change of the workspace mapping call it). A refused user lane is
// only unavailable — a claim of a work item on it is refused, nothing else
// is — and is recorded once per lane and distinct defect set: a
// daemon-scope user_lane_refused event naming every defect, and a log
// line. A lane fixed and broken again is recorded again.
func (d *Daemon) reportUserLanes() {
	if d.cfg.WorkflowsDir == "" || d.obs == nil {
		return
	}
	d.userLaneMu.Lock()
	defer d.userLaneMu.Unlock()
	if d.userLaneRefused == nil {
		d.userLaneRefused = map[string]string{}
	}
	refused := map[string]bool{}
	record := func(ws, lane, dir string, msgs []string) {
		refused[dir] = true
		text := strings.Join(msgs, "\n")
		if d.userLaneRefused[dir] == text {
			return
		}
		d.userLaneRefused[dir] = text
		fmt.Fprintf(os.Stderr, "daemon: the user lane %s of workspace %s is refused and unavailable (the daemon runs on; a claim of a work item on it is refused):\n  %s\n", lane, ws, strings.Join(msgs, "\n  "))
		_ = d.obs.Events().Append("", observe.EvUserLaneRefused, map[string]any{"workspace": ws, "lane": lane, "dir": dir, "errors": msgs})
	}
	for _, ws := range d.plan().Workspaces() {
		set := workflow.ForWorkspace(d.cfg.WorkflowsDir, ws)
		lanes, err := set.UserLanes()
		if err != nil {
			record(ws, "", set.User, []string{fmt.Sprintf("the user lanes root %s is unreadable: %v", set.User, err)})
			continue
		}
		for _, l := range lanes {
			if len(l.Errors) == 0 {
				continue
			}
			msgs := make([]string, 0, len(l.Errors))
			for _, e := range l.Errors {
				msgs = append(msgs, e.Error())
			}
			record(ws, l.Name, l.Dir, msgs)
		}
	}
	for dir := range d.userLaneRefused {
		if !refused[dir] {
			delete(d.userLaneRefused, dir)
		}
	}
}
