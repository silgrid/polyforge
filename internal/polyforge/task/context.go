package task

// context.go: the running task's context-window usage (polyforge#230).
//
// A task writes no transcript (its harness runs with --no-session /
// --no-session-persistence), so its conversation's fill is counted from the
// records its stdout already carries, through the SAME fold the step lane
// reads a session transcript with (sessionjsonl.ContextReader): the pi
// stream's `message_end` carries the finished assistant message, which is the
// record a pi transcript holds as a `message` entry, so it is wrapped into
// one (with the time the daemon received it as its timestamp) and fed; a
// claude stream-json record is transcript-shaped already and only lacks a
// timestamp, which the daemon's receive time supplies.
//
// The result is "the same counting" in the sense it can be: the same fold,
// the same model cleaning and the same chars/4 estimate, fed the records
// equivalent to the transcript's. It is not the transcript itself: a pi
// stream event with no transcript equivalent (a compaction boundary) is not
// in the count, so a compressed pi conversation reads high rather than zero.
//
// Reach: the value rides the same read faces as the task record — GET
// /task/list and GET /task/show, both readable by any local UDS peer. It is
// numbers, the model id and a timestamp: no transcript content.

import (
	"bytes"
	"encoding/json"
	"time"

	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/polyforge/sessionjsonl"
)

// contextTracker folds one task attempt's stdout records into its context
// usage. One runner owns one tracker (no locking here, like StreamParser).
type contextTracker struct {
	harness string
	// now answers the receive time stamped on a record the stream carries
	// none for (a transcript entry has its own timestamp; a stream record
	// does not).
	now    func() time.Time
	reader *sessionjsonl.ContextReader
}

// newContextTracker answers the tracker of one harness attempt.
func newContextTracker(harness string) *contextTracker {
	h := "pi"
	if harness == harnessClaude {
		h = "claude"
	}
	return &contextTracker{harness: harness, now: time.Now, reader: sessionjsonl.NewContextReader(h)}
}

// feed folds one stdout line and answers the usage the folded records show
// (nil when nothing is known), and whether the line was a record the fold
// consumes. Only the records that correspond to a transcript entry are
// consumed: pi's `message_end` and claude's `assistant` / `system` records;
// everything else (turn markers, tool execution events, pi's in-flight
// `message_update`) is not part of the conversation's fill.
func (c *contextTracker) feed(line []byte) (*protocol.ContextUsage, bool) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] != '{' {
		return nil, false
	}
	var rec struct {
		Type      string          `json:"type"`
		Message   json.RawMessage `json:"message"`
		Timestamp string          `json:"timestamp"`
	}
	if json.Unmarshal(line, &rec) != nil {
		return nil, false
	}
	switch c.harness {
	case harnessClaude:
		switch rec.Type {
		case "assistant", "system":
		default:
			return nil, false
		}
		if rec.Timestamp == "" {
			// a stream-json record has no timestamp; the receive time is the
			// transcript entry's equivalent
			var fields map[string]json.RawMessage
			if json.Unmarshal(line, &fields) != nil {
				return nil, false
			}
			stamp, err := json.Marshal(c.now().UTC().Format(time.RFC3339Nano))
			if err != nil {
				return nil, false
			}
			fields["timestamp"] = stamp
			out, err := json.Marshal(fields)
			if err != nil {
				return nil, false
			}
			line = out
		}
	default: // pi
		if rec.Type != "message_end" || len(rec.Message) == 0 || string(rec.Message) == "null" {
			return nil, false
		}
		wrapped, err := json.Marshal(struct {
			Type      string          `json:"type"`
			Timestamp string          `json:"timestamp"`
			Message   json.RawMessage `json:"message"`
		}{Type: "message", Timestamp: c.now().UTC().Format(time.RFC3339Nano), Message: rec.Message})
		if err != nil {
			return nil, false
		}
		line = wrapped
	}
	return contextUsage(c.reader.Feed(line), nil), true
}

// usage answers the usage the folded records show, with the context windows
// the attempt's harness reported (nil when nothing is known).
func (c *contextTracker) usage(windows map[string]int64) *protocol.ContextUsage {
	return contextUsage(c.reader.Context(), windows)
}

// contextUsage is the task face's mapping of a folded session value to the
// wire shape: the shared sessionjsonl mapping (one fact, one place, the step
// lane's `context` included), with the window and percent the harness
// reported for the value's model.
func contextUsage(ctx sessionjsonl.Context, windows map[string]int64) *protocol.ContextUsage {
	return ctx.Usage(windows)
}
