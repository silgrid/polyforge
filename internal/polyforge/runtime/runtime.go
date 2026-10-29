// Package runtime implements the v1 Runtime lane: the WI lifecycle manager
// (Start / Stop / Recover / Restart), the pi-harness agent spawner with the
// real RPC envelope, the six-layer process cleanup and the recovery engine.
//
// Current behavior: docs/design/polyforge-vnext/reference.md §3.5 (session
// state), §6.2 (restart, recovery engine, cleanup) and §8 (git); decisions:
// v2-converge.md §6. The v1 records are under docs/design/polyforge-vnext/archive/.
// The Runtime talks to aihub through the shared aihubclient, to capsule git
// through the shared gitops choke point, and to the Observe lane ONLY
// through channels carrying common/protocol message types. Each work item's
// job is owned by its actor (actor.go).
package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/atomicfile"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/crashpoint"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
	"github.com/silgrid/polyforge/internal/polyforge/liveconfig"
	"github.com/silgrid/polyforge/internal/polyforge/mapping"
	"github.com/silgrid/polyforge/internal/polyforge/supervise"
	"github.com/silgrid/polyforge/internal/polyforge/workspace"
)

// Aihub is the server face Runtime needs (v1.5 session model: the
// generation-era Claim/EndGeneration are replaced by the session
// endpoints). *aihubclient.Client satisfies it.
type Aihub interface {
	SessionClaim(req api.SessionClaimRequest) (*api.SessionClaimResponse, error)
	SessionAttach(wi string, req api.SessionAttachRequest) (*api.SessionClaimResponse, error)
	SessionVerify(wi, sessionID string) (*api.SessionVerifyResponse, error)
	SessionEnd(wi string, req api.SessionEndRequest) error
	GetWorkItem(idOrSlug string) (*api.WorkItem, error)
}

// Config is the daemon-supplied runtime configuration.
type Config struct {
	// AgentStarter is the execution-engine binding — the ONLY engine seam
	// and the driver is the only engine (polyforge#106). The daemon binds
	// bridge.Start here unconditionally at assembly; tests bind
	// driver-compatible scripted starters. nil is a MISASSEMBLY, not an
	// engine choice: dispatch refuses it loudly (there is no fallback
	// engine).
	AgentStarter AgentStartFunc
	MachineID    string // registered machine identity
	// DaemonSocket is the daemon's UDS path, handed to step processes as
	// PF_DAEMON_SOCKET so their pf reaches this daemon for memory reads
	// and writes, wherever it listens.
	DaemonSocket string
	// Mapping is the workspace→projects plan of record (polyforge#113):
	// the daemon assembles it from the shared cli.toml ([workspace.
	// "<path>"] sections) and the runtime routes every capsule through it
	// — <mapped-workspace>/capsules/<project>-<seq> per the claim answer.
	// The daemon refuses to start when the mapping is empty; a recorded
	// mid-flight project that falls out of it blocks only that work item
	// at aihub (restart.go, blockUnrecoverable).
	Mapping *mapping.Plan
	// Live, when set, is the daemon's shared live configuration: the
	// mapping and claude_bin are read through it at their use sites so a
	// reload takes effect without a restart. nil (unit tests) falls back
	// to Mapping/ClaudeBin.
	Live          *liveconfig.State
	DataDir       string // state.json + external git-dirs live here
	GitRemoteBase string // e.g. https://aihub.example/git → <base>/<wi>.git
	WorkflowsDir  string // daemon checkout's workflows/ (materialization source)
	PiBin         string // harness binary (default "pi")
	// ClaudeBin is the Claude Code binary (default "claude"; cli.toml
	// claude_bin / --claude-bin), used by steps whose harness is claude.
	ClaudeBin string
	// ToolWindow is the daemon's supervision tool window Y (one tool
	// running longer than it is judged hung). A claude step's Bash tool
	// timeouts derive from it (claudeBashTimeouts: max = Y - 1m, default
	// 10m clamped to the max); <= 0 is the supervision default, 30m.
	ToolWindow time.Duration
	// HarnessRoot is the parent root of the three harness state dirs
	// (polyforge#171): <harness-root>/step-agent (the dedicated step
	// agent dir), <harness-root>/step-agent-sessions (the pi steps'
	// session store) and <harness-root>/claude-config (claude's config
	// dir: settings.json — the single credential authority layer —
	// plus projects/ and debug/). Resolved as --harness-root (cli.toml
	// [daemon] harness_root) > DataDir; the Dir() derivations and
	// stepagentdir.go hold the map.
	HarnessRoot string
	// OperatorAgentDir is the SOURCE of the dedicated step agent
	// directory's minimal set (addendum 2, owner ruling A): the
	// operator's agent dir by default; a flag/env-configured source is
	// Explicit — a missing auth.json there is a spawn-refusing error,
	// while the implicit default without file credentials is carried on
	// with a warning (the harness adapter may hold credentials
	// elsewhere).
	OperatorAgentDir         string
	OperatorAgentDirExplicit bool
	MaxRecoveries            int // consecutive recovery failures before bounded fail (default 2; C7 — the only recovery ledger)
	// EndRetryBase/EndRetryMax shape the backoff of every pending hub
	// step a work item's actor retries (a close the hub has not taken, a
	// paused record's verify, a claiming-journal replay): first retry
	// after Base, doubling up to Max, then every Max (defaults 2s / 60s).
	// EndRetryWindow bounds one chain by the time since its first retry
	// (default 24h): an aihub outage shorter than that is ridden out, a
	// longer one has a person on it who restarts the daemon.
	EndRetryBase   time.Duration
	EndRetryMax    time.Duration
	EndRetryWindow time.Duration
	// SessionRevalidateEvery is the M4 cadence for the retained PAUSED
	// records (revalidatePaused) — the records observe's monitor no
	// longer revalidates. The daemon passes the same resolved cadence
	// observe uses for running jobs; <= 0 (the unit-test default)
	// disables it.
	SessionRevalidateEvery time.Duration
	// EventTap, when non-nil, receives runtime-sourced diagnostic events
	// (kind "push_failed" on a seal push that failed after its retries)
	// so they land in the daemon's durable events log next to the
	// observe lane's own entries. The daemon binds it to the observe
	// EventLog; nil (tests) disables it.
	EventTap func(wi, kind string, body map[string]any)
	// ReleaseMonitor, when non-nil, stops the work item's observe monitor
	// and returns once it is gone — its in-flight capsule push drained,
	// its queued ones dropped. A terminal close calls it before its seal
	// push, so no monitor push can run after the close's SessionEnd ends
	// the session (such a push was refused as an
	// "Authentication failed" GIT-500); the seal push is the final
	// carrier. The daemon binds Observe.Release; nil (tests) = no monitor.
	ReleaseMonitor func(wi string)
	// EnvSecrets are configured secret values (machine api key, push
	// credential) the spawned agents' environment must never carry, even
	// under a renamed variable.
	EnvSecrets []string
	// RecallTopK bounds the F6-A claim-time memory snapshot: how many
	// semantic hits over the goal+content query land in the capsule's
	// runtime-state/recalled-memories.json (default 10, capped at 50).
	RecallTopK int
	// MemoryRecall, when non-nil, is the F6-A claim-time semantic-recall
	// seam: materialize calls it with the claim's goal+content, the work
	// item's own project (audit B5: the snapshot is scoped to that project,
	// not to everything the daemon credential can read) and the top-K, and
	// writes the answer as a static snapshot for the agent.
	// The daemon binds it to aihub's memory search face (the machine
	// credential stays with the daemon — agents hold no aihub credential
	// of any kind); nil disables the snapshot entirely (slim installs,
	// most unit tests).
	MemoryRecall func(query, project string, topK int) ([]RecalledMemory, error)
}

func (c *Config) fill() {
	if c.OperatorAgentDir == "" {
		c.OperatorAgentDir = OperatorAgentDirDefault()
		c.OperatorAgentDirExplicit = false
	}
	// #171/#159: step processes run with their own session store
	// (PI_CODING_AGENT_SESSION_DIR under the harness root), so the
	// live-transcript locator's root follows them there by default. An
	// empty-DataDir bare Config (unit tests) keeps no redirect.
	if c.HarnessRoot == "" && c.DataDir != "" {
		c.HarnessRoot = c.DataDir
	}
	if c.MaxRecoveries <= 0 {
		c.MaxRecoveries = 2
	}
	if c.RecallTopK <= 0 {
		c.RecallTopK = defaultRecallTopK
	}
	if c.EndRetryBase <= 0 {
		c.EndRetryBase = 2 * time.Second
	}
	if c.EndRetryMax <= 0 {
		c.EndRetryMax = 60 * time.Second
	}
	if c.EndRetryWindow <= 0 {
		c.EndRetryWindow = 24 * time.Hour
	}
}

