package driver

// handoff_test.go (workflow contract 4 stage C2,): the
// driver's reaction to a dispatch's hand-off, row by row of design #483
// §7 — done makes the step round's pending hub writes and then dispatches
// the next step; a write that fails parks write_failed and the resume
// makes only what is left, then advances without re-dispatching the step;
// a gate's fail appends a 回退 event and rewinds, charged unless it cites
// the owner; blocked parks; cancel / waive / rewind (citing the owner)
// cancel, accept the FAIL or rewind charged to nothing; every status but
// done drops the round's writes (aihub sees none). And the hand-off
// document: its opening at the claim, the round's start commits on the
// record, the completion event.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// hubSink is a pendingwrites.Sink recording every write; failFile makes
// the next File calls fail (counted down).
type hubSink struct {
	mu       sync.Mutex
	calls    []string
	failFile int
	// spawnsAt records the dispatches the spawner had made at each call.
	sp       *fakeSpawner
	spawnsAt []int
}

func (s *hubSink) record(c string) {
	s.calls = append(s.calls, c)
	if s.sp != nil {
		s.spawnsAt = append(s.spawnsAt, len(s.sp.dispatched()))
	}
}

func (s *hubSink) File(_ context.Context, op stepcall.Op) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failFile > 0 {
		s.failFile--
		return "", errors.New("aihub unreachable")
	}
	s.record("file " + op.Key)
	return "wi_fix_" + op.Key, nil
}

func (s *hubSink) Attrs(_ context.Context, op stepcall.Op) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var v struct {
		FixTickets any `json:"fix_tickets"`
	}
	_ = json.Unmarshal(op.Value, &v)
	s.record(fmt.Sprintf("attrs %s %v %v", op.Target, op.If["integration.state"], v.FixTickets))
	return nil
}

func (s *hubSink) seen() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.calls...)
}

