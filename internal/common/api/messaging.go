package api

// messaging.go: the user-to-user messaging wire contract (workbench#11):
// one direct conversation per user pair, permanent append-only history,
// per-user read cursors, and the send/ack/analyze bodies.

// Message text bound (bytes, UTF-8) — over it is CORE-413.
const MessageTextMaxBytes = 4096

// Conversation kinds. Only direct 1:1 exists; no group is ever created.
const ConversationKindDirect = "direct"

// Conversation is one member-visible conversation row.
type Conversation struct {
	ID        string   `json:"id"`
	Kind      string   `json:"kind"`
	Members   []string `json:"members"`
	CreatedAt string   `json:"created_at"`
	LastSeq   int64    `json:"last_seq"`
	Unread    int      `json:"unread,omitempty"`
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

// AnalyzeResult is the daemon analyzer answer (read-only, never stored).
type AnalyzeResult struct {
	MessageID  string `json:"message_id"`
	Conclusion string `json:"conclusion"`
	Model      string `json:"model,omitempty"`
}
