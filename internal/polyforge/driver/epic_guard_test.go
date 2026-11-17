package driver

// epic_guard_test.go (polyforge#117 u5, AC2/Q5): the driver's running
// re-check of an epic's children — before EVERY dispatch it compares the
// hub's current children with the claim's immutable snapshot: a child
// that failed or was canceled parks the workflow blocked on the driver's
// own authority (no question, no dispatch), a child the snapshot does not
// carry parks it naming the gain, and an unchanged set lets the lane
// proceed. The resume half: the marker a changed snapshot wrote
// (runtime-state/epic-resume.json) applies at the plan pass after the
// owner's resume as an owner-class rewind to review — the affected steps
// re-run, charged to no budget — and is consumed.
//
// Expected values are the owner's settled Q3/Q5 and AC2's own words
// (阻塞交给 owner, daemon 不提问), not the implementation's shape: the
// blocked reason names the child and its status, the gain names the
// child, and the reset is one owner-class rewind to review with the
// agent budget untouched.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
)

// epicGuardEnv builds the shipped-lane driver of epicEnv over a capsule
// that carries the snapshot given, with the guard answering live().
func epicGuardEnv(t *testing.T, snapshot capsule.EpicChildren, script []fakeEntry,
	live func() ([]capsule.EpicChild, error)) (*Driver, *fakeSpawner, *eventLog, string) {
	t.Helper()
	d, sp, log, dir := epicEnv(t, script)
	raw, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	writeCapsuleFile(t, dir, capsule.ChildrenJSON, string(raw))
	d.cfg.EpicChildren = live
	return d, sp, log, dir
}

// epicLiveChildren is the guard's answer over the children given.
func epicLiveChildren(cs ...capsule.EpicChild) func() ([]capsule.EpicChild, error) {
	return func() ([]capsule.EpicChild, error) { return cs, nil }
}

// epicWrap is one wrapped child of a snapshot or a guard answer.
func epicWrap(id, slug string) capsule.EpicChild {
	return capsule.EpicChild{ID: id, Slug: slug, Status: "wrapped"}
}

// TestEpicChildBlocked: a child that failed parks the workflow blocked
// before the dispatch, the reason naming the child and its status — no
// question, no dispatch.
func TestEpicChildBlocked(t *testing.T) {
	d, sp, log, dir := epicGuardEnv(t,
		capsule.EpicChildren{Version: 1, Children: []capsule.EpicChild{epicWrap("wi_c1", "demo#1")}},
		nil,
		epicLiveChildren(capsule.EpicChild{ID: "wi_c1", Slug: "demo#1", Status: "failed"}))
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rewindDispatched(sp); len(got) != 0 {
		t.Fatalf("nothing may dispatch over a failed child, got %v", got)
	}
	x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
	if x.WorkflowStatus != StatusBlocked || x.CurrentStep != "verify-children" {
		t.Fatalf("the workflow parks blocked at the step about to dispatch: %q at %q", x.WorkflowStatus, x.CurrentStep)
	}
	if !strings.Contains(x.BlockedReason, "demo#1") || !strings.Contains(x.BlockedReason, "failed") {
		t.Fatalf("the blocked reason names the child and its status, got %q", x.BlockedReason)
	}
	if kinds := log.kinds(); reflect.DeepEqual(kinds, []string{"exec_init", "blocked"}) == false {
		t.Fatalf("the park emits no question of its own: events %v", kinds)
	}
}

// TestEpicNewChildBlocks: a child the snapshot does not carry parks the
// workflow blocked naming the gain.
func TestEpicNewChildBlocks(t *testing.T) {
	d, sp, _, dir := epicGuardEnv(t,
		capsule.EpicChildren{Version: 1, Children: []capsule.EpicChild{epicWrap("wi_c1", "demo#1")}},
		nil,
		epicLiveChildren(epicWrap("wi_c1", "demo#1"), epicWrap("wi_c2", "demo#2")))
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rewindDispatched(sp); len(got) != 0 {
		t.Fatalf("nothing may dispatch over a new child, got %v", got)
	}
	x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
	if x.WorkflowStatus != StatusBlocked || !strings.Contains(x.BlockedReason, "demo#2") {
		t.Fatalf("a new child parks the workflow naming it: %q (%q)", x.BlockedReason, x.WorkflowStatus)
	}
}