// Runtime is the per-daemon WI lifecycle manager.
type Runtime struct {
	cfg     Config
	hub     Aihub
	spawner Spawner
	toOB    *supervise.Queue[protocol.RTMsg]
	fromOB  *supervise.Queue[protocol.OBMsg]
	// ctx is the actors' context: the shutdown cancels it (Close, or the
	// end of Run's or Restart's context) — a cancelled actor starts no
	// agent and schedules no retry.
	ctx    context.Context
	cancel context.CancelFunc

	mu   sync.Mutex
	jobs map[string]*job
	// boxes (under mu) are the work items' mailboxes (actor.go), by
	// canonical id.
	boxes map[string]*mailbox
	// aliases maps every NON-canonical id form the runtime has ever been
	// asked to run (a "<project>#<seq>" slug, most often) to the
	// canonical work-item id the claim resolved it to. In-memory only:
	// Resolve's unknown-input answer is "", and the daemon's resolver
	// falls back to aihub for those.
	aliases map[string]string
	// dropped (under mu) retains the scope facts — the origin chat id
	// and the project — of every job record the table dropped (dropJob):
	// a terminal close moves the job to DONE and drops it, and the scoped
	// decision wake (the daemon's wakeScope resolvers through
	// ScopeOf) must still resolve both facts after it — the terminal
	// decision is exactly the one that fires it. In-memory only; the
	// facts are the dropped record's own point-in-time view.
	dropped map[string]wiScope
	// manualMu guards the daemon-local manual-models.json (manualmodels.go).
	manualMu sync.Mutex
	// claimMu serializes the claiming journal's read-modify-writes: its
	// writers are different work items' actors.
	claimMu sync.Mutex

	statePath string
	// persistGen (under mu) numbers persist's snapshots in the order they
	// were taken; persistMu serializes the writes, and a write whose
	// snapshot is older than the one already on disk (persistDone) is
	// skipped — its change is part of that newer snapshot — so an older
	// snapshot never lands last. persistErr is the outcome of the write
	// that landed persistDone.
	persistGen  uint64
	persistMu   sync.Mutex
	persistDone uint64
	persistErr  error
}

// AgentStartInput carries the exported fields the execution engine
// starter needs (the bridge cannot see the runtime's unexported job).
type AgentStartInput struct {
	WI         string
	Project    string
	CapsuleDir string
	BundleName string
	// SessionHandle is the owning session's PUBLIC fingerprint (B3); it
	// names the dispatches' spawn tokens and capsule transcripts.
	SessionHandle string
	SpawnN        int64
	// Compose is the dispatch-time candidate composition (polyforge#33):
	// the driver calls it at every dispatch, so a yaml or bundle edit
	// governs the next dispatch (the manual `pf submit --models` list stays
	// frozen). Every candidate is `<harness>:<model>` (polyforge#175).
	Compose func() (map[string][]string, error)
	// InterruptedCause is the recovery lane's cause string when THIS
	// derivation is a recovery re-derivation ("" on a fresh claim): the
	// driver archives the interrupted dispatch's infra attempt from it.
	InterruptedCause string
	// Spawner is the runtime's process spawner (production: pi).
	Spawner Spawner
	// StepEnded is the driver's step-end hook: the task-repo WIP push of
	// the step that just ended (SaveTaskWIP, bounded by stepWIPBound).
	StepEnded func(context.Context)
}

// AgentStartFunc starts the execution engine for a claimed work item
// and returns a supervision handle. The daemon binds the Go driver
// bridge here unconditionally; nil is refused loudly at dispatch —
// there is no fallback engine (polyforge#106).
type AgentStartFunc func(ctx context.Context, in AgentStartInput) (AgentHandle, error)

// job is one WI's runtime bookkeeping. Only its work item's actor writes
// it; mu guards the fields the read faces (Info, List, persist) see.
type job struct {
	wi      string
	project string
	// slug is the work item's human-facing name ("<project>#<seq>") from
	// the claim answer — display only; every key and path stays on wi,
	// except the capsule directory name (api.PathName).
	slug string
	// sessionID is the v1.5 SECRET (B3): 64-hex minted at claim, the git
	// push password. It never reaches the capsule, agent env, argv or
	// any log/event surface — only state.json and the one-shot askpass.
	sessionID string
	// sessionFingerprint is the PUBLIC handle (api.SessionFingerprint).
	sessionFingerprint string
	state              JobState
	stopReason         string // sealed-in reason while SEALING (restart can finish the stop)
	// sealedPush records that the close's seal push already reached the
	// hub: a resumed close then skips the push (after a STALE_SESSION the
	// credential is dead). Persisted with the record.
	sealedPush bool
	capsuleDir string
	git        gitops.Opts // Username=wi, Secret=session_id (reference.md §3.4)
	transcript string
	bundleName string
	// origin is how the job was submitted (nil = unknown).
	origin *Origin
	// item is the claim answer's work-item snapshot (the F1 materializer
	// input). In-memory only: the durable copy is the capsule's
	// runtime-state/work-item.json, which recovery re-derives from aihub
	// (GetWorkItem) when a capsule predates it.
	item *api.WorkItem
	// repos is the claim-served task-repo inventory (api.Claim.Repos):
	// materialize clones each entry into repos/<name> before the agent
	// starts. In-memory only — recovery never re-materializes.
	repos []api.ProjectRepo
	// sealedScene is the quarantined capsule of the blocked seal this
	// claim continues on the sealing machine (verifySealedHead): its
	// task repos are restored from there, not from aihub's carriers.
	// Claim lane only; "" otherwise.
	sealedScene string

	agent          AgentHandle
	agentPID       int
	agentStartedAt string

	spawnN       int
	recoverFails int // consecutive recovery failures; reset on an accepted step result (C7)
	// acceptedAtRelaunch is the accepted-result count (execution state)
	// the last relaunch started from — the watermark the next recovery
	// entry compares against to tell real progress from another failure.
	acceptedAtRelaunch int64
	// lastRelaunchRecovery: the job's live relaunch was a RECOVERY's (not
	// the original claim's) — the mark that lets the ledger count a
	// relaunch that stabilized nothing while never counting the original
	// claim's own death as a recovery failure.
	lastRelaunchRecovery bool
	// cleanPause mirrors the hub's operator-pause flag: latched true by
	// the pause close (Finding 2) so the retained Paused record carries
	// the same marker the hub does; false everywhere else.
	cleanPause bool
	// endNote/endHead: the note and capsule head the pending close
	// carries (an end_pending job's SessionEnd(blocked), an interrupted
	// seal's cause-unknown note).
	endNote string
	endHead string
	// endPushGaveUp: the end_pending chain push met a PERMANENT rejection
	// (hook refusal, non-fast-forward, size limit), or there is nothing
	// to push (an abandoned claim) — the close goes out without it (the
	// commits stay in the local capsule). Persisted.
	endPushGaveUp bool
	// sealPushFailNoted: this SEALING series already raised its
	// push_failed event (one per series, not one per seal retry).
	sealPushFailNoted bool
	// claimCancel cancels a CLAIMING job's clone/materialize; nil once
	// the claim settles. claimClose is the close an operator requested
	// meanwhile (Stop). Both under mu.
	claimCancel context.CancelFunc
	claimClose  *claimClose
	// retryIn is the backoff of the actor's next retry (retry), retryN
	// the retries of the current chain, retrySince the chain's start
	// (its bound is EndRetryWindow from there) and retryPending whether
	// one is already on a timer (retryOutstanding) — the mark that lets a
	// reconcile tell the backoff an operation just scheduled from a
	// settled episode. retryExhausted: the last chain hit its bound and
	// nothing retries the record (the operator's doors say so); a newly
	// scheduled retry clears it.
	retryIn        time.Duration
	retryN         int
	retrySince     time.Time
	retryPending   bool
	retryExhausted bool
	// closePark is the close (seal, pause or blocked verdict) parked on a
	// driver Run that outlived its bounded teardown (parkClose): nothing
	// writes the capsule until that Run exits, then the park's own waiter
	// finishes the close. nil when no close is parked; a later park
	// replaces an earlier one (its waiter then stands down).
	closePark *closePark
	// verified: the hub confirmed, since this process holds the job, that
	// its session holds the work item (the claim, or a matching verify). A
	// record restored at restart starts unverified: nothing pushes or ends
	// under it before a verify (fence).
	verified bool
	mu       sync.Mutex
}

// claimClose is an operator close requested while the claim was still
// materializing: the claim settles it (closeClaim) and records err; the
// Stop that asked reads it once its own message runs.
type claimClose struct {
	reason  string
	settled bool
	err     error
}

// New builds a Runtime. toOB receives RT→Observe messages, fromOB carries
// Observe→Runtime messages; both sides speak only common/protocol types.
func New(cfg Config, hub Aihub, spawner Spawner, toOB *supervise.Queue[protocol.RTMsg], fromOB *supervise.Queue[protocol.OBMsg]) *Runtime {
	cfg.fill()
	ctx, cancel := context.WithCancel(context.Background())
	return &Runtime{
		cfg:       cfg,
		hub:       hub,
		spawner:   spawner,
		toOB:      toOB,
		fromOB:    fromOB,
		ctx:       ctx,
		cancel:    cancel,
		jobs:      map[string]*job{},
		boxes:     map[string]*mailbox{},
		aliases:   map[string]string{},
		dropped:   map[string]wiScope{},
		statePath: filepath.Join(cfg.DataDir, "runtime-state.json"),
	}
}

