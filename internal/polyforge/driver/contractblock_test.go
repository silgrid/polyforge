package driver

// contractblock_test.go (AC7): a capsule whose lane snapshot
// targets another workflow contract version starts nothing — its workflow
// parks blocked with kind contract, the reason naming both versions and
// the docs/upgrade.md section — and a resume parks it again.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// oldSnapshotCapsule lays out a capsule whose micro snapshot is a lane of
// contract version v in contract 4's shape — a workflow.md whose
// frontmatter declares v, no workflow.yaml (a capsule a drain left in
// flight) — parked running on fix; v == ContractVersion keeps the
// shipped workflow.yaml (the same park on a snapshot of this contract).
func oldSnapshotCapsule(t *testing.T, v int) string {
	t.Helper()
	capDir := capsuleWithShippedBundle(t, "micro")
	if v != workflow.ContractVersion {
		p := filepath.Join(capDir, capsule.DirWorkflows, "micro", workflow.WorkflowFile)
		raw, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		cur := fmt.Sprintf("contract: %d\n", workflow.ContractVersion)
		front := strings.Replace(string(raw), cur, fmt.Sprintf("contract: %d\n", v), 1)
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
		writeCapsuleFile(t, capDir, "workflows/micro/"+workflow.Contract4File, "---\n"+front+"---\n\n# micro\n")
	}
	ex := capsule.Execution{CapsuleID: "cap_wi_old", WorkItemID: "wi_old", CurrentStep: "fix", WorkflowStatus: StatusRunning, Version: 3,
		Steps: map[string]*capsule.Step{"fix": {Status: capsule.StepInProgress}, "review": {Status: capsule.StepPending}, "deliver": {Status: capsule.StepPending}}}
	blob, _ := json.Marshal(ex)
	writeCapsuleFile(t, capDir, capsule.ExecutionJSON, string(blob))
	return capDir
}

// internalBuildContract is the stale contract a drain can leave in
// flight: the previous contract, 4, whose lane file is a workflow.md.
const internalBuildContract = 4

func TestContractMismatchBlocksTheStart(t *testing.T) {
	if internalBuildContract == workflow.ContractVersion {
		t.Fatalf("contract %d is this daemon's: pick another stale version", workflow.ContractVersion)
	}
	capDir := oldSnapshotCapsule(t, internalBuildContract)
	sp := &startSpawner{}
	for round := 1; round <= 2; round++ { // the claim, then the owner's resume
		h, err := Start(context.Background(), StartConfig{WI: "wi_old", CapsuleDir: capDir, BundleName: "micro", SessionHandle: "fp", SpawnN: 1, Spawner: sp})
		if err != nil {
			t.Fatalf("round %d: Start: %v", round, err)
		}
		select {
		case <-h.Exit():
		default:
			t.Fatalf("round %d: a contract-blocked start must have exited", round)
		}
		if _, err := h.OwnerMessage(context.Background(), "a", "alice"); !errors.Is(err, errNoDriver) {
			t.Fatalf("round %d: an owner message reached no driver: %v", round, err)
		}
		raw, err := os.ReadFile(filepath.Join(capDir, capsule.ExecutionJSON))
		if err != nil {
			t.Fatal(err)
		}
		var ex capsule.Execution
		if err := json.Unmarshal(raw, &ex); err != nil {
			t.Fatal(err)
		}
		if ex.WorkflowStatus != StatusBlocked || !ContractBlocked(ex.BlockedReason) || ex.Version != int64(3+round) || ex.CurrentStep != "fix" {
			t.Fatalf("round %d: execution = %s %q v%d", round, ex.WorkflowStatus, ex.BlockedReason, ex.Version)
		}
		for _, want := range []string{
			fmt.Sprintf("targets workflow contract %d", internalBuildContract),
			fmt.Sprintf("implements contract %d", workflow.ContractVersion),
			workflow.CodeContractVersion, "docs/upgrade.md", workflow.UpgradeSection,
		} {
			if !strings.Contains(ex.BlockedReason, want) {
				t.Fatalf("round %d: reason %q does not name %q", round, ex.BlockedReason, want)
			}
		}
	}
	sp.mu.Lock()
	n := len(sp.params)
	sp.mu.Unlock()
	if n != 0 {
		t.Fatalf("a contract-blocked capsule dispatched %d step(s)", n)
	}
	// a snapshot with no contract at all (pre-contract) blocks the same way
	if r := snapshotContractMismatch(filepath.Join(oldSnapshotCapsule(t, workflow.ContractVersion), capsule.DirWorkflows, "micro")); r != "" {
		t.Fatalf("the current version must not block: %q", r)
	}
	pre := capsuleWithShippedBundle(t, "micro")
	p := filepath.Join(pre, capsule.DirWorkflows, "micro", workflow.WorkflowFile)
	raw, _ := os.ReadFile(p)
	_ = os.WriteFile(p, []byte(strings.Replace(string(raw), fmt.Sprintf("contract: %d\n", workflow.ContractVersion), "", 1)), 0o644)
	if r := snapshotContractMismatch(filepath.Join(pre, capsule.DirWorkflows, "micro")); !strings.Contains(r, "targets workflow contract 0") || !strings.Contains(r, workflow.CodeContractMissing) {
		t.Fatalf("a pre-contract snapshot must block naming contract 0 and %s: %q", workflow.CodeContractMissing, r)
	}
}
