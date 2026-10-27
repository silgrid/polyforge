package supervise

// translator.go: the harness translator seam (v2-converge 2.5). Every
// harness gets one Translator that folds its native stdout records into
// the standard signals below; the supervisor only ever sees those
// signals. This is also the plug-in point for "per-step harness
// selection": a new harness adds a Translator, nothing else changes.

import "time"

// Granularity is how finely a harness reports model output. It drives
// the default silence bound X (2.4): token-level harnesses emit output
// while the model thinks, so a short bound is safe; message-level ones
// stay silent for the whole reply and need a long one.
type Granularity string

const (
	GranularityToken   Granularity = "token"
	GranularityMessage Granularity = "message"
)

// OutputKind classifies one piece of model output.
type OutputKind string

const (
	OutputThinking OutputKind = "thinking"
	OutputText     OutputKind = "text"
	OutputToolCall OutputKind = "toolcall"
)

// ExitInfo is the process-exit signal: the exit code and the last error
// text the process left behind (stderr tail), when known.
type ExitInfo struct {
	Code      int    `json:"code"`
	Signal    string `json:"signal,omitempty"`
	LastError string `json:"last_error,omitempty"`
}

// Sink receives the standard signals. The Tracker implements it; tests
// use recording sinks. Every method must be cheap and non-blocking: it
// runs on the harness stdout reader.
type Sink interface {
	// Alive: a work-class record arrived. UI refreshes are NOT work (F15)
	// and arrive through Noise instead.
	Alive()
	// Noise: a non-work record (extension UI chatter, an echo, anything
	// unclassified). It never counts as life.
	Noise()
	// WaitingModel: a model call started; the model owes a reply.
	WaitingModel()
	// ModelOutput: the model produced output.
	ModelOutput(kind OutputKind)
	// ToolStart / ToolEnd bracket one tool execution.
	ToolStart(id, name, args string)
	ToolEnd(id, name string)
	// TurnEnded: the agent turn ended, ok or with an error text.
	TurnEnded(ok bool, errText string)
	// ErrorSeen records the latest error text without ending the turn.
	ErrorSeen(text string)
	// ProcessExit: the process is gone.
	ProcessExit(info ExitInfo)
}

// Translator folds one harness's native records into Sink signals.
type Translator interface {
	// Name is the harness id ("pi").
	Name() string
	// Granularity declares the output granularity of the harness.
	Granularity() Granularity
	// Feed consumes ONE native stdout record (one line).
	Feed(line []byte)
	// Exit reports the process exit (code + last error text).
	Exit(info ExitInfo)
}

// DefaultSilence answers the default X for a granularity (2.4): 10 minutes
// for token-level harnesses, 30 for message-level ones.
func DefaultSilence(g Granularity) time.Duration {
	if g == GranularityMessage {
		return 30 * time.Minute
	}
	return 10 * time.Minute
}