// DefaultSpawner returns the production spawner: each derivation goes
// to its step harness's spawner (pi or claude, polyforge#159). It takes
// the runtime config BY POINTER: the spawners read its fields at spawn
// time from the shared config, so any field bound after assembly is seen.
func DefaultSpawner(cfg *Config) Spawner {
	return &harnessSpawner{pi: newPiSpawner(cfg), claude: &claudeSpawner{cfg: cfg}}
}

// Run is the OB-message loop: it hands every Observe message to its work
// item's actor until ctx ends, and then cancels the actors' context. It
// also drives the retained paused records' session revalidation
// (revalidatePaused) at cfg.SessionRevalidateEvery.
func (r *Runtime) Run(ctx context.Context) error {
	defer r.cancel()
	var tick <-chan time.Time
	if every := r.cfg.SessionRevalidateEvery; every > 0 {
		t := time.NewTicker(every)
		defer t.Stop()
		tick = t.C
	}
	for {
		for {
			msg, ok := r.fromOB.Pop()
			if !ok {
				break
			}
			r.post(msg.WorkItem, func() { r.applyOB(msg) })
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-r.fromOB.Notify():
		case <-tick:
			r.revalidatePaused()
		}
	}
}

// applyOB fences one OB message and runs what it calls for (wi's actor).
// OBNeedsRecovery is fenced on the derivation counter (§二: spawn_n ≠
// current ⇒ dropped) and acts on a RUNNING job only. OBSuperseded is a
// SESSION-level verdict (v1.5 Finding 1(a)): it bypasses the derivation
// fence — a verify in flight while a recovery bumps spawn N→N+1 must not
// be lost — and is fenced on the session fingerprint instead (NEW-1: a
// stale message never kills a successor job of the same WI). A CLAIMING
// job has no monitor, so any message for it is stale.
func (r *Runtime) applyOB(msg protocol.OBMsg) {
	j := r.jobOf(msg.WorkItem)
	if j == nil || r.ctx.Err() != nil {
		return // after the shutdown nothing recovers: the record waits for the next start
	}
	j.mu.Lock()
	state, spawn, fp := j.state, j.spawnN, j.sessionFingerprint
	j.mu.Unlock()
	switch msg.Kind {
	case protocol.OBSuperseded:
		if state == StateClaiming || state == StateDone || (msg.SessionFP != "" && msg.SessionFP != fp) {
			return
		}
		_ = r.supersede(j, "session_superseded", msg.Detail, "") // a parked release finishes from its waiter
		r.release(j.wi)
	case protocol.OBNeedsRecovery:
		if state == StateRunning && msg.SpawnN == spawn {
			r.recoverCore(r.ctx, j, msg.Reason, msg.Detail)
		}
	}
}

// send queues one RT→Observe message. The queue is unbounded and never
// drops: lifecycle facts must reach Observe (v2-converge §2.2), and the
// sender never blocks.
func (r *Runtime) send(msg protocol.RTMsg) { r.toOB.Push(msg) }

// Close cancels the actors' context (the shutdown): no agent starts and
// no retry runs after it. Idempotent.
func (r *Runtime) Close() { r.cancel() }

// stopped reports whether the shutdown (or the caller's ctx) ended: an
// actor then starts no agent.
func (r *Runtime) stopped(ctx context.Context) bool {
	return ctx.Err() != nil || r.ctx.Err() != nil
}

func (r *Runtime) jobOf(wi string) *job {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.jobs[wi]
}

// persist rewrites state.json from the live job set. A CLAIMING
// placeholder is never written: its durable form is the claiming journal
// (B-8), which the next start replays.
//
// The snapshot is taken and numbered under mu (no I/O there); the write
// runs under persistMu alone. A caller whose snapshot is older than the
// one already written returns that write's outcome without writing: it
// changed the job set before the newer snapshot was taken, so its change
// is on disk.
func (r *Runtime) persist() error {
	r.mu.Lock()
	r.persistGen++
	gen := r.persistGen
	recs := make([]*jobRecord, 0, len(r.jobs))
	for _, j := range r.jobs {
		if st, _ := j.snapshot(); st == StateClaiming {
			continue
		}
		recs = append(recs, j.record())
	}
	r.mu.Unlock()
	blob, err := json.MarshalIndent(&stateFile{Version: 1, Jobs: recs}, "", "  ")
	if persistSnapped != nil {
		persistSnapped(gen)
	}
	r.persistMu.Lock()
	defer r.persistMu.Unlock()
	if gen <= r.persistDone {
		return r.persistErr
	}
	if err == nil {
		err = writeStateFn(r.statePath, blob, stateFilePerm)
	}
	r.persistDone, r.persistErr = gen, err
	return err
}

// persistLog is persist whose failure is only logged; it reports
// whether the write landed.
func (r *Runtime) persistLog(wi string) bool {
	if err := r.persist(); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: state persist failed: %v\n", wi, err)
		return false
	}
	return true
}

// AdoptOrigin rewrites the persisted job record's origin chat to the
// adopting chat — the same field a resume rewrites — so the adopter is
// the WI's origin chat in every sense (`pf watch adopt <wi>`;
// the daemon's adopt route calls it after the chat-subs surgery). The
// rewrite mutates under the job's own mutex and persists after it is
// released (the existing record()/info() pattern: no I/O under the job
// mutex, none under the job-table lock beyond the snapshot); the record
// pointer is replaced, not mutated in place, so a reader that already
// took it keeps its point-in-time view. Origin.Owner is untouched —
// owner routing keys on the owner, never the chat. A record with no
// origin gains one (kind chat). The chat id is a notification label,
// never a credential.
func (r *Runtime) AdoptOrigin(wi, chatID string) error {
	j := r.resolveJob(wi)
	if j == nil {
		return fmt.Errorf("runtime: %s: no local job to adopt", wi)
	}
	j.mu.Lock()
	if j.origin == nil {
		j.origin = &Origin{Kind: OriginChat, ChatID: chatID}
	} else {
		o := *j.origin
		o.Kind, o.ChatID = OriginChat, chatID
		j.origin = &o
	}
	j.mu.Unlock()
	return r.persist()
}

// writeStateFn is persist's file write; persistSnapped, when set, runs
// between a snapshot and its write (seams for the write-ordering tests).
var (
	writeStateFn   = atomicfile.Write
	persistSnapped func(gen uint64)
)

// putJob enters j in the job table (replacing any job of its work item).
func (r *Runtime) putJob(j *job) {
	r.mu.Lock()
	r.jobs[j.wi] = j
	r.mu.Unlock()
}

// dropJob removes j (only if it is still its work item's job) and the
// aliases naming it, retaining the record's scope facts (dropped): the
// wake-target scope survives the drop.
func (r *Runtime) dropJob(j *job) {
	// the record's scope facts first, under the job's own mutex alone
	// (persist's r.mu → j.mu order never nests the other way): origin
	// chat id and project, the dropped record's point-in-time view.
	j.mu.Lock()
	sc := wiScope{project: j.project}
	if j.origin != nil {
		sc.origin = j.origin.ChatID
	}
	j.mu.Unlock()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.jobs[j.wi] != j {
		return
	}
	r.dropped[j.wi] = sc
	delete(r.jobs, j.wi)
	for a, id := range r.aliases {
		if id == j.wi {
			delete(r.aliases, a)
		}
	}
}

// NoteAlias records that alias (a slug or any other non-canonical id
// form) names the canonical work item — the daemon registers the
// operator's submit-time spelling so later route lookups by the same
// spelling resolve locally without an aihub round-trip.
func (r *Runtime) NoteAlias(alias, canonical string) {
	if alias == "" || canonical == "" || alias == canonical {
		return
	}
	r.mu.Lock()
	r.aliases[alias] = canonical
	r.mu.Unlock()
}

