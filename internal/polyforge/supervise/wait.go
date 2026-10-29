package supervise

// wait.go: the wait-call counter. A step agent that waits
// by issuing `sleep N` as a tool call of its own spends the step's wall
// clock on a wait with no condition that could end it early. For now such
// a call is counted and surfaced only: no rule in rules.go reads it, no
// verdict changes because of it and nothing is killed for it.
//
// A wait call is a bash tool call (pi's "bash", Claude Code's "Bash")
// whose command, trimmed, STARTS with `sleep` and whose duration operands
// sum to more than WaitCallFloor. Only that leading form counts: a short
// `sleep` inside a condition loop (`until ready; do sleep 2; done`) is the
// legal way to wait and is never counted, and neither is a sleep that is
// not the command's first word. Both translators hand every tool start to
// the Tracker with the tool's name and arguments, so the Tracker detects
// it for every harness at one place.

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// WaitCallFloor is the longest leading sleep that is NOT a wait call: a
// pause this short is a settle delay, not a wait.
const WaitCallFloor = 3 * time.Second

// maxWaitArgv bounds the command excerpt a wait call keeps, in characters.
const maxWaitArgv = 120

// maxWaitSeconds caps one call's counted seconds (a `sleep 1e99`-sized
// operand must not overflow the sums).
const maxWaitSeconds = math.MaxInt32

// maxPendingWaitCalls bounds the wait calls a tracker holds for a reader
// that never takes them (a dispatch without a driver relay, unit tests).
// The counters keep counting past it.
const maxPendingWaitCalls = 256

// WaitCall is one wait call of a dispatch.
type WaitCall struct {
	// Seconds is the call's sleep, rounded to whole seconds.
	Seconds int64
	// Argv is the command excerpt (trimmed, at most 120 characters).
	Argv string
	// ToolCallID is the harness's id of the tool call.
	ToolCallID string
	// At is when the tool call started.
	At time.Time
	// WaitCalls and WaitSeconds are the dispatch's totals including this
	// call.
	WaitCalls   int
	WaitSeconds int64
}

// isBashTool reports whether a tool runs a shell command line.
func isBashTool(name string) bool { return strings.EqualFold(name, "bash") }

// leadingSleep answers the duration of a command that starts with
// `sleep <N>`: N is a non-negative decimal number with an optional unit
// suffix (s, m, h, d), and several operands sum (`sleep 1m 30s`), as
// sleep(1) does. The operands end at the first shell separator (`;`,
// `&`, `|`, a newline, a redirection, a parenthesis or a comment); a
// redirection's file-descriptor word (the `2` of `2>/dev/null`: digits
// right before `>` or `<`) is part of the redirection, never an operand.
// An operand sleep would refuse, or one only the shell can evaluate
// (`sleep $N`), answers false.
func leadingSleep(cmd string) (time.Duration, bool) {
	cmd = strings.TrimSpace(cmd)
	rest, ok := strings.CutPrefix(cmd, "sleep")
	if !ok || rest == "" || (rest[0] != ' ' && rest[0] != '\t') {
		return 0, false
	}
	if i := strings.IndexAny(rest, ";&|\n<>()#`"); i >= 0 {
		if rest[i] == '<' || rest[i] == '>' {
			j := i
			for j > 0 && rest[j-1] >= '0' && rest[j-1] <= '9' {
				j--
			}
			if j < i && (rest[j-1] == ' ' || rest[j-1] == '\t') {
				i = j // a whole word of digits: the redirected descriptor
			}
		}
		rest = rest[:i]
	}
	ops := strings.Fields(rest)
	if len(ops) == 0 {
		return 0, false
	}
	var secs float64
	for _, op := range ops {
		s, ok := sleepOperand(op)
		if !ok {
			return 0, false
		}
		secs += s
	}
	secs = min(secs, maxWaitSeconds)
	return time.Duration(secs * float64(time.Second)), true
}

// sleepOperand parses one sleep(1) operand into seconds.
func sleepOperand(op string) (float64, bool) {
	unit := 1.0
	switch op[len(op)-1] {
	case 's':
		op = op[:len(op)-1]
	case 'm':
		unit, op = 60, op[:len(op)-1]
	case 'h':
		unit, op = 3600, op[:len(op)-1]
	case 'd':
		unit, op = 86400, op[:len(op)-1]
	}
	digits := 0
	for _, r := range op {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r == '.':
		default:
			return 0, false // a sign, an exponent, a variable: not a literal duration
		}
	}
	if digits == 0 {
		return 0, false
	}
	f, err := strconv.ParseFloat(op, 64)
	if err != nil {
		return 0, false
	}
	return f * unit, true
}

// detectWaitCall answers the wait call a tool start is, if it is one.
// command is the tool's command line as the live-progress summary
// rendered it (summarizeTool: the bash `command` argument, trimmed).
func detectWaitCall(id, name, command string, at time.Time) (WaitCall, bool) {
	if !isBashTool(name) {
		return WaitCall{}, false
	}
	d, ok := leadingSleep(command)
	if !ok || d <= WaitCallFloor {
		return WaitCall{}, false
	}
	return WaitCall{
		Seconds:    int64(math.Round(d.Seconds())),
		Argv:       excerpt(strings.TrimSpace(command), maxWaitArgv),
		ToolCallID: id,
		At:         at,
	}, true
}

// excerpt cuts s to at most n characters.
func excerpt(s string, n int) string {
	i := 0
	for pos := range s {
		if i == n {
			return s[:pos]
		}
		i++
	}
	return s
}
