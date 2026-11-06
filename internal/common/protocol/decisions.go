package protocol

// decisions.go: the wire shapes of the daemon's decision feed
// the machine-level record of every human decision the
// daemon observed (a question waiting for an answer, a blocked or failed
// work item, a dropped submit, a finished background task, …), read by the
// Pi Web Polyforge panel and `pf watch decisions`. It replaced the
// per-chat subscription routes: nothing is routed to a chat; the feed is
// one list per Host, every entry naming its Cell.
//
//	GET  /decisions                          -> DecisionFeed
//	POST /decisions/dismiss  DecisionDismiss -> DecisionDismissAnswer

// Decision kinds.
const (
	DecisionWorkItem = "work_item"
	DecisionTask     = "task"
)

// Decision is one feed entry. Class is the closed decision name (the
// daemon's decision table, watcher.Classify); Source is the raw daemon
// event kind, workflow status or aihub status it came from (e.g. class
// "failed" from source "failed_gate"). Phrase is the table's fixed
// sentence — never text an agent wrote. Text is set only on a task entry:
// the task's terminal summary.
type Decision struct {
	ID         int64  `json:"id"`
	At         string `json:"at"`
	Kind       string `json:"kind"`
	Profile    string `json:"profile,omitempty"`
	WI         string `json:"wi,omitempty"`
	Slug       string `json:"slug,omitempty"`
	TaskID     string `json:"task_id,omitempty"`
	Class      string `json:"class"`
	Source     string `json:"source"`
	Phrase     string `json:"phrase"`
	QuestionID string `json:"question_id,omitempty"`
	Repo       string `json:"repo,omitempty"`
	Text       string `json:"text,omitempty"`
	// Attention: the class needs a human (the table decides).
	Attention bool `json:"attention"`
	// Resolved: the state the entry announced is gone (the question was
	// answered, the work item runs again or reached a later state).
	Resolved bool `json:"resolved"`
	// Dismissed: a human marked the entry seen.
	Dismissed bool `json:"dismissed"`
	// Replied: the owner replied to the question the entry announced (a
	// reply whose ask_at names its QuestionID); it no longer needs a human
	// (Attention false) but stays unresolved until the step hands over.
	// RepliedAt is the reply's own stamp.
	Replied   bool   `json:"replied"`
	RepliedAt string `json:"replied_at,omitempty"`
}

// Open reports whether the entry still needs a human.
func (d Decision) Open() bool { return d.Attention && !d.Resolved && !d.Dismissed }

// DecisionFeed is GET /decisions' answer: the retained entries, newest
// first, and the number of open ones.
type DecisionFeed struct {
	Decisions []Decision `json:"decisions"`
	Attention int        `json:"attention"`
}

// DecisionDismiss is POST /decisions/dismiss's body: the entry ids, or
// All for every open entry.
type DecisionDismiss struct {
	IDs []int64 `json:"ids,omitempty"`
	All bool    `json:"all,omitempty"`
}

// DecisionDismissAnswer answers a dismiss: how many entries it marked.
type DecisionDismissAnswer struct {
	Dismissed int `json:"dismissed"`
}