// Resolve maps any id form this runtime knows — a live job key (already
// canonical) or a registered alias — to the canonical work-item id;
// "" when the form is unknown to this daemon (the daemon's resolver
// then falls back to aihub's id-or-slug endpoint).
func (r *Runtime) Resolve(id string) string {
	if id == "" {
		return ""
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.jobs[id]; ok {
		return id // a job key is canonical by construction (F2)
	}
	return r.aliases[id]
}

// CanonicalID resolves any WI spelling (id or slug) to the canonical ID.
func (r *Runtime) CanonicalID(wi string) string {
	if id := r.Resolve(wi); id != "" {
		return id
	}
	return wi
}

// plan answers the live mapping when the daemon bound one, else the
// startup mapping (never nil).
func (r *Runtime) plan() *mapping.Plan {
	if r.cfg.Live != nil {
		return r.cfg.Live.Mapping()
	}
	if r.cfg.Mapping == nil {
		return &mapping.Plan{}
	}
	return r.cfg.Mapping
}

// liveClaudeBin answers the live claude binary when the daemon bound a
// live state, else the startup value.
func (c *Config) liveClaudeBin() string {
	if c.Live != nil {
		return c.Live.ClaudeBin()
	}
	return c.ClaudeBin
}

// capsuleDirForProject resolves the capsule base for one project through
// the inverse mapping (polyforge#113): <workspace>/capsules/<name>, name
// being the work item's api.PathName ("<project>-<seq>"). An unmapped
// project answers the typed *mapping.UnmappedError.
func (r *Runtime) capsuleDirForProject(project, name string) (string, error) {
	ws, err := r.plan().WorkspaceFor(project)
	if err != nil {
		return "", err
	}
	return filepath.Join(ws, "capsules", name), nil
}

func (r *Runtime) gitDirFor(wi string) string {
	return filepath.Join(r.cfg.DataDir, "git", wi+".git")
}

func (r *Runtime) remoteURLFor(wi string) string {
	if r.cfg.GitRemoteBase == "" {
		return ""
	}
	return strings.TrimSuffix(r.cfg.GitRemoteBase, "/") + "/" + wi + ".git"
}

// sessionPathFor is the capsule-side session copy path every derivation
// declares in its spawn journal (session/session-<fence-token>.jsonl): a
// fresh derivation gets its own token-named file, so a re-derivation
// never clobbers a dead derivation's transcript. The token is the session
// fingerprint (+#N after re-derivations) — public, never the secret.
func sessionPathFor(capsuleDir, token string) string {
	return filepath.Join(capsuleDir, capsule.DirSession, "session-"+token+".jsonl")
}

// mintSessionID mints the v1.5 SECRET (reference.md §3.1): 32 crypto-random
// bytes, hex — 64 chars. A CSPRNG failure is FATAL to the start: a
// session secret must never fall back to a clock (a guessable credential,
// the exact failure B3 exists to prevent).
func mintSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("runtime: session secret mint failed: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// ---------------------------------------------------------------------------
// Start (§一 Runtime): claim → clone/init capsule → materialize → spawn →
// RTStarted.

// StartOpts is what the daemon's start carries beyond the id: the
// origin the job records (nil: none) and the manual `pf submit --models`
// list composed as the top precedence level (nil: the one recorded for
// a re-claim, if any).
type StartOpts struct {
	Origin *Origin
	Models []string
}

// Start claims wi and runs it to a live agent, returning once the claim
// settled. The read-only work-item probe and the local pre-claim checks
// (workspace yaml, bundle) run on the caller's goroutine — a refusal
// there claims nothing and the work item stays queued; the claim itself,
// the capsule clone, the materialize and the agent start are one message
// of the canonical id's actor (claim), so two starts of one work item
// under different spellings meet in one mailbox and the second finds the
// first's job.
func (r *Runtime) Start(ctx context.Context, wi string, opts StartOpts) error {
	if r.stopped(ctx) {
		return claimDeferredError(wi)
	}
	if j := r.resolveJob(wi); j != nil {
		// a paused record is re-claimable, and a restart's journal
		// placeholder (CLAIMING, no clone under way) may be dropped by its
		// replay — the claim re-checks once it is its turn
		j.mu.Lock()
		st, cloning := j.state, j.claimCancel != nil
		j.mu.Unlock()
		if st != StatePaused && (st != StateClaiming || cloning) {
			return alreadyActive(j.wi, st)
		}
	}
	probe, err := r.probeWorkItem(wi)
	if err != nil {
		return err
	}
	// Pre-claim placement + workspace-yaml + bundle checks (polyforge#113,
	// #79, #175): a bad local file must not block the team's task — the
	// work item stays queued at aihub so another machine may take it.
	if _, err := r.LoadWorkspace(probe.ProjectName); err != nil {
		return err
	}
	if probe.BundleName != nil {
		if err := r.CheckBundle(wi, *probe.BundleName); err != nil {
			return err
		}
	}
	err = r.call(ctx, probe.ID, func() error { return r.claim(ctx, wi, probe.ID, opts) })
	if errors.Is(err, errAbandoned) {
		return claimDeferredError(wi) // the shutdown came while it waited its turn
	}
	return err
}

// probeWorkItem is Start's read-only work-item probe: the canonical id
// and the project the claim is placed by. The hub's own refusal is
// final, a transport loss or a hub 5xx the typed transient the caller
// retries. An answer without an id or a project refuses too.
func (r *Runtime) probeWorkItem(wi string) (*api.WorkItem, error) {
	probe, err := r.hub.GetWorkItem(wi)
	if err != nil {
		if claimRefused(err) {
			return nil, fmt.Errorf("runtime: %s: work-item probe refused (not claiming): %w", wi, err)
		}
		return nil, &api.Error{Code: api.ErrDaemonHubUnavailable,
			Detail: fmt.Sprintf("%s: work-item probe failed (not claiming; retry later): %v", wi, err)}
	}
	if probe == nil || probe.ProjectName == "" {
		return nil, fmt.Errorf("runtime: %s: work-item probe answered no project (not claiming)", wi)
	}
	if probe.ID == "" {
		return nil, fmt.Errorf("runtime: %s: work-item probe answered no id (not claiming)", wi)
	}
	return probe, nil
}

// claim is one start of id, run to its end by id's actor: the journaled
// claim (B1), the CLAIMING placeholder, the capsule scene and the agent.
// spelling is the id form the start was asked for (registered as an
// alias). A retained paused record (Finding 2) or a restart's CLAIMING
// placeholder of the claiming journal may sit in the table; any other
// job refuses.
//
// B1: the session secret is journaled durably BEFORE the claim call. A journal entry left by an earlier attempt is
// reused when its session still holds the work item (the claim is then a
// D4 replay that hands the same answer back), so a claim whose answer was
// lost is adopted instead of orphaned.
func (r *Runtime) claim(ctx context.Context, spelling, id string, opts StartOpts) error {
	if ex := r.jobOf(id); ex != nil {
		if st, _ := ex.snapshot(); st != StatePaused && st != StateClaiming {
			return alreadyActive(id, st)
		}
	}
	if r.stopped(ctx) {
		return claimDeferredError(id)
	}
	sessionID, replay, err := r.claimIntent(id)
	if err != nil {
		return err
	}
	if err := r.noteClaiming(id, sessionID, opts.Models); err != nil {
		return fmt.Errorf("runtime: %s: claiming journal write failed (refusing to claim): %w", id, err)
	}
	crashpoint.At("claim.before-answer")
	resp, err := r.hub.SessionClaim(api.SessionClaimRequest{
		WorkItemID: id,
		SessionID:  sessionID,
		MachineID:  r.cfg.MachineID,
	})
	bare := &job{wi: id, sessionID: sessionID}
	if err != nil {
		if !claimRefused(err) {
			// Transport loss, timeout or a hub 5xx: the claim MAY have
			// been committed with the answer lost. The journal entry (the
			// only copy of the secret) is KEPT; the retry replays it.
			return &api.Error{Code: api.ErrDaemonHubUnavailable,
				Detail: fmt.Sprintf("%s: claim answer lost (claiming journal kept; the retry replays the same claim): %v", id, err)}
		}
		if replay {
			// a replay of a claim whose verify matched: the session is
			// open at the hub under the secret we hold — end it blocked
			return r.abandonClaim(bare, err, "the replayed claim of an open session was refused: "+err.Error())
		}
		// the hub refused: the claim never opened, nothing can dangle
		r.clearClaimingSession(id, sessionID)
		return err
	}
	if cid := claimAnswerID(resp); cid != id {
		cerr := fmt.Errorf("runtime: %s: the claim answer names work item %s", id, cid)
		return r.abandonClaim(bare, cerr, cerr.Error())
	}
	crashpoint.At("claim.after-answer")
	j, cctx, err := r.claimPlaceholder(ctx, spelling, sessionID, resp, opts)
	if err != nil {
		return err
	}
	note, sceneErr := r.claimScene(cctx, j, resp.Claim)
	return r.finishClaim(ctx, j, sceneErr, note)
}

// alreadyActive is the DAEMON-409 refusal of a start of a work item this
// daemon already holds.
func alreadyActive(id string, st JobState) error {
	return &api.Error{Code: api.ErrDaemonConflict, Detail: fmt.Sprintf("%s already active (state %s)", id, st)}
}

// claimRefused reports whether a hub error is the hub's own typed
// refusal (a 4xx envelope). Anything else — a transport error, a timeout,
// a 5xx — leaves the outcome unknown.
func claimRefused(err error) bool {
	var ae *api.Error
	return errors.As(err, &ae) && ae.HTTPStatus() < 500
}

// hubUnavailable reports the typed transient an actor retries.
func hubUnavailable(err error) bool {
	var ae *api.Error
	return errors.As(err, &ae) && ae.Code == api.ErrDaemonHubUnavailable
}

// claimIntent answers the session secret this start claims with. With
// no journal entry for id it mints a fresh one. An entry left by an
// earlier attempt is checked first: its session holds the WI → reuse the
// secret (it replays the same claim, D4); it does not → drop the entry
// and mint fresh (aihub would refuse that secret STALE_SESSION); verify
// unreachable → the typed transient, entry kept.
func (r *Runtime) claimIntent(id string) (sessionID string, replay bool, err error) {
	if rec := r.loadClaiming()[id]; rec != nil && rec.SessionID != "" {
		v, verr := r.hub.SessionVerify(rec.WorkItemID, rec.SessionID)
		switch {
		case verr != nil && !claimRefused(verr):
			return "", false, &api.Error{Code: api.ErrDaemonHubUnavailable,
				Detail: fmt.Sprintf("%s: verify of the journaled claim failed (claiming journal kept; retry later): %v", id, verr)}
		case verr == nil && v.Matches:
			return rec.SessionID, true, nil
		}
		r.clearClaimingSession(id, rec.SessionID)
	}
	sessionID, err = mintSessionID()
	return sessionID, false, err
}

// claimPlaceholder is the post-claim bookkeeping of one OPEN claim: the
// replaced placeholder or paused record, the manual models, the job, its
// capsule placement and the per-step compose — then the job enters the
// table CLAIMING (B-8) with the cancellable context its clone and
// materialize run under. Every failure after the claim is compensated
// with SessionEnd(blocked) (abandonClaim, the D5 rule).
//
// The job and every machine-side path — external git-dir, push remote,
// spawn journal — are keyed by the CANONICAL work-item id the claim answer
// carries (F2); the capsule directory alone is named for humans, after the
// answer's slug in its path-safe form "<project>-<seq>" (api.PathName). A
// raw slug never reaches any path ("#" is the URL fragment separator).
func (r *Runtime) claimPlaceholder(ctx context.Context, spelling, sessionID string, resp *api.SessionClaimResponse, opts StartOpts) (*job, context.Context, error) {
	c := resp.Claim
	id := claimAnswerID(resp)
	fp := api.SessionFingerprint(sessionID)
	bare := &job{wi: id, sessionID: sessionID}
	if c.SessionFingerprint != "" && c.SessionFingerprint != fp {
		// the server computed a different fingerprint for our secret: the
		// answer belongs to another session. Refuse rather than adopt someone else's claim.
		return nil, nil, fmt.Errorf("runtime: %s: claim answer fingerprint %s does not match this session (%s)", id, c.SessionFingerprint, fp)
	}
	if ex := r.jobOf(id); ex != nil {
		// a retained paused record or a journal placeholder: the new
		// claim owns the WI (prepareCapsule quarantines the old scene)
		r.dropJob(ex)
		r.persistLog(id)
	}
	r.NoteAlias(spelling, id)
	// The workspace yaml governs this claim (loaded fresh — an edit
	// between the pre-check and the answer, or a journal replay over a
	// changed config, ends the open claim blocked).
	_, werr := r.LoadWorkspace(resp.WorkItem.ProjectName)
	if werr != nil {
		note := werr.Error()
		var ae *api.Error
		if errors.As(werr, &ae) && ae.Detail != "" {
			note = ae.Detail
		}
		return nil, nil, r.abandonClaim(bare, werr, note)
	}
	// The manual `pf submit --models` list is daemon-local: a submit that
	// carries one records it under the daemon's data dir; a re-claim
	// (pf resume of a blocked WI) carries none and reads it back. Never
	// from the capsule — the agent writes there.
	manual := opts.Models
	if len(manual) > 0 {
		r.saveManualModels(id, manual)
	} else {
		manual = r.loadManualModels(id)
	}
	j := &job{
		wi:                 id,
		project:            resp.WorkItem.ProjectName,
		slug:               resp.WorkItem.Slug,
		sessionID:          sessionID,
		sessionFingerprint: fp,
		state:              StateClaiming,
		verified:           true,
		git: gitops.Opts{
			WorkItemID: id,
			GitDir:     r.gitDirFor(id),
			RemoteURL:  r.remoteURLFor(id),
			Username:   id,
			Secret:     sessionID, // v1.5: the session secret IS the push password
			EventSink:  r.gitEventSink(id),
			// once-per-repo carrier_push_failed state: per job, so a
			// re-claim (a new job) alerts afresh.
			CarrierAlerts: &gitops.CarrierAlerts{},
		},
		bundleName: c.BundleName,
		repos:      c.Repos,
		item:       &resp.WorkItem,
		origin:     opts.Origin,
	}
	// polyforge#113: the capsule routes by the claim answer's project
	// through the inverse mapping; an unmapped answer touches no capsule.
	capDir, rerr := r.capsuleDirForProject(j.project, api.PathName(j.slug, id))
	if rerr != nil {
		err2 := unmappedProjectError(j.project)
		return nil, nil, r.abandonClaim(j, err2, err2.Detail)
	}
	j.capsuleDir = capDir
	j.git.CapsuleDir = capDir
	j.transcript = sessionPathFor(j.capsuleDir, fp)
	// polyforge#79/#175: a claim over an unreadable or non-`<harness>:<model>`
	// bundle ends blocked; the candidates themselves are composed at every
	// dispatch (polyforge#33), never snapshotted here.
	if _, ferr := r.readBundleFrontmatter(j.bundleName); ferr != nil {
		return nil, nil, r.abandonClaim(j, ferr, ferr.Error())
	}
	cctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(r.ctx, cancel) // the shutdown cancels the clone too
	j.claimCancel = func() { stop(); cancel() }
	r.putJob(j)
	return j, cctx, nil
}

// claimScene lays the claimed capsule out: a fresh clone, the
// cross-daemon sealed-head guard and the materialize (task-repo clones
// included). A failure
// answers the error and the close note.
func (r *Runtime) claimScene(ctx context.Context, j *job, c api.SessionClaim) (note string, err error) {
	if err := ctx.Err(); err != nil {
		return err.Error(), err
	}
	if err := r.prepareCapsule(ctx, j); err != nil {
		return "prepare capsule: " + err.Error(), err
	}
	if err := r.verifySealedHead(ctx, j, c); err != nil {
		return err.Error(), err
	}
	if err := r.materialize(ctx, j); err != nil {
		// A typed task-repo clone failure (DAEMON-507) keeps its detail as
		// the close note; every other materialization failure names itself.
		note := err.Error()
		var ae *api.Error
		if errors.As(err, &ae) && ae.Code == api.ErrDaemonSpawnFail && ae.Detail != "" {
			note = ae.Detail
		}
		return note, err
	}
	return "", nil
}

// finishClaim settles a CLAIMING job. In order: a close the operator
// requested meanwhile is honoured (closeClaim); a shutdown DEFERS the
// claim — the placeholder goes, the claiming-journal entry stays and the
// next daemon start replays it, so a cancelled clone never turns the work
// item blocked; a scene failure is the D5 blocked compensation; otherwise
// the engine starts and the job turns RUNNING.
func (r *Runtime) finishClaim(ctx context.Context, j *job, sceneErr error, note string) error {
	j.mu.Lock()
	cancel, cc := j.claimCancel, j.claimClose
	j.claimCancel, j.claimClose = nil, nil
	j.mu.Unlock()
	cancel()
	if cc != nil {
		// the close's own outcome goes to the Stop that asked for it; the
		// start answers the settled close either way
		cc.err, cc.settled = r.closeClaim(j, cc.reason), true
		return fmt.Errorf("%w: %s (%s)", ErrClaimClosed, j.wi, cc.reason)
	}
	if r.stopped(ctx) {
		r.dropJob(j)
		fmt.Fprintf(os.Stderr, "runtime: %s: claim deferred by the shutdown (claiming journal kept; the next start replays it)\n", j.wi)
		return claimDeferredError(j.wi)
	}
	if sceneErr != nil {
		r.dropJob(j)
		return r.abandonClaim(j, sceneErr, note)
	}
	if r.cfg.AgentStarter == nil {
		err := fmt.Errorf("runtime misassembly: no execution engine bound (Config.AgentStarter is nil) — the legacy in-package WI-Agent engine was deleted (polyforge#106); the daemon binds the driver bridge starter unconditionally at assembly")
		r.dropJob(j)
		return r.abandonClaim(j, err, err.Error())
	}
	// The derivation advances the counter so RTStarted carries SpawnN ≥ 1
	// (observe refuses to monitor SpawnN ≤ 0).
	_, err := r.startEngine(ctx, j, "")
	if errors.Is(err, errEngineStopped) {
		// nothing runs behind the shutdown: the claim is deferred like
		// any the shutdown caught
		r.dropJob(j)
		return claimDeferredError(j.wi)
	}
	if err != nil {
		r.dropJob(j)
		return r.abandonClaim(j, err, "driver start: "+err.Error())
	}
	if err := j.transition(StateRunning); err != nil {
		return err // unreachable: only the claim leaves CLAIMING
	}
	if r.persistLog(j.wi) {
		r.clearClaiming(j.wi) // the start settled: the journal entry has done its job
	}
	// the secret rides in-process to the observe lane only (its M4
	// session revalidation needs it); never a log, never the capsule
	j.mu.Lock()
	spawnN, pid := j.spawnN, j.agentPID
	j.mu.Unlock()
	r.send(protocol.RTMsg{Kind: protocol.RTStarted, SpawnN: spawnN, WorkItem: j.wi, AgentPID: pid, SessionID: j.sessionID})
	return nil
}

// unmappedProjectError is the typed DAEMON-412 placement refusal.
func unmappedProjectError(project string) *api.Error {
	return &api.Error{
		Code: api.ErrDaemonUnmappedProject,
		Detail: fmt.Sprintf("project %q is not in the [workspace] mapping of this machine — the claim is refused before any capsule is created; add the project to a [workspace.\"<path>\"] section in the config (`pf doctor` checks the mapping)",
			project),
	}
}

// LoadWorkspace is the polyforge#79 pre-claim read (fresh, no cache, so
// an edit governs the next dispatch with no daemon restart): resolve the
// project's workspace through the inverse mapping (DAEMON-412 when
// unmapped) and strictly validate <root>/.polyforge.yaml through the
// shared loader, its per-step keys against the step ids of the installed
// workflows and every candidate's `<harness>:` prefix (polyforge#175)
// — DAEMON-414 when invalid. The parsed config is nil when the
// workspace carries no yaml (absent-equivalent: bundle defaults).
// Nothing is claimed or created by it; the daemon's submit face and
// auto-pull call it too, so a refusal reaches the operator before enqueue.
func (r *Runtime) LoadWorkspace(project string) (*workspace.Config, error) {
	ws, err := r.plan().WorkspaceFor(project)
	if err != nil {
		return nil, unmappedProjectError(project)
	}
	wcfg, err := workspace.Load(ws, r.cfg.WorkflowsDir, r.plan().ProjectsOf(ws))
	if err != nil {
		return nil, &api.Error{
			Code:   api.ErrDaemonWorkspaceYaml,
			Detail: fmt.Sprintf("the workspace yaml of project %q refused the claim before it was made (the work item stays queued): %v", project, err),
		}
	}
	return wcfg, nil
}

// EvClaimPausedEndedBlocked is the runtime event of a pf pause that
// landed while the claim was still materializing (B-8): there is no
// scene to retain a paused record for, so the session ended blocked and
// `pf resume` re-claims the work item.
const EvClaimPausedEndedBlocked = "claim_paused_ended_blocked"

// CheckBundle is the polyforge#175 pre-claim bundle read: the installed
// bundle's candidates must all be `<harness>:<model>` (and it may carry
// no `harness` key). A refusal is the typed DAEMON-414 naming the file
// and step — nothing is claimed, the work item stays queued. An empty
// bundle name (not bound yet) passes.
func (r *Runtime) CheckBundle(wi, bundle string) error {
	if bundle == "" {
		return nil
	}
	if bundle == workflow.SharedDir {
		return &api.Error{
			Code:   api.ErrDaemonWorkspaceYaml,
			Detail: fmt.Sprintf("the workflow bundle of %s refused the claim before it was made (the work item stays queued): %q holds the shared step definitions and is not a workflow bundle", wi, workflow.SharedDir),
		}
	}
	if _, ferr := r.readBundleFrontmatter(bundle); ferr != nil {
		return &api.Error{
			Code:   api.ErrDaemonWorkspaceYaml,
			Detail: fmt.Sprintf("the workflow bundle of %s refused the claim before it was made (the work item stays queued): %v", wi, ferr),
		}
	}
	return nil
}

// RefusedBundles answers the installed bundles whose frontmatter fails to
// load (polyforge#175: an unprefixed candidate, a retired `harness`
// key, or torn yaml), name → load error — the auto-pull wave skips work
// items on them instead of enqueueing a claim that is bound to refuse.
func (r *Runtime) RefusedBundles() map[string]error {
	_, bad, _ := workspace.Bundles(r.cfg.WorkflowsDir, "")
	return bad
}

// readBundleFrontmatter reads the pinned bundle's workflow.md
// frontmatter from the daemon checkout. A torn frontmatter, or one whose
// candidate format is refused (polyforge#175), answers the load error
// naming the file and the step; a missing one answers (nil, nil).
func (r *Runtime) readBundleFrontmatter(bundleName string) (*workflow.Frontmatter, error) {
	if bundleName == "" || r.cfg.WorkflowsDir == "" {
		return nil, nil
	}
	fm, err := workflow.ReadFrontmatter(filepath.Join(r.cfg.WorkflowsDir, bundleName, "workflow.md"))
	if err != nil {
		return nil, fmt.Errorf("workflow bundle %s refused: %w", bundleName, err)
	}
	return fm, nil
}

// abandonClaim is the D5 compensation carried into the session model:
// every post-claim failure ends the session BLOCKED at aihub (the hub
// clears the session row; the WI leaves the claimable set until a human
// intervenes) and clears the claiming journal entry, then hands the
// original error back — never an orphaned running session, never a
// daemon crash. A close the hub does not take is not dropped: the session
// is kept as an end_pending job (pendingClose), whose actor retries the
// blocked close.
func (r *Runtime) abandonClaim(j *job, origErr error, note string) error {
	err := r.hub.SessionEnd(j.wi, api.SessionEndRequest{SessionID: j.sessionID, Status: api.StatusBlocked, Note: note})
	if err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: session-end(blocked) after failed start failed (kept end_pending, retrying): %v\n", j.wi, err)
		if !r.pendingClose(j, note) {
			return origErr // the secret is not durable elsewhere: the journal keeps it
		}
	}
	r.clearClaimingSession(j.wi, j.sessionID)
	return origErr
}

// pendingClose keeps a claim's session whose blocked close the hub has
// not taken: the job turns end_pending (nothing of it is pushed), is
// persisted, and its actor retries the close. It reports whether the
// record is durable — only then may the claiming journal entry go.
func (r *Runtime) pendingClose(j *job, note string) bool {
	j.mu.Lock()
	j.state, j.endNote, j.endPushGaveUp, j.agent = StateEndPending, note, true, nil
	j.mu.Unlock()
	r.putJob(j)
	r.retry(j)
	return r.persistLog(j.wi)
}

// verifySealedHead is the continue guard of a blocked seal (v2-converge §7):
// a blocked WI never continues without the sealing daemon's recorded
// capsule head. A clone already at that head proceeds. When aihub lacks it
// on the sealing machine, the sealed scene is the newest quarantined
// generation of the work item (prepareCapsule moved it aside) whose
// git-dir holds the head — a materialized one first, so an adopted clone
// that never laid its task repos out is passed over. The fresh clone
// adopts the head from it (the next publish pushes it), and the task
// repos are restored from the same generation (j.sealedScene,
// cloneTaskRepos). Anywhere else a clone not at the head
// is pre-blocked state (the #116 lesson) and the start is refused, as on
// the sealing machine when no generation holds the head. A WI with no
// recorded seal is not gated.
func (r *Runtime) verifySealedHead(ctx context.Context, j *job, c api.SessionClaim) error {
	if c.SealedHead == "" || c.SealedMachine == "" {
		return nil
	}
	head, err := gitops.Head(j.git)
	atHead := err == nil && head == c.SealedHead
	if c.SealedMachine == r.cfg.MachineID {
		if atHead {
			return nil
		}
		gen, scene := "", ""
		for _, q := range quarantinedGitDirs(j.git.GitDir) {
			if !gitops.HasCommit(j.git, q, c.SealedHead) {
				continue
			}
			s := j.capsuleDir + strings.TrimPrefix(q, j.git.GitDir)
			if _, serr := os.Stat(filepath.Join(s, filepath.FromSlash(RelMaterializeMarker))); serr == nil {
				gen, scene = q, s
				break
			}
			if gen == "" {
				gen = q
			}
		}
		if gen != "" {
			if aerr := gitops.AdoptCommit(ctx, j.git, c.SealedHead, gen); aerr != nil {
				return aerr
			}
			j.sealedScene = scene
			fmt.Fprintf(os.Stderr, "runtime: %s: continuing from the sealed head %s adopted from %s\n", j.wi, c.SealedHead, gen)
			return nil
		}
		return &api.Error{Code: api.ErrDaemonSealedHeadMismatch,
			Detail: fmt.Sprintf("continue refused: the work item was sealed blocked by this machine (%q) at capsule head %s, but neither the aihub capsule (at %q) nor any quarantined capsule of it on this machine holds that commit — accept losing that work with pf resume %s --accept-lost-work, or abandon (pf cancel)",
				c.SealedMachine, c.SealedHead, head, j.wi)}
	}
	if atHead {
		return nil
	}
	return &api.Error{Code: api.ErrDaemonSealedHeadMismatch,
		Detail: fmt.Sprintf("continue refused: the work item was sealed blocked by machine %q at capsule head %s, but the aihub capsule this daemon cloned is at %q (%v) — the sealing daemon's last push did not land; continue it on %q, accept losing that work with pf resume %s --accept-lost-work, or abandon (pf cancel)",
			c.SealedMachine, c.SealedHead, head, err, c.SealedMachine, j.wi)}
}

// ---------------------------------------------------------------------------
// Stop (§一): seal → monitor release → final push → SessionEnd → RTFinished.

// Stop seals wi with a terminal reason (protocol.FinishWrapped | -Failed)
// — or performs the operator pause close when reason is
// protocol.FinishPaused (Finding 2: the record is RETAINED in StatePaused;
// see pauseOp). It is a message of the work item's actor, so it answers
// once the operations queued before it settled: a second close of the
// same work item finds it closed. A job still CLAIMING has its clone
// cancelled first, and the claim settles the close (closeClaim).
func (r *Runtime) Stop(ctx context.Context, wi, reason string) error {
	id := r.CanonicalID(wi)
	var cc *claimClose
	if j := r.jobOf(id); j != nil {
		cc = j.requestClaimClose(reason)
	}
	return r.call(ctx, id, func() error {
		if cc != nil && cc.settled {
			return cc.err
		}
		j := r.jobOf(id)
		if j == nil {
			return fmt.Errorf("runtime: %s: no active job", wi)
		}
		switch st, _ := j.snapshot(); {
		case j.closeParked():
			// the parked close finishes by itself once the old Run exits;
			// a second close would only park again (or swap its verdict)
			return j.parkedError(id)
		case st == StatePaused && closePending(j.record()):
			// one close at a time: a repeat would arm another retry, or
			// swap the pending verdict mid-close
			return &api.Error{Code: api.ErrDaemonConflict,
				Detail: fmt.Sprintf("%s: its %s close is already in progress (%s)", id, j.record().StopReason, j.closeRetryNote())}
		case st == StatePaused && reason == protocol.FinishPaused:
			return nil // the pause already holds
		case st == StateSealing, st == StateEndPending:
			return &api.Error{Code: api.ErrDaemonConflict,
				Detail: fmt.Sprintf("%s: work item is %s; its close is already in progress (%s)", id, st, j.closeRetryNote())}
		case st == StateClaiming:
			return &api.Error{Code: api.ErrDaemonHubUnavailable,
				Detail: fmt.Sprintf("%s: the claim of this work item is still waiting for aihub; %s", id, j.retryChainNote("retry later"))}
		}
		if err := r.fence(j); err != nil {
			return err
		}
		return r.stopOp(ctx, j, reason, false, "")
	})
}

// requestClaimClose records a close on a CLAIMING job and cancels its
// clone/materialize; it answers the record the claim settles, or nil when
// the job is not materializing a claim. The first request wins.
func (j *job) requestClaimClose(reason string) *claimClose {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.claimCancel == nil {
		return nil
	}
	if j.claimClose == nil {
		j.claimClose = &claimClose{reason: reason}
	}
	j.claimCancel()
	return j.claimClose
}

// closeClaim ends a CLAIMING job's session on the operator's request: the
// agent never started and nothing was produced, so there is no seal push —
// the session is ended with the requested verdict and the placeholder
// dropped. A pause has no retained record to attach to, so it closes
// BLOCKED (`pf resume` re-claims). A close the hub does not take is kept
// end_pending and retried.
func (r *Runtime) closeClaim(j *job, reason string) error {
	r.dropJob(j)
	status := reason
	note := "closed by the operator while the claim was materializing (the agent never started)"
	if reason == protocol.FinishPaused {
		status = api.StatusBlocked
		note = "paused by the operator while the claim was materializing (the agent never started); pf resume re-claims it"
		r.tap(j.wi, EvClaimPausedEndedBlocked, map[string]any{"session_fingerprint": j.sessionFingerprint, "note": note})
	}
	// The (partly) materialized scene never ran an agent and belongs to a
	// session that ends here: move it aside now.
	if err := quarantineCapsule(j.capsuleDir, j.git.GitDir); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: quarantine of the closed claim's capsule failed (scene left in place): %v\n", j.wi, err)
	}
	if err := r.hub.SessionEnd(j.wi, api.SessionEndRequest{SessionID: j.sessionID, Status: status, Note: note}); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: session-end(%s) of the claim failed (kept end_pending; retried with backoff up to the chain's bound, or parked on a typed 4xx refusal): %v\n", j.wi, status, err)
		if r.pendingClose(j, note) {
			r.clearClaimingSession(j.wi, j.sessionID)
		}
		return fmt.Errorf("runtime: %s: the claim was stopped but its session-end failed (the blocked close is retried with backoff up to the chain's bound, or parked on a typed 4xx refusal): %w", j.wi, err)
	}
	r.clearClaimingSession(j.wi, j.sessionID)
	if status != api.StatusBlocked {
		r.clearManualModels(j.wi)
	}
	return nil
}

