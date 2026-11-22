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

import (
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
	now func() time.Time
}

// newContextTracker answers the tracker of one harness attempt.
func newContextTracker(harness string) *contextTracker {
	return &contextTracker{harness: harness, now: time.Now}
}

// feed folds one stdout line and answers the usage as it now stands, and
// whether the line was a record the fold consumes.
func (c *contextTracker) feed(line []byte) (*protocol.ContextUsage, bool) {
	return nil, false
}

// usage answers the usage the folded records show (nil when nothing is
// known), with the context windows the attempt's harness reported.
func (c *contextTracker) usage(windows map[string]int64) *protocol.ContextUsage {
	return nil
}

// contextUsage turns a folded session value into the task read face's
// ContextUsage, carrying a window and percent only when the harness reported
// that model's window.
func contextUsage(ctx sessionjsonl.Context, windows map[string]int64) *protocol.ContextUsage {
	return nil
}