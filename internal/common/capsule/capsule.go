// Package capsule defines the v1 execution-capsule contract shared by pf
// (runtime creates capsules, observe commits them) and aihub (serves the
// matching bare repos; git-dir lives OUTSIDE the capsule).
//
// Layout and chain rules: docs/design/polyforge-vnext/v2-converge.md §14 and
// Appendix E.5 (summary in reference.md §8); the v1 records are archived.
//
// v1 deltas vs v0:
//   - The capsule IS a git worktree; the revision chain IS git history.
//   - git-dir is externalized: <data>/git/wi_xxx.git — agents cannot reach
//     hooks/config/index.
//   - .gitignore covers only repos/, artifacts/release-*/ and the raw
//     downloads directory artifacts/downloads/ (v1 ruling C1 (archive/v1-architecture-spec.md):
//     transcripts go INTO git; secret scanning is dropped). A file over
//     MaxBlobBytes never enters the chain either: the daemon leaves it out
//     at commit time and lists it in OversizeManifest.
//   - session-data/ side upload is deleted; transcripts are committed like
//     any other capsule file.
package capsule

import "encoding/json"

// Capsule directory layout (relative to capsule root).
const (
	DirArtifacts    = "artifacts"           // step outputs; release trees under artifacts/release-*/
	DirDownloads    = "artifacts/downloads" // raw downloads (CI artifacts, traces, screenshots, video bundles); never committed
	DirLogs         = "logs"                // daemons logs, step journals
	DirRepos        = "repos"               // task repo checkouts (own git histories)
	DirRuntimeState = "runtime-state"       // execution.json, journal, spawn.log
	DirSession      = "session"             // harness transcripts synced at boundaries
	DirWorkflows    = "workflows"           // materialized workflow bundle
	CapsuleJSON     = "capsule.json"        // capsule identity
	WorkItemJSON    = "runtime-state/work-item.json"
	ExecutionJSON   = "runtime-state/execution.json"
	// RecalledMemoriesJSON is the claim-time memory snapshot the daemon
	// writes and the driver renders into every step prompt.
	RecalledMemoriesJSON = "runtime-state/recalled-memories.json"
	// MemoryPolicyJSON is the claimed project's effective memory policy
	// (the workspace yaml's memory_policy, else the built-in default),
	// written by the daemon at claim and rendered into every step prompt.
	MemoryPolicyJSON = "runtime-state/memory-policy.json"
	// SkippedStepsJSON is the work item's skipped-step set:
	// a JSON list of the step ids its lane snapshot declares skippable that
	// the project's workspace yaml switches off (`skip: true`), in
	// declaration order. The daemon writes it at the first claim only — a
	// re-claim or a resume keeps the one the capsule chain carries — and
	// the driver reads it, never the yaml.
	SkippedStepsJSON = "runtime-state/skipped-steps.json"
	// OversizeManifest lists the files the daemon left out of the chain
	// because each is over MaxBlobBytes: path, size and sha256 (gitops'
	// commit guard writes it,).
	OversizeManifest = "artifacts/oversize.json"
	// StepCallsJSONL is the `pf step` call ledger (workflow contract 4,
	//): the daemon appends every call of a step before it
	// runs and what it came to after — the daemon's record of a step's
	// hand-off and of the hub writes it left pending (stepcall package).
	StepCallsJSONL = "runtime-state/step-calls.jsonl"
	// HandoffMD is the work item's one hand-off document (workflow
	// contract 4,): append-only, each section a
	// daemon-stamped header over the body a step handed off with
	// `pf step handoff`.
	HandoffMD = "artifacts/handoff.md"
)

// GitignoreContent is the capsule .gitignore (v1 ruling C1 (archive/v1-architecture-spec.md)), written at
// every claim and rewritten at every recovery, so an existing capsule gets
// a new line on its next claim or resume. /artifacts/downloads/ is the raw
// downloads directory (DirDownloads,).
const GitignoreContent = "/repos/\n/artifacts/release-*/\n/" + DirDownloads + "/\n"

// Identity is capsule.json (v2-converge.md Appendix E.5). Repos records the SHA each task repo
// was checked out at by the latest materialization (a re-claim that restores the
// carrier re-stamps it to the restored HEAD), so it is not a claim-time base and
// nothing judges delivery by it. The v1 generation/attempt_id lineage fields and base_commit are
// deleted: capsules still carrying those keys parse fine — encoding/json
// ignores unknown keys.
type Identity struct {
	WorkItemID string            `json:"work_item_id"`
	CapsuleID  string            `json:"capsule_id"`
	Project    string            `json:"project"`
	BundleName string            `json:"bundle_name"`
	Repos      map[string]string `json:"repos"` // repo name -> checked-out SHA at the latest materialization
	CreatedAt  string            `json:"created_at"`
}

