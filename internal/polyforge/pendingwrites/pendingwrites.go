// Package pendingwrites is the executor of the hub writes a step records
// pending with `pf step hub` (workflow contract 4 §5.2,): once
// a step round hands off done, its pending writes are made in declaration
// order through the daemon's own aihub binding (Sink), and each one's
// execution — done with the reference it produced, or failed with why — is
// appended to the capsule's call ledger right after the write; any other
// hand-off drops them (Drop: an exec line of outcome dropped each).
//
// Replay. The ledger is the executor's state: Execute skips every write the
// ledger records done and makes the rest, so a pass that stopped at a
// failure (or a crash) is finished by running it again — the writes made
// before are never made twice. The crash window between a write and its
// exec line is closed on the hub's side: a file looks its key up before
// filing (Sink.File), and an attrs write tolerates its own fence once the
// key already holds the value it writes (Sink.Attrs).
//
// References. A write may name a work item a file of the same step round
// files as `key:<K>` (its target, or any string inside an attrs write's
// value);
// the reference resolves, when the write is made, to the id that file
// produced — so a file is declared before the writes naming it.
//
// wrap-attrs. A wrap attr is merged into the work item by its wrap, not
// written now: its execution marks it done (no hub call), and WrapAttrs
// answers the done ones for the wrap.
//
// Reachability: daemon code only (the driver runs it when a step round
// hands off done, and drops the round's writes on any other status); no
// step reaches it — a step only records writes, through the daemon.
package pendingwrites

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// Sink makes the writes: the daemon's aihub binding. Each write is
// idempotent on the hub's side, as the replay relies on.
type Sink interface {
	// File files op's work item (not submitted) and answers its
	// reference; a work item of the project on op's lane whose body
	// carries op's key marker is answered instead of filing another.
	File(ctx context.Context, op stepcall.Op) (string, error)
	// Attrs writes op's top-level attrs key (op.Name) of the work item
	// op.Target whole, fenced by op.If and carrying op.OwnerMessage; a
	// fence refusal whose key already holds the value written is no
	// failure.
	Attrs(ctx context.Context, op stepcall.Op) error
}

// Outcome is one write as a pass found or left it.
type Outcome struct {
	Op   string
	Verb string
	Key  string
	// Ref is the reference the write produced (a filed work item's).
	Ref string
	// Replayed marks a write the ledger already recorded done: the pass
	// did not make it again.
	Replayed bool
}

// Execute makes the pending writes of the step round (step, round)
// recorded on the capsule's ledger, in declaration order, appending each
// one's execution right after it; the writes the ledger records done are
// not made again, and the ones a hand-off dropped are not made at all. The
// first failure ends the pass: it is recorded and answered naming the
// write (the ones after it stay pending for the replay). A round with no
// pending write needs no sink.
func Execute(ctx context.Context, capsuleDir, step string, round int64, sink Sink) ([]Outcome, error) {
	lines, err := stepcall.Read(capsuleDir)
	if err != nil {
		return nil, err
	}
	ops := stepcall.PendingOps(lines, step, round)
	execs := stepcall.Executions(lines)
	if sink == nil {
		for _, op := range ops {
			if e, ok := execs[op.ID]; !ok || e.Outcome != stepcall.ExecDone {
				return nil, fmt.Errorf("pendingwrites: no sink is bound to make write %s (%s)", op.ID, op.Verb)
			}
		}
	}
	refs := map[string]string{} // a file's key → the reference it produced
	var out []Outcome
	for _, op := range ops {
		if e, ok := execs[op.ID]; ok && e.Outcome == stepcall.ExecDone {
			if op.Verb == stepcall.HubFile {
				refs[op.Key] = e.Ref
			}
			out = append(out, Outcome{Op: op.ID, Verb: op.Verb, Key: op.Key, Ref: e.Ref, Replayed: true})
			continue
		}
		ref, werr := write(ctx, sink, op, refs)
		line := stepcall.Line{Kind: stepcall.KindExec, Step: step, Round: round, Verb: stepcall.VerbHubPrefix + op.Verb, Op: op.ID, Ref: ref, Outcome: stepcall.ExecDone}
		if werr != nil {
			line.Outcome, line.Ref, line.Error = stepcall.ExecFailed, "", werr.Error()
		}
		if _, aerr := stepcall.Append(capsuleDir, line); aerr != nil {
			if werr != nil {
				return out, fmt.Errorf("write %s (%s): %w (and its failure is not recorded: %v)", op.ID, op.Verb, werr, aerr)
			}
			return out, fmt.Errorf("write %s (%s) was made but its execution is not recorded: %w", op.ID, op.Verb, aerr)
		}
		if werr != nil {
			return out, fmt.Errorf("write %s (%s): %w", op.ID, op.Verb, werr)
		}
		if op.Verb == stepcall.HubFile {
			refs[op.Key] = ref
		}
		out = append(out, Outcome{Op: op.ID, Verb: op.Verb, Key: op.Key, Ref: ref})
	}
	return out, nil
}

// Drop discards the pending writes of the step round (step, round) that
// are not made yet: each gets an exec line of outcome dropped, so no later
// pass makes it (a hand-off of fail, blocked, cancel, waive or rewind ends
// the round's writes; workflow contract 4 §5.2). It answers the ids it
// dropped.
func Drop(capsuleDir, step string, round int64, why string) ([]string, error) {
	lines, err := stepcall.Read(capsuleDir)
	if err != nil {
		return nil, err
	}
	execs := stepcall.Executions(lines)
	var out []string
	for _, op := range stepcall.PendingOps(lines, step, round) {
		if e, ok := execs[op.ID]; ok && e.Outcome == stepcall.ExecDone {
			continue
		}
		line := stepcall.Line{Kind: stepcall.KindExec, Step: step, Round: round, Verb: stepcall.VerbHubPrefix + op.Verb, Op: op.ID, Outcome: stepcall.ExecDropped, Error: why}
		if _, err := stepcall.Append(capsuleDir, line); err != nil {
			return out, err
		}
		out = append(out, op.ID)
	}
	return out, nil
}

// write makes one write through the sink, its key references resolved.
func write(ctx context.Context, sink Sink, op stepcall.Op, refs map[string]string) (string, error) {
	op, err := stepcall.ResolveRefs(op, refs)
	if err != nil {
		return "", err
	}
	switch op.Verb {
	case stepcall.HubFile:
		return sink.File(ctx, op)
	case stepcall.HubAttrs:
		return "", sink.Attrs(ctx, op)
	case stepcall.HubWrapAttrs:
		return "", nil // merged by the wrap (WrapAttrs), not written now
	}
	return "", fmt.Errorf("no hub write %q", op.Verb)
}

// WrapAttrs answers the wrap attrs the capsule's executed writes carry,
// by name; a later write of a name replaces an earlier one.
func WrapAttrs(lines []stepcall.Line) map[string]json.RawMessage {
	execs := stepcall.Executions(lines)
	out := map[string]json.RawMessage{}
	for _, l := range lines {
		if l.Kind != stepcall.KindResult || l.Outcome != stepcall.OutcomeOK {
			continue
		}
		for _, op := range l.Pending {
			if e, ok := execs[op.ID]; ok && op.Verb == stepcall.HubWrapAttrs && e.Outcome == stepcall.ExecDone {
				out[op.Name] = op.Value
			}
		}
	}
	return out
}