// tap emits one runtime event (EventTap; nil in most tests).
func (r *Runtime) tap(wi, kind string, body map[string]any) {
	if r.cfg.EventTap != nil {
		r.cfg.EventTap(wi, kind, body)
	}
}

// claimDeferredError is the typed answer of a start the shutdown stopped
// (DAEMON-410): nothing turned blocked; a claim that landed stays in the
// claiming journal and the next daemon start replays it.
func claimDeferredError(wi string) error {
	return &api.Error{Code: api.ErrDaemonGone,
		Detail: fmt.Sprintf("%s: the daemon is shutting down; the start is deferred (a landed claim stays in the claiming journal and resumes at the next start)", wi)}
}

// ErrClaimClosed is the Start answer of a claim an operator close (Stop
// during CLAIMING) settled: the start is over and must not be retried.
var ErrClaimClosed = errors.New("runtime: the claim was closed by the operator before its agent started")

// IsClaimDeferred reports whether a Start error is the shutdown deferral
// (Start's only DAEMON-410 answer).
func IsClaimDeferred(err error) bool {
	var ae *api.Error
	return errors.As(err, &ae) && ae.Code == api.ErrDaemonGone
}

// supersede releases a work item another session holds (v1.5 M4 + m3:
// the periodic revalidation's OBSuperseded, or a verify that answered
// not-ours): the new owner owns the work, so this daemon makes NO hub
// transition (fencing-on-use rejects our writes; a blocked verdict would
// sabotage the new owner) and runs NO recovery. It kills the local tree,
// quarantines the capsule in place (capsules/<name>.quarantined.<ts>),
// drops the record and emits the owner-visible event — the local capsule
// (and any answers recorded in it) did not travel, so "answers may be
// lost; question re-opened" (m3).
//
// A driver Run that outlives its bounded teardown may still write the
// capsule: renaming the scene under it (or dropping the record, which lets
// a re-claim start a second writer there) must wait. The release parks
// (parkClose, kind release) and re-runs once that Run exits; the answer is
// the parked 409.
func (r *Runtime) supersede(j *job, kind, detail, currentFP string) error {
	if err := r.killAgentTree(j); err != nil {
		return r.parkClose(j, parkKindRelease, kind, func() { _ = r.supersede(j, kind, detail, currentFP) })
	}
	if err := quarantineCapsule(j.capsuleDir, j.git.GitDir); err != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: superseded capsule quarantine failed (scene left in place): %v\n", j.wi, err)
	}
	r.dropJob(j)
	r.persistLog(j.wi)
	body := map[string]any{"session_fingerprint": j.sessionFingerprint, "note": "answers may be lost; question re-opened"}
	if detail != "" {
		body["detail"] = detail
	}
	if currentFP != "" {
		body["current_fingerprint"] = currentFP
	}
	r.tap(j.wi, kind, body)
	return nil
}

