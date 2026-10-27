// state.go: the per-WI state machine (reference.md §3.5) plus the Runtime's
// own bookkeeping file (state.json). Daemon state is BOOKKEEPING ONLY —
// workflow execution progress lives in the capsule's
// runtime-state/execution.json and is never mirrored here.
//
// v1.5 (session model): the record carries the session identity — the
// SECRET session_id plus its public fingerprint — and the per-WI monotonic
// derivation counter spawn_n. The v1 attempt/generation/instance/credential
// fields are deleted; the JobState machine is UNCHANGED.
package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/gitops"
)

// JobState is one per-WI Runtime state.
type JobState string

// The v1 state machine, plus the v1.5 review Finding 2 pause edges:
//
//	RUNNING → SEALING → DONE                  (stop path)
//	RUNNING → RECOVERING → RUNNING            (recovery succeeded)
//	RUNNING → RECOVERING → DONE               (bounded fail)
//	RUNNING → PAUSED                          (operator pause close, Finding 2)
//	PAUSED → RUNNING                          (restart attach/resume, M3)
//	PAUSED → DONE                             (later close of a retained record)
//	CLAIMING → RUNNING                        (the claim's agent started)
//
// CLAIMING is the in-memory placeholder of a start whose claim landed and
// whose capsule clone/materialize is still running on its actor. It
// shows in status and lets a close cancel the clone, but it is never
// persisted: the claiming journal is its durable form, and every way out
// of it other than RUNNING drops the record.
//
// Every other edge is illegal and transition() refuses it.
const (
	StateRunning    JobState = "running"
	StateRecovering JobState = "recovering"
	StateSealing    JobState = "sealing"
	StateDone       JobState = "done"
	// StatePaused is the OPERATOR-pause retention state (v1.5 review
	// Finding 2): a pause close KILLS the tree and seals the dirty
	// capsule state server-side (SessionEnd paused, clean_pause=true)
	// but RETAINS the job record — the session secret survives locally
	// so the holder daemon's restart can attach (M3) once the operator
	// clears clean_pause. No read-only seal, no dropJob, no DONE.
	StatePaused JobState = "paused"
	// StateEndPending is the durable "blocked, but the hub has not heard"
	// state (v2-converge §6 bug 5): a bounded-fail whose SessionEnd(blocked)
	// failed keeps the record — the session secret only that record holds —
	// and retries the close (in-process with backoff, and at every daemon
	// start) until the hub accepts it. Only then is the record dropped.
	StateEndPending JobState = "end_pending"
	// StateClaiming: see the state-machine comment above (B-8).
	StateClaiming JobState = "claiming"
)

var legalEdges = map[JobState][]JobState{
	StateRunning:    {StateSealing, StateRecovering, StatePaused},
	StateSealing:    {StateDone},
	StateRecovering: {StateRunning, StateSealing, StateDone, StateEndPending},
	StateEndPending: {StateDone},
	StatePaused:     {StateRunning, StateDone},
	StateClaiming:   {StateRunning},
}

// can reports whether s → to is a legal edge of the state machine.
func (s JobState) can(to JobState) bool {
	for _, n := range legalEdges[s] {
		if n == to {
			return true
		}
	}
	return false
}

// transition moves the job's state along one legal edge. Illegal edges are
// refused with an error naming both states — the fencing rule (SEALING
// drops same-fence WINeedsRecovery, RECOVERING ignores duplicates) rides
// on these refusals.
func (j *job) transition(to JobState) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if !j.state.can(to) {
		return fmt.Errorf("runtime: %s: illegal transition %s → %s", j.wi, j.state, to)
	}
	j.state = to
	return nil
}

// snapshot answers the job's state plus the derivation counter the OB
// fence compares against (B2).
func (j *job) snapshot() (state JobState, spawn int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.state, j.spawnN
}

// ---------------------------------------------------------------------------
// state.json — the Runtime's persisted bookkeeping (restart input).

const stateFilePerm = 0o600