// TestEpicGuardUnchangedPasses: an unchanged set lets the lane proceed —
// the whole lane completes, no blocked event.
func TestEpicGuardUnchangedPasses(t *testing.T) {
	d, sp, log, dir := epicGuardEnv(t,
		capsule.EpicChildren{Version: 1, Children: []capsule.EpicChild{epicWrap("wi_c1", "demo#1")}},
		[]fakeEntry{done(`{}`), done(`{}`), done(`{}`), done(`{}`), done(`{}`), done(`{}`)},
		epicLiveChildren(epicWrap("wi_c1", "demo#1")))
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rewindDispatched(sp); len(got) != 6 {
		t.Fatalf("the lane runs its six dispatches over an unchanged set, got %v", got)
	}
	x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
	if x.WorkflowStatus != StatusCompleted {
		t.Fatalf("an unchanged set completes the lane, got %q", x.WorkflowStatus)
	}
	if n := log.count("blocked"); n != 0 {
		t.Fatalf("an unchanged set parks nothing: %d blocked events", n)
	}
}

// TestEpicSnapshotResumeResetsReview: after the guard parks the epic over
// a changed child, the owner resumes — the marker the changed snapshot
// wrote applies as ONE owner-class rewind to review (the agent budget
// untouched), the lane re-runs from review and completes, and the marker
// is consumed.
func TestEpicSnapshotResumeResetsReview(t *testing.T) {
	live := []capsule.EpicChild{epicWrap("wi_c1", "demo#1")}
	guard := func() ([]capsule.EpicChild, error) { return live, nil }
	d, sp, _, dir := epicGuardEnv(t,
		capsule.EpicChildren{Version: 1, Children: []capsule.EpicChild{epicWrap("wi_c1", "demo#1")}},
		[]fakeEntry{done(`{}`), done(`{}`), done(`{}`), done(`{}`), done(`{}`),
			done(`{}`), done(`{}`), done(`{}`), done(`{}`)},
		guard)
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := rewindDispatched(sp); reflect.DeepEqual(got, []string{"verify-children", "sync", reviewStepID, verificationStepID, publishStepID}) {
		t.Fatalf("the guard parks the epic before merge, got %v", got)
	}
	x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
	if x.WorkflowStatus != StatusBlocked {
		t.Fatalf("the epic parks blocked over the changed child, got %q", x.WorkflowStatus)
	}
	// the owner's fix child lands: the re-claim wrote snapshot v2 and
	// the marker; the guard answers the new set
	live = []capsule.EpicChild{epicWrap("wi_c1", "demo#1"), epicWrap("wi_c2", "demo#2")}
	next, _ := json.Marshal(capsule.EpicChildren{Version: 2, Children: live})
	writeCapsuleFile(t, dir, capsule.ChildrenJSON, string(next))
	writeCapsuleFile(t, dir, capsule.EpicResumeJSON, `{"reset_from": "review", "snapshot_version": 2}`)
	if err := d.ResumeCurrent(); err != nil {
		t.Fatalf("the owner resumes: %v", err)
	}
	if err := d.Run(context.Background()); err != nil {
		t.Fatalf("the resumed Run: %v", err)
	}
	if got := rewindDispatched(sp); !reflect.DeepEqual(got,
		[]string{"verify-children", "sync", reviewStepID, verificationStepID, publishStepID, reviewStepID, verificationStepID, publishStepID, "merge"}) {
		t.Fatalf("the resume re-runs from review, got %v", got)
	}
	x = readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
	if x.WorkflowStatus != StatusCompleted {
		t.Fatalf("the resumed epic completes, got %q", x.WorkflowStatus)
	}
	if x.RewindStats.Count != 0 || len(x.RewindStats.Rewinds) != 1 {
		t.Fatalf("the reset is one rewind charged to no budget: %+v", x.RewindStats)
	}
	if r := x.RewindStats.Rewinds[0]; r.Class != capsule.RewindOwner || r.ToStep != reviewStepID || r.Gate != "" {
		t.Fatalf("the reset is an owner-class rewind to review: %+v", r)
	}
	if st := x.Steps[reviewStepID]; st == nil || st.Status != capsule.StepDone || st.Round < 2 {
		t.Fatalf("review re-ran on its second round: %+v", st)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(capsule.EpicResumeJSON))); !os.IsNotExist(err) {
		t.Fatalf("the marker is consumed by the reset, got %v", err)
	}
}
