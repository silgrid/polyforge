package sessionjsonl

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"sync"
)

// Context is the context-window usage a step session's transcript shows:
// the tokens the conversation fills, counted the way the chat header counts
// an interactive session of the same harness, the model and the timestamp of
// the record the count was taken from. The zero value is "unknown".
type Context struct {
	// Tokens is the count; meaningful only when TokensKnown.
	Tokens int64
	// TokensKnown is false when the transcript shows nothing to count, or
	// when a compaction left the count unknown until the next reply.
	TokensKnown bool
	// Model is the model id (pi: "<provider>/<model>"); "" when none or
	// when the transcript's value is not a plain model id.
	Model string
	// At is the timestamp of the record that last decided Tokens.
	At string
}

// ContextReader reads a session transcript's context usage incrementally:
// it remembers how far it has read and the unfinished last line, so each
// Read folds only the bytes appended since — the transcript itself is never
// held in memory. Reading another path starts over. Safe for concurrent use.
type ContextReader struct {
	mu      sync.Mutex
	harness string
	path    string
	off     int64
	partial []byte
	fold    contextFold
}

// NewContextReader returns a reader for the given harness: "claude", or
// pi's session format for anything else.
func NewContextReader(harness string) *ContextReader {
	r := &ContextReader{harness: harness}
	r.reset("")
	return r
}

func (r *ContextReader) reset(path string) {
	r.path, r.off, r.partial = path, 0, nil
	if r.harness == "claude" {
		r.fold = &claudeFold{}
	} else {
		r.fold = &piFold{}
	}
}

// contextFold folds one parsed transcript record into the running state.
type contextFold interface {
	add(rec map[string]json.RawMessage)
	context() Context
}

// Read folds the bytes appended to path since the last Read and returns the
// session's context usage. A missing or unreadable file reads as what has
// been folded so far (the zero value when nothing); a malformed or torn line
// is skipped, never an error. A file shorter than what was read (rewritten)
// starts over.
func (r *ContextReader) Read(path string) Context {
	r.mu.Lock()
	defer r.mu.Unlock()
	if path != r.path {
		r.reset(path)
	}
	f, err := os.Open(path)
	if err != nil {
		return r.fold.context()
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() < r.off {
		r.reset(path)
	}
	if _, err := f.Seek(r.off, io.SeekStart); err != nil {
		return r.fold.context()
	}
	buf := make([]byte, 64<<10)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			r.off += int64(n)
			r.consume(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return r.fold.context()
}

// consume folds every complete line of chunk (prefixed by the unfinished
// line carried over) and keeps the new unfinished tail.
func (r *ContextReader) consume(chunk []byte) {
	for {
		i := bytes.IndexByte(chunk, '\n')
		if i < 0 {
			r.partial = append(r.partial, chunk...)
			return
		}
		line := chunk[:i]
		if len(r.partial) > 0 {
			line = append(r.partial, line...)
			r.partial = nil
		}
		r.foldLine(line)
		chunk = chunk[i+1:]
	}
}

func (r *ContextReader) foldLine(line []byte) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return
	}
	var rec map[string]json.RawMessage
	if json.Unmarshal(line, &rec) != nil {
		return
	}
	r.fold.add(rec)
}

// field decodes rec[key] into a T; ok is false when absent or of another type.
func field[T any](rec map[string]json.RawMessage, key string) (T, bool) {
	var v T
	raw, ok := rec[key]
	if !ok || json.Unmarshal(raw, &v) != nil {
		return v, false
	}
	return v, true
}

func str(rec map[string]json.RawMessage, key string) string {
	s, _ := field[string](rec, key)
	return s
}

func num(rec map[string]json.RawMessage, key string) int64 {
	f, ok := field[float64](rec, key)
	if !ok || math.IsNaN(f) || f < 0 || f > math.MaxInt64/2 {
		return 0
	}
	return int64(f)
}

// modelID keeps a plain model id (≤128 bytes of [A-Za-z0-9._:/@+-]); anything
// else is "".
func modelID(s string) string {
	if s == "" || len(s) > 128 {
		return ""
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.', c == '_', c == ':', c == '/', c == '@', c == '+', c == '-':
		default:
			return ""
		}
	}
	return s
}

// piFold counts the way pi's own session does (getContextUsage): the last
// valid reply's usage — stopReason neither "aborted" nor "error", totalTokens
// or the sum of its parts above zero — plus a chars/4 estimate of every
// message after it; the estimate of every message while there is no reply
// yet; unknown after a compaction until a valid reply follows it.
type piFold struct {
	usage     int64 // the last valid usage, when haveUsage
	haveUsage bool
	trailing  int64 // estimate of the messages since that usage (or of all of them)
	messages  bool  // a message has been counted
	compacted bool  // a compaction with no valid reply since
	model     string
	at        string
}

func (p *piFold) add(rec map[string]json.RawMessage) {
	at := str(rec, "timestamp")
	switch str(rec, "type") {
	case "model_change":
		if m := piModel(str(rec, "provider"), str(rec, "modelId")); m != "" {
			p.model = m
		}
	case "compaction":
		p.compacted = true
		p.haveUsage, p.usage = false, 0
		p.trailing = piEstimateChars(len(str(rec, "summary")))
		p.at = at
	case "branch_summary":
		p.countMessage(piEstimateChars(len(str(rec, "summary"))), at)
	case "custom_message":
		p.countMessage(piEstimateChars(piContentChars(rec["content"])), at)
	case "message":
		msg, ok := field[map[string]json.RawMessage](rec, "message")
		if !ok {
			return
		}
		if str(msg, "role") == "assistant" {
			if m := piModel(str(msg, "provider"), str(msg, "model")); m != "" {
				p.model = m
			}
			if tokens, ok := piUsage(msg); ok {
				p.usage, p.haveUsage, p.trailing, p.compacted = tokens, true, 0, false
				p.messages = true
				p.at = at
				return
			}
		}
		p.countMessage(piEstimate(msg), at)
	}
}