// WorkItemDoc is runtime-state/work-item.json: the daemon-materialized
// copy of the work item's task definition (goal, content, labels,
// priority, milestone, bundle pin and project name). v1 has NO
// agent→server read path (v1 ruling C3 (archive/v1-architecture-spec.md): the whole agent contact surface is
// the capsule), so this file is the ONE
// authoritative statement of what the WI agent is working on — the
// claim answer is its only source, and recovery re-materializes it from
// aihub when a legacy capsule predates it.
type WorkItemDoc struct {
	ID         string   `json:"id"`
	Slug       string   `json:"slug,omitempty"`
	Project    string   `json:"project"`
	Goal       string   `json:"goal"`
	Content    *string  `json:"content"`
	Labels     []string `json:"labels"`
	Priority   string   `json:"priority"`
	Milestone  *string  `json:"milestone"`
	BundleName string   `json:"bundle_name"`
	UpdatedAt  string   `json:"updated_at"`
}

// The workflow_status vocabulary (reference.md §7.3): the closed status set the
// execution.json contract speaks. The DRIVER owns the writes and the
// transitions (its own aliases in internal/polyforge/driver); these
// constants live at the SCHEMA owner so non-driver lanes (the runtime's
// recovery wiring) can read a terminal verdict without importing the
// driver (import cycle: the driver's tests import the runtime).
const (
	WorkflowStatusRunning    = "running"
	WorkflowStatusCompleted  = "completed"
	WorkflowStatusFailedGate = "failed_gate"
	// WorkflowStatusBlocked is the NON-terminal waiting-on-owner state.
	WorkflowStatusBlocked = "blocked"
)

// Execution is runtime-state/execution.json (reference.md §7.3): the single source of
// truth the daemon's Go driver writes (the single writer) and observe
// watches (v1 ruling C3 (archive/v1-architecture-spec.md): the agent is zero-blocking).
type Execution struct {
	CapsuleID      string           `json:"capsule_id"`
	WorkItemID     string           `json:"work_item_id"`
	CurrentStep    string           `json:"current_step"`
	WorkflowStatus string           `json:"workflow_status"`
	Steps          map[string]*Step `json:"steps"`
	RewindStats    RewindStats      `json:"rewind_stats"`
	BudgetConsumed Budget           `json:"budget_consumed"`
	UpdatedAt      string           `json:"updated_at"`
	Version        int64            `json:"version"`
	// BlockedReason names why the workflow is parked blocked: a step's
	// own `blocked` hand-off, or the driver's own authority (a host-class
	// fault, a failure counter at its bound, ...); empty when not blocked.
	BlockedReason string `json:"blocked_reason,omitempty"`
	// CancelRequested is the durable intent of an owner's reserved
	// `cancel` answer: written by the answer's own transition together
	// with workflow_status blocked (the closed status vocabulary is
	// unchanged). The daemon's blocked close then ends the session at the
	// hub as blocked and calls the hub's existing blocked→canceled door
	// with the verbatim answer (the door treats an already-canceled work
	// item as done). A blocked close that settles — at once, on a restart's
	// end_pending replay, or with its answer lost — calls the door; a cancel
	// that still does not land (a crash between the settled close and the
	// door, a hub refusal) leaves the work item blocked, its blocked_reason
	// naming the request: `pf cancel` finishes it, and the owner's resume
	// drops this intent.
	CancelRequested *CancelIntent `json:"cancel_requested,omitempty"`
}

// CancelIntent is one owner's cancel the daemon carries out: an `intent:
// cancel` the step's agent delivered on an owner message (intent.go) — the
// message's stamp (QuestionID), the owner's words with the agent's reading
// (Answer), the owner's identity and the stamp.
type CancelIntent struct {
	QuestionID string `json:"question_id"`
	Answer     string `json:"answer"`
	AnsweredBy string `json:"answered_by"`
	At         string `json:"at"`
}

// Step statuses.
const (
	StepPending    = "pending"
	StepInProgress = "in_progress"
	StepDone       = "done"
	// StepSkipped is a step the driver never dispatches: the work item's
	// skipped-step set (SkippedStepsJSON) names it and its lane snapshot
	// declares it skippable.
	StepSkipped = "skipped"
)

