package supervise

// pi.go: the translator of `pi --mode rpc` stdout. The record shapes
// are pinned from the harness source (agent-loop.js) and the frozen v0
// capture:
//
//	{"type":"agent_start"}                         agent loop begins
//	{"type":"turn_start"}                          a model call is about to start
//	{"type":"message_start","message":{...}}       a message begins (user/assistant)
//	{"type":"message_update","assistantMessageEvent":{"type":"thinking_delta"|"text_delta"|...}}
//	{"type":"message_end","message":{...}}
//	{"type":"tool_execution_start","toolCallId","toolName","args"}
//	{"type":"tool_execution_update",...}
//	{"type":"tool_execution_end","toolCallId","toolName",...}
//	{"type":"turn_end","message":{...},"toolResults":[...]}
//	{"type":"agent_end","messages":[...],"willRetry":false}
//
// A provider failure rides the ORDINARY records as
// message.stopReason == "error" + message.errorMessage (the harness
// emits no type:"error" record); the turn that ends on it closes with
// agent_end whose last message carries the same stopReason.
//
// What is NOT work (F15): extension_ui_request (UI chatter), the
// `response` command echo and the boot-time agent_settled — they prove
// the harness process is alive, never that the agent is working.

import (
	"bytes"
	"encoding/json"
	"strings"
)

// PiTranslator is the pi --mode rpc translator.
type PiTranslator struct {
	sink Sink
}

// NewPi builds a pi translator feeding sink.
func NewPi(sink Sink) *PiTranslator { return &PiTranslator{sink: sink} }

// Name implements Translator.
func (p *PiTranslator) Name() string { return "pi" }

// Granularity implements Translator: pi streams thinking_delta and
// text_delta tokens, so the harness is token-level.
func (p *PiTranslator) Granularity() Granularity { return GranularityToken }

// Exit implements Translator.
func (p *PiTranslator) Exit(info ExitInfo) { p.sink.ProcessExit(info) }

type piMessage struct {
	Role         string `json:"role"`
	StopReason   string `json:"stopReason"`
	ErrorMessage string `json:"errorMessage"`
}

type piRecord struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
	// messages (agent_end)
	Messages  []json.RawMessage `json:"messages"`
	WillRetry bool              `json:"willRetry"`
	// message_update
	AssistantMessageEvent struct {
		Type string `json:"type"`
	} `json:"assistantMessageEvent"`
	// tool_execution_*
	ToolCallID string          `json:"toolCallId"`
	ToolName   string          `json:"toolName"`
	Args       json.RawMessage `json:"args"`
}

// Feed implements Translator.
func (p *PiTranslator) Feed(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 {
		return
	}
	var rec piRecord
	if json.Unmarshal(line, &rec) != nil || rec.Type == "" {
		p.sink.Noise()
		return
	}
	switch rec.Type {
	case "agent_start":
		p.sink.Alive()
	case "turn_start":
		p.sink.Alive()
		p.sink.WaitingModel()
	case "message_start":
		p.sink.Alive()
		if m := decodePiMessage(rec.Message); m.Role == "assistant" && m.StopReason == "error" {
			p.sink.ErrorSeen(errorText(m))
		}
	case "message_update":
		switch rec.AssistantMessageEvent.Type {
		case "thinking_delta":
			p.sink.ModelOutput(OutputThinking)
		case "text_delta":
			p.sink.ModelOutput(OutputText)
		case "toolcall_delta":
			p.sink.ModelOutput(OutputToolCall)
		default:
			p.sink.Alive()
		}
	case "message_end", "turn_end":
		p.sink.Alive()
		if m := decodePiMessage(rec.Message); m.StopReason == "error" {
			p.sink.ErrorSeen(errorText(m))
		}
	case "tool_execution_start":
		p.sink.ToolStart(rec.ToolCallID, rec.ToolName, boundedArgs(rec.Args))
	case "tool_execution_update":
		p.sink.Alive()
	case "tool_execution_end":
		p.sink.ToolEnd(rec.ToolCallID, rec.ToolName)
	case "agent_end":
		last := lastAssistant(rec.Messages)
		if last.StopReason == "error" && !rec.WillRetry {
			p.sink.TurnEnded(false, errorText(last))
		} else {
			p.sink.TurnEnded(true, "")
		}
	default:
		// extension_ui_request, response, agent_settled, auto-retry and
		// compaction chatter, anything unknown: not work.
		p.sink.Noise()
	}
}

func decodePiMessage(raw json.RawMessage) piMessage {
	var m piMessage
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &m)
	}
	return m
}

func lastAssistant(msgs []json.RawMessage) piMessage {
	for i := len(msgs) - 1; i >= 0; i-- {
		if m := decodePiMessage(msgs[i]); m.Role == "assistant" || m.StopReason != "" {
			return m
		}
	}
	return piMessage{}
}

func errorText(m piMessage) string {
	if t := strings.TrimSpace(m.ErrorMessage); t != "" {
		return t
	}
	return "harness reported stopReason=error with no errorMessage"
}

// maxToolArgsBytes bounds the raw tool arguments a translator hands on
// (the tracker parses them for the live-progress summary and the changed
// file paths; an oversized payload is cut and simply not parsed).
const maxToolArgsBytes = 8 << 10

// boundedArgs renders tool args as JSON text, bounded.
func boundedArgs(raw json.RawMessage) string {
	s := strings.TrimSpace(string(raw))
	if len(s) > maxToolArgsBytes {
		s = s[:maxToolArgsBytes]
	}
	return s
}
