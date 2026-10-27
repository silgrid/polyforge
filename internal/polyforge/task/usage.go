package task

// usage.go: the token-usage accumulator and text capture for the two
// harness streams (workbench#40).
//
//   - claude: `--output-format stream-json` records. An `assistant` record
//     carries message.usage (input_tokens + output_tokens +
//     cache_creation_input_tokens + cache_read_input_tokens); the final
//     `result` record carries an aggregate usage that is counted only
//     when no assistant usage was seen (so the total is never doubled).
//   - pi: `--mode json` records. A `message_update` record's top-level
//     usage.totalTokens is cumulative PER TURN; the max seen in a turn is
//     added to the running total at the turn boundary.
//
// A harness that emits no usage record leaves Tokens at 0; the manager
// refuses such a candidate before it starts (SupportsUsage), so a task
// can never run unmeasured.

import (
	"bytes"
	"encoding/json"
	"strings"
)

// SupportsUsage reports whether a harness reports token usage on its
// streamed output. Both harnesses of the closed set do.
func SupportsUsage(h string) bool {
	switch h {
	case harnessPi, harnessClaude:
		return true
	}
	return false
}

const (
	harnessPi     = "pi"
	harnessClaude = "claude"
)

// usageCounts is the shared usage object of claude and pi records.
type usageCounts struct {
	Input       int64 `json:"input_tokens"`
	Output      int64 `json:"output_tokens"`
	CacheCreate int64 `json:"cache_creation_input_tokens"`
	CacheRead   int64 `json:"cache_read_input_tokens"`
	TotalTokens int64 `json:"totalTokens"`
}

func (u usageCounts) claudeSum() int64 {
	return u.Input + u.Output + u.CacheCreate + u.CacheRead
}

// StreamParser feeds on one harness stdout record at a time. It is not
// safe for concurrent use (one runner owns one parser).
type StreamParser struct {
	harness string

	total       int64           // flushed turns (pi) / accumulated messages (claude)
	turnMax     int64           // pi's current turn's cumulative totalTokens
	claudeUsage bool            // an assistant usage was seen (the result aggregate is then skipped)
	claudeSeen  map[string]bool // claude assistant message ids whose usage was already counted

	claudeAssistant []string // assistant message texts, in order
	claudeResult    string   // the final result text (authoritative when non-empty)
	piTurns         []string
	piDeltas        strings.Builder // fallback when no message_end text arrives
}

// NewStreamParser answers the parser of one harness stream.
func NewStreamParser(harness string) *StreamParser {
	return &StreamParser{harness: harness, claudeSeen: map[string]bool{}}
}

// Tokens answers the usage seen so far (pi includes the unflushed turn).
func (p *StreamParser) Tokens() int64 {
	if p.harness == harnessPi {
		return p.total + p.turnMax
	}
	return p.total
}

// Text answers the captured assistant text so far.
func (p *StreamParser) Text() string {
	if p.harness == harnessPi {
		if len(p.piTurns) > 0 {
			return strings.TrimSpace(strings.Join(p.piTurns, "\n\n"))
		}
		return strings.TrimSpace(p.piDeltas.String())
	}
	if strings.TrimSpace(p.claudeResult) != "" {
		return strings.TrimSpace(p.claudeResult)
	}
	return strings.TrimSpace(strings.Join(p.claudeAssistant, "\n\n"))
}

// Feed consumes one JSONL record. Malformed records are ignored (a
// harness diagnostic line must not abort the accumulation).
func (p *StreamParser) Feed(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return
	}
	if p.harness == harnessPi {
		p.feedPi(line)
		return
	}
	p.feedClaude(line)
}

type piTaskRecord struct {
	Type string `json:"type"`
	// message_update top-level usage
	Usage *usageCounts `json:"usage"`
	// message_update
	AssistantMessageEvent struct {
		Type  string `json:"type"`
		Delta string `json:"delta"`
	} `json:"assistantMessageEvent"`
	// message_end / turn_end message
	Message json.RawMessage `json:"message"`
}

func (p *StreamParser) feedPi(line []byte) {
	var rec piTaskRecord
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	switch rec.Type {
	case "turn_start":
		p.flushPiTurn()
	case "message_update":
		if rec.Usage != nil && rec.Usage.TotalTokens > p.turnMax {
			p.turnMax = rec.Usage.TotalTokens
		}
		if rec.AssistantMessageEvent.Type == "text_delta" {
			p.piDeltas.WriteString(rec.AssistantMessageEvent.Delta)
		}
	case "message_end":
		if text, ok := messageText(rec.Message); ok {
			p.piTurns = append(p.piTurns, text)
		}
		p.flushPiTurn()
	case "turn_end":
		p.flushPiTurn()
	case "agent_end":
		p.flushPiTurn()
	}
}

func (p *StreamParser) flushPiTurn() {
	p.total += p.turnMax
	p.turnMax = 0
}

type claudeTaskRecord struct {
	Type    string          `json:"type"`
	Usage   *usageCounts    `json:"usage"`
	Message json.RawMessage `json:"message"`
	Result  string          `json:"result"`
}

func (p *StreamParser) feedClaude(line []byte) {
	var rec claudeTaskRecord
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	switch rec.Type {
	case "assistant":
		if rec.Message != nil {
			var msg struct {
				ID      string          `json:"id"`
				Role    string          `json:"role"`
				Content json.RawMessage `json:"content"`
				Usage   *usageCounts    `json:"usage"`
			}
			if json.Unmarshal(rec.Message, &msg) == nil {
				// One API message is emitted as several content-block
				// records, each repeating the SAME usage; count it once per
				// message id (polyforge#40 review N3).
				if msg.Usage != nil && (msg.ID == "" || !p.claudeSeen[msg.ID]) {
					p.total += msg.Usage.claudeSum()
					p.claudeUsage = true
					if msg.ID != "" {
						p.claudeSeen[msg.ID] = true
					}
				}
				if text, ok := contentText(msg.Content); ok && strings.TrimSpace(text) != "" {
					p.claudeAssistant = append(p.claudeAssistant, text)
				}
			}
		}
	case "result":
		if rec.Result != "" {
			p.claudeResult = rec.Result
		}
		if rec.Usage != nil && !p.claudeUsage {
			p.total += rec.Usage.claudeSum()
			p.claudeUsage = true
		}
	}
}

// messageText extracts the concatenated text blocks of a message object
// ({role, content:[{type:text,text}]}). ok=false when the message carries
// no text block (a tool-only assistant turn).
func messageText(raw json.RawMessage) (string, bool) {
	if raw == nil {
		return "", false
	}
	var msg struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &msg) != nil || msg.Role != "assistant" {
		return "", false
	}
	return contentText(msg.Content)
}

// contentText extracts the text blocks of a content array (or a plain
// string content).
func contentText(raw json.RawMessage) (string, bool) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", false
	}
	if raw[0] == '"' {
		var s string
		if json.Unmarshal(raw, &s) == nil {
			return s, true
		}
		return "", false
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return "", false
	}
	var b strings.Builder
	found := false
	for _, blk := range blocks {
		if blk.Type == "text" && blk.Text != "" {
			if found {
				b.WriteString("\n")
			}
			b.WriteString(blk.Text)
			found = true
		}
	}
	return b.String(), found
}
