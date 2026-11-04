package api

// comments.go: append-only work-item comments. A comment
// carries its author, a server-assigned time, the body and an optional
// reply_to naming an earlier comment on the SAME work item. There is no
// edit and no delete: the only write is the append (POST
// /api/v1/work-items/{id}/comments).

// Comment is one stored comment row on a work item.
type Comment struct {
	ID        string `json:"id"`
	Author    string `json:"author"`
	Body      string `json:"body"`
	ReplyTo   string `json:"reply_to,omitempty"`
	CreatedAt string `json:"created_at"`
}

// AddCommentRequest is the POST /api/v1/work-items/{id}/comments body.
// reply_to is optional and must name a comment that already exists on the
// addressed work item; anything else is refused and nothing is written.
type AddCommentRequest struct {
	Body    string `json:"body"`
	ReplyTo string `json:"reply_to,omitempty"`
}
