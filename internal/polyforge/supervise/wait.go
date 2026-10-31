package supervise

// wait.go: the wait-call counter. A step
// agent that waits by issuing `sleep N` as a tool call of its own, or that
// blocks in the shell's `wait` builtin on background jobs, spends the
// step's wall clock on a wait with no condition that could end it early.
// For now such a call is counted and surfaced only: no rule in rules.go
// reads it, no verdict changes because of it and nothing is killed for it.
//
// A wait call is a bash tool call (pi's "bash", Claude Code's "Bash") of
// one of two forms, and one call counts at most once:
//
//   - sleep: the command, trimmed, STARTS with `sleep` and its duration
//     operands sum to more than WaitCallFloor. Only that leading form
//     counts: a short `sleep` inside a condition loop (`until ready; do
//     sleep 2; done`) is the legal way to wait and is never counted, and
//     neither is a sleep that is not the command's first word. Its seconds
//     are the operand sum, known when the call starts.
//   - wait: otherwise, the command runs the `wait` builtin at command
//     position (waitBuiltin), as a call that backgrounds jobs with `&` and
//     then awaits them does. Its seconds are the call's measured wall
//     time, known only when the call ends (or the dispatch does, for a call
//     still blocked then); it counts when that time exceeds WaitCallFloor.
//
// Both translators hand every tool start to the Tracker with the tool's
// name and arguments, so the Tracker detects it for every harness at one
// place.

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// WaitCallFloor is the longest leading sleep, or measured `wait` call,
// that is NOT a wait call: a pause this short is a settle delay, not a
// wait.
const WaitCallFloor = 3 * time.Second

// The wait-call forms (WaitCall.Form).
const (
	WaitFormSleep = "sleep"
	WaitFormWait  = "wait"
)

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
	// Form is WaitFormSleep or WaitFormWait.
	Form string
	// Seconds is, rounded to whole seconds, the leading sleep's operand
	// sum (sleep form) or the call's measured wall time (wait form).
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
		Form:       WaitFormSleep,
		Seconds:    int64(math.Round(d.Seconds())),
		Argv:       excerpt(strings.TrimSpace(command), maxWaitArgv),
		ToolCallID: id,
		At:         at,
	}, true
}

// detectWaitBuiltinCall answers the wait-form candidate a tool start is,
// if its command runs the `wait` builtin: its Seconds are left for the
// Tracker to measure when the call ends.
func detectWaitBuiltinCall(id, name, command string, at time.Time) (WaitCall, bool) {
	if !isBashTool(name) || !waitBuiltin(command) {
		return WaitCall{}, false
	}
	return WaitCall{
		Form:       WaitFormWait,
		Argv:       excerpt(strings.TrimSpace(command), maxWaitArgv),
		ToolCallID: id,
		At:         at,
	}, true
}

// waitBuiltin reports whether a command line runs the `wait` builtin: a
// word that is exactly `wait`, unquoted, at command position — the start
// of the line, or after `;`, `&`, `&&`, `|`, `||`, a newline, a
// parenthesis or a brace, or after the reserved words `do`, `then`,
// `else` and `!`. A `wait` that is an argument (`grep wait`), part of a
// longer word (`await`, `waiting`, `--wait`), quoted or in a comment is
// not the builtin, and the `&` of a redirection (`2>&1`, `&>file`) is no
// separator.
func waitBuiltin(cmd string) bool {
	cmdPos := true // the next word starts a command
	for i := 0; i < len(cmd); {
		c := cmd[i]
		switch {
		case c == ' ' || c == '\t':
			i++
			continue
		case c == '\n' || c == ';' || c == '|' || c == '(' || c == ')' || c == '{' || c == '}':
			cmdPos = true
			i++
			continue
		case c == '&':
			if (i > 0 && (cmd[i-1] == '>' || cmd[i-1] == '<')) || (i+1 < len(cmd) && cmd[i+1] == '>') {
				cmdPos = false // a redirection's &, not a job separator
			} else {
				cmdPos = true
			}
			i++
			continue
		case c == '#':
			for i < len(cmd) && cmd[i] != '\n' {
				i++
			}
			continue
		}
		// one word: up to the next unquoted blank or separator
		start, quoted := i, false
		for i < len(cmd) {
			c := cmd[i]
			if c == ' ' || c == '\t' || c == '\n' || c == ';' || c == '&' || c == '|' || c == '(' || c == ')' {
				break
			}
			switch c {
			case '\\':
				quoted = true
				i += 2
				continue
			case '\'', '"':
				quoted = true
				if j := strings.IndexByte(cmd[i+1:], c); j >= 0 {
					i += j + 2
				} else {
					i = len(cmd)
				}
				continue
			}
			i++
		}
		word := cmd[start:min(i, len(cmd))]
		if cmdPos && !quoted && word == "wait" {
			return true
		}
		switch word {
		case "do", "then", "else", "!":
			cmdPos = !quoted && cmdPos
		default:
			cmdPos = false
		}
	}
	return false
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
