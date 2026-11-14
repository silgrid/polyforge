package driver

// wake_test.go: the owner's message to a parked work item (workflow
// contract 4, design #483 §7 "叫醒谁") — the park's
// classification (WakeOf, one case per row of the table) and the shape of
// the woken dispatch: a step that handed off `blocked` resumes its session
// with the message alone, derived from the capsule's records so the
// re-claim of the parked work item (a new driver) keeps it.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

// wakeCapsule is a capsule fixture: its execution record, spawn journal,
// call ledger and ask record.
type wakeCapsule struct {
	t   *testing.T
	dir string
}

func newWakeCapsule(t *testing.T, ex *capsule.Execution) *wakeCapsule {
	t.Helper()
	c := &wakeCapsule{t: t, dir: t.TempDir()}
	if ex != nil {
		writeExecFixture(t, filepath.Join(c.dir, capsule.ExecutionJSON), ex)
	}
	return c
}

// journal appends a spawn-journal line of dispatch n of step.
func (c *wakeCapsule) journal(step string, n int) {
	c.t.Helper()
	p := filepath.Join(c.dir, capsule.DirRuntimeState, capsule.SpawnJournalFile)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		c.t.Fatal(err)
	}
	raw, _ := json.Marshal(capsule.SpawnJournalEntry{StepID: step, SpawnN: n, Harness: "pi",
		Transcript: filepath.Join(c.dir, capsule.DirSession, "s.jsonl")})
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		c.t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(append(raw, '\n')); err != nil {
		c.t.Fatal(err)
	}
}

// handoff records an accepted hand-off of dispatch n of step, with the
// given pending writes (their execution recorded done when made).
func (c *wakeCapsule) handoff(step string, n int64, status string, writes []stepcall.Op, made bool) {
	c.t.Helper()
	_, err := stepcall.Append(c.dir, stepcall.Line{Kind: stepcall.KindResult, Step: step, Round: 1, Spawn: n, Verb: stepcall.VerbHandoff,
		Outcome: stepcall.OutcomeOK, Handoff: &stepcall.Handoff{Status: status}, Pending: writes})
	if err != nil {
		c.t.Fatal(err)
	}
	if made {
		for _, op := range writes {
			if _, err := stepcall.Append(c.dir, stepcall.Line{Kind: stepcall.KindExec, Step: step, Spawn: n, Op: op.ID, Outcome: stepcall.ExecDone}); err != nil {
				c.t.Fatal(err)
			}
		}
	}
}

func (c *wakeCapsule) ask(l capsule.AskLine) {
	c.t.Helper()
	if err := capsule.AppendAsk(c.dir, l); err != nil {
		c.t.Fatal(err)
	}
}

func blockedExec(reason string) *capsule.Execution {
	return &capsule.Execution{
		CapsuleID: "cap_wi_run", WorkItemID: "wi_run", WorkflowStatus: StatusBlocked, CurrentStep: "s", BlockedReason: reason, Version: 3,
		Steps: map[string]*capsule.Step{"s": {Status: capsule.StepInProgress, Round: 1, StepStartedAt: "2026-11-11T10:00:00Z"}},
	}
}