// handoffEnv is runEnv with the hub writes bound to a recording sink and
// the work item's document in the capsule.
func handoffEnv(t *testing.T, max int, steps ...StepSpec) (*Driver, *fakeSpawner, *hubSink, string) {
	t.Helper()
	dir := t.TempDir()
	sp := &fakeSpawner{}
	sink := &hubSink{sp: sp}
	d, err := New(Config{
		WI:        WIInfo{ID: "wi_ho", CapsuleDir: dir, BundleName: "b"},
		ExecPath:  filepath.Join(dir, capsule.ExecutionJSON),
		Workflow:  testLane(max, steps...),
		Spawner:   sp,
		Log:       &eventLog{},
		HubWrites: sink,
		// the fixture's stamps are pinned zh-CN
		Language: func() string { return stepcall.LangZhCN },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	writeCapsuleFile(t, dir, capsule.WorkItemJSON, `{"id":"wi_ho","slug":"demo-7","project":"proj","goal":"make the demo green","content":"Some background.\n\n## Acceptance\n- AC1 the demo is green\n- AC2 nothing else breaks\n\n## Notes\nnone\n"}`)
	return d, sp, sink, dir
}

// registers records hub writes pending for the dispatch's step round, as
// `pf step hub batch` does while the step runs (the daemon's result line).
func registers(t *testing.T, dir, step string, round int64, ops ...stepcall.Op) func() {
	return func() {
		t.Helper()
		call, err := stepcall.Append(dir, stepcall.Line{Kind: stepcall.KindCall, Step: step, Round: round, Verb: "hub.batch"})
		if err != nil {
			t.Fatal(err)
		}
		for i := range ops {
			ops[i].ID = fmt.Sprintf("%d.%d", call.Seq, i+1)
		}
		if _, err := stepcall.Append(dir, stepcall.Line{Kind: stepcall.KindResult, Step: step, Round: round, Verb: "hub.batch", Call: call.Seq, Outcome: stepcall.OutcomeOK, Pending: ops}); err != nil {
			t.Fatal(err)
		}
	}
}

// fileAndMove is a fix ticket filed and the landed item moved naming it.
func fileAndMove() []stepcall.Op {
	return []stepcall.Op{
		{Verb: stepcall.HubFile, Key: "k1", Project: "proj", Lane: "develop", Goal: "fix it", Content: "c"},
		{Verb: stepcall.HubAttrs, Target: "wi_landed", Name: "integration", Value: json.RawMessage(`{"state":"landed","fix_tickets":["key:k1"]}`), If: map[string]any{"integration.state": "landed"}},
	}
}

// execOutcomes answers each pending write's newest execution outcome.
func execOutcomes(t *testing.T, dir string) map[string]string {
	t.Helper()
	lines, err := stepcall.Read(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for id, l := range stepcall.Executions(lines) {
		out[id] = l.Outcome
	}
	return out
}

// handoffDocOf reads the capsule's hand-off document.
func handoffDocOf(t *testing.T, dir string) string {
	t.Helper()
	raw, err := os.ReadFile(stepcall.HandoffPath(dir))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

// handedOff is a dispatch ending with the given hand-off; act runs first
// (the step's work: its hub registrations).
func handedOff(h *stepcall.Handoff, act func()) fakeEntry {
	return fakeEntry{res: &StepResult{Handoff: h}, act: act}
}

// ownerSaid records an owner message to step (the --per-owner target).
func ownerSaid(t *testing.T, dir, step, at, text string) {
	t.Helper()
	if err := capsule.AppendAsk(dir, capsule.AskLine{At: at, Step: step, Spawn: 1, Kind: capsule.AskKindMessage, Text: text, By: "owner"}); err != nil {
		t.Fatal(err)
	}
}

// §7 done: the round's pending writes are made in order (a move naming
// the file's key gets the filed reference) before the next step
// dispatches; every one is on the ledger as done.
func TestHandoffDoneMakesThePendingWritesThenDispatchesTheNext(t *testing.T) {
	d, sp, sink, dir := handoffEnv(t, 4, spec("collect"), spec("close"))
	sp.script = []fakeEntry{
		handedOff(&stepcall.Handoff{Status: stepcall.StatusDone}, registers(t, dir, "collect", 1, fileAndMove()...)),
		done(`{}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := sink.seen(); strings.Join(got, " | ") != "file k1 | attrs wi_landed landed [wi_fix_k1]" {
		t.Fatalf("writes %v, want the file then the move naming the filed ticket", got)
	}
	if sink.spawnsAt[0] != 1 || sink.spawnsAt[1] != 1 {
		t.Fatalf("the writes must be made before the next dispatch: dispatches at each write %v", sink.spawnsAt)
	}
	for id, o := range execOutcomes(t, dir) {
		if o != stepcall.ExecDone {
			t.Fatalf("write %s: %s, want done", id, o)
		}
	}
	if x := d.Snapshot(); x.WorkflowStatus != StatusCompleted || len(sp.dispatched()) != 2 {
		t.Fatalf("status %q dispatches %v", x.WorkflowStatus, sp.dispatched())
	}
}

// §7 done + write_failed: a write aihub refuses parks the work item
// blocked (write_failed) with the step still on its dispatch; the resume
// settles the same hand-off again — the writes made before are not made
// again, the rest are, and the lane advances without dispatching the
// step again.
func TestHandoffWriteFailedParksAndTheResumeReplaysOnlyWhatIsLeft(t *testing.T) {
	d, sp, sink, dir := handoffEnv(t, 4, spec("collect"), spec("close"))
	ops := []stepcall.Op{
		{Verb: stepcall.HubAttrs, Target: "wi_a", Name: "integration", Value: json.RawMessage(`{"state":"merged"}`), If: map[string]any{"integration.state": "landed"}},
		{Verb: stepcall.HubFile, Key: "k1", Project: "proj", Lane: "develop", Goal: "fix it", Content: "c"},
	}
	sink.failFile = 1
	// the step registers its writes, then hands off: the daemon records the
	// hand-off on the call ledger, which the resume settles from
	register := registers(t, dir, "collect", 1, ops...)
	sp.script = []fakeEntry{handedOff(&stepcall.Handoff{Status: stepcall.StatusDone}, func() {
		register()
		appendLedgerHandoff(t, dir, "collect", 1, stepcall.StatusDone)
	})}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := d.Snapshot()
	if x.WorkflowStatus != StatusBlocked || !WriteFailedBlocked(x.BlockedReason) || !strings.Contains(x.BlockedReason, "aihub unreachable") {
		t.Fatalf("a failed write parks write_failed: %q %q", x.WorkflowStatus, x.BlockedReason)
	}
	if st := x.Steps["collect"]; st.Status != capsule.StepInProgress || st.Dispatch != 1 {
		t.Fatalf("the park keeps the step on its dispatch: %+v", st)
	}
	if got := sink.seen(); len(got) != 1 || !strings.HasPrefix(got[0], "attrs wi_a") {
		t.Fatalf("the writes before the failure are made, the failed one is not: %v", got)
	}

	// the resume: a new driver over the capsule (the claim), the park
	// released, the same hand-off settled again
	sp2 := &fakeSpawner{script: []fakeEntry{done(`{}`)}}
	sink.sp = sp2
	d2, err := New(Config{WI: d.wi, ExecPath: d.execPath, Workflow: d.workflow, Spawner: sp2, SpawnN: 1, HubWrites: sink})
	if err != nil {
		t.Fatal(err)
	}
	if err := d2.ResumeCurrent(); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if err := d2.Run(context.Background()); err != nil {
		t.Fatalf("resumed Run: %v", err)
	}
	if got := sink.seen(); strings.Join(got, " | ") != "attrs wi_a landed <nil> | file k1" {
		t.Fatalf("the replay makes only what is left: %v", got)
	}
	if got := sp2.dispatched(); len(got) != 1 || !strings.HasPrefix(got[0], "close@") {
		t.Fatalf("the resume advances without dispatching collect again: %v", got)
	}
	if x := d2.Snapshot(); x.WorkflowStatus != StatusCompleted {
		t.Fatalf("status %q", x.WorkflowStatus)
	}
}

// §7 fail (a gate): the round's writes are dropped (never made), a 回退
// event names the steps to redo and the count, the lane rewinds charged to
// max_rewinds, and the rewound target resumes its previous conversation.
func TestHandoffGateFailRewindsChargedWithItsEvent(t *testing.T) {
	d, sp, sink, dir := handoffEnv(t, 4, spec("build"), gateSpec("check", "build"))
	sp.script = []fakeEntry{
		done(`{}`),
		handedOff(&stepcall.Handoff{Status: stepcall.StatusFail, Reason: "the demo is red"}, registers(t, dir, "check", 1, fileAndMove()...)),
		done(`{}`), done(`{}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := sink.seen(); len(got) != 0 {
		t.Fatalf("a fail's writes reach aihub: %v", got)
	}
	for id, o := range execOutcomes(t, dir) {
		if o != stepcall.ExecDropped {
			t.Fatalf("write %s: %s, want dropped", id, o)
		}
	}
	x := d.Snapshot()
	if r := x.RewindStats.Rewinds; len(r) != 1 || r[0].Class != capsule.RewindCharged || r[0].ToStep != "build" || AgentRewindCount(r) != 1 {
		t.Fatalf("the rewind is charged: %+v", x.RewindStats)
	}
	if got := sp.dispatched(); len(got) != 4 || !sp.spawnAt(2).Resume || sp.spawnAt(2).StepID != "build" {
		t.Fatalf("the rewound build resumes its round: %v", got)
	}
	doc := handoffDocOf(t, dir)
	for _, want := range []string{"## 事件 · 回退", "`check` 第 1 轮交接 `fail`", "原因：the demo is red", "要重做的步骤（按顺序）：`build` → `check`", "回退次数：1/4"} {
		if !strings.Contains(doc, want) {
			t.Fatalf("the rewind event lacks %q:\n%s", want, doc)
		}
	}
	if x.WorkflowStatus != StatusCompleted || !strings.Contains(doc, "## 事件 · 收尾") {
		t.Fatalf("the lane completes with its 收尾 event: %q\n%s", x.WorkflowStatus, doc)
	}
}

// §7 fail citing the owner: the rewind is the owner's, charged to nothing.
func TestHandoffGateFailCitingTheOwnerIsChargedToNothing(t *testing.T) {
	d, sp, _, dir := handoffEnv(t, 1, spec("build"), gateSpec("check", "build"))
	ownerSaid(t, dir, "check", "2026-11-11T09:00:00.000000001Z", "redo it")
	sp.script = []fakeEntry{
		done(`{}`),
		handedOff(&stepcall.Handoff{Status: stepcall.StatusFail, PerOwner: "2026-11-11T09:00:00.000000001Z"}, nil),
		done(`{}`), done(`{}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := d.Snapshot()
	if r := x.RewindStats.Rewinds; len(r) != 1 || r[0].Class != capsule.RewindOwner || AgentRewindCount(r) != 0 {
		t.Fatalf("a fail citing the owner is charged to nothing: %+v", x.RewindStats)
	}
	if !strings.Contains(handoffDocOf(t, dir), "本次依据 owner 的话，不计次数") {
		t.Fatalf("the rewind event says it is uncharged:\n%s", handoffDocOf(t, dir))
	}
}

// §7 blocked: the work item parks, the round's writes dropped.
func TestHandoffBlockedParksAndDropsTheWrites(t *testing.T) {
	d, sp, sink, dir := handoffEnv(t, 4, spec("collect"), spec("close"))
	sp.script = []fakeEntry{handedOff(&stepcall.Handoff{Status: stepcall.StatusBlocked, Reason: "the registry is down"}, registers(t, dir, "collect", 1, fileAndMove()...))}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := d.Snapshot()
	why, evidence := StepBlockedParts(x.BlockedReason)
	if x.WorkflowStatus != StatusBlocked || !StepBlocked(x.BlockedReason) || why != "the registry is down" ||
		evidence != "artifacts/handoff.md, the section of step collect's call 0" {
		t.Fatalf("blocked parks with its reason and its section: %q %q (%q, %q)", x.WorkflowStatus, x.BlockedReason, why, evidence)
	}
	if got := sink.seen(); len(got) != 0 {
		t.Fatalf("a blocked hand-off's writes reach aihub: %v", got)
	}
	if o := execOutcomes(t, dir); len(o) != 2 || o["1.1"] != stepcall.ExecDropped || o["1.2"] != stepcall.ExecDropped {
		t.Fatalf("the writes are dropped on the ledger: %v", o)
	}
}

// §7 cancel / waive / rewind, each citing the owner: the writes are
// dropped; cancel ends the work item (the cancel request, parked), waive
// accepts a waivable gate's FAIL and the next step runs, rewind --to an
// earlier step rewinds charged to nothing with its 回退 event, rewind --to
// the step itself runs it again in a new conversation.
func TestHandoffOwnerStatusesActWithoutCharge(t *testing.T) {
	const at = "2026-11-11T09:30:00.000000001Z"
	waivable := gateSpec("check", "build")
	waivable.Waivable = true
	cases := []struct {
		name     string
		h        *stepcall.Handoff
		script   []fakeEntry
		check    func(t *testing.T, x *capsule.Execution, sp *fakeSpawner, doc string)
		atStep   string
		register bool
	}{
		{"cancel", &stepcall.Handoff{Status: stepcall.StatusCancel, PerOwner: at, Reason: "the owner stops it"}, nil,
			func(t *testing.T, x *capsule.Execution, sp *fakeSpawner, doc string) {
				if x.WorkflowStatus != StatusBlocked || x.CancelRequested == nil || x.CancelRequested.AnsweredBy != "owner" {
					t.Fatalf("cancel: %q %+v", x.WorkflowStatus, x.CancelRequested)
				}
				if !strings.Contains(doc, "## 事件 · 收尾") || !strings.Contains(doc, "owner 取消") {
					t.Fatalf("cancel: the 收尾 event:\n%s", doc)
				}
			}, "check", true},
		{"waive", &stepcall.Handoff{Status: stepcall.StatusWaive, PerOwner: at}, []fakeEntry{done(`{}`)},
			func(t *testing.T, x *capsule.Execution, sp *fakeSpawner, doc string) {
				if x.WorkflowStatus != StatusCompleted || x.Steps["check"].Status != capsule.StepDone || len(x.RewindStats.Rewinds) != 0 {
					t.Fatalf("waive: %q %+v %+v", x.WorkflowStatus, x.Steps["check"], x.RewindStats)
				}
				if got := sp.dispatched(); !strings.HasPrefix(got[len(got)-1], "ship@") {
					t.Fatalf("waive: the next step runs: %v", got)
				}
			}, "check", true},
		{"rewind to an earlier step", &stepcall.Handoff{Status: stepcall.StatusRewind, To: "build", PerOwner: at}, []fakeEntry{done(`{}`), done(`{}`), done(`{}`)},
			func(t *testing.T, x *capsule.Execution, sp *fakeSpawner, doc string) {
				if r := x.RewindStats.Rewinds; len(r) != 1 || r[0].Class != capsule.RewindOwner || AgentRewindCount(r) != 0 {
					t.Fatalf("rewind: charged to nothing: %+v", x.RewindStats)
				}
				if !strings.Contains(doc, "## 事件 · 回退") || !strings.Contains(doc, "要重做的步骤（按顺序）：`build` → `check`") {
					t.Fatalf("rewind: the 回退 event:\n%s", doc)
				}
			}, "check", true},
		{"rewind to itself", &stepcall.Handoff{Status: stepcall.StatusRewind, To: "check", PerOwner: at}, []fakeEntry{done(`{}`), done(`{}`)},
			func(t *testing.T, x *capsule.Execution, sp *fakeSpawner, doc string) {
				got := sp.dispatched()
				if len(got) != 4 || sp.spawnAt(2).StepID != "check" || sp.spawnAt(2).Resume || len(x.RewindStats.Rewinds) != 0 {
					t.Fatalf("rewind --to itself runs it again in a new conversation, uncharged: %v %+v", got, x.RewindStats)
				}
				if !strings.Contains(doc, "要重做的步骤（按顺序）：`check`") {
					t.Fatalf("rewind --to itself: the 回退 event:\n%s", doc)
				}
			}, "check", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, sp, sink, dir := handoffEnv(t, 0, spec("build"), waivable, spec("ship"))
			ownerSaid(t, dir, c.atStep, at, "the owner's words")
			sp.script = append([]fakeEntry{done(`{}`), handedOff(c.h, registers(t, dir, "check", 1, fileAndMove()...))}, c.script...)
			if err := d.Run(context.Background()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			if got := sink.seen(); len(got) != 0 {
				t.Fatalf("%s: writes reach aihub: %v", c.name, got)
			}
			if o := execOutcomes(t, dir); o["1.1"] != stepcall.ExecDropped || o["1.2"] != stepcall.ExecDropped {
				t.Fatalf("%s: the writes are dropped: %v", c.name, o)
			}
			c.check(t, d.Snapshot(), sp, handoffDocOf(t, dir))
		})
	}
}

// A hand-off citing a message the record does not hold for the step's
// round (another step's, a message gone) parks the work item naming it —
// the dispatch stays the step's, so the resume settles the same hand-off
// again — and nothing moves (ported from contract 3's
// intent reference check).
func TestHandoffCitingNoMessageOfTheRoundParks(t *testing.T) {
	const other = "2026-11-11T09:30:00.000000001Z"
	waivable := gateSpec("check", "build")
	waivable.Waivable = true
	d, sp, _, dir := handoffEnv(t, 0, spec("build"), waivable, spec("ship"))
	ownerSaid(t, dir, "build", other, "another step's word")
	sp.script = []fakeEntry{done(`{}`), handedOff(&stepcall.Handoff{Status: stepcall.StatusWaive, PerOwner: other}, nil)}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := d.Snapshot()
	if x.WorkflowStatus != StatusBlocked || !strings.Contains(x.BlockedReason, "citing the owner message "+other+", which the record no longer holds for its round") {
		t.Fatalf("a hand-off citing no message of the round parks naming it: %q %q", x.WorkflowStatus, x.BlockedReason)
	}
	if st := x.Steps["check"]; st.Status != capsule.StepInProgress || st.Dispatch == 0 || len(x.RewindStats.Rewinds) != 0 {
		t.Fatalf("nothing moved, the dispatch stays the step's: %+v %+v", st, x.RewindStats)
	}
}

// The workflow's last agent step cannot be skipped: a waive of its FAIL
// re-runs it in place in a new conversation, its round closed, nothing
// rewound (ported from contract 3's rewind_exhausted waive).
func TestHandoffWaiveOnTheLastStepRerunsIt(t *testing.T) {
	const at = "2026-11-11T09:30:00.000000001Z"
	last := gateSpec("check", "build")
	last.Waivable = true
	d, sp, _, dir := handoffEnv(t, 0, spec("build"), last)
	ownerSaid(t, dir, "check", at, "merge anyway")
	sp.script = []fakeEntry{done(`{}`), handedOff(&stepcall.Handoff{Status: stepcall.StatusWaive, PerOwner: at}, nil), done(`{}`)}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := d.Snapshot()
	if got := sp.dispatched(); len(got) != 3 || sp.spawnAt(2).StepID != "check" || sp.spawnAt(2).Resume || !sp.spawnAt(2).Fresh {
		t.Fatalf("the waived last step runs again in place, in a new conversation: %v %+v", got, sp.spawnAt(2))
	}
	if x.WorkflowStatus != StatusCompleted || len(x.RewindStats.Rewinds) != 0 || x.Steps["check"].Round != 2 {
		t.Fatalf("its re-run completes the lane, nothing rewound, its round closed: %q %+v round %d", x.WorkflowStatus, x.RewindStats, x.Steps["check"].Round)
	}
}

// The hand-off document opens at the claim's first run with the work
// item, its goal, its acceptance section verbatim and the lane's step
// order — once (a restart leaves it as it is).
func TestHandoffDocumentOpensAtTheClaim(t *testing.T) {
	d, sp, _, dir := handoffEnv(t, 3, spec("build"), gateSpec("review", "build"), spec("merge"))
	sp.script = []fakeEntry{done(`{}`), done(`{}`), done(`{}`)}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	doc := handoffDocOf(t, dir)
	for _, want := range []string{
		"# demo-7 · 交接文档", "| 工单 | demo-7 (`wi_ho`) |", "| 项目 | proj |", "| 目标 | make the demo green |",
		"## 验收清单（工单原文）\n\n- AC1 the demo is green\n- AC2 nothing else breaks\n",
		"## 车道步骤顺序\n\n1. `build`\n2. `review`（gate，fail 回退到 `build`）\n3. `merge`（最后一步）\n",
		"回退次数上限（max_rewinds）：3",
	} {
		if !strings.Contains(doc, want) {
			t.Fatalf("the opening lacks %q:\n%s", want, doc)
		}
	}
	if strings.Contains(doc, "## Notes") || strings.Count(doc, "# demo-7 · 交接文档") != 1 {
		t.Fatalf("the opening quotes the acceptance section alone, once:\n%s", doc)
	}
	// a second driver over the same capsule (a restart) writes no second opening
	d2, err := New(Config{WI: d.wi, ExecPath: d.execPath, Workflow: d.workflow, Spawner: &fakeSpawner{}})
	if err != nil {
		t.Fatal(err)
	}
	d2.writeHandoffOpening()
	if handoffDocOf(t, dir) != doc {
		t.Fatal("the opening is written once")
	}
}

// The step round's start commits are on the record (Step.StartHeads,
// the daemon's stamp of the section's span): written by the round's
// first dispatch, kept by a re-dispatch of the same round, replaced when
// a rewind opens the next round.
func TestHandoffRoundStartCommitsAreRecorded(t *testing.T) {
	d, sp, _, dir := handoffEnv(t, 4, spec("build"), gateSpec("check", "build"))
	repo := filepath.Join(dir, capsule.DirRepos, "app")
	gitc := func(args ...string) string {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", repo, "-c", "user.email=t@t", "-c", "user.name=t"}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	gitc("init", "-q")
	gitc("commit", "-q", "--allow-empty", "-m", "c1")
	c1 := gitc("rev-parse", "HEAD")
	raw, _ := json.Marshal(capsule.Identity{WorkItemID: "wi_ho", BundleName: "b", Repos: map[string]string{"app": c1}})
	writeCapsuleFile(t, dir, capsule.CapsuleJSON, string(raw))
	d.claimRepos = ClaimRepos(dir)
	var c2 string
	sp.script = []fakeEntry{
		{res: &StepResult{Handoff: &stepcall.Handoff{Status: stepcall.StatusDone}}, act: func() { gitc("commit", "-q", "--allow-empty", "-m", "c2"); c2 = gitc("rev-parse", "HEAD") }},
		gateFAIL(), done(`{}`), done(`{}`),
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := d.Snapshot()
	if sh := x.Steps["build"].StartHeads; sh == nil || sh.Round != 2 || sh.Heads["app"] != c2 {
		t.Fatalf("build's second round starts at c2 (%s): %+v", c2, sh)
	}
	if sh := x.Steps["check"].StartHeads; sh == nil || sh.Round != 2 || sh.Heads["app"] != c2 {
		t.Fatalf("check's round 2 start: %+v", sh)
	}
	_ = c1
}
