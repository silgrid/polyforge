package driver

// epic_lane_contract_test.go (polyforge#117 u2, AC1): the shipped epic
// lane — verify-children → sync → build → review → verification →
// publish → merge, the two preparation steps without a backward rewind of
// their own, build rewind_only (the fix build runs only after a rewind
// covers it), every gate after build rewinding to build and merge's
// moved-base FAIL back to sync (the uncounted class), review in the epic
// mode (the total acceptance, the children snapshot and their PR bodies,
// the seams and the final diff — never a child's internals) and
// verification explicitly full.
//
// Expected values are the work item's own AC1 order and the owner's
// settled Q1/Q2/Q7, not the implementation's shape: the order is the
// ticket's arrow chain verbatim, the rewind targets are 「任一闸门失败
// 回到修复 build」 with merge's moved-base going back to sync, and the
// review/verification anchors are the epic-mode inputs the ticket names.

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/stepcall"
)

const epicBundle = "epic"

// epicEnv lays out a capsule whose lane is the SHIPPED epic lane (the
// bundle read from the shipped workflows tree, as a claim's snapshot
// copies it, shared tree included) and builds a driver over it with the
// scripted spawner.
func epicEnv(t *testing.T, script []fakeEntry) (*Driver, *fakeSpawner, *eventLog, string) {
	t.Helper()
	ws := t.TempDir()
	dir := filepath.Join(ws, "capsules", "wi_epic")
	writeCapsuleFile(t, dir, capsule.WorkItemJSON, `{"id":"wi_epic","slug":"demo-2","project":"proj","goal":"g"}`)
	sp := &fakeSpawner{script: script}
	log := &eventLog{}
	d, err := New(Config{
		WI:        WIInfo{ID: "wi_epic", CapsuleDir: dir, BundleName: epicBundle},
		BundleDir: filepath.Join(shippedWorkflowsDir, epicBundle),
		Spawner:   sp,
		Forge:     epicFakeForge{},
		Log:       log,
		Language:  func() string { return stepcall.LangZhCN },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return d, sp, log, dir
}

// epicFakeForge is the forge binding the scripted runs take the merge
// lock through (a capsule with no task repos names no key, so the lock
// itself is never taken).
type epicFakeForge struct{}

func (epicFakeForge) CommandEnv() []string { return nil }

func (epicFakeForge) LockRepo(context.Context, string) (func(), error) {
	return func() {}, nil
}

// epicShippedFront answers the shipped epic workflow.yaml verbatim (the
// capsule's lane snapshot is the shipped tree's copy).
func epicShippedFront(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(shippedWorkflowsDir, epicBundle, workflow.WorkflowFile))
	if err != nil {
		t.Fatalf("the shipped epic lane's workflow.yaml: %v", err)
	}
	return string(raw)
}

// TestEpicLaneContract pins the shipped epic lane's declared graph: the
// ticket's step order, the two preparation steps without on_fail, the
// gates' rewind targets (review, verification and publish to the
// rewind_only build; merge to sync), the uncounted moved-base class, the
// epic capability keys, verification waivable, review not, and the merge
// wrap attrs.
func TestEpicLaneContract(t *testing.T) {
	dir := filepath.Join(shippedWorkflowsDir, epicBundle)
	wf, err := LoadWorkflow(dir)
	if err != nil {
		t.Fatalf("the epic lane must load through LoadWorkflow: %v", err)
	}
	lane, err := workflow.LoadStrict(dir)
	if err != nil {
		t.Fatalf("the epic lane must load strict: %v", err)
	}
	want := []string{"verify-children", "sync", "build", reviewStepID, verificationStepID, publishStepID, "merge"}
	if got := laneOrder(t, epicBundle); strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("the epic lane's order must be %v, got %v", want, got)
	}
	if !lane.Epic || lane.ChildrenBranch != "epic/{seq}" {
		t.Fatalf("the epic lane declares its capability: epic %v children_branch %q", lane.Epic, lane.ChildrenBranch)
	}
	if wf.MaxRewinds != 3 {
		t.Fatalf("the epic lane's rewind budget is 3, got %d", wf.MaxRewinds)
	}
	if lane.UncountedRewinds == nil || lane.UncountedRewinds.ReasonContains != "branch moved since verification" || lane.UncountedRewinds.Max != 2 {
		t.Fatalf("the epic lane declares the moved-base uncounted class (max 2): %+v", lane.UncountedRewinds)
	}
	// the two preparation steps carry no backward rewind of their own
	for _, id := range []string{"verify-children", "sync"} {
		if s := lane.Step(id); s == nil || s.Gate || s.OnFailTarget != "" {
			t.Fatalf("%s is a preparation step: no gate, no on_fail: %+v", id, s)
		}
	}
	// the gates after build rewind to it; merge's moved base goes back to
	// sync (the re-sync and the re-run of everything after it)
	for id, target := range map[string]string{
		reviewStepID: "build", verificationStepID: "build", publishStepID: "build", "merge": "sync",
	} {
		s := lane.Step(id)
		if s == nil || !s.Gate || s.OnFailTarget != target {
			t.Fatalf("%s is a gate rewinding to %s: %+v", id, target, s)
		}
	}
	if s := lane.Step("build"); s == nil || s.Gate || !s.RewindOnly {
		t.Fatalf("build is the rewind_only fix build: %+v", s)
	}
	if v := lane.Step(verificationStepID); v == nil || !v.Waivable {
		t.Fatalf("verification is the waivable full gate: %+v", v)
	}
	if r := lane.Step(reviewStepID); r == nil || r.Waivable {
		t.Fatalf("review is never waived: %+v", r)
	}
	if got := wf.WrapAttrs("merge"); !reflect.DeepEqual(got, []string{"integration"}) {
		t.Fatalf("the epic merge registers the landing record: wrap attrs %v", got)
	}
	// review resolves to the lane's OWN epic-mode skill, not the shared one
	if s := lane.Step(reviewStepID); s == nil || s.Skill != reviewStepID {
		t.Fatalf("review's skill is the lane's own: %+v", s)
	}
	if _, err := os.Stat(filepath.Join(dir, "skills", reviewStepID+".md")); err != nil {
		t.Fatalf("the epic lane ships its own review skill: %v", err)
	}
	// the fix build inherits the plan and never re-makes it (AC1): the
	// definition a rewound dispatch renders says both
	build := readBundleFile(t, epicBundle, "steps", "build.md")
	for _, want := range []string{"never runs on the first round", "inheriting the plan", "never re-planning"} {
		if !strings.Contains(build, want) {
			t.Errorf("the epic fix build's definition must state %q (the inherited plan, never re-planned)", want)
		}
	}
}