type Step struct {
	Status string `json:"status"`
	// Candidate is the `<harness>:<model>[:effort]` candidate string the
	// step's current/next dispatch runs (polyforge#33/#34) — the step's
	// one model fact (harness.ParseCandidate derives the model id and the
	// effort; an older record's model/effort keys are ignored): written at
	// step_start and at the failure that decides a switch (the decided next
	// model, persisted with that death's archive entry), and the anchor of
	// the candidate pointer. Archived attempts are stamped with the
	// candidate the dying dispatch ran. Cleared by a rewind archive (the
	// era reset).
	Candidate     string `json:"candidate,omitempty"`
	StepStartedAt string `json:"step_started_at,omitempty"`
	// Round counts the step's rounds: the step_start of a pending step
	// opens one (its first entry, and every re-entry a rewind's reset
	// ordered), and a gate's FAIL verdict that routed a rewind opens the
	// gate's next (the gate stays in_progress; closeGateRoundLocked); a
	// resume, a crash re-dispatch and an owner's message stay in the
	// round. Every dispatch of the round carries it as PF_ROUND, so a
	// command's result file can say which round wrote it. Never cleared.
	Round          int64     `json:"round,omitempty"`
	BudgetConsumed Budget    `json:"budget_consumed"`
	Attempts       []Attempt `json:"attempts"`
	// Supervision is the step's failure ledger and relaxed supervision
	// thresholds (v2-converge 2.3/2.4): execution.json is the single
	// authority for them, so a restart resumes the same counts.
	Supervision *StepSupervision `json:"supervision,omitempty"`
	// Dispatch is the derivation ordinal whose result the step awaits:
	// written by the step_start of that dispatch, cleared by the write
	// that settles it (an accepted result or a failed dispatch). A driver
	// starting with it equal to the current ordinal settles the envelope
	// a dead predecessor already collected instead of dispatching again.
	Dispatch int64 `json:"dispatch,omitempty"`
	// StartHeads are the task repos' HEADs when the step's current round
	// opened (workflow contract 4): written by the round's first
	// step_start, read by the daemon's `pf step handoff` stamp as each
	// repo's start commit, so a section's span is its round's own work.
	// Nil = none recorded.
	StartHeads *StartHeads `json:"start_heads,omitempty"`
}

// StartHeads are the task repos' HEADs (repo name → commit) at the start
// of the step round Round.
type StartHeads struct {
	Round int64             `json:"round"`
	Heads map[string]string `json:"heads"`
}

// OwnerRewindRequest is a rewind the owner's word orders (a step's
// hand-off `rewind --to <step> --per-owner`, workflow contract 4): the work
// item rewinds to To, an agent step before the current one, and Reason —
// the owner's message and the step's reading of it — is recorded on the
// rewind ledger. By names the owner.
type OwnerRewindRequest struct {
	To     string
	Reason string
	By     string
}

// StepSupervision is one step's driver-owned supervision state.
type StepSupervision struct {
	// FailureCount counts failed dispatches since the last reset (gate
	// rewind into the step, switch to a new candidate, human continue).
	FailureCount int `json:"failure_count,omitempty"`
	// LongTaskTimeouts counts the timeout-class verdicts
	// (step_timeout_long_task and, since workflow contract 4, tool_hung)
	// since the last reset; the second one blocks the step.
	LongTaskTimeouts int `json:"long_task_timeouts,omitempty"`
	// SlowModelTimeouts counts step_timeout_slow_model rotations since the
	// same resets as LongTaskTimeouts; a later slow-model timeout is
	// archived as step_timeout_long_task, so the step rotates the
	// candidate on a timeout at most once per reset.
	SlowModelTimeouts int `json:"slow_model_timeouts,omitempty"`
	// RelaxedToolMs / RelaxedWallMs are the relaxed Y and T (each
	// relaxation doubles); zero = never relaxed.
	RelaxedToolMs int64 `json:"relaxed_tool_ms,omitempty"`
	RelaxedWallMs int64 `json:"relaxed_wall_ms,omitempty"`
	// NextDispatch shapes the next dispatch after a failure: "" resumes
	// the transcript, "fresh" starts a new conversation with the previous
	// transcript as read-only reference. Cleared by the next step_start.
	NextDispatch string `json:"next_dispatch,omitempty"`
	// ResumedAt stamps the owner's last continue of the blocked step
	// (RFC3339Nano). Like an answer, it opens a new streak era: the
	// attempts-ledger streaks (the model-class and EWR rotations, the
	// retry pacing) do not reach across it, and no backoff owed by an
	// earlier death survives it.
	ResumedAt string `json:"resumed_at,omitempty"`
}

