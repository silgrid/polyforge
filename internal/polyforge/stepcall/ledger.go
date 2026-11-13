// Package stepcall is the record of a step's one entry point, `pf step`
// (workflow contract 4, = its first stage C1): the call
// ledger runtime-state/step-calls.jsonl, the hand-off document
// artifacts/handoff.md and the shapes the step's calls carry.
//
// The ledger is the daemon's truth about a step's calls. The daemon
// appends every call it receives from a step BEFORE it runs it (a `call`
// line: the dispatch and the request as it arrived), then what the call
// came to (a `result` line naming the call by its seq: ok, refused or
// failed — an accepted hand-off's facts, or the hub writes recorded
// pending), and later each pending write's execution (an `exec` line
// naming the write by its op id). The file is append-only and crash-safe:
// one JSON object per line, fsynced per append, never rewritten; a torn
// trailing line is skipped by the reader. Nothing reads handoff.md for a
// state: a step that edits the document changes no recorded status.
//
// Reachability: the daemon writes both files (its /step/call route and the
// pending-writes executor); a step reaches them only through `pf step`,
// which the daemon serves to step processes of its own (routes.go).
package stepcall

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// The line kinds.
const (
	// KindCall is a call as it arrived, appended before it runs.
	KindCall = "call"
	// KindResult is what one call came to (Call names it).
	KindResult = "result"
	// KindExec is one pending write's execution (Op names it).
	KindExec = "exec"
)

// The outcomes of a result line.
const (
	// OutcomeOK: the call was carried out (a hand-off accepted, a write
	// recorded pending, a read answered).
	OutcomeOK = "ok"
	// OutcomeRefused: the call's checks refused it; nothing was done.
	OutcomeRefused = "refused"
	// OutcomeFailed: the call could not be carried out (aihub or the disk).
	OutcomeFailed = "failed"
)

// The outcomes of an exec line: a write made (done), a write that failed
// (failed: the replay makes it again), and a write the step round's
// hand-off discarded (dropped: a fail, blocked, cancel, waive or rewind —
// it is never made; workflow contract 4 §5.2).
const (
	ExecDone    = "done"
	ExecFailed  = "failed"
	ExecDropped = "dropped"
)

// Line is one line of the ledger.
type Line struct {
	// Seq is the line's ordinal in the ledger (1-based, assigned by
	// Append): a result line names its call by it, and an op id carries
	// the seq of the call that recorded the op.
	Seq  int64  `json:"seq"`
	At   string `json:"at"`
	Kind string `json:"kind"`
	// Step, Round and Spawn name the dispatch the line belongs to, as the
	// daemon resolved the caller (never as the caller claimed).
	Step  string `json:"step,omitempty"`
	Round int64  `json:"round,omitempty"`
	Spawn int64  `json:"spawn,omitempty"`
	// Verb is the call's verb (on a call and its result line).
	Verb string `json:"verb,omitempty"`
	// Args is the call's request as it arrived (a call line).
	Args json.RawMessage `json:"args,omitempty"`
	// Call is the seq of the call a result line settles.
	Call int64 `json:"call,omitempty"`
	// Outcome is a result's (OutcomeOK, OutcomeRefused, OutcomeFailed) or
	// an exec's (ExecDone, ExecFailed, ExecDropped).
	Outcome string `json:"outcome,omitempty"`
	// Code and Error are a refusal's or failure's typed code and detail.
	Code  string `json:"code,omitempty"`
	Error string `json:"error,omitempty"`
	// Handoff is an accepted hand-off's record (a result line of verb
	// handoff with outcome ok).
	Handoff *Handoff `json:"handoff,omitempty"`
	// Pending are the hub writes a result line recorded pending, in
	// declaration order.
	Pending []Op `json:"pending,omitempty"`
	// Op is the id of the pending write an exec line executed; Ref the
	// reference it produced (a filed work item's).
	Op  string `json:"op,omitempty"`
	Ref string `json:"ref,omitempty"`
}

// Handoff is an accepted hand-off as the ledger records it: the facts
// the daemon stamped on the section's header that a reader acts on.
type Handoff struct {
	Status   string `json:"status"`
	To       string `json:"to,omitempty"`
	PerOwner string `json:"per_owner,omitempty"`
	Reason   string `json:"reason,omitempty"`
	// Evidence are the capsule-relative evidence paths.
	Evidence []string `json:"evidence,omitempty"`
	// Heads are the task repos' HEADs when the hand-off was accepted: the
	// next hand-off's start commits.
	Heads     map[string]string `json:"heads,omitempty"`
	Candidate string            `json:"candidate,omitempty"`
	StartedAt string            `json:"started_at,omitempty"`
}

// Path answers a capsule's ledger path.
func Path(capsuleDir string) string { return filepath.Join(capsuleDir, capsule.StepCallsJSONL) }

