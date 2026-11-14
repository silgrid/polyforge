package driver

// subagentrec.go: the pi half — a pi step's subagent
// conversations, recorded into the capsule while they run.
//
// pi's subagent tool (the extension the step agent dir carries) runs each
// child as `pi --mode json -p --no-session`: the child writes no session of
// its own. What it does do is stream the child's conversation to the
// parent: every child message_end / tool_result_end raises a
// tool_execution_update on the step's stdout whose partialResult.details
// .results[i].messages is the child's whole conversation so far, and the
// call's tool_execution_end carries the final one. The driver already
// reads that stdout (the harness translator); this recorder hangs on the
// same tap and keeps each child's conversation as a pi-shaped transcript
// beside the conversation's capsule transcript (capsule.SubagentDir):
//
//	session/<stem>/subagents/<tool call id>-<i>.jsonl      one {"type":"message",
//	                                                       "timestamp",message} per message
//	session/<stem>/subagents/<tool call id>-<i>.meta.json  {agentType, description,
//	                                                       toolUseId, index, finished,
//	                                                       exitCode?, isError?}
//
// <i> is the result's index in the call (parallel tasks, chain steps; 0
// for a single task); the meta's keys are Claude Code's sidecar keys
// where they mean the same thing, so a reader parses both harnesses' meta
// alike. A child is finished at the call's tool_execution_end (in a chain,
// also once a later step started) — the extension streams a running
// child's exitCode as 0, so that field says nothing until then — and its
// isError is its own (the extension's isFailedResult), never the call's. Messages only grow, so the transcript is appended to (each line
// written whole); a call first seen by this recorder starts its file
// afresh. Written live, the files are what a reader of the running step
// sees and what the next commit boundary commits — a step killed mid-call
// keeps the conversation up to its last update.
//
// Bound: the runtime drops a stdout record over its record-line bound
// (runtime maxRecordLineBytes, 4 MiB) before any tap sees it, and each
// update carries the child's whole conversation, so a child conversation
// beyond that size stops being recorded at the last update under it; the
// parent's own transcript still holds the final result (pi persists the
// tool result's details).

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/capsule"
)

// piSubagentTool is the subagent tool's name on pi's stdout.
const piSubagentTool = "subagent"

// piSubagentRecorder records one pi dispatch's subagent conversations. It
// is fed from the dispatch's stdout reader goroutine only.
type piSubagentRecorder struct {
	capsuleDir string
	transcript string // the conversation's capsule transcript
	dir        string // its subagent dir, once ensured
	disabled   bool   // no trusted directory: record nothing
	runs       map[string]*recordedSubagent
}

// recordedSubagent is what this recorder has written for one child.
type recordedSubagent struct {
	written int    // messages appended to its transcript
	meta    string // the meta file's last written body
}

func newPiSubagentRecorder(capsuleDir, transcript string) *piSubagentRecorder {
	return &piSubagentRecorder{capsuleDir: capsuleDir, transcript: transcript, runs: map[string]*recordedSubagent{}}
}

// piToolExecution is the part of a pi tool_execution_* record the
// recorder reads.
type piToolExecution struct {
	Type          string         `json:"type"`
	ToolCallID    string         `json:"toolCallId"`
	ToolName      string         `json:"toolName"`
	PartialResult *piToolOutcome `json:"partialResult"`
	Result        *piToolOutcome `json:"result"`
}

type piToolOutcome struct {
	Details struct {
		Mode    string             `json:"mode"`
		Results []piSubagentResult `json:"results"`
	} `json:"details"`
}

type piSubagentResult struct {
	Agent      string            `json:"agent"`
	Task       string            `json:"task"`
	ExitCode   *int              `json:"exitCode"`
	StopReason string            `json:"stopReason"`
	Messages   []json.RawMessage `json:"messages"`
}

// failed is the extension's own failure rule for one child
// (isFailedResult): a non-zero exit, or a turn that stopped on an error
// or an abort.
func (r piSubagentResult) failed() bool {
	return (r.ExitCode != nil && *r.ExitCode != 0) || r.StopReason == "error" || r.StopReason == "aborted"
}

// notStarted is a parallel task's placeholder before its child starts:
// exitCode -1 and no message yet.
func (r piSubagentResult) notStarted() bool {
	return r.ExitCode != nil && *r.ExitCode == -1 && len(r.Messages) == 0
}

