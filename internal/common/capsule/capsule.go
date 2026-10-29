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
//   - .gitignore covers only repos/ and artifacts/release-*/ (v1 ruling C1 (archive/v1-architecture-spec.md):
//     transcripts go INTO git; secret scanning is dropped).
//   - session-data/ side upload is deleted; transcripts are committed like
//     any other capsule file.
package capsule

import "encoding/json"

// Capsule directory layout (relative to capsule root).
const (
	DirArtifacts    = "artifacts"     // step outputs; release trees under artifacts/release-*/
	DirLogs         = "logs"          // daemons logs, step journals
	DirRepos        = "repos"         // task repo checkouts (own git histories)
	DirRuntimeState = "runtime-state" // execution.json, journal, spawn.log
	DirSession      = "session"       // harness transcripts synced at boundaries
	DirWorkflows    = "workflows"     // materialized workflow bundle
	CapsuleJSON     = "capsule.json"  // capsule identity
	WorkItemJSON    = "runtime-state/work-item.json"
	ExecutionJSON   = "runtime-state/execution.json"
	// RecalledMemoriesJSON is the claim-time memory snapshot the daemon
	// writes and the driver renders into every step prompt.
	RecalledMemoriesJSON = "runtime-state/recalled-memories.json"
	// MemoryPolicyJSON is the claimed project's effective memory policy
	// (the workspace yaml's memory_policy, else the built-in default),
	// written by the daemon at claim and rendered into every step prompt.
	MemoryPolicyJSON = "runtime-state/memory-policy.json"
)

// GitignoreContent is the .gitignore written at capsule init (v1 ruling C1 (archive/v1-architecture-spec.md)).
const GitignoreContent = "/repos/\n/artifacts/release-*/\n"

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
	WorkflowStatusRunning               = "running"
	WorkflowStatusCompleted             = "completed"
	WorkflowStatusFailedGate            = "failed_gate"
	WorkflowStatusFailedRewindExhausted = "failed_rewind_exhausted"
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
	ReviewRounds   map[string]int64 `json:"review_rounds"`
	RewindStats    RewindStats      `json:"rewind_stats"`
	BudgetConsumed Budget           `json:"budget_consumed"`
	UpdatedAt      string           `json:"updated_at"`
	Version        int64            `json:"version"`
	// ScopeTier is the verification tier the work item runs at
	//: "scoped" or "full" once derived, "" before the
	// first verification dispatch. The driver records it once at that
	// dispatch (from the work-item marker and the lane) and restores it
	// unchanged on every later re-dispatch — a fresh retry never recomputes
	// the tier nor asks again. The deliver 「全量重验」 rewind overwrites it
	// to "full".
	ScopeTier string `json:"scope_tier,omitempty"`
	// BlockedReason names why the driver parked the workflow blocked on
	// its own authority (host-class fault, failure counter at its bound);
	// empty when blocked on an honest incomplete result or not blocked.
	BlockedReason string `json:"blocked_reason,omitempty"`
}

// Step statuses.
const (
	StepPending    = "pending"
	StepInProgress = "in_progress"
	StepDone       = "done"
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
	Candidate      string           `json:"candidate,omitempty"`
	Output         map[string]any   `json:"output,omitempty"`
	StepStartedAt  string           `json:"step_started_at,omitempty"`
	BudgetConsumed Budget           `json:"budget_consumed"`
	Attempts       []Attempt        `json:"attempts"`
	Answers        []ArchivedAnswer `json:"answers"`
	// AnswerBase is len(Answers) at the step's last gate-fail rewind: the
	// answers at index >= AnswerBase form the step's CURRENT attempt chain
	// (the human-gate guarantee counts only those). Zero until a rewind.
	AnswerBase int `json:"answer_base,omitempty"`
	// PendingQuestion is the step's open question (D8/D9): the durable
	// waiting marker while a human is asked; the answer transition archives
	// the pair and clears it in one atomic write.
	PendingQuestion *PendingQuestion `json:"pending_question,omitempty"`
	// PendingRewindInput is the round's gate FAIL verdict: written by the
	// rewind (the same atomic write that resets the step to pending) and
	// read by every dispatch of the round — the first one, a retry after
	// an infra death, a restart after a daemon death. It is
	// cleared by the acceptance write that ends the round and replaced by
	// the next rewind or reset.
	PendingRewindInput *RewindInput `json:"pending_rewind_input,omitempty"`
	// LastReviewRound is the review step's last
	// verdict-bearing round's facts — the convergence baseline the next
	// review FAIL's residual-of judgment compares against. Survives
	// rewinds (the record of the round that produced them); cleared by
	// nothing except the next verdict-bearing round overwriting it.
	LastReviewRound *ReviewRound `json:"last_review_round,omitempty"`
	// JudgedRepoHash is the task-repo tree hash a GATE step judged when it
	// last delivered a verdict (PASS or FAIL), driver-written at that
	// settlement ( criterion 6). A gate re-entered after a
	// rewind of the lane's build step whose target resubmitted the same
	// code tree is not re-run: the driver blocks it with a human-visible
	// reason naming the crashed build attempt. It covers repos/ only (not
	// driver-owned artifacts/steps archives, which move every round).
	// Empty for non-gates and before the gate's first verdict. When the
	// last verdict was delivered is derived from the attempts ledger (the
	// gate's newest accepted attempt, or the rewind archive naming it), not
	// persisted a second time.
	JudgedRepoHash string `json:"judged_repo_hash,omitempty"`
	// PendingLightRecheck is the mechanical fast lane's pending light
	// recheck marker (polyforge#260): set by the mechanical rewind's own
	// atomic write, the durable carrier of the fast-lane state after the
	// FAIL verdict's output is replaced — its presence makes the
	// verification step's next dispatch the light recheck. Cleared by
	// whichever write settles the light recheck (light pass or escalation)
	// and by every rewind-range reset (a reset step never carries a stale
	// marker).
	PendingLightRecheck *LightRecheck `json:"pending_light_recheck,omitempty"`
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
	// EnvelopeRejects is the step's consecutive envelope-rejection count
	// (the in-process retry lane's bound): incremented by the driver's
	// rejection write, reset by an accepted result, a rewind reset or an
	// owner continue. EnvelopeRejectSpawnN is the ordinal the last counted
	// rejection belonged to, so a restart that re-collects the same
	// archived envelope does not count it twice.
	EnvelopeRejects      int   `json:"envelope_rejects,omitempty"`
	EnvelopeRejectSpawnN int64 `json:"envelope_reject_spawn_n,omitempty"`
}

