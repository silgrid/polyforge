package protocol

// watch.go: the wire shape of the per-WI watch snapshot (`pf watch <wi>
// --once`, the Pi Web panel's job detail; v2-converge §3). The daemon
// serves it over its UDS face:
//
//	GET  /jobs/{wi}/watch?after=<seq>          -> WatchSnapshot
//
// The machine's decision feed is decisions.go.

// WatchSnapshot is one WI's authoritative state plus the events after a
// cursor. Execution (execution.json) is the authority; Job is the daemon's
// local bookkeeping; Events carry only what happened after `after`.
type WatchSnapshot struct {
	WorkItem string `json:"work_item"`
	// HolderMachine is set only on a snapshot relayed from another
	// machine: the machine that produced it. The local
	// snapshot shape is otherwise field-for-field identical.
	HolderMachine string `json:"holder_machine,omitempty"`
	// Slug is the WI's "<project>#<seq>" display name, from the daemon's
	// job record or the aihub record — never agent text. Empty when
	// neither knows it; the human faces then show WorkItem.
	Slug string `json:"slug,omitempty"`
	// Known is false when the daemon holds neither a job record nor a
	// capsule for the WI — a WI submitted but not yet registered (queued /
	// preparing), or one this daemon never saw.
	Known bool `json:"known"`
	// Queued is true while the submit sits in the daemon's queue.
	Queued    bool            `json:"queued,omitempty"`
	Job       *WatchJob       `json:"job,omitempty"`
	Execution *WatchExecution `json:"execution,omitempty"`
	// HubStatus is the work item's aihub status, filled only when the
	// daemon no longer holds a live local job for it (closed by wrap /
	// wrap --fail / cancel / pause): execution.json is not rewritten by a
	// manual close and keeps saying `running`, so this is the only
	// authority for how the WI ended. Empty when not consulted or the hub
	// was unreachable.
	HubStatus string `json:"hub_status,omitempty"`
	// Progress is the optional live-progress block (current command, recent
	// files, tool-call count); every field is shown only when present.
	Progress *WatchProgress `json:"progress,omitempty"`
	// Summary is the last finished summarize line (polyforge#224) —
	// NARRATION, never authority: the panel may display it, but the
	// authoritative fields above are unchanged. No value while no call has
	// finished since the daemon started (the lane's state is memory-only).
	Summary *string `json:"summary,omitempty"`
	// SummaryError is the raw error text of the latest failed summarize
	// call — the harness's own stderr/exit wording, never faked. No value
	// after a success or when none failed.
	SummaryError *string `json:"summary_error,omitempty"`
	// SummaryAgeSec is the summary's age in seconds — present only
	// together with Summary.
	SummaryAgeSec *int64 `json:"summary_age_sec,omitempty"`
	// SummaryCapped is present and true only while the per-attempt cap
	// (20 calls per dispatch) is reached: the last summary stays and the
	// panel can display the cap honestly.
	SummaryCapped *bool        `json:"summary_capped,omitempty"`
	Events        []EventEntry `json:"events"`
	// LastSeq is the highest event seq the daemon retains for the WI (0 =
	// none): a cursor above it means the numbering restarted.
	LastSeq int64 `json:"last_seq"`
}

// WatchVerifyQueue is a job queued in the heavy-run admission: its
// 1-based position in the admission queue, the gate holding it and when
// it first queued (RFC3339). The status face's job row carries it
// (`/status` jobs[].verify_queue); the watch snapshot does not (the
// `verify_queued` event says it there).
type WatchVerifyQueue struct {
	Position int    `json:"position"`
	Reason   string `json:"reason"`
	Since    string `json:"since"`
}

// WatchJob is the daemon-side job bookkeeping of a WI.
type WatchJob struct {
	Status             string  `json:"status"`
	QuestionID         string  `json:"question_id,omitempty"`
	LastProgressAgoSec *int64  `json:"last_progress_ago_sec,omitempty"`
	Origin             *Origin `json:"origin,omitempty"`
}

// Origin is how a daemon job was submitted: its kind (chat = an
// interactive submit | auto) and the owner. It rides /status jobs[] and
// the watch snapshot.
type Origin struct {
	Kind  string `json:"kind"`
	Owner string `json:"owner,omitempty"`
}