// Feed takes one stdout record of the dispatch.
func (r *piSubagentRecorder) Feed(line string) {
	if r.disabled || !strings.Contains(line, `"tool_execution_`) || !strings.Contains(line, `"`+piSubagentTool+`"`) {
		return
	}
	var rec piToolExecution
	if json.Unmarshal([]byte(line), &rec) != nil || rec.ToolName != piSubagentTool || rec.ToolCallID == "" {
		return
	}
	var outcome *piToolOutcome
	end := false
	switch rec.Type {
	case "tool_execution_update":
		outcome = rec.PartialResult
	case "tool_execution_end":
		outcome, end = rec.Result, true
	default:
		return
	}
	if outcome == nil || len(outcome.Details.Results) == 0 {
		return
	}
	if r.dir == "" {
		dir, ok := capsule.EnsureSubagentDir(r.capsuleDir, r.transcript)
		if !ok {
			r.disabled = true
			fmt.Fprintf(os.Stderr, "driver: subagent transcripts not recorded: no trusted directory beside %q\n", r.transcript)
			return
		}
		r.dir = dir
	}
	// When is a child finished? The extension seeds every started child
	// with exitCode 0 and streams that while it runs, so neither the exit
	// code nor a parallel task's leaving its -1 placeholder says it ended:
	// a child is finished at the call's tool_execution_end, or — in a
	// chain — once a later step has started (an earlier step's result is
	// final before the next one runs).
	results := outcome.Details.Results
	for i, res := range results {
		if !end && res.notStarted() {
			continue // a parallel task still queued: nothing to record yet
		}
		finished := end || (outcome.Details.Mode == "chain" && i < len(results)-1)
		if err := r.record(rec.ToolCallID, i, res, finished); err != nil {
			fmt.Fprintf(os.Stderr, "driver: subagent transcript %s-%d: %v\n", rec.ToolCallID, i, err)
		}
	}
}

// record brings one child's transcript and meta up to the update.
func (r *piSubagentRecorder) record(callID string, index int, res piSubagentResult, finished bool) error {
	stem := subagentFileStem(callID, index)
	run := r.runs[stem]
	fresh := run == nil || len(res.Messages) < run.written
	if run == nil {
		run = &recordedSubagent{}
		r.runs[stem] = run
	}
	path := filepath.Join(r.dir, stem+".jsonl")
	if fresh || len(res.Messages) > run.written {
		from := run.written
		if fresh {
			from = 0
		}
		var b strings.Builder
		for _, raw := range res.Messages[from:] {
			b.WriteString(piMessageRecord(raw))
			b.WriteByte('\n')
		}
		if err := writeRecords(path, b.String(), fresh); err != nil {
			return err
		}
		run.written = len(res.Messages)
	}
	meta := map[string]any{
		"agentType":   res.Agent,
		"description": res.Task,
		"toolUseId":   callID,
		"index":       index,
		"finished":    finished,
	}
	if finished {
		// each child's own failure, never the call's: a chain stopped by
		// its last step leaves the steps before it successful
		meta["isError"] = res.failed()
		if res.ExitCode != nil {
			meta["exitCode"] = *res.ExitCode
		}
	}
	body, err := json.Marshal(meta)
	if err != nil || string(body) == run.meta {
		return err
	}
	if err := atomicfile.Write(filepath.Join(r.dir, stem+".meta.json"), body, 0o644); err != nil {
		return err
	}
	run.meta = string(body)
	return nil
}

// writeRecords appends whole lines to the transcript (truncating it first
// when fresh); never through a symlink.
func writeRecords(path, lines string, fresh bool) error {
	flags := os.O_WRONLY | os.O_CREATE | syscall.O_NOFOLLOW
	if fresh {
		flags |= os.O_TRUNC
	} else {
		flags |= os.O_APPEND
	}
	f, err := os.OpenFile(path, flags, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(lines); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// piMessageLine is one record of a recorded child transcript: pi's
// session message record shape.
type piMessageLine struct {
	Type      string          `json:"type"`
	Timestamp string          `json:"timestamp,omitempty"`
	Message   json.RawMessage `json:"message"`
}

// piMessageRecord wraps one child message as a pi session message record,
// its timestamp (the message's epoch ms) as the record's ISO time.
func piMessageRecord(raw json.RawMessage) string {
	rec := piMessageLine{Type: "message", Message: raw}
	var probe struct {
		Timestamp json.Number `json:"timestamp"`
	}
	if json.Unmarshal(raw, &probe) == nil {
		if ms, err := probe.Timestamp.Int64(); err == nil && ms > 0 {
			rec.Timestamp = time.UnixMilli(ms).UTC().Format("2006-01-02T15:04:05.000Z")
		}
	}
	out, err := json.Marshal(rec)
	if err != nil {
		return `{"type":"message"}`
	}
	return string(out)
}

// subagentFileStem names one child's files: the tool call id with every
// character outside [A-Za-z0-9_-] replaced by '_' and cut at 64 (plus 8
// hex of the id's sha256 when either changed it, so two ids never share a
// name), then "-<index>".
func subagentFileStem(callID string, index int) string {
	safe := []rune{}
	changed := false
	for _, c := range callID {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' {
			safe = append(safe, c)
		} else {
			safe = append(safe, '_')
			changed = true
		}
	}
	if len(safe) > 64 {
		safe, changed = safe[:64], true
	}
	name := string(safe)
	if changed {
		sum := sha256.Sum256([]byte(callID))
		name += "_" + hex.EncodeToString(sum[:4])
	}
	return fmt.Sprintf("%s-%d", name, index)
}