// StepSupervision is one step's driver-owned supervision state.
type StepSupervision struct {
	// FailureCount counts failed dispatches since the last reset (gate
	// rewind into the step, switch to a new candidate, human continue).
	FailureCount int `json:"failure_count,omitempty"`
	// LongTaskTimeouts counts step_timeout_long_task verdicts since the
	// last reset; the second one blocks the step.
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

type RepoRef struct {
	Repo string `json:"repo"`
	SHA  string `json:"sha"`
}

type RewindInput struct {
	ToStep string             `json:"to_step"`
	Note   string             `json:"note"`
	Refs   map[string]RepoRef `json:"refs"`
	Reason string             `json:"reason"`
}

// LightRecheck is the pending light recheck marker (polyforge#260, the
// mechanical fast lane): the durable carrier of the fast-lane state after
// the FAIL verdict's output is replaced. Bound is the FAIL verdict's
// mechanical classification copied verbatim at the mechanical rewind
// write (never re-typed by any step agent); DiffBase is each task repo's
// HEAD at the FAIL verdict's settlement (driver-recorded at the rewind
// write, never step-supplied) — the light recheck's deterministic
// repair-diff base.
type LightRecheck struct {
	Bound    json.RawMessage   `json:"bound"`
	DiffBase map[string]string `json:"diff_base"`
}

type PendingQuestion struct {
	ID       string          `json:"id"`
	Step     string          `json:"step"`
	Question string          `json:"question"`
	Context  json.RawMessage `json:"context,omitempty"`
	AskedAt  string          `json:"asked_at"`
}

type ArchivedAnswer struct {
	QuestionID string `json:"question_id"`
	Answer     string `json:"answer"`
	AnsweredBy string `json:"answered_by"`
	AnsweredAt string `json:"answered_at"`
}

// RewindRecord is one rewind's ledger entry: the gate
// that fired it (the edge attribution), its on_fail target and whether
// it charged that gate→target edge's max_per_step bound. The list is the
// single rewind ledger — Count is its tally — and a ledger that cannot
// attribute its own count (Count > len(Rewinds), the pre-w69 shape with
// no per-record history) proves no per-edge quota: the next rewind
// fails closed (no compat path, no guess).
type RewindRecord struct {
	Gate    string `json:"gate"`
	ToStep  string `json:"to_step"`
	Charged bool   `json:"charged"`
}

// ReviewRound is the last verdict-bearing review
// round's facts: the round ordinal (execution.json's review_rounds count),
// its blocking-findings count, and the task-repo HEADs the round judged
// (code review: recorded for EVERY verdict-bearing round — a FAIL names
// the tree it failed, a PASS the tree it covered — so the next
// re-entry's fix surface always has an anchor that survives rewinds,
// unlike reviewed_heads which a PASS alone writes). Written only by
// the settlement of a review round that carried a verdict — a pass
// completion or a gate FAIL — in that same atomic write; a needs_input
// or incomplete round records nothing. The converging-round quota
// judgment compares the next FAIL's residual-marked blocking findings
// against it.
type ReviewRound struct {
	Round    int64             `json:"round"`
	Blocking int64             `json:"blocking"`
	Heads    map[string]string `json:"heads,omitempty"`
}

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