// TestEpicRewindBuild: the first round records the rewind_only build
// skipped without a dispatch and the lane completes over it; a gate FAIL
// after it runs the fix build, and the lane continues from it.
func TestEpicRewindBuild(t *testing.T) {
	t.Run("the first round skips the fix build", func(t *testing.T) {
		d, sp, log, dir := epicEnv(t, []fakeEntry{
			done(`{}`), done(`{}`), done(`{}`), done(`{}`), done(`{}`), done(`{}`),
		})
		if err := d.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := rewindDispatched(sp); !reflect.DeepEqual(got,
			[]string{"verify-children", "sync", reviewStepID, verificationStepID, publishStepID, "merge"}) {
			t.Fatalf("dispatched %v, want the lane over the skipped build", got)
		}
		x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
		if x.WorkflowStatus != StatusCompleted || x.Steps["build"].Status != capsule.StepSkipped {
			t.Fatalf("the first round completes with build skipped: %q build %q", x.WorkflowStatus, x.Steps["build"].Status)
		}
		if n := log.count("step_skipped"); n != 1 || !log.has("step_skipped", "step", "build") {
			t.Fatalf("events %v: want one step_skipped {step: build}", log.kinds())
		}
	})
	t.Run("a gate FAIL runs the fix build and continues from it", func(t *testing.T) {
		d, sp, _, dir := epicEnv(t, []fakeEntry{
			done(`{}`), done(`{}`), gateFAIL(), done(`{}`), done(`{}`), done(`{}`), done(`{}`), done(`{}`),
		})
		if err := d.Run(context.Background()); err != nil {
			t.Fatalf("Run: %v", err)
		}
		if got := rewindDispatched(sp); !reflect.DeepEqual(got,
			[]string{"verify-children", "sync", reviewStepID, "build", reviewStepID, verificationStepID, publishStepID, "merge"}) {
			t.Fatalf("dispatched %v, want the FAIL to run build then the lane again", got)
		}
		x := readExec(t, filepath.Join(dir, capsule.ExecutionJSON))
		if x.WorkflowStatus != StatusCompleted || x.Steps["build"].Status != capsule.StepDone || x.RewindStats.Count != 1 {
			t.Fatalf("the fix build ran after the FAIL: %q build %q rewinds %+v", x.WorkflowStatus, x.Steps["build"].Status, x.RewindStats)
		}
	})
}

// TestEpicReviewScope pins the epic review's own inputs (Q7): the total
// acceptance, the children snapshot and their PR bodies, the seams and
// the final diff — never a child's internals; only a rewind fix's
// increments read the build evidence; a big problem asks the owner for a
// separate fix sub-ticket.
func TestEpicReviewScope(t *testing.T) {
	skill := readBundleFile(t, epicBundle, "skills", reviewStepID+".md")
	for _, want := range []string{
		"the total acceptance",
		"runtime-state/children.json",
		"gh pr view",
		"seams",
		"never re-audits a child's internals",
		"artifacts/build/units.json",
		"a separate fix sub-ticket",
	} {
		if !strings.Contains(skill, want) {
			t.Errorf("the epic review skill must state %q", want)
		}
	}
}

// TestEpicVerificationFullTier pins the epic verification step's own
// definition: the full tier explicitly, the build's change analysis only
// when a rewind fix build wrote it.
func TestEpicVerificationFullTier(t *testing.T) {
	def, err := readShippedDefinition(t, filepath.Join(shippedWorkflowsDir, epicBundle), verificationStepID)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"--tier full", "artifacts/build/change-analysis.json"} {
		if !strings.Contains(def, want) {
			t.Errorf("the epic verification definition must state %q", want)
		}
	}
}
