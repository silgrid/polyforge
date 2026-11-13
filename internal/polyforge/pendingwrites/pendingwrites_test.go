package pendingwrites

// pendingwrites_test.go: the executor makes a dispatch's
// pending writes in declaration order, resolving key:<K> to the id the
// file made; a replay makes no write twice; a pass that stopped at a
// failure is finished by the replay, making only what was left; every
// write's execution is on the ledger; only the dispatch's own writes are
// made.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// fakeSink records every write; failOn names a call (verb:target) that
// fails while it is set.
type fakeSink struct {
	calls  []string
	filed  map[string]string // key → ref (the hub's idempotency)
	failOn string
}

func (s *fakeSink) File(_ context.Context, op stepcall.Op) (string, error) {
	s.calls = append(s.calls, "file:"+op.Key)
	if s.failOn == "file:"+op.Key {
		return "", errors.New("hub down")
	}
	if ref, ok := s.filed[op.Key]; ok {
		return ref, nil
	}
	ref := fmt.Sprintf("proj#%d", 10+len(s.filed))
	s.filed[op.Key] = ref
	return ref, nil
}

func (s *fakeSink) Attrs(_ context.Context, op stepcall.Op) error {
	conds, _ := json.Marshal(op.If)
	call := "attrs:" + op.Target + ":" + op.Name + ":" + string(op.Value) + ":if" + string(conds)
	if om := op.OwnerMessage; om != nil {
		call += ":" + om.By + "@" + om.At + ":" + om.Words
	}
	s.calls = append(s.calls, call)
	if s.failOn == "attrs:"+op.Target {
		return errors.New("fence refused")
	}
	return nil
}

// record puts one call's pending writes on the ledger (as the daemon's
// /step/call does: a call line, then its ok result line carrying them),
// recorded by dispatch 1 of the step round.
func record(t *testing.T, dir, step string, round int64, ops ...stepcall.Op) {
	t.Helper()
	recordBy(t, dir, step, round, 1, ops...)
}

// recordBy is record by dispatch spawn of the step round.
func recordBy(t *testing.T, dir, step string, round, spawn int64, ops ...stepcall.Op) {
	t.Helper()
	call, err := stepcall.Append(dir, stepcall.Line{Kind: stepcall.KindCall, Step: step, Round: round, Spawn: spawn, Verb: "hub.batch"})
	if err != nil {
		t.Fatal(err)
	}
	for i := range ops {
		ops[i].ID = fmt.Sprintf("%d.%d", call.Seq, i+1)
	}
	if _, err := stepcall.Append(dir, stepcall.Line{Kind: stepcall.KindResult, Step: step, Round: round, Spawn: spawn, Verb: "hub.batch", Call: call.Seq, Outcome: stepcall.OutcomeOK, Pending: ops}); err != nil {
		t.Fatal(err)
	}
}

func fixture(t *testing.T) string {
	dir := t.TempDir()
	record(t, dir, "close", 2,
		stepcall.Op{Verb: stepcall.HubFile, Key: "K1", Project: "proj", Lane: "develop", Goal: "g", Content: "c"},
		stepcall.Op{Verb: stepcall.HubAttrs, Target: "wi_src", Name: "integration", Value: json.RawMessage(`{"state":"landed","fix_tickets":["key:K1"]}`), If: map[string]any{"integration.state": "landed"}},
	)
	record(t, dir, "close", 2,
		stepcall.Op{Verb: stepcall.HubAttrs, Target: "key:K1", Name: "note", Value: json.RawMessage(`"r"`), PerOwner: "t1", OwnerMessage: &api.OwnerMessage{Words: "clear it", By: "u_owner", At: "t1"}},
		stepcall.Op{Verb: stepcall.HubWrapAttrs, Name: "landing", Value: json.RawMessage(`{"state":"green"}`)},
	)
	// another dispatch's write: never this pass's
	record(t, dir, "close", 1, stepcall.Op{Verb: stepcall.HubFile, Key: "K0", Project: "proj", Lane: "develop", Goal: "g", Content: "c"})
	return dir
}

func TestExecuteInOrderResolvingKeyRefs(t *testing.T) {
	dir := fixture(t)
	sink := &fakeSink{filed: map[string]string{}}
	out, err := Execute(context.Background(), dir, "close", 2, sink)
	if err != nil {
		t.Fatal(err)
	}
	want := `file:K1 attrs:wi_src:integration:{"fix_tickets":["proj#10"],"state":"landed"}:if{"integration.state":"landed"} attrs:proj#10:note:"r":ifnull:u_owner@t1:clear it`
	if got := strings.Join(sink.calls, " "); got != want {
		t.Fatalf("the writes:\n got %s\nwant %s", got, want)
	}
	if len(out) != 4 || out[0].Ref != "proj#10" {
		t.Fatalf("outcomes: %+v", out)
	}
	lines, _ := stepcall.Read(dir)
	execs := stepcall.Executions(lines)
	for _, o := range out {
		if e, ok := execs[o.Op]; !ok || e.Outcome != stepcall.ExecDone || e.Step != "close" || e.Round != 2 {
			t.Fatalf("write %s: its execution on the ledger = %+v", o.Op, e)
		}
	}
	if e := execs[out[0].Op]; e.Ref != "proj#10" {
		t.Fatalf("the filed reference is not on the ledger: %+v", e)
	}
	if v := WrapAttrs(lines)["landing"]; string(v) != `{"state":"green"}` {
		t.Fatalf("the wrap attrs: %s", v)
	}
	// a replay makes no write again
	sink.calls = nil
	out, err = Execute(context.Background(), dir, "close", 2, sink)
	if err != nil || len(sink.calls) != 0 || len(out) != 4 || !out[0].Replayed || out[0].Ref != "proj#10" {
		t.Fatalf("the replay: %v, writes %v, outcomes %+v", err, sink.calls, out)
	}
}