// appendLocks serializes the appends to one ledger within the process
// (the daemon is its one writer): the seq is read and written under it.
var appendLocks sync.Map

func appendLock(path string) *sync.Mutex {
	m, _ := appendLocks.LoadOrStore(path, &sync.Mutex{})
	return m.(*sync.Mutex)
}

// Append stamps l with the ledger's next seq (and its at when unset),
// appends it to the capsule's ledger (created on the first append) and
// fsyncs it. It answers the line as written.
func Append(capsuleDir string, l Line) (Line, error) {
	if l.Kind == "" {
		return l, errors.New("stepcall: a ledger line needs its kind")
	}
	path := Path(capsuleDir)
	mu := appendLock(path)
	mu.Lock()
	defer mu.Unlock()
	lines, err := Read(capsuleDir)
	if err != nil {
		return l, err
	}
	for _, x := range lines {
		if x.Seq > l.Seq {
			l.Seq = x.Seq
		}
	}
	l.Seq++
	if l.At == "" {
		l.At = time.Now().UTC().Format(time.RFC3339Nano)
	}
	raw, err := json.Marshal(l)
	if err != nil {
		return l, fmt.Errorf("stepcall: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return l, fmt.Errorf("stepcall: %w", err)
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return l, fmt.Errorf("stepcall: %w", err)
	}
	defer f.Close()
	// a torn trailing line (a crash mid-append) must not swallow this one
	if st, err := f.Stat(); err == nil && st.Size() > 0 {
		last := make([]byte, 1)
		if _, err := f.ReadAt(last, st.Size()-1); err == nil && last[0] != '\n' {
			raw = append([]byte{'\n'}, raw...)
		}
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		return l, fmt.Errorf("stepcall: %w", err)
	}
	if err := f.Sync(); err != nil {
		return l, fmt.Errorf("stepcall: %w", err)
	}
	return l, nil
}

// Read reads the capsule's ledger in order. A missing file is an empty
// ledger; a torn or malformed line is skipped; any other read failure is
// an error (a ledger that cannot be read is never taken for an empty one).
func Read(capsuleDir string) ([]Line, error) {
	f, err := os.Open(Path(capsuleDir))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("stepcall: %w", err)
	}
	defer f.Close()
	var out []Line
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 8*1024*1024)
	for sc.Scan() {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var l Line
		if json.Unmarshal([]byte(text), &l) != nil || l.Seq == 0 || l.Kind == "" {
			continue // torn or foreign: not a line of the ledger
		}
		out = append(out, l)
	}
	if err := sc.Err(); err != nil {
		return out, fmt.Errorf("stepcall: %w", err)
	}
	return out, nil
}

// accepted reports whether l is an accepted call of verb (an ok result).
func accepted(l Line, verb string) bool {
	return l.Kind == KindResult && l.Verb == verb && l.Outcome == OutcomeOK
}

// HandoffOf answers the accepted hand-off of one dispatch (its result
// line), nil when the dispatch has not handed off.
func HandoffOf(lines []Line, step string, spawn int64) *Line {
	for i := range lines {
		l := lines[i]
		if accepted(l, VerbHandoff) && l.Step == step && l.Spawn == spawn && l.Handoff != nil {
			return &l
		}
	}
	return nil
}

// StepHandoffs answers every accepted hand-off of step, in order.
func StepHandoffs(lines []Line, step string) []Line {
	var out []Line
	for _, l := range lines {
		if accepted(l, VerbHandoff) && l.Step == step && l.Handoff != nil {
			out = append(out, l)
		}
	}
	return out
}

// PendingOps answers the hub writes the dispatches of one step round
// recorded pending, in declaration order, except the ones a hand-off of
// that round dropped. A write belongs to its step round, not to the
// dispatch that recorded it (owner ruling R8): a dispatch
// that died before its hand-off leaves its writes to the next dispatch of
// the round, and the round's done makes them all (each idempotent by its
// key, fence or hold, so a repeat makes nothing twice).
func PendingOps(lines []Line, step string, round int64) []Op {
	dropped := map[string]bool{}
	for _, l := range lines {
		if l.Kind == KindExec && l.Outcome == ExecDropped {
			dropped[l.Op] = true
		}
	}
	var out []Op
	for _, l := range lines {
		if l.Kind == KindResult && l.Outcome == OutcomeOK && l.Step == step && l.Round == round {
			for _, op := range l.Pending {
				if !dropped[op.ID] {
					out = append(out, op)
				}
			}
		}
	}
	return out
}

// Executions answers the newest exec line of every op id.
func Executions(lines []Line) map[string]Line {
	out := map[string]Line{}
	for _, l := range lines {
		if l.Kind == KindExec && l.Op != "" {
			out[l.Op] = l
		}
	}
	return out
}