// releaseMonitor stops wi's observe monitor and waits until it is gone
// (Config.ReleaseMonitor): a close calls it before its own pushes and
// SessionEnd.
func (r *Runtime) releaseMonitor(wi string) {
	if r.cfg.ReleaseMonitor != nil {
		r.cfg.ReleaseMonitor(wi)
	}
}

// release stops the observe monitor of a work item this daemon gave up
// (RTFinished superseded also stops its re-emitting revalidation).
func (r *Runtime) release(wi string) {
	r.send(protocol.RTMsg{Kind: protocol.RTFinished, WorkItem: wi, Reason: protocol.FinishSuperseded})
}

// stopOp runs one close to the end. reason is the close verdict (a
// terminal wrapped|failed|blocked, or paused — the operator pause); note
// is the human-facing elaboration the SessionEnd carries. alreadySealing
// resumes a recorded close (a retry, a restart): its seal push is skipped
// when it already reached the hub. A SessionEnd the hub does not take
// leaves the close pending — the job SEALING, or a retained PAUSED record
// holding its terminal stop_reason (closePending) — persisted with its
// verdict, and the ATTEMPT's own actor retry (retry) is the one scheduler
// of the next try, first close and replay alike; the reconcile that runs
// a replay must not clear that backoff. A 4xx refusal is
// not a pending close at all: it is parked (parkRefused), since no retry
// can change it — STALE_SESSION excepted, which the next reconcile
// finishes locally.
func (r *Runtime) stopOp(ctx context.Context, j *job, reason string, alreadySealing bool, note string) error {
	if reason == protocol.FinishPaused && !alreadySealing {
		return r.pauseOp(j)
	}
	j.mu.Lock()
	alreadyPushed := alreadySealing && j.sealedPush
	fromPaused := j.state == StatePaused
	j.mu.Unlock()
	if !alreadySealing {
		// Closing a RETAINED paused record (wrap/fail after an operator
		// pause, Finding 2) has no SEALING hop — Paused → Done directly;
		// its persisted terminal stop_reason is the pending close.
		if !fromPaused {
			if err := j.transition(StateSealing); err != nil {
				return err
			}
		}
		j.mu.Lock()
		j.stopReason, j.sealedPush, j.endNote = reason, false, note
		j.mu.Unlock()
		r.persistLog(j.wi)
	}
	// Six-layer teardown: no writer survives into the sealed scene —
	// neither the agent tree nor the observe monitor's capsule pushes. A
	// driver Run that outlives its bounded teardown is still a writer: the
	// seal push, the WIP lane and the read-only seal must not run next to
	// it, so the close parks in its persisted state (SEALING, or the
	// retained PAUSED record holding its terminal stop_reason) and its
	// waiter replays it once that Run exits.
	if err := r.killAgentTree(j); err != nil {
		return r.parkClose(j, "close", reason, func() {
			st, _ := j.snapshot()
			if perr := r.stopOp(r.ctx, j, reason, st == StateSealing, note); perr != nil {
				fmt.Fprintf(os.Stderr, "runtime: %s: parked %s close resumed and failed: %v\n", j.wi, reason, perr)
			}
		})
	}
	r.releaseMonitor(j.wi)
	finalReason, endErr := r.sealNetwork(j, reason, note, alreadyPushed)
	if endErr != nil {
		// the close stays pending — SEALING, or a retained PAUSED record
		// holding its terminal stop_reason — and this attempt's own retry
		// is what replays it. A typed 4xx refusal is final instead: no
		// retry can land it, so the record is parked. STALE_SESSION is not
		// that case — it means the session is gone, and the next reconcile
		// finishes the close locally (finishSeal).
		st, _ := j.snapshot()
		switch {
		case claimRefused(endErr) && !isStaleSession(endErr):
			r.persistLog(j.wi)
			r.parkRefused(j, EventCloseRefused, st, endErr)
			return fmt.Errorf("runtime: %s: session-end failed (the hub refused the close; the record is parked, not retried): %w", j.wi, endErr)
		case st == StateSealing || (st == StatePaused && !alreadySealing):
			r.persistLog(j.wi)
			r.retry(j)
		}
		return fmt.Errorf("runtime: %s: session-end failed (the close stays pending, retrying): %w", j.wi, endErr)
	}
	crashpoint.At("seal.ended")
	if err := sealReadOnly(j.capsuleDir); err != nil {
		// a scene-preservation wart, not a seal failure: loud, non-fatal
		fmt.Fprintf(os.Stderr, "runtime: %s: read-only seal incomplete: %v\n", j.wi, err)
	}
	if err := j.transition(StateDone); err != nil {
		return err
	}
	r.dropJob(j)
	if finalReason != protocol.FinishBlocked {
		// terminal on this daemon: the manual --models list dies with it
		// (a blocked end keeps it for the re-claim)
		r.clearManualModels(j.wi)
	}
	r.persistLog(j.wi)
	r.send(protocol.RTMsg{Kind: protocol.RTFinished, WorkItem: j.wi, Reason: finalReason})
	return nil
}

