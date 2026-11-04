package api

// messaging.go: the user-to-user messaging wire contract:
// one direct conversation per user pair, permanent append-only history,
// per-user read cursors, and the send/ack/analyze/prompt bodies.

// Message text bound (bytes, UTF-8) — over it is CORE-413.
const MessageTextMaxBytes = 4096

// Conversation kinds. Only direct 1:1 exists; no group is ever created.
const ConversationKindDirect = "direct"

// Conversation is one member-visible conversation row. MemberNames maps
// exactly the members' user ids to their display names:
// the scope is membership only, with no admin branch, no directory and no
// name resolution. A member with an empty display name keeps its key with
// an empty value, so the key set always equals Members.
type Conversation struct {
	ID          string            `json:"id"`
	Kind        string            `json:"kind"`
	Members     []string          `json:"members"`
	MemberNames map[string]string `json:"member_names"`
	CreatedAt   string            `json:"created_at"`
	LastSeq     int64             `json:"last_seq"`
	Unread      int               `json:"unread,omitempty"`
}

// Message is one append-only message row.
type Message struct {
	ID             string `json:"id"`
	Seq            int64  `json:"seq"`
	ConversationID string `json:"conversation_id"`
	SenderID       string `json:"sender_id"`
	Text           string `json:"text"`
	AboutWI        string `json:"about_wi,omitempty"`
	ReplyTo        string `json:"reply_to,omitempty"`
	CreatedAt      string `json:"created_at"`
}

// SendMessage is the POST /api/v1/messages body. There is deliberately
// no chat/WI/machine/broadcast address: `to` is a human user id.
type SendMessage struct {
	To      string `json:"to"`
	Text    string `json:"text"`
	AboutWI string `json:"about_wi,omitempty"`
	ReplyTo string `json:"reply_to,omitempty"`
}

// MessagePage is the conversation history answer.
type MessagePage struct {
	Messages []Message `json:"messages"`
	LastSeq  int64     `json:"last_seq"`
	AfterSeq int64     `json:"after_seq,omitempty"`
}

// ConversationsAnswer is the GET /api/v1/conversations answer.
type ConversationsAnswer struct {
	Conversations []Conversation `json:"conversations"`
}

// AckMessage is the POST /api/v1/messages/ack body: a per-message read
// cursor advance (max-advance, idempotent). The delivery route reuses the
// same body shape for POST /api/v1/messages/delivered.
type AckMessage struct {
	ConversationID string `json:"conversation_id"`
	Seq            int64  `json:"seq"`
}

// AnalyzeResult is the daemon analyzer answer: the
// structured result of one terminal analysis task, answered only to the
// caller (the task record is the only stored copy). Type is one of
// AnalyzeTypes; SuggestedActions is a possibly-empty subset of
// AnalyzeActions (always an array); ReplyDraft appears only beside a
// reply suggestion. Model is the task's candidate, Duration its wall time
// as a Go duration string, Project the project the task ran in.
type AnalyzeResult struct {
	MessageID        string   `json:"message_id"`
	TaskID           string   `json:"task_id"`
	Project          string   `json:"project"`
	Type             string   `json:"type"`
	Summary          string   `json:"summary"`
	SuggestedActions []string `json:"suggested_actions"`
	Model            string   `json:"model"`
	Duration         string   `json:"duration"`
	ReplyDraft       string   `json:"reply_draft,omitempty"`
}

// MessagePromptResult is the daemon's message-to-prompt answer
// the execution prompt one terminal read-only task wrote
// for one message, and that task's facts. These four fields are the whole
// wire shape (pi-web's sanitizer accepts exactly these keys); the task
// record is the only stored copy. Model is the task's candidate, Duration
// its wall time as a Go duration string.
type MessagePromptResult struct {
	Prompt   string `json:"prompt"`
	TaskID   string `json:"task_id"`
	Model    string `json:"model"`
	Duration string `json:"duration"`
}

// The analysis vocabularies are closed sets.
const (
	AnalyzeTypeOther        = "other"
	AnalyzeActionFileTicket = "file_ticket"
	AnalyzeActionReply      = "reply"
	AnalyzeActionNoteOnly   = "note_only"
)

// AnalyzeTypes is the closed set of analysis types.
var AnalyzeTypes = []string{"requirement", "bug", "ruling", "discussion", "clarification", "outreach", AnalyzeTypeOther}

// AnalyzeActions is the closed set of suggested actions.
var AnalyzeActions = []string{AnalyzeActionFileTicket, AnalyzeActionReply, AnalyzeActionNoteOnly}

// IsAnalyzeType reports whether s is one of AnalyzeTypes.
func IsAnalyzeType(s string) bool { return inSet(AnalyzeTypes, s) }

// IsAnalyzeAction reports whether s is one of AnalyzeActions.
func IsAnalyzeAction(s string) bool { return inSet(AnalyzeActions, s) }

func inSet(set []string, s string) bool {
	for _, v := range set {
		if v == s {
			return true
		}
	}
	return false
}