type stateFile struct {
	Version int          `json:"version"`
	Jobs    []*jobRecord `json:"jobs"`
}

// jobRecord is one persisted job (reference.md §3.5): the session
// identity replaces the v1 attempt/generation/instance/credential
// lineage. The SECRET never leaves this file (0600) and the one-shot
// GIT_ASKPASS.
type jobRecord struct {
	WorkItemID string `json:"work_item_id"`
	Project    string `json:"project"`
	// Slug is the claim answer's "<project>#<seq>" — the display name of
	// the human-facing faces (status, watch, wake-up lines). Absent on
	// records written before it existed: those faces show the id.
	Slug string `json:"slug,omitempty"`
	// SessionID is the v1.5 SECRET (B3): 64-hex, minted by this daemon
	// at claim, the git push password. It lives ONLY here and in the
	// one-shot GIT_ASKPASS — never in the capsule, never in agent env,
	// never in logs or events.
	SessionID string `json:"session_id"`
	// SessionFingerprint is the PUBLIC handle (hex(sha256(secret))[:16]):
	// the value logs and events carry.
	SessionFingerprint string `json:"session_fingerprint"`
	State              string `json:"state"`
	StopReason         string `json:"stop_reason,omitempty"`
	// SealedPush: the SEALING record's seal-knowledge (see job.sealedPush)
	// — the restart's stop finisher skips its own seal push when set.
	SealedPush bool   `json:"sealed_push,omitempty"`
	CapsuleDir string `json:"capsule_dir"`
	GitDir     string `json:"git_dir"`
	RemoteURL  string `json:"remote_url"`
	// CleanPause mirrors the hub's operator-pause flag (reference.md §3.5).
	// Per the v1.5 review Finding 2, an operator pause RETAINS the
	// record (StatePaused): the flag is latched true at the pause close
	// — exactly the marker the restart reconciliation reads alongside
	// the hub's own clean_pause verdict. A graceful shutdown leaves it
	// false — the "paused-with-session, will attach on restart" shape
	// (M3).
	CleanPause bool `json:"clean_pause"`
	// SpawnN is the per-WI monotonic derivation counter (B2): the number
	// of derivations this session has run. Same-machine restarts and
	// recoveries continue from it; a fresh clone (re-claim on another machine) re-seeds it
	// from the capsule's spawn journal.
	SpawnN       int `json:"spawn_n"`
	RecoverFails int `json:"recover_fails"` // consecutive recovery failures (C7)
	// AcceptedAtRelaunch is the accepted-result count (the capsule's
	// execution state) the last relaunch started from — the watermark the
	// next recovery entry compares progress against. Survives a crash
	// with the counter itself.
	AcceptedAtRelaunch int64 `json:"accepted_at_relaunch,omitempty"`
	// RecoveryRelaunch persists lastRelaunchRecovery (see job).
	RecoveryRelaunch bool   `json:"recovery_relaunch,omitempty"`
	Transcript       string `json:"transcript"`
	BundleName       string `json:"bundle_name"`
	AgentPID         int    `json:"agent_pid"`
	AgentStart       string `json:"agent_started_at"`
	// Origin is how the job was submitted (v2 identity model). Absent on
	// records written before it existed: such a job loads fine and is
	// treated as owned by the WI reporter by later consumers.
	Origin *Origin `json:"origin,omitempty"`
	// EndNote/EndHead persist the pending SessionEnd(blocked) of an
	// end_pending record: the verdict's note and the capsule head the
	// sealing daemon pushed (the cross-daemon continue check, §7).
	EndNote string `json:"end_note,omitempty"`
	EndHead string `json:"end_head,omitempty"`
	// EndPushGaveUp persists job.endPushGaveUp: the end_pending chain push
	// met a PERMANENT rejection, so the blocked-close retry — after a
	// restart too — closes without pushing again. Absent (older records)
	// = false: the retry tries the push.
	EndPushGaveUp bool `json:"end_push_gave_up,omitempty"`
}

