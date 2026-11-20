package components

// status.go (polyforge#190, 84-inplace-upgrade): the volume's upgrade
// status file and cancel marker — the one record Pi Web reads across
// its own restart (the orchestration writes it, the page replays the
// final result from it after the reconnect) and the one way a person
// or the page asks a waiting upgrade to stand down. Both live in the
// components root, beside current/previous: the volume is what
// survives every restart in the workstation.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/silgrid/polyforge/internal/common/atomicfile"
)

// The root's upgrade-transaction file names.
const (
	// UpgradeStatusFilename is the volume's upgrade status record:
	// what operation ran, which phase it reached, from and to which
	// version, and — on a refusal — the reason. Crash-atomically
	// written by the orchestration at every phase change.
	UpgradeStatusFilename = "upgrade-status.json"

	// UpgradeCancelFilename is the cancel marker: present means a
	// waiting upgrade (or rollback) must stand down — restart the
	// daemon (clearing the drain) and change nothing else. The marker
	// is the whole message; it carries no body.
	UpgradeCancelFilename = "upgrade-cancel"
)

// The operations and states an UpgradeStatus names.
const (
	OperationUpgrade      = "upgrade"
	OperationRollback     = "rollback"
	UpgradeStateRunning   = "running"
	UpgradeStateWaiting   = "waiting-drain"
	UpgradeStateDone      = "done"
	UpgradeStateFailed    = "failed"
	UpgradeStateCancelled = "cancelled"
)

// upgradeStatusSchemaVersion is the status record's shape this package
// reads and writes.
const upgradeStatusSchemaVersion = 1

// UpgradeStatus is one line of the upgrade's history: the current (or
// last) transaction as the volume records it.
type UpgradeStatus struct {
	SchemaVersion int    `json:"schemaVersion"`
	Operation     string `json:"operation"` // OperationUpgrade or OperationRollback
	State         string `json:"state"`     // one of the UpgradeState* names
	Phase         string `json:"phase"`     // the fine-grained step the run reached
	From          string `json:"from"`      // the version current pointed at when it started
	To            string `json:"to"`        // the version it aims current at ("" until known)
	Error         string `json:"error,omitempty"`
	At            string `json:"at"` // RFC3339, the write's own moment
}

// ReadUpgradeStatus answers the root's recorded status, or nil with no
// error when none is recorded.
func ReadUpgradeStatus(root string) (*UpgradeStatus, error) {
	raw, err := os.ReadFile(filepath.Join(root, UpgradeStatusFilename))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var s UpgradeStatus
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, fmt.Errorf("%s: %w", filepath.Join(root, UpgradeStatusFilename), err)
	}
	return &s, nil
}

// WriteUpgradeStatus crash-atomically records s, filling the schema
// version and the write's own timestamp.
func WriteUpgradeStatus(root string, s *UpgradeStatus) error {
	s.SchemaVersion = upgradeStatusSchemaVersion
	s.At = time.Now().UTC().Format(time.RFC3339)
	body, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return atomicfile.Write(filepath.Join(root, UpgradeStatusFilename), body, 0o644)
}

// RequestUpgradeCancel lays the cancel marker (idempotent).
func RequestUpgradeCancel(root string) error {
	return atomicfile.Write(filepath.Join(root, UpgradeCancelFilename), []byte{}, 0o644)
}

// UpgradeCancelRequested answers whether the marker is laid.
func UpgradeCancelRequested(root string) bool {
	_, err := os.Stat(filepath.Join(root, UpgradeCancelFilename))
	return err == nil
}

// ClearUpgradeCancel removes the marker (the run consumed it).
func ClearUpgradeCancel(root string) error {
	if err := os.Remove(filepath.Join(root, UpgradeCancelFilename)); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
