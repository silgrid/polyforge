package runtime

// w429_contract_reclaim_test.go ( AC7, contract §13.4 "resume
// / re-claim ⇒ blocked contract"): a re-claim — `pf resume` of a work item
// whose capsule chain carries a lane snapshot of another workflow
// contract version — keeps that snapshot instead of materializing the
// daemon's current lane over it, so the driver parks the work item
// blocked with kind contract rather than running its old record on a new
// lane; and the claim materializes the lane its pre-claim check judged,
// so a lane removed in between never reaches the capsule.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/protocol"
	"github.com/silgrid/polyforge/internal/common/workflow"
	"github.com/silgrid/polyforge/internal/polyforge/driver"
)

func TestW429ReclaimKeepsAnOldContractSnapshotBlocked(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.WorkflowsDir = pristineBundle(t)
	const wi = "wi_w429old"
	// the work item's capsule chain carries the lane snapshot an earlier
	// claim materialized under a contract 4 daemon (a capsule a drain
	// left in flight): a workflow.md whose frontmatter declares contract 4
	remote := filepath.Join(r.cfg.DataDir, "capsule-remotes", wi+".git")
	work := t.TempDir()
	git := func(dir string, args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if err := os.MkdirAll(remote, 0o755); err != nil {
		t.Fatal(err)
	}
	git(remote, "init", "-q", "--bare")
	git(remote, "symbolic-ref", "HEAD", "refs/heads/main") // aihub's EnsureBare
	git(work, "init", "-q", "-b", "main")
	raw, err := os.ReadFile(filepath.Join(r.cfg.WorkflowsDir, "demo-bundle", workflow.WorkflowFile))
	if err != nil {
		t.Fatal(err)
	}
	front := strings.Replace(string(raw), fmt.Sprintf("contract: %d\n", workflow.ContractVersion), "contract: 4\n", 1)
	if front == string(raw) {
		t.Fatal("the lane declares no contract to rewrite")
	}
	old := "---\n" + front + "---\n\n# demo-bundle\n"
	step, err := os.ReadFile(filepath.Join(r.cfg.WorkflowsDir, "demo-bundle", "steps", "step.md"))
	if err != nil {
		t.Fatal(err)
	}
	for rel, body := range map[string]string{"workflows/demo-bundle/" + workflow.Contract4File: old, "workflows/demo-bundle/steps/step.md": string(step)} {
		p := filepath.Join(work, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	git(work, "add", "-A")
	git(work, "commit", "-q", "-m", "an old-contract lane snapshot")
	git(work, "push", "-q", remote, "HEAD:refs/heads/main")
	// the owner's resume re-claims it on a daemon whose lane is contract 1
	if err := r.Start(context.Background(), wi, StartOpts{}); err != nil {
		t.Fatalf("re-claim: %v", err)
	}
	t.Cleanup(func() { _ = r.Stop(context.Background(), wi, protocol.FinishWrapped) })
	capDir := testCapsuleDir(r, wi)
	got, err := os.ReadFile(filepath.Join(capDir, capsule.DirWorkflows, "demo-bundle", workflow.Contract4File))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(capDir, capsule.DirWorkflows, "demo-bundle", workflow.WorkflowFile)); !os.IsNotExist(err) {
		t.Fatalf("the re-claim laid the daemon's workflow.yaml over the kept snapshot: %v", err)
	}
	if string(got) != old {
		t.Fatalf("the re-claim replaced the chain's old-contract snapshot with the daemon's lane:\n%q\nwant %q", got, old)
	}
	if m := readMarker(t, capDir); m.Contract != 4 || m.BundleName != "demo-bundle" || len(m.Targets) == 0 {
		t.Fatalf("the marker must vouch for the kept snapshot at contract 4: %+v", m)
	}
	// the driver the re-claim starts parks it blocked, kind contract
	h, err := driver.Start(context.Background(), driver.StartConfig{WI: wi, CapsuleDir: capDir, BundleName: "demo-bundle", SessionHandle: "fp", SpawnN: 1, Spawner: &scriptedSpawner{}})
	if err != nil {
		t.Fatalf("driver start: %v", err)
	}
	<-h.Exit()
	var ex capsule.Execution
	if raw, err := os.ReadFile(filepath.Join(capDir, capsule.ExecutionJSON)); err != nil || json.Unmarshal(raw, &ex) != nil {
		t.Fatalf("execution.json: %v", err)
	}
	if ex.WorkflowStatus != driver.StatusBlocked || !driver.ContractBlocked(ex.BlockedReason) || !strings.Contains(ex.BlockedReason, "targets workflow contract 4") {
		t.Fatalf("the re-claimed old snapshot must park blocked, kind contract: %s %q", ex.WorkflowStatus, ex.BlockedReason)
	}
}

// A lane removed after the pre-claim check judged it never reaches the
// capsule: the claim materializes the bytes the check staged, so the work
// item is not blocked by the removal.
func TestW429ClaimMaterializesTheCheckedLane(t *testing.T) {
	setGitIdentity(t)
	hub := newFakeHub()
	r, _, _ := newTestRuntime(t, hub, &scriptedSpawner{})
	r.cfg.WorkflowsDir = pristineBundle(t)
	stage, err := r.stageCheckedLane("wi_w429stage", "proj", "demo-bundle")
	if err != nil || stage == nil {
		t.Fatalf("stage: %v", err)
	}
	if err := os.RemoveAll(filepath.Join(r.cfg.WorkflowsDir, "demo-bundle")); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(fmt.Sprint(r.CheckBundle("wi_x", "proj", "demo-bundle")), workflow.CodeLaneMissing) {
		t.Fatal("the lane is gone from the daemon's tree")
	}
	j := &job{wi: "wi_w429stage", bundleName: "demo-bundle", capsuleDir: t.TempDir(), lane: stage}
	if err := r.materializeBundle(j); err != nil {
		t.Fatalf("the staged lane must materialize after its removal: %v", err)
	}
	if m := readMarker(t, j.capsuleDir); m.Contract != workflow.ContractVersion || len(m.Targets) == 0 {
		t.Fatalf("marker %+v", m)
	}
	// without a staged lane (a journal replay) the daemon's tree is read:
	// the removal is the claim's refusal
	j2 := &job{wi: "wi_w429replay", bundleName: "demo-bundle", capsuleDir: t.TempDir()}
	if err := r.materializeBundle(j2); err == nil || !strings.Contains(err.Error(), workflow.CodeLaneMissing) {
		t.Fatalf("a replay over a removed lane must refuse naming WFC-123: %v", err)
	}
}