// WatchExecution is the subset of execution.json the watcher reads:
// workflow status, current step, per-step status/start, pending question,
// plus the declared step structure of the WI's workflow.
type WatchExecution struct {
	WorkflowStatus  string                `json:"workflow_status"`
	CurrentStep     string                `json:"current_step"`
	UpdatedAt       string                `json:"updated_at,omitempty"`
	Steps           map[string]WatchStep  `json:"steps,omitempty"`
	PendingQuestion *WatchPendingQuestion `json:"pending_question,omitempty"`
	// Blocked is present only while the workflow is blocked
	// (workflow_status blocked): why, and what releases it.
	// A blocked work item asks nothing — it is resumed (`pf resume`) once
	// the required action is done — so a panel shows it as blocked, never
	// as a question.
	Blocked *WatchBlocked `json:"blocked,omitempty"`
	// DeclaredSteps is the step sequence of the workflow the WI runs (the
	// capsule's pinned bundle), in frontmatter declaration order — Steps
	// is a map and carries no order. Omitted when that workflow.yaml is
	// missing or unparseable.
	DeclaredSteps []WatchDeclaredStep `json:"declared_steps,omitempty"`
	// Rewinds counts the rewinds on record, kept apart:
	// the automatic ones a gate's on_fail spent (counted toward the lane's
	// max_rewinds) and the owner's (a hand-off citing the owner, charged
	// to no budget). Absent when the workflow is unreadable.
	Rewinds *WatchRewinds `json:"rewinds,omitempty"`
}

// WatchRewinds is the rewind tally: Agent of MaxRewinds (the lane's one
// rewind budget) spent by the automatic loops, Owner charged to no budget.
type WatchRewinds struct {
	Agent      int64 `json:"agent"`
	MaxRewinds int   `json:"max_rewinds"`
	Owner      int64 `json:"owner"`
}

// WatchDeclaredStep is one declared workflow step: its id and its gate
// FAIL rewind target (on_fail.target), an explicit null when it has none.
type WatchDeclaredStep struct {
	ID     string  `json:"id"`
	OnFail *string `json:"on_fail"`
}

// WatchStep is one step's status and start moment.
type WatchStep struct {
	Status    string `json:"status"`
	StartedAt string `json:"started_at,omitempty"`
}

// WatchPendingQuestion is the open question of a step — the current
// dispatch's open `pf step ask` (workflow contract v2 §5.2): ID and AskedAt
// are the ask's own stamp. Question is agent-written free text and is
// shown only by `pf watch <wi> --once`; the owner replies with
// `pf answer <wi> --text`. (The v1 question's option set left with
// workflow contract v2 change 19.)
type WatchPendingQuestion struct {
	ID       string `json:"id"`
	Step     string `json:"step"`
	Question string `json:"question"`
	AskedAt  string `json:"asked_at,omitempty"`
}

// WatchBlocked is a blocked workflow's cause. Kind is one of:
//   - "contract": the capsule's lane snapshot targets a workflow contract
//     version this daemon does not implement (a work item a contract
//     upgrade's drain left in flight); resuming parks it again — cancel
//     it and file it anew (docs/upgrade.md names the section);
//   - "incomplete": a step handed off `blocked` (Reason its reason,
//     Evidence its section of artifacts/handoff.md); a message wakes it;
//   - "write_failed": the pending hub writes of a `done` hand-off could
//     not be made; the resume replays the writes not made yet;
//   - "driver": the driver parked the workflow (a supervision verdict, a
//     dispatch it could not assemble, an owner's cancel in flight).
//
// Step is the blocked step; Reason why; RequiredAction what releases it;
// Evidence where the record is.
type WatchBlocked struct {
	Kind           string `json:"kind"`
	Step           string `json:"step,omitempty"`
	Reason         string `json:"reason"`
	RequiredAction string `json:"required_action,omitempty"`
	Evidence       string `json:"evidence,omitempty"`
}

// WatchProgress is the optional mechanical-progress block.
type WatchProgress struct {
	Command       string   `json:"command,omitempty"`
	CommandAgeSec *int64   `json:"command_age_sec,omitempty"`
	RecentFiles   []string `json:"recent_files,omitempty"`
	ToolCalls     *int64   `json:"tool_calls,omitempty"`
	// WaitCalls / WaitSeconds: the current step's wait calls (bash calls
	// that only `sleep N`, N > 3s, or that block in the `wait` builtin for
	// over 3s of measured time) and their summed seconds; absent when the
	// step has none.
	WaitCalls   *int64 `json:"wait_calls,omitempty"`
	WaitSeconds *int64 `json:"wait_seconds,omitempty"`
	// Context is the current step session's context-window usage;
	// absent when unknown.
	Context *ContextUsage `json:"context,omitempty"`
}

// ContextUsage is a running step session's context-window usage
// the tokens its conversation fills, the model's window
// and the percent when known, the model id and the transcript timestamp
// the count was taken from. Every field is optional: an unknown value is
// absent, never a guess.
type ContextUsage struct {
	Tokens  *int64   `json:"tokens,omitempty"`
	Window  *int64   `json:"window,omitempty"`
	Percent *float64 `json:"percent,omitempty"`
	Model   string   `json:"model,omitempty"`
	AsOf    string   `json:"as_of,omitempty"`
}
