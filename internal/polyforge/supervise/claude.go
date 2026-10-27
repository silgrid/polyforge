package supervise

// claude.go: the translator of Claude Code's headless stream
// (`claude -p --bare|--safe-mode --output-format stream-json --verbose
// --include-partial-messages`, polyforge#159). One JSON record per line:
//
//	{"type":"system","subtype":"init",...}            session ready, first model call
//	{"type":"stream_event","event":{"type":...}}       token-level partial messages:
//	    message_start / content_block_start / message_delta / message_stop / ping,
//	    content_block_delta{delta.type: thinking_delta | text_delta | input_json_delta}
//	{"type":"assistant","message":{"content":[...tool_use...]},"error":"<category>"?}
//	{"type":"user","message":{"content":[...tool_result...]}}
//	{"type":"system","subtype":"api_retry","attempt","max_retries","retry_delay_ms","error_status","error"}
//	{"type":"result","subtype":"success"|...,"is_error","result","api_error_status","terminal_reason","errors":[...]}
//
// Any record carrying a non-empty parent_tool_use_id belongs to a
// sub-agent running inside the outer `Agent` tool: it proves life only
// (the outer tool call is the one tool execution, the pi sub-agent
// parity), so inner tools never move the tool_hung bookkeeping.
//
// D7 (design §3): Claude reports failures STRUCTURALLY (an error
// category, an HTTP status, a terminal reason). The translator rewrites
// them into one canonical text —
//
//	claude-code <phrase> (HTTP <status>, terminal_reason=<r>): <message>
//
// — whose <phrase> lands in the class the driver's existing exit
// classifier expects (rate limit → same-model backoff, auth/billing →
// switch, model not found → switch, 5xx → bounded same-model retry, the
// rest → the error-turn fallback). The classifier itself is unchanged.

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// ClaudeTranslator is the Claude Code stream-json translator.
type ClaudeTranslator struct {
	sink Sink
	// lastCategory / lastText: the newest API-error assistant message,
	// the result record's fallback when it carries no detail of its own.
	lastCategory string
	lastText     string
}

// NewClaude builds a Claude Code translator feeding sink.
func NewClaude(sink Sink) *ClaudeTranslator { return &ClaudeTranslator{sink: sink} }

// Name implements Translator.
func (c *ClaudeTranslator) Name() string { return "claude" }

// Granularity implements Translator: --include-partial-messages streams
// token deltas.
func (c *ClaudeTranslator) Granularity() Granularity { return GranularityToken }

// Exit implements Translator.
func (c *ClaudeTranslator) Exit(info ExitInfo) { c.sink.ProcessExit(info) }

