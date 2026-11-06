package driver

// contractblock.go: a capsule whose lane snapshot targets a workflow
// contract version this daemon does not implement (docs/reference/
// workflow-contract.md, "Versions and the drain"). Each contract version
// is deployed by draining: in-flight work items finish on the old daemon
// first, and one that did not is left with a snapshot the new daemon's
// loader refuses (WFC-100/101). Its start parks the workflow blocked with
// kind `contract` — one write naming both versions and the upgrade
// section — and runs nothing. A resume parks it again: its re-claim keeps
// a snapshot of another contract version rather than materializing the
// daemon's lane over it (runtime keptContractSnapshot), so the snapshot
// cannot change — the work item is cancelled and re-filed.

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
	"github.com/silgrid/polyforge/internal/common/capsule"
	"github.com/silgrid/polyforge/internal/common/workflow"
)

// BlockerKindContract is the blocked kind of a capsule whose lane snapshot
// targets another workflow contract version.
const BlockerKindContract = "contract"

// contractReasonPrefix starts the blocked reason of a contract block
// (ContractBlocked recognizes it).
const contractReasonPrefix = "contract: "

// ContractRequiredAction is the contract block's required action.
const ContractRequiredAction = "cancel the work item (`pf cancel <wi>`) and file it again on an installed lane: its lane snapshot cannot run on this daemon, and resuming parks it again"

// ContractBlocked reports whether a blocked reason is a contract block.
func ContractBlocked(reason string) bool { return strings.HasPrefix(reason, contractReasonPrefix) }

// snapshotContractMismatch answers the contract block's reason when the
// lane snapshot at bundleDir targets a contract version other than
// workflow.ContractVersion ("" when it does not).
func snapshotContractMismatch(bundleDir string) string {
	lane, errs := workflow.Load(bundleDir)
	for _, e := range errs {
		if e.Code != workflow.CodeContractMissing && e.Code != workflow.CodeContractVersion {
			continue
		}
		got := 0
		if lane != nil {
			got = lane.Contract
		}
		return fmt.Sprintf("%sthe capsule's lane snapshot targets workflow contract %d, this daemon implements contract %d (%s); "+
			"it cannot run here — see docs/upgrade.md, section %q",
			contractReasonPrefix, got, workflow.ContractVersion, e.Code, workflow.UpgradeSection)
	}
	return ""
}

// blockOnContract parks the capsule's workflow blocked with reason, in one
// write of its execution record (no driver runs it: the start is the only
// writer). A capsule without a record gets one holding only the block.
func blockOnContract(execPath, wi, reason string) error {
	ex := capsule.Execution{CapsuleID: capsuleIDFor(wi), WorkItemID: wi, Steps: map[string]*capsule.Step{}}
	if raw, err := os.ReadFile(execPath); err == nil {
		if err := json.Unmarshal(raw, &ex); err != nil {
			return fmt.Errorf("driver: execution.json unparsable: %w", err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	ex.WorkflowStatus = StatusBlocked
	ex.BlockedReason = reason
	ex.Version++
	ex.UpdatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	blob, err := json.MarshalIndent(ex, "", "  ")
	if err != nil {
		return err
	}
	return atomicfile.Write(execPath, blob, 0o644)
}