// TestWakeOfClassifiesParks: one case per row of the design's wake table.
func TestWakeOfClassifiesParks(t *testing.T) {
	write := []stepcall.Op{{ID: "1.1", Verb: stepcall.HubAttrs, Target: "wi_x", Name: "integration", Value: json.RawMessage(`{"state":"merged"}`)}}
	for _, tc := range []struct {
		name  string
		ex    *capsule.Execution
		setup func(c *wakeCapsule)
		want  WakeKind
		spawn int64
	}{
		{"not parked", &capsule.Execution{WorkflowStatus: StatusRunning, CurrentStep: "s"}, nil, WakeNone, 0},
		{"the step's own blocked hand-off", blockedExec("step s handed off blocked"), func(c *wakeCapsule) {
			c.journal("s", 1)
			c.journal("s", 2)
			c.handoff("s", 2, stepcall.StatusBlocked, nil, false)
		}, WakeStepBlocked, 2},
		{"a blocked hand-off an older dispatch made: a later one parked it", blockedExec("step s ended without `pf step handoff` 3 times in a row"), func(c *wakeCapsule) {
			c.journal("s", 2)
			c.handoff("s", 2, stepcall.StatusBlocked, nil, false)
			c.journal("s", 3)
		}, WakeStep, 3},
		{"a supervision block", blockedExec("step s timed out again (tool_hung) after its bound was already doubled once: x"), func(c *wakeCapsule) {
			c.journal("s", 4)
		}, WakeStep, 4},
		{"a done hand-off whose writes failed", blockedExec("write_failed: step s"), func(c *wakeCapsule) {
			c.journal("s", 2)
			c.handoff("s", 2, stepcall.StatusDone, write, false)
		}, WakeWriteFailed, 2},
		{"a done hand-off whose writes were all made is no write park", blockedExec("parked"), func(c *wakeCapsule) {
			c.journal("s", 2)
			c.handoff("s", 2, stepcall.StatusDone, write, true)
		}, WakeStep, 2},
		{"no usable model", blockedExec(NoUsableModelBlockPrefix + "step s: a model-class death"), nil, WakeCannotStart, 0},
		{"a lane snapshot of another contract", blockedExec(contractReasonPrefix + "the capsule's lane snapshot targets workflow contract 3"), nil, WakeCannotStart, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newWakeCapsule(t, nil)
			if tc.setup != nil {
				tc.setup(c)
			}
			w := WakeOf(c.dir, tc.ex)
			if w.Kind != tc.want || (tc.want != WakeNone && w.Spawn != tc.spawn) {
				t.Fatalf("WakeOf = %+v, want kind %q spawn %d", w, tc.want, tc.spawn)
			}
			if tc.want != WakeCannotStart {
				return
			}
			refusal := CannotStartRefusal("wi_run", w)
			if w.Why != tc.ex.BlockedReason || !strings.Contains(refusal, "pf cancel wi_run") {
				t.Fatalf("a cannot-start park names why and `pf cancel`: %+v / %q", w, refusal)
			}
			// a contract park offers no resume: it parks again
			if resume := strings.Contains(refusal, "pf resume wi_run"); resume == ContractBlocked(w.Why) {
				t.Fatalf("`pf resume` is offered for every cannot-start park but the contract one (resume=%v): %q", resume, refusal)
			}
		})
	}
}

// TestWakeOfAParkOfThePreviousContractCannotStart: a work
// item parked before the upgrade — whatever its reason — keeps a lane
// snapshot of the previous contract, whose claim would only park it
// `contract`: the message cannot wake it, and the refusal names `pf
// cancel` alone. The same park on a snapshot of this contract wakes.
func TestWakeOfAParkOfThePreviousContractCannotStart(t *testing.T) {
	for _, tc := range []struct {
		version int
		want    WakeKind
	}{{internalBuildContract, WakeCannotStart}, {workflow.ContractVersion, WakeStep}} {
		capDir := oldSnapshotCapsule(t, tc.version)
		writeCapsuleFile(t, capDir, capsule.CapsuleJSON, `{"work_item_id":"wi_old","capsule_id":"cap_wi_old","bundle_name":"micro"}`)
		ex := &capsule.Execution{WorkItemID: "wi_old", CurrentStep: "fix", WorkflowStatus: StatusBlocked,
			BlockedReason: "step fix timed out again (tool_hung) after its bound was already doubled once: x",
			Steps:         map[string]*capsule.Step{"fix": {Status: capsule.StepInProgress}}}
		w := WakeOf(capDir, ex)
		if w.Kind != tc.want {
			t.Fatalf("contract %d: WakeOf = %+v, want %q", tc.version, w, tc.want)
		}
		if tc.want != WakeCannotStart {
			continue
		}
		refusal := CannotStartRefusal("wi_old", w)
		if !ContractBlocked(w.Why) || !strings.Contains(w.Why, fmt.Sprintf("targets workflow contract %d", tc.version)) ||
			!strings.Contains(refusal, "pf cancel wi_old") || strings.Contains(refusal, "pf resume") {
			t.Fatalf("the previous contract's park cannot start and offers `pf cancel` alone: %+v / %q", w, refusal)
		}
	}
}

// TestWakeOfCompositionFailure: the block composeDispatch writes when the
// candidate composition fails is a park whose agent cannot start (the two
// are pinned together by compositionFailedMarker).
func TestWakeOfCompositionFailure(t *testing.T) {
	env := newSupEnv(t, func(c *Config) {
		c.Compose = func() (map[string][]string, error) { return nil, errors.New("DAEMON-414: broken yaml") }
	}, spec("s"))
	if err := env.d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	x := env.exec(t)
	if x.WorkflowStatus != StatusBlocked {
		t.Fatalf("status %q, want blocked by the composition", x.WorkflowStatus)
	}
	if w := WakeOf(env.d.wi.CapsuleDir, x); w.Kind != WakeCannotStart {
		t.Fatalf("WakeOf(%q) = %+v, want cannot_start", x.BlockedReason, w)
	}
}