func TestExecuteReplayFinishesAStoppedPass(t *testing.T) {
	dir := fixture(t)
	sink := &fakeSink{filed: map[string]string{}, failOn: "attrs:wi_src"}
	if _, err := Execute(context.Background(), dir, "close", 2, sink); err == nil || !strings.Contains(err.Error(), "fence refused") {
		t.Fatalf("the failing pass = %v", err)
	}
	if got := strings.Join(sink.calls, " "); !strings.HasPrefix(got, "file:K1 attrs:wi_src") || strings.Contains(got, "proj#10:note") {
		t.Fatalf("the pass went on past its failure: %s", got)
	}
	lines, _ := stepcall.Read(dir)
	ops := stepcall.PendingOps(lines, "close", 2)
	if e := stepcall.Executions(lines)[ops[1].ID]; e.Outcome != stepcall.ExecFailed || !strings.Contains(e.Error, "fence refused") {
		t.Fatalf("the failure on the ledger: %+v", e)
	}
	sink.calls, sink.failOn = nil, ""
	if _, err := Execute(context.Background(), dir, "close", 2, sink); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sink.calls, " "); got != `attrs:wi_src:integration:{"fix_tickets":["proj#10"],"state":"landed"}:if{"integration.state":"landed"} attrs:proj#10:note:"r":ifnull:u_owner@t1:clear it` {
		t.Fatalf("the replay made: %s", got)
	}
}

// TestExecuteAFileWhoseExecutionWasLost: the write was made but its exec
// line never landed (a crash between them) — the replay files again and
// the sink's key lookup answers the ticket already filed: one ticket.
func TestExecuteAFileWhoseExecutionWasLost(t *testing.T) {
	dir := fixture(t)
	sink := &fakeSink{filed: map[string]string{"K1": "proj#7"}}
	if _, err := Execute(context.Background(), dir, "close", 2, sink); err != nil {
		t.Fatal(err)
	}
	if len(sink.filed) != 1 || !strings.Contains(strings.Join(sink.calls, " "), `"fix_tickets":["proj#7"]`) {
		t.Fatalf("filed %v, writes %v", sink.filed, sink.calls)
	}
}

// merged is an attrs write moving wi's record to merged, fenced on landed.
func merged(wi string) stepcall.Op {
	return stepcall.Op{Verb: stepcall.HubAttrs, Target: wi, Name: "integration", Value: json.RawMessage(`{"state":"merged"}`), If: map[string]any{"integration.state": "landed"}}
}

// TestPendingWritesBelongToTheStepRound (owner ruling R8): the writes a
// dispatch recorded and never handed off for are the next dispatch's of
// the same step round — the round's done makes both dispatches' writes;
// another round's are not made.
func TestPendingWritesBelongToTheStepRound(t *testing.T) {
	dir := t.TempDir()
	recordBy(t, dir, "close", 2, 3, merged("wi_a"))
	recordBy(t, dir, "close", 2, 4, merged("wi_b"))
	recordBy(t, dir, "close", 1, 1, merged("wi_old"))
	sink := &fakeSink{}
	if _, err := Execute(context.Background(), dir, "close", 2, sink); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sink.calls, ","); !strings.Contains(got, "wi_a") || !strings.Contains(got, "wi_b") || strings.Contains(got, "wi_old") || len(sink.calls) != 2 {
		t.Fatalf("the round's writes, both dispatches', and no other round's: %v", sink.calls)
	}
}

// TestDropDiscardsTheRoundsWrites: Drop gives every write of the round
// not made yet an exec line of outcome dropped (one already made stays
// done); a later Execute of the round makes none of them.
func TestDropDiscardsTheRoundsWrites(t *testing.T) {
	dir := t.TempDir()
	record(t, dir, "close", 2, merged("wi_a"))
	sink := &fakeSink{}
	if _, err := Execute(context.Background(), dir, "close", 2, sink); err != nil {
		t.Fatal(err)
	}
	record(t, dir, "close", 2, merged("wi_b"),
		merged("wi_c"))
	dropped, err := Drop(dir, "close", 2, "the step round handed off fail")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(dropped, ",") != "4.1,4.2" {
		t.Fatalf("dropped %v, want the two not made", dropped)
	}
	lines, _ := stepcall.Read(dir)
	execs := stepcall.Executions(lines)
	if execs["1.1"].Outcome != stepcall.ExecDone || execs["4.1"].Outcome != stepcall.ExecDropped || execs["4.2"].Outcome != stepcall.ExecDropped || execs["4.1"].Error == "" {
		t.Fatalf("executions %+v", execs)
	}
	sink.calls = nil
	if _, err := Execute(context.Background(), dir, "close", 2, sink); err != nil || len(sink.calls) != 0 {
		t.Fatalf("a dropped write is never made: %v %v", sink.calls, err)
	}
	if ops := stepcall.PendingOps(lines, "close", 2); len(ops) != 1 || ops[0].ID != "1.1" {
		t.Fatalf("pending ops leave out the dropped ones: %+v", ops)
	}
	// a round with nothing pending needs no sink
	if _, err := Execute(context.Background(), dir, "close", 2, nil); err != nil {
		t.Fatalf("a round whose writes are all made or dropped needs no sink: %v", err)
	}
	if _, err := Execute(context.Background(), t.TempDir(), "close", 2, nil); err != nil {
		t.Fatalf("an empty round needs no sink: %v", err)
	}
}