// NextDispatchFresh is the StepSupervision.NextDispatch value that
// orders a fresh conversation.
const NextDispatchFresh = "fresh"

type Attempt struct {
	Cause          string          `json:"cause"`
	AttemptSeq     int             `json:"attempt_seq"`
	StartedAt      string          `json:"started_at"`
	ConsumedBudget *Budget         `json:"consumed_budget,omitempty"`
	Result         json.RawMessage `json:"result,omitempty"`
	ArchivedAt     string          `json:"archived_at,omitempty"`
	RewindGate     string          `json:"rewind_gate,omitempty"`
	InterruptedAt  string          `json:"interrupted_at,omitempty"`
	Detail         string          `json:"detail,omitempty"`
	// FailureCause is the supervision sub-class of an infra_failure
	// attempt (harness_silent, model_error, model_hung, tool_hung,
	// step_timeout_long_task, step_timeout_slow_model, exit_model_class,
	// exit_host_class, exit_other, driver_stalled, daemon_restart).
	FailureCause string `json:"failure_cause,omitempty"`
	// Candidate is the candidate string of the dispatch an infra_failure
	// attempt records (polyforge#33): the model-identity ledger the live
	// candidate pointer and the ended_without_result streak derive from.
	// Empty on every other attempt and on pre-#33 records.
	Candidate string `json:"candidate,omitempty"`
	// TreeHash is the capsule tree hash recorded when an infra_failure
	// attempt was archived: the re-dispatch fast path
	// compares the current tree against it. Empty on every other attempt
	// and on pre-feature records (the mismatch/no-hash path is exactly
	// the pre-feature behavior).
	TreeHash string `json:"tree_hash,omitempty"`
}

type Budget struct {
	WallMs int64 `json:"wall_ms"`
}

// RewindRecord is one rewind's ledger entry: the gate
// that fired it (the edge attribution), its target and its accounting
// class. The list is the single rewind ledger — Count is its tally
// (Count == len(Rewinds)).
//
// Class decides what the record counts toward: RewindCharged (every
// automatic rewind) counts toward the lane's max_rewinds — the runaway
// guard, held when a gate hands off fail (`pf step handoff` refuses a fail
// not citing the owner once it is spent); RewindOwner (a rewind the
// owner's word routed: a gate's fail or a rewind --to citing the owner)
// counts toward nothing.
//
// By, At and Reason are set on a rewind --to citing the owner only: who
// ordered it, when (RFC3339Nano) and the reason verbatim; Gate is then the
// step it was sent back from.
type RewindRecord struct {
	Gate   string `json:"gate"`
	ToStep string `json:"to_step"`
	Class  string `json:"class"`
	By     string `json:"by,omitempty"`
	At     string `json:"at,omitempty"`
	Reason string `json:"reason,omitempty"`
}

// The rewind accounting classes (RewindRecord.Class).
const (
	RewindCharged = "charged"
	RewindOwner   = "owner"
)

type RewindStats struct {
	Count   int64          `json:"count"`
	Rewinds []RewindRecord `json:"rewinds"`
}

// MaxBlobBytes is the pre-receive blob size limit for the capsule chain
// (default 16 MiB; the secret scanner is gone per v1 ruling C1 (archive/v1-architecture-spec.md) but size
// stays bounded).
const MaxBlobBytes = 16 << 20

// MaxTaskBlobBytes is the pre-receive blob size limit for the task-repo
// carrier refs (refs/pf-task/*). Their history is the task repo's own,
// upstream history included (the first carrier push of a repo carries
// all of it), so it is not the capsule's to keep small: an upstream
// asset above MaxBlobBytes must not make a repo uncarriable
// (polyforge#158).
const MaxTaskBlobBytes = 100 << 20

// MaxGitRequestBodyBytes caps the git smart-HTTP proxy's request bodies
// (upload-pack and receive-pack alike) at exactly 1 GiB. The value is
// the owner's ruling: 10× the largest blob the hook accepts
// (MaxTaskBlobBytes = 100 MiB, so a shallow carrier's first push — a
// whole root-commit tree snapshot — fits with an order of magnitude to
// spare;), pinned at the binary GiB boundary the ruling
// names (the raw 10× product is 1000 MiB; the cap must not shrink
// below 1 GiB). It is a constant: no flag, config key or environment
// variable adjusts it (the git.Manager field merely lets tests inject a
// small value).
const MaxGitRequestBodyBytes = int64(1) << 30