// Origin kinds.
const (
	OriginChat = "chat" // interactive submit — a harness chat when ChatID is set, a chat-less manual submit when it is empty
	OriginAuto = "auto" // auto-claim (pf ops auto on)
)

// Origin is the record of where a job came from (the one wire shape,
// protocol.Origin). ChatID is a notification label, not a credential.
// Owner may be empty (unknown).
type Origin = protocol.Origin

func (j *job) record() *jobRecord {
	j.mu.Lock()
	defer j.mu.Unlock()
	return &jobRecord{
		WorkItemID:         j.wi,
		Project:            j.project,
		Slug:               j.slug,
		SessionID:          j.sessionID,
		SessionFingerprint: j.sessionFingerprint,
		State:              string(j.state),
		StopReason:         j.stopReason,
		SealedPush:         j.sealedPush,
		CapsuleDir:         j.capsuleDir,
		GitDir:             j.git.GitDir,
		RemoteURL:          j.git.RemoteURL,
		CleanPause:         j.cleanPause,
		SpawnN:             j.spawnN,
		RecoverFails:       j.recoverFails,
		AcceptedAtRelaunch: j.acceptedAtRelaunch,
		RecoveryRelaunch:   j.lastRelaunchRecovery,
		Transcript:         j.transcript,
		BundleName:         j.bundleName,
		AgentPID:           j.agentPID,
		AgentStart:         j.agentStartedAt,
		Origin:             j.origin,
		EndNote:            j.endNote,
		EndHead:            j.endHead,
		EndPushGaveUp:      j.endPushGaveUp,
	}
}

func jobFromRecord(rec *jobRecord) *job {
	return &job{
		wi:                 rec.WorkItemID,
		project:            rec.Project,
		slug:               rec.Slug,
		sessionID:          rec.SessionID,
		sessionFingerprint: rec.SessionFingerprint,
		state:              JobState(rec.State),
		stopReason:         rec.StopReason,
		sealedPush:         rec.SealedPush,
		capsuleDir:         rec.CapsuleDir,
		git: gitops.Opts{
			// v1.5 gitops call sites: Username = the work-item id,
			// Secret = the session id (the push password).
			WorkItemID: rec.WorkItemID,
			CapsuleDir: rec.CapsuleDir,
			GitDir:     rec.GitDir,
			RemoteURL:  rec.RemoteURL,
			Username:   rec.WorkItemID,
			Secret:     rec.SessionID,
		},
		cleanPause:           rec.CleanPause,
		spawnN:               rec.SpawnN,
		recoverFails:         rec.RecoverFails,
		acceptedAtRelaunch:   rec.AcceptedAtRelaunch,
		lastRelaunchRecovery: rec.RecoveryRelaunch,
		transcript:           rec.Transcript,
		bundleName:           rec.BundleName,
		agentPID:             rec.AgentPID,
		agentStartedAt:       rec.AgentStart,
		origin:               rec.Origin,
		endNote:              rec.EndNote,
		endHead:              rec.EndHead,
		endPushGaveUp:        rec.EndPushGaveUp,
	}
}

// loadState reads state.json; a missing file is an empty state (first
// boot), an unparsable one is a hard error (never guess bookkeeping).
func loadState(path string) (*stateFile, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return &stateFile{Version: 1}, nil
		}
		return nil, err
	}
	var st stateFile
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("runtime: state file unparsable: %w", err)
	}
	return &st, nil
}

func nowStamp() string { return time.Now().UTC().Format(time.RFC3339Nano) }

// restoredJob is jobFromRecord plus the runtime-owned wiring (the gitops
// event sink and the job's in-memory carrier alert state) a bare record
// cannot carry.
func (r *Runtime) restoredJob(rec *jobRecord) *job {
	j := jobFromRecord(rec)
	j.git.EventSink = r.gitEventSink(rec.WorkItemID)
	j.git.CarrierAlerts = &gitops.CarrierAlerts{}
	return j
}
