package protocol

// watch.go: the wire shapes of the `pf watch` lane — the per-WI watch
// snapshot and the daemon-held chat subscription routes (v2-converge §3).
// The daemon serves them over its UDS face; the watcher decodes them.
//
//	GET  /jobs/{wi}/watch?after=<seq>          -> WatchSnapshot
//	GET  /chats/{id}/subscriptions             -> ChatSubscriptions
//	POST /chats/{id}/subscribe   {"wi"}        -> ChatSubscribeAnswer
//	POST /chats/{id}/unsubscribe {"wi"}        -> ChatSubscribeAnswer
//	POST /chats/{id}/ack         ChatAck       -> {"ok":true}
//	POST /chats/{id}/heartbeat   {"pid"}       -> {"ok":true}
//	GET  /chats/{id}/attention?owner=&mark=1[&ack=1] -> ChatAttention
//	POST /chats/{id}/attention/ack {"keys"}    -> {"ok":true}
//
// Delivery is fetch-then-ack on both decision paths, so a watcher that
// dies after fetching and before printing loses nothing:
//   - subscription decisions: the daemon-held cursor/notified set moves
//     only on POST ack, which the watcher sends after printing;
//   - attention notices: with ack=1 the daemon lists each notice with its
//     Key and keeps listing it on every fetch until POST attention/ack
//     names that key (acks are idempotent; unknown keys are ignored).
//     Without ack=1 (an older watcher), mark=1 marks the
//     listed notices shown at fetch, as before; an older daemon ignores
//     ack=1 and answers items without a Key, and a watcher acks nothing
//     then.
//
// A crash between printing and acking re-delivers: the next watcher
// start prints that line once more (at-least-once; the only duplicate).
// Within one watcher run a failed ack is retried without re-printing.

// WatchSnapshot is one WI's authoritative state plus the events after a
// cursor. Execution (execution.json) is the authority; Job is the daemon's
// local bookkeeping; Events carry only what happened after `after`.
type WatchSnapshot struct {
	WorkItem string `json:"work_item"`
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

// WatchJob is the daemon-side job bookkeeping of a WI.
type WatchJob struct {
	Status             string  `json:"status"`
	QuestionID         string  `json:"question_id,omitempty"`
	LastProgressAgoSec *int64  `json:"last_progress_ago_sec,omitempty"`
	Origin             *Origin `json:"origin,omitempty"`
}

// Origin is how a daemon job was submitted: its kind (chat | auto), the
// submitting chat id and the owner. It rides /status jobs[] and the
// watch snapshot.
type Origin struct {
	Kind   string `json:"kind"`
	ChatID string `json:"chat_id,omitempty"`
	Owner  string `json:"owner,omitempty"`
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
	// DeclaredSteps is the step sequence of the workflow the WI runs (the
	// capsule's pinned bundle), in frontmatter declaration order — Steps
	// is a map and carries no order. Omitted when that workflow.md is
	// missing or unparseable.
	DeclaredSteps []WatchDeclaredStep `json:"declared_steps,omitempty"`
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

// WatchPendingQuestion is the open question of a step; Question is
// agent-written free text and is shown only by `pf watch <wi> --once`.
type WatchPendingQuestion struct {
	ID       string `json:"id"`
	Step     string `json:"step"`
	Question string `json:"question"`
	AskedAt  string `json:"asked_at,omitempty"`
}

// WatchProgress is the optional mechanical-progress block.
type WatchProgress struct {
	Command       string   `json:"command,omitempty"`
	CommandAgeSec *int64   `json:"command_age_sec,omitempty"`
	RecentFiles   []string `json:"recent_files,omitempty"`
	ToolCalls     *int64   `json:"tool_calls,omitempty"`
}

// ChatSub is one daemon-held subscription of a chat to a WI.
type ChatSub struct {
	WI string `json:"wi"`
	// Slug is the WI's display name, recorded at subscribe time (empty
	// when unknown then).
	Slug string `json:"slug,omitempty"`
	// Cursor is the last event seq the chat's watcher consumed.
	Cursor int64 `json:"cursor"`
	// Notified lists the state-derived decision keys already delivered
	// (e.g. "needs_input:q-1", "completed"); replaced wholesale on ack.
	Notified []string `json:"notified,omitempty"`
	// Done is set once the WI's terminal decision was delivered: the
	// watcher stops polling it.
	Done bool `json:"done,omitempty"`
	// OriginChat is set on a subscription owner routing added to a chat
	// that did not submit the WI: the submitting (origin) chat, the one
	// the WI's decisions wake (polyforge#199). While the origin chat holds
	// a subscription to the WI, this one shares its delivery record:
	// Cursor, Notified and Done are answered from it and acks land in it,
	// so a decision is delivered once for both chats. Empty = this chat
	// is a wake target with its own record (it submitted the WI,
	// subscribed explicitly, or the WI has no origin chat).
	OriginChat string `json:"origin_chat,omitempty"`
	// Passive is computed per subscriptions answer, never stored: another
	// chat is woken for this WI (the origin chat while its watcher is
	// live, else the fallback chat), so this chat's watcher keeps the
	// WI's progress, prints none of its decisions and acks nothing.
	Passive bool `json:"passive,omitempty"`
}

// ChatSubscriptions is the GET /chats/{id}/subscriptions answer, and the
// POST /chats/{id}/subscribe-owned answer (the subscriptions it added).
type ChatSubscriptions struct {
	ChatID        string    `json:"chat_id"`
	Subscriptions []ChatSub `json:"subscriptions"`
}

// ChatSubscribeAnswer answers subscribe/unsubscribe.
type ChatSubscribeAnswer struct {
	ChatID  string `json:"chat_id"`
	WI      string `json:"wi"`
	Slug    string `json:"slug,omitempty"`
	Changed bool   `json:"changed"`
}

// ChatAck records a watcher's progress for one subscription: the new
// event cursor, the full set of delivered state keys, and whether the WI
// finished.
type ChatAck struct {
	WI       string   `json:"wi"`
	Cursor   int64    `json:"cursor"`
	Notified []string `json:"notified,omitempty"`
	Done     bool     `json:"done,omitempty"`
}
