// Package protocol defines the in-process/in-machine contracts of v1
// (current routes: docs/design/polyforge-vnext/reference.md §6.1; decisions:
// v2-converge.md §2; the v1 message design is archive/v1-spec-full.md §二 §五):
//
//  1. Runtime ↔ Observe messages (5 kinds, every message carries the
//     per-WI derivation counter spawn_n).
//  2. The pf CLI ↔ daemon UDS HTTP API surface (reference.md §6.1).
//
// (The liveness-probe control message was deleted with the guessing
// supervision lanes; the driver ↔ supervisor contract lives in
// internal/polyforge/supervise.)
package protocol

import "encoding/json"

// ---------------------------------------------------------------------------
// 1. Runtime ↔ Observe messages (archive/v1-spec-full.md §二)
// ---------------------------------------------------------------------------

// RTMsg is a Runtime→Observe message.
type RTMsg struct {
	Kind RTMsgKind `json:"kind"`
	// v1.5 B2: the per-WI monotonic derivation counter — the
	// session-model fence key.
	SpawnN   int    `json:"spawn_n"`
	WorkItem string `json:"work_item"`
	AgentPID int    `json:"agent_pid,omitempty"`
	Reason   string `json:"reason,omitempty"` // WIFinished: wrapped|failed
	Detail   string `json:"detail,omitempty"`
	// SessionID (v1.5): the WI session secret, passed runtime→observe
	// IN-PROCESS so the monitor can run the periodic session-verify (M4).
	// It never leaves the daemon: not in logs, not in capsule, not in
	// agent env.
	SessionID string `json:"session_id,omitempty"`
}

// OBMsg is an Observe→Runtime message. Observe must attach the derivation
// counter (spawn_n) of the agent handle it was monitoring when the event
// fired; Runtime drops messages whose counter no longer matches the
// current derivation.
type OBMsg struct {
	Kind     OBMsgKind `json:"kind"`
	SpawnN   int       `json:"spawn_n"`
	WorkItem string    `json:"work_item"`
	Reason   string    `json:"reason"` // WINeedsRecovery: death|hang|provider_error|idle|...
	Detail   string    `json:"detail,omitempty"`
	// SessionFP (v1.5 NEW-1 fence): OBSuperseded carries the SUPERSEDED
	// session's public fingerprint. The runtime fences the message on it
	// (session-level verdict): a stale buffered message must never kill a
	// successor job of the same WI claimed after a re-claim.
	SessionFP string `json:"session_fp,omitempty"`
}

type RTMsgKind string

const (
	RTStarted   RTMsgKind = "wi_started"   // new attempt began; carries agent handle
	RTRecovered RTMsgKind = "wi_recovered" // recovery succeeded; new handle, same attempt lineage
	RTFinished  RTMsgKind = "wi_finished"  // terminal: stop monitoring
)

type OBMsgKind string

const (
	OBNeedsRecovery OBMsgKind = "wi_needs_recovery" // agent died/hung; Runtime recovers
	// OBSuperseded (v1.5 M4): the periodic session revalidation learned
	// this WI is held by another daemon. Runtime's response is NOT a
	// recovery — it kills the tree, quarantines the capsule and drops the
	// record; the daemon makes no hub transition (the new owner owns it).
	OBSuperseded OBMsgKind = "wi_superseded"
)

// Recovery causes carried by WINeedsRecovery. The supervisor's step-level
// judgements (harness_silent, model_hung, tool_hung, step timeouts, ...)
// are NOT recoveries: they ride the in-process driver link and the
// driver handles them itself. Recovery is only about the driver.
const (
	// RecoverDeath: the driver returned (crash, error, or terminal exit;
	// the runtime tells them apart from the capsule).
	RecoverDeath = "agent_death"
	// RecoverDriverStalled: the driver sat between steps (not waiting on
	// a human) past the stall bound — the recovery engine kills its tree
	// and relaunches it, counted against the driver-crash bound.
	RecoverDriverStalled = "driver_stalled"
)

// Terminal reasons carried by WIFinished.
const (
	FinishWrapped = "wrapped"
	FinishFailed  = "failed"
	FinishBlocked = "blocked"
)

// Non-terminal release reasons carried by WIFinished (v1.5 review
// Findings 2 and 4): the runtime's OBSERVE RELEASE. RTFinished is not a
// terminal-only message — it is also the "this daemon no longer
// supervises the work item" signal that stops the monitor loop, prunes
// the latestSpawn entry. FinishPaused rides the operator pause close (session-end's 4th
// verdict, M3); FinishSuperseded rides the OBSuperseded response (M4).
const (
	FinishPaused     = "paused"
	FinishSuperseded = "superseded"
)

// PushTimeout is the hard bound on one capsule commit+push (the
// monitor's periodic push; archive/v1-spec-full.md §五).
const PushTimeout = 60 * 1e9 // 60 seconds (ns, for time.Duration)

// ---------------------------------------------------------------------------
// 2. pf CLI ↔ daemon UDS HTTP API (reference.md §6.1; v0 surface minus publish_request)
// ---------------------------------------------------------------------------

// Daemon job states the watcher decides on.
const (
	JobNeedsInput = "needs_input"
	// JobEndPending: blocked, the hub not told yet (runtime.StateEndPending).
	JobEndPending = "end_pending"
)

// EventEntry is one line of the daemon events file + observe output.
// Seq is the observe ring's per-WI monotonic append ordinal, assigned at
// Append time; served entries carry it so a client can resume with
// ?after=<last seq>. The durable events file keeps its wire-stable shape
// and does NOT write a seq (Seq is a live-ring concept that a daemon
// restart legitimately renumbers from 1), so Seq marshals away there
// via omitempty.
type EventEntry struct {
	At   string          `json:"at"`
	Kind string          `json:"kind"`
	WI   string          `json:"work_item,omitempty"`
	Body json.RawMessage `json:"body,omitempty"`
	Seq  int64           `json:"seq,omitempty"`
}

// Heartbeat mode strings: the daemon has one auto switch (v2-converge §5);
// the machines heartbeat body still carries a mode string for aihub
// compatibility.
const (
	HeartbeatModeAuto   = "auto"   // the daemon pulls from the ready queue itself
	HeartbeatModeManual = "manual" // only explicit pf submit
)