// sealNetwork is stopOp's network half: the final push — unless it
// already reached the hub (a second push could only fail against a
// revoked credential) — the task-repo WIP lane and the fenced SessionEnd.
// A persistently failed push degrades the close to blocked (the local
// commit keeps the bytes, but the server never heard the final state).
// A blocked close carries the local chain head, as every blocked verdict
// does (endBlockedAtHub): aihub records it as the blocked seal, so a
// continue on another machine refuses a clone that lacks the unpushed
// commits instead of silently dropping them.
func (r *Runtime) sealNetwork(j *job, reason, note string, alreadyPushed bool) (string, error) {
	finalReason := reason
	chainOK := true
	if !alreadyPushed {
		r.syncLiveTranscript(j.capsuleDir)
		crashpoint.At("seal.committing")
		if perr := sealPush(j, reason); perr != nil {
			fmt.Fprintf(os.Stderr, "runtime: %s: final push failed after retries: %v\n", j.wi, perr)
			// one durable push_failed event per sealing series
			j.mu.Lock()
			noted := j.sealPushFailNoted
			j.sealPushFailNoted = true
			j.mu.Unlock()
			if !noted {
				r.tap(j.wi, "push_failed", map[string]any{"phase": "seal", "reason": reason, "error": perr.Error()})
			}
			finalReason = protocol.FinishBlocked
			chainOK = false
		} else {
			j.mu.Lock()
			j.sealedPush = true
			j.mu.Unlock()
			crashpoint.At("seal.pushed")
		}
	}
	// Task-repo uncommitted work (polyforge#167): every close but wrapped
	// snapshots it to refs/pf-wip; the wrapped close only drops stale WIP
	// refs. Before SessionEnd: the session credential dies with it.
	r.saveTaskWIP(j, finalReason != protocol.FinishWrapped, chainOK)
	// v1.5: the terminal close is SessionEnd, fenced on the session hash.
	// A STALE_SESSION refusal surfaces as the error and leaves the job
	// SEALING; its retry finishes it locally (finishSeal).
	req := api.SessionEndRequest{SessionID: j.sessionID, Status: finalReason, Note: note}
	if finalReason == protocol.FinishBlocked {
		req.HeadSHA, _ = gitops.Head(j.git)
	}
	return finalReason, r.hub.SessionEnd(j.wi, req)
}

