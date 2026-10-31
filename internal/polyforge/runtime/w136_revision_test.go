package runtime

// w136_revision_test.go: the paused-record half of the owner revision
// face. A paused work item has no live driver, so the
// runtime applies the revision through a record-only driver and commits
// the corrected execution.json to the capsule chain. This test pins both
// halves of the reported "the fix cannot persist": the corrected binding
// is in the canonical record, and the chain gained a fast-forward commit
// carrying it (no history rewrite).

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/api"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

const (
	w136RuntimeBad  = `YINGFANG_TEST_DSN="$(grep -m1 -o 'postgres://[a-zA-Z0-9_:@./?=-]*' tests/e2e_s37/main_test.go)' go test ./tests/e2e_cp37/ -count=1 -v`
	w136RuntimeGood = `YINGFANG_TEST_DSN="$(grep -m1 -o 'postgres://[a-zA-Z0-9_:@./?=-]*' tests/e2e_s37/main_test.go)" go test ./tests/e2e_cp37/ -count=1 -v`
)

func TestW136PausedRecordRevisionPersistsToTheChain(t *testing.T) {
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.ReviseRecord = func(capsuleDir, bundleName, sessionHandle, wi string, spawnN int64, stepID string, corrections []capsule.BindingCorrection, by, reason string) error {
		d, err := driver.OpenRecord(driver.Config{
			WI:     driver.WIInfo{ID: wi, CapsuleDir: capsuleDir, BundleName: bundleName, SessionHandle: sessionHandle},
			SpawnN: spawnN,
		})
		if err != nil {
			return err
		}
		return d.ReviseBinding(stepID, driver.BindingRevisionRequest{Corrections: corrections, By: by, Reason: reason})
	}
	j := seedJob(r, "wi_w136_rev")
	bare := initGitCapsule(t, j)

	// The capsule's pinned workflow and the malformed canonical record.
	if err := os.MkdirAll(filepath.Join(j.capsuleDir, "workflows", "demo-bundle"), 0o755); err != nil {
		t.Fatal(err)
	}
	wf := "---\nname: demo-bundle\nsteps:\n  - { id: e2e-author, context_policy: fresh }\n---\n"
	if err := os.WriteFile(filepath.Join(j.capsuleDir, "workflows", "demo-bundle", "workflow.md"), []byte(wf), 0o644); err != nil {
		t.Fatal(err)
	}
	// The yingfang#37 shape: every binding carries the same unpaired quote,
	// so one single-index revision could never correct the record.
	bad := make([]string, 3)
	good := make([]string, 3)
	corrections := make([]capsule.BindingCorrection, 3)
	bindings := make([]any, 3)
	titles := make([]any, 3)
	for i := range bad {
		bad[i] = fmt.Sprintf("%s # binding %d", w136RuntimeBad, i)
		good[i] = fmt.Sprintf("%s # binding %d", w136RuntimeGood, i)
		corrections[i] = capsule.BindingCorrection{Index: i, Command: good[i]}
		titles[i] = fmt.Sprintf("S%d", i+1)
		bindings[i] = map[string]any{
			"title": titles[i], "repo": "yingfang", "command": bad[i],
			"unit": fmt.Sprintf("TestS%d", i+1), "marker": fmt.Sprintf("^PASS S%d$", i+1),
		}
	}
	ex := &capsule.Execution{
		CapsuleID:      "cap_" + j.wi,
		WorkItemID:     j.wi,
		CurrentStep:    "e2e-author",
		WorkflowStatus: capsule.WorkflowStatusBlocked,
		Steps: map[string]*capsule.Step{
			"e2e-author": {
				Status: capsule.StepDone,
				Output: map[string]any{"scenario_titles": titles, "bindings": bindings},
			},
		},
	}
	blob, err := json.Marshal(ex)
	if err != nil {
		t.Fatal(err)
	}
	execPath := filepath.Join(j.capsuleDir, capsule.ExecutionJSON)
	if err := os.MkdirAll(filepath.Dir(execPath), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(execPath, blob, 0o644); err != nil {
		t.Fatal(err)
	}
	j.mu.Lock()
	j.state = StatePaused
	j.mu.Unlock()
	// The paused path verifies the session before it pushes under it.
	hub.hold(j.wi, j.sessionID, api.StatusPaused)

	if err := r.ReviseBinding(context.Background(), j.wi, "e2e-author", corrections, "usr_owner", "yingfang#37 pair every quote in one pass"); err != nil {
		t.Fatalf("ReviseBinding on a paused record: %v", err)
	}

	// The canonical record (what a re-claim materializes) carries the
	// correction and its audit entry.
	raw, err := os.ReadFile(execPath)
	if err != nil {
		t.Fatal(err)
	}
	var got capsule.Execution
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	gotBindings := got.Steps["e2e-author"].Output["bindings"].([]any)
	for i := range good {
		cmd := gotBindings[i].(map[string]any)["command"]
		if cmd != good[i] {
			t.Fatalf("canonical bindings[%d].command = %q, want %q", i, cmd, good[i])
		}
	}
	revs := got.Steps["e2e-author"].BindingRevisions
	if len(revs) != len(corrections) {
		t.Fatalf("revision audit records = %d, want one per corrected index (%d): %+v", len(revs), len(corrections), revs)
	}
	for i, rev := range revs {
		if rev.Index != i || rev.Before != bad[i] || rev.After != good[i] || rev.By != "usr_owner" {
			t.Fatalf("revision audit record %d = %+v", i, rev)
		}
	}

	// The chain got the correction by appending: the seed commit plus one.
	out, err := exec.Command("git", "--git-dir", bare, "rev-list", "--count", "main").CombinedOutput()
	if err != nil {
		t.Fatalf("count chain commits: %v: %s", err, out)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("chain commits = %d, want the seed plus the revision (an append, never a rewrite)", n)
	}
	show, err := exec.Command("git", "--git-dir", bare, "show", "main:"+capsule.ExecutionJSON).Output()
	if err != nil {
		t.Fatalf("read the chain's execution.json: %v", err)
	}
	var onChain capsule.Execution
	if err := json.Unmarshal(show, &onChain); err != nil {
		t.Fatalf("parse the chain's execution.json: %v", err)
	}
	onChainBindings := onChain.Steps["e2e-author"].Output["bindings"].([]any)
	for i := range good {
		if cmd := onChainBindings[i].(map[string]any)["command"]; cmd != good[i] {
			t.Fatalf("the pushed chain record carries bindings[%d].command = %q, want %q", i, cmd, good[i])
		}
	}
}