type claudeBlock struct {
	Type      string          `json:"type"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	ToolUseID string          `json:"tool_use_id"`
	Text      string          `json:"text"`
}

type claudeRecord struct {
	Type            string          `json:"type"`
	Subtype         string          `json:"subtype"`
	ParentToolUseID *string         `json:"parent_tool_use_id"`
	Event           json.RawMessage `json:"event"`
	Message         struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	// assistant: the error category of an API-error message
	Error json.RawMessage `json:"error"`
	// api_retry
	ErrorStatus *int `json:"error_status"`
	// result
	IsError        bool     `json:"is_error"`
	Result         string   `json:"result"`
	APIErrorStatus *int     `json:"api_error_status"`
	TerminalReason string   `json:"terminal_reason"`
	Errors         []string `json:"errors"`
}

// Feed implements Translator.
func (c *ClaudeTranslator) Feed(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var rec claudeRecord
	if json.Unmarshal(line, &rec) != nil || rec.Type == "" {
		c.sink.Noise()
		return
	}
	if rec.ParentToolUseID != nil && *rec.ParentToolUseID != "" {
		if rec.Type == "system" || rec.Type == "rate_limit_event" {
			c.sink.Noise()
		} else {
			c.sink.Alive() // sub-agent internals: life only
		}
		return
	}
	switch rec.Type {
	case "system":
		switch rec.Subtype {
		case "init":
			c.sink.Alive()
			c.sink.WaitingModel()
		case "api_retry":
			// Claude's own bounded retry: a NEW request starts (the wait
			// clock restarts — never a model_hung), the error is evidence
			// only; D7 takes over once Claude gives up (the result record).
			c.sink.Alive()
			c.sink.WaitingModel()
			c.sink.ErrorSeen(ClaudeCanonicalError(rawCategory(rec.Error), intOf(rec.ErrorStatus), "", rawMessage(rec.Error)))
		default:
			c.sink.Noise() // task_*, permission_denied, status, compaction chatter
		}
	case "stream_event":
		var ev struct {
			Type  string `json:"type"`
			Delta struct {
				Type string `json:"type"`
			} `json:"delta"`
		}
		_ = json.Unmarshal(rec.Event, &ev)
		if ev.Type == "content_block_delta" {
			switch ev.Delta.Type {
			case "thinking_delta":
				c.sink.ModelOutput(OutputThinking)
				return
			case "text_delta":
				c.sink.ModelOutput(OutputText)
				return
			case "input_json_delta":
				c.sink.ModelOutput(OutputToolCall)
				return
			}
		}
		// message_start / content_block_start / message_delta /
		// message_stop / ping: life, never output (model_hung reads output)
		c.sink.Alive()
	case "assistant":
		c.sink.Alive()
		blocks := decodeClaudeBlocks(rec.Message.Content)
		if cat := rawCategory(rec.Error); cat != "" {
			var text []string
			for _, b := range blocks {
				if b.Type == "text" && strings.TrimSpace(b.Text) != "" {
					text = append(text, strings.TrimSpace(b.Text))
				}
			}
			c.lastCategory, c.lastText = cat, strings.Join(text, " ")
			c.sink.ErrorSeen(ClaudeCanonicalError(cat, 0, "", c.lastText))
		}
		for _, b := range blocks {
			if b.Type == "tool_use" {
				c.sink.ToolStart(b.ID, b.Name, boundedArgs(b.Input))
			}
		}
	case "user":
		ended := false
		for _, b := range decodeClaudeBlocks(rec.Message.Content) {
			if b.Type == "tool_result" {
				c.sink.ToolEnd(b.ToolUseID, "")
				ended = true
			}
		}
		if ended {
			c.sink.WaitingModel() // the next request starts
		} else {
			c.sink.Alive()
		}
	case "result":
		if rec.Subtype == "success" && !rec.IsError {
			c.sink.TurnEnded(true, "")
			return
		}
		msg := strings.TrimSpace(rec.Result)
		if len(rec.Errors) > 0 {
			msg = strings.TrimSpace(strings.Join(append([]string{msg}, rec.Errors...), " "))
		}
		if msg == "" {
			msg = c.lastText
		}
		cat := c.lastCategory
		if cat == "" && rec.Subtype != "" && rec.Subtype != "success" {
			cat = rec.Subtype
		}
		c.sink.TurnEnded(false, ClaudeCanonicalError(cat, intOf(rec.APIErrorStatus), rec.TerminalReason, msg))
	default:
		// rate_limit_event and anything unknown: not work
		c.sink.Noise()
	}
}

func decodeClaudeBlocks(raw json.RawMessage) []claudeBlock {
	var blocks []claudeBlock
	if len(raw) > 0 && raw[0] == '[' {
		_ = json.Unmarshal(raw, &blocks)
	}
	return blocks
}

// rawCategory reads an error field that is a category string, or an
// object carrying one under "type" (tolerant of both shapes).
func rawCategory(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return strings.TrimSpace(s)
	}
	var o struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &o) == nil {
		return strings.TrimSpace(o.Type)
	}
	return ""
}

// rawMessage reads the message of an object-shaped error field.
func rawMessage(raw json.RawMessage) string {
	var o struct {
		Message string `json:"message"`
	}
	if len(raw) > 0 && raw[0] == '{' && json.Unmarshal(raw, &o) == nil {
		return strings.TrimSpace(o.Message)
	}
	return ""
}

func intOf(p *int) int {
	if p == nil {
		return 0
	}
	return *p
}

// ClaudeCanonicalError renders one Claude failure as the canonical D7
// text (design §3 table): the phrase is chosen from the STRUCTURED
// fields (category, HTTP status, terminal reason) so the driver's text
// classifier lands it in the intended class whatever the raw wording.
func ClaudeCanonicalError(category string, status int, terminalReason, message string) string {
	phrase := claudePhrase(category, status, terminalReason, message)
	var meta []string
	if status > 0 {
		meta = append(meta, fmt.Sprintf("HTTP %d", status))
	}
	if terminalReason != "" {
		meta = append(meta, "terminal_reason="+terminalReason)
	}
	out := "claude-code " + phrase
	if len(meta) > 0 {
		out += " (" + strings.Join(meta, ", ") + ")"
	}
	if m := strings.TrimSpace(message); m != "" {
		if len(m) > 400 {
			m = m[:400]
		}
		out += ": " + m
	}
	return out
}

func claudePhrase(category string, status int, terminalReason, message string) string {
	cat := strings.ToLower(strings.TrimSpace(category))
	low := strings.ToLower(message)
	switch {
	case cat == "rate_limit" || status == 429:
		return "rate limit (429)"
	case cat == "overloaded" || cat == "overloaded_error" || status == 529:
		return "overloaded (529)"
	case cat == "authentication_failed" || cat == "oauth_org_not_allowed" || cat == "account_on_hold" ||
		cat == "cloud_credential_error" || status == 401 || status == 403:
		return "authentication failed (401)"
	case cat == "billing_error" || status == 402 || strings.Contains(low, "credit balance is too low"):
		return "billing: credit balance too low"
	case cat == "model_not_found" || status == 404:
		return "model not found"
	case cat == "server_error" || (status >= 500 && status <= 599) ||
		strings.Contains(low, "server error mid-response") || strings.Contains(low, "no response from api") ||
		strings.Contains(low, "connection error"):
		return "upstream connection dropped (HTTP 5xx)"
	case terminalReason == "malformed_tool_use_exhausted":
		return "malformed tool call arguments"
	case cat != "":
		return cat
	case terminalReason != "":
		return terminalReason
	}
	return "error"
}