// piModel is "<provider>/<model>" when both are present and plain.
func piModel(provider, model string) string {
	if provider == "" || model == "" {
		return ""
	}
	return modelID(provider + "/" + model)
}

func (p *piFold) countMessage(tokens int64, at string) {
	p.trailing += tokens
	p.messages = true
	p.at = at
}

func (p *piFold) context() Context {
	c := Context{Model: p.model}
	if p.compacted || !p.messages {
		return c
	}
	c.Tokens, c.TokensKnown, c.At = p.usage+p.trailing, true, p.at
	return c
}

// piUsage is a reply's context tokens when its usage is valid.
func piUsage(msg map[string]json.RawMessage) (int64, bool) {
	switch str(msg, "stopReason") {
	case "aborted", "error":
		return 0, false
	}
	usage, ok := field[map[string]json.RawMessage](msg, "usage")
	if !ok {
		return 0, false
	}
	tokens := num(usage, "totalTokens")
	if tokens == 0 {
		tokens = num(usage, "input") + num(usage, "output") + num(usage, "cacheRead") + num(usage, "cacheWrite")
	}
	return tokens, tokens > 0
}

func piEstimateChars(chars int) int64 { return int64((chars + 3) / 4) }

// piImageChars is pi's estimate for an image block.
const piImageChars = 4800

// piContentChars counts a string content or the text and image blocks of a
// block list (UTF-16 code units, as pi's string length does).
func piContentChars(raw json.RawMessage) int {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return utf16Len(s)
	}
	var blocks []map[string]json.RawMessage
	if json.Unmarshal(raw, &blocks) != nil {
		return 0
	}
	chars := 0
	for _, b := range blocks {
		switch str(b, "type") {
		case "text":
			chars += utf16Len(str(b, "text"))
		case "image":
			chars += piImageChars
		}
	}
	return chars
}

// piEstimate is pi's chars/4 estimate of one message (estimateTokens).
func piEstimate(msg map[string]json.RawMessage) int64 {
	chars := 0
	switch str(msg, "role") {
	case "system":
		chars = piContentChars(msg["content"])
		if sections, ok := field[map[string]string](msg, "sections"); ok {
			for _, s := range sections {
				chars += utf16Len(s)
			}
		}
		if raw, ok := msg["toolsAdded"]; ok && string(raw) != "null" {
			chars += len(compactJSON(raw))
		}
	case "user", "custom", "toolResult":
		chars = piContentChars(msg["content"])
	case "assistant":
		blocks, _ := field[[]map[string]json.RawMessage](msg, "content")
		for _, b := range blocks {
			switch str(b, "type") {
			case "text":
				chars += utf16Len(str(b, "text"))
			case "thinking":
				chars += utf16Len(str(b, "thinking"))
			case "toolCall":
				chars += utf16Len(str(b, "name")) + len(compactJSON(b["arguments"]))
			}
		}
	case "bashExecution":
		chars = utf16Len(str(msg, "command")) + utf16Len(str(msg, "output"))
	case "branchSummary", "compactionSummary":
		chars = utf16Len(str(msg, "summary"))
	}
	return piEstimateChars(chars)
}

func compactJSON(raw json.RawMessage) []byte {
	if len(raw) == 0 {
		return nil
	}
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return raw
	}
	return b.Bytes()
}

func utf16Len(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x10000 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// claudeFold counts the way the chat header counts a claude session: the
// last main-chain (not sidechain) reply's input + cache creation + cache
// read + output (a reply counting zero keeps the previous value); a later
// compact_boundary replaces it with its compactMetadata.postTokens, or
// leaves it unknown when it reported none.
type claudeFold struct {
	tokens int64
	known  bool
	model  string
	at     string
}

func (c *claudeFold) add(rec map[string]json.RawMessage) {
	if side, _ := field[bool](rec, "isSidechain"); side {
		return
	}
	at := str(rec, "timestamp")
	switch str(rec, "type") {
	case "system":
		if str(rec, "subtype") != "compact_boundary" {
			return
		}
		meta, _ := field[map[string]json.RawMessage](rec, "compactMetadata")
		post, ok := field[float64](meta, "postTokens")
		c.known = ok && post >= 0 && !math.IsNaN(post) && !math.IsInf(post, 0)
		c.tokens, c.at = 0, at
		if c.known {
			c.tokens = int64(post)
		}
	case "assistant":
		msg, ok := field[map[string]json.RawMessage](rec, "message")
		if !ok {
			return
		}
		if m := str(msg, "model"); m != "<synthetic>" {
			if m = modelID(m); m != "" {
				c.model = m
			}
		}
		usage, ok := field[map[string]json.RawMessage](msg, "usage")
		if !ok {
			return
		}
		total := num(usage, "input_tokens") + num(usage, "cache_creation_input_tokens") +
			num(usage, "cache_read_input_tokens") + num(usage, "output_tokens")
		if total > 0 {
			c.tokens, c.known, c.at = total, true, at
		}
	}
}

func (c *claudeFold) context() Context {
	ctx := Context{Model: c.model}
	if c.known {
		ctx.Tokens, ctx.TokensKnown, ctx.At = c.tokens, true, c.at
	}
	return ctx
}