// pauseOp is the operator pause close (v1.5 review Finding 2): the M3
// pause transition WITH record retention — the session secret must
// survive locally so the holder can attach once the operator clears
// clean_pause:
//
//	RUNNING → PAUSED, persisted FIRST (a crash anywhere after it leaves a
//	Paused record the restart reconciles); kill the tree; commit+push the
//	dirty capsule (a failed push is LOG-ONLY: the verdict is not
//	terminal); SessionEnd(paused, CleanPause=true) — the hub keeps the
//	session row; RTFinished(paused) — the observe release. NO read-only
//	seal, NO dropJob, NO DONE.
//
// A SessionEnd failure (or a crash before it) leaves the record PAUSED
// against a hub that still says running: the restart's reconciliation
// replays the pause (reconcileRecord), so the pause is never lost.
func (r *Runtime) pauseOp(j *job) error {
	if err := j.transition(StatePaused); err != nil {
		return err
	}
	j.mu.Lock()
	j.stopReason, j.sealedPush, j.cleanPause = protocol.FinishPaused, false, true
	j.mu.Unlock()
	r.persistLog(j.wi)
	crashpoint.At("pause.persisted")
	return r.finishPause(j)
}

// finishPause is pauseOp's half after the PAUSED record is persisted:
// the teardown, the agentless record, the network half and the observe
// release. A driver Run that outlives its teardown parks it — the pause's
// push and WIP lane must not run next to that writer; the persisted
// record is a pending pause a restart replays, and the park's waiter runs
// finishPause again once the Run exits.
func (r *Runtime) finishPause(j *job) error {
	if err := r.killAgentTree(j); err != nil {
		return r.parkClose(j, "pause", protocol.FinishPaused, func() {
			if perr := r.finishPause(j); perr != nil {
				fmt.Fprintf(os.Stderr, "runtime: %s: parked pause resumed and failed: %v\n", j.wi, perr)
			}
		})
	}
	// the retained record carries no live agent: the status face (and the
	// restart's kill pass) read it agentless
	j.mu.Lock()
	j.agentPID, j.agentStartedAt = 0, ""
	j.mu.Unlock()
	r.persistLog(j.wi)
	if err := r.pauseNetwork(j); err != nil {
		return fmt.Errorf("runtime: %s: session-end(paused) failed (record left paused; the next restart reconciles): %w", j.wi, err)
	}
	r.send(protocol.RTMsg{Kind: protocol.RTFinished, WorkItem: j.wi, Reason: protocol.FinishPaused})
	return nil
}

// pauseNetwork is pauseOp's network half: commit+push the dirty capsule
// (log-only on failure), the task-repo WIP lane (an attach resumes in
// place, a later re-claim restores from the carrier) and
// SessionEnd(paused) — always a clean pause: the operator CLI is the only
// pause producer (M3).
func (r *Runtime) pauseNetwork(j *job) error {
	r.syncLiveTranscript(j.capsuleDir)
	perr := sealPush(j, protocol.FinishPaused)
	if perr != nil {
		fmt.Fprintf(os.Stderr, "runtime: %s: pause seal push failed after retries (local commit stands): %v\n", j.wi, perr)
		r.tap(j.wi, "push_failed", map[string]any{"phase": "pause-seal", "reason": protocol.FinishPaused, "error": perr.Error()})
	}
	r.saveTaskWIP(j, true, perr == nil)
	return r.hub.SessionEnd(j.wi, api.SessionEndRequest{SessionID: j.sessionID, Status: api.StatusPaused, CleanPause: true})
}

// sealPush commits the capsule and pushes it, retrying the push twice.
func sealPush(j *job, reason string) error {
	_, _, _, err := gitops.CommitPush(context.Background(), j.git, "seal: "+reason)
	for i := 0; i < 2 && err != nil; i++ {
		_, err = gitops.Push(j.git)
	}
	return err
}

// DriverAnswerHandle returns the bridge Handle for a driver-driven WI
// (nil when the job's agent is not a bridge handle — the WI-Agent path
// answers through the observe lane instead).
func (r *Runtime) DriverAnswerHandle(wi string) interface {
	Answer(questionID, answer, answeredBy string) error
} {
	j := r.jobOf(r.CanonicalID(wi))
	if j == nil {
		return nil
	}
	j.mu.Lock()
	h := j.agent
	j.mu.Unlock()
	if bh, ok := h.(interface {
		Answer(questionID, answer, answeredBy string) error
	}); ok {
		return bh
	}
	return nil
}