// reclaimDriver is the re-claim of a parked capsule: a new driver over
// it, the owner's resume applied (driver.Start's ResumeCurrent), one
// delivering dispatch scripted.
func reclaimDriver(t *testing.T, dir string, spawnN int64) (*Driver, *fakeSpawner) {
	t.Helper()
	sp := &fakeSpawner{script: []fakeEntry{done(`{}`)}}
	d, err := New(Config{
		WI:       WIInfo{ID: "wi_run", CapsuleDir: dir, BundleName: "b"},
		ExecPath: filepath.Join(dir, capsule.ExecutionJSON),
		Workflow: testLane(4, spec("s")),
		Spawner:  sp,
		Log:      &safeLog{},
		SpawnN:   spawnN,
		Sleep:    func(context.Context, time.Duration) {},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, sp
}

// TestWakeOfBlockedStepResumesWithTheMessageAlone: a step that handed off
// `blocked` is woken by the owner's message — the re-claim's new driver
// resumes the step's session and its first message is the owner's words
// alone (the conversation holds the prompt and the reason it stopped). The
// in-memory note of the old driver is gone; the capsule's records carry
// the fact. A later dispatch that did not hand off blocked (here dispatch
// 3) is resumed with the full prompt instead.
func TestWakeOfBlockedStepResumesWithTheMessageAlone(t *testing.T) {
	for _, tc := range []struct {
		name      string
		later     bool
		wantShort bool
	}{
		{"the blocked hand-off is the newest conversation", false, true},
		{"a later dispatch ran after it", true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newWakeCapsule(t, blockedExec("step s handed off blocked"))
			c.journal("s", 2)
			c.handoff("s", 2, stepcall.StatusBlocked, nil, false)
			n := int64(2)
			if tc.later {
				c.journal("s", 3)
				n = 3
			}
			// the wake's record, as the claim lands it in the claimed scene
			c.ask(capsule.AskLine{At: "2026-11-11T11:00:00Z", Step: "s", Spawn: n, Kind: capsule.AskKindMessage, Text: "the token is in vault, go on", By: "alice"})
			d, sp := reclaimDriver(t, c.dir, n)
			if err := d.ResumeCurrent(); err != nil {
				t.Fatalf("ResumeCurrent: %v", err)
			}
			if err := d.Run(context.Background()); err != nil {
				t.Fatalf("Run: %v", err)
			}
			got := sp.spawnAt(0)
			if !got.Resume || got.MessageResume != tc.wantShort || len(got.OwnerMessages) != 1 || got.OwnerMessages[0].Text != "the token is in vault, go on" {
				t.Fatalf("the woken dispatch: resume=%v messageResume=%v messages=%+v, want a resume carrying the message (alone: %v)",
					got.Resume, got.MessageResume, got.OwnerMessages, tc.wantShort)
			}
		})
	}
}

// TestAskParkMessageResumeSurvivesRestart: a dispatch that asked the
// owner and ended awaiting (its process gone), replied to while no driver
// ran (a daemon restart in between): the successor resumes the session
// with the reply alone — before the fact lived in the old
// driver's memory and the restart sent the full prompt instead.
func TestAskParkMessageResumeSurvivesRestart(t *testing.T) {
	ex := blockedExec("")
	ex.WorkflowStatus = StatusRunning
	ex.Steps["s"].Dispatch = 2
	c := newWakeCapsule(t, ex)
	c.journal("s", 2)
	c.ask(capsule.AskLine{At: "2026-11-11T10:05:00Z", Step: "s", Spawn: 2, Kind: capsule.AskKindAsk, Text: "which region?"})
	c.ask(capsule.AskLine{At: "2026-11-11T10:30:00Z", Step: "s", Spawn: 2, Kind: capsule.AskKindReply, AskAt: "2026-11-11T10:05:00Z", Text: "tokyo", By: "alice"})
	d, sp := reclaimDriver(t, c.dir, 2)
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := sp.spawnAt(0)
	if !got.Resume || !got.MessageResume || len(got.OwnerMessages) != 1 || got.OwnerMessages[0].Text != "tokyo" {
		t.Fatalf("the successor's dispatch: resume=%v messageResume=%v messages=%+v, want a resume with the reply alone",
			got.Resume, got.MessageResume, got.OwnerMessages)
	}
}
